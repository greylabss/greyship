package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"greyship.tech/internal/deploy"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
)

type fakeDeployments struct {
	err    error
	caller project.Caller
	src    deploy.Source
	bins   []deploy.Binary
	files  *deploy.Files
	logs   []logPage // returned one per Logs call; the last repeats
	calls  int
}

type logPage struct {
	status string
	lines  []string
}

func (f *fakeDeployments) one(c project.Caller) (deploy.Deployment, error) {
	f.caller = c
	if f.err != nil {
		return deploy.Deployment{}, f.err
	}
	return deploy.Deployment{
		Deployment: store.Deployment{ID: "d1", ProjectID: "p1", Source: "cli", Status: "uploading", Env: "production", BuildMode: "remote", CreatedAt: time.Unix(0, 0)},
		Project:    project.Project{Project: store.Project{ID: "p1", Slug: "shop"}, TeamSlug: "haki"},
	}, nil
}

func (f *fakeDeployments) Create(_ context.Context, c project.Caller, _ string, src deploy.Source) (deploy.Upload, error) {
	f.src = src
	d, err := f.one(c)
	return deploy.Upload{Deployment: d, URL: "http://storage/x?sig", ExpiresAt: time.Unix(900, 0)}, err
}

func (f *fakeDeployments) CreateLocal(_ context.Context, c project.Caller, _, _ string, bins []deploy.Binary, files *deploy.Files) (deploy.LocalUpload, error) {
	f.bins, f.files = bins, files
	d, err := f.one(c)
	d.BuildMode = "local"
	up := deploy.LocalUpload{Deployment: d, Uploads: []deploy.BinaryUpload{{Service: "api", URL: "http://storage/b?sig"}}}
	if files != nil {
		up.FilesURL = "http://storage/f?sig"
	}
	return up, err
}

func (f *fakeDeployments) Start(_ context.Context, c project.Caller, _ string) (deploy.Deployment, error) {
	return f.one(c)
}

func (f *fakeDeployments) Get(_ context.Context, c project.Caller, _ string) (deploy.Deployment, error) {
	return f.one(c)
}

func (f *fakeDeployments) List(_ context.Context, c project.Caller, _ string, _ int32) ([]deploy.Deployment, error) {
	d, err := f.one(c)
	if err != nil {
		return nil, err
	}
	return []deploy.Deployment{d}, nil
}

func (f *fakeDeployments) Instances(_ context.Context, c project.Caller, _ string) ([]store.ListDeploymentInstancesRow, error) {
	f.caller = c
	addr := "127.0.0.1:49153"
	return []store.ListDeploymentInstancesRow{{ID: "i1", ServiceName: "api", NodeName: "dev-1", State: "running", Addr: &addr}}, nil
}

func (f *fakeDeployments) URLs(_ context.Context, c project.Caller, _ string) ([]store.ServiceURLsRow, error) {
	f.caller = c
	return []store.ServiceURLsRow{{Name: "api", Subdomain: "api-shop"}}, nil
}

func (f *fakeDeployments) AppLogs(_ context.Context, c project.Caller, _, service string, after int64, tail int32) ([]store.AppLogsAfterRow, error) {
	f.caller = c
	if after > 0 || f.err != nil {
		return nil, f.err
	}
	return []store.AppLogsAfterRow{
		{ID: 1, Ts: time.Unix(0, 0), Stream: "stdout", Line: `{"level":"INFO","msg":"listening"}`, InstanceID: "11111111-2222", ServiceName: "api"},
		{ID: 2, Ts: time.Unix(1, 0), Stream: "stderr", Line: "boom", InstanceID: "11111111-2222", ServiceName: "api"},
	}, nil
}

func (f *fakeDeployments) Services(_ context.Context, c project.Caller, _ string) ([]store.LiveServiceStatesRow, error) {
	f.caller = c
	if f.err != nil {
		return nil, f.err
	}
	label := "api-shop"
	at := time.Unix(0, 0)
	return []store.LiveServiceStatesRow{
		{Name: "api", Type: "web", Path: "./cmd/api", Subdomain: &label, DeploymentID: "d1", ReleasedAt: &at, Paused: 1},
		{Name: "worker", Type: "worker", Path: "./cmd/worker", DeploymentID: "d1", ReleasedAt: &at, Running: 1},
	}, nil
}

