//go:build integration

package projdel_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/projdel"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
	"greyship.tech/internal/testdb"
)

type objects struct {
	mu      sync.Mutex
	deleted []string
}

func (o *objects) Delete(_ context.Context, key string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.deleted = append(o.deleted, key)
	return nil
}

func job(t *testing.T, id string) bus.Job {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"project_id": id})
	return bus.Job{ID: 1, Kind: bus.ProjectDelete, Payload: b}
}

// A deleted project's running instance is told to stop, and nothing is removed until it has; then its stored
// objects, rows and everything hanging off them go.
func TestDeleteWaitsForInstancesThenRemovesEverything(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := store.New(pool)
	team, _ := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "acme", Name: "acme"})
	user, _ := q.CreateUser(ctx, store.CreateUserParams{Email: "o@example.com"})
	_ = q.AddMember(ctx, store.AddMemberParams{TeamID: team.ID, UserID: user.ID, Role: "owner"})
	svc := project.NewService(pool)
	p, err := svc.Create(ctx, project.Caller{UserID: user.ID}, "acme", "Shop")
	if err != nil {
		t.Fatal(err)
	}
	node, _ := q.UpsertNode(ctx, store.UpsertNodeParams{Name: "n1", Region: "dev", TotalMb: 4096})
	src, size, sum := "sources/shop.tar.gz", int64(1), strings.Repeat("a", 64)
	d, err := q.CreateDeployment(ctx, store.CreateDeploymentParams{ID: "00000000-0000-4000-8000-000000000001", ProjectID: p.ID, Source: "cli", SourceKey: &src, SourceSize: &size, SourceSha256: &sum})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateArtifact(ctx, store.CreateArtifactParams{DeploymentID: d.ID, ServiceName: "api", ServicePath: "./cmd/api", ServiceType: "web", ObjectKey: "artifacts/shop/api", Sha256: sum, SizeBytes: 1, GoVersion: "1.24", Goarch: "arm64"}); err != nil {
		t.Fatal(err)
	}
	s, err := q.UpsertService(ctx, store.UpsertServiceParams{ProjectID: p.ID, Name: "api", Path: "./cmd/api", Type: "web"})
	if err != nil {
		t.Fatal(err)
	}
	art, _ := q.ListArtifacts(ctx, d.ID)
	inst, err := q.CreateInstance(ctx, store.CreateInstanceParams{ServiceID: s.ID, ArtifactID: art[0].ID, DeploymentID: d.ID, NodeID: node.ID, MemoryMb: 128})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE instances SET state = 'running' WHERE id = $1", inst.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, project.Caller{UserID: user.ID}, p.ID, p.Slug); err != nil {
		t.Fatal(err)
	}

	obj := &objects{}
	del := &projdel.Deleter{Pool: pool, Objects: obj, Logger: slog.New(slog.DiscardHandler), Wait: 300 * time.Millisecond, Poll: 20 * time.Millisecond}

	// The agent hasn't stopped it: the job asks, waits, and tries again later. Nothing is removed.
	if err := del.Handle(ctx, job(t, p.ID)); !errors.Is(err, projdel.ErrStillRunning) {
		t.Fatalf("with a running instance: %v, want ErrStillRunning", err)
	}
	var commands int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE payload->>'instance_id' = $1 AND payload->>'action' = 'instance.stop'", inst.ID).Scan(&commands)
	if commands == 0 {
		var kinds []string
		rows, _ := pool.Query(ctx, "SELECT kind || ' ' || payload::text FROM jobs")
		for rows.Next() {
			var k string
			_ = rows.Scan(&k)
			kinds = append(kinds, k)
		}
		t.Fatalf("no instance.stop was sent; jobs: %v", kinds)
	}
	if _, err := q.GetDeletingProject(ctx, p.ID); err != nil || len(obj.deleted) != 0 {
		t.Fatalf("the project or its objects were removed while an instance ran: %v %v", err, obj.deleted)
	}

	// The agent stops it: the next attempt removes everything.
	if _, err := pool.Exec(ctx, "UPDATE instances SET state = 'stopped' WHERE id = $1", inst.ID); err != nil {
		t.Fatal(err)
	}
	if err := del.Handle(ctx, job(t, p.ID)); err != nil {
		t.Fatal(err)
	}
	if len(obj.deleted) != 2 {
		t.Fatalf("deleted objects = %v, want the source and the binary", obj.deleted)
	}
	for table, col := range map[string]string{"projects": "id", "deployments": "project_id", "services": "project_id"} {
		var n int
		_ = pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE "+col+" = $1", p.ID).Scan(&n)
		if n != 0 {
			t.Errorf("%s still has %d rows for the deleted project", table, n)
		}
	}
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM instances WHERE id = $1", inst.ID).Scan(&n)
	if n != 0 {
		t.Fatal("the instance row survived")
	}
	// A repeated job for a project that's gone does nothing.
	if err := del.Handle(ctx, job(t, p.ID)); err != nil {
		t.Fatalf("repeated job: %v", err)
	}
}
