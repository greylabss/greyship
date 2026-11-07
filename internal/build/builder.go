// Package build turns a deployment's uploaded source into one binary per service (spec F-23, F-24,
// architecture §2.2). It handles build.requested jobs from the bus.
package build

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"os/exec"
	"path"
	"strings"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/deploy"
	"greyship.tech/internal/detect"
	"greyship.tech/internal/pack"
	"greyship.tech/internal/store"
)

type Storage interface {
	Get(ctx context.Context, key string, limit int64) ([]byte, error)
	Put(ctx context.Context, key string, body io.Reader, size int64) error
}

type Builder struct {
	Pool      *pgxpool.Pool
	Storage   Storage
	Runner    Runner
	DefaultGo string // used when go.mod names no version, e.g. "1.27"
	GOARCH    string // CPU of the runtime nodes
	Logger    *slog.Logger
	Now       func() time.Time
}

// userError is a build that failed because of the source (it doesn't compile, bad greyship.toml, too slow).
// The deployment fails and the job is done. Other errors are ours and the job is retried.
type userError struct{ msg string }

func (e *userError) Error() string { return e.msg }

func failf(format string, args ...any) error { return &userError{msg: fmt.Sprintf(format, args...)} }

// Handle builds one deployment. Running it twice is safe: a finished deployment is left alone and a
// half-done attempt is cleared and started again.
func (b *Builder) Handle(ctx context.Context, job bus.Job) error {
	var payload struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("decode job %d: %w", job.ID, err)
	}
	q := store.New(b.Pool)
	d, err := q.StartBuild(ctx, payload.DeploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("start build %s: %w", payload.DeploymentID, err)
	}
	if err := q.DeleteArtifacts(ctx, d.ID); err != nil {
		return fmt.Errorf("clear artifacts of %s: %w", d.ID, err)
	}
	if err := q.DeleteBuildLogs(ctx, d.ID); err != nil {
		return fmt.Errorf("clear build log of %s: %w", d.ID, err)
	}

	log := NewLog(q, d.ID, 0, b.Now)
	built, err := b.build(ctx, q, d, log)
	var userErr *userError
	switch {
	case err == nil:
	case errors.As(err, &userErr):
		log.System("build failed: " + userErr.msg)
		log.System(KeepsServing(ctx, q, d.ProjectID))
	case job.Attempts >= job.MaxAttempts:
		log.System("build failed: an internal error stopped it; deploy again")
		log.System(KeepsServing(ctx, q, d.ProjectID))
	default:
		log.System("the build was interrupted by an internal error; retrying")
	}
	// The log is complete before the status changes, so a follower that sees the end has every line.
	if lerr := log.Close(); lerr != nil {
		b.Logger.Error("write build log", "deployment", d.ID, "err", lerr)
	}

	switch {
	case err == nil:
		if ferr := b.finish(ctx, d.ID); ferr != nil {
			return ferr
		}
		b.Logger.Info("build succeeded", "deployment", d.ID, "services", len(built))
		return nil
	case errors.As(err, &userErr):
		return b.fail(ctx, q, d.ID, userErr.msg)
	case job.Attempts >= job.MaxAttempts:
		if ferr := b.fail(ctx, q, d.ID, "internal error during the build"); ferr != nil {
			return ferr
		}
		return err
	default:
		return err
	}
}

// finish marks the deployment built and asks the scheduler to release it, in one transaction.
func (b *Builder) finish(ctx context.Context, id string) error {
	tx, err := b.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin finish build %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	if _, err := q.FinishBuild(ctx, id); err != nil {
		return fmt.Errorf("finish build %s: %w", id, err)
	}
	if _, err := bus.Enqueue(ctx, q, bus.DeploymentRelease, map[string]string{"deployment_id": id}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit finish build %s: %w", id, err)
	}
	return nil
}

func (b *Builder) fail(ctx context.Context, _ *store.Queries, id, msg string) error {
	tx, err := b.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	_, err = q.FailBuild(ctx, store.FailBuildParams{ID: id, Error: &msg})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil // already failed or finished: nothing new to tell anyone
	case err != nil:
		return fmt.Errorf("fail build %s: %w", id, err)
	}
	if err := bus.EnqueueNotify(ctx, q, bus.DeployFailed, id, ""); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit failed build %s: %w", id, err)
	}
	b.Logger.Info("build failed", "deployment", id, "reason", msg)
	return nil
}

