package sched

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/store"
)

// EnvChanged rolls a project's live services onto new variables without a rebuild (F-42, architecture §4.4):
// for each service with instances, start a fresh instance (it reads the new values), wait for it to be
// healthy, then stop the old ones. A service with nothing running needs nothing: its next start reads them.
// Main variables reach every service, so every running service rolls; overrides arrive with per-service settings.
func (s *Scheduler) EnvChanged(ctx context.Context, job bus.Job) error {
	var payload struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("decode job %d: %w", job.ID, err)
	}
	q := store.New(s.Pool)
	services, err := q.LiveServicesOfProject(ctx, payload.ProjectID)
	if err != nil {
		return fmt.Errorf("list services of %s: %w", payload.ProjectID, err)
	}
	for _, svc := range services {
		if err := s.roll(ctx, svc); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) roll(ctx context.Context, row store.LiveServicesOfProjectRow) error {
	q := store.New(s.Pool)
	old, err := q.LiveServiceInstances(ctx, row.ID)
	if err != nil || len(old) == 0 {
		return err
	}
	svc := store.Service{ID: row.ID, ProjectID: row.ProjectID, Name: row.Name, MemoryMb: row.MemoryMb}
	nodes, err := q.ListNodeLoad(ctx, nodeFresh.Seconds())
	if err != nil {
		return fmt.Errorf("load nodes: %w", err)
	}
	node, err := place(nodes, svc.MemoryMb)
	if err != nil {
		s.Logger.Warn("can't roll service onto new variables", "service", svc.ID, "err", err)
		return nil
	}
	fresh, err := s.startInstance(ctx, svc, row.ArtifactID, row.DeploymentID, node)
	if err != nil {
		return err
	}
	if ok, err := s.waitRunning(ctx, q, fresh.ID); err != nil {
		return err
	} else if !ok {
		// The new instance didn't come up (a bad value, maybe); the old ones keep serving with the old values.
		s.Logger.Warn("new instance didn't start after an env change; keeping the old ones", "service", svc.ID, "instance", fresh.ID)
		return s.stopInstances(ctx, []store.Instance{fresh})
	}
	return s.stopInstances(ctx, old)
}

func (s *Scheduler) waitRunning(ctx context.Context, q *store.Queries, id string) (bool, error) {
	deadline := s.Now().Add(startTimeout)
	for s.Now().Before(deadline) {
		inst, err := q.GetInstance(ctx, id)
		if err != nil {
			return false, err
		}
		switch inst.State {
		case "running":
			return true, nil
		case "failed", "exited", "stopped":
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(s.Poll):
		}
	}
	return false, nil
}

func (s *Scheduler) stopInstances(ctx context.Context, instances []store.Instance) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	for _, inst := range instances {
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
