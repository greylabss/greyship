// Package deploy creates deployments and hands their source to object storage (spec F-20, architecture §4.1).
package deploy

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/objstore"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
)

// MaxSourceSize is the largest compressed source accepted (spec §6). internal/pack checks the same limit first.
const MaxSourceSize = 100 << 20

const uploadTTL = 15 * time.Minute

var (
	ErrNotFound      = errors.New("deployment not found")
	ErrTooLarge      = fmt.Errorf("source is larger than %d MB compressed", MaxSourceSize>>20)
	ErrBadSource     = errors.New("invalid source description")
	ErrUploadMissing = errors.New("source upload not found or incomplete")
	ErrNotUploading  = errors.New("deployment already started")
	ErrNotDeployable = errors.New("only a deployment that went live can be redeployed or rolled back to")
)

type Projects interface {
	Access(ctx context.Context, c project.Caller, id string, write bool) (project.Project, error)
	Member(ctx context.Context, c project.Caller, teamSlug string, write bool) (store.GetMembershipRow, error)
}

type Storage interface {
	PresignPut(key string, size int64, ttl time.Duration) (string, error)
	Stat(ctx context.Context, key string) (int64, error)
}

type Service struct {
	pool     *pgxpool.Pool
	projects Projects
	storage  Storage
	logger   *slog.Logger
	now      func() time.Time
}

func NewService(pool *pgxpool.Pool, projects Projects, storage Storage, logger *slog.Logger, now func() time.Time) *Service {
	return &Service{pool: pool, projects: projects, storage: storage, logger: logger, now: now}
}

type Deployment struct {
	store.Deployment
	Project project.Project
}

type Source struct {
	Size      int64
	SHA256    string
	CommitSHA string // optional
}

type Upload struct {
	Deployment Deployment
	URL        string
	ExpiresAt  time.Time
}

