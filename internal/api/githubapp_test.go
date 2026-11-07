package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"greyship.tech/internal/github"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
)

func appReq(s *Server, method, path, body, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		req.Header.Set("Origin", "http://dash.test")
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func appAuth() *fakeAuth {
	return &fakeAuth{sessions: map[string]store.User{"u1": {ID: "u1"}}}
}

func TestTeamGitHubApp(t *testing.T) {
	gh := &fakeGitHub{}
	s := githubServer(appAuth(), gh)

	rec := appReq(s, "GET", "/v1/teams/haki/github/app", "", "u1")
	if rec.Code != 200 {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	var st struct {
		Configured  bool     `json:"configured"`
		DefaultName string   `json:"default_name"`
		WebhookURL  string   `json:"webhook_url"`
		Warnings    []string `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Configured || st.DefaultName != "Greyship haki" || !strings.HasSuffix(st.WebhookURL, "/v1/github/webhook/t1") || st.Warnings == nil {
		t.Fatalf("status = %+v", st)
	}

	rec = appReq(s, "POST", "/v1/teams/haki/github/app/manifest", `{"org":"acme","name":"Greyship haki"}`, "u1")
	if rec.Code != 200 {
		t.Fatalf("manifest = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Action   string         `json:"action"`
		Manifest map[string]any `json:"manifest"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	hook := out.Manifest["hook_attributes"].(map[string]any)["url"]
	if !strings.HasPrefix(out.Action, "https://github.com/settings/apps/new") || hook != "http://dash.test/v1/github/webhook/t1" || len(gh.started) != 1 || gh.started[0] != "u1/haki" {
		t.Fatalf("action %q hook %v started %v", out.Action, hook, gh.started)
	}

	for name, body := range map[string]string{"bad org": `{"org":"../evil"}`, "bad name": `{"name":"<script>"}`} {
		if rec := appReq(s, "POST", "/v1/teams/haki/github/app/manifest", body, "u1"); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}

	if rec := appReq(s, "DELETE", "/v1/teams/haki/github/app", "", "u1"); rec.Code != http.StatusNoContent || len(gh.deleted) != 1 || gh.deleted[0] != "haki" {
		t.Fatalf("delete = %d %v", rec.Code, gh.deleted)
	}
}

// People who may not write to the team, or aren't in it, can't create or remove its app; signed out gets 401.
func TestTeamGitHubAppAccess(t *testing.T) {
	for name, tc := range map[string]struct {
		err    error
		method string
		path   string
		status int
	}{
		"viewer creates":  {project.ErrReadOnly, "POST", "/v1/teams/haki/github/app/manifest", http.StatusForbidden},
		"outsider reads":  {project.ErrNotFound, "GET", "/v1/teams/other/github/app", http.StatusNotFound},
		"viewer deletes":  {project.ErrReadOnly, "DELETE", "/v1/teams/haki/github/app", http.StatusForbidden},
		"signed out":      {nil, "GET", "/v1/teams/haki/github/app", http.StatusUnauthorized},
		"no app, install": {github.ErrOff, "GET", "/v1/teams/haki/github/install", http.StatusNotFound},
	} {
		gh := &fakeGitHub{err: tc.err}
		s := githubServer(appAuth(), gh)
		cookie := "u1"
		if name == "signed out" {
			cookie = ""
		}
		if rec := appReq(s, tc.method, tc.path, `{}`, cookie); rec.Code != tc.status {
			t.Errorf("%s: %d %s, want %d", name, rec.Code, rec.Body.String(), tc.status)
		}
	}
}

func TestGitHubManifestCallback(t *testing.T) {
	gh := &fakeGitHub{}
	s := githubServer(appAuth(), gh)
	for name, tc := range map[string]struct {
		query, cookie, want string
		finishErr           error
	}{
		"done":           {"?code=abc&state=good-state", "u1", "/haki/new?github_app=created", nil},
		"bad state":      {"?code=abc&state=forged", "u1", "github_app_error=expired", nil},
		"github refused": {"?code=abc&state=good-state", "u1", "github_app_error=github_refused", github.ErrRefused},
		"save failed":    {"?code=abc&state=good-state", "u1", "github_app_error=failed", errors.New("disk full")},
	} {
		gh.finishErr = tc.finishErr
		rec := appReq(s, "GET", "/v1/github/manifest/callback"+tc.query, "", tc.cookie)
		if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), tc.want) {
			t.Errorf("%s: %d %s, want a redirect containing %q", name, rec.Code, rec.Header().Get("Location"), tc.want)
		}
	}
	if rec := appReq(s, "GET", "/v1/github/manifest/callback?code=abc&state=good-state", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("signed out callback = %d", rec.Code)
	}
}
