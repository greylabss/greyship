//go:build integration

package build_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/build"
	"greyship.tech/internal/bus"
	"greyship.tech/internal/objstore"
	"greyship.tech/internal/pack"
	"greyship.tech/internal/store"
	"greyship.tech/internal/testdb"
)

type fakeSandbox struct {
	got  build.Spec
	bins map[string][]byte
	err  error
}

func (f *fakeSandbox) Run(_ context.Context, spec build.Spec, _ []byte, logLine func(string, string)) (map[string][]byte, error) {
	f.got = spec
	logLine("build", "go: downloading github.com/go-chi/chi/v5 v5.1.0")
	if f.err != nil {
		logLine("build", "./main.go:3:1: syntax error: unexpected }")
		return nil, f.err
	}
	return f.bins, nil
}

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

// queued makes a team, project and queued deployment whose source is the given files, uploaded to storage.
func queued(t *testing.T, pool *pgxpool.Pool, s3 *objstore.Store, files map[string]string) (store.Deployment, bus.Job) {
	t.Helper()
	ctx := context.Background()
	q := store.New(pool)
	team, err := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "acme", Name: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := q.CreateProject(ctx, store.CreateProjectParams{TeamID: team.ID, Name: "shop", Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(full), 0o750)
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var src bytes.Buffer
	if _, err := pack.Write(root, &src); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(src.Bytes())
	id := "11111111-2222-4333-8444-" + strings.Repeat("5", 12)
	key := "sources/" + p.ID + "/" + id + ".tar.gz"
	if err := s3.Put(ctx, key, bytes.NewReader(src.Bytes()), int64(src.Len())); err != nil {
		t.Fatal(err)
	}
	d, err := q.CreateDeployment(ctx, store.CreateDeploymentParams{ID: id, ProjectID: p.ID, Source: "cli", SourceKey: &key, SourceSize: ptr(int64(src.Len())), SourceSha256: ptr(hex.EncodeToString(sum[:]))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.MarkDeploymentQueued(ctx, id); err != nil {
		t.Fatal(err)
	}
	job, err := bus.Enqueue(ctx, q, bus.BuildRequested, map[string]string{"deployment_id": id})
	if err != nil {
		t.Fatal(err)
	}
	job.Attempts, job.MaxAttempts = 1, 5
	return d, job
}

var sample = map[string]string{
	"go.mod":             "module example.com/shop\n\ngo 1.24\n",
	"cmd/api/main.go":    "package main\n\nimport \"net/http\"\n\nfunc main() { http.ListenAndServe(\":8080\", nil) }\n",
	"cmd/worker/main.go": "package main\n\nfunc main() { select {} }\n",
}

func newBuilder(pool *pgxpool.Pool, s3 *objstore.Store, sb build.Compiler) *build.Builder {
	return &build.Builder{Pool: pool, Storage: s3, Runner: &build.LocalRunner{Compiler: sb, Storage: s3, Now: time.Now}, DefaultGo: "1.27", GOARCH: "amd64", Logger: slog.New(slog.DiscardHandler), Now: time.Now}
}

func logText(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	lines, err := store.New(pool).BuildLogsAfter(context.Background(), store.BuildLogsAfterParams{DeploymentID: id, Seq: 0, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Line + "\n")
	}
	return b.String()
}

func TestBuildSucceeds(t *testing.T) {
	ctx := context.Background()
	s3 := storage(t)
	pool := testdb.New(t)
	d, job := queued(t, pool, s3, sample)
	sb := &fakeSandbox{bins: map[string][]byte{"api": []byte("\x7fELF api"), "worker": []byte("\x7fELF worker")}}
	b := newBuilder(pool, s3, sb)

	if err := b.Handle(ctx, job); err != nil {
		t.Fatal(err)
	}
	q := store.New(pool)
	got, _ := q.GetDeployment(ctx, d.ID)
	if got.Status != "built" {
		t.Fatalf("deployment = %s, want built", got.Status)
	}
	var releases int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind = 'deployment.release' AND payload->>'deployment_id' = $1", d.ID).Scan(&releases); err != nil || releases != 1 {
		t.Fatalf("release jobs = %d (%v), want 1: a finished build hands over to the scheduler", releases, err)
	}
	if sb.got.GoVersion != "1.24" || len(sb.got.Targets) != 2 || sb.got.Targets[0].Path != "./cmd/api" {
		t.Fatalf("sandbox spec = %+v", sb.got)
	}
	arts, _ := q.ListArtifacts(ctx, d.ID)
	if len(arts) != 2 || arts[0].ServiceName != "api" || arts[0].ServiceType != "web" || arts[1].ServiceType != "worker" {
		t.Fatalf("artifacts = %+v", arts)
	}
	bin, err := s3.Get(ctx, arts[0].ObjectKey, 1<<20)
	if err != nil || string(bin) != "\x7fELF api" {
		t.Fatalf("stored binary = %q, %v", bin, err)
	}
	if log := logText(t, pool, d.ID); !strings.Contains(log, "go: downloading github.com/go-chi") || !strings.Contains(log, "built api") {
		t.Fatalf("log:\n%s", log)
	}

	// Delivered again (at least once): nothing changes.
	if err := b.Handle(ctx, job); err != nil {
		t.Fatal(err)
	}
	if again, _ := q.ListArtifacts(ctx, d.ID); len(again) != 2 {
		t.Fatalf("second delivery changed artifacts: %d", len(again))
	}
}

func TestBuildFailsCleanly(t *testing.T) {
	ctx := context.Background()
	s3 := storage(t)
	pool := testdb.New(t)
	d, job := queued(t, pool, s3, sample)
	b := newBuilder(pool, s3, &fakeSandbox{err: &exec.ExitError{}})

	if err := b.Handle(ctx, job); err != nil {
		t.Fatalf("a broken build is not a job error: %v", err)
	}
	got, _ := store.New(pool).GetDeployment(ctx, d.ID)
	if got.Status != "failed" || got.Error == nil || !strings.Contains(*got.Error, "go build failed") {
		t.Fatalf("deployment = %s %v", got.Status, got.Error)
	}
	if log := logText(t, pool, d.ID); !strings.Contains(log, "syntax error") || !strings.Contains(log, "build failed") || !strings.Contains(log, "nothing changed: this project has no live version yet") {
		t.Fatalf("log:\n%s", log)
	}
	if arts, _ := store.New(pool).ListArtifacts(ctx, d.ID); len(arts) != 0 {
		t.Fatalf("failed build left artifacts: %d", len(arts))
	}
}

func TestInternalErrorRetriesThenFails(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	s3 := storage(t)
	d, job := queued(t, pool, s3, sample)
	broken := *s3
	broken.Bucket = "no-such-bucket"
	b := newBuilder(pool, &broken, &fakeSandbox{})

	if err := b.Handle(ctx, job); err == nil {
		t.Fatal("storage outage should be returned so the job retries")
	}
	got, _ := store.New(pool).GetDeployment(ctx, d.ID)
	if got.Status != "building" {
		t.Fatalf("status after a retryable error = %s, want building", got.Status)
	}
	job.Attempts = job.MaxAttempts
	if err := b.Handle(ctx, job); err == nil {
		t.Fatal("want the error returned on the last attempt too")
	}
	got, _ = store.New(pool).GetDeployment(ctx, d.ID)
	if got.Status != "failed" {
		t.Fatalf("status after the last attempt = %s, want failed", got.Status)
	}
}

func TestBuildRejectsBadConfig(t *testing.T) {
	ctx := context.Background()
	s3 := storage(t)
	pool := testdb.New(t)
	files := map[string]string{"go.mod": "module x\n", "main.go": "package main\n\nfunc main() {}\n", "greyship.toml": "unknown_key = 1\n"}
	d, job := queued(t, pool, s3, files)
	if err := newBuilder(pool, s3, &fakeSandbox{}).Handle(ctx, job); err != nil {
		t.Fatal(err)
	}
	got, _ := store.New(pool).GetDeployment(ctx, d.ID)
	if got.Status != "failed" || got.Error == nil || !strings.Contains(*got.Error, "unknown_key") {
		t.Fatalf("deployment = %s %v", got.Status, got.Error)
	}
}

// greyship.toml's files are bundled from the uploaded source and stored next to the binaries.
func TestBuildBundlesFiles(t *testing.T) {
	ctx := context.Background()
	s3 := storage(t)
	pool := testdb.New(t)
	files := map[string]string{"greyship.toml": "files = [\"templates/\"]\n", "templates/home.html": "<h1>hi</h1>"}
	for k, v := range sample {
		files[k] = v
	}
	d, job := queued(t, pool, s3, files)
	sb := &fakeSandbox{bins: map[string][]byte{"api": []byte("\x7fELF api"), "worker": []byte("\x7fELF worker")}}
	if err := newBuilder(pool, s3, sb).Handle(ctx, job); err != nil {
		t.Fatal(err)
	}
	got, _ := store.New(pool).GetDeployment(ctx, d.ID)
	if got.Status != "built" || got.FilesKey == nil || got.FilesSha256 == nil || !strings.HasPrefix(*got.FilesKey, "files/"+d.ProjectID+"/") {
		t.Fatalf("deployment = %s, files %v\n%s", got.Status, got.FilesKey, logText(t, pool, d.ID))
	}
	bundle, err := s3.Get(ctx, *got.FilesKey, 1<<20)
	if sum := sha256.Sum256(bundle); err != nil || hex.EncodeToString(sum[:]) != *got.FilesSha256 || int64(len(bundle)) != *got.FilesSize {
		t.Fatalf("stored bundle doesn't match the deployment: %v", err)
	}
	if log := logText(t, pool, d.ID); !strings.Contains(log, "files: templates/ (1 file)") || strings.Contains(log, "warning:") {
		t.Fatalf("log:\n%s", log)
	}

	files["greyship.toml"] = "files = [\"static/\"]\n"
	pool2 := testdb.New(t)
	bad, job := queued(t, pool2, s3, files)
	if err := newBuilder(pool2, s3, sb).Handle(ctx, job); err != nil {
		t.Fatal(err)
	}
	failed, _ := store.New(pool2).GetDeployment(ctx, bad.ID)
	if failed.Status != "failed" || failed.Error == nil || !strings.Contains(*failed.Error, `"static/"`) {
		t.Fatalf("deployment = %s %v; a missing files entry must fail the build", failed.Status, failed.Error)
	}
}

func ptr[T any](v T) *T { return &v }
