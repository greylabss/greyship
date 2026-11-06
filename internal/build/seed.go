package build

import (
	"context"
	"sync"
	"time"
)

// Pre-built standard library caches ("seeds"), one per Go version and CPU. A project's first build normally
// compiles the whole standard library; with a seed its build cache starts as a copy of one instead.
//
// Safety: a shared, writable build cache could be poisoned to slip code into someone else's binary. Seeds are
// built only by the platform, from the official Go image, with no network and no user input beyond a validated
// version string. They are never mounted into a user's build: each project gets its own copy, made once, when
// its cache doesn't exist yet.

const seedMarker = ".greyship-seed"

type seeds struct {
	mu    sync.Mutex
	ready map[string]bool
	queue chan seedJob
}

type seedJob struct{ goVersion, goarch string }

func seedVolume(goVersion, goarch string) string {
	return "gs-gocache-seed-" + goVersion + "-" + goarch
}

func (s *Sandbox) seedState() *seeds {
	s.seedsOnce.Do(func() { s.seeds = &seeds{ready: map[string]bool{}, queue: make(chan seedJob, 16)} })
	return s.seeds
}

// SeedLater asks for a seed to be made by RunSeeder; it never blocks a build.
func (s *Sandbox) SeedLater(goVersion, goarch string) {
	if !validGoVersion(goVersion) || (goarch != "amd64" && goarch != "arm64") {
		return
	}
	st := s.seedState()
	st.mu.Lock()
	ready := st.ready[goVersion+"/"+goarch]
	st.mu.Unlock()
	if ready {
		return
	}
	select {
	case st.queue <- seedJob{goVersion, goarch}:
	default:
	}
}

// RunSeeder makes requested seeds one at a time until ctx ends. The builder runs it next to its worker.
func (s *Sandbox) RunSeeder(ctx context.Context, logf func(msg string, args ...any)) {
	st := s.seedState()
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-st.queue:
			if s.seedReady(ctx, job.goVersion, job.goarch) {
				continue
			}
			// Only between builds: a seed's memory plus the builds' would eat into the apps' share.
			for s.active.Load() > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
			}
			logf("preparing build cache", "go", job.goVersion, "arch", job.goarch)
			if err := s.makeSeed(ctx, job.goVersion, job.goarch); err != nil && ctx.Err() == nil {
				logf("preparing build cache failed", "go", job.goVersion, "err", err.Error())
			}
		}
	}
}

func validGoVersion(v string) bool { return goVersion.MatchString(v) }

func (s *Sandbox) seedReady(ctx context.Context, ver, arch string) bool {
	st := s.seedState()
	key := ver + "/" + arch
	st.mu.Lock()
	ready := st.ready[key]
	st.mu.Unlock()
	if ready {
		return true
	}
	if _, err := s.docker(ctx, "volume", "inspect", seedVolume(ver, arch)); err != nil {
		return false
	}
	_, err := s.docker(ctx, "run", "--rm", "--runtime=runsc", "--network=none", "--read-only",
		"-v", seedVolume(ver, arch)+":/seed:ro", "golang:"+ver+"-alpine", "test", "-f", "/seed/"+seedMarker)
	if err != nil {
		return false
	}
	st.mu.Lock()
	st.ready[key] = true
	st.mu.Unlock()
	return true
}

// makeSeed compiles the standard library exactly as user builds do (same GOOS, GOARCH, CGO and -trimpath),
// so the cache entries match, then marks the volume complete.
func (s *Sandbox) makeSeed(ctx context.Context, ver, arch string) error {
	vol := seedVolume(ver, arch)
	if _, err := s.docker(ctx, "volume", "create", "--label", "greyship=seed", vol); err != nil {
		return err
	}
	_, err := s.docker(ctx, "run", "--rm", "--runtime=runsc", "--network=none",
		// A small share: this runs beside builds and apps.
		"--memory=1024m", "--memory-swap=1024m", "--cpus=1", "--pids-limit=512",
		"-v", vol+":/root/.cache/go-build",
		"-e", "CGO_ENABLED=0", "-e", "GOOS=linux", "-e", "GOARCH="+arch, "-e", "GOFLAGS=-trimpath", "-e", "GOTOOLCHAIN=local",
		"golang:"+ver+"-alpine", "sh", "-c", "go build std && touch /root/.cache/go-build/"+seedMarker)
	if err != nil {
		return err
	}
	st := s.seedState()
	st.mu.Lock()
	st.ready[ver+"/"+arch] = true
	st.mu.Unlock()
	return nil
}

// seedProject gives a project whose build cache doesn't exist yet a copy of the matching seed. Projects that
// already have a cache keep it; without a ready seed nothing happens and the build compiles as before.
func (s *Sandbox) seedProject(ctx context.Context, spec Spec) bool {
	project := "gs-gocache-" + spec.ProjectID
	if _, err := s.docker(ctx, "volume", "inspect", project); err == nil {
		return false
	}
	if !s.seedReady(ctx, spec.GoVersion, spec.GOARCH) {
		return false
	}
	if _, err := s.docker(ctx, "volume", "create", project); err != nil {
		return false
	}
	_, err := s.docker(ctx, "run", "--rm", "--runtime=runsc", "--network=none",
		"-v", seedVolume(spec.GoVersion, spec.GOARCH)+":/from:ro", "-v", project+":/to",
		"golang:"+spec.GoVersion+"-alpine", "cp", "-a", "/from/.", "/to/")
	return err == nil
}
