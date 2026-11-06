//go:build integration

package build_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/deploy"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
	"greyship.tech/internal/testdb"
)

// localBinary compiles a tiny program the way grey deploy does (for goos) and describes it for CreateLocal.
func localBinary(t *testing.T, service, goos string) (deploy.Binary, []byte) {
	t.Helper()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/shop\n\ngo 1.24\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600)
	out := filepath.Join(dir, "bin")
	cmd := exec.Command("go", "build", "-trimpath", "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH=amd64")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	sum := sha256.Sum256(raw)
	return deploy.Binary{Service: service, Path: "./cmd/" + service, Type: "web", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(raw)), GzBytes: int64(gz.Len())}, gz.Bytes()
}

func put(t *testing.T, url string, body []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
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

func verifyJob(t *testing.T, pool *pgxpool.Pool, id string) bus.Job {
	t.Helper()
	var job bus.Job
	err := pool.QueryRow(context.Background(), "SELECT id, kind, payload FROM jobs WHERE kind = 'build.verify' AND payload->>'deployment_id' = $1", id).Scan(&job.ID, &job.Kind, &job.Payload)
	if err != nil {
		t.Fatalf("no build.verify job for %s: %v", id, err)
	}
	job.Attempts, job.MaxAttempts = 1, 5
	return job
}

func TestLocalBuildFlow(t *testing.T) {
	ctx := context.Background()
	s3 := storage(t)
	pool := testdb.New(t)
	q := store.New(pool)
	svc := deploy.NewService(pool, project.NewService(pool), s3, slog.New(slog.DiscardHandler), time.Now)
	b := newBuilder(pool, s3, &fakeSandbox{})

	team, err := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "acme", Name: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := q.CreateUser(ctx, store.CreateUserParams{Email: "owner@acme.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.AddMember(ctx, store.AddMemberParams{TeamID: team.ID, UserID: u.ID, Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	owner := project.Caller{UserID: u.ID}
	p, err := project.NewService(pool).Create(ctx, owner, "acme", "shop")
	if err != nil {
		t.Fatal(err)
	}

	api, apiGz := localBinary(t, "api", "linux")
	up, err := svc.CreateLocal(ctx, owner, p.ID, "", []deploy.Binary{api}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if up.Deployment.BuildMode != "local" || up.Deployment.SourceKey != nil || len(up.Uploads) != 1 {
		t.Fatalf("created = %+v, uploads %d", up.Deployment.Deployment, len(up.Uploads))
	}
	if _, err := svc.Start(ctx, owner, up.Deployment.ID); err == nil {
		t.Fatal("started before the binary was uploaded")
	}
	put(t, up.Uploads[0].URL, apiGz)
	if _, err := svc.Start(ctx, owner, up.Deployment.ID); err != nil {
		t.Fatal(err)
	}
	if err := b.Verify(ctx, verifyJob(t, pool, up.Deployment.ID)); err != nil {
		t.Fatal(err)
	}
	got, _ := q.GetDeployment(ctx, up.Deployment.ID)
	if got.Status != "built" {
		t.Fatalf("status = %s, want built\n%s", got.Status, logText(t, pool, got.ID))
	}
	arts, _ := q.ListArtifacts(ctx, got.ID)
	if len(arts) != 1 || arts[0].Sha256 != api.SHA256 || arts[0].Goarch != "amd64" || !strings.HasPrefix(arts[0].GoVersion, "1.") {
		t.Fatalf("artifacts = %+v", arts)
	}
	if raw, err := s3.Get(ctx, arts[0].ObjectKey, 1<<30); err != nil || int64(len(raw)) != api.SizeBytes {
		t.Fatalf("stored artifact = %d bytes, %v; the agent expects the uncompressed binary", len(raw), err)
	}
	var releases int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind = 'deployment.release' AND payload->>'deployment_id' = $1", got.ID).Scan(&releases)
	if releases != 1 {
		t.Fatalf("release jobs = %d, want 1", releases)
	}
	if log := logText(t, pool, got.ID); !strings.Contains(log, "checked api: Go 1.") {
		t.Fatalf("log:\n%s", log)
	}

	again, err := svc.CreateLocal(ctx, owner, p.ID, "", []deploy.Binary{api}, nil)
	if err != nil || len(again.Uploads) != 0 {
		t.Fatalf("an unchanged binary must not be uploaded again: %d uploads, %v", len(again.Uploads), err)
	}
	// Its check is skipped too: the new deployment points at the binary already stored and checked.
	if _, err := svc.Start(ctx, owner, again.Deployment.ID); err != nil {
		t.Fatal(err)
	}
	if err := b.Verify(ctx, verifyJob(t, pool, again.Deployment.ID)); err != nil {
		t.Fatal(err)
	}
	reused, _ := q.ListArtifacts(ctx, again.Deployment.ID)
	if len(reused) != 1 || reused[0].ObjectKey != arts[0].ObjectKey || reused[0].GoVersion != arts[0].GoVersion {
		t.Fatalf("reused artifacts = %+v, want the first deploy's object", reused)
	}
	if log := logText(t, pool, again.Deployment.ID); !strings.Contains(log, "unchanged api") {
		t.Fatalf("log:\n%s", log)
	}

	// A files bundle travels like a binary: uploaded once per content, checked before the deploy starts.
	bundle := []byte("not really a tarball, but storage doesn't look")
	bsum := sha256.Sum256(bundle)
	files := &deploy.Files{SHA256: hex.EncodeToString(bsum[:]), SizeBytes: int64(len(bundle))}
	withFiles, err := svc.CreateLocal(ctx, owner, p.ID, "", []deploy.Binary{api}, files)
	if err != nil || withFiles.FilesURL == "" || withFiles.Deployment.FilesKey == nil || *withFiles.Deployment.FilesKey != "files/"+p.ID+"/"+files.SHA256+".tar.gz" {
		t.Fatalf("with files = %+v, %v", withFiles, err)
	}
	if _, err := svc.Start(ctx, owner, withFiles.Deployment.ID); !errors.Is(err, deploy.ErrUploadMissing) {
		t.Fatalf("start before the files were uploaded = %v", err)
	}
	put(t, withFiles.FilesURL, bundle)
	if _, err := svc.Start(ctx, owner, withFiles.Deployment.ID); err != nil {
		t.Fatal(err)
	}
	if same, err := svc.CreateLocal(ctx, owner, p.ID, "", []deploy.Binary{api}, files); err != nil || same.FilesURL != "" {
		t.Fatalf("unchanged files must not be uploaded again: %q, %v", same.FilesURL, err)
	}

	mac, macGz := localBinary(t, "api", "darwin")
	bad, err := svc.CreateLocal(ctx, owner, p.ID, "", []deploy.Binary{mac}, nil)
	if err != nil {
		t.Fatal(err)
	}
	put(t, bad.Uploads[0].URL, macGz)
	if _, err := svc.Start(ctx, owner, bad.Deployment.ID); err != nil {
		t.Fatal(err)
	}
	if err := b.Verify(ctx, verifyJob(t, pool, bad.Deployment.ID)); err != nil {
		t.Fatal(err)
	}
	failed, _ := q.GetDeployment(ctx, bad.Deployment.ID)
	if failed.Status != "failed" || failed.Error == nil || !strings.Contains(*failed.Error, "built for darwin/amd64") {
		t.Fatalf("darwin binary: status %s, error %v", failed.Status, failed.Error)
	}
}