func (b *Builder) build(ctx context.Context, q *store.Queries, d store.Deployment, log *Log) ([]store.Artifact, error) {
	p, err := q.GetProject(ctx, d.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("load project %s: %w", d.ProjectID, err)
	}
	log.System("fetching source")
	if d.SourceKey == nil || d.SourceSha256 == nil {
		return nil, failf("this deployment has no source to build")
	}
	src, err := b.Storage.Get(ctx, *d.SourceKey, deploy.MaxSourceSize)
	if err != nil {
		return nil, fmt.Errorf("download source of %s: %w", d.ID, err)
	}
	if sum := sha256.Sum256(src); hex.EncodeToString(sum[:]) != *d.SourceSha256 {
		return nil, failf("the uploaded source doesn't match its checksum; deploy again")
	}
	fsys, err := SourceFS(src)
	if err != nil {
		return nil, failf("%s", err)
	}
	found, err := detect.Detect(fsys, p.Slug)
	if err != nil {
		return nil, failf("%s", err)
	}
	if len(found.Services) == 0 {
		return nil, failf("no services found: add a package main, for example cmd/api/main.go")
	}
	if err := b.bundleFiles(ctx, q, d, fsys, found.Files, log); err != nil {
		return nil, err
	}

	kinds := map[string]detect.Service{}
	var targets []Target
	for _, s := range found.Services {
		targets = append(targets, Target{Name: s.Name, Path: s.Path})
		kinds[s.Name] = s
	}
	if found.Release != nil {
		targets = append(targets, Target{Name: "release", Path: found.Release.Path})
		kinds["release"] = detect.Service{Name: "release", Path: found.Release.Path, Type: "release", Reason: found.Release.Reason}
	}
	goVersion := strings.TrimPrefix(found.GoVersion, "go")
	if goVersion == "" {
		goVersion = b.DefaultGo
	}
	for _, t := range targets {
		log.System(fmt.Sprintf("service %s (%s, %s): %s", t.Name, kinds[t.Name].Type, t.Path, kinds[t.Name].Reason))
	}
	for _, w := range found.Warnings {
		log.System("warning: " + w)
	}
	log.System("building with Go " + goVersion)

	spec := Spec{ID: d.ID, GoVersion: goVersion, GOARCH: b.GOARCH, TeamID: p.TeamID, ProjectID: p.ID, Targets: targets}
	keys := map[string]string{}
	for _, t := range targets {
		keys[t.Name] = fmt.Sprintf("artifacts/%s/%s", d.ID, t.Name)
	}
	res, err := b.Runner.Run(ctx, Job{Spec: spec, Source: src, SourceKey: *d.SourceKey, OutputKeys: keys}, func(stream, line string) {
		if stream == "system" {
			log.System(line)
			return
		}
		log.Build(line)
	})
	var exitErr *exec.ExitError
	var failed *Failed
	switch {
	case errors.As(err, &failed):
		return nil, failf("%s", failed)
	case errors.Is(err, ErrTimeout):
		return nil, failf("the build took longer than the limit")
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 125:
		// docker itself failed (daemon, image pull), not the build: ours to retry.
		return nil, fmt.Errorf("sandbox: %w", err)
	case errors.As(err, &exitErr):
		return nil, failf("go build failed (exit status %d)", exitErr.ExitCode())
	case err != nil:
		return nil, failf("%s", err)
	}

	if err := q.RecordBuildRun(ctx, store.RecordBuildRunParams{ID: d.ID, Runner: &res.Runner, MachineSize: nonEmpty(res.MachineSize), BuildSeconds: ptrInt32(int32(math.Ceil(res.Seconds)))}); err != nil {
		return nil, fmt.Errorf("record build time of %s: %w", d.ID, err)
	}
	var out []store.Artifact
	for _, t := range targets {
		bin, ok := res.Binaries[t.Name]
		if !ok {
			return nil, fmt.Errorf("runner returned no %s binary", t.Name)
		}
		typ := kinds[t.Name].Type
		a, err := q.CreateArtifact(ctx, store.CreateArtifactParams{
			DeploymentID: d.ID, ServiceName: t.Name, ServicePath: t.Path, ServiceType: typ,
			ObjectKey: keys[t.Name], Sha256: bin.SHA256, SizeBytes: bin.Size, GoVersion: goVersion, Goarch: b.GOARCH,
		})
		if err != nil {
			return nil, fmt.Errorf("record %s artifact: %w", t.Name, err)
		}
		log.System(fmt.Sprintf("built %s (%.1f MB)", t.Name, float64(bin.Size)/(1<<20)))
		out = append(out, a)
	}
	return out, nil
}

// bundleFiles packs greyship.toml's files from the source and stores them, keyed by content within the project
// like binaries, so an unchanged set is stored once.
func (b *Builder) bundleFiles(ctx context.Context, q *store.Queries, d store.Deployment, fsys fs.FS, entries []string, log *Log) error {
	if len(entries) == 0 {
		return nil
	}
	var buf bytes.Buffer
	res, err := pack.BundleFS(fsys, entries, &buf)
	if err != nil {
		return failf("%s", err)
	}
	sum := sha256.Sum256(buf.Bytes())
	sha := hex.EncodeToString(sum[:])
	key := deploy.FilesKey(d.ProjectID, sha)
	size := int64(buf.Len())
	if err := b.Storage.Put(ctx, key, bytes.NewReader(buf.Bytes()), size); err != nil {
		return fmt.Errorf("store files of %s: %w", d.ID, err)
	}
	if err := q.SetDeploymentFiles(ctx, store.SetDeploymentFilesParams{ID: d.ID, FilesKey: &key, FilesSha256: &sha, FilesSize: &size}); err != nil {
		return fmt.Errorf("record files of %s: %w", d.ID, err)
	}
	log.System("files: " + res.Summary())
	return nil
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ptrInt32(v int32) *int32 { return &v }

const (
	maxSourceFiles = 50_000
	maxSourceBytes = 512 << 20
)

// SourceFS reads a source tarball into memory for detection. Detection only parses Go files; nothing runs.
func SourceFS(src []byte) (fstest.MapFS, error) {
	gz, err := gzip.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, errors.New("the source isn't a .tar.gz")
	}
	fsys := fstest.MapFS{}
	var total int64
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fsys, nil
		}
		if err != nil {
			return nil, errors.New("the source tarball is damaged")
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Clean(h.Name)
		if path.IsAbs(name) || name == "." || strings.HasPrefix(name, "../") || name == ".." {
			return nil, fmt.Errorf("the source contains an unsafe path %q", h.Name)
		}
		total += h.Size
		if len(fsys) >= maxSourceFiles || total > maxSourceBytes {
			return nil, errors.New("the source is too big once unpacked")
		}
		b, err := io.ReadAll(io.LimitReader(tr, h.Size))
		if err != nil {
			return nil, errors.New("the source tarball is damaged")
		}
		fsys[name] = &fstest.MapFile{Data: b, Mode: 0o644}
	}
}
