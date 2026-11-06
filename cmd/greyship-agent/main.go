package main

import (
	"context"
	"crypto/rand"
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

	"greyship.tech/internal/agent"
	"greyship.tech/internal/bus"
	"greyship.tech/internal/config"
	"greyship.tech/internal/env"
	"greyship.tech/internal/objstore"
	"greyship.tech/internal/runtime/gvisor"
	"greyship.tech/internal/secrets"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("greyship-agent stopped", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	host, _ := os.Hostname()
	cfg, err := config.LoadAgent(os.Getenv, strings.TrimSuffix(host, ".local"))
	if err != nil {
		return err
	}
	init, err := os.ReadFile(cfg.InitBinary)
	if err != nil {
		return fmt.Errorf("read guest init (build it with make agent): %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := requireRunsc(ctx, cfg.Docker, cfg.DockerHost); err != nil {
		return err
	}
	pgcfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	// Each command worker holds one connection to listen and uses another while it works.
	pgcfg.MaxConns = 2*commandWorkers + 4
	pool, err := pgxpool.NewWithConfig(ctx, pgcfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	keyring, err := secrets.NewKeyring(cfg.MasterKey)
	if err != nil {
		return err
	}
	driver := &gvisor.Driver{Docker: cfg.Docker, Host: cfg.DockerHost, Shared: cfg.SharedDir}
	node := &agent.Node{
		Pool: pool, Leftovers: driver, Env: env.NewService(pool, keyring, nil), Name: cfg.NodeName, Region: cfg.Region, TotalMB: int32(cfg.TotalMB), Logger: logger, //nolint:gosec // validated positive, far below 2^31
		Agent: &agent.Agent{
			Driver: driver, Images: driver, Init: init, Region: cfg.Region, Logger: logger, NewID: newID,
			Storage: &objstore.Store{
				Endpoint: cfg.Storage.Endpoint, Region: cfg.Storage.Region, Bucket: cfg.Storage.Bucket,
				AccessKey: cfg.Storage.AccessKey, SecretKey: cfg.Storage.SecretKey,
				HTTP: &http.Client{Timeout: 2 * time.Minute}, Now: time.Now,
			},
		},
	}
	if err := node.Register(ctx); err != nil {
		return err
	}
	defer node.Wait()

	beat := make(chan struct{})
	go func() {
		defer close(beat)
		node.Heartbeat(ctx)
	}()
	defer func() { <-beat }()

	// Several instances start at once (a deploy of many services); each instance's own commands stay in order
	// because the scheduler and router group them by instance.
	g, gctx := errgroup.WithContext(ctx)
	for range commandWorkers {
		w := &bus.Worker{
			Queue: bus.NewQueue(pool), Pool: pool, ID: bus.WorkerID(), Kind: bus.NodeCommands(node.ID),
			Lease: time.Minute, Poll: 2 * time.Second, Logger: logger,
		}
		g.Go(func() error { return w.Run(gctx, node.Handle) })
	}
	logger.Info("greyship-agent ready", "node", cfg.NodeName, "node_id", node.ID, "docker_host", cfg.DockerHost, "command_workers", commandWorkers)
	return g.Wait()
}

// commandWorkers is how many instance commands the agent carries out at once.
const commandWorkers = 4

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// requireRunsc refuses to start without gVisor: instances never run in a plain container.
func requireRunsc(ctx context.Context, docker, host string) error {
	cmd := exec.CommandContext(ctx, docker, "info", "--format", "{{json .Runtimes}}") //nolint:gosec // fixed arguments
	cmd.Env = []string{"DOCKER_HOST=" + host, "HOME=/tmp"}
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("docker at %s isn't reachable (run make sandbox): %w", host, err)
	}
	if !strings.Contains(string(out), `"runsc"`) {
		return errors.New("the docker at " + host + " has no runsc runtime; instances need gVisor (run make sandbox)")
	}
	return nil
}
