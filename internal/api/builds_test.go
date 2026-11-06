package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"greyship.tech/internal/build/fly"
)

type fakeBuilds struct{ token string }

func (f *fakeBuilds) UploadURL(_ context.Context, token, _ string, req fly.UploadRequest) (string, error) {
	if token != f.token {
		return "", fly.ErrBadToken
	}
	if req.Size <= 0 {
		return "", fly.ErrBadReport
	}
	return "https://r2.example/put?sig", nil
}

func (f *fakeBuilds) Add(_ context.Context, token, _ string, _ fly.Report) error {
	if token != f.token {
		return fly.ErrBadToken
	}
	return nil
}

func TestBuildMachineEndpoints(t *testing.T) {
	s := newTestServer(&fakeAuth{})
	s.Builds = &fakeBuilds{token: "good"}
	tests := []struct {
		name, path, auth, body string
		want                   int
	}{
		{"upload", "/v1/internal/builds/d1/upload", "Build good", `{"target":"api","size":10}`, http.StatusOK},
		{"upload bad size", "/v1/internal/builds/d1/upload", "Build good", `{"target":"api","size":0}`, http.StatusBadRequest},
		{"upload forged token", "/v1/internal/builds/d1/upload", "Build forged", `{"target":"api","size":10}`, http.StatusUnauthorized},
		{"upload with a user token", "/v1/internal/builds/d1/upload", "Bearer gs_x", `{"target":"api","size":10}`, http.StatusUnauthorized},
		{"report", "/v1/internal/builds/d1/report", "Build good", `{"lines":[{"stream":"build","text":"ok"}]}`, http.StatusNoContent},
		{"report without token", "/v1/internal/builds/d1/report", "", `{"lines":[]}`, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("%s = %d %s, want %d", tt.path, rec.Code, rec.Body, tt.want)
			}
		})
	}
}
