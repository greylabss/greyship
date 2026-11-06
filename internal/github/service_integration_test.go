//go:build integration

package github_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/bus"
	"greyship.tech/internal/deploy"
	"greyship.tech/internal/github"
	"greyship.tech/internal/objstore"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
	"greyship.tech/internal/testdb"
)

const sha1 = "1111111111111111111111111111111111111111"

// fakeGitHub plays github.com for one user (token u_tok) who can access installation 7 (haki/temp). Installation 8
// (evil/repo) belongs to someone else.
type fakeGitHub struct {
	mu       sync.Mutex
	statuses []string
	pubHead  string // the public repo haki/pub's main branch
}

func (f *fakeGitHub) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "good-code" {
			_, _ = w.Write([]byte(`{"error":"bad_verification_code"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"u_tok"}`))
	})
	mux.HandleFunc("GET /user/installations", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer u_tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"installations":[{"id":7,"account":{"login":"haki"}}]}`))
	})
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_" + r.PathValue("id"), "expires_at": time.Now().Add(time.Hour)})
	})
	mux.HandleFunc("GET /installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "token ghs_7":
			_, _ = w.Write([]byte(`{"repositories":[{"id":100,"full_name":"haki/temp","default_branch":"main"}]}`))
		case "token ghs_8":
			_, _ = w.Write([]byte(`{"repositories":[{"id":200,"full_name":"evil/repo","default_branch":"main"}]}`))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	})
	mux.HandleFunc("GET /repos/haki/temp/branches/main", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"commit":{"sha":"` + sha1 + `"}}`))
	})
	mux.HandleFunc("GET /repos/haki/temp/tarball/{sha}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive(t))
	})
	mux.HandleFunc("POST /repos/haki/temp/statuses/{sha}", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.statuses = append(f.statuses, body["state"]+" "+body["target_url"])
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("GET /haki/pub.git/info/refs", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		head := f.pubHead
		f.mu.Unlock()
		line := func(s string) string { return fmt.Sprintf("%04x%s", len(s)+4, s) }
		_, _ = w.Write([]byte(line("# service=git-upload-pack\n") + "0000" + line(head+" HEAD\x00symref=HEAD:refs/heads/main\n") + line(head+" refs/heads/main\n") + "0000"))
	})
	mux.HandleFunc("GET /haki/secret.git/info/refs", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("GET /codeload/haki/pub/tar.gz/{sha}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive(t))
	})
	return httptest.NewServer(mux)
}

