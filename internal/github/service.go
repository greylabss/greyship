package github

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/build"
	"greyship.tech/internal/bus"
	"greyship.tech/internal/deploy"
	"greyship.tech/internal/detect"
	"greyship.tech/internal/dotenv"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
)

// GitHub is the part of App the service uses, so tests can fake GitHub.
type GitHub interface {
	InstallURL(state string) string
	ExchangeCode(ctx context.Context, code string) (string, error)
	UserInstallations(ctx context.Context, userToken string) ([]Installation, error)
	Repos(ctx context.Context, installation int64) ([]Repo, error)
	BranchHead(ctx context.Context, installation int64, repo, branch string) (string, error)
	PublicRefs(ctx context.Context, repo string) (string, map[string]string, error)
	Tarball(ctx context.Context, installation int64, repo, sha string, limit int64) ([]byte, error)
	PublicTarball(ctx context.Context, repo, sha string, limit int64) ([]byte, error)
}

// Service connects teams' GitHub installations and repos to projects, and turns pushes into deployments.
type Service struct {
	Pool     *pgxpool.Pool
	GitHub   GitHub
	Projects *project.Service
	Signer   auth.Signer
	Logger   *slog.Logger
}

var (
	ErrNotInstalled    = errors.New("the Greyship GitHub app isn't installed for this team")
	ErrNoAccess        = errors.New("this GitHub installation isn't one you can access")
	ErrUnknownRepo     = errors.New("that repository isn't shared with the Greyship GitHub app")
	ErrBadState        = errors.New("this GitHub link has expired or wasn't started here; try installing again")
	ErrUnknownBranch   = errors.New("the repository has no such branch")
	ErrAlreadyDeployed = errors.New("the latest commit is already deployed or deploying")
	branchName         = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
)

// FetchJob is the job kind that downloads a pushed commit for its deployment.
const FetchJob = "github.fetch"

type installState struct {
	Kind string `json:"kind"`
	Team string `json:"team"`
	User string `json:"user"`
}

// InstallURL starts installing the app for a team. The signed state ties GitHub's answer to this person and team.
func (s *Service) InstallURL(ctx context.Context, c project.Caller, team string) (string, error) {
	t, err := s.Projects.Member(ctx, c, team, true)
	if err != nil {
		return "", err
	}
	state, err := s.Signer.Sign(installState{Kind: "github-install", Team: t.ID, User: c.UserID}, 30*time.Minute)
	if err != nil {
		return "", err
	}
	return s.GitHub.InstallURL(state), nil
}

// Callback records an installation GitHub sends back, but only if the person proves with their own GitHub
// login that they can access it: an installation id alone is just a number anyone could type.
func (s *Service) Callback(ctx context.Context, c project.Caller, state, code string, installation int64) (string, error) {
	var st installState
	if err := s.Signer.Verify(state, &st); err != nil || st.Kind != "github-install" || st.User != c.UserID {
		return "", ErrBadState
	}
	q := store.New(s.Pool)
	team, err := q.GetMembershipByTeamID(ctx, store.GetMembershipByTeamIDParams{UserID: c.UserID, ID: st.Team})
	if err != nil {
		return "", ErrBadState
	}
	if team.Role == "viewer" {
		return "", project.ErrReadOnly
	}
	userToken, err := s.GitHub.ExchangeCode(ctx, code)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoAccess, err)
	}
	theirs, err := s.GitHub.UserInstallations(ctx, userToken)
	if err != nil {
		return "", fmt.Errorf("list GitHub installations: %w", err)
	}
	for _, in := range theirs {
		if in.ID != installation {
			continue
		}
		if err := q.AddInstallation(ctx, store.AddInstallationParams{ID: in.ID, TeamID: team.ID, AccountLogin: in.Account.Login, CreatedBy: &c.UserID}); err != nil {
			return "", fmt.Errorf("record installation %d: %w", in.ID, err)
		}
		return team.Slug, nil
	}
	return "", ErrNoAccess
}

// TeamRepo is a repository one of the team's installations can read.
type TeamRepo struct {
	Installation int64
	Repo
}

// Repos lists what the team's installations can read.
func (s *Service) Repos(ctx context.Context, c project.Caller, team string) ([]TeamRepo, error) {
	t, err := s.Projects.Member(ctx, c, team, false)
	if err != nil {
		return nil, err
	}
	return s.teamRepos(ctx, t.ID)
}

