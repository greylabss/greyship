// Package slug turns names into URL- and DNS-safe labels: lowercase letters, digits and single dashes.
package slug

import (
	"regexp"
	"strings"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Make slugifies s and cuts it to at most max bytes without leaving a trailing dash.
func Make(s string, max int) string {
	out := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(out) > max {
		out = strings.TrimRight(out[:max], "-")
	}
	return out
}
