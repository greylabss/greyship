//go:build integration

package fly_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/build"
	"greyship.tech/internal/build/fly"
	"greyship.tech/internal/objstore"
	"greyship.tech/internal/store"
	"greyship.tech/internal/testdb"
)

// fakeFly answers the Machines API and plays the build machine: when a machine starts, it uploads a binary and
// reports through the same Reports service the API uses.
type fakeFly struct {
	t       *testing.T
	reports *fly.Reports
	s3      *objstore.Store
	fail    bool // the code doesn't compile
	silent  bool // the machine never reports

	mu        sync.Mutex
	calls     []string
	state     map[string]string
	machines  int
	destroyed []string
}

func (f *fakeFly) server() *httptest.Server {
	mux := http.NewServeMux()
	record := func(r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
	}
	mux.HandleFunc("POST /v1/apps/greyship-build/volumes", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write([]byte(`{"id":"vol_1"}`))
	})
	mux.HandleFunc("POST /v1/apps/greyship-build/machines", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		var body struct{ Config fly.MachineConfig }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.machines++
		id := "m" + string(rune('0'+f.machines))
		f.state[id] = "started"
		f.mu.Unlock()
		go f.play(id, body.Config)
		_ = json.NewEncoder(w).Encode(fly.Machine{ID: id, State: "started"})
	})
	mux.HandleFunc("POST /v1/apps/greyship-build/machines/{id}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		var body struct{ Config fly.MachineConfig }
		_ = json.NewDecoder(r.Body).Decode(&body)
		id := r.PathValue("id")
		f.mu.Lock()
		f.state[id] = "started"
		f.mu.Unlock()
		go f.play(id, body.Config)
		_ = json.NewEncoder(w).Encode(fly.Machine{ID: id, State: "started"})
	})
	mux.HandleFunc("POST /v1/apps/greyship-build/machines/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		f.mu.Lock()
		f.state[r.PathValue("id")] = "stopped"
		f.mu.Unlock()
	})
	mux.HandleFunc("DELETE /v1/apps/greyship-build/machines/{id}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		f.mu.Lock()
		f.destroyed = append(f.destroyed, r.PathValue("id"))
		f.mu.Unlock()
	})
	mux.HandleFunc("GET /v1/apps/greyship-build/machines/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		st := f.state[r.PathValue("id")]
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(fly.Machine{ID: r.PathValue("id"), State: st})
	})
	return httptest.NewServer(mux)
}

