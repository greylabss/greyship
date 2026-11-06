package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeAPI answers the endpoints grey login/whoami/logout use. polls lists the device-token answers in order.
type fakeAPI struct {
	polls     []string
	pollCount int
	revoked   bool
	opened    string
}

func (f *fakeAPI) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/auth/device/code", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"dev","user_code":"KQWD-7HXP","verification_uri":"http://dash/device","verification_uri_complete":"http://dash/device?code=KQWD-7HXP","expires_in":600,"interval":5}`))
	})
	mux.HandleFunc("POST /v1/auth/device/token", func(w http.ResponseWriter, _ *http.Request) {
		answer := f.polls[min(f.pollCount, len(f.polls)-1)]
		f.pollCount++
		if answer == "ok" {
			_, _ = w.Write([]byte(`{"access_token":"gs_secret","token_type":"bearer","user":{"email":"a@b.co"}}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": answer, "message": answer}})
	})
	mux.HandleFunc("GET /v1/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gs_secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_token","message":"bad"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"user":{"email":"a@b.co","teams":[{"slug":"haki","plan":"free","role":"owner"}]}}`))
	})
	mux.HandleFunc("DELETE /v1/tokens/current", func(w http.ResponseWriter, _ *http.Request) {
		f.revoked = true
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func run(t *testing.T, f *fakeAPI, env map[string]string, configPath string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	a := &App{
		Out: &out, Err: &out, ConfigPath: configPath,
		Getenv:      func(k string) string { return env[k] },
		HTTP:        http.DefaultClient,
		Sleep:       func(context.Context, time.Duration) error { return nil },
		OpenBrowser: func(u string) error { f.opened = u; return nil },
		Hostname:    "macbook.local",
		Version:     "test",
	}
	root := NewRoot(a)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func TestLogin(t *testing.T) {
	tests := []struct {
		name    string
		polls   []string
		wantErr string
		wantOut string
	}{
		{"approved after waiting", []string{"authorization_pending", "slow_down", "ok"}, "", "✓ Logged in as a@b.co"},
		{"denied", []string{"authorization_pending", "access_denied"}, "login denied", ""},
		{"expired", []string{"expired_token"}, "expired", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeAPI{polls: tt.polls}
			srv := f.server(t)
			path := filepath.Join(t.TempDir(), "greyship", "config.json")
			out, err := run(t, f, map[string]string{"GREYSHIP_API_URL": srv.URL}, path, "login")

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if _, statErr := os.Stat(path); statErr == nil {
					t.Fatal("config written after a failed login")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "KQWD-7HXP") || !strings.Contains(out, tt.wantOut) || f.opened != "http://dash/device?code=KQWD-7HXP" {
				t.Fatalf("output %q, opened %q", out, f.opened)
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("config mode %v, want 0600", info.Mode().Perm())
			}
			cfg, _ := loadConfig(path)
			if cfg.Token != "gs_secret" || cfg.Email != "a@b.co" {
				t.Fatalf("saved config = %+v", cfg)
			}
		})
	}
}

func TestWhoamiAndLogout(t *testing.T) {
	f := &fakeAPI{}
	srv := f.server(t)
	path := filepath.Join(t.TempDir(), "config.json")

	if _, err := run(t, f, map[string]string{"GREYSHIP_API_URL": srv.URL}, path, "whoami"); err == nil || !strings.Contains(err.Error(), "grey login") {
		t.Fatalf("whoami logged out: %v", err)
	}

	// CI: the token comes from the environment, nothing on disk.
	out, err := run(t, f, map[string]string{"GREYSHIP_API_URL": srv.URL, "GREYSHIP_TOKEN": "gs_secret"}, path, "whoami", "--json")
	if err != nil || !strings.Contains(out, `"email": "a@b.co"`) {
		t.Fatalf("whoami --json: %v %s", err, out)
	}

	if err := saveConfig(path, Config{APIURL: srv.URL, Token: "gs_secret", Email: "a@b.co"}); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, f, nil, path, "whoami")
	if err != nil || !strings.Contains(out, "haki (owner, free)") {
		t.Fatalf("whoami: %v %s", err, out)
	}
	out, err = run(t, f, nil, path, "logout")
	if err != nil || !f.revoked || !strings.Contains(out, "revoked") {
		t.Fatalf("logout: %v revoked=%v %s", err, f.revoked, out)
	}
	if cfg, _ := loadConfig(path); cfg.Token != "" {
		t.Fatalf("token kept after logout: %+v", cfg)
	}
}

func TestRevokedTokenMessage(t *testing.T) {
	f := &fakeAPI{}
	srv := f.server(t)
	_, err := run(t, f, map[string]string{"GREYSHIP_API_URL": srv.URL, "GREYSHIP_TOKEN": "gs_old"}, filepath.Join(t.TempDir(), "c.json"), "whoami")
	if err == nil || !strings.Contains(err.Error(), "Run: grey login") {
		t.Fatalf("err = %v", err)
	}
}

func TestNormalizeServer(t *testing.T) {
	good := map[string]string{
		"greyship.example.com":          "https://greyship.example.com",
		"https://greyship.example.com/": "https://greyship.example.com",
		" HTTPS://x.test ":              "https://x.test",
		"http://localhost:3000":         "http://localhost:3000",
		"1-2-3-4.sslip.io":              "https://1-2-3-4.sslip.io",
	}
	for in, want := range good {
		got, err := normalizeServer(in)
		if err != nil || got != strings.ToLower(want) && got != want {
			t.Errorf("normalizeServer(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "http://greyship.example.com", "https://x.test/path", "https://user@x.test", "https://x.test?a=1", "ftp://x.test"} {
		if _, err := normalizeServer(bad); err == nil {
			t.Errorf("normalizeServer(%q) accepted it", bad)
		}
	}
}

// grey login <server> switches to a self-hosted Greyship and remembers it, even when logged in elsewhere.
func TestLoginToAnotherServer(t *testing.T) {
	f := &fakeAPI{polls: []string{"ok"}}
	srv := f.server(t)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := saveConfig(path, Config{Token: "token-for-the-default-server", Email: "old@x.co"}); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, f, map[string]string{}, path, "login", srv.URL)
	if err != nil || !strings.Contains(out, "✓ Logged in as a@b.co on "+srv.URL) {
		t.Fatalf("login: %v\n%s", err, out)
	}
	cfg, _ := loadConfig(path)
	if cfg.APIURL != srv.URL || cfg.Token != "gs_secret" {
		t.Fatalf("saved config = %+v, want the new server and its token", cfg)
	}
	if out, err := run(t, f, map[string]string{}, path, "whoami"); err != nil || !strings.Contains(out, "a@b.co on "+srv.URL) {
		t.Fatalf("whoami: %v\n%s", err, out)
	}
	if _, err := run(t, f, map[string]string{}, path, "login", "http://greyship.example.com"); err == nil {
		t.Fatal("plain http to a remote server was accepted")
	}
}
