package sched

import (
	"context"
	"fmt"
	"time"

	"greyship.tech/internal/build"
	"greyship.tech/internal/store"
)

// releaseWait is how long the scheduler waits for a release command: the agent stops it at 10 minutes
// (decision 012), so this only covers an agent that never reports back.
const releaseWait = 12 * time.Minute

// runReleaseCommand runs the deployment's release command (F-29) once, before any of its services start, and
// reports why it failed ("" when it succeeded or there is none). One release per project runs at a time.
func (s *Scheduler) runReleaseCommand(ctx context.Context, q *store.Queries, d store.Deployment, log *build.Log) (string, error) {
	artifacts, err := q.ListArtifacts(ctx, d.ID)
	if err != nil {
		return "", fmt.Errorf("list artifacts of %s: %w", d.ID, err)
	}
	var art *store.Artifact
	for i := range artifacts {
		if artifacts[i].ServiceType == "release" {
			art = &artifacts[i]
		}
	}
	if art == nil {
		return "", nil
	}

	lock, err := s.Pool.Acquire(ctx)
	if err != nil {
		return "", err
	}
	defer lock.Release()
	if _, err := lock.Exec(ctx, "SELECT pg_advisory_lock(hashtext($1))", "release:"+d.ProjectID); err != nil {
		return "", fmt.Errorf("take release lock: %w", err)
	}
	defer func() {
		_, _ = lock.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtext($1))", "release:"+d.ProjectID)
	}()

	svc, err := q.UpsertService(ctx, store.UpsertServiceParams{ProjectID: d.ProjectID, Name: "release", Path: art.ServicePath, Type: "release"})
	if err != nil {
		return "", fmt.Errorf("record release service: %w", err)
	}
	if err := q.SetArtifactService(ctx, store.SetArtifactServiceParams{ID: art.ID, ServiceID: &svc.ID}); err != nil {
		return "", err
	}

	// A repeated job finds the instance an earlier attempt started and waits for it instead of running it twice.
	var inst store.Instance
	rows, err := q.ListDeploymentInstances(ctx, d.ID)
	if err != nil {
		return "", err
	}
	for _, r := range rows {
		if r.ServiceID == svc.ID {
			inst = store.Instance{ID: r.ID, State: r.State, Error: r.Error}
		}
	}
	if inst.ID == "" {
		nodes, err := q.ListNodeLoad(ctx, nodeFresh.Seconds())
		if err != nil {
			return "", err
		}
		node, err := place(nodes, svc.MemoryMb)
		if err != nil {
			return "no capacity to run the release command", nil
		}
		if inst, err = s.startInstance(ctx, svc, art.ID, d.ID, node); err != nil {
			return "", err
		}
		log.System(fmt.Sprintf("running the release command (%s) on %s", art.ServicePath, node.Name))
	}

	deadline := s.Now().Add(releaseWait)
	for {
		cur, err := q.GetInstance(ctx, inst.ID)
		if err != nil {
			return "", err
		}
		switch cur.State {
		case "stopped":
			if cur.Error == nil {
				log.System("release command finished")
				return "", nil
			}
			return "release command didn't finish: " + *cur.Error, nil
		case "exited", "failed":
			reason := "it failed"
			if cur.Error != nil {
				reason = *cur.Error
			}
			return "release command failed: " + reason, nil
		}
		if s.Now().After(deadline) {
			return "release command didn't report back in time", nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(s.Poll):
		}
	}
}