func (f *fakeFly) play(id string, cfg fly.MachineConfig) {
	ctx := context.Background()
	var job fly.MachineJob
	if err := json.Unmarshal([]byte(cfg.Env["GREYSHIP_BUILD_JOB"]), &job); err != nil {
		f.t.Errorf("machine got no job: %v", err)
		return
	}
	if strings.Contains(cfg.Env["GREYSHIP_BUILD_JOB"], "SECRET") {
		f.t.Error("a secret reached the build machine")
	}
	if f.silent {
		f.mu.Lock()
		f.state[id] = "stopped"
		f.mu.Unlock()
		return
	}
	_ = f.reports.Add(ctx, job.Token, job.Deployment, fly.Report{Lines: []fly.Line{{Stream: "build", Text: "go: downloading gin"}}})
	outcome := fly.Outcome{OK: true, Seconds: 12.4, Binaries: map[string]fly.Binary{}}
	if f.fail {
		outcome = fly.Outcome{ExitCode: 1, Error: "go build ./cmd/api failed", Seconds: 3}
	} else {
		bin := []byte("\x7fELF api")
		_ = f.s3.Put(ctx, fly.OutputKey(job.Deployment, "api"), bytes.NewReader(bin), int64(len(bin)))
		outcome.Binaries["api"] = fly.Binary{SHA256: "ab", Size: int64(len(bin))}
	}
	_ = f.reports.Add(ctx, job.Token, job.Deployment, fly.Report{Outcome: &outcome})
	f.mu.Lock()
	f.state[id] = "stopped"
	f.mu.Unlock()
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

// building makes a team on plan and a deployment in status building, as the builder would.
func building(t *testing.T, pool *pgxpool.Pool, teamPlan string) (store.Deployment, string) {
	t.Helper()
	ctx := context.Background()
	q := store.New(pool)
	slug := teamPlan + "-" + newID(t, pool)[:8]
	team, err := q.CreateTeam(ctx, store.CreateTeamParams{Slug: slug, Name: slug})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE teams SET plan = $2 WHERE id = $1", team.ID, teamPlan); err != nil {
		t.Fatal(err)
	}
	p, err := q.CreateProject(ctx, store.CreateProjectParams{TeamID: team.ID, Name: "shop", Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	key, size, sha := "sources/x.tar.gz", int64(1), strings.Repeat("a", 64)
	d, err := q.CreateDeployment(ctx, store.CreateDeploymentParams{ID: newID(t, pool), ProjectID: p.ID, Source: "cli", SourceKey: &key, SourceSize: &size, SourceSha256: &sha})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE deployments SET status = 'building' WHERE id = $1", d.ID); err != nil {
		t.Fatal(err)
	}
	return d, team.ID
}

func newID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), "SELECT gen_random_uuid()::text").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFlyRunner(t *testing.T) {
	ctx := context.Background()
	s3 := storage(t)
	pool := testdb.New(t)
	signer := auth.Signer{Key: []byte(strings.Repeat("k", 32)), Now: time.Now}
	ff := &fakeFly{t: t, reports: &fly.Reports{Pool: pool, Storage: s3, Signer: signer}, s3: s3, state: map[string]string{}}
	srv := ff.server()
	defer srv.Close()
	runner := &fly.Runner{
		Fly:  &fly.Client{Base: srv.URL + "/v1", App: "greyship-build", Token: "fly-token", Region: "fra", HTTP: srv.Client()},
		Pool: pool, Storage: s3, Signer: signer, APIURL: "https://api.test", ImagePrefix: "registry.fly.io/greyship-build",
		MaxMachines: 3, VolumeGB: 10, Poll: 20 * time.Millisecond, Logger: slog.New(slog.DiscardHandler), Now: time.Now,
	}
	jobFor := func(d store.Deployment, team string) build.Job {
		return build.Job{
			Spec:       build.Spec{ID: d.ID, GoVersion: "1.27.3", GOARCH: "amd64", TeamID: team, Targets: []build.Target{{Name: "api", Path: "./cmd/api"}}},
			SourceKey:  *d.SourceKey,
			OutputKeys: map[string]string{"api": fly.OutputKey(d.ID, "api")},
		}
	}
	var lines []string
	logLine := func(_, line string) { lines = append(lines, line) }

	pro, proTeam := building(t, pool, "pro")
	res, err := runner.Run(ctx, jobFor(pro, proTeam), logLine)
	if err != nil {
		t.Fatal(err)
	}
	if res.Runner != "fly" || res.MachineSize != "performance-4x" || res.Binaries["api"].Size != 8 || !strings.Contains(strings.Join(lines, "\n"), "go: downloading gin") {
		t.Fatalf("result %+v, log %v", res, lines)
	}
	if ff.calls[0] != "POST /v1/apps/greyship-build/volumes" {
		t.Fatalf("a paid team's first build must create its cache volume: %v", ff.calls)
	}

	// The same team's next build reuses its machine and volume instead of making new ones.
	pro2 := pro
	pro2.ID = newID(t, pool)
	if _, err := pool.Exec(ctx, `INSERT INTO deployments (id, project_id, source, status, source_key, source_size, source_sha256)
		SELECT $1, project_id, source, 'building', source_key, source_size, source_sha256 FROM deployments WHERE id = $2`, pro2.ID, pro.ID); err != nil {
		t.Fatal(err)
	}
	before := ff.machines
	if _, err := runner.Run(ctx, jobFor(pro2, proTeam), logLine); err != nil {
		t.Fatal(err)
	}
	if ff.machines != before {
		t.Fatal("the team's stopped machine should have been reused")
	}

	// Free builds start cold on a machine that is destroyed afterwards, and never share a volume.
	free, freeTeam := building(t, pool, "free")
	res, err = runner.Run(ctx, jobFor(free, freeTeam), logLine)
	if err != nil || res.MachineSize != "performance-2x" {
		t.Fatalf("free build: %+v %v", res, err)
	}
	if len(ff.destroyed) != 1 {
		t.Fatalf("the free build machine must be destroyed: %v", ff.destroyed)
	}

	// Code that doesn't compile is the user's failure, not ours to retry.
	ff.fail = true
	bad, badTeam := building(t, pool, "team")
	_, err = runner.Run(ctx, jobFor(bad, badTeam), logLine)
	var failed *build.Failed
	if !errors.As(err, &failed) || failed.Code != 1 {
		t.Fatalf("err = %v, want build.Failed", err)
	}
	ff.fail = false

	// A machine that stops without reporting is our failure: the job retries.
	ff.silent = true
	lost, lostTeam := building(t, pool, "team")
	if _, err := runner.Run(ctx, jobFor(lost, lostTeam), logLine); err == nil || errors.As(err, &failed) {
		t.Fatalf("err = %v, want an internal error", err)
	}

	var busy int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM build_machines WHERE status = 'busy'").Scan(&busy)
	if busy != 0 {
		t.Fatalf("%d build machines left busy", busy)
	}
}
