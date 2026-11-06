package deploy

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckBinaries(t *testing.T) {
	sha := strings.Repeat("a", 64)
	ok := Binary{Service: "api", Path: "./cmd/api", Type: "web", SHA256: sha, SizeBytes: 10, GzBytes: 5}
	with := func(f func(*Binary)) []Binary {
		b := ok
		f(&b)
		return []Binary{b}
	}
	many := make([]Binary, maxBinaries+1)
	for i := range many {
		many[i] = ok
		many[i].Service = "s" + strings.Repeat("x", i)
	}
	release := ok
	release.Service, release.Type = "migrate", "release"
	release2 := release
	release2.Service = "seed"

	tests := []struct {
		name string
		bins []Binary
		ok   bool
	}{
		{"one web service", []Binary{ok}, true},
		{"root path", with(func(b *Binary) { b.Path = "." }), true},
		{"service and release", []Binary{ok, release}, true},
		{"none", nil, false},
		{"too many", many, false},
		{"duplicate name", []Binary{ok, ok}, false},
		{"bad name", with(func(b *Binary) { b.Service = "API" }), false},
		{"parent path", with(func(b *Binary) { b.Path = "./../etc" }), false},
		{"absolute path", with(func(b *Binary) { b.Path = "/cmd/api" }), false},
		{"bad type", with(func(b *Binary) { b.Type = "daemon" }), false},
		{"bad sha", with(func(b *Binary) { b.SHA256 = "abc" }), false},
		{"empty", with(func(b *Binary) { b.SizeBytes = 0 }), false},
		{"too big", with(func(b *Binary) { b.SizeBytes = maxBinaryRaw + 1 }), false},
		{"too big compressed", with(func(b *Binary) { b.GzBytes = maxBinaryGzip + 1 }), false},
		{"two release commands", []Binary{ok, release, release2}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkBinaries(tt.bins)
			if tt.ok != (err == nil) || (err != nil && !errors.Is(err, ErrBadBinary)) {
				t.Fatalf("checkBinaries = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestBinaryKeyIsPerProject(t *testing.T) {
	if binaryKey("p1", "ab") == binaryKey("p2", "ab") {
		t.Fatal("projects must not share binary keys")
	}
}