func archive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{"go.mod": "module temp\n\ngo 1.24\n", "main.go": "package main\n\nfunc main() {}\n", ".env": "SECRET=1", ".env.example": "DATABASE_URL=\nSTRIPE_KEY=sk_test_x\n"} {
		_ = tw.WriteHeader(&tar.Header{Name: "haki-temp-1111111/" + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestGitHubDeployFlow(t *testing.T) {
	ctx := context.Background()
	endpoint := os.Getenv("GREYSHIP_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("GREYSHIP_TEST_S3_ENDPOINT not set; run make test-int")
	}
	s3 := &objstore.Store{
		Endpoint: endpoint, Region: os.Getenv("GREYSHIP_TEST_S3_REGION"), Bucket: "greyship-test",
		AccessKey: os.Getenv("GREYSHIP_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("GREYSHIP_TEST_S3_SECRET_KEY"),
		HTTP: &http.Client{Timeout: 10 * time.Second}, Now: time.Now,
	}
	pool := testdb.New(t)
	q := store.New(pool)
	fg := &fakeGitHub{}
	srv := fg.server(t)
	defer srv.Close()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	app := &github.App{ID: 1, Slug: "greyship", ClientID: "c", ClientSecret: "s", Key: key, API: srv.URL, Web: srv.URL, HTTP: srv.Client(), Now: time.Now}
	projects := project.NewService(pool)
	logger := slog.New(slog.DiscardHandler)
	signer := auth.Signer{Key: []byte(strings.Repeat("k", 32)), Now: time.Now}
	svc := &github.Service{Pool: pool, GitHub: app, Projects: projects, Signer: signer, Logger: logger}

	team, _ := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "haki", Name: "haki"})
	u, _ := q.CreateUser(ctx, store.CreateUserParams{Email: "haki@example.com"})
	_ = q.AddMember(ctx, store.AddMemberParams{TeamID: team.ID, UserID: u.ID, Role: "owner"})
	owner := project.Caller{UserID: u.ID}
	other, _ := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "evil", Name: "evil"})
	_ = q.AddInstallation(ctx, store.AddInstallationParams{ID: 8, TeamID: other.ID, AccountLogin: "evil"})

	link, err := svc.InstallURL(ctx, owner, "haki")
	if err != nil {
		t.Fatal(err)
	}
	state := link[strings.Index(link, "state=")+len("state="):]
	state = strings.ReplaceAll(state, "%3D", "=")
	if _, err := svc.Callback(ctx, owner, state, "good-code", 8); !errors.Is(err, github.ErrNoAccess) {
		t.Fatalf("claiming someone else's installation = %v, want ErrNoAccess", err)
	}
	if _, err := svc.Callback(ctx, project.Caller{UserID: other.ID}, state, "good-code", 7); !errors.Is(err, github.ErrBadState) {
		t.Fatalf("another person using this state = %v, want ErrBadState", err)
	}
	if slug, err := svc.Callback(ctx, owner, state, "good-code", 7); err != nil || slug != "haki" {
		t.Fatalf("own installation: %q %v", slug, err)
	}

	if _, _, err := svc.Import(ctx, owner, "haki", "evil/repo", "", true); !errors.Is(err, github.ErrUnknownRepo) {
		t.Fatalf("importing another team's repo = %v, want ErrUnknownRepo", err)
	}
	p, dep, err := svc.Import(ctx, owner, "haki", "haki/temp", "", true)
	if err != nil || p.Slug != "temp" || dep == "" {
		t.Fatalf("import: %+v %q %v", p, dep, err)
	}

	// The builder fetches the commit, repacks it without .env and queues the build.
	var jobID int64
	var payload []byte
	if err := pool.QueryRow(ctx, "SELECT id, payload FROM jobs WHERE kind = $1", github.FetchJob).Scan(&jobID, &payload); err != nil {
		t.Fatal(err)
	}
	fetcher := &github.Fetcher{Pool: pool, App: app, Storage: s3, Deploy: deploy.NewService(pool, projects, s3, logger, time.Now), Logger: logger}
	if err := fetcher.Handle(ctx, bus.Job{ID: jobID, Kind: github.FetchJob, Payload: payload, Attempts: 1, MaxAttempts: 5}); err != nil {
		t.Fatal(err)
	}
	d, _ := q.GetDeployment(ctx, dep)
	if d.Status != "queued" || d.SourceKey == nil || *d.CommitSha != sha1 || d.Branch == nil || *d.Branch != "main" {
		t.Fatalf("after fetch: %+v", d)
	}
	src, err := s3.Get(ctx, *d.SourceKey, 1<<20)
	if err != nil || bytes.Contains(src, []byte("SECRET=1")) {
		t.Fatalf("stored source (%v) must not contain the repo's .env", err)
	}
	var builds int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind = 'build.requested' AND payload->>'deployment_id' = $1", dep).Scan(&builds)
	if builds != 1 {
		t.Fatalf("build jobs = %d, want 1", builds)
	}

	// A repeated push of the same commit deploys nothing; a new commit deploys once; the wrong installation, never.
	push := func(sha string, installation int64) int {
		body := fmt.Sprintf(`{"ref":"refs/heads/main","after":%q,"repository":{"id":100},"installation":{"id":%d}}`, sha, installation)
		n, err := svc.Push(ctx, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := push(sha1, 7); n != 0 {
		t.Fatalf("repeated delivery deployed %d", n)
	}
	if n := push(strings.Repeat("2", 40), 8); n != 0 {
		t.Fatal("a push signed for another installation must not deploy this project")
	}
	if n := push(strings.Repeat("2", 40), 7); n != 1 {
		t.Fatalf("new commit deployed %d, want 1", n)
	}

	// Commit statuses follow the deployment: pending, then success with a link to it.
	statuses := &github.Statuses{Pool: pool, App: app, Dashboard: "https://greyship.tech", Logger: logger}
	report := func() {
		cctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer cancel()
		statuses.Run(cctx, 200*time.Millisecond)
	}
	report()
	if _, err := pool.Exec(ctx, "UPDATE deployments SET status = 'ready' WHERE id = $1", dep); err != nil {
		t.Fatal(err)
	}
	report()
	fg.mu.Lock()
	got := strings.Join(fg.statuses, "\n")
	fg.mu.Unlock()
	want := "success https://greyship.tech/haki/temp/deployments/" + dep
	if !strings.Contains(got, "pending https://greyship.tech/haki/temp/deployments/"+dep) || !strings.Contains(got, want) {
		t.Fatalf("statuses:\n%s", got)
	}
	if strings.Count(got, want) != 1 {
		t.Fatalf("success reported more than once:\n%s", got)
	}
}

// A public repository pasted by URL deploys without the app, never on push, and again only on demand.
func TestPublicRepoFlow(t *testing.T) {
	ctx := context.Background()
	endpoint := os.Getenv("GREYSHIP_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("GREYSHIP_TEST_S3_ENDPOINT not set; run make test-int")
	}
	s3 := &objstore.Store{
		Endpoint: endpoint, Region: os.Getenv("GREYSHIP_TEST_S3_REGION"), Bucket: "greyship-test",
		AccessKey: os.Getenv("GREYSHIP_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("GREYSHIP_TEST_S3_SECRET_KEY"),
		HTTP: &http.Client{Timeout: 10 * time.Second}, Now: time.Now,
	}
	pool := testdb.New(t)
	q := store.New(pool)
	fg := &fakeGitHub{pubHead: sha1}
	srv := fg.server(t)
	defer srv.Close()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	app := &github.App{ID: 1, Slug: "greyship", Key: key, API: srv.URL, Web: srv.URL, Codeload: srv.URL + "/codeload", HTTP: srv.Client(), Now: time.Now}
	projects := project.NewService(pool)
	logger := slog.New(slog.DiscardHandler)
	svc := &github.Service{Pool: pool, GitHub: app, Projects: projects, Signer: auth.Signer{Key: []byte(strings.Repeat("k", 32)), Now: time.Now}, Logger: logger}

	team, _ := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "haki", Name: "haki"})
	u, _ := q.CreateUser(ctx, store.CreateUserParams{Email: "haki@example.com"})
	_ = q.AddMember(ctx, store.AddMemberParams{TeamID: team.ID, UserID: u.ID, Role: "owner"})
	owner := project.Caller{UserID: u.ID}

	// The dashboard imports without deploying and shows a preview first.
	quiet, none, err := svc.ImportPublic(ctx, owner, "haki", "https://github.com/haki/pub", "", false)
	if err != nil || none != "" {
		t.Fatalf("import without deploy: %q %v", none, err)
	}
	pv, err := svc.Preview(ctx, owner, quiet.ID)
	if err != nil || pv.Commit != sha1 || len(pv.Detected.Services) != 1 || strings.Join(pv.EnvExample, ",") != "DATABASE_URL,STRIPE_KEY" {
		t.Fatalf("preview: %+v %v", pv, err)
	}
	var deps int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM deployments WHERE project_id = $1", quiet.ID).Scan(&deps)
	if deps != 0 {
		t.Fatal("importing without deploy must not create a deployment")
	}

	if _, _, err := svc.ImportPublic(ctx, owner, "haki", "https://github.com/haki/secret", "", true); !errors.Is(err, github.ErrNotPublic) {
		t.Fatalf("private repo = %v, want ErrNotPublic", err)
	}
	p, dep, err := svc.ImportPublic(ctx, owner, "haki", "https://github.com/haki/pub", "", true)
	if err != nil || p.Slug != "pub-2" || dep == "" {
		t.Fatalf("import: %+v %q %v", p, dep, err)
	}
	repo, _ := q.ProjectRepo(ctx, p.ID)
	if repo.InstallationID != nil || repo.Branch != "main" {
		t.Fatalf("connection %+v, want a public repo on main", repo)
	}

	var jobID int64
	var payload []byte
	if err := pool.QueryRow(ctx, "SELECT id, payload FROM jobs WHERE kind = $1", github.FetchJob).Scan(&jobID, &payload); err != nil {
		t.Fatal(err)
	}
	fetcher := &github.Fetcher{Pool: pool, App: app, Storage: s3, Deploy: deploy.NewService(pool, projects, s3, logger, time.Now), Logger: logger}
	if err := fetcher.Handle(ctx, bus.Job{ID: jobID, Kind: github.FetchJob, Payload: payload, Attempts: 1, MaxAttempts: 5}); err != nil {
		t.Fatal(err)
	}
	if d, _ := q.GetDeployment(ctx, dep); d.Status != "queued" || d.SourceKey == nil {
		t.Fatalf("after fetch: %+v", d)
	}

	// Pushes to a public repo never deploy: only the app's webhooks speak for a repository.
	if n, err := svc.Push(ctx, []byte(`{"ref":"refs/heads/main","after":"`+strings.Repeat("3", 40)+`","repository":{"id":0},"installation":{"id":0}}`)); err != nil || n != 0 {
		t.Fatalf("push to a public repo deployed %d (%v)", n, err)
	}

	if _, err := svc.DeployLatest(ctx, owner, p.ID); !errors.Is(err, github.ErrAlreadyDeployed) {
		t.Fatalf("deploying the commit already deploying = %v, want ErrAlreadyDeployed", err)
	}
	// After a failed deploy (say env vars were missing), the same commit may be deployed again.
	if _, err := pool.Exec(ctx, "UPDATE deployments SET status = 'failed' WHERE id = $1", dep); err != nil {
		t.Fatal(err)
	}
	again, err := svc.DeployLatest(ctx, owner, p.ID)
	if err != nil || again == "" || again == dep {
		t.Fatalf("retry after failure: %q %v", again, err)
	}
	fg.mu.Lock()
	fg.pubHead = strings.Repeat("4", 40)
	fg.mu.Unlock()
	latest, err := svc.DeployLatest(ctx, owner, p.ID)
	if err != nil || latest == "" {
		t.Fatalf("new commit: %q %v", latest, err)
	}
	if d, _ := q.GetDeployment(ctx, latest); *d.CommitSha != strings.Repeat("4", 40) {
		t.Fatalf("deployed %s, want the new head", *d.CommitSha)
	}
}