func (s *Service) teamRepos(ctx context.Context, teamID string) ([]TeamRepo, error) {
	installs, err := store.New(s.Pool).TeamInstallations(ctx, teamID)
	if err != nil {
		return nil, err
	}
	if len(installs) == 0 {
		return nil, ErrNotInstalled
	}
	var out []TeamRepo
	for _, in := range installs {
		repos, err := s.GitHub.Repos(ctx, in.ID)
		if errors.Is(err, ErrNotFound) {
			continue // uninstalled on GitHub since
		}
		if err != nil {
			return nil, err
		}
		for _, r := range repos {
			out = append(out, TeamRepo{Installation: in.ID, Repo: r})
		}
	}
	return out, nil
}

// find is the team's access to one repo; another team's repo, or one not shared with the app, isn't found.
func (s *Service) find(ctx context.Context, teamID, full string) (TeamRepo, error) {
	if !ValidRepo(full) {
		return TeamRepo{}, ErrUnknownRepo
	}
	repos, err := s.teamRepos(ctx, teamID)
	if err != nil {
		return TeamRepo{}, err
	}
	for _, r := range repos {
		if strings.EqualFold(r.FullName, full) {
			return r, nil
		}
	}
	return TeamRepo{}, ErrUnknownRepo
}

// Connect makes pushes to branch of repo deploy the project. Empty branch means the repo's default branch.
func (s *Service) Connect(ctx context.Context, c project.Caller, projectID, full, branch string) (store.ProjectRepo, error) {
	p, err := s.Projects.Access(ctx, c, projectID, true)
	if err != nil {
		return store.ProjectRepo{}, err
	}
	return s.connect(ctx, p, full, branch)
}

func (s *Service) connect(ctx context.Context, p project.Project, full, branch string) (store.ProjectRepo, error) {
	repo, err := s.find(ctx, p.TeamID, full)
	if err != nil {
		return store.ProjectRepo{}, err
	}
	if branch == "" {
		branch = repo.DefaultBranch
	}
	if !branchName.MatchString(branch) || strings.Contains(branch, "..") {
		return store.ProjectRepo{}, fmt.Errorf("%w: branch %q", ErrUnknownRepo, branch)
	}
	return store.New(s.Pool).ConnectRepo(ctx, store.ConnectRepoParams{
		ProjectID: p.ID, InstallationID: &repo.Installation, RepoID: repo.ID, FullName: repo.FullName, Branch: branch,
	})
}

// Repo is the project's connected repository, or ErrNotFound.
func (s *Service) Repo(ctx context.Context, c project.Caller, projectID string) (store.ProjectRepo, error) {
	p, err := s.Projects.Access(ctx, c, projectID, false)
	if err != nil {
		return store.ProjectRepo{}, err
	}
	r, err := store.New(s.Pool).ProjectRepo(ctx, p.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ProjectRepo{}, ErrNotFound
	}
	return r, err
}

// Import creates a project from a repo, connects it, and deploys the branch head.
// Import creates a project from a repo and connects it. With deploy it also deploys the branch head; the dashboard
// imports without deploying, to show the preview and env vars first.
func (s *Service) Import(ctx context.Context, c project.Caller, team, full, branch string, deploy bool) (project.Project, string, error) {
	t, err := s.Projects.Member(ctx, c, team, true)
	if err != nil {
		return project.Project{}, "", err
	}
	repo, err := s.find(ctx, t.ID, full)
	if err != nil {
		return project.Project{}, "", err
	}
	_, name, _ := strings.Cut(repo.FullName, "/")
	p, err := s.createProject(ctx, c, team, name)
	if err != nil {
		return project.Project{}, "", err
	}
	conn, err := s.connect(ctx, p, repo.FullName, branch)
	if err != nil {
		return project.Project{}, "", err
	}
	if !deploy {
		return p, "", nil
	}
	sha, err := s.head(ctx, conn)
	if err != nil {
		return project.Project{}, "", err
	}
	id, err := s.deploy(ctx, p.ID, sha, conn.Branch, &c.UserID)
	return p, id, err
}

