package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManifest(t *testing.T) {
	m := Manifest("https://app.example.com/", "Greyship app", "team-1")
	hook := m["hook_attributes"].(map[string]any)
	if m["url"] != "https://app.example.com" || hook["url"] != "https://app.example.com/v1/github/webhook/team-1" || hook["active"] != true {
		t.Fatalf("manifest = %v", m)
	}
	if m["redirect_url"] != "https://app.example.com/v1/github/manifest/callback" || m["setup_url"] != "https://app.example.com/v1/github/callback" {
		t.Fatalf("redirect or setup url = %v %v", m["redirect_url"], m["setup_url"])
	}
	if m["public"] != false || m["request_oauth_on_install"] != true {
		t.Fatalf("public/oauth = %v %v", m["public"], m["request_oauth_on_install"])
	}
	perms := m["default_permissions"].(map[string]string)
	if perms["contents"] != "read" || perms["statuses"] != "write" || len(perms) != 3 {
		t.Fatalf("permissions = %v; the app needs repository read and commit status write, nothing more", perms)
	}
	if ev := m["default_events"].([]string); len(ev) != 1 || ev[0] != "push" {
		t.Fatalf("events = %v", ev)
	}
}

func TestNewAppURL(t *testing.T) {
	tests := []struct {
		org, want string
		wantErr   bool
	}{
		{"", "https://github.com/settings/apps/new?state=s%2B1", false},
		{"acme-inc", "https://github.com/organizations/acme-inc/settings/apps/new?state=s%2B1", false},
		{"../evil", "", true},
		{"a b", "", true},
		{"-lead", "", true},
	}
	for _, tt := range tests {
		got, err := NewAppURL("https://github.com/", tt.org, "s+1")
		if tt.wantErr {
			if !errors.Is(err, ErrBadInput) {
				t.Errorf("org %q: err = %v, want ErrBadInput", tt.org, err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("org %q: %q, %v; want %q", tt.org, got, err, tt.want)
		}
	}
}

func TestCleanAppName(t *testing.T) {
	for name, ok := range map[string]bool{"Greyship": true, "  Greyship app.example.com ": true, "": false, "<b>": false, strings.Repeat("a", 35): false, strings.Repeat("a", 34): true} {
		if _, err := CleanAppName(name); (err == nil) != ok {
			t.Errorf("CleanAppName(%q) err = %v, want ok=%v", name, err, ok)
		}
	}
}

func TestConvert(t *testing.T) {
	var path, method string
	status := http.StatusCreated
	body := `{"id":42,"slug":"greyship-x","name":"Greyship x","html_url":"https://github.com/apps/greyship-x","client_id":"Iv1.x","client_secret":"cs","webhook_secret":"ws","pem":"PEM-BODY"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, method = r.URL.Path, r.Method
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c, err := Convert(context.Background(), srv.Client(), srv.URL, "code123")
	if err != nil || method != "POST" || path != "/app-manifests/code123/conversions" {
		t.Fatalf("convert = %+v %v (%s %s)", c, err, method, path)
	}
	if c.AppID != 42 || c.Slug != "greyship-x" || c.ClientSecret != "cs" || c.WebhookSecret != "ws" || c.PrivateKeyPEM != "PEM-BODY" {
		t.Fatalf("credentials = %+v", c)
	}

	status, body = http.StatusNotFound, `{"message":"Not Found","client_secret":"must-not-appear"}`
	_, err = Convert(context.Background(), srv.Client(), srv.URL, "code123")
	if err == nil || !strings.Contains(err.Error(), "Not Found") || strings.Contains(err.Error(), "must-not-appear") {
		t.Fatalf("refused conversion error = %v", err)
	}

	status, body = http.StatusCreated, `{"id":1,"slug":"x"}`
	if _, err = Convert(context.Background(), srv.Client(), srv.URL, "code123"); err == nil {
		t.Fatal("an answer missing the secrets was accepted")
	}
	for _, bad := range []string{"", "a/b", "a?b", "a b", "../x"} {
		if _, err := Convert(context.Background(), srv.Client(), srv.URL, bad); !errors.Is(err, ErrBadInput) {
			t.Errorf("code %q: err = %v, want ErrBadInput", bad, err)
		}
	}
}
