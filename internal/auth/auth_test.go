package auth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTokensAreRandomAndHashed(t *testing.T) {
	a, _ := newToken()
	b, _ := newToken()
	if a == b || len(a) < 40 {
		t.Fatalf("tokens not random or too short: %q %q", a, b)
	}
	if string(hashToken(a)) == a {
		t.Fatal("token stored unhashed")
	}
}

func TestSigner(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	s := Signer{Key: []byte(strings.Repeat("k", 32)), Now: func() time.Time { return now }}
	type payload struct{ State string }

	value, err := s.Sign(payload{"abc"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		value string
		at    time.Time
		key   string
		ok    bool
	}{
		{"valid", value, now, "", true},
		{"tampered payload", "x" + value, now, "", false},
		{"tampered mac", value[:len(value)-2] + "AA", now, "", false},
		{"expired", value, now.Add(2 * time.Minute), "", false},
		{"other key", value, now, strings.Repeat("z", 32), false},
		{"garbage", "not-a-signed-value", now, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := s
			v.Now = func() time.Time { return tt.at }
			if tt.key != "" {
				v.Key = []byte(tt.key)
			}
			var got payload
			err := v.Verify(tt.value, &got)
			if tt.ok && (err != nil || got.State != "abc") {
				t.Fatalf("Verify = %v, %+v", err, got)
			}
			if !tt.ok && !errors.Is(err, ErrBadSignature) {
				t.Fatalf("Verify err = %v, want ErrBadSignature", err)
			}
		})
	}
}

func TestSlugFrom(t *testing.T) {
	tests := []struct{ email, want string }{
		{"haki@acme.dev", "haki"},
		{"Ada.Lovelace@x.io", "ada-lovelace"},
		{"bob+greyship@x.io", "bob"},
		{"admin@x.io", "team-admin"},
		{"pricing@x.io", "team-pricing"},
		{"blog@x.io", "team-blog"},
		{"42@x.io", "team-42"},
		{"___@x.io", "team"},
		{"a-very-long-local-part-that-keeps-going-forever@x.io", "a-very-long-local-part-that-ke"},
	}
	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			if got := slugFrom(tt.email); got != tt.want {
				t.Fatalf("slugFrom(%q) = %q, want %q", tt.email, got, tt.want)
			}
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"  Haki@Acme.DEV ", "haki@acme.dev", true},
		{"a@b.co", "a@b.co", true},
		{"no-at-sign", "", false},
		{"a@localhost", "", false},
		{"Haki <haki@acme.dev>", "", false},
		{strings.Repeat("a", 250) + "@b.co", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := NormalizeEmail(tt.in)
			if tt.ok && (err != nil || got != tt.want) {
				t.Fatalf("NormalizeEmail(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidEmail) {
				t.Fatalf("NormalizeEmail(%q) err = %v, want ErrInvalidEmail", tt.in, err)
			}
		})
	}
}
