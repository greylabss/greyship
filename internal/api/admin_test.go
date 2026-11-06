package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"greyship.tech/internal/admin"
	"greyship.tech/internal/auth"
	"greyship.tech/internal/store"
)

type fakeAdmin struct{ calls int }

func (f *fakeAdmin) Overview(context.Context) (admin.Overview, error) {
	f.calls++
	return admin.Overview{Counts: store.AdminCountsRow{Users: 3}}, nil
}
func (f *fakeAdmin) Deployments(context.Context, string, string, int32) ([]store.AdminDeploymentsRow, error) {
	f.calls++
	return nil, nil
}
func (f *fakeAdmin) Deployment(_ context.Context, id string) (admin.Detail, error) {
	f.calls++
	if id == "abcd" {
		return admin.Detail{}, admin.ErrAmbiguous
	}
	return admin.Detail{}, admin.ErrNotFound
}
func (f *fakeAdmin) Users(context.Context, int32) ([]store.AdminUsersRow, error) {
	f.calls++
	return nil, nil
}

// Only the operator, signed in to the dashboard, gets in; a token of theirs, or anyone else, sees nothing.
func TestAdminAccess(t *testing.T) {
	fa := &fakeAuth{
		sessions: map[string]store.User{"op": {ID: "u1", Email: "owner@example.com", IsOwner: true}, "other": {ID: "u2", Email: "someone@example.com"}},
		tokens:   map[string]store.User{"optoken": {ID: "u1", Email: "owner@example.com", IsOwner: true}},
	}
	tests := []struct {
		name, path     string
		cookie, bearer string
		status         int
		want           string
	}{
		{"operator", "/v1/admin/overview", "op", "", http.StatusOK, `"Users":3`},
		{"operator list", "/v1/admin/deployments?status=failed", "op", "", http.StatusOK, `"deployments":[]`},
		{"operator missing deployment", "/v1/admin/deployments/ffff", "op", "", http.StatusNotFound, "No deployment"},
		{"operator ambiguous prefix", "/v1/admin/deployments/abcd", "op", "", http.StatusConflict, "ambiguous"},
		{"operator check", "/v1/admin", "op", "", http.StatusOK, `"admin":true`},
		{"someone else", "/v1/admin/overview", "other", "", http.StatusNotFound, "not_found"},
		{"someone else check", "/v1/admin", "other", "", http.StatusNotFound, "not_found"},
		{"operator's API token", "/v1/admin/overview", "", "optoken", http.StatusNotFound, "not_found"},
		{"nobody", "/v1/admin/users", "", "", http.StatusUnauthorized, "unauthenticated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fad := &fakeAdmin{}
			s := New(Options{Auth: fa, Admin: fad, Signer: auth.Signer{Key: []byte(strings.Repeat("k", 32)), Now: time.Now}, DashboardURL: "http://dash.test", SecureCookie: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			req := httptest.NewRequest("GET", tt.path, nil)
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "__Host-gs_session", Value: tt.cookie})
			}
			if tt.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tt.bearer)
			}
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("got %d %s, want %d containing %s", rec.Code, rec.Body.String(), tt.status, tt.want)
			}
			if tt.status >= 400 && tt.status != http.StatusConflict && tt.name != "operator missing deployment" && fad.calls != 0 {
				t.Fatal("a refused request reached the admin service")
			}
		})
	}
}
