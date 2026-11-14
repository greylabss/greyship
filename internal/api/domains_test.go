package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"greyship.tech/internal/domains"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
)

type fakeDomains struct {
	err     error
	added   []string
	removed []string
	caller  project.Caller
}

func (f *fakeDomains) List(_ context.Context, c project.Caller, _ string) ([]domains.Entry, error) {
	f.caller = c
	return []domains.Entry{
		{Hostname: "api-shop.greyshipapp.tech", Service: "api"},
		{Hostname: "shop.example.com", Service: "api", Custom: true, DNS: domains.Pointing},
	}, f.err
}

func (f *fakeDomains) Add(_ context.Context, c project.Caller, _, hostname, service string) (domains.Entry, error) {
	f.caller = c
	f.added = append(f.added, hostname+"/"+service)
	return domains.Entry{Hostname: hostname, Service: "api", Custom: true, DNS: domains.Unresolved}, f.err
}

func (f *fakeDomains) Remove(_ context.Context, c project.Caller, _, hostname string) error {
	f.caller = c
	f.removed = append(f.removed, hostname)
	return f.err
}

func (f *fakeDomains) Verify(_ context.Context, c project.Caller, _, hostname string) (domains.Entry, error) {
	f.caller = c
	return domains.Entry{Hostname: hostname, Service: "api", Custom: true, DNS: domains.Pointing,
		Records: []domains.Record{{Type: "CNAME", Name: "shop", Value: "api-shop.greyshipapp.tech"}}}, f.err
}

func (f *fakeDomains) Connect(_ context.Context, c project.Caller, _, _ string) (domains.Link, error) {
	f.caller = c
	return domains.Link{Provider: "cloudflare", URL: "https://dash.cloudflare.com/apply"}, f.err
}

func TestDomainEndpoints(t *testing.T) {
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
		{"list", nil, "GET", "/v1/projects/p1/domains", "", true, 200, `"dns":"pointing"`},
		{"list shows the default address first", nil, "GET", "/v1/projects/p1/domains", "", true, 200, `"hostname":"api-shop.greyshipapp.tech","service":"api","custom":false`},
		{"list needs auth", nil, "GET", "/v1/projects/p1/domains", "", false, 401, "unauthenticated"},
		{"add", nil, "POST", "/v1/projects/p1/domains", `{"hostname":"shop.example.com","service":"api"}`, true, 201, `"dns":"unresolved"`},
		{"verify", nil, "POST", "/v1/projects/p1/domains/shop.example.com/verify", "", true, 200, `"records":[{"type":"CNAME","name":"shop","value":"api-shop.greyshipapp.tech"}]`},
		{"verify needs auth", nil, "POST", "/v1/projects/p1/domains/shop.example.com/verify", "", false, 401, "unauthenticated"},
		{"verify unknown name", domains.ErrNotFound, "POST", "/v1/projects/p1/domains/nope.example.com/verify", "", true, 404, "not_found"},
		{"connect", nil, "GET", "/v1/projects/p1/domains/shop.example.com/connect", "", true, 200, `"provider":"cloudflare"`},
		{"connect not available", domains.ErrNoAutoSetup, "GET", "/v1/projects/p1/domains/shop.example.com/connect", "", true, 404, "no_auto_setup"},
		{"add rejects unknown fields", nil, "POST", "/v1/projects/p1/domains", `{"hostname":"x.example.com","id":"y"}`, true, 400, "invalid_json"},
		{"invalid hostname", &domains.InvalidError{Reason: "use a name, not an IP address"}, "POST", "/v1/projects/p1/domains", `{"hostname":"1.2.3.4"}`, true, 400, "invalid_hostname"},
		{"reserved", domains.ErrReserved, "POST", "/v1/projects/p1/domains", `{"hostname":"x.greyshipapp.tech"}`, true, 400, "reserved_hostname"},
		{"taken", domains.ErrTaken, "POST", "/v1/projects/p1/domains", `{"hostname":"shop.example.com"}`, true, 409, "hostname_taken"},
		{"not deployed yet", domains.ErrNoWebService, "POST", "/v1/projects/p1/domains", `{"hostname":"shop.example.com"}`, true, 409, "no_web_service"},
		{"which service", domains.ErrServiceRequired, "POST", "/v1/projects/p1/domains", `{"hostname":"shop.example.com"}`, true, 400, "service_required"},
		{"unknown service", domains.ErrUnknownService, "POST", "/v1/projects/p1/domains", `{"hostname":"shop.example.com","service":"x"}`, true, 400, "unknown_service"},
		{"viewer", project.ErrReadOnly, "POST", "/v1/projects/p1/domains", `{"hostname":"shop.example.com"}`, true, 403, "read_only"},
		{"outsider", project.ErrNotFound, "GET", "/v1/projects/p9/domains", "", true, 404, "not_found"},
		{"another team's token", project.ErrWrongTeam, "GET", "/v1/projects/p1/domains", "", true, 403, "wrong_team"},
		{"remove", nil, "DELETE", "/v1/projects/p1/domains/shop.example.com", "", true, 204, ""},
		{"remove what isn't there", domains.ErrNotFound, "DELETE", "/v1/projects/p1/domains/nope.example.com", "", true, 404, "not_found"},
		{"remove needs auth", nil, "DELETE", "/v1/projects/p1/domains/shop.example.com", "", false, 401, "unauthenticated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fd := &fakeDomains{err: tt.err}
			s := newTestServer(&fakeAuth{tokens: map[string]store.User{"cli": {ID: "u1"}}})
			s = New(func() Options { o := s.Options; o.Domains = fd; return o }())
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
			if tt.auth && tt.wantStatus < 400 && fd.caller != (project.Caller{UserID: "u1", TokenTeamID: "t1"}) {
				t.Fatalf("caller = %+v, want the token's user and team", fd.caller)
			}
		})
	}
}
