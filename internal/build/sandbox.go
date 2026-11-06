package build

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Target is one main package to compile into /out/<Name>.
type Target struct {
	Name string
	Path string // "./cmd/api" or "."
}

// Sandbox runs builds in gVisor containers through the docker CLI (architecture §2.2, §5).
// The container's only network is Network, where the module proxy is the one reachable host.
type Sandbox struct {
	Docker  string // path to the docker binary
	Host    string // DOCKER_HOST of the VM that has runsc
	Network string // internal network shared only with the module proxy
	Proxy   string // module proxy URL as seen from inside the network, e.g. http://modproxy:8080
	// ProxyHost maps the proxy's name to its address ("modproxy:172.30.0.2"): Docker's DNS doesn't work under gVisor.
	ProxyHost string
	Timeout   time.Duration
	// CPUs and MemoryMB cap one build. Builds run one at a time per builder, so they can use most of the
	// machine's spare CPU; the memory budget sits beside the apps' (architecture §7).
	CPUs     float64
	MemoryMB int

	seedsOnce sync.Once
	seeds     *seeds
	active    atomic.Int32 // builds running now; seeds wait for zero so memory stays inside its budget
}

// docker runs a short docker command against the sandbox host and returns its output.
func (s *Sandbox) docker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, s.Docker, args...) //nolint:gosec // fixed binary; arguments are separate and validated
	cmd.Env = []string{"DOCKER_HOST=" + s.Host, "HOME=/tmp"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *Sandbox) cpus() float64 {
	if s.CPUs <= 0 {
		return 2
	}
	return s.CPUs
}

func (s *Sandbox) memoryMB() int {
	if s.MemoryMB <= 0 {
		return 1536
	}
	return s.MemoryMB
}

type Spec struct {
	ID        string // deployment id; names the container so a timed-out build can be killed
	GoVersion string
	GOARCH    string // amd64 or arm64: the CPU of the nodes that will run the binaries
	TeamID    string
	ProjectID string
	Targets   []Target
}

var (
	goVersion  = regexp.MustCompile(`^1\.[0-9]+(\.[0-9]+)?$`)
	targetName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	targetPath = regexp.MustCompile(`^\.(/[A-Za-z0-9_.\-]+)*$`)
	safeID     = regexp.MustCompile(`^[0-9a-f-]{36}$`)
)

// The build script is fixed text. Targets reach it as positional arguments ("$@"), never spliced into it.
// Everything it prints goes to stderr (the build log); stdout carries only the tar of /out.
const script = `set -eu
exec 3>&1 1>&2
cd /src
tar -xzf -
echo "::system:: downloading modules"
go mod download
while [ "$#" -ge 2 ]; do
  echo "::system:: building $1 ($2)"
  go build -ldflags="-s -w" -o "/out/$1" "$2"
  shift 2
done
echo "::system:: go vet (warnings only)"
go vet ./... || echo "::system:: go vet reported problems; the build continues"
tar -cf - -C /out . >&3
`

func (s *Sandbox) validate(spec Spec) error {
	if !safeID.MatchString(spec.ID) || !safeID.MatchString(spec.TeamID) || !safeID.MatchString(spec.ProjectID) {
		return errors.New("invalid build ids")
	}
	if spec.GOARCH != "amd64" && spec.GOARCH != "arm64" {
		return fmt.Errorf("unsupported GOARCH %q", spec.GOARCH)
	}
	if !goVersion.MatchString(spec.GoVersion) {
		return fmt.Errorf("unsupported Go version %q", spec.GoVersion)
	}
	if len(spec.Targets) == 0 {
		return errors.New("nothing to build")
	}
	for _, t := range spec.Targets {
		if !targetName.MatchString(t.Name) || !targetPath.MatchString(t.Path) || strings.Contains(t.Path, "..") {
			return fmt.Errorf("invalid build target %q (%q)", t.Name, t.Path)
		}
	}
	return nil
}

