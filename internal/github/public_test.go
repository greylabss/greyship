package github

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestParseRepoURL(t *testing.T) {
	tests := []struct {
		in, repo, branch string
		ok               bool
	}{
		{"https://github.com/haki/temp", "haki/temp", "", true},
		{"https://github.com/haki/temp.git", "haki/temp", "", true},
		{"https://github.com/haki/temp/", "haki/temp", "", true},
		{"https://github.com/haki/temp/tree/dev", "haki/temp", "dev", true},
		{"https://github.com/haki/temp/tree/feature/login", "haki/temp", "feature/login", true},
		{"github.com/haki/temp", "haki/temp", "", true},
		{"haki/temp", "haki/temp", "", true},
		{"  https://www.github.com/haki/temp  ", "haki/temp", "", true},
		{"https://gitlab.com/haki/temp", "", "", false},
		{"https://github.com.evil.io/haki/temp", "", "", false},
		{"https://github.com/haki", "", "", false},
		{"https://github.com/haki/temp/blob/main/x.go", "", "", false},
		{"file:///etc/passwd", "", "", false},
		{"../../etc", "", "", false},
	}
	for _, tt := range tests {
		repo, branch, err := ParseRepoURL(tt.in)
		if tt.ok != (err == nil) || repo != tt.repo || branch != tt.branch {
			t.Errorf("ParseRepoURL(%q) = %q %q %v", tt.in, repo, branch, err)
		}
		if err != nil && !errors.Is(err, ErrBadURL) {
			t.Errorf("%q: %v, want ErrBadURL", tt.in, err)
		}
	}
}

func pkt(s string) string { return fmt.Sprintf("%04x%s", len(s)+4, s) }

func TestParseRefs(t *testing.T) {
	main := strings.Repeat("a", 40)
	dev := strings.Repeat("b", 40)
	body := pkt("# service=git-upload-pack\n") + "0000" +
		pkt(main+" HEAD\x00multi_ack symref=HEAD:refs/heads/main agent=git/github\n") +
		pkt(main+" refs/heads/main\n") +
		pkt(dev+" refs/heads/dev\n") +
		pkt(strings.Repeat("c", 40)+" refs/tags/v1\n") + "0000"
	def, heads, err := parseRefs(strings.NewReader(body))
	if err != nil || def != "main" || heads["main"] != main || heads["dev"] != dev || len(heads) != 2 {
		t.Fatalf("default %q heads %v err %v", def, heads, err)
	}
	for name, bad := range map[string]string{"garbage": "zzzz", "cut short": pkt(main + " refs/heads/main\n")[:20], "no branches": "0000"} {
		if _, _, err := parseRefs(strings.NewReader(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
