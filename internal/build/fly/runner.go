package fly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/build"
	"greyship.tech/internal/store"
)

type Storage interface {
	PresignGet(key string, ttl time.Duration) (string, error)
	Stat(ctx context.Context, key string) (int64, error)
}

// Runner builds on Fly Machines: one machine per build, reused only by the same team when it carries that
// team's cache volume (decisions.md 039).
type Runner struct {
	Fly         *Client
	Pool        *pgxpool.Pool
	Storage     Storage
	Signer      auth.Signer
	APIURL      string // public API address build machines report to
	ImagePrefix string // e.g. registry.fly.io/greyship-build; the image is <prefix>:go<minor>
	MaxMachines int64
	VolumeGB    int
	CPUs        int           // CPUs of a build machine
	MemoryMB    int           // memory of a build machine
	Timeout     time.Duration // a build running longer is stopped
	Poll        time.Duration
	Logger      *slog.Logger
	Now         func() time.Time
}

// ErrNoMachine means every build machine is busy; the job is retried later.
var ErrNoMachine = errors.New("no build machine free")

type leased struct {
	row store.BuildMachine
}

func (r *Runner) Run(ctx context.Context, job build.Job, logLine func(stream, line string)) (build.Result, error) {
	q := store.New(r.Pool)
	dep := job.Spec.ID
	ttl := r.Timeout + 10*time.Minute
	src, err := r.Storage.PresignGet(job.SourceKey, ttl)
	if err != nil {
		return build.Result{}, fmt.Errorf("presign source: %w", err)
	}
	token, err := SignToken(r.Signer, dep, ttl)
	if err != nil {
		return build.Result{}, err
	}
	targets := make([]Target, 0, len(job.Spec.Targets))
	for _, t := range job.Spec.Targets {
		targets = append(targets, Target{Name: t.Name, Path: t.Path})
	}
	payload, err := json.Marshal(MachineJob{Deployment: dep, SourceURL: src, APIURL: r.APIURL, Token: token, GOARCH: job.Spec.GOARCH, Targets: targets})
	if err != nil {
		return build.Result{}, err
	}
	if err := q.DeleteBuildReports(ctx, dep); err != nil {
		return build.Result{}, fmt.Errorf("clear old reports: %w", err)
	}

	cfg := MachineConfig{
		Image:   r.image(job.Spec.GoVersion),
		Env:     map[string]string{"GREYSHIP_BUILD_JOB": string(payload)},
		Guest:   Guest{CPUKind: "performance", CPUs: r.CPUs, MemoryMB: r.MemoryMB},
		Restart: map[string]string{"policy": "no"},
	}
	m, err := r.lease(ctx, job.Spec.TeamID, cfg)
	if err != nil {
		return build.Result{}, err
	}
	defer r.release(m)
	logLine("system", fmt.Sprintf("building on a %s machine", r.size()))

	outcome, err := r.follow(ctx, m.row.FlyMachineID, dep, r.Timeout, logLine)
	if err != nil {
		return build.Result{}, err
	}
	if !outcome.OK {
		if outcome.ExitCode > 0 {
			logLine("system", outcome.Error)
			return build.Result{}, &build.Failed{Code: outcome.ExitCode}
		}
		return build.Result{}, fmt.Errorf("build machine: %s", outcome.Error)
	}
	res := build.Result{Binaries: map[string]build.Built{}, Seconds: outcome.Seconds, Runner: "fly", MachineSize: r.size()}
	for _, t := range job.Spec.Targets {
		bin, ok := outcome.Binaries[t.Name]
		if !ok {
			return build.Result{}, fmt.Errorf("build machine reported no %s binary", t.Name)
		}
		size, err := r.Storage.Stat(ctx, job.OutputKeys[t.Name])
		if err != nil {
			return build.Result{}, fmt.Errorf("check stored %s binary: %w", t.Name, err)
		}
		if size != bin.Size {
			return build.Result{}, fmt.Errorf("%s binary is stored with %d bytes, reported %d", t.Name, size, bin.Size)
		}
		res.Binaries[t.Name] = build.Built{SHA256: bin.SHA256, Size: bin.Size}
	}
	return res, nil
}

// size is the Fly machine size name for the configured CPUs, e.g. "performance-4x".
func (r *Runner) size() string {
	return fmt.Sprintf("performance-%dx", r.CPUs)
}

