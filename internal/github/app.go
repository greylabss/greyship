package github

import (
	"context"
	"errors"
	"strings"
	"time"

	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
)

// A team creates its own GitHub App with GitHub's app manifest flow (decisions.md 055): the service hands out the
// manifest, and takes the credentials GitHub returns once the person confirmed.

const manifestKind = "github-manifest"

type manifestState struct {
	Kind string `json:"kind"`
	Team string `json:"team"`
	User string `json:"user"`
}

// AppInfo is a team's app as the dashboard shows it.
type AppInfo struct {
	Status
	TeamID      string
	WebhookURL  string
	DefaultName string
}

// AppStart is what the browser needs to create the app: the GitHub page and the manifest to post to it.
type AppStart struct {
	Action   string
	Manifest map[string]any
}

// AppStatus shows whether the team has an app. Any member may look.
func (s *Service) AppStatus(ctx context.Context, c project.Caller, team string) (AppInfo, error) {
	t, err := s.Projects.Member(ctx, c, team, false)
	if err != nil {
		return AppInfo{}, err
	}
	st, err := s.Apps.Status(ctx, t.ID)
	if err != nil {
		return AppInfo{}, err
	}
	return AppInfo{Status: st, TeamID: t.ID, WebhookURL: WebhookURL(s.DashboardURL, t.ID), DefaultName: defaultAppName(t.Slug)}, nil
}

// defaultAppName is "Greyship <team>": GitHub app names are unique across GitHub, at most 34 characters.
func defaultAppName(teamSlug string) string {
	name := "Greyship " + teamSlug
	if len(name) > 34 {
		name = name[:34]
	}
	return strings.TrimRight(name, " .-_")
}

// StartApp begins creating the team's app. The signed state ties GitHub's answer to this person and team.
func (s *Service) StartApp(ctx context.Context, c project.Caller, team, org, name string) (AppStart, error) {
	t, err := s.Projects.Member(ctx, c, team, true)
	if err != nil {
		return AppStart{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = defaultAppName(t.Slug)
	}
	name, err = CleanAppName(name)
	if err != nil {
		return AppStart{}, err
	}
	state, err := s.Signer.Sign(manifestState{Kind: manifestKind, Team: t.ID, User: c.UserID}, 10*time.Minute)
	if err != nil {
		return AppStart{}, err
	}
	action, err := s.Apps.NewAppURL(strings.TrimSpace(org), state)
	if err != nil {
		return AppStart{}, err
	}
	return AppStart{Action: action, Manifest: Manifest(s.DashboardURL, name, t.ID)}, nil
}

// FinishApp takes the code GitHub sends the browser back with, and keeps the new app for the team. Only the person
// who started it, within ten minutes, and only while they can still write to the team. It returns the team's slug.
func (s *Service) FinishApp(ctx context.Context, c project.Caller, state, code string) (string, error) {
	var st manifestState
	if err := s.Signer.Verify(state, &st); err != nil || st.Kind != manifestKind || st.User != c.UserID {
		return "", ErrBadState
	}
	team, err := store.New(s.Pool).GetMembershipByTeamID(ctx, store.GetMembershipByTeamIDParams{UserID: c.UserID, ID: st.Team})
	if err != nil {
		return "", ErrBadState
	}
	if team.Role == "viewer" {
		return "", project.ErrReadOnly
	}
	creds, err := s.Apps.Convert(ctx, code)
	if err != nil {
		return team.Slug, errors.Join(ErrRefused, err)
	}
	if err := s.Apps.Save(ctx, team.ID, c.UserID, creds); err != nil {
		return team.Slug, err
	}
	return team.Slug, nil
}

// DeleteApp removes the team's app and, with it, its installations and the repositories connected through them.
func (s *Service) DeleteApp(ctx context.Context, c project.Caller, team string) error {
	t, err := s.Projects.Member(ctx, c, team, true)
	if err != nil {
		return err
	}
	return s.Apps.Delete(ctx, t.ID)
}

// WebhookSecret is the secret the team's app signs its deliveries with.
func (s *Service) WebhookSecret(ctx context.Context, team string) (string, error) {
	return s.Apps.WebhookSecret(ctx, team)
}