// ImportPublic creates a project from a public repository link and deploys it, without the app (decisions.md 043).
// Later commits deploy only when someone asks (DeployLatest).
func (s *Service) ImportPublic(ctx context.Context, c project.Caller, team, link, branch string, deploy bool) (project.Project, string, error) {
	if _, err := s.Projects.Member(ctx, c, team, true); err != nil {
		return project.Project{}, "", err
	}
	full, fromLink, err := ParseRepoURL(link)
	if err != nil {
		return project.Project{}, "", err
	}
	if branch == "" {
		branch = fromLink
	}
	def, heads, err := s.GitHub.PublicRefs(ctx, full)
	if err != nil {
		return project.Project{}, "", err
	}
	if branch == "" {
		branch = def
	}
	sha, ok := heads[branch]
	if !ok {
		return project.Project{}, "", fmt.Errorf("%w: %s", ErrUnknownBranch, branch)
	}
	_, name, _ := strings.Cut(full, "/")
	p, err := s.createProject(ctx, c, team, strings.ToLower(name))
	if err != nil {
		return project.Project{}, "", err
	}
	// A public repo has no installation; its GitHub id isn't needed, since pushes never deploy it.
	if _, err := store.New(s.Pool).ConnectRepo(ctx, store.ConnectRepoParams{ProjectID: p.ID, FullName: full, Branch: branch}); err != nil {
		return project.Project{}, "", err
	}
	if !deploy {
		return p, "", nil
	}
	id, err := s.deploy(ctx, p.ID, sha, branch, &c.UserID)
	return p, id, err
}

// DeployLatest deploys the head of the project's connected branch, for app and public repos alike. A commit that is
// live or deploying isn't deployed again; one whose deploy failed is, for example after fixing its env vars.
func (s *Service) DeployLatest(ctx context.Context, c project.Caller, projectID string) (string, error) {
	p, err := s.Projects.Access(ctx, c, projectID, true)
	if err != nil {
		return "", err
	}
	conn, err := store.New(s.Pool).ProjectRepo(ctx, p.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	sha, err := s.head(ctx, conn)
	if err != nil {
		return "", err
	}
	id, err := s.deploy(ctx, p.ID, sha, conn.Branch, &c.UserID)
	if err == nil && id == "" {
		return "", ErrAlreadyDeployed
	}
	return id, err
}

// head is the commit the connected branch points at, read through the app or, for public repos, git's ref list.
func (s *Service) head(ctx context.Context, conn store.ProjectRepo) (string, error) {
	if conn.InstallationID != nil {
		sha, err := s.GitHub.BranchHead(ctx, *conn.InstallationID, conn.FullName, conn.Branch)
		if errors.Is(err, ErrNotFound) {
			return "", fmt.Errorf("%w: %s", ErrUnknownBranch, conn.Branch)
		}
		return sha, err
	}
	_, heads, err := s.GitHub.PublicRefs(ctx, conn.FullName)
	if err != nil {
		return "", err
	}
	sha, ok := heads[conn.Branch]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownBranch, conn.Branch)
	}
	return sha, nil
}

// createProject names the project after the repo, adding -2, -3, … when the team already has one by that name.
func (s *Service) createProject(ctx context.Context, c project.Caller, team, name string) (project.Project, error) {
	var taken *project.TakenError
	for i := 1; i <= 20; i++ {
		try := name
		if i > 1 {
			try = fmt.Sprintf("%s-%d", name, i)
		}
		p, err := s.Projects.Create(ctx, c, team, try)
		if errors.As(err, &taken) {
			continue
		}
		return p, err
	}
	return project.Project{}, fmt.Errorf("every name like %s is taken", name)
}

