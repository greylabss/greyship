//go:build integration

package agent_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"greyship.tech/internal/agent"
	"greyship.tech/internal/build"
	"greyship.tech/internal/pack"
	"greyship.tech/internal/runtime/gvisor"
)

type memStorage map[string][]byte

func (m memStorage) Get(_ context.Context, key string, _ int64) ([]byte, error) {
	b, ok := m[key]
	if !ok {
		return nil, errors.New("no such object")
	}
	return b, nil
}

type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) add(_, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, line)
}

func (l *logs) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func uuid() (string, error) {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

type env struct {
	agent *agent.Agent
	host  string
	art   agent.Artifact
	ids   *idLog
}

// idLog remembers the instance ids a test's agent made, so checks look only at this test's containers.
type idLog struct {
	mu  sync.Mutex
	ids []string
}

func (l *idLog) newID() (string, error) {
	id, err := uuid()
	l.mu.Lock()
	l.ids = append(l.ids, id)
	l.mu.Unlock()
	return id, err
}

func (l *idLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ids...)
}

// setup builds guest init and the test app for the VM's CPU and returns an agent on the sandbox VM.
func setup(t *testing.T) env {
	t.Helper()
	host := os.Getenv("GREYSHIP_TEST_SANDBOX_HOST")
	if host == "" {
		t.Skip("GREYSHIP_TEST_SANDBOX_HOST not set; run make sandbox, then make test-int")
	}
	out, err := exec.Command("docker", "-H", host, "info", "--format", "{{.Architecture}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	goarch := map[string]string{"aarch64": "arm64", "x86_64": "amd64"}[strings.TrimSpace(string(out))]
	dir := t.TempDir()
	build := func(pkg, name string) []byte {
		cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, name), pkg)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+goarch)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", pkg, err, b)
		}
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return b
	}
	init := build("greyship.tech/cmd/greyship-init", "init")
	app := build("./testdata/app", "app")
	sum := sha256.Sum256(app)
	art := agent.Artifact{ObjectKey: "artifacts/test/app", SHA256: hex.EncodeToString(sum[:])}
	driver := &gvisor.Driver{Docker: "docker", Host: host, Shared: "/var/lib/greyship-sandbox"}
	ids := &idLog{}
	a := &agent.Agent{
		Driver: driver, Images: driver, Storage: memStorage{art.ObjectKey: app}, Init: init,
		Region: "dev", Logger: slog.New(slog.DiscardHandler), NewID: ids.newID,
	}
	return env{agent: a, host: host, art: art, ids: ids}
}

