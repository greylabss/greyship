// Package buildd is greyship-buildd, the program a Fly build machine runs (decisions.md 039). It fetches one
// deployment's source, builds every target, uploads the binaries and reports to the API, then exits.
package buildd

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"greyship.tech/internal/build/fly"
)

const (
	maxSource  = 100 << 20 // compressed, as uploaded
	maxFiles   = 50_000
	maxUnpack  = 512 << 20
	flushEvery = 500 * time.Millisecond
)

// Builder holds where the build happens. Cache is the mounted volume, or "" for a machine without one.
type Builder struct {
	Work  string // emptied before and after every build
	Cache string
	Go    string // the go command
	HTTP  *http.Client
	Now   func() time.Time
}

// Run builds job and always tries to report an outcome, so the builder learns what happened.
func (b *Builder) Run(ctx context.Context, job fly.MachineJob) error {
	rep := newReporter(ctx, b.HTTP, job)
	defer rep.close()
	start := b.Now()
	outcome := b.build(ctx, job, rep)
	outcome.Seconds = b.Now().Sub(start).Seconds()
	if err := os.RemoveAll(b.Work); err != nil {
		rep.system("couldn't clear the workspace: " + err.Error())
	}
	return rep.finish(outcome)
}

func (b *Builder) build(ctx context.Context, job fly.MachineJob, rep *reporter) fly.Outcome {
	src := filepath.Join(b.Work, "src")
	out := filepath.Join(b.Work, "out")
	_ = os.RemoveAll(b.Work)
	for _, d := range []string{src, out} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return fly.Outcome{Error: "prepare workspace: " + err.Error()}
		}
	}
	rep.system("downloading the source onto the build machine")
	tarball, err := b.fetch(ctx, job.SourceURL)
	if err != nil {
		return fly.Outcome{Error: err.Error()}
	}
	if err := unpack(tarball, src); err != nil {
		return fly.Outcome{ExitCode: 1, Error: err.Error()}
	}

	env := b.env(job.GOARCH)
	rep.system("downloading modules")
	if code, err := b.goCmd(ctx, src, env, rep, "mod", "download"); err != nil {
		return fly.Outcome{ExitCode: code, Error: "go mod download failed"}
	}
	bins := map[string]fly.Binary{}
	for _, t := range job.Targets {
		rep.system(fmt.Sprintf("building %s (%s)", t.Name, t.Path))
		dst := filepath.Join(out, t.Name)
		if code, err := b.goCmd(ctx, src, env, rep, "build", "-ldflags=-s -w", "-o", dst, t.Path); err != nil {
			return fly.Outcome{ExitCode: code, Error: fmt.Sprintf("go build %s failed", t.Path)}
		}
		bin, err := b.upload(ctx, job, t.Name, dst)
		if err != nil {
			return fly.Outcome{Error: err.Error()}
		}
		bins[t.Name] = bin
	}
	rep.system("go vet (warnings only)")
	if _, err := b.goCmd(ctx, src, env, rep, "vet", "./..."); err != nil {
		rep.system("go vet reported problems; the build continues")
	}
	return fly.Outcome{OK: true, Binaries: bins}
}

// env is the build's whole environment: no variables from the machine leak in (decisions.md 041).
func (b *Builder) env(goarch string) []string {
	cache := b.Cache
	if cache == "" {
		cache = filepath.Join(b.Work, "cache")
	}
	return []string{
		"HOME=" + b.Work, "PATH=" + os.Getenv("PATH"),
		"GOMODCACHE=" + filepath.Join(cache, "mod"), "GOCACHE=" + filepath.Join(cache, "build"),
		"GOPROXY=https://proxy.golang.org", "GOSUMDB=sum.golang.org", "GOTOOLCHAIN=local",
		"GOFLAGS=-mod=readonly -trimpath", "CGO_ENABLED=0", "GOOS=linux", "GOARCH=" + goarch,
	}
}

// goCmd runs the go command in dir and streams its output to the log. It returns the exit code on failure.
func (b *Builder) goCmd(ctx context.Context, dir string, env []string, rep *reporter, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, b.Go, args...) //nolint:gosec // the image's go command; arguments are separate, targets validated by the builder
	cmd.Dir, cmd.Env = dir, env
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			rep.build(sc.Text())
		}
		_, _ = io.Copy(io.Discard, pr)
	}()
	err := cmd.Run()
	_ = pw.Close()
	<-done
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), err
	}
	return 0, err
}