// PushEvent is the part of GitHub's push payload a deploy needs.
type PushEvent struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Deleted    bool   `json:"deleted"`
	Repository struct {
		ID int64 `json:"id"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

// Push deploys every project connected to the pushed branch. A repeated delivery deploys nothing new.
func (s *Service) Push(ctx context.Context, body []byte) (int, error) {
	var ev PushEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return 0, fmt.Errorf("decode push: %w", err)
	}
	branch, ok := strings.CutPrefix(ev.Ref, "refs/heads/")
	if !ok || ev.Deleted || len(ev.After) != 40 {
		return 0, nil // a tag, a deleted branch: nothing to deploy
	}
	rows, err := store.New(s.Pool).ProjectsForPush(ctx, store.ProjectsForPushParams{RepoID: ev.Repository.ID, Branch: branch})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		if r.InstallationID == nil || *r.InstallationID != ev.Installation.ID {
			continue // only the installation that connected the repo speaks for it
		}
		id, err := s.deploy(ctx, r.ProjectID, ev.After, branch, nil)
		if err != nil {
			return n, err
		}
		if id != "" {
			n++
		}
	}
	return n, nil
}

// deploy creates a GitHub deployment for a commit and asks the builder to fetch it. It returns "" when the commit
// already has one.
func (s *Service) deploy(ctx context.Context, projectID, sha, branch string, by *string) (string, error) {
	id, err := deploy.NewID()
	if err != nil {
		return "", err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	d, err := q.CreateGithubDeployment(ctx, store.CreateGithubDeploymentParams{ID: id, ProjectID: projectID, CommitSha: &sha, Branch: &branch, CreatedBy: by})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("create deployment for %s: %w", sha, err)
	}
	if _, err := bus.Enqueue(ctx, q, FetchJob, map[string]string{"deployment_id": d.ID}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	s.Logger.Info("github deploy", "deployment", d.ID, "project", projectID, "branch", branch, "commit", sha[:min(7, len(sha))])
	return d.ID, nil
}

// Fetcher downloads a pushed commit, repacks it like grey deploy would and hands it to the builder.
type Fetcher struct {
	Pool    *pgxpool.Pool
	App     *App
	Storage interface {
		Put(ctx context.Context, key string, body io.Reader, size int64) error
	}
	Deploy *deploy.Service
	Logger *slog.Logger
}

func (f *Fetcher) Handle(ctx context.Context, job bus.Job) error {
	var payload struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("decode job %d: %w", job.ID, err)
	}
	q := store.New(f.Pool)
	d, err := q.GetDeployment(ctx, payload.DeploymentID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (d.Status != "uploading" || d.Source != "github" || d.SourceKey != nil) {
		return nil // gone or already fetched: a repeated job
	}
	if err != nil {
		return err
	}
	repo, err := q.ProjectRepo(ctx, d.ProjectID)
	if err != nil {
		return f.fail(ctx, q, d.ID, "the project is no longer connected to GitHub")
	}
	var archive []byte
	if repo.InstallationID != nil {
		archive, err = f.App.Tarball(ctx, *repo.InstallationID, repo.FullName, *d.CommitSha, deploy.MaxSourceSize*4)
	} else {
		archive, err = f.App.PublicTarball(ctx, repo.FullName, *d.CommitSha, deploy.MaxSourceSize*4)
	}
	switch {
	case errors.Is(err, ErrNotFound):
		return f.fail(ctx, q, d.ID, "GitHub doesn't have that commit any more, or the app lost access to the repository")
	case err != nil && strings.Contains(err.Error(), "larger than"):
		return f.fail(ctx, q, d.ID, err.Error())
	case err != nil:
		return f.retry(ctx, q, d.ID, job, err)
	}
	var src bytes.Buffer
	if _, err := Repack(archive, &src); err != nil {
		return f.fail(ctx, q, d.ID, err.Error())
	}
	if int64(src.Len()) > deploy.MaxSourceSize {
		return f.fail(ctx, q, d.ID, fmt.Sprintf("the source is larger than %d MB after leaving out ignored files", deploy.MaxSourceSize>>20))
	}
	sum := sha256.Sum256(src.Bytes())
	key := "sources/" + d.ProjectID + "/" + d.ID + ".tar.gz"
	if err := f.Storage.Put(ctx, key, bytes.NewReader(src.Bytes()), int64(src.Len())); err != nil {
		return f.retry(ctx, q, d.ID, job, err)
	}
	size, digest := int64(src.Len()), hex.EncodeToString(sum[:])
	if _, err := q.SetFetchedSource(ctx, store.SetFetchedSourceParams{ID: d.ID, SourceKey: &key, SourceSize: &size, SourceSha256: &digest}); err != nil {
		return err
	}
	p, err := q.GetProject(ctx, d.ProjectID)
	if err != nil {
		return err
	}
	return f.Deploy.QueueBuild(ctx, d.ID, p.TeamID)
}

func (f *Fetcher) fail(ctx context.Context, q *store.Queries, id, reason string) error {
	f.Logger.Info("github fetch failed", "deployment", id, "reason", reason)
	if err := q.FailFetch(ctx, store.FailFetchParams{ID: id, Error: &reason}); err != nil {
		return err
	}
	return bus.EnqueueNotify(ctx, q, bus.DeployFailed, id, "")
}

// retry gives an outage on our side or GitHub's another go; the last attempt fails the deployment.
func (f *Fetcher) retry(ctx context.Context, q *store.Queries, id string, job bus.Job, err error) error {
	if job.Attempts >= job.MaxAttempts {
		if ferr := f.fail(ctx, q, id, "couldn't download the commit from GitHub"); ferr != nil {
			return ferr
		}
	}
	return fmt.Errorf("fetch %s: %w", id, err)
}

// Statuses keeps each GitHub deployment's commit status in step with it: pending, then success or failure, with
// a link to the deployment. It is a sweep, so a failed post is simply tried again.
type Statuses struct {
	Pool      *pgxpool.Pool
	App       *App
	Dashboard string
	Logger    *slog.Logger
}

func (s *Statuses) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := s.report(ctx); err != nil && ctx.Err() == nil {
			s.Logger.Warn("report GitHub statuses", "err", err)
		}
	}
}

func (s *Statuses) report(ctx context.Context) error {
	q := store.New(s.Pool)
	due, err := q.GithubStatusesDue(ctx)
	if err != nil {
		return err
	}
	for _, d := range due {
		target := fmt.Sprintf("%s/%s/%s/deployments/%s", strings.TrimRight(s.Dashboard, "/"), d.TeamSlug, d.ProjectSlug, d.ID)
		if d.InstallationID == nil {
			continue
		}
		if err := s.App.SetStatus(ctx, *d.InstallationID, d.FullName, *d.CommitSha, d.State, target, describe(d.State, d.Error)); err != nil {
			s.Logger.Warn("set GitHub status", "deployment", d.ID, "err", err)
			continue
		}
		if err := q.MarkGithubReported(ctx, store.MarkGithubReportedParams{ID: d.ID, GithubReported: &d.State}); err != nil {
			return err
		}
	}
	return nil
}

func describe(state string, reason *string) string {
	switch state {
	case "success":
		return "Live on Greyship"
	case "failure":
		if reason != nil && *reason != "" {
			msg := *reason
			if len(msg) > 130 {
				msg = msg[:130] + "…"
			}
			return msg
		}
		return "Deploy failed"
	default:
		return "Deploying on Greyship"
	}
}

// Preview is what a deploy of the connected branch's head would run, worked out before deploying.
type Preview struct {
	Branch     string
	Commit     string
	Detected   detect.Result
	EnvExample []string // key names from .env.example or .env.sample, never values
}

// Preview downloads the branch head and runs the same detection the build will, so the dashboard can show the
// services and suggest env vars before the first deploy. Detection problems come back as *detect.ConfigError.
func (s *Service) Preview(ctx context.Context, c project.Caller, projectID string) (Preview, error) {
	p, err := s.Projects.Access(ctx, c, projectID, false)
	if err != nil {
		return Preview{}, err
	}
	conn, err := store.New(s.Pool).ProjectRepo(ctx, p.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Preview{}, ErrNotFound
	}
	if err != nil {
		return Preview{}, err
	}
	sha, err := s.head(ctx, conn)
	if err != nil {
		return Preview{}, err
	}
	var archive []byte
	if conn.InstallationID != nil {
		archive, err = s.GitHub.Tarball(ctx, *conn.InstallationID, conn.FullName, sha, deploy.MaxSourceSize*4)
	} else {
		archive, err = s.GitHub.PublicTarball(ctx, conn.FullName, sha, deploy.MaxSourceSize*4)
	}
	if err != nil {
		return Preview{}, err
	}
	out := Preview{Branch: conn.Branch, Commit: sha, EnvExample: exampleKeys(archive)}
	var src bytes.Buffer
	if _, err := Repack(archive, &src); err != nil {
		return Preview{}, err
	}
	fsys, err := build.SourceFS(src.Bytes())
	if err != nil {
		return Preview{}, err
	}
	out.Detected, err = detect.Detect(fsys, p.Slug)
	return out, err
}

// exampleKeys lists the variable names in the repo's .env.example (or .env.sample), so the dashboard can suggest
// them. Values are ignored, and .env itself is never read.
func exampleKeys(archive []byte) []string {
	for _, name := range []string{".env.example", ".env.sample"} {
		body, ok := archiveFile(archive, name)
		if !ok {
			continue
		}
		values, err := dotenv.Parse(string(body))
		if err != nil {
			return nil
		}
		keys := make([]string, 0, len(values))
		for k := range values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	}
	return nil
}