func (e env) docker(t *testing.T, args ...string) string {
	t.Helper()
	out, _ := exec.Command("docker", append([]string{"-H", e.host}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out))
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestWebInstance(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var out logs
	in := agent.Instance{DeploymentID: "d1", Service: "api", Type: "web", Artifact: e.art, MemoryMB: 128, CPU: 0.25,
		Env: map[string]string{"STRIPE_KEY": "sk_live_must_not_leak"}}
	run, err := e.agent.Start(ctx, in, out.add)
	if err != nil {
		t.Fatalf("start: %v\n%s", err, out.text())
	}
	defer func() { _ = e.agent.Stop(ctx, run) }()

	if body := get(t, "http://"+run.Addr+"/"); body != "hello from greyship" {
		t.Fatalf("app answered %q", body)
	}
	if text := out.text(); !strings.Contains(text, "env GOMAXPROCS=1 GOMEMLIMIT=115MiB GREYSHIP_SERVICE=api PORT=8080") || !strings.Contains(text, "uid 65534") {
		t.Fatalf("app output:\n%s", text)
	}

	name := "gs-i-" + run.ID
	inspect := e.docker(t, "inspect", name)
	if strings.Contains(inspect, "sk_live_must_not_leak") {
		t.Fatal("a secret reached the container's stored configuration")
	}
	for _, want := range []string{`"Runtime": "runsc"`, `"ReadonlyRootfs": true`, `"User": "65534:65534"`, `"ALL"`} {
		if !strings.Contains(inspect, want) {
			t.Errorf("docker inspect missing %s", want)
		}
	}

	// Isolation, tested from inside the instance: no new connections to the node, none to another instance,
	// outbound internet allowed (architecture §5).
	gateway := e.docker(t, "network", "inspect", "--format", "{{(index .IPAM.Config 0).Gateway}}", name)
	other, err := e.agent.Start(ctx, agent.Instance{DeploymentID: "d2", Service: "other", Type: "web", Artifact: e.art, MemoryMB: 128, CPU: 0.25}, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.agent.Stop(ctx, other) }()
	otherIP := e.docker(t, "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", "gs-i-"+other.ID)
	probes := []struct {
		addr string
		want string
	}{
		{gateway + ":22", "blocked"},
		{otherIP + ":8080", "blocked"},
		{"1.1.1.1:443", "open"},
	}
	for _, p := range probes {
		if got := get(t, "http://"+run.Addr+"/probe?addr="+p.addr); got != p.want {
			t.Errorf("instance -> %s = %s, want %s", p.addr, got, p.want)
		}
	}

	if err := e.agent.Stop(ctx, run); err != nil {
		t.Fatal(err)
	}
	if left := e.docker(t, "ps", "-aq", "--filter", "name="+name); left != "" {
		t.Fatal("container still there after Stop")
	}
	if left := e.docker(t, "network", "ls", "-q", "--filter", "name="+name); left != "" {
		t.Fatal("network still there after Stop")
	}
	select {
	case <-run.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("Exited never fired after Stop")
	}
}

func TestHealthFailures(t *testing.T) {
	e := setup(t)
	tests := []struct {
		mode   string
		reason string
		output string
	}{
		{"exit", "exited with code 2", "boom: missing DATABASE_URL"},
		{"silent", "didn't start listening on any port within 30s", "env GOMAXPROCS"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			in := agent.Instance{DeploymentID: "d1", Service: "api", Type: "web", Artifact: e.art, MemoryMB: 128, CPU: 0.25, Env: map[string]string{"MODE": tt.mode}}
			_, err := e.agent.Start(context.Background(), in, func(string, string) {})
			var he *agent.HealthError
			if !errors.As(err, &he) || !strings.Contains(he.Reason, tt.reason) {
				t.Fatalf("err = %v, want health error %q", err, tt.reason)
			}
			if !strings.Contains(strings.Join(he.Output, "\n"), tt.output) {
				t.Fatalf("output = %q, want it to include %q", he.Output, tt.output)
			}
			for _, id := range e.ids.all() {
				if left := e.docker(t, "ps", "-aq", "--filter", "name=gs-i-"+id); left != "" {
					t.Fatalf("a failed instance was left running: %s", left)
				}
			}
		})
	}
}

// A web app may listen anywhere; it's served on its URL either way (docs/plans/any-port.md).
func TestAnyPort(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	tests := []struct {
		name string
		env  map[string]string
		log  string
	}{
		{"reads PORT", nil, ""},
		{"hard-coded port", map[string]string{"ADDR": ":3000"}, "api listens on port 3000; serving it on its URL"},
		{"localhost only", map[string]string{"ADDR": "127.0.0.1:3000"}, "api listens on port 3000"},
		{"localhost on 8080", map[string]string{"ADDR": "127.0.0.1:8080"}, "api listens on port 8080"},
		{"two ports", map[string]string{"ADDR": ":4000", "EXTRA": ":6060"}, "listens on port 4000; serving it on its URL (it also listens on 6060, which isn't served)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &logs{}
			in := agent.Instance{DeploymentID: "d1", Service: "api", Type: "web", Artifact: e.art, MemoryMB: 128, CPU: 0.25, Env: tt.env}
			run, err := e.agent.Start(ctx, in, out.add)
			if err != nil {
				t.Fatalf("start: %v\n%s", err, out.text())
			}
			defer func() { _ = e.agent.Stop(ctx, run) }()
			for range 3 {
				if body := get(t, "http://"+run.Addr+"/"); body != "hello from greyship" {
					t.Fatalf("app answered %q", body)
				}
			}
			if text := out.text(); tt.log == "" && strings.Contains(text, "greyship:") || !strings.Contains(text, tt.log) {
				t.Fatalf("log:\n%s\nwant %q", text, tt.log)
			}
		})
	}
}

