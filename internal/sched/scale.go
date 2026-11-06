package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/store"
)

// ScaleRequest is what the router asks for: a first instance for a service with none (wake), or one more
// because every instance is at its concurrency limit (busy, F-33).
type ScaleRequest struct {
	ServiceID string `json:"service_id"`
	Reason    string `json:"reason"` // wake | busy
}

// ScaleUp starts an instance of a service's live deployment if the request still holds. Requests repeat and
// race (the router sends one per waiting burst), so it re-checks instead of trusting the request.
func (s *Scheduler) ScaleUp(ctx context.Context, job bus.Job) error {
	var req ScaleRequest
	if err := json.Unmarshal(job.Payload, &req); err != nil {
		return fmt.Errorf("decode job %d: %w", job.ID, err)
	}
	q := store.New(s.Pool)
	svc, err := q.GetService(ctx, req.ServiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load service %s: %w", req.ServiceID, err)
	}
	live, err := q.LiveServiceInstances(ctx, svc.ID)
	if err != nil {
		return fmt.Errorf("list instances of %s: %w", svc.ID, err)
	}
	for _, i := range live {
		if i.State == "starting" {
			return nil // one is already on its way
		}
	}
	switch {
	case len(live) >= int(svc.MaxInstances):
		return nil
	case req.Reason == "wake" && len(live) > 0:
		return nil // a paused instance is there; the router resumes it
	}
	art, err := q.LiveServiceArtifact(ctx, &svc.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // nothing live to scale
	}
	if err != nil {
		return fmt.Errorf("load live artifact of %s: %w", svc.ID, err)
	}
	nodes, err := q.ListNodeLoad(ctx, nodeFresh.Seconds())
	if err != nil {
		return fmt.Errorf("load nodes: %w", err)
	}
	node, err := place(nodes, svc.MemoryMb)
	if errors.Is(err, ErrNoCapacity) {
		s.Logger.Warn("no capacity to scale up", "service", svc.ID, "reason", req.Reason)
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := s.startInstance(ctx, svc, art.ID, art.DeploymentID, node); err != nil {
		return err
	}
	s.Logger.Info("scaling up", "service", svc.ID, "reason", req.Reason, "node", node.Name)
	return nil
}

// SweepIdle stops instances that have been paused longer than their service's idle timeout (F-31), every
// interval until ctx ends. Stopped services start again on their next request.
func (s *Scheduler) SweepIdle(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.stopIdle(ctx); err != nil && ctx.Err() == nil {
				s.Logger.Warn("stop idle instances", "err", err)
			}
		}
	}
}

func (s *Scheduler) stopIdle(ctx context.Context) error {
	idle, err := store.New(s.Pool).IdlePausedInstances(ctx)
	if err != nil || len(idle) == 0 {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	for _, inst := range idle {
		if err := q.MarkInstanceStopping(ctx, inst.ID); err != nil {
			return err
		}
		if err := SendCommand(ctx, q, inst.NodeID, Command{Action: "instance.stop", InstanceID: inst.ID}); err != nil {
			return err
		}
	}
	if err := q.NotifyRoutes(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// logRetention is the free plan's (spec §6); per-plan retention comes with billing.
const logRetention = 24 * time.Hour

// SweepLogs deletes app log lines older than the retention, every interval until ctx ends (F-61).
func (s *Scheduler) SweepLogs(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := store.New(s.Pool).DeleteOldAppLogs(ctx, logRetention.Seconds()); err != nil && ctx.Err() == nil {
				s.Logger.Warn("delete old app logs", "err", err)
			}
		}
	}
}
