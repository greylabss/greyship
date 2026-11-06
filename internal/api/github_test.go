package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"greyship.tech/internal/detect"
	"greyship.tech/internal/github"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
)

type fakeGitHub struct {
	pushes    int
	callbacks int
	err       error
}

func (f *fakeGitHub) InstallURL(context.Context, project.Caller, string) (string, error) {
	return "https://github.com/apps/greyship/installations/new?state=s", f.err
}

func (f *fakeGitHub) Callback(context.Context, project.Caller, string, string, int64) (string, error) {
	f.callbacks++
	return "haki", f.err
}

func (f *fakeGitHub) Repos(context.Context, project.Caller, string) ([]github.TeamRepo, error) {
	return []github.TeamRepo{{Installation: 1, Repo: github.Repo{FullName: "haki/temp", DefaultBranch: "main"}}}, f.err
}

func (f *fakeGitHub) Import(_ context.Context, _ project.Caller, _, _, _ string, deploy bool) (project.Project, string, error) {
	dep := ""
	if deploy {
		dep = "d1"
	}
	return project.Project{Project: store.Project{ID: "p1", Slug: "temp"}, TeamSlug: "haki"}, dep, f.err
}

func (f *fakeGitHub) Preview(context.Context, project.Caller, string) (github.Preview, error) {
	if f.err != nil {
		return github.Preview{Branch: "main"}, f.err
	}
	return github.Preview{Branch: "main", Commit: "abc", EnvExample: []string{"DATABASE_URL"}, Detected: detect.Result{
		GoVersion: "1.25", Services: []detect.Service{{Name: "api", Type: "web", Path: "./cmd/api", Reason: "starts a server"}},
	}}, nil
}

func (f *fakeGitHub) Connect(context.Context, project.Caller, string, string, string) (store.ProjectRepo, error) {
	return store.ProjectRepo{FullName: "haki/temp", Branch: "main"}, f.err
}

func (f *fakeGitHub) Repo(context.Context, project.Caller, string) (store.ProjectRepo, error) {
	return store.ProjectRepo{}, github.ErrNotFound
}

func (f *fakeGitHub) ImportPublic(context.Context, project.Caller, string, string, string, bool) (project.Project, string, error) {
	return project.Project{Project: store.Project{ID: "p2", Slug: "temp"}, TeamSlug: "haki"}, "d2", f.err
}

func (f *fakeGitHub) DeployLatest(context.Context, project.Caller, string) (string, error) {
	return "d3", f.err
}

func (f *fakeGitHub) Push(context.Context, []byte) (int, error) {
	f.pushes++
	return 1, nil
}

// githubServer is a test server with GitHub deploys on; their routes only exist when the app is configured.
func githubServer(fa *fakeAuth, gh *fakeGitHub) *Server {
	o := newTestServer(fa).Options
	o.GitHub, o.WebhookSecret = gh, "whsec"
	return New(o)
}

func sign(secret, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestGitHubWebhook(t *testing.T) {
	body := `{"ref":"refs/heads/main"}`
	tests := []struct {
		name, event, signature string
		want, pushes           int
	}{
		{"push", "push", sign("whsec", body), http.StatusAccepted, 1},
		{"forged", "push", sign("guess", body), http.StatusUnauthorized, 0},
		{"unsigned", "push", "", http.StatusUnauthorized, 0},
		{"ping", "ping", sign("whsec", body), http.StatusNoContent, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := &fakeGitHub{}
			s := githubServer(&fakeAuth{}, gh)
			req := httptest.NewRequest("POST", "/v1/github/webhook", strings.NewReader(body))
			req.Header.Set("X-GitHub-Event", tt.event)
			req.Header.Set("X-Hub-Signature-256", tt.signature)
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != tt.want || gh.pushes != tt.pushes {
				t.Fatalf("webhook = %d (pushes %d), want %d (%d)", rec.Code, gh.pushes, tt.want, tt.pushes)
			}
		})
	}
}

