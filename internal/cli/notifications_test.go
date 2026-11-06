package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNotificationsCommand(t *testing.T) {
	var patched map[string][]notifySetting
	settings := `{"settings":[{"kind":"deploy.succeeded","email":true,"push":true},{"kind":"deploy.failed","email":true,"push":true},{"kind":"project.created","email":true,"push":true}],"push_available":true}`
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/me", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"user":{"id":"u1","email":"a@b.co","token_team":"haki"}}`))
	})
	mux.HandleFunc("GET /v1/me/notifications/settings", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(settings)) })
	mux.HandleFunc("PATCH /v1/me/notifications/settings", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&patched)
		b, _ := json.Marshal(map[string]any{"settings": patched["settings"], "push_available": true})
		_, _ = w.Write(b)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := runIn(t, srv, t.TempDir(), "", false, "notifications")
	if err != nil || !strings.Contains(out, "deploy.succeeded") || !strings.Contains(out, "EMAIL") {
		t.Fatalf("show: %v\n%s", err, out)
	}
	out, err = runIn(t, srv, t.TempDir(), "", false, "notifications", "email", "off", "deploy.succeeded")
	if err != nil || len(patched["settings"]) != 3 || patched["settings"][0].Email || !patched["settings"][1].Email {
		t.Fatalf("email off: %v %+v\n%s", err, patched, out)
	}
	if _, err := runIn(t, srv, t.TempDir(), "", false, "notifications", "push", "maybe"); err == nil {
		t.Fatal("a state other than on/off was accepted")
	}
}
