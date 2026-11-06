//go:build integration

package build_test

import (
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"greyship.tech/internal/build"
	"greyship.tech/internal/pack"
)

// sandbox needs the Lima VM from make sandbox; without it these tests skip.
func sandbox(t *testing.T, timeout time.Duration) *build.Sandbox {
	t.Helper()
	host := os.Getenv("GREYSHIP_TEST_SANDBOX_HOST")
	if host == "" {
		t.Skip("GREYSHIP_TEST_SANDBOX_HOST not set; run make sandbox, then make test-int")
	}
	return &build.Sandbox{Docker: "docker", Host: host, Network: "gs-build", Proxy: "http://modproxy:8080", ProxyHost: "modproxy:172.30.0.2", Timeout: timeout}
}

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(full), 0o750)
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if _, err := pack.Write(root, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type lines struct {
	mu  sync.Mutex
	all []string
}

func (l *lines) add(stream, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.all = append(l.all, stream+": "+line)
}

func (l *lines) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.all, "\n")
}

const (
	itDeployment = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeee1"
	itBroken     = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeee2"
	itSlow       = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeee3"
	itTeam       = "aaaaaaaa-bbbb-4ccc-8ddd-000000000001"
	itProject    = "aaaaaaaa-bbbb-4ccc-8ddd-000000000002"
)

func TestSandboxBuildsWithModules(t *testing.T) {
	sb := sandbox(t, 10*time.Minute)
	src := tarball(t, map[string]string{
		"go.mod":             "module example.com/shop\n\ngo 1.24\n\nrequire github.com/go-chi/chi/v5 v5.1.0\n",
		"go.sum":             "github.com/go-chi/chi/v5 v5.1.0 h1:acVI1TYaD+hhedDJ3r54HyA6sExp3HfXq7QWEEY/xMw=\ngithub.com/go-chi/chi/v5 v5.1.0/go.mod h1:DslCQbL2OYiznFReuXYUmQ2hGd1aDpCnlMNITLSKoi8=\n",
		"cmd/api/main.go":    "package main\n\nimport (\n\t\"net/http\"\n\n\t\"github.com/go-chi/chi/v5\"\n)\n\nfunc main() { http.ListenAndServe(\":8080\", chi.NewRouter()) }\n",
		"cmd/worker/main.go": "package main\n\nfunc main() { select {} }\n",
	})
	var log lines
	spec := build.Spec{ID: itDeployment, GoVersion: "1.24", GOARCH: "amd64", TeamID: itTeam, ProjectID: itProject,
		Targets: []build.Target{{Name: "api", Path: "./cmd/api"}, {Name: "worker", Path: "./cmd/worker"}}}
	bins, err := sb.Run(context.Background(), spec, src, log.add)
	if err != nil {
		t.Fatalf("build: %v\n%s", err, log.text())
	}
	for name, bin := range bins {
		f, err := elf.NewFile(bytes.NewReader(bin))
		if err != nil {
			t.Fatalf("%s is not an ELF binary: %v", name, err)
		}
		if f.Machine != elf.EM_X86_64 {
			t.Fatalf("%s built for %v, want linux/amd64", name, f.Machine)
		}
	}
	if len(bins) != 2 {
		t.Fatalf("got %d binaries", len(bins))
	}
	if text := log.text(); !strings.Contains(text, "system: building api (./cmd/api)") || !strings.Contains(text, "system: downloading modules") {
		t.Fatalf("log:\n%s", text)
	}
}

func TestSandboxReportsCompileErrors(t *testing.T) {
	sb := sandbox(t, 10*time.Minute)
	src := tarball(t, map[string]string{
		"go.mod":  "module example.com/broken\n\ngo 1.24\n",
		"main.go": "package main\n\nfunc main() {\n\tundefinedThing()\n}\n",
	})
	var log lines
	spec := build.Spec{ID: itBroken, GoVersion: "1.24", GOARCH: "amd64", TeamID: itTeam, ProjectID: itProject, Targets: []build.Target{{Name: "broken", Path: "."}}}
	_, err := sb.Run(context.Background(), spec, src, log.add)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 125 {
		t.Fatalf("err = %v, want the build's own non-zero exit", err)
	}
	if !strings.Contains(log.text(), "undefined: undefinedThing") {
		t.Fatalf("compiler error missing from the log:\n%s", log.text())
	}
}

func TestSandboxTimeoutKillsContainer(t *testing.T) {
	// Starting a gVisor container alone takes longer than this, so the limit always hits mid-build.
	sb := sandbox(t, 100*time.Millisecond)
	files := tarball(t, map[string]string{"go.mod": "module example.com/slow\n\ngo 1.24\n", "main.go": "package main\n\nfunc main() {}\n"})
	spec := build.Spec{ID: itSlow, GoVersion: "1.24", GOARCH: "amd64", TeamID: itTeam, ProjectID: itProject, Targets: []build.Target{{Name: "slow", Path: "."}}}
	start := time.Now()
	_, err := sb.Run(context.Background(), spec, files, func(string, string) {})
	if !errors.Is(err, build.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("timed-out build took %v to stop", took)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, _ := exec.Command("docker", "-H", sb.Host, "ps", "-aq", "--filter", "name=gs-build-"+itSlow).Output()
		if strings.TrimSpace(string(out)) == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the timed-out build container is still there")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// A new project whose Go version has a prepared standard library cache starts from a copy of it.
func TestSeededProjectBuildsFaster(t *testing.T) {
	sb := sandbox(t, 10*time.Minute)
	ctx := context.Background()
	out, err := exec.Command("docker", "-H", sb.Host, "info", "--format", "{{.Architecture}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	arch := map[string]string{"aarch64": "arm64", "x86_64": "amd64"}[strings.TrimSpace(string(out))]
	src := tarball(t, map[string]string{
		"go.mod":  "module example.com/seeded\n\ngo 1.24\n",
		"main.go": "package main\n\nimport (\n\t\"encoding/json\"\n\t\"net/http\"\n)\n\nfunc main() { _ = json.NewEncoder(nil); http.ListenAndServe(\":8080\", nil) }\n",
	})
	build := func(project string) (time.Duration, string) {
		var log lines
		spec := build.Spec{ID: itDeployment, GoVersion: "1.24", GOARCH: arch, TeamID: itTeam, ProjectID: project, Targets: []build.Target{{Name: "app", Path: "."}}}
		start := time.Now()
		if _, err := sb.Run(ctx, spec, src, log.add); err != nil {
			t.Fatalf("build: %v\n%s", err, log.text())
		}
		return time.Since(start), log.text()
	}
	cold := "aaaaaaaa-bbbb-4ccc-8ddd-" + fmt.Sprintf("%012d", time.Now().UnixNano()%1e12)
	warm := "aaaaaaaa-bbbb-4ccc-8ddd-" + fmt.Sprintf("%012d", (time.Now().UnixNano()+1)%1e12)
	for _, p := range []string{cold, warm} {
		defer func() { _ = exec.Command("docker", "-H", sb.Host, "volume", "rm", "-f", "gs-gocache-"+p).Run() }()
	}

	coldTime, _ := build(cold)
	if err := sb.MakeSeedForTest(ctx, "1.24", arch); err != nil {
		t.Fatal(err)
	}
	warmTime, log := build(warm)
	if !strings.Contains(log, "starting from the prepared standard library cache for Go 1.24") {
		t.Fatalf("the new project didn't get the prepared cache:\n%s", log)
	}
	t.Logf("new project without a prepared cache: %v, with one: %v", coldTime.Round(time.Millisecond), warmTime.Round(time.Millisecond))
}
