package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/build"
	"greyship.tech/internal/build/fly"
	"greyship.tech/internal/bus"
	"greyship.tech/internal/config"
	"greyship.tech/internal/deploy"
	"greyship.tech/internal/github"
	"greyship.tech/internal/objstore"
	"greyship.tech/internal/projdel"
	"greyship.tech/internal/project"
	"greyship.tech/internal/secrets"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("greyship-builder stopped", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.LoadBuilder(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.Runner == "gvisor" {
		if err := requireRunsc(ctx, cfg.Docker, cfg.DockerHost); err != nil {
			return err
		}
	}
	workerCount := cfg.Concurrency
	if cfg.Runner == "fly" {
		workerCount = int(cfg.Fly.MaxMachines)
	}
	pgcfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	// Every worker (the builds, the verifier and the GitHub fetcher) holds one connection for LISTEN and uses another while it
	// works; a smaller pool leaves claims waiting forever for a connection.
	pgcfg.MaxConns = int32(2*(workerCount+2) + 4) //nolint:gosec // small, validated counts
	pool, err := pgxpool.NewWithConfig(ctx, pgcfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}

	storage := &objstore.Store{
		Endpoint: cfg.Storage.Endpoint, Region: cfg.Storage.Region, Bucket: cfg.Storage.Bucket,
		AccessKey: cfg.Storage.AccessKey, SecretKey: cfg.Storage.SecretKey,
		HTTP: &http.Client{Timeout: 2 * time.Minute}, Now: time.Now,
	}
	sandbox := &build.Sandbox{
		Docker: cfg.Docker, Host: cfg.DockerHost, Network: cfg.Network, Proxy: cfg.ProxyURL, ProxyHost: cfg.ProxyHost, Timeout: cfg.Timeout,
		CPUs: cfg.CPUs, MemoryMB: cfg.MemoryMB,
	}
	var runner build.Runner = &build.LocalRunner{Compiler: sandbox, Storage: storage, Now: time.Now}
	concurrency := workerCount
	var flyRunner *fly.Runner
	if cfg.Runner == "fly" {
		flyRunner = &fly.Runner{
			Fly:  &fly.Client{Base: "https://api.machines.dev/v1", App: cfg.Fly.App, Token: cfg.Fly.Token, Region: cfg.Fly.Region, HTTP: &http.Client{Timeout: time.Minute}},
			Pool: pool, Storage: storage, Signer: auth.Signer{Key: cfg.TokenSecret, Now: time.Now},
			APIURL: cfg.Fly.APIURL, ImagePrefix: cfg.Fly.ImagePrefix, MaxMachines: cfg.Fly.MaxMachines, VolumeGB: cfg.Fly.VolumeGB,
			CPUs: cfg.Fly.CPUs, MemoryMB: cfg.Fly.MemoryMB, Timeout: cfg.Timeout,
			Poll: 500 * time.Millisecond, Logger: logger, Now: time.Now,
		}
		// Builds run on Fly, so this machine only waits for them: one worker per possible build machine.
		runner = flyRunner
	}
	builder := &build.Builder{
		Pool:      pool,
		Storage:   storage,
		Runner:    runner,
		DefaultGo: cfg.DefaultGo,
		GOARCH:    cfg.GOARCH,
		Logger:    logger,
		Now:       time.Now,
	}
	// cfg.Concurrency builds at a time, each within its CPU and memory caps (architecture §7). The queue lets
	// each team have one build running, so a burst from one team doesn't hold everyone else up.
	workers := make([]*bus.Worker, concurrency)
	for i := range workers {
		workers[i] = &bus.Worker{
			Queue: bus.NewQueue(pool), Pool: pool, ID: bus.WorkerID(), Kind: bus.BuildRequested,
			Lease: 2 * time.Minute, Poll: 5 * time.Second, Logger: logger,
		}
	}
	// Prepared build caches: the configured Go versions now (which also downloads their images), and any other
	// version after its first build. One at a time, beside builds, with a small CPU and memory share.
	if cfg.Runner == "gvisor" {
		for _, v := range append([]string{cfg.DefaultGo}, cfg.Seed...) {
			sandbox.SeedLater(v, cfg.GOARCH)
		}
	}
	seedCtx, stopSeeder := context.WithCancel(ctx)
	seeder := make(chan struct{})
	go func() {
		defer close(seeder)
		sandbox.RunSeeder(seedCtx, logger.Info)
	}()
	defer func() {
		stopSeeder()
		<-seeder
	}()

	logger.Info("greyship-builder ready", "runner", cfg.Runner, "builds_at_once", concurrency)
	g, gctx := errgroup.WithContext(ctx)
	if flyRunner != nil {
		g.Go(func() error { flyRunner.SweepVolumes(gctx, time.Hour, 14); return nil })
	}
	for _, w := range workers {
		g.Go(func() error { return w.Run(gctx, builder.Handle) })
	}
	keys, err := secrets.NewKeyring(cfg.MasterKey)
	if err != nil {
		return err
	}
	runGitHub(gctx, g, pool, storage, keys, logger)
	deleter := &projdel.Deleter{Pool: pool, Objects: storage, Logger: logger, Wait: time.Minute, Poll: time.Second}
	deletes := &bus.Worker{Queue: bus.NewQueue(pool), Pool: pool, ID: bus.WorkerID(), Kind: bus.ProjectDelete, Lease: 3 * time.Minute, Poll: 5 * time.Second, Logger: logger}
	g.Go(func() error { return deletes.Run(gctx, deleter.Handle) })
	// Checking binaries built on developers' machines is quick and never waits behind a compile.
	verifier := &bus.Worker{
		Queue: bus.NewQueue(pool), Pool: pool, ID: bus.WorkerID(), Kind: bus.BuildVerify,
		Lease: 2 * time.Minute, Poll: 5 * time.Second, Logger: logger,
	}
	g.Go(func() error { return verifier.Run(gctx, builder.Verify) })
	return g.Wait()
}

// requireRunsc refuses to start without gVisor: builds never run in a plain container.
func requireRunsc(ctx context.Context, docker, host string) error {
	cmd := exec.CommandContext(ctx, docker, "info", "--format", "{{json .Runtimes}}") //nolint:gosec // fixed arguments
	cmd.Env = []string{"DOCKER_HOST=" + host, "HOME=/tmp"}
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("docker at %s isn't reachable (run make sandbox): %w", host, err)
	}
	if !strings.Contains(string(out), `"runsc"`) {
		return errors.New("the docker at " + host + " has no runsc runtime; builds need gVisor (run make sandbox)")
	}
	return nil
}

