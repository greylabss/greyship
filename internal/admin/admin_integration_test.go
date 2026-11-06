//go:build integration

package admin_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"greyship.tech/internal/admin"
	"greyship.tech/internal/store"
	"greyship.tech/internal/testdb"
)

func TestAdminQueries(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := store.New(pool)
	team, _ := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "acme", Name: "acme"})
	u, _ := q.CreateUser(ctx, store.CreateUserParams{Email: "dev@acme.test"})
	_ = q.AddMember(ctx, store.AddMemberParams{TeamID: team.ID, UserID: u.ID, Role: "owner"})
	p, _ := q.CreateProject(ctx, store.CreateProjectParams{TeamID: team.ID, Name: "shop", Slug: "shop"})
	key, size, sum := "k", int64(1), strings.Repeat("a", 64)
	d, err := q.CreateDeployment(ctx, store.CreateDeploymentParams{ID: "abcdef01-0000-4000-8000-000000000001", ProjectID: p.ID, Source: "cli", CreatedBy: &u.ID, SourceKey: &key, SourceSize: &size, SourceSha256: &sum})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE deployments SET status = 'failed', error = 'go build failed', finished_at = now() WHERE id = $1", d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO build_logs (deployment_id, seq, stream, line) VALUES ($1, 1, 'system', 'fetching source'), ($1, 2, 'build', 'syntax error')", d.ID); err != nil {
		t.Fatal(err)
	}
	s := &admin.Service{Pool: pool}

	o, err := s.Overview(ctx)
	if err != nil || o.Counts.Users != 1 || o.Counts.Failed24h != 1 || len(o.Failed) != 1 || o.Failed[0].CreatedByEmail != "dev@acme.test" {
		t.Fatalf("overview = %+v, %v", o.Counts, err)
	}
	for _, query := range []string{"abcdef01", "shop", "acme", ""} {
		rows, err := s.Deployments(ctx, "", query, 10)
		if err != nil || len(rows) != 1 {
			t.Fatalf("deployments(%q) = %d, %v", query, len(rows), err)
		}
	}
	if rows, _ := s.Deployments(ctx, "ready", "", 10); len(rows) != 0 {
		t.Fatalf("status filter let %d through", len(rows))
	}

	detail, err := s.Deployment(ctx, "ABCDEF01")
	if err != nil || detail.Deployment.ID != d.ID || len(detail.BuildLog) != 2 || detail.Deployment.TeamSlug != "acme" {
		t.Fatalf("detail = %+v, %v", detail.Deployment, err)
	}
	if _, err := s.Deployment(ctx, "ffffffff"); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("unknown prefix = %v", err)
	}
	if _, err := s.Deployment(ctx, "'; drop table users; --"); !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("junk id = %v", err)
	}
	// Someone who never deployed has no last deploy.
	if _, err := q.CreateUser(ctx, store.CreateUserParams{Email: "new@acme.test"}); err != nil {
		t.Fatal(err)
	}
	users, err := s.Users(ctx, 10)
	if err != nil || len(users) != 2 || users[1].Teams != "acme" || users[1].Projects != 1 || users[0].LastDeploy != nil {
		t.Fatalf("users = %+v, %v", users, err)
	}
}
