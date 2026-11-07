//go:build integration

package deploy_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"greyship.tech/internal/deploy"
	"greyship.tech/internal/objstore"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
	"greyship.tech/internal/testdb"
)

func storage(t *testing.T) *objstore.Store {
	t.Helper()
	endpoint := os.Getenv("GREYSHIP_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("GREYSHIP_TEST_S3_ENDPOINT not set; run make test-int")
	}
	return &objstore.Store{
		Endpoint: endpoint, Region: os.Getenv("GREYSHIP_TEST_S3_REGION"), Bucket: "greyship-test",
		AccessKey: os.Getenv("GREYSHIP_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("GREYSHIP_TEST_S3_SECRET_KEY"),
		HTTP: &http.Client{Timeout: 10 * time.Second}, Now: time.Now,
	}
}

func member(t *testing.T, q *store.Queries, email, teamID, role string) project.Caller {
	t.Helper()
	ctx := context.Background()
	u, err := q.CreateUser(ctx, store.CreateUserParams{Email: email})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.AddMember(ctx, store.AddMemberParams{TeamID: teamID, UserID: u.ID, Role: role}); err != nil {
		t.Fatal(err)
	}
	return project.Caller{UserID: u.ID}
}

func upload(t *testing.T, url string, body []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d %s", resp.StatusCode, msg)
	}
}

