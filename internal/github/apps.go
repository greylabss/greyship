package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/secrets"
	"greyship.tech/internal/store"
)

const (
	refreshEvery = 30 * time.Second

	// Labels bind each sealed value to its purpose; they are not credentials.
	labelClientSecret  = "github:client_secret" //nolint:gosec
	labelPrivateKey    = "github:private_key"
	labelWebhookSecret = "github:webhook_secret" //nolint:gosec
)

// ErrOff means the team has no GitHub App yet: a member creates one in the dashboard (decisions.md 055).
var ErrOff = errors.New("this team has no GitHub App yet")

// Credentials is what GitHub returns for a new app.
type Credentials struct {
	AppID         int64
	Slug          string
	Name          string
	HTMLURL       string
	ClientID      string
	ClientSecret  string
	WebhookSecret string
	PrivateKeyPEM string
}

// Status is what the dashboard shows about a team's app. It never carries a secret.
type Status struct {
	Configured bool
	Slug       string
	Name       string
	HTMLURL    string
}

type entry struct {
	app     *App
	webhook string
	status  Status
	at      time.Time
}

// Apps holds each team's own GitHub App, read from the database and kept for a few seconds, so a new app works at
// once and every process (the API, the builder) sees it without a restart.
type Apps struct {
	Pool   *pgxpool.Pool
	Keys   *secrets.Keyring
	Now    func() time.Time
	Logger *slog.Logger
	// API, Web and Codeload point at github.com; tests point them at a fake.
	API, Web, Codeload string
	HTTP               *http.Client

	mu    sync.Mutex
	cache map[string]entry
}

func (a *Apps) entry(ctx context.Context, teamID string, fresh bool) (entry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e, ok := a.cache[teamID]; ok && !fresh && a.Now().Sub(e.at) < refreshEvery {
		return e, nil
	}
	e, err := a.load(ctx, teamID)
	if err != nil {
		if old, ok := a.cache[teamID]; ok {
			a.Logger.Warn("load GitHub App", "team", teamID, "err", err)
			return old, nil
		}
		return e, err
	}
	if a.cache == nil {
		a.cache = map[string]entry{}
	}
	e.at = a.Now()
	a.cache[teamID] = e
	return e, nil
}

func (a *Apps) forget(teamID string) {
	a.mu.Lock()
	delete(a.cache, teamID)
	a.mu.Unlock()
}

func (a *Apps) load(ctx context.Context, teamID string) (entry, error) {
	row, err := store.New(a.Pool).GetTeamGitHubApp(ctx, teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		return entry{}, nil
	}
	if err != nil {
		return entry{}, fmt.Errorf("read GitHub App: %w", err)
	}
	st := Status{Configured: true, Slug: row.Slug, Name: row.Name, HTMLURL: row.HtmlUrl}
	secret, err := a.Keys.Open(labelClientSecret, row.ClientSecretEnc)
	if err != nil {
		return entry{status: st}, fmt.Errorf("open the GitHub App client secret: %w", err)
	}
	pem, err := a.Keys.Open(labelPrivateKey, row.PrivateKeyEnc)
	if err != nil {
		return entry{status: st}, fmt.Errorf("open the GitHub App key: %w", err)
	}
	hook, err := a.Keys.Open(labelWebhookSecret, row.WebhookSecretEnc)
	if err != nil {
		return entry{status: st}, fmt.Errorf("open the GitHub App webhook secret: %w", err)
	}
	app, err := NewApp(row.AppID, row.Slug, row.ClientID, string(secret), pem)
	if err != nil {
		return entry{status: st}, err
	}
	app.API, app.Web, app.Codeload = orDefault(a.API, app.API), orDefault(a.Web, app.Web), orDefault(a.Codeload, app.Codeload)
	return entry{app: app, webhook: string(hook), status: st}, nil
}

// ForTeam is the team's app, or ErrOff when it has none.
func (a *Apps) ForTeam(ctx context.Context, teamID string) (*App, error) {
	e, err := a.entry(ctx, teamID, false)
	if err != nil {
		return nil, err
	}
	if e.app == nil {
		return nil, ErrOff
	}
	return e.app, nil
}

// WebhookSecret checks GitHub's deliveries for the team's app; ErrOff when it has none.
func (a *Apps) WebhookSecret(ctx context.Context, teamID string) (string, error) {
	e, err := a.entry(ctx, teamID, false)
	if err != nil {
		return "", err
	}
	if e.app == nil {
		return "", ErrOff
	}
	return e.webhook, nil
}

// Status reports whether the team has an app, without secrets. An app whose secrets can't be opened still shows as
// configured, so the person sees the problem instead of a blank page.
func (a *Apps) Status(ctx context.Context, teamID string) (Status, error) {
	e, err := a.entry(ctx, teamID, true)
	if err != nil && e.status.Configured {
		return e.status, nil
	}
	return e.status, err
}