// lease gets a machine for this build and starts it. A team reuses its own stopped machine and its cache volume;
// otherwise a new machine is made with a volume.
func (r *Runner) lease(ctx context.Context, team string, cfg MachineConfig) (leased, error) {
	q := store.New(r.Pool)
	row, err := q.PickBuildMachine(ctx, store.PickBuildMachineParams{TeamID: team, Size: r.size()})
	if err == nil {
		cfg.Mounts = []Mount{{Volume: *row.VolumeID, Path: "/cache"}}
		if err := r.restart(ctx, row.FlyMachineID, cfg); err != nil {
			_ = q.ReleaseBuildMachine(ctx, row.ID)
			return leased{}, err
		}
		return leased{row: row}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return leased{}, fmt.Errorf("pick build machine: %w", err)
	}
	n, err := q.CountBuildMachines(ctx)
	if err != nil {
		return leased{}, err
	}
	if n >= r.MaxMachines {
		return leased{}, ErrNoMachine
	}
	volume, err := r.Fly.CreateVolume(ctx, "gs_cache_"+strings.ReplaceAll(team, "-", "")[:12], r.VolumeGB)
	if err != nil {
		return leased{}, err
	}
	cfg.Mounts = []Mount{{Volume: volume, Path: "/cache"}}
	m, err := r.Fly.CreateMachine(ctx, cfg)
	if err != nil {
		_ = r.Fly.DeleteVolume(context.WithoutCancel(ctx), volume)
		return leased{}, err
	}
	created, err := q.AddBuildMachine(ctx, store.AddBuildMachineParams{FlyMachineID: m.ID, VolumeID: &volume, Size: r.size(), TeamID: &team})
	if err != nil {
		return leased{}, fmt.Errorf("record build machine %s: %w", m.ID, err)
	}
	return leased{row: created}, nil
}

// restart gives a stopped machine the new build's config and starts it.
func (r *Runner) restart(ctx context.Context, id string, cfg MachineConfig) error {
	m, err := r.Fly.UpdateMachine(ctx, id, cfg)
	if err != nil {
		return err
	}
	if m.State == "stopped" || m.State == "created" {
		return r.Fly.StartMachine(ctx, id)
	}
	return nil
}

// follow passes the machine's log lines on until it stops, and returns its outcome. A build past the plan's
// timeout is stopped.
func (r *Runner) follow(ctx context.Context, machine, dep string, timeout time.Duration, logLine func(stream, line string)) (Outcome, error) {
	q := store.New(r.Pool)
	deadline := r.Now().Add(timeout)
	var after int64
	var outcome *Outcome
	drain := func() error {
		for {
			rows, err := q.BuildReportsAfter(ctx, store.BuildReportsAfterParams{DeploymentID: dep, ID: after})
			if err != nil {
				return err
			}
			for _, row := range rows {
				after = row.ID
				if row.Result != nil {
					var o Outcome
					if err := json.Unmarshal(row.Result, &o); err == nil {
						outcome = &o
					}
					continue
				}
				logLine(row.Stream, row.Line)
			}
			if len(rows) < 500 {
				return nil
			}
		}
	}
	t := time.NewTicker(r.Poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = r.Fly.StopMachine(context.WithoutCancel(ctx), machine)
			return Outcome{}, ctx.Err()
		case <-t.C:
		}
		if err := drain(); err != nil {
			return Outcome{}, fmt.Errorf("read build reports: %w", err)
		}
		if outcome != nil {
			return *outcome, nil
		}
		if r.Now().After(deadline) {
			_ = r.Fly.StopMachine(ctx, machine)
			return Outcome{}, build.ErrTimeout
		}
		m, err := r.Fly.GetMachine(ctx, machine)
		if err != nil {
			continue // a hiccup talking to Fly; the deadline still holds
		}
		if m.State == "stopped" || m.State == "destroyed" || m.State == "failed" {
			if err := drain(); err == nil && outcome != nil {
				return *outcome, nil
			}
			return Outcome{}, fmt.Errorf("build machine %s stopped without reporting a result", machine)
		}
	}
}

// release returns a team's machine to the pool, or destroys a Free build's machine.
func (r *Runner) release(m leased) {
	ctx := context.Background()
	q := store.New(r.Pool)
	_ = r.Fly.StopMachine(ctx, m.row.FlyMachineID)
	if err := q.ReleaseBuildMachine(ctx, m.row.ID); err != nil {
		r.Logger.Warn("release build machine", "machine", m.row.FlyMachineID, "err", err)
	}
}

// image is the build image for a project's Go version: one per minor (go1.27), built by make build-images.
func (r *Runner) image(goVersion string) string {
	parts := strings.SplitN(goVersion, ".", 3)
	minor := goVersion
	if len(parts) >= 2 {
		minor = parts[0] + "." + parts[1]
	}
	return r.ImagePrefix + ":go" + minor
}

// SweepVolumes deletes team machines and their volumes after days without a build, every interval until ctx
// ends: a stopped machine costs nothing, but its volume does (decisions.md 039).
func (r *Runner) SweepVolumes(ctx context.Context, interval time.Duration, days int32) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		q := store.New(r.Pool)
		stale, err := q.StaleBuildMachines(ctx, days)
		if err != nil {
			r.Logger.Warn("list stale build machines", "err", err)
			continue
		}
		for _, m := range stale {
			if err := r.Fly.DestroyMachine(ctx, m.FlyMachineID); err != nil {
				r.Logger.Warn("destroy stale build machine", "machine", m.FlyMachineID, "err", err)
				continue
			}
			if m.VolumeID != nil {
				if err := r.Fly.DeleteVolume(ctx, *m.VolumeID); err != nil {
					r.Logger.Warn("delete stale volume", "volume", *m.VolumeID, "err", err)
				}
			}
			_ = q.RemoveBuildMachine(ctx, m.ID)
		}
	}
}
