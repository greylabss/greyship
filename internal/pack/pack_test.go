package pack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Cases from git's gitignore documentation plus the edge cases grey deploy relies on.
func TestIgnore(t *testing.T) {
	tests := []struct {
		name     string
		patterns string
		base     string
		path     string
		dir      bool
		want     bool
	}{
		{"plain name at any depth", "debug.log", "", "a/b/debug.log", false, true},
		{"star", "*.log", "", "logs/x.log", false, true},
		{"star does not cross slash", "logs/*.log", "", "logs/a/x.log", false, false},
		{"anchored", "/bin", "", "bin", true, true},
		{"anchored not deeper", "/bin", "", "cmd/bin", true, false},
		{"middle slash anchors", "doc/frotz", "", "a/doc/frotz", true, false},
		{"dir only skips files", "build/", "", "build", false, false},
		{"dir only matches dirs", "build/", "", "x/build", true, true},
		{"leading double star", "**/foo", "", "a/b/foo", false, true},
		{"trailing double star", "abc/**", "", "abc/x/y", false, true},
		{"trailing double star not the dir", "abc/**", "", "abc", true, false},
		{"middle double star zero dirs", "a/**/b", "", "a/b", false, true},
		{"middle double star many dirs", "a/**/b", "", "a/x/y/b", false, true},
		{"question mark", "file?.txt", "", "file1.txt", false, true},
		{"class", "file[0-9].txt", "", "filea.txt", false, false},
		{"negation re-includes", "*.log\n!keep.log", "", "keep.log", false, false},
		{"last match wins", "!keep.log\n*.log", "", "keep.log", false, true},
		{"comment and blank", "# *.go\n\n", "", "main.go", false, false},
		{"escaped hash", `\#notes`, "", "#notes", false, true},
		{"nested file applies below its folder", "secret.txt", "cmd", "cmd/api/secret.txt", false, true},
		{"nested file not outside its folder", "secret.txt", "cmd", "secret.txt", false, false},
		{"nested anchored is relative to its folder", "/gen", "cmd", "cmd/gen", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := parseIgnore(strings.NewReader(tt.patterns), tt.base)
			if got := ignored(rules, tt.path, tt.dir); got != tt.want {
				t.Fatalf("ignored(%q) with %q = %v, want %v", tt.path, tt.patterns, got, tt.want)
			}
		})
	}
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func paths(files []File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func TestList(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":                 "module x",
		"main.go":                "package main",
		".gitignore":             "bin/\n*.log\n",
		".greyshipignore":        "docs/\n",
		".env":                   "A=1",
		".env.prod":              "SECRET=1",
		".env.example":           "A=",
		"cmd/api/.env.local":     "B=2",
		"cmd/api/main.go":        "package main",
		"cmd/api/.gitignore":     "fixtures/\n!important.log\n",
		"cmd/api/important.log":  "keep",
		"cmd/api/fixtures/a.txt": "x",
		"bin/api":                "binary",
		"debug.log":              "x",
		"docs/readme.md":         "x",
		".git/config":            "x",
		"node_modules/a/b.js":    "x",
		".greyship/project.json": `{"id":"x"}`,
	})
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "passwd")); err != nil {
		t.Fatal(err)
	}
	files, symlinks, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".gitignore", ".greyship/project.json", ".greyshipignore", "cmd/api/.gitignore", "cmd/api/important.log", "cmd/api/main.go", "go.mod", "main.go"}
	if got := paths(files); !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v\nwant    %v", got, want)
	}
	if !reflect.DeepEqual(symlinks, []string{"passwd"}) {
		t.Fatalf("symlinks = %v", symlinks)
	}
}

func TestWriteIsDeterministic(t *testing.T) {
	files := map[string]string{"go.mod": "module x", "main.go": "package main", "cmd/w/main.go": "package main"}
	a, b := writeTree(t, files), writeTree(t, files)
	if err := os.Chtimes(filepath.Join(b, "main.go"), timeAgo(), timeAgo()); err != nil {
		t.Fatal(err)
	}
	var bufA, bufB bytes.Buffer
	if _, err := Write(a, &bufA); err != nil {
		t.Fatal(err)
	}
	res, err := Write(b, &bufB)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(bufA.Bytes()) != sha256.Sum256(bufB.Bytes()) {
		t.Fatal("same files packed to different bytes")
	}
	if res.Files != 3 || res.Bytes != int64(len("module x")+2*len("package main")) {
		t.Fatalf("result = %+v", res)
	}

	gz, err := gzip.NewReader(&bufA)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag != tar.TypeReg || strings.HasPrefix(h.Name, "/") || strings.Contains(h.Name, "..") || h.Uid != 0 || h.Uname != "" {
			t.Fatalf("bad entry %+v", h)
		}
		names = append(names, h.Name)
	}
	if !reflect.DeepEqual(names, []string{"cmd/w/main.go", "go.mod", "main.go"}) {
		t.Fatalf("entries = %v", names)
	}
}

func TestWriteTooLarge(t *testing.T) {
	root := writeTree(t, map[string]string{"go.mod": "module x"})
	big, err := os.Create(filepath.Join(root, "blob.bin"))
	if err != nil {
		t.Fatal(err)
	}
	// Random-looking data doesn't compress, so the tarball passes the limit.
	chunk := make([]byte, 1<<20)
	seed := uint32(1)
	for i := range chunk {
		seed = seed*1664525 + 1013904223
		chunk[i] = byte(seed >> 24)
	}
	for i := 0; i < MaxSize>>20+2; i++ {
		chunk[0] = byte(i)
		if _, err := big.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	_ = big.Close()

	_, err = Write(root, io.Discard)
	var tooLarge *ErrTooLarge
	if !errors.As(err, &tooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if tooLarge.Largest[0].Path != "blob.bin" {
		t.Fatalf("largest = %+v", tooLarge.Largest)
	}
}

func timeAgo() time.Time { return time.Now().Add(-48 * time.Hour) }
