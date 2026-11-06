package auth

import (
	"strings"
	"testing"
)

func TestPasswordHash(t *testing.T) {
	h, err := hashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "pbkdf2-sha256$600000$") || strings.Contains(h, "correct") {
		t.Fatalf("hash = %q", h)
	}
	again, _ := hashPassword("correct horse battery")
	if h == again {
		t.Fatal("two hashes of one password are equal: the salt isn't random")
	}
	tests := []struct {
		name, stored, password string
		want                   bool
	}{
		{"right", h, "correct horse battery", true},
		{"wrong", h, "correct horse batter", false},
		{"empty", h, "", false},
		{"no hash", "", "correct horse battery", false},
		{"unknown scheme", "md5$1$aa$bb", "x", false},
		{"truncated", "pbkdf2-sha256$600000$AAAA", "x", false},
		{"absurd cost", "pbkdf2-sha256$999999999$AAAAAAAAAAAAAAAAAAAAAA$AAAA", "x", false},
	}
	for _, tt := range tests {
		if got := passwordMatches(tt.stored, tt.password); got != tt.want {
			t.Errorf("%s: matches = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestCheckPassword(t *testing.T) {
	for pw, ok := range map[string]bool{"": false, "short": false, "123456789": false, "1234567890": true, strings.Repeat("a", 128): true, strings.Repeat("a", 129): false, "pässwörd-ünïcode": true} {
		if got := checkPassword(pw) == nil; got != ok {
			t.Errorf("checkPassword(%d chars) ok = %v, want %v", len(pw), got, ok)
		}
	}
}
