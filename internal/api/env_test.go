package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"greyship.tech/internal/env"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
)

type fakeEnv struct {
	err     error
	restart bool
}

func (f *fakeEnv) List(context.Context, project.Caller, string, string) ([]env.Var, error) {
	return []env.Var{{Key: "STRIPE_KEY", Hash: "abc", UpdatedAt: time.Unix(0, 0)}}, f.err
}

func (f *fakeEnv) Values(context.Context, project.Caller, string, string) (map[string]string, error) {
	return map[string]string{"STRIPE_KEY": "sk_live"}, f.err
}

func (f *fakeEnv) Apply(_ context.Context, _ project.Caller, _, _ string, set map[string]string, _ []string, restart bool) (env.Change, error) {
	f.restart = restart
	keys := []string{}
	for k := range set {
		keys = append(keys, k)
	}
	return env.Change{Added: keys}, f.err
}

func TestEnvEndpoints(t *testing.T) {
	tests := []struct {
		name, method, path, body string
		err                      error
		code                     int
		want, never              string
	}{
		{"list shows keys and hashes", "GET", "/v1/projects/p1/env", "", nil, 200, `"key":"STRIPE_KEY","hash":"abc"`, "sk_live"},
		{"values only on the values endpoint", "GET", "/v1/projects/p1/env/values", "", nil, 200, `"STRIPE_KEY":"sk_live"`, ""},
		{"viewer can't pull", "GET", "/v1/projects/p1/env/values", "", project.ErrReadOnly, 403, "read_only", "sk_live"},
		{"set", "PATCH", "/v1/projects/p1/env", `{"set":{"A":"1"}}`, nil, 200, `"added":["A"]`, ""},
		{"reserved key", "PATCH", "/v1/projects/p1/env", `{"set":{"PORT":"1"}}`, &env.KeyError{Key: "PORT", Reason: "set by Greyship"}, 400, "invalid_key", ""},
		{"bad env", "GET", "/v1/projects/p1/env?env=staging", "", env.ErrBadEnv, 400, "invalid_env", ""},
		{"other team", "GET", "/v1/projects/p1/env", "", project.ErrNotFound, 404, "not_found", "STRIPE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(&fakeAuth{tokens: map[string]store.User{"cli": {ID: "u1"}}})
			fe := &fakeEnv{err: tt.err}
			s.Env = fe
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("Authorization", "Bearer cli")
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != tt.code || !strings.Contains(rec.Body.String(), tt.want) || (tt.never != "" && strings.Contains(rec.Body.String(), tt.never)) {
				t.Fatalf("got %d %s", rec.Code, rec.Body.String())
			}
		})
	}
	s := newTestServer(&fakeAuth{tokens: map[string]store.User{"cli": {ID: "u1"}}})
	fe := &fakeEnv{}
	s.Env = fe
	req := httptest.NewRequest("PATCH", "/v1/projects/p1/env", strings.NewReader(`{"set":{"A":"1"},"restart":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer cli")
	s.ServeHTTP(httptest.NewRecorder(), req)
	if fe.restart {
		t.Fatal("restart:false was ignored")
	}
}
