package build

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// compile builds a tiny program with the given environment and flags and returns the binary.
func compile(t *testing.T, env []string, flags ...string) []byte {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/v\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "bin")
	cmd := exec.Command("go", append(append([]string{"build"}, flags...), "-o", out, ".")...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestCheckBinary(t *testing.T) {
	good := compile(t, []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64"}, "-trimpath")
	if v, err := CheckBinary("api", good, sum(good), "amd64"); err != nil || !strings.HasPrefix(v, "1.") {
		t.Fatalf("good binary: %q, %v", v, err)
	}

	tests := []struct {
		name string
		bin  []byte
		sha  string
		want string
	}{
		{"wrong cpu", good, sum(good), "built for linux/amd64; Greyship runs linux/arm64"},
		{"checksum", good, sum([]byte("other")), "doesn't match its checksum"},
		{"not a go binary", []byte("#!/bin/sh\necho hi\n"), sum([]byte("#!/bin/sh\necho hi\n")), "isn't a Go binary"},
		{"garbage", bytes.Repeat([]byte{0x7f, 'E', 'L', 'F', 0xff}, 1000), sum(bytes.Repeat([]byte{0x7f, 'E', 'L', 'F', 0xff}, 1000)), "Go binary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arch := "amd64"
			if tt.name == "wrong cpu" {
				arch = "arm64"
			}
			if _, err := CheckBinary("api", tt.bin, tt.sha, arch); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}

	mac := compile(t, []string{"CGO_ENABLED=0", "GOOS=darwin", "GOARCH=" + runtime.GOARCH}, "-trimpath")
	if _, err := CheckBinary("api", mac, sum(mac), "amd64"); err == nil || !strings.Contains(err.Error(), "built for darwin") {
		t.Fatalf("darwin binary: %v", err)
	}
	untrimmed := compile(t, []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64"})
	if _, err := CheckBinary("api", untrimmed, sum(untrimmed), "amd64"); err == nil || !strings.Contains(err.Error(), "-trimpath") {
		t.Fatalf("untrimmed binary: %v", err)
	}
}

func TestInflate(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte("binary"))
	_ = zw.Close()
	if b, err := Inflate(buf.Bytes()); err != nil || string(b) != "binary" {
		t.Fatalf("inflate = %q, %v", b, err)
	}
	if _, err := Inflate([]byte("not gzip")); err == nil {
		t.Fatal("accepted non-gzip")
	}
	if _, err := Inflate(buf.Bytes()[:buf.Len()-6]); err == nil {
		t.Fatal("accepted a truncated upload")
	}
}