var (
	sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Create records a deployment and returns a one-off URL the caller PUTs the tarball to.
func (s *Service) Create(ctx context.Context, c project.Caller, projectID string, src Source) (Upload, error) {
	switch {
	case src.Size > MaxSourceSize:
		return Upload{}, ErrTooLarge
	case src.Size <= 0, !sha256Pattern.MatchString(src.SHA256), src.CommitSHA != "" && !commitPattern.MatchString(src.CommitSHA):
		return Upload{}, ErrBadSource
	}
	p, err := s.projects.Access(ctx, c, projectID, true)
	if err != nil {
		return Upload{}, err
	}
	id, err := newID()
	if err != nil {
		return Upload{}, err
	}
	key := fmt.Sprintf("sources/%s/%s.tar.gz", p.ID, id)
	params := store.CreateDeploymentParams{
		ID: id, ProjectID: p.ID, Source: "cli",
		SourceKey: &key, SourceSize: &src.Size, SourceSha256: &src.SHA256, CreatedBy: &c.UserID,
	}
	if src.CommitSHA != "" {
		params.CommitSha = &src.CommitSHA
	}
	url, err := s.storage.PresignPut(key, src.Size, uploadTTL)
	if err != nil {
		return Upload{}, fmt.Errorf("presign upload: %w", err)
	}
	d, err := store.New(s.pool).CreateDeployment(ctx, params)
	if err != nil {
		return Upload{}, fmt.Errorf("create deployment for %s: %w", p.ID, err)
	}
	return Upload{Deployment: Deployment{Deployment: d, Project: p}, URL: url, ExpiresAt: s.now().Add(uploadTTL)}, nil
}

// Start confirms the tarball arrived with the declared size and queues the deployment for a builder.
func (s *Service) Start(ctx context.Context, c project.Caller, id string) (Deployment, error) {
	d, err := s.load(ctx, c, id, true)
	if err != nil {
		return Deployment{}, err
	}
	if d.Status != "uploading" {
		return Deployment{}, ErrNotUploading
	}
	kind := bus.BuildRequested
	if d.BuildMode == "local" {
		if err := s.checkUploads(ctx, d.Deployment); err != nil {
			return Deployment{}, err
		}
		kind = bus.BuildVerify
	} else {
		size, err := s.storage.Stat(ctx, *d.SourceKey)
		if err != nil && !errors.Is(err, objstore.ErrNotFound) {
			return Deployment{}, fmt.Errorf("check source upload for %s: %w", d.ID, err)
		}
		if err != nil || size != *d.SourceSize {
			return Deployment{}, ErrUploadMissing
		}
	}
	queued, err := s.queue(ctx, d.ID, d.Project.TeamID, kind)
	if err != nil {
		return Deployment{}, err
	}
	s.logger.Info("deployment queued", "deployment", queued.ID, "project", queued.ProjectID, "size", queued.SourceSize)
	return Deployment{Deployment: queued, Project: d.Project}, nil
}

// QueueBuild hands an uploaded deployment to the builder, as Start does once the CLI's upload is in; GitHub deploys
// use it after fetching their source.
func (s *Service) QueueBuild(ctx context.Context, id, teamID string) error {
	_, err := s.queue(ctx, id, teamID, bus.BuildRequested)
	return err
}

// queue marks the deployment queued and asks for a build in one transaction, so neither happens without the other.
func (s *Service) queue(ctx context.Context, id, teamID, kind string) (store.Deployment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Deployment{}, fmt.Errorf("begin queue deployment %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	queued, err := q.MarkDeploymentQueued(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Deployment{}, ErrNotUploading
	}
	if err != nil {
		return store.Deployment{}, fmt.Errorf("queue deployment %s: %w", id, err)
	}
	// An admin may limit how many builds one user runs at once; a user's burst of deploys then waits behind their
	// own builds, not everyone else's. A check of a local build is a different kind, so it never waits behind
	// someone's compile. No limit means no group.
	maxBuilds, err := q.TeamMaxConcurrentBuilds(ctx, teamID)
	if err != nil {
		return store.Deployment{}, fmt.Errorf("load build limit of team %s: %w", teamID, err)
	}
	var group bus.Group
	if maxBuilds != nil {
		group = bus.Group{Key: teamID, Limit: *maxBuilds}
	}
	if _, err := bus.EnqueueGrouped(ctx, q, kind, group, map[string]string{"deployment_id": id}); err != nil {
		return store.Deployment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.Deployment{}, fmt.Errorf("commit queue deployment %s: %w", id, err)
	}
	return queued, nil
}

func (s *Service) Get(ctx context.Context, c project.Caller, id string) (Deployment, error) {
	return s.load(ctx, c, id, false)
}

func (s *Service) List(ctx context.Context, c project.Caller, projectID string, limit int32) ([]Deployment, error) {
	p, err := s.projects.Access(ctx, c, projectID, false)
	if err != nil {
		return nil, err
	}
	rows, err := store.New(s.pool).ListDeployments(ctx, store.ListDeploymentsParams{ProjectID: p.ID, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list deployments for %s: %w", p.ID, err)
	}
	out := make([]Deployment, 0, len(rows))
	for _, d := range rows {
		out = append(out, Deployment{Deployment: d, Project: p})
	}
	return out, nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// load returns the deployment if the caller can see its project. Deployments in other teams are ErrNotFound.
func (s *Service) load(ctx context.Context, c project.Caller, id string, write bool) (Deployment, error) {
	if !uuidPattern.MatchString(id) {
		return Deployment{}, ErrNotFound
	}
	d, err := store.New(s.pool).GetDeployment(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("load deployment %s: %w", id, err)
	}
	p, err := s.projects.Access(ctx, c, d.ProjectID, write)
	if errors.Is(err, project.ErrNotFound) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, err
	}
	return Deployment{Deployment: d, Project: p}, nil
}

// newID is a random (version 4) UUID, made here so the storage key can be signed before the row exists.
// Redeploy makes a new deployment from an existing one's binaries and releases it: Redeploy for the live one (fresh
// instances, current env vars), Roll back for an earlier one (F-27). Nothing is rebuilt and the release command
// doesn't run, since its migrations already ran for these binaries.
func (s *Service) Redeploy(ctx context.Context, c project.Caller, id string) (Deployment, error) {
	d, err := s.load(ctx, c, id, true)
	if err != nil {
		return Deployment{}, err
	}
	if d.Status != "ready" && d.Status != "superseded" {
		return Deployment{}, ErrNotDeployable
	}
	newID, err := newID()
	if err != nil {
		return Deployment{}, err
	}
	// A token is the CLI; a session cookie is the dashboard.
	source := "dashboard"
	if c.TokenTeamID != "" {
		source = "cli"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Deployment{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	nd, err := q.CreateRedeploy(ctx, store.CreateRedeployParams{ID: newID, Source: source, CreatedBy: &c.UserID, Of: d.ID})
	if err != nil {
		return Deployment{}, fmt.Errorf("create redeploy of %s: %w", d.ID, err)
	}
	if err := q.CopyArtifacts(ctx, store.CopyArtifactsParams{ToDeployment: nd.ID, FromDeployment: d.ID}); err != nil {
		return Deployment{}, fmt.Errorf("copy artifacts of %s: %w", d.ID, err)
	}
	if _, err := bus.Enqueue(ctx, q, bus.DeploymentRelease, map[string]any{"deployment_id": nd.ID, "skip_release_command": true}); err != nil {
		return Deployment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Deployment{}, err
	}
	s.logger.Info("redeploy", "deployment", nd.ID, "of", d.ID)
	return Deployment{Deployment: nd, Project: d.Project}, nil
}

// NewID makes a deployment id, for deployments created outside this package (GitHub pushes).
func NewID() (string, error) { return newID() }

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate deployment id: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// Finished reports whether the deployment is over (live, failed or replaced), so a log follower can stop.
func Finished(status string) bool {
	return status == "ready" || status == "failed" || status == "superseded"
}

// Logs returns build log lines after seq, the deployment's current state, and its artifacts once built.
func (s *Service) Logs(ctx context.Context, c project.Caller, id string, after int32, limit int32) (Deployment, []store.BuildLog, []store.Artifact, error) {
	d, err := s.load(ctx, c, id, false)
	if err != nil {
		return Deployment{}, nil, nil, err
	}
	q := store.New(s.pool)
	lines, err := q.BuildLogsAfter(ctx, store.BuildLogsAfterParams{DeploymentID: d.ID, Seq: after, Limit: limit})
	if err != nil {
		return Deployment{}, nil, nil, fmt.Errorf("read build log of %s: %w", d.ID, err)
	}
	var artifacts []store.Artifact
	if Finished(d.Status) {
		if artifacts, err = q.ListArtifacts(ctx, d.ID); err != nil {
			return Deployment{}, nil, nil, fmt.Errorf("list artifacts of %s: %w", d.ID, err)
		}
	}
	return d, lines, artifacts, nil
}

// Instances lists where the deployment's services run (or ran).
func (s *Service) Instances(ctx context.Context, c project.Caller, id string) ([]store.ListDeploymentInstancesRow, error) {
	d, err := s.load(ctx, c, id, false)
	if err != nil {
		return nil, err
	}
	rows, err := store.New(s.pool).ListDeploymentInstances(ctx, d.ID)
	if err != nil {
		return nil, fmt.Errorf("list instances of %s: %w", d.ID, err)
	}
	return rows, nil
}

// Services lists a project's services in its live deployment with their instance counts. A project that was never
// released has none.
func (s *Service) Services(ctx context.Context, c project.Caller, projectID string) ([]store.LiveServiceStatesRow, error) {
	p, err := s.projects.Access(ctx, c, projectID, false)
	if err != nil {
		return nil, err
	}
	rows, err := store.New(s.pool).LiveServiceStates(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("list services of %s: %w", p.ID, err)
	}
	return rows, nil
}

// URLs lists the public web services of a deployment and their subdomain labels.
func (s *Service) URLs(ctx context.Context, c project.Caller, id string) ([]store.ServiceURLsRow, error) {
	d, err := s.load(ctx, c, id, false)
	if err != nil {
		return nil, err
	}
	rows, err := store.New(s.pool).ServiceURLs(ctx, d.ID)
	if err != nil {
		return nil, fmt.Errorf("list URLs of %s: %w", d.ID, err)
	}
	return rows, nil
}

// AppLogs returns a project's app log lines after id (or the last tail lines when tail > 0), optionally for
// one service.
func (s *Service) AppLogs(ctx context.Context, c project.Caller, projectID, service string, after int64, tail int32) ([]store.AppLogsAfterRow, error) {
	p, err := s.projects.Access(ctx, c, projectID, false)
	if err != nil {
		return nil, err
	}
	var svc *string
	if service != "" {
		svc = &service
	}
	q := store.New(s.pool)
	if tail > 0 {
		rows, err := q.AppLogsTail(ctx, store.AppLogsTailParams{ProjectID: p.ID, Service: svc, Limit: tail})
		if err != nil {
			return nil, fmt.Errorf("read logs of %s: %w", p.ID, err)
		}
		out := make([]store.AppLogsAfterRow, len(rows))
		for i, r := range rows {
			out[len(rows)-1-i] = store.AppLogsAfterRow(r)
		}
		return out, nil
	}
	rows, err := q.AppLogsAfter(ctx, store.AppLogsAfterParams{ProjectID: p.ID, ID: after, Service: svc, Limit: 500})
	if err != nil {
		return nil, fmt.Errorf("read logs of %s: %w", p.ID, err)
	}
	return rows, nil
}

// BuildsAhead is how many builds will run before this deployment's (0 once it is building).
func (s *Service) BuildsAhead(ctx context.Context, c project.Caller, id string) (int64, error) {
	d, err := s.load(ctx, c, id, false)
	if err != nil {
		return 0, err
	}
	if d.Status != "queued" {
		return 0, nil
	}
	n, err := store.New(s.pool).BuildsAhead(ctx, d.ID)
	if err != nil {
		return 0, fmt.Errorf("count builds ahead of %s: %w", d.ID, err)
	}
	return n, nil
}