func TestDeployFlow(t *testing.T) {
	ctx := context.Background()
	s3 := storage(t)
	pool := testdb.New(t)
	q := store.New(pool)
	projects := project.NewService(pool)
	svc := deploy.NewService(pool, projects, s3, slog.New(slog.DiscardHandler), time.Now)

	acme, err := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "acme", Name: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "other", Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	owner := member(t, q, "owner@acme.test", acme.ID, "owner")
	viewer := member(t, q, "viewer@acme.test", acme.ID, "viewer")
	outsider := member(t, q, "owner@other.test", other.ID, "owner")
	p, err := projects.Create(ctx, owner, "acme", "shop")
	if err != nil {
		t.Fatal(err)
	}

	body := []byte("tarball bytes")
	sum := sha256.Sum256(body)
	src := deploy.Source{Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}

	if _, err := svc.Create(ctx, viewer, p.ID, src); !errors.Is(err, project.ErrReadOnly) {
		t.Fatalf("viewer create = %v, want ErrReadOnly", err)
	}
	if _, err := svc.Create(ctx, outsider, p.ID, src); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("outsider create = %v, want ErrNotFound", err)
	}
	if _, err := svc.Create(ctx, owner, p.ID, deploy.Source{Size: deploy.MaxSourceSize + 1, SHA256: src.SHA256}); !errors.Is(err, deploy.ErrTooLarge) {
		t.Fatalf("oversized create = %v, want ErrTooLarge", err)
	}

	up, err := svc.Create(ctx, owner, p.ID, src)
	if err != nil {
		t.Fatal(err)
	}
	d := up.Deployment
	if d.Status != "uploading" || d.Source != "cli" || d.SourceKey == nil || !strings.HasPrefix(*d.SourceKey, "sources/"+p.ID+"/") {
		t.Fatalf("created = %+v", d.Deployment)
	}

	if _, err := svc.Start(ctx, owner, d.ID); !errors.Is(err, deploy.ErrUploadMissing) {
		t.Fatalf("start before upload = %v, want ErrUploadMissing", err)
	}
	upload(t, up.URL, body)
	if _, err := svc.Start(ctx, outsider, d.ID); !errors.Is(err, deploy.ErrNotFound) {
		t.Fatalf("outsider start = %v, want ErrNotFound", err)
	}
	if _, err := svc.Start(ctx, viewer, d.ID); !errors.Is(err, project.ErrReadOnly) {
		t.Fatalf("viewer start = %v, want ErrReadOnly", err)
	}
	started, err := svc.Start(ctx, owner, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != "queued" {
		t.Fatalf("status = %s, want queued", started.Status)
	}
	if _, err := svc.Start(ctx, owner, d.ID); !errors.Is(err, deploy.ErrNotUploading) {
		t.Fatalf("second start = %v, want ErrNotUploading", err)
	}
	var kind, payload string
	var jobs int
	if err := pool.QueryRow(ctx, "SELECT count(*), min(kind), min(payload->>'deployment_id') FROM jobs WHERE kind <> 'notify'").Scan(&jobs, &kind, &payload); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 || kind != "build.requested" || payload != d.ID {
		t.Fatalf("jobs = %d %s %s, want one build.requested for %s", jobs, kind, payload, d.ID)
	}

	// No limit set: builds aren't grouped. With one, a user's builds wait behind their own.
	var group *string
	if err := pool.QueryRow(ctx, "SELECT group_key FROM jobs WHERE kind = 'build.requested'").Scan(&group); err != nil || group != nil {
		t.Fatalf("an unlimited team's build is in group %v (%v)", group, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE teams SET max_concurrent_builds = 2 WHERE id = $1", acme.ID); err != nil {
		t.Fatal(err)
	}
	up2, err := svc.Create(ctx, owner, p.ID, src)
	if err != nil {
		t.Fatal(err)
	}
	upload(t, up2.URL, body)
	if _, err := svc.Start(ctx, owner, up2.Deployment.ID); err != nil {
		t.Fatal(err)
	}
	var limit int
	if err := pool.QueryRow(ctx, "SELECT group_key, group_limit FROM jobs WHERE payload->>'deployment_id' = $1", up2.Deployment.ID).Scan(&group, &limit); err != nil || group == nil || *group != acme.ID || limit != 2 {
		t.Fatalf("a limited team's build is in group %v limit %d (%v), want its team and 2", group, limit, err)
	}

	got, err := svc.Get(ctx, viewer, d.ID)
	if err != nil || got.Status != "queued" {
		t.Fatalf("viewer get = %+v, %v", got.Deployment, err)
	}
	if _, err := svc.Get(ctx, outsider, d.ID); !errors.Is(err, deploy.ErrNotFound) {
		t.Fatalf("outsider get = %v, want ErrNotFound", err)
	}
	if _, err := svc.Get(ctx, owner, "not-a-uuid"); !errors.Is(err, deploy.ErrNotFound) {
		t.Fatalf("bad id get = %v, want ErrNotFound", err)
	}
	list, err := svc.List(ctx, owner, p.ID, 10)
	if err != nil || len(list) != 2 || (list[0].ID != d.ID && list[1].ID != d.ID) {
		t.Fatalf("list = %+v, %v", list, err)
	}

	if _, err := svc.Redeploy(ctx, owner, d.ID); !errors.Is(err, deploy.ErrNotDeployable) {
		t.Fatalf("redeploy of a queued deployment = %v, want ErrNotDeployable", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE deployments SET status = 'superseded' WHERE id = $1", d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Redeploy(ctx, viewer, d.ID); !errors.Is(err, project.ErrReadOnly) {
		t.Fatalf("viewer redeploy = %v, want ErrReadOnly", err)
	}
	if _, err := svc.Redeploy(ctx, outsider, d.ID); !errors.Is(err, deploy.ErrNotFound) {
		t.Fatalf("outsider redeploy = %v, want ErrNotFound", err)
	}
	back, err := svc.Redeploy(ctx, owner, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.Status != "built" || back.RollbackOf == nil || *back.RollbackOf != d.ID || back.Source != "dashboard" || *back.SourceKey != *d.SourceKey {
		t.Fatalf("redeploy = %+v", back.Deployment)
	}
	var skip bool
	if err := pool.QueryRow(ctx, "SELECT (payload->>'skip_release_command')::bool FROM jobs WHERE kind = 'deployment.release' AND payload->>'deployment_id' = $1", back.ID).Scan(&skip); err != nil || !skip {
		t.Fatalf("release job skip_release_command = %v, %v; want true", skip, err)
	}

	tokenOtherTeam := project.Caller{UserID: owner.UserID, TokenTeamID: other.ID}
	if _, err := svc.Get(ctx, tokenOtherTeam, d.ID); !errors.Is(err, project.ErrWrongTeam) {
		t.Fatalf("token for another team = %v, want ErrWrongTeam", err)
	}
}
