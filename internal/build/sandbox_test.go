package build

import (
	"archive/tar"
	"bytes"
	"slices"
	"strings"
	"testing"
)

const (
	depID  = "39444b61-b723-4fb7-83ed-df5916432dea"
	teamID = "981a717f-89b6-4c12-96c9-44c6d047b219"
	projID = "cc5b78f6-f500-44fd-8cd2-c7d1da8a2e7b"
)

func spec(targets ...Target) Spec {
	return Spec{ID: depID, GoVersion: "1.24", GOARCH: "amd64", TeamID: teamID, ProjectID: projID, Targets: targets}
}

func TestValidate(t *testing.T) {
	s := &Sandbox{}
	tests := []struct {
		name string
		spec Spec
		ok   bool
	}{
		{"normal", spec(Target{"api", "./cmd/api"}, Target{"worker", "./cmd/worker"}), true},
		{"root main", spec(Target{"shop", "."}), true},
		{"patch version", Spec{ID: depID, GoVersion: "1.24.3", GOARCH: "arm64", TeamID: teamID, ProjectID: projID, Targets: []Target{{"api", "./cmd/api"}}}, true},
		{"no targets", spec(), false},
		{"flag as path", spec(Target{"api", "-toolexec=/bin/sh"}), false},
		{"parent path", spec(Target{"api", "./../etc"}), false},
		{"absolute path", spec(Target{"api", "/etc"}), false},
		{"name with slash", spec(Target{"../x", "./cmd/x"}), false},
		{"name with space", spec(Target{"a b", "./cmd/x"}), false},
		{"shell in path", spec(Target{"api", "./cmd/$(id)"}), false},
		{"image injection", Spec{ID: depID, GoVersion: "1.24-alpine@sha256:x", GOARCH: "amd64", TeamID: teamID, ProjectID: projID, Targets: []Target{{"api", "."}}}, false},
		{"bad arch", Spec{ID: depID, GoVersion: "1.24", GOARCH: "riscv64", TeamID: teamID, ProjectID: projID, Targets: []Target{{"api", "."}}}, false},
		{"bad id", Spec{ID: "x; rm -rf /", GoVersion: "1.24", GOARCH: "amd64", TeamID: teamID, ProjectID: projID, Targets: []Target{{"api", "."}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.validate(tt.spec); (err == nil) != tt.ok {
				t.Fatalf("validate = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestArgsLockTheSandboxDown(t *testing.T) {
	s := &Sandbox{Network: "gs-build", Proxy: "http://modproxy:8080", ProxyHost: "modproxy:172.30.0.2"}
	args := s.args(spec(Target{"api", "./cmd/api"}, Target{"worker", "./cmd/worker"}))
	for _, want := range []string{"--runtime=runsc", "--network=gs-build", "--add-host=modproxy:172.30.0.2", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=512", "--memory=1536m", "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOARCH=amd64", "golang:1.24-alpine"} {
		if !slices.Contains(args, want) {
			t.Errorf("args missing %s", want)
		}
	}
	for _, a := range args {
		if strings.Contains(a, "--privileged") || strings.HasPrefix(a, "--network=host") || strings.Contains(a, "docker.sock") {
			t.Fatalf("unsafe argument %q", a)
		}
	}
	// Targets come after the fixed script as separate arguments, never inside it.
	i := slices.Index(args, script)
	if i < 0 || !slices.Equal(args[i+1:], []string{"build", "api", "./cmd/api", "worker", "./cmd/worker"}) {
		t.Fatalf("targets not passed as positional arguments: %q", args[i+1:])
	}
	if strings.Contains(script, "api") {
		t.Fatal("the script must not mention targets")
	}
}

func TestReadBinaries(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(name string, typ byte, body string) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: typ, Size: int64(len(body)), Mode: 0o755})
		_, _ = tw.Write([]byte(body))
	}
	add("./", tar.TypeDir, "")
	add("./api", tar.TypeReg, "ELF-api")
	add("./extra", tar.TypeReg, "ignored")
	_ = tw.Close()

	bins, err := readBinaries(bytes.NewReader(buf.Bytes()), []Target{{"api", "./cmd/api"}})
	if err != nil || string(bins["api"]) != "ELF-api" || len(bins) != 1 {
		t.Fatalf("bins = %v, err = %v", bins, err)
	}
	if _, err := readBinaries(bytes.NewReader(buf.Bytes()), []Target{{"api", "."}, {"worker", "."}}); err == nil || !strings.Contains(err.Error(), "worker") {
		t.Fatalf("missing binary not reported: %v", err)
	}
}

func TestVetSharesTheBuildCache(t *testing.T) {
	s := &Sandbox{Network: "gs-build", Proxy: "http://modproxy:8080", ProxyHost: "modproxy:172.30.0.2", CPUs: 4, MemoryMB: 2560}
	args := s.args(spec(Target{"api", "./cmd/api"}))
	if !slices.Contains(args, "GOFLAGS=-mod=mod -modcacherw -trimpath") {
		t.Fatal("-trimpath must be in GOFLAGS so go vet reuses what go build compiled")
	}
	if !slices.Contains(args, "--cpus=4.0") || !slices.Contains(args, "--memory=2560m") {
		t.Fatalf("limits not applied: %v", args)
	}
	if strings.Index(script, "go build") > strings.Index(script, "go vet") {
		t.Fatal("build before vet: compile errors show first and vet reuses the build's work")
	}
	if strings.Contains(script, "-trimpath") {
		t.Fatal("-trimpath belongs in GOFLAGS, not on one command")
	}
}