func (f *fakeDeployments) Redeploy(_ context.Context, c project.Caller, id string) (deploy.Deployment, error) {
	d, err := f.one(c)
	d.ID, d.Status, d.RollbackOf = "d2", "built", &id
	return d, err
}

func (f *fakeDeployments) BuildsAhead(context.Context, project.Caller, string) (int64, error) {
	return 2, nil
}

func (f *fakeDeployments) Logs(_ context.Context, c project.Caller, _ string, after, _ int32) (deploy.Deployment, []store.BuildLog, []store.Artifact, error) {
	d, err := f.one(c)
	if err != nil {
		return d, nil, nil, err
	}
	page := f.logs[min(f.calls, len(f.logs)-1)]
	f.calls++
	d.Status = page.status
	var out []store.BuildLog
	for i, l := range page.lines {
		out = append(out, store.BuildLog{Seq: after + int32(i) + 1, Stream: "build", Line: l, Ts: time.Unix(0, 0)})
	}
	var arts []store.Artifact
	if page.status == "ready" {
		secs := int32(42)
		d.BuildSeconds = &secs
		arts = []store.Artifact{{ServiceName: "api", ServicePath: "./cmd/api", ServiceType: "web", SizeBytes: 42, Sha256: "ab"}}
	}
	return d, out, arts, nil
}

func TestDeploymentLogsFollow(t *testing.T) {
	fd := &fakeDeployments{logs: []logPage{
		{"queued", nil},
		{"building", []string{"go: downloading chi", "building api"}},
		{"building", nil},
		{"ready", []string{"done"}},
	}}
	s := newTestServer(&fakeAuth{tokens: map[string]store.User{"cli": {ID: "u1"}}})
	s.Deployments = fd
	req := httptest.NewRequest("GET", "/v1/deployments/d1/logs?follow=1", nil)
	req.Header.Set("Authorization", "Bearer cli")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	body := rec.Body.String()
	want := []string{`"queue":{"ahead":2}`, `"line":"go: downloading chi"`, `"seq":2`, `"seq":3`, `"line":"done"`, `"end":{"status":"ready"`, `"service":"api"`, `"url":"https://api-shop.greyship.tech"`, `"build":{"seconds":42}`}
	last := -1
	for _, w := range want {
		i := strings.Index(body, w)
		if i < 0 || i < last {
			t.Fatalf("stream missing or out of order %s:\n%s", w, body)
		}
		last = i
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content type = %s", ct)
	}
}

func TestDeploymentLogsWithoutFollowEnds(t *testing.T) {
	fd := &fakeDeployments{logs: []logPage{{"building", []string{"one"}}}}
	s := newTestServer(&fakeAuth{tokens: map[string]store.User{"cli": {ID: "u1"}}})
	s.Deployments = fd
	req := httptest.NewRequest("GET", "/v1/deployments/d1/logs?after=4", nil)
	req.Header.Set("Authorization", "Bearer cli")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if fd.calls != 1 || !strings.Contains(rec.Body.String(), `"seq":5`) || strings.Contains(rec.Body.String(), `"end"`) {
		t.Fatalf("calls = %d, body = %s", fd.calls, rec.Body.String())
	}
	req = httptest.NewRequest("GET", "/v1/deployments/d1/logs?after=-1", nil)
	req.Header.Set("Authorization", "Bearer cli")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("negative after = %d, want 400", rec.Code)
	}
}

