package sched

import (
	"regexp"
	"strings"
	"testing"
)

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func TestLabel(t *testing.T) {
	long := strings.Repeat("stripe-webhook-", 6)
	tests := []struct {
		service, project, suffix string
		want                     string
	}{
		{"api", "shop", "", "api-shop"},
		{"API", "Acme API", "", "api-acme-api"},
		{"api", "shop", "k7x2", "api-shop-k7x2"},
	}
	for _, tt := range tests {
		if got := Label(tt.service, tt.project, tt.suffix); got != tt.want {
			t.Errorf("Label(%q, %q, %q) = %q, want %q", tt.service, tt.project, tt.suffix, got, tt.want)
		}
	}
	for _, suffix := range []string{"", "k7x2"} {
		got := Label(long, "payments", suffix)
		if len(got) > 63 || !dnsLabel.MatchString(got) {
			t.Fatalf("long label %q (%d chars) is not a valid DNS label", got, len(got))
		}
		if got == Label(long+"x", "payments", suffix) {
			t.Fatal("different long names must not shorten to the same label")
		}
	}
	for i := 0; i < 50; i++ {
		if s := randomSuffix(); len(s) != 4 || !dnsLabel.MatchString(s) {
			t.Fatalf("suffix %q", s)
		}
	}
}