// runGitHub starts the GitHub side of deploys: fetching pushed commits and keeping their commit statuses up to
// date (decisions.md 042). The app is read from the database on each use, so it works as soon as the admin creates it.
func runGitHub(ctx context.Context, g *errgroup.Group, pool *pgxpool.Pool, storage *objstore.Store, keys *secrets.Keyring, logger *slog.Logger) {
	apps := github.Dynamic{Apps: &github.Apps{Pool: pool, Keys: keys, Now: time.Now, Logger: logger}}
	deploys := deploy.NewService(pool, project.NewService(pool), storage, logger, time.Now)
	fetcher := &github.Fetcher{Pool: pool, App: apps, Storage: storage, Deploy: deploys, Logger: logger}
	worker := &bus.Worker{Queue: bus.NewQueue(pool), Pool: pool, ID: bus.WorkerID(), Kind: github.FetchJob, Lease: 2 * time.Minute, Poll: 5 * time.Second, Logger: logger}
	statuses := &github.Statuses{Pool: pool, App: apps, Dashboard: getenvOr("GREYSHIP_DASHBOARD_URL", "https://greyship.tech"), Logger: logger}
	g.Go(func() error { return worker.Run(ctx, fetcher.Handle) })
	g.Go(func() error { statuses.Run(ctx, 5*time.Second); return nil })
}

func getenvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