func TestGitHubCallback(t *testing.T) {
	gh := &fakeGitHub{}
	s := githubServer(&fakeAuth{sessions: map[string]store.User{"good": {ID: "u1"}}}, gh)

	req := httptest.NewRequest("GET", "/v1/github/callback?installation_id=7&code=c&state=s", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "http://dash.test/signin?next=") || gh.callbacks != 0 {
		t.Fatalf("signed out: %d %s", rec.Code, rec.Header().Get("Location"))
	}

	req = httptest.NewRequest("GET", "/v1/github/callback?installation_id=7&code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "good"})
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "http://dash.test/haki/new" {
		t.Fatalf("signed in: %d %s", rec.Code, rec.Header().Get("Location"))
	}

	gh.err = github.ErrNoAccess
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if !strings.Contains(rec.Header().Get("Location"), "github_error=no_access") {
		t.Fatalf("someone else's installation: %s", rec.Header().Get("Location"))
	}
}

func TestPublicRepoEndpoints(t *testing.T) {
	tests := []struct {
		name, path, body string
		err              error
		want             int
		code             string
	}{
		{"import url", "/v1/teams/haki/github/import-url", `{"url":"https://github.com/haki/temp"}`, nil, http.StatusCreated, `"deployment_id":"d2"`},
		{"private repo", "/v1/teams/haki/github/import-url", `{"url":"https://github.com/haki/secret"}`, github.ErrNotPublic, http.StatusBadRequest, "not_public"},
		{"not github", "/v1/teams/haki/github/import-url", `{"url":"https://gitlab.com/a/b"}`, github.ErrBadURL, http.StatusBadRequest, "bad_url"},
		{"deploy latest", "/v1/projects/p1/github/deploy", ``, nil, http.StatusCreated, `"deployment_id":"d3"`},
		{"already live", "/v1/projects/p1/github/deploy", ``, github.ErrAlreadyDeployed, http.StatusConflict, "already_deployed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := githubServer(&fakeAuth{tokens: map[string]store.User{"cli": {ID: "u1"}}}, &fakeGitHub{err: tt.err})
			req := httptest.NewRequest("POST", tt.path, strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer cli")
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != tt.want || !strings.Contains(rec.Body.String(), tt.code) {
				t.Fatalf("%s = %d %s, want %d %s", tt.path, rec.Code, rec.Body, tt.want, tt.code)
			}
		})
	}
}

func TestImportWithoutDeployAndPreview(t *testing.T) {
	auth := &fakeAuth{tokens: map[string]store.User{"cli": {ID: "u1"}}}
	call := func(s *Server, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer cli")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}
	s := githubServer(auth, &fakeGitHub{})
	if rec := call(s, "POST", "/v1/teams/haki/github/import", `{"repo":"haki/temp","deploy":false}`); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"deployment_id":""`) {
		t.Fatalf("import without deploy = %d %s", rec.Code, rec.Body)
	}
	if rec := call(s, "POST", "/v1/teams/haki/github/import", `{"repo":"haki/temp"}`); !strings.Contains(rec.Body.String(), `"deployment_id":"d1"`) {
		t.Fatalf("import without the field must deploy, as the CLI expects: %s", rec.Body)
	}
	rec := call(s, "GET", "/v1/projects/p1/github/preview", "")
	for _, want := range []string{`"env_example":["DATABASE_URL"]`, `"name":"api","type":"web","path":"./cmd/api"`, `"go_version":"1.25"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("preview missing %s: %s", want, rec.Body)
		}
	}
	bad := githubServer(auth, &fakeGitHub{err: &detect.ConfigError{Msg: "no services found", Fix: "add a package main"}})
	if rec := call(bad, "GET", "/v1/projects/p1/github/preview", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"problem":{"fix":"add a package main","message":"no services found"}`) {
		t.Fatalf("detection problem = %d %s", rec.Code, rec.Body)
	}
}

func TestGitHubEndpointErrors(t *testing.T) {
	s := githubServer(&fakeAuth{tokens: map[string]store.User{"cli": {ID: "u1"}}}, &fakeGitHub{err: github.ErrUnknownRepo})
	req := httptest.NewRequest("POST", "/v1/teams/haki/github/import", strings.NewReader(`{"repo":"other/repo"}`))
	req.Header.Set("Authorization", "Bearer cli")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "unknown_repo") {
		t.Fatalf("import of an unshared repo = %d %s", rec.Code, rec.Body)
	}
}
