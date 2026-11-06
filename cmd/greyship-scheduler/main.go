package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/sched"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("greyship-scheduler stopped", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	dbURL := os.Getenv("GREYSHIP_DATABASE_URL")
	if dbURL == "" {
		return errors.New("GREYSHIP_DATABASE_URL is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pgcfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return err
	}
	// Each of the three job workers holds a connection for LISTEN, and the leader lock holds one; the default pool
	// is as big as the CPU count, so on a small server none were left to claim jobs.
	pgcfg.MaxConns = 14
	pool, err := pgxpool.NewWithConfig(ctx, pgcfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	logger.Info("greyship-scheduler waiting for the leader lock")
	lock, err := sched.Lead(ctx, pool)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer lock.Release()
	// If the connection holding the lock dies, another scheduler may take over; stop rather than run as two leaders.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	watch := make(chan struct{})
	go func() {
		defer close(watch)
		for ctx.Err() == nil {
			if err := lock.Ping(ctx); err != nil && ctx.Err() == nil {
				cancel(errors.New("lost the leader lock connection"))
				return
			}
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
		}
	}()
	defer func() { cancel(nil); <-watch }()

	s := &sched.Scheduler{Pool: pool, Logger: logger, Now: time.Now, Poll: 500 * time.Millisecond}
	worker := func(kind string) *bus.Worker {
		return &bus.Worker{
			Queue: bus.NewQueue(pool), Pool: pool, ID: bus.WorkerID(), Kind: kind,
			Lease: 2 * time.Minute, Poll: 2 * time.Second, Logger: logger,
		}
	}
	logger.Info("greyship-scheduler leading")
	// Releases can take minutes; scale-ups must not wait behind them, so each kind has its own worker.
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return worker(bus.DeploymentRelease).Run(gctx, s.Release) })
	g.Go(func() error { return worker(bus.ScaleUp).Run(gctx, s.ScaleUp) })
	g.Go(func() error { return worker(bus.EnvChanged).Run(gctx, s.EnvChanged) })
	g.Go(func() error { s.SweepIdle(gctx, time.Minute); return nil })
	g.Go(func() error { s.SweepLogs(gctx, 10*time.Minute); return nil })
	err = g.Wait()
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return err
}
