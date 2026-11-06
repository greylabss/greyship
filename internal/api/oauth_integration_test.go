//go:build integration

package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"greyship.tech/internal/api"
	"greyship.tech/internal/auth"
	"greyship.tech/internal/mail"
	"greyship.tech/internal/testdb"
)

// fakeGitHub plays GitHub's OAuth and API endpoints, including the PKCE check.
func fakeGitHub(t *testing.T, email string, verified bool) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "good-code" || r.Form.Get("code_verifier") == "" {
			http.Error(w, "bad grant", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"gh-token","token_type":"bearer"}`))
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gh-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"id":7,"login":"gopher","name":"","avatar_url":"https://avatars/7"}`))
	})
	mux.HandleFunc("GET /user/emails", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"email": "other@x.io", "primary": false, "verified": true}, {"email": email, "primary": true, "verified": verified}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestGitHubLoginEndToEnd(t *testing.T) {
	tests := []struct {
		name         string
		verified     bool
		wantLocation string
		wantSession  bool
	}{
		{"verified email signs in", true, "http://dash.test/continue", true},
		{"unverified email is refused", false, "http://dash.test/signin?error=unverified_email", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := testdb.New(t)
			gh := fakeGitHub(t, "gopher@x.io", tt.verified)
			ep := auth.Endpoints{AuthURL: gh.URL + "/login/oauth/authorize", TokenURL: gh.URL + "/login/oauth/access_token", APIURL: gh.URL}

			var apiURL string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				api.New(api.Options{
					Auth:         auth.NewService(pool, &mail.Outbox{Dir: t.TempDir(), Now: time.Now}, time.Now, auth.Signup{Open: true}),
					Providers:    map[string]auth.Provider{"github": auth.GitHub("cid", "secret", apiURL+"/v1/auth/oauth/github/callback", ep)},
					Signer:       auth.Signer{Key: []byte(strings.Repeat("k", 32)), Now: time.Now},
					DashboardURL: "http://dash.test",
					Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
				}).ServeHTTP(w, r)
			}))
			defer srv.Close()
			apiURL = srv.URL

			jar, _ := cookiejar.New(nil)
			client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

			resp, err := client.Get(srv.URL + "/v1/auth/oauth/github/start")
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			authorize, _ := url.Parse(resp.Header.Get("Location"))
			state := authorize.Query().Get("state")

			resp, err = client.Get(srv.URL + "/v1/auth/oauth/github/callback?code=good-code&state=" + url.QueryEscape(state))
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if got := resp.Header.Get("Location"); got != tt.wantLocation {
				t.Fatalf("callback redirect = %q, want %q", got, tt.wantLocation)
			}

			resp, err = client.Get(srv.URL + "/v1/me")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if tt.wantSession != (resp.StatusCode == http.StatusOK) {
				t.Fatalf("/v1/me = %d %s", resp.StatusCode, body)
			}
			if tt.wantSession && (!strings.Contains(string(body), `"email":"gopher@x.io"`) || !strings.Contains(string(body), `"name":"gopher"`)) {
				t.Fatalf("/v1/me body = %s", body)
			}
		})
	}
}