// greyship.toml's files are in the app's working folder at their repo paths, read-only.
func TestRuntimeFiles(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var bundle bytes.Buffer
	src := fstest.MapFS{"scripts/install.sh": {Data: []byte("#!/bin/sh\necho hi\n")}, "templates/a/home.html": {Data: []byte("<h1>hi</h1>")}}
	if _, err := pack.BundleFS(src, []string{"scripts/install.sh", "templates/"}, &bundle); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(bundle.Bytes())
	files := &agent.Artifact{ObjectKey: "files/p/bundle.tar.gz", SHA256: hex.EncodeToString(sum[:])}
	e.agent.Storage.(memStorage)[files.ObjectKey] = bundle.Bytes()

	run, err := e.agent.Start(ctx, agent.Instance{DeploymentID: "d1", Service: "api", Type: "web", Artifact: e.art, Files: files, MemoryMB: 128, CPU: 0.25}, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.agent.Stop(ctx, run) }()
	for name, want := range map[string]string{"scripts/install.sh": "#!/bin/sh\necho hi\n", "templates/a/home.html": "<h1>hi</h1>"} {
		if got := get(t, "http://"+run.Addr+"/file?name="+name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got := get(t, "http://"+run.Addr+"/write"); got != "refused" {
		t.Fatalf("writing a shipped file = %s, want refused", got)
	}

	plain, err := e.agent.Start(ctx, agent.Instance{DeploymentID: "d2", Service: "api", Type: "web", Artifact: e.art, MemoryMB: 128, CPU: 0.25}, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.agent.Stop(ctx, plain) }()
	if got := get(t, "http://"+plain.Addr+"/file?name=scripts/install.sh"); !strings.HasPrefix(got, "error:") {
		t.Fatalf("without files the app read %q", got)
	}
}

func TestWorkerInstance(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	in := agent.Instance{DeploymentID: "d1", Service: "worker", Type: "worker", Artifact: e.art, MemoryMB: 128, CPU: 0.25, Env: map[string]string{"MODE": "worker"}}
	run, err := e.agent.Start(ctx, in, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if run.Addr != "" {
		t.Fatalf("worker got an address %q", run.Addr)
	}
	if err := e.agent.Stop(ctx, run); err != nil {
		t.Fatal(err)
	}
}

// The step 9 "done when": an app built by the builder's sandbox starts in its own sandbox and passes its health check.
func TestBuiltArtifactRuns(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	goarch := map[string]string{"aarch64": "arm64", "x86_64": "amd64"}[e.docker(t, "info", "--format", "{{.Architecture}}")]

	var src bytes.Buffer
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "cmd", "api"), 0o750)
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/shop\n\ngo 1.24\n"), 0o600)
	_ = os.WriteFile(filepath.Join(root, "cmd", "api", "main.go"), []byte("package main\n\nimport (\n\t\"fmt\"\n\t\"net/http\"\n\t\"os\"\n)\n\nfunc main() {\n\thttp.HandleFunc(\"/\", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, \"built by greyship\") })\n\thttp.ListenAndServe(\":\"+os.Getenv(\"PORT\"), nil)\n}\n"), 0o600)
	if _, err := pack.Write(root, &src); err != nil {
		t.Fatal(err)
	}
	sb := &build.Sandbox{Docker: "docker", Host: e.host, Network: "gs-build", Proxy: "http://modproxy:8080", ProxyHost: "modproxy:172.30.0.2", Timeout: 10 * time.Minute}
	id, _ := uuid()
	bins, err := sb.Run(ctx, build.Spec{ID: id, GoVersion: "1.24", GOARCH: goarch, TeamID: id, ProjectID: id, Targets: []build.Target{{Name: "api", Path: "./cmd/api"}}}, src.Bytes(), func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(bins["api"])
	art := agent.Artifact{ObjectKey: "artifacts/built/api", SHA256: hex.EncodeToString(sum[:])}
	e.agent.Storage.(memStorage)[art.ObjectKey] = bins["api"]

	run, err := e.agent.Start(ctx, agent.Instance{DeploymentID: id, Service: "api", Type: "web", Artifact: art}, func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.agent.Stop(ctx, run) }()
	if body := get(t, "http://"+run.Addr+"/"); body != "built by greyship" {
		t.Fatalf("app answered %q", body)
	}
}
