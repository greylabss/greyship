// Package projdel finishes deleting a project (decisions.md 056): its instances stop first, so no container is
// left behind, then its stored objects go, then the project and everything that cascades from it.
package projdel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/sched"
	"greyship.tech/internal/store"
)

// Objects deletes stored objects by key.
type Objects interface {
	Delete(ctx context.Context, key string) error
}

// ErrStillRunning means an instance hasn't stopped yet; the job is retried later.
var ErrStillRunning = errors.New("the project's instances are still stopping")

type Deleter struct {
	Pool    *pgxpool.Pool
	Objects Objects
	Logger  *slog.Logger
	// Wait is how long one attempt waits for instances to stop; Poll is how often it looks.
	Wait, Poll time.Duration
}

// Handle is the project.delete job.
func (d *Deleter) Handle(ctx context.Context, job bus.Job) error {
	var payload struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("decode job %d: %w", job.ID, err)
	}
	q := store.New(d.Pool)
	if _, err := q.GetDeletingProject(ctx, payload.ProjectID); errors.Is(err, pgx.ErrNoRows) {
		return nil // already gone
	} else if err != nil {
		return fmt.Errorf("load project %s: %w", payload.ProjectID, err)
	}
	if err := d.stopInstances(ctx, q, payload.ProjectID); err != nil {
		return err
	}
	keys, err := q.ProjectObjectKeys(ctx, payload.ProjectID)
	if err != nil {
		return fmt.Errorf("list objects of %s: %w", payload.ProjectID, err)
	}
	for _, key := range keys {
		if err := d.Objects.Delete(ctx, key); err != nil {
			d.Logger.Warn("delete stored object of a deleted project", "project", payload.ProjectID, "key", key, "err", err)
		}
	}
	if err := q.DeleteProject(ctx, payload.ProjectID); err != nil {
		return fmt.Errorf("delete project %s: %w", payload.ProjectID, err)
	}
	d.Logger.Info("deleted a project", "project", payload.ProjectID, "objects", len(keys))
	return nil
}

// stopInstances asks every live instance to stop and waits until none holds a container.
func (d *Deleter) stopInstances(ctx context.Context, q *store.Queries, projectID string) error {
	deadline := time.Now().Add(d.Wait)
	asked := map[string]bool{}
	for {
		live, err := q.ProjectLiveInstances(ctx, projectID)
		if err != nil {
			return fmt.Errorf("list instances of %s: %w", projectID, err)
		}
		if len(live) == 0 {
			return nil
		}
		if err := d.ask(ctx, live, asked); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return ErrStillRunning
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.Poll):
		}
	}
}

func (d *Deleter) ask(ctx context.Context, live []store.Instance, asked map[string]bool) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	n := 0
	for _, inst := range live {
		if asked[inst.ID] {
			continue
		}
		asked[inst.ID] = true
		if err := q.MarkInstanceStopping(ctx, inst.ID); err != nil {
			return err
		}
		if err := sched.SendCommand(ctx, q, inst.NodeID, sched.Command{Action: "instance.stop", InstanceID: inst.ID}); err != nil {
			return err
		}
		n++
	}
	if n == 0 {
		return nil
	}
	if err := q.NotifyRoutes(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
