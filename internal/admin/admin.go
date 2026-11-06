// Package admin is the instance owner's read-only view across every team (decisions.md 048, 050): find any
// deployment by id and see everything needed to debug it. It changes nothing.
package admin

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/store"
)

var (
	ErrNotFound  = errors.New("no such deployment")
	ErrAmbiguous = errors.New("that prefix matches several deployments")
)

var idChars = regexp.MustCompile(`^[0-9a-f-]{4,36}$`)

type Service struct{ Pool *pgxpool.Pool }

type Overview struct {
	Counts     store.AdminCountsRow
	Nodes      []store.AdminNodesRow
	Jobs       []store.AdminJobStatsRow
	FailedJobs []store.AdminFailedJobsRow
	Failed     []store.AdminDeploymentsRow // the latest failed deployments
}

func (s *Service) Overview(ctx context.Context) (Overview, error) {
	q := store.New(s.Pool)
	var o Overview
	var err error
	if o.Counts, err = q.AdminCounts(ctx); err != nil {
		return o, fmt.Errorf("counts: %w", err)
	}
	if o.Nodes, err = q.AdminNodes(ctx); err != nil {
		return o, fmt.Errorf("nodes: %w", err)
	}
	if o.Jobs, err = q.AdminJobStats(ctx); err != nil {
		return o, fmt.Errorf("job stats: %w", err)
	}
	if o.FailedJobs, err = q.AdminFailedJobs(ctx, 20); err != nil {
		return o, fmt.Errorf("failed jobs: %w", err)
	}
	if o.Failed, err = q.AdminDeployments(ctx, store.AdminDeploymentsParams{Status: "failed", Lim: 15}); err != nil {
		return o, fmt.Errorf("failed deployments: %w", err)
	}
	return o, nil
}

func (s *Service) Deployments(ctx context.Context, status, query string, limit int32) ([]store.AdminDeploymentsRow, error) {
	return store.New(s.Pool).AdminDeployments(ctx, store.AdminDeploymentsParams{Status: status, Q: strings.ToLower(strings.TrimSpace(query)), Lim: limit})
}

// Detail is everything about one deployment, for debugging it.
type Detail struct {
	Deployment store.AdminDeploymentRow
	Artifacts  []store.Artifact
	Uploaded   []store.UploadedBinary
	Instances  []store.AdminDeploymentInstancesRow
	Jobs       []store.AdminDeploymentJobsRow
	BuildLog   []store.AdminBuildLogRow
	AppLogs    []store.AdminDeploymentAppLogsRow
}

// Deployment finds a deployment by its id or the start of it (at least 4 characters), as shown in the CLI.
func (s *Service) Deployment(ctx context.Context, idOrPrefix string) (Detail, error) {
	id := strings.ToLower(strings.TrimSpace(idOrPrefix))
	if !idChars.MatchString(id) {
		return Detail{}, ErrNotFound
	}
	q := store.New(s.Pool)
	if len(id) < 36 {
		ids, err := q.AdminDeploymentByPrefix(ctx, id)
		if err != nil {
			return Detail{}, fmt.Errorf("find deployment %s: %w", id, err)
		}
		switch len(ids) {
		case 0:
			return Detail{}, ErrNotFound
		case 1:
			id = ids[0]
		default:
			return Detail{}, ErrAmbiguous
		}
	}
	var d Detail
	var err error
	d.Deployment, err = q.AdminDeployment(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, fmt.Errorf("load deployment %s: %w", id, err)
	}
	if d.Artifacts, err = q.ListArtifacts(ctx, id); err != nil {
		return d, fmt.Errorf("artifacts: %w", err)
	}
	if d.Uploaded, err = q.ListUploadedBinaries(ctx, id); err != nil {
		return d, fmt.Errorf("uploaded binaries: %w", err)
	}
	if d.Instances, err = q.AdminDeploymentInstances(ctx, id); err != nil {
		return d, fmt.Errorf("instances: %w", err)
	}
	if d.Jobs, err = q.AdminDeploymentJobs(ctx, id); err != nil {
		return d, fmt.Errorf("jobs: %w", err)
	}
	if d.BuildLog, err = q.AdminBuildLog(ctx, id); err != nil {
		return d, fmt.Errorf("build log: %w", err)
	}
	if d.AppLogs, err = q.AdminDeploymentAppLogs(ctx, id); err != nil {
		return d, fmt.Errorf("app logs: %w", err)
	}
	return d, nil
}

func (s *Service) Users(ctx context.Context, limit int32) ([]store.AdminUsersRow, error) {
	return store.New(s.Pool).AdminUsers(ctx, limit)
}