func TestDeploymentEndpoints(t *testing.T) {
	sha := strings.Repeat("a", 64)
	tests := []struct {
		name       string
		err        error
		method     string
		path       string
		body       string
		auth       bool
		wantStatus int
		wantBody   string
	}{
		{"create needs auth", nil, "POST", "/v1/projects/p1/deployments", `{"size_bytes":10,"sha256":"` + sha + `"}`, false, http.StatusUnauthorized, "unauthenticated"},
		{"create", nil, "POST", "/v1/projects/p1/deployments", `{"size_bytes":10,"sha256":"` + sha + `"}`, true, http.StatusCreated, `"upload_url":"http://storage/x?sig"`},
		{"create rejects unknown fields", nil, "POST", "/v1/projects/p1/deployments", `{"size_bytes":10,"source_key":"x"}`, true, http.StatusBadRequest, "invalid_json"},
		{"create local", nil, "POST", "/v1/projects/p1/deployments", `{"binaries":[{"service":"api","path":"./cmd/api","type":"web","sha256":"` + sha + `","size_bytes":10,"gz_bytes":5}]}`, true, http.StatusCreated, `"uploads":[{"service":"api","upload_url":"http://storage/b?sig"}]`},
		{"create local with files", nil, "POST", "/v1/projects/p1/deployments", `{"binaries":[],"files":{"sha256":"` + sha + `","size_bytes":10}}`, true, http.StatusCreated, `"files_upload_url":"http://storage/f?sig"`},
		{"create local shows mode", nil, "POST", "/v1/projects/p1/deployments", `{"binaries":[]}`, true, http.StatusCreated, `"build_mode":"local"`},
		{"bad binaries", deploy.ErrBadBinary, "POST", "/v1/projects/p1/deployments", `{"binaries":[]}`, true, http.StatusBadRequest, "invalid_binaries"},
		{"build target", nil, "GET", "/v1/build-target", "", true, http.StatusOK, `"goos":"linux"`},
		{"build target needs auth", nil, "GET", "/v1/build-target", "", false, http.StatusUnauthorized, "unauthenticated"},
		{"too large", deploy.ErrTooLarge, "POST", "/v1/projects/p1/deployments", `{"size_bytes":1}`, true, http.StatusRequestEntityTooLarge, "source_too_large"},
		{"bad source", deploy.ErrBadSource, "POST", "/v1/projects/p1/deployments", `{"size_bytes":0}`, true, http.StatusBadRequest, "invalid_source"},
		{"viewer", project.ErrReadOnly, "POST", "/v1/projects/p1/deployments", `{"size_bytes":1}`, true, http.StatusForbidden, "read_only"},
		{"other team's project", project.ErrNotFound, "POST", "/v1/projects/p1/deployments", `{"size_bytes":1}`, true, http.StatusNotFound, "not_found"},
		{"wrong team token", project.ErrWrongTeam, "GET", "/v1/deployments/d1", "", true, http.StatusForbidden, "wrong_team"},
		{"start", nil, "POST", "/v1/deployments/d1/start", "", true, http.StatusOK, `"id":"d1"`},
		{"start before upload", deploy.ErrUploadMissing, "POST", "/v1/deployments/d1/start", "", true, http.StatusConflict, "upload_missing"},
		{"start twice", deploy.ErrNotUploading, "POST", "/v1/deployments/d1/start", "", true, http.StatusConflict, "already_started"},
		{"redeploy", nil, "POST", "/v1/deployments/d1/redeploy", "", true, http.StatusCreated, `"rollback_of":"d1"`},
		{"redeploy needs auth", nil, "POST", "/v1/deployments/d1/redeploy", "", false, http.StatusUnauthorized, "unauthenticated"},
		{"redeploy a failed build", deploy.ErrNotDeployable, "POST", "/v1/deployments/d1/redeploy", "", true, http.StatusConflict, "not_deployable"},
		{"redeploy as viewer", project.ErrReadOnly, "POST", "/v1/deployments/d1/redeploy", "", true, http.StatusForbidden, "read_only"},
		{"redeploy another team's", deploy.ErrNotFound, "POST", "/v1/deployments/d1/redeploy", "", true, http.StatusNotFound, "not_found"},
		{"get missing", deploy.ErrNotFound, "GET", "/v1/deployments/nope", "", true, http.StatusNotFound, "No such deployment."},
		{"get", nil, "GET", "/v1/deployments/d1", "", true, http.StatusOK, `"project":"shop"`},
		{"get shows urls", nil, "GET", "/v1/deployments/d1", "", true, http.StatusOK, `"urls":[{"service":"api","url":"https://api-shop.greyship.tech"}]`},
		{"get shows instances", nil, "GET", "/v1/deployments/d1", "", true, http.StatusOK, `"instances":[{"id":"i1","service":"api","node":"dev-1","state":"running"`},
		{"list", nil, "GET", "/v1/projects/p1/deployments", "", true, http.StatusOK, `"deployments":[{"id":"d1"`},
		{"services", nil, "GET", "/v1/projects/p1/services", "", true, http.StatusOK, `{"name":"api","type":"web","path":"./cmd/api","url":"https://api-shop.greyship.tech","state":"sleeping","instances":0`},
		{"services show running", nil, "GET", "/v1/projects/p1/services", "", true, http.StatusOK, `{"name":"worker","type":"worker","path":"./cmd/worker","url":null,"state":"running","instances":1`},
		{"services of another team", project.ErrNotFound, "GET", "/v1/projects/p1/services", "", true, http.StatusNotFound, "not_found"},
		{"list bad limit", nil, "GET", "/v1/projects/p1/deployments?limit=1000", "", true, http.StatusBadRequest, "invalid_limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fd := &fakeDeployments{err: tt.err}
			s := newTestServer(&fakeAuth{tokens: map[string]store.User{"cli": {ID: "u1"}}})
			s.Deployments = fd
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tt.auth {
				req.Header.Set("Authorization", "Bearer cli")
			}
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus || !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("got %d %s, want %d containing %s", rec.Code, rec.Body.String(), tt.wantStatus, tt.wantBody)
			}
			if tt.wantStatus < 400 && tt.path != "/v1/build-target" && fd.caller != (project.Caller{UserID: "u1", TokenTeamID: "t1"}) {
				t.Fatalf("caller = %+v, want the token's user and team", fd.caller)
			}
			if strings.Contains(rec.Body.String(), "source_key") || strings.Contains(rec.Body.String(), "127.0.0.1") {
				t.Fatal("storage keys and node addresses are internal and must not be returned")
			}
		})
	}
}