func (b *Builder) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build source request: %w", err)
	}
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download source: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download source: storage answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSource+1))
	if err != nil {
		return nil, fmt.Errorf("download source: %w", err)
	}
	if len(body) > maxSource {
		return nil, errors.New("the source is larger than 100 MB")
	}
	return body, nil
}

// upload asks the API for a URL signed for this binary's size, then PUTs it there.
func (b *Builder) upload(ctx context.Context, job fly.MachineJob, name, path string) (fly.Binary, error) {
	bin, err := os.ReadFile(path) //nolint:gosec // our own output folder
	if err != nil {
		return fly.Binary{}, fmt.Errorf("read %s binary: %w", name, err)
	}
	if len(bin) > fly.MaxBinary {
		return fly.Binary{}, fmt.Errorf("%s is larger than %d MB", name, fly.MaxBinary>>20)
	}
	var u struct {
		URL string `json:"url"`
	}
	if err := post(ctx, b.HTTP, job, "upload", fly.UploadRequest{Target: name, Size: int64(len(bin))}, &u); err != nil {
		return fly.Binary{}, fmt.Errorf("get upload URL for %s: %w", name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.URL, bytes.NewReader(bin))
	if err != nil {
		return fly.Binary{}, fmt.Errorf("build upload request: %w", err)
	}
	req.ContentLength = int64(len(bin))
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return fly.Binary{}, fmt.Errorf("upload %s: %w", name, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fly.Binary{}, fmt.Errorf("upload %s: storage answered %d", name, resp.StatusCode)
	}
	sum := sha256.Sum256(bin)
	return fly.Binary{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(bin))}, nil
}

// unpack writes the tarball under dir. Only regular files and folders are written, and nothing may land
// outside dir: the source is the user's, so every name is checked.
func unpack(tarball []byte, dir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return errors.New("the source isn't a .tar.gz")
	}
	tr := tar.NewReader(gz)
	var files int
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errors.New("the source tarball is damaged")
		}
		name := filepath.Clean(filepath.FromSlash(h.Name))
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("the source has a file outside its folder: %s", h.Name)
		}
		target := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
			continue
		case tar.TypeReg:
		default:
			continue
		}
		if files++; files > maxFiles {
			return errors.New("the source has too many files")
		}
		if total += h.Size; total > maxUnpack {
			return errors.New("the source unpacks to more than 512 MB")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640) //nolint:gosec // checked to be inside dir
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(tr, h.Size))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
}

// reporter batches log lines to the API, so a chatty build costs a few requests per second.
type reporter struct {
	ctx  context.Context
	http *http.Client
	job  fly.MachineJob

	mu      sync.Mutex
	pending []fly.Line
	stop    chan struct{}
	done    chan struct{}
}

func newReporter(ctx context.Context, client *http.Client, job fly.MachineJob) *reporter {
	r := &reporter{ctx: ctx, http: client, job: job, stop: make(chan struct{}), done: make(chan struct{})}
	go r.run()
	return r
}

func (r *reporter) system(text string) { r.add("system", text) }
func (r *reporter) build(text string)  { r.add("build", text) }

func (r *reporter) add(stream, text string) {
	r.mu.Lock()
	r.pending = append(r.pending, fly.Line{Stream: stream, Text: text})
	r.mu.Unlock()
}

func (r *reporter) run() {
	defer close(r.done)
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
			_ = r.flush(nil) // a failed send keeps its lines for the next one
		}
	}
}

// flush sends what's pending; a failed send keeps the lines for the next try.
func (r *reporter) flush(outcome *fly.Outcome) error {
	r.mu.Lock()
	lines := r.pending
	r.pending = nil
	r.mu.Unlock()
	if len(lines) == 0 && outcome == nil {
		return nil
	}
	err := post(r.ctx, r.http, r.job, "report", fly.Report{Lines: lines, Outcome: outcome}, nil)
	if err != nil {
		r.mu.Lock()
		r.pending = append(lines, r.pending...)
		r.mu.Unlock()
	}
	return err
}

func (r *reporter) finish(outcome fly.Outcome) error {
	r.close()
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = r.flush(&outcome); err == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
	return fmt.Errorf("report outcome: %w", err)
}

func (r *reporter) close() {
	select {
	case <-r.stop:
	default:
		close(r.stop)
		<-r.done
	}
}

func post(ctx context.Context, client *http.Client, job fly.MachineJob, what string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := strings.TrimRight(job.APIURL, "/") + "/v1/internal/builds/" + job.Deployment + "/" + what
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Build "+job.Token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("API answered %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