// Save stores a new app's credentials for the team, sealed, replacing any earlier one, and applies them at once.
func (a *Apps) Save(ctx context.Context, teamID, userID string, c Credentials) error {
	if _, err := ParseKey([]byte(c.PrivateKeyPEM)); err != nil {
		return fmt.Errorf("GitHub sent a key Greyship can't read: %w", err)
	}
	secret, err := a.Keys.Seal(labelClientSecret, []byte(c.ClientSecret))
	if err != nil {
		return fmt.Errorf("seal client secret: %w", err)
	}
	key, err := a.Keys.Seal(labelPrivateKey, []byte(c.PrivateKeyPEM))
	if err != nil {
		return fmt.Errorf("seal key: %w", err)
	}
	hook, err := a.Keys.Seal(labelWebhookSecret, []byte(c.WebhookSecret))
	if err != nil {
		return fmt.Errorf("seal webhook secret: %w", err)
	}
	q := store.New(a.Pool)
	// A new app is a different GitHub app: installations of an earlier one don't belong to it.
	if err := q.DeleteTeamInstallations(ctx, teamID); err != nil {
		return fmt.Errorf("clear installations: %w", err)
	}
	err = q.SaveTeamGitHubApp(ctx, store.SaveTeamGitHubAppParams{
		TeamID: teamID, AppID: c.AppID, Slug: c.Slug, Name: c.Name, HtmlUrl: c.HTMLURL, ClientID: c.ClientID,
		ClientSecretEnc: secret, PrivateKeyEnc: key, WebhookSecretEnc: hook, CreatedBy: &userID,
	})
	if err != nil {
		return fmt.Errorf("save GitHub App: %w", err)
	}
	a.forget(teamID)
	return nil
}

// Delete removes the team's app and its installations, with the repositories connected through them.
func (a *Apps) Delete(ctx context.Context, teamID string) error {
	q := store.New(a.Pool)
	if err := q.DeleteTeamInstallations(ctx, teamID); err != nil {
		return fmt.Errorf("clear installations: %w", err)
	}
	if err := q.DeleteTeamGitHubApp(ctx, teamID); err != nil {
		return fmt.Errorf("delete GitHub App: %w", err)
	}
	a.forget(teamID)
	return nil
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// NewAppURL is where the person's browser is sent to create the app.
func (a *Apps) NewAppURL(org, state string) (string, error) {
	return NewAppURL(orDefault(a.Web, "https://github.com"), org, state)
}

// Convert turns the code GitHub sends back after the person confirmed into the app's credentials.
func (a *Apps) Convert(ctx context.Context, code string) (Credentials, error) {
	return Convert(ctx, a.client(), orDefault(a.API, "https://api.github.com"), code)
}

func (a *Apps) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// public is an app with no credentials, for public repositories: reading them needs none.
func (a *Apps) public() *App {
	return &App{
		API: orDefault(a.API, "https://api.github.com"), Web: orDefault(a.Web, "https://github.com"),
		Codeload: orDefault(a.Codeload, "https://codeload.github.com"), HTTP: a.client(), Now: a.Now,
	}
}

// Dynamic is a GitHub that finds the right team's app for each call, so code built once keeps working as teams
// create their apps. Calls that carry an installation id use the app of the team that owns the installation.
type Dynamic struct{ Apps *Apps }

func (d Dynamic) forInstallation(ctx context.Context, installation int64) (*App, error) {
	in, err := store.New(d.Apps.Pool).GetInstallation(ctx, installation)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOff
	}
	if err != nil {
		return nil, fmt.Errorf("find installation %d: %w", installation, err)
	}
	return d.Apps.ForTeam(ctx, in.TeamID)
}

func (d Dynamic) InstallURL(ctx context.Context, team, state string) (string, error) {
	app, err := d.Apps.ForTeam(ctx, team)
	if err != nil {
		return "", err
	}
	return app.InstallURL(ctx, team, state)
}

func (d Dynamic) ExchangeCode(ctx context.Context, team, code string) (string, error) {
	app, err := d.Apps.ForTeam(ctx, team)
	if err != nil {
		return "", err
	}
	return app.ExchangeCode(ctx, team, code)
}

func (d Dynamic) UserInstallations(ctx context.Context, team, userToken string) ([]Installation, error) {
	app, err := d.Apps.ForTeam(ctx, team)
	if err != nil {
		return nil, err
	}
	return app.UserInstallations(ctx, team, userToken)
}

func (d Dynamic) Repos(ctx context.Context, installation int64) ([]Repo, error) {
	app, err := d.forInstallation(ctx, installation)
	if err != nil {
		return nil, err
	}
	return app.Repos(ctx, installation)
}

func (d Dynamic) BranchHead(ctx context.Context, installation int64, repo, branch string) (string, error) {
	app, err := d.forInstallation(ctx, installation)
	if err != nil {
		return "", err
	}
	return app.BranchHead(ctx, installation, repo, branch)
}

func (d Dynamic) Tarball(ctx context.Context, installation int64, repo, sha string, limit int64) ([]byte, error) {
	app, err := d.forInstallation(ctx, installation)
	if err != nil {
		return nil, err
	}
	return app.Tarball(ctx, installation, repo, sha, limit)
}

func (d Dynamic) SetStatus(ctx context.Context, installation int64, repo, sha, state, target, description string) error {
	app, err := d.forInstallation(ctx, installation)
	if err != nil {
		return err
	}
	return app.SetStatus(ctx, installation, repo, sha, state, target, description)
}

func (d Dynamic) PublicRefs(ctx context.Context, repo string) (string, map[string]string, error) {
	return d.Apps.public().PublicRefs(ctx, repo)
}

func (d Dynamic) PublicTarball(ctx context.Context, repo, sha string, limit int64) ([]byte, error) {
	return d.Apps.public().PublicTarball(ctx, repo, sha, limit)
}
