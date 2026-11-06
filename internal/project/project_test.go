package project

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		in       string
		wantName string
		wantSlug string
		wantErr  bool
	}{
		{"Acme API", "Acme API", "acme-api", false},
		{"  orders  ", "orders", "orders", false},
		{"payments_v2", "payments_v2", "payments-v2", false},
		{strings.Repeat("a", 50), strings.Repeat("a", 50), strings.Repeat("a", 40), false},
		{"", "", "", true},
		{"   ", "", "", true},
		{strings.Repeat("a", 65), "", "", true},
		{"1password", "", "", true},
		{"!!!", "", "", true},
		{"Settings", "", "", true},
		{"new", "", "", true},
	}
	for _, tt := range tests {
		name, slug, err := Normalize(tt.in)
		if tt.wantErr {
			var nameErr *NameError
			if !errors.As(err, &nameErr) {
				t.Errorf("Normalize(%q) err = %v, want a NameError", tt.in, err)
			}
			continue
		}
		if err != nil || name != tt.wantName || slug != tt.wantSlug {
			t.Errorf("Normalize(%q) = %q, %q, %v; want %q, %q", tt.in, name, slug, err, tt.wantName, tt.wantSlug)
		}
	}
}
