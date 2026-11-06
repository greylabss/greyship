package mail

import (
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

func TestOutboxWritesPrivateFile(t *testing.T) {
	dir := t.TempDir()
	o := &Outbox{Dir: filepath.Join(dir, "outbox"), Now: func() time.Time { return time.Unix(0, 0) }}
	if err := o.Send(context.Background(), Message{To: "a@b.co", Subject: "123456 is your Greyship code", Text: "code 123456"}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(o.Dir, "*.eml"))
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %v", files)
	}
	info, _ := os.Stat(files[0])
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	body, _ := os.ReadFile(files[0])
	if !strings.Contains(string(body), "code 123456") {
		t.Fatalf("message body missing: %s", body)
	}
}

func TestResend(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"accepted", http.StatusOK, false},
		{"rejected", http.StatusUnprocessableEntity, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer re_test" {
					t.Errorf("missing bearer key")
				}
				_ = json.NewDecoder(r.Body).Decode(&got)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"message":"domain not verified"}`))
			}))
			defer srv.Close()

			r := NewResend("re_test", "Greyship <login@greyship.tech>")
			r.Endpoint = srv.URL
			err := r.Send(context.Background(), Message{To: "a@b.co", Subject: "s", Text: "t"})
			if (err != nil) != tt.wantErr {
				t.Fatalf("Send err = %v, wantErr %v", err, tt.wantErr)
			}
			if got["from"] != "Greyship <login@greyship.tech>" || got["subject"] != "s" {
				t.Fatalf("payload = %v", got)
			}
		})
	}
}
