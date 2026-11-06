package fly

import (
	"errors"
	"strings"
	"testing"
	"time"

	"greyship.tech/internal/auth"
)

func TestBuildToken(t *testing.T) {
	now := time.Now()
	s := auth.Signer{Key: []byte(strings.Repeat("k", 32)), Now: func() time.Time { return now }}
	tok, err := SignToken(s, "d1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckToken(s, tok, "d1"); err != nil {
		t.Fatalf("own token refused: %v", err)
	}
	if err := CheckToken(s, tok, "d2"); !errors.Is(err, ErrBadToken) {
		t.Fatal("a token for one deployment must not work for another")
	}
	other := auth.Signer{Key: []byte(strings.Repeat("x", 32)), Now: s.Now}
	if err := CheckToken(other, tok, "d1"); !errors.Is(err, ErrBadToken) {
		t.Fatal("a token signed with another key must be refused")
	}
	// Anything else the API signs (an OAuth state, say) is not a build token.
	state, _ := s.Sign(map[string]string{"deployment": "d1"}, time.Minute)
	if err := CheckToken(s, state, "d1"); !errors.Is(err, ErrBadToken) {
		t.Fatal("a non-build signed value must be refused")
	}
	later := auth.Signer{Key: s.Key, Now: func() time.Time { return now.Add(2 * time.Minute) }}
	if err := CheckToken(later, tok, "d1"); !errors.Is(err, ErrBadToken) {
		t.Fatal("an expired token must be refused")
	}
}

func TestAuthorization(t *testing.T) {
	if got := authorization("FlyV1 fm2_abc,fm2_def"); got != "FlyV1 fm2_abc,fm2_def" {
		t.Fatalf("macaroon token = %q", got)
	}
	if got := authorization(" personal "); got != "Bearer personal" {
		t.Fatalf("personal token = %q", got)
	}
}
