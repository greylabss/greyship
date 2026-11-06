package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	secret := strings.Repeat("ab", 32)
	base := map[string]string{
		"GREYSHIP_ENV":           "development",
		"GREYSHIP_DATABASE_URL":  "postgres://localhost/greyship",
		"GREYSHIP_COOKIE_SECRET": secret,
	}
	with := func(overrides map[string]string) func(string) string {
		env := map[string]string{}
		for k, v := range base {
			env[k] = v
		}
		for k, v := range overrides {
			env[k] = v
		}
		return func(k string) string { return env[k] }
	}

	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"development defaults", nil, ""},
		{"missing database", map[string]string{"GREYSHIP_DATABASE_URL": ""}, "GREYSHIP_DATABASE_URL is required"},
		{"short secret", map[string]string{"GREYSHIP_COOKIE_SECRET": "abcd"}, "at least 32 bytes"},
		{"non-hex secret", map[string]string{"GREYSHIP_COOKIE_SECRET": "zz"}, "must be hex"},
		{"relative dashboard url", map[string]string{"GREYSHIP_DASHBOARD_URL": "/dash"}, "GREYSHIP_DASHBOARD_URL must be an absolute URL"},
		{"production needs storage", map[string]string{"GREYSHIP_ENV": "production"}, "GREYSHIP_S3_ENDPOINT must be an absolute URL"},
		{"production needs storage keys", map[string]string{"GREYSHIP_ENV": "production", "GREYSHIP_RESEND_API_KEY": "re_123", "GREYSHIP_S3_ENDPOINT": "https://s3.example.com"}, "GREYSHIP_S3_ACCESS_KEY"},
		{"production complete", map[string]string{"GREYSHIP_ENV": "production", "GREYSHIP_S3_ENDPOINT": "https://s3.example.com", "GREYSHIP_S3_ACCESS_KEY": "a", "GREYSHIP_S3_SECRET_KEY": "b"}, "GREYSHIP_MASTER_KEY"},
		{"production complete with master key", map[string]string{"GREYSHIP_ENV": "production", "GREYSHIP_RESEND_API_KEY": "re_123", "GREYSHIP_S3_ENDPOINT": "https://s3.example.com", "GREYSHIP_S3_ACCESS_KEY": "a", "GREYSHIP_S3_SECRET_KEY": "b", "GREYSHIP_MASTER_KEY": "00"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(with(tt.env))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				if cfg.Addr != ":8080" || cfg.DashboardURL != "http://localhost:3000" || len(cfg.CookieSecret) != 32 || cfg.Storage.Bucket != "greyship" || cfg.Storage.Endpoint == "" {
					t.Fatalf("unexpected defaults: %+v", cfg)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadBuilder(t *testing.T) {
	env := map[string]string{"GREYSHIP_ENV": "development", "GREYSHIP_DATABASE_URL": "postgres://localhost/greyship", "HOME": "/Users/x"}
	b, err := LoadBuilder(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if b.DockerHost != "unix:///Users/x/.lima/greyship/sock/docker.sock" || b.Network != "gs-build" || b.Timeout.Minutes() != 10 || b.Storage.Endpoint == "" {
		t.Fatalf("defaults = %+v", b)
	}
	env["GREYSHIP_ENV"] = "production"
	env["GREYSHIP_BUILD_TIMEOUT"] = "soon"
	if _, err := LoadBuilder(func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), "GREYSHIP_S3_ENDPOINT") || !strings.Contains(err.Error(), "GREYSHIP_BUILD_TIMEOUT") {
		t.Fatalf("err = %v", err)
	}
}

// A fresh install has no domain and no TLS: the router needs no ACME email, then.
func TestRouterHTTPOnly(t *testing.T) {
	env := map[string]string{"GREYSHIP_DATABASE_URL": "postgres://x", "GREYSHIP_APPS_DOMAIN": "apps-1-2-3-4.sslip.io"}
	get := func(k string) string { return env[k] }
	if _, err := LoadRouter(get); err == nil || !strings.Contains(err.Error(), "GREYSHIP_ACME_EMAIL") {
		t.Fatalf("an HTTPS router without an email = %v, want the ACME email error", err)
	}
	env["GREYSHIP_HTTP_ONLY"] = "true"
	r, err := LoadRouter(get)
	if err != nil || !r.HTTPOnly {
		t.Fatalf("an HTTP-only router = %+v, %v", r, err)
	}
}
