package bus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler does one job. It may run more than once for the same job (a lease can run out mid-way),
// so it must be safe to repeat. Its context is cancelled if the lease is lost or the worker stops.
type Handler func(ctx context.Context, job Job) error

type Worker struct {
	Queue  *Queue
	Pool   *pgxpool.Pool
	ID     string
	Kind   string
	Lease  time.Duration // renewed every Lease/3 while the handler runs
	Poll   time.Duration // fallback when a notification is missed
	Logger *slog.Logger
}

// Run takes jobs of w.Kind one at a time until ctx is cancelled. It sleeps on LISTEN jobs between jobs.
func (w *Worker) Run(ctx context.Context, handle Handler) error {
	conn, err := w.listen(ctx)
	if err != nil {
		return err
	}
	defer func() { conn.Release() }()

	for ctx.Err() == nil {
		job, err := w.Queue.Claim(ctx, w.Kind, w.ID, w.Lease)
		switch {
		case err == nil:
			w.process(ctx, job, handle)
			continue
		case ctx.Err() != nil:
			return nil
		case !errors.Is(err, ErrNoJob):
			w.Logger.Error("claim job", "kind", w.Kind, "err", err)
		}

		waitCtx, cancel := context.WithTimeout(ctx, w.Poll)
		_, err = conn.Conn().WaitForNotification(waitCtx)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		// A timed-out wait can leave the connection unusable; take a fresh one and listen again.
		if err != nil && conn.Conn().IsClosed() {
			fresh, err := w.listen(ctx)
			if err != nil {
				return err
			}
			conn.Release()
			conn = fresh
		}
	}
	return nil
}

func (w *Worker) listen(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := w.Pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire listen connection: %w", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN jobs"); err != nil {
		conn.Release()
		return nil, fmt.Errorf("listen for jobs: %w", err)
	}
	return conn, nil
}

func (w *Worker) process(ctx context.Context, job Job, handle Handler) {
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	heartbeat := make(chan struct{})
	go func() {
		defer close(heartbeat)
		w.keepLease(jobCtx, cancel, job)
	}()

	err := safeHandle(jobCtx, job, handle)
	cancel()
	<-heartbeat

	if ctx.Err() != nil {
		// Shutting down: leave the lease to run out so another worker picks the job up.
		return
	}
	log := w.Logger.With("job", job.ID, "kind", job.Kind, "attempt", job.Attempts)
	if err != nil {
		log.Warn("job failed", "err", err)
		err = w.Queue.Fail(ctx, job, w.ID, err)
	} else {
		err = w.Queue.Complete(ctx, job, w.ID)
	}
	if err != nil {
		log.Error("record job result", "err", err)
	}
}

// keepLease renews the lease until ctx ends; losing it cancels the handler.
func (w *Worker) keepLease(ctx context.Context, cancel context.CancelFunc, job Job) {
	t := time.NewTicker(w.Lease / 3)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.Queue.Extend(ctx, job, w.ID, w.Lease); err != nil {
				if ctx.Err() == nil {
					w.Logger.Warn("lost job lease", "job", job.ID, "err", err)
					cancel()
				}
				return
			}
		}
	}
}

func safeHandle(ctx context.Context, job Job, handle Handler) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("handler panicked: %v", p)
		}
	}()
	return handle(ctx, job)
}
