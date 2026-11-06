// Package project creates, lists and renames projects (spec F-10, F-10a) and checks who may touch them.
package project

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/slug"
	"greyship.tech/internal/store"
)

const (
	maxNameLen = 64
	// Subdomains are <service>-<project> and must fit in 63 characters (F-50b); 40 leaves room for the service.
	maxSlugLen = 40
)

var (
	ErrNotFound  = errors.New("project not found")
	ErrWrongTeam = errors.New("token belongs to a different team")
	ErrReadOnly  = errors.New("viewers can't change projects")
)

// NameError explains why a project name can't be used; Reason is written for people.
type NameError struct{ Reason string }

func (e *NameError) Error() string { return "invalid project name: " + e.Reason }

// TakenError means another project in the team already has the slug.
type TakenError struct{ Existing Project }

func (e *TakenError) Error() string { return fmt.Sprintf("project %s already exists", e.Existing.Slug) }

// Dashboard routes under /<team>/ that a project slug would shadow.
var reserved = map[string]bool{"new": true, "settings": true, "usage": true, "billing": true, "deployments": true}

// Caller is who is asking. TokenTeamID is set when they authenticated with an API token, which only works within its own team (F-3).
type Caller struct {
	UserID      string
	TokenTeamID string
}

type Project struct {
	store.Project
	TeamSlug string
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Normalize validates a display name and returns it trimmed with its slug.
func Normalize(raw string) (name, s string, err error) {
	name = strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > maxNameLen {
		return "", "", &NameError{Reason: fmt.Sprintf("use 1 to %d characters", maxNameLen)}
	}
	s = slug.Make(name, maxSlugLen)
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return "", "", &NameError{Reason: "start the name with a letter"}
	}
	if reserved[s] {
		return "", "", &NameError{Reason: fmt.Sprintf("%q is reserved, pick another name", s)}
	}
	return name, s, nil
}