func (s *Sandbox) args(spec Spec) []string {
	args := []string{
		"run", "--rm", "-i",
		"--name", "gs-build-" + spec.ID,
		"--runtime=runsc",
		"--network=" + s.Network,
		"--add-host=" + s.ProxyHost,
		"--memory=" + strconv.Itoa(s.memoryMB()) + "m", "--memory-swap=" + strconv.Itoa(s.memoryMB()) + "m",
		"--cpus=" + strconv.FormatFloat(s.cpus(), 'f', 1, 64), "--pids-limit=512",
		"--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--tmpfs", "/src:rw,size=512m",
		"--tmpfs", "/out:rw,size=512m",
		"--tmpfs", "/tmp:rw,size=1g",
		"-v", "gs-modcache-" + spec.TeamID + ":/go/pkg/mod",
		"-v", "gs-gocache-" + spec.ProjectID + ":/root/.cache/go-build",
		"-e", "GOPROXY=" + s.Proxy,
		"-e", "GOSUMDB=sum.golang.org " + s.Proxy + "/sumdb/sum.golang.org",
		"-e", "GOTOOLCHAIN=local",
		// -trimpath lives in GOFLAGS so go vet compiles packages exactly as go build did and reuses them from
		// the cache instead of type-checking everything again (about 40% less work on a fresh cache).
		"-e", "GOFLAGS=-mod=mod -modcacherw -trimpath",
		"-e", "CGO_ENABLED=0", "-e", "GOOS=linux", "-e", "GOARCH=" + spec.GOARCH,
		"golang:" + spec.GoVersion + "-alpine",
		"sh", "-c", script, "build",
	}
	for _, t := range spec.Targets {
		args = append(args, t.Name, t.Path)
	}
	return args
}

// ErrTimeout means the build ran past Sandbox.Timeout and was killed.
var ErrTimeout = errors.New("build timed out")

// Run builds spec from the source tarball and returns the binaries by target name. Each output line is passed
// to logLine. A non-nil error means the build failed; the log says why.
func (s *Sandbox) Run(ctx context.Context, spec Spec, source []byte, logLine func(stream, line string)) (map[string][]byte, error) {
	if err := s.validate(spec); err != nil {
		return nil, err
	}
	s.active.Add(1)
	defer s.active.Add(-1)
	if s.seedProject(ctx, spec) {
		logLine("system", "starting from the prepared standard library cache for Go "+spec.GoVersion)
	}
	env := []string{"DOCKER_HOST=" + s.Host, "HOME=/tmp"}
	// A crashed earlier attempt of the same deployment may have left its container behind.
	rm := exec.CommandContext(ctx, s.Docker, "rm", "-f", "gs-build-"+spec.ID) //nolint:gosec // fixed binary, validated id
	rm.Env = env
	_ = rm.Run()

	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.Docker, s.args(spec)...) //nolint:gosec // fixed binary; arguments are validated above and passed separately
	cmd.Env = env
	// Killing the docker CLI would leave the container running; kill the container itself.
	cmd.Cancel = func() error {
		kill := exec.Command(s.Docker, "kill", "gs-build-"+spec.ID) //nolint:gosec // fixed binary, validated id
		kill.Env = cmd.Env
		_ = kill.Run()
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 10 * time.Second
	cmd.Stdin = bytes.NewReader(source)
	var out bytes.Buffer
	cmd.Stdout = &limitedBuffer{buf: &out, max: 1 << 30}
	stderr := &lineWriter{emit: func(line string) {
		if rest, ok := strings.CutPrefix(line, "::system:: "); ok {
			logLine("system", rest)
			return
		}
		logLine("build", line)
	}}
	cmd.Stderr = stderr

	err := cmd.Run()
	stderr.Close()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, ErrTimeout
	}
	if err != nil {
		return nil, fmt.Errorf("build exited: %w", err)
	}
	// The next new project on this Go version starts from a prepared cache.
	s.SeedLater(spec.GoVersion, spec.GOARCH)
	return readBinaries(&out, spec.Targets)
}

func readBinaries(r io.Reader, targets []Target) (map[string][]byte, error) {
	want := map[string]bool{}
	for _, t := range targets {
		want[t.Name] = true
	}
	bins := map[string][]byte{}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read build output: %w", err)
		}
		name := strings.TrimPrefix(h.Name, "./")
		if h.Typeflag != tar.TypeReg || !want[name] {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, 512<<20))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		bins[name] = b
	}
	for name := range want {
		if _, ok := bins[name]; !ok {
			return nil, fmt.Errorf("build produced no binary for %s", name)
		}
	}
	return bins, nil
}

type limitedBuffer struct {
	buf *bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.max {
		return 0, errors.New("build output too large")
	}
	return l.buf.Write(p)
}
