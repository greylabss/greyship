package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
)

type fakeProjects struct {
	err    error
	caller project.Caller
}

func (f *fakeProjects) result(c project.Caller, team, name string) (project.Project, error) {
	f.caller = c
	if f.err != nil {
		return project.Project{}, f.err
	}
	return project.Project{Project: store.Project{ID: "p1", Name: name, Slug: "acme-api", CreatedAt: time.Unix(0, 0)}, TeamSlug: team}, nil
}

func (f *fakeProjects) Create(_ context.Context, c project.Caller, team, name string) (project.Project, error) {
	return f.result(c, team, name)
}

func (f *fakeProjects) List(_ context.Context, c project.Caller, team string) ([]project.Project, error) {
	p, err := f.result(c, team, "Acme API")
	if err != nil {
		return nil, err
	}
	return []project.Project{p}, nil
}

func (f *fakeProjects) Get(_ context.Context, c project.Caller, _ string) (project.Project, error) {
	return f.result(c, "haki", "Acme API")
}

func (f *fakeProjects) BySlug(_ context.Context, c project.Caller, team, _ string) (project.Project, error) {
	return f.result(c, team, "Acme API")
}

func (f *fakeProjects) Rename(_ context.Context, c project.Caller, _, name string) (project.Project, error) {
	return f.result(c, "haki", name)
}

func (f *fakeProjects) Delete(_ context.Context, c project.Caller, _, _ string) error {
	f.caller = c
	return f.err
}

func TestProjectEndpoints(t *testing.T) {
	taken := &project.TakenError{Existing: project.Project{Project: store.Project{ID: "p0", Slug: "acme-api"}, TeamSlug: "haki"}}
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
		{"list needs auth", nil, "GET", "/v1/teams/haki/projects", "", false, http.StatusUnauthorized, "unauthenticated"},
		{"list", nil, "GET", "/v1/teams/haki/projects", "", true, http.StatusOK, `"slug":"acme-api"`},
		{"create", nil, "POST", "/v1/teams/haki/projects", `{"name":"Acme API"}`, true, http.StatusCreated, `"team":"haki"`},
		{"create rejects unknown fields", nil, "POST", "/v1/teams/haki/projects", `{"name":"x","id":"y"}`, true, http.StatusBadRequest, "invalid_json"},
		{"invalid name", &project.NameError{Reason: "start the name with a letter"}, "POST", "/v1/teams/haki/projects", `{"name":"1"}`, true, http.StatusBadRequest, "That project name can't be used: start the name with a letter."},
		{"taken", taken, "POST", "/v1/teams/haki/projects", `{"name":"Acme API"}`, true, http.StatusConflict, `"id":"p0"`},
		{"not a member", project.ErrNotFound, "GET", "/v1/teams/other/projects", "", true, http.StatusNotFound, "not_found"},
		{"wrong team token", project.ErrWrongTeam, "GET", "/v1/projects/p1", "", true, http.StatusForbidden, "wrong_team"},
		{"viewer", project.ErrReadOnly, "PATCH", "/v1/projects/p1", `{"name":"New"}`, true, http.StatusForbidden, "read_only"},
		{"rename", nil, "PATCH", "/v1/projects/p1", `{"name":"New"}`, true, http.StatusOK, `"name":"New"`},
		{"delete", nil, "DELETE", "/v1/projects/p1", `{"confirm":"acme-api"}`, true, http.StatusAccepted, ""},
		{"delete with the wrong confirmation", project.ErrConfirm, "DELETE", "/v1/projects/p1", `{"confirm":"nope"}`, true, http.StatusBadRequest, "confirm_mismatch"},
		{"viewer can't delete", project.ErrReadOnly, "DELETE", "/v1/projects/p1", `{"confirm":"acme-api"}`, true, http.StatusForbidden, "read_only"},
		{"outsider can't delete", project.ErrNotFound, "DELETE", "/v1/projects/p9", `{"confirm":"x"}`, true, http.StatusNotFound, "not_found"},
		{"another team's token can't delete", project.ErrWrongTeam, "DELETE", "/v1/projects/p1", `{"confirm":"acme-api"}`, true, http.StatusForbidden, "wrong_team"},
		{"delete needs auth", nil, "DELETE", "/v1/projects/p1", `{"confirm":"acme-api"}`, false, http.StatusUnauthorized, "unauthenticated"},
		{"get", nil, "GET", "/v1/projects/p1", "", true, http.StatusOK, `"id":"p1"`},
		{"by slug", nil, "GET", "/v1/teams/haki/projects/acme-api", "", true, http.StatusOK, `"slug":"acme-api"`},
		{"by slug, not a member", project.ErrNotFound, "GET", "/v1/teams/other/projects/acme-api", "", true, http.StatusNotFound, "not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := &fakeProjects{err: tt.err}
			s := newTestServer(&fakeAuth{tokens: map[string]store.User{"cli": {ID: "u1"}}})
			s.Projects = fp
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
			if tt.auth && tt.wantStatus != http.StatusBadRequest && fp.caller != (project.Caller{UserID: "u1", TokenTeamID: "t1"}) {
				t.Fatalf("caller = %+v, want the token's user and team", fp.caller)
			}
		})
	}
}