func TestProjectLogs(t *testing.T) {
	s := newTestServer(&fakeAuth{tokens: map[string]store.User{"cli": {ID: "u1"}}})
	s.Deployments = &fakeDeployments{}
	req := httptest.NewRequest("GET", "/v1/projects/p1/logs?service=api", nil)
	req.Header.Set("Authorization", "Bearer cli")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"line":"boom"`) || !strings.Contains(rec.Body.String(), `"instance":"11111111"`) {
		t.Fatalf("logs = %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest("GET", "/v1/projects/p1/logs?tail=5000", nil)
	req.Header.Set("Authorization", "Bearer cli")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("tail=5000 = %d, want 400", rec.Code)
	}
}

func TestAppURLScheme(t *testing.T) {
	tests := []struct {
		name string
		s    Server
		want string
	}{
		{"https", Server{Options: Options{AppsDomain: "greyshipapp.tech"}}, "https://api-shop.greyshipapp.tech"},
		{"https on another port", Server{Options: Options{AppsDomain: "localhost", AppsPort: "8443"}}, "https://api-shop.localhost:8443"},
		{"http only (a fresh install)", Server{Options: Options{AppsDomain: "apps-1-2-3-4.sslip.io", HTTPOnly: true}}, "http://api-shop.apps-1-2-3-4.sslip.io"},
		{"http only on port 80", Server{Options: Options{AppsDomain: "apps-1-2-3-4.sslip.io", AppsPort: "80", HTTPOnly: true}}, "http://api-shop.apps-1-2-3-4.sslip.io"},
	}
	for _, tt := range tests {
		if got := tt.s.appURL("api-shop"); got != tt.want {
			t.Errorf("%s: appURL = %q, want %q", tt.name, got, tt.want)
		}
	}
}
