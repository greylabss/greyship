package beta

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsTester(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/testers/check" || r.Header.Get("Authorization") != "Bearer s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		_ = json.NewEncoder(w).Encode(map[string]bool{"tester": in["email"] == "ada@example.com"})
	}))
	defer srv.Close()

	c := New(srv.URL+"/", "s3cret")
	for email, want := range map[string]bool{"ada@example.com": true, "bob@example.com": false} {
		got, err := c.IsTester(context.Background(), email)
		if err != nil || got != want {
			t.Errorf("IsTester(%s) = %v, %v; want %v", email, got, err, want)
		}
	}
	if _, err := New(srv.URL, "wrong").IsTester(context.Background(), "ada@example.com"); err == nil {
		t.Fatal("a refused request must be an error, not a no")
	}
}