func (s *Service) Create(ctx context.Context, c Caller, teamSlug, rawName string) (Project, error) {
	name, sl, err := Normalize(rawName)
	if err != nil {
		return Project{}, err
	}
	q := store.New(s.pool)
	team, err := s.member(ctx, q, c, teamSlug)
	if err != nil {
		return Project{}, err
	}
	if team.Role == "viewer" {
		return Project{}, ErrReadOnly
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Project{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	p, err := store.New(tx).CreateProject(ctx, store.CreateProjectParams{TeamID: team.ID, Name: name, Slug: sl})
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, s.taken(ctx, q, team.ID, team.Slug, sl)
	}
	if err != nil {
		return Project{}, fmt.Errorf("create project %s: %w", sl, err)
	}
	if err := bus.EnqueueNotify(ctx, store.New(tx), bus.ProjectCreated, p.ID, c.UserID); err != nil {
		return Project{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Project{}, fmt.Errorf("commit project %s: %w", sl, err)
	}
	return Project{Project: p, TeamSlug: team.Slug}, nil
}

func (s *Service) List(ctx context.Context, c Caller, teamSlug string) ([]Project, error) {
	q := store.New(s.pool)
	team, err := s.member(ctx, q, c, teamSlug)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListProjects(ctx, team.ID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out := make([]Project, 0, len(rows))
	for _, p := range rows {
		out = append(out, Project{Project: p, TeamSlug: team.Slug})
	}
	return out, nil
}

// BySlug loads a project by its team and project slugs, as the dashboard's URLs name it.
func (s *Service) BySlug(ctx context.Context, c Caller, teamSlug, slug string) (Project, error) {
	q := store.New(s.pool)
	team, err := s.member(ctx, q, c, teamSlug)
	if err != nil {
		return Project{}, err
	}
	p, err := q.GetProjectBySlug(ctx, store.GetProjectBySlugParams{TeamID: team.ID, Slug: slug})
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("load project %s/%s: %w", teamSlug, slug, err)
	}
	return Project{Project: p, TeamSlug: team.Slug}, nil
}

func (s *Service) Get(ctx context.Context, c Caller, id string) (Project, error) {
	p, _, err := s.load(ctx, store.New(s.pool), c, id)
	return p, err
}

// Access loads a project the caller can see. With write, viewers get ErrReadOnly.
func (s *Service) Access(ctx context.Context, c Caller, id string, write bool) (Project, error) {
	p, role, err := s.load(ctx, store.New(s.pool), c, id)
	if err != nil {
		return Project{}, err
	}
	if write && role == "viewer" {
		return Project{}, ErrReadOnly
	}
	return p, nil
}

// Rename changes the name and slug; the id stays, so linked folders keep working.
func (s *Service) Rename(ctx context.Context, c Caller, id, rawName string) (Project, error) {
	name, sl, err := Normalize(rawName)
	if err != nil {
		return Project{}, err
	}
	q := store.New(s.pool)
	p, role, err := s.load(ctx, q, c, id)
	if err != nil {
		return Project{}, err
	}
	if role == "viewer" {
		return Project{}, ErrReadOnly
	}
	renamed, err := q.RenameProject(ctx, store.RenameProjectParams{ID: id, Name: name, Slug: sl})
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Project{}, s.taken(ctx, q, p.TeamID, p.TeamSlug, sl)
	}
	if err != nil {
		return Project{}, fmt.Errorf("rename project %s: %w", id, err)
	}
	return Project{Project: renamed, TeamSlug: p.TeamSlug}, nil
}

// Member loads a team the caller belongs to; with write, viewers get ErrReadOnly.
func (s *Service) Member(ctx context.Context, c Caller, teamSlug string, write bool) (store.GetMembershipRow, error) {
	team, err := s.member(ctx, store.New(s.pool), c, teamSlug)
	if err != nil {
		return team, err
	}
	if write && team.Role == "viewer" {
		return team, ErrReadOnly
	}
	return team, nil
}

// member returns the team if the caller belongs to it. Non-members get ErrNotFound so team names can't be probed.
func (s *Service) member(ctx context.Context, q *store.Queries, c Caller, teamSlug string) (store.GetMembershipRow, error) {
	team, err := q.GetMembership(ctx, store.GetMembershipParams{UserID: c.UserID, Slug: teamSlug})
	if errors.Is(err, pgx.ErrNoRows) {
		return team, ErrNotFound
	}
	if err != nil {
		return team, fmt.Errorf("load membership: %w", err)
	}
	if c.TokenTeamID != "" && c.TokenTeamID != team.ID {
		return team, ErrWrongTeam
	}
	return team, nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (s *Service) load(ctx context.Context, q *store.Queries, c Caller, id string) (Project, string, error) {
	if !uuidPattern.MatchString(id) {
		return Project{}, "", ErrNotFound
	}
	p, err := q.GetProject(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, "", ErrNotFound
	}
	if err != nil {
		return Project{}, "", fmt.Errorf("load project %s: %w", id, err)
	}
	team, err := q.GetMembershipByTeamID(ctx, store.GetMembershipByTeamIDParams{UserID: c.UserID, ID: p.TeamID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, "", ErrNotFound
	}
	if err != nil {
		return Project{}, "", fmt.Errorf("load membership: %w", err)
	}
	if c.TokenTeamID != "" && c.TokenTeamID != p.TeamID {
		return Project{}, "", ErrWrongTeam
	}
	return Project{Project: p, TeamSlug: team.Slug}, team.Role, nil
}

func (s *Service) taken(ctx context.Context, q *store.Queries, teamID, teamSlug, sl string) error {
	existing, err := q.GetProjectBySlug(ctx, store.GetProjectBySlugParams{TeamID: teamID, Slug: sl})
	if err != nil {
		return fmt.Errorf("load existing project %s: %w", sl, err)
	}
	return &TakenError{Existing: Project{Project: existing, TeamSlug: teamSlug}}
}
