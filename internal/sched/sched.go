// Package sched decides where instances run and rolls deployments out (architecture §2.3). It writes desired
// state to Postgres and sends commands to node agents through the job queue; agents report back in the
// instances table.
package sched

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

	"greyship.tech/internal/build"
	"greyship.tech/internal/bus"
	"greyship.tech/internal/store"
)

const (
	env          = "production"
	nodeFresh    = 30 * time.Second // a node that hasn't sent a heartbeat for this long gets no work
	startTimeout = 2 * time.Minute
)

var ErrNoCapacity = errors.New("no node has room for this instance")

// place picks the ready node with the most free memory that still fits memoryMB.
func place(nodes []store.ListNodeLoadRow, memoryMB int32) (store.ListNodeLoadRow, error) {
	var best store.ListNodeLoadRow
	found := false
	for _, n := range nodes {
		free := n.TotalMb - n.UsedMb
		if free < memoryMB {
			continue
		}
		if !found || free > best.TotalMb-best.UsedMb {
			best, found = n, true
		}
	}
	if !found {
		return store.ListNodeLoadRow{}, ErrNoCapacity
	}
	return best, nil
}

type Scheduler struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	Now    func() time.Time
	Poll   time.Duration // how often a release checks on its instances
}

// Release rolls a built deployment out: start its instances, wait for them to be healthy, switch the live
// deployment, stop the old one's instances. If anything fails, the new instances go and nothing else changes.
// Safe to run again for the same deployment: it continues with the instances already created.
func (s *Scheduler) Release(ctx context.Context, job bus.Job) error {
	var payload struct {
		DeploymentID string `json:"deployment_id"`
		// Redeploys and rollbacks run binaries whose migrations already ran (F-27).
		SkipReleaseCommand bool `json:"skip_release_command"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("decode job %d: %w", job.ID, err)
	}
	q := store.New(s.Pool)
	d, err := q.StartRelease(ctx, payload.DeploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("start release %s: %w", payload.DeploymentID, err)
	}
	after, err := q.MaxBuildLogSeq(ctx, d.ID)
	if err != nil {
		return fmt.Errorf("read log position of %s: %w", d.ID, err)
	}
	log := build.NewLog(q, d.ID, after, s.Now)
	defer func() {
		if err := log.Close(); err != nil {
			s.Logger.Error("write release log", "deployment", d.ID, "err", err)
		}
	}()

	// Migrations run once, after the build and before any new instance or traffic switch (F-29).
	if payload.SkipReleaseCommand {
		log.System("redeploying the same binaries: no build, no release command")
	} else if msg, err := s.runReleaseCommand(ctx, q, d, log); err != nil {
		return err
	} else if msg != "" {
		return s.fail(ctx, d.ID, log, msg)
	}

	instances, err := s.ensureInstances(ctx, q, d, log)
	if errors.Is(err, ErrNoCapacity) {
		return s.fail(ctx, d.ID, log, "no capacity: no node has room for this deployment")
	}
	if err != nil {
		return err
	}
	if msg, err := s.waitHealthy(ctx, q, d.ID, instances); err != nil {
		return err
	} else if msg != "" {
		return s.fail(ctx, d.ID, log, msg)
	}
	return s.switchTraffic(ctx, d, log)
}

// ensureInstances creates one instance per web or worker service, unless an earlier attempt already did.
func (s *Scheduler) ensureInstances(ctx context.Context, q *store.Queries, d store.Deployment, log *build.Log) ([]store.Instance, error) {
	existing, err := q.ListDeploymentInstances(ctx, d.ID)
	if err != nil {
		return nil, fmt.Errorf("list instances of %s: %w", d.ID, err)
	}
	have := map[string]store.Instance{}
	for _, row := range existing {
		have[row.ServiceID] = store.Instance{ID: row.ID, ServiceID: row.ServiceID, NodeID: row.NodeID, State: row.State, Error: row.Error}
	}
	artifacts, err := q.ListArtifacts(ctx, d.ID)
	if err != nil {
		return nil, fmt.Errorf("list artifacts of %s: %w", d.ID, err)
	}
	p, err := q.GetProject(ctx, d.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("load project %s: %w", d.ProjectID, err)
	}

	var out []store.Instance
	for _, a := range artifacts {
		if a.ServiceType != "web" && a.ServiceType != "worker" {
			// Cron schedules and the release command come with their own steps.
			continue
		}
		svc, err := q.UpsertService(ctx, store.UpsertServiceParams{ProjectID: d.ProjectID, Name: a.ServiceName, Path: a.ServicePath, Type: a.ServiceType})
		if err != nil {
			return nil, fmt.Errorf("record service %s: %w", a.ServiceName, err)
		}
		if svc, err = ensureSubdomain(ctx, q, svc, p.Slug); err != nil {
			return nil, err
		}
		if err := q.SetArtifactService(ctx, store.SetArtifactServiceParams{ID: a.ID, ServiceID: &svc.ID}); err != nil {
			return nil, fmt.Errorf("link artifact %s: %w", a.ID, err)
		}
		if inst, ok := have[svc.ID]; ok {
			out = append(out, inst)
			continue
		}
		nodes, err := q.ListNodeLoad(ctx, nodeFresh.Seconds())
		if err != nil {
			return nil, fmt.Errorf("load nodes: %w", err)
		}
		node, err := place(nodes, svc.MemoryMb)
		if err != nil {
			return out, err
		}
		inst, err := s.startInstance(ctx, svc, a.ID, d.ID, node)
		if err != nil {
			return nil, err
		}
		log.System(fmt.Sprintf("starting %s on %s", svc.Name, node.Name))
		out = append(out, inst)
	}
	return out, nil
}

// startInstance records the instance and sends the start command in one transaction.
func (s *Scheduler) startInstance(ctx context.Context, svc store.Service, artifactID, deploymentID string, node store.ListNodeLoadRow) (store.Instance, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return store.Instance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	inst, err := q.CreateInstance(ctx, store.CreateInstanceParams{ServiceID: svc.ID, ArtifactID: artifactID, DeploymentID: deploymentID, NodeID: node.ID, MemoryMb: svc.MemoryMb})
	if err != nil {
		return store.Instance{}, fmt.Errorf("create instance of %s: %w", svc.Name, err)
	}
	if err := SendCommand(ctx, q, node.ID, Command{Action: "instance.start", InstanceID: inst.ID}); err != nil {
		return store.Instance{}, err
	}
	return inst, tx.Commit(ctx)
}

// Command is what the scheduler asks a node agent to do.
type Command struct {
	Action     string `json:"action"` // instance.start | instance.stop
	InstanceID string `json:"instance_id"`
}

// waitHealthy polls until every instance runs (""), or returns why one didn't.
func (s *Scheduler) waitHealthy(ctx context.Context, q *store.Queries, deploymentID string, instances []store.Instance) (string, error) {
	deadline := s.Now().Add(startTimeout)
	for {
		rows, err := q.ListDeploymentInstances(ctx, deploymentID)
		if err != nil {
			return "", fmt.Errorf("check instances of %s: %w", deploymentID, err)
		}
		running := 0
		for _, row := range rows {
			if !contains(instances, row.ID) {
				continue
			}
			switch row.State {
			case "running":
				running++
			case "failed", "exited", "stopped":
				reason := "it stopped"
				if row.Error != nil {
					reason = *row.Error
				}
				return fmt.Sprintf("%s didn't start on %s: %s", row.ServiceName, row.NodeName, reason), nil
			}
		}
		if running == len(instances) {
			return "", nil
		}
		if s.Now().After(deadline) {
			return "instances didn't become healthy within 2 minutes", nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(s.Poll):
		}
	}
}

func contains(instances []store.Instance, id string) bool {
	for _, i := range instances {
		if i.ID == id {
			return true
		}
	}
	return false
}

// switchTraffic makes the deployment live, marks the previous one superseded and stops its instances.
func (s *Scheduler) switchTraffic(ctx context.Context, d store.Deployment, log *build.Log) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	prev, err := q.GetRelease(ctx, store.GetReleaseParams{ProjectID: d.ProjectID, Env: env})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("load release of %s: %w", d.ProjectID, err)
	}
	if err := q.SetRelease(ctx, store.SetReleaseParams{ProjectID: d.ProjectID, Env: env, DeploymentID: d.ID}); err != nil {
		return fmt.Errorf("switch release of %s: %w", d.ProjectID, err)
	}
	if err := q.MarkDeploymentReady(ctx, d.ID); err != nil {
		return fmt.Errorf("mark %s ready: %w", d.ID, err)
	}
	if err := bus.EnqueueNotify(ctx, q, bus.DeploySucceeded, d.ID, ""); err != nil {
		return err
	}
	if err := q.NotifyRoutes(ctx); err != nil {
		return fmt.Errorf("notify routers: %w", err)
	}
	stopped := 0
	if prev.DeploymentID != "" && prev.DeploymentID != d.ID {
		if err := q.MarkDeploymentSuperseded(ctx, prev.DeploymentID); err != nil {
			return fmt.Errorf("supersede %s: %w", prev.DeploymentID, err)
		}
		if stopped, err = stopAll(ctx, q, prev.DeploymentID); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit release of %s: %w", d.ID, err)
	}
	if stopped > 0 {
		log.System(fmt.Sprintf("live; stopping %d instance(s) of the previous version", stopped))
	} else {
		log.System("live")
	}
	s.Logger.Info("deployment live", "deployment", d.ID, "project", d.ProjectID)
	return nil
}

// stopAll asks the agents to stop every live instance of a deployment.
func stopAll(ctx context.Context, q *store.Queries, deploymentID string) (int, error) {
	live, err := q.LiveInstancesOf(ctx, deploymentID)
	if err != nil {
		return 0, fmt.Errorf("list live instances of %s: %w", deploymentID, err)
	}
	for _, inst := range live {
		if err := q.MarkInstanceStopping(ctx, inst.ID); err != nil {
			return 0, fmt.Errorf("mark %s stopping: %w", inst.ID, err)
		}
		if err := SendCommand(ctx, q, inst.NodeID, Command{Action: "instance.stop", InstanceID: inst.ID}); err != nil {
			return 0, err
		}
	}
	return len(live), nil
}

// fail stops whatever this release started and fails the deployment; the live deployment is untouched (F-24, F-26).
// reason's first line is the summary; any further lines are the app's last output and go to the log as they are.
func (s *Scheduler) fail(ctx context.Context, deploymentID string, log *build.Log, reason string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	if _, err := stopAll(ctx, q, deploymentID); err != nil {
		return err
	}
	summary, output, _ := strings.Cut(reason, "\n")
	if err := q.FailRelease(ctx, store.FailReleaseParams{ID: deploymentID, Error: &summary}); err != nil {
		return fmt.Errorf("fail release %s: %w", deploymentID, err)
	}
	if err := bus.EnqueueNotify(ctx, q, bus.DeployFailed, deploymentID, ""); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, line := range strings.Split(output, "\n") {
		if line != "" {
			log.Build(line)
		}
	}
	log.System("deploy failed: " + summary)
	if d, err := store.New(s.Pool).GetDeployment(ctx, deploymentID); err == nil {
		log.System(build.KeepsServing(ctx, store.New(s.Pool), d.ProjectID))
	}
	s.Logger.Info("release failed", "deployment", deploymentID, "reason", summary)
	return nil
}

// leaderLock is the advisory lock that makes one scheduler the leader (architecture §2.3).
const leaderLock = 0x67726579 // "grey"

// Lead blocks until this process holds the leader lock. The lock lives as long as the returned connection;
// release it to step down. A standby simply waits here.
func Lead(ctx context.Context, pool *pgxpool.Pool) (*pgxpool.Conn, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire leader connection: %w", err)
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", int64(leaderLock)); err != nil {
		conn.Release()
		return nil, fmt.Errorf("take leader lock: %w", err)
	}
	return conn, nil
}

// SendCommand asks a node's agent to act on one instance. Commands are grouped by instance, so one instance's
// commands run in order (a stop never overtakes its start) while different instances' run side by side.
func SendCommand(ctx context.Context, q *store.Queries, nodeID string, cmd Command) error {
	_, err := bus.EnqueueGrouped(ctx, q, bus.NodeCommands(nodeID), bus.Group{Key: cmd.InstanceID, Limit: 1}, cmd)
	return err
}
