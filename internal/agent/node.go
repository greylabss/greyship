package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/runtime"
	"greyship.tech/internal/store"
)

const (
	heartbeatEvery = 10 * time.Second
	releaseTimeout = 10 * time.Minute // decision 012: a release that takes longer fails the deploy
)

// Leftovers clears instances a previous agent on this node left behind.
type Leftovers interface {
	StopLeftovers(ctx context.Context) error
}

// EnvSource gives one service its decrypted variables. Values exist in memory only for the start that needs them.
type EnvSource interface {
	Effective(ctx context.Context, projectID, serviceID, environment string) (map[string]string, error)
}

// Node is the agent process for one runtime host: it registers the node, sends heartbeats and carries out the
// scheduler's commands, recording each instance's state in Postgres.
type Node struct {
	Pool      *pgxpool.Pool
	Agent     *Agent
	Leftovers Leftovers
	Env       EnvSource
	Name      string
	Region    string
	TotalMB   int32
	Logger    *slog.Logger

	ID string // set by Register

	mu      sync.Mutex
	running map[string]Running
	watch   sync.WaitGroup
}

// Register records the node and starts it clean: instances from an earlier agent run are written off and
// their containers removed, since nothing tracks them any more.
func (n *Node) Register(ctx context.Context) error {
	q := store.New(n.Pool)
	node, err := q.UpsertNode(ctx, store.UpsertNodeParams{Name: n.Name, Region: n.Region, TotalMb: n.TotalMB})
	if err != nil {
		return fmt.Errorf("register node %s: %w", n.Name, err)
	}
	n.ID = node.ID
	if err := n.Leftovers.StopLeftovers(ctx); err != nil {
		return fmt.Errorf("clear leftover instances: %w", err)
	}
	if err := q.StopNodeInstances(ctx, n.ID); err != nil {
		return fmt.Errorf("write off old instances: %w", err)
	}
	return nil
}

// Heartbeat tells the scheduler the node is alive until ctx ends.
func (n *Node) Heartbeat(ctx context.Context) {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := store.New(n.Pool).HeartbeatNode(ctx, n.ID); err != nil && ctx.Err() == nil {
				n.Logger.Warn("node heartbeat", "err", err)
			}
		}
	}
}

// Wait returns once every exit watcher has finished; call it after the context that ran the node is done.
func (n *Node) Wait() { n.watch.Wait() }

type command struct {
	Action     string `json:"action"`
	InstanceID string `json:"instance_id"`
}

// Handle carries out one command. Commands repeat safely: starting an instance that is no longer
// "starting" and stopping one that is already gone do nothing.
func (n *Node) Handle(ctx context.Context, job bus.Job) error {
	var cmd command
	if err := json.Unmarshal(job.Payload, &cmd); err != nil {
		return fmt.Errorf("decode command %d: %w", job.ID, err)
	}
	switch cmd.Action {
	case "instance.start":
		return n.start(ctx, cmd.InstanceID)
	case "instance.stop":
		return n.stop(ctx, cmd.InstanceID)
	case "instance.pause":
		return n.pause(ctx, cmd.InstanceID)
	case "instance.resume":
		return n.resume(ctx, cmd.InstanceID)
	default:
		n.Logger.Warn("unknown node command", "action", cmd.Action)
		return nil
	}
}

func (n *Node) start(ctx context.Context, id string) error {
	q := store.New(n.Pool)
	inst, err := q.InstanceDetail(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load instance %s: %w", id, err)
	}
	if inst.State != "starting" {
		return nil
	}
	vars, err := n.Env.Effective(ctx, inst.ProjectID, inst.ServiceID, "production")
	if err != nil {
		// Ours to retry: the store or the key is unavailable. The values themselves are never part of the error.
		return fmt.Errorf("load env for instance %s: %w", id, err)
	}
	var files *Artifact
	if inst.FilesKey != nil && inst.FilesSha256 != nil {
		files = &Artifact{ObjectKey: *inst.FilesKey, SHA256: *inst.FilesSha256}
	}
	logs := NewAppLog(q, inst.ProjectID, inst.ServiceID, id, time.Now)
	run, err := n.Agent.Start(ctx, Instance{
		ID:           id,
		DeploymentID: inst.DeploymentID,
		Service:      inst.ServiceName,
		Type:         inst.ServiceType,
		Artifact:     Artifact{ObjectKey: inst.ObjectKey, SHA256: inst.Sha256},
		Files:        files,
		MemoryMB:     int(inst.MemoryMb),
		CPU:          float64(inst.CpuMillis) / 1000,
		Env:          vars,
	}, logs.Add)
	if ctx.Err() != nil {
		logs.Close()
		return ctx.Err()
	}
	if err != nil {
		logs.Add("system", "didn't start: "+err.Error())
		logs.Close()
		reason := err.Error()
		var he *HealthError
		if errors.As(err, &he) && len(he.Output) > 0 {
			reason += "\n" + strings.Join(he.Output, "\n")
		}
		if merr := q.MarkInstanceFailed(ctx, store.MarkInstanceFailedParams{ID: id, Error: &reason}); merr != nil {
			return fmt.Errorf("record failed instance %s: %w", id, merr)
		}
		return nil
	}
	addr := run.Addr
	if err := q.MarkInstanceRunning(ctx, store.MarkInstanceRunningParams{ID: id, Addr: &addr}); err != nil {
		_ = n.Agent.Stop(context.Background(), run)
		return fmt.Errorf("record running instance %s: %w", id, err)
	}
	n.mu.Lock()
	if n.running == nil {
		n.running = map[string]Running{}
	}
	n.running[id] = run
	n.mu.Unlock()
	// A router holding requests for this service (a wake from zero) lets them through now.
	if err := q.NotifyRoutes(ctx); err != nil {
		n.Logger.Warn("notify routers", "instance", id, "err", err)
	}

	// An instance that stops on its own (crash, exit) is recorded; one stopped on purpose is already "stopping".
	// A release command is expected to exit: 0 is success, anything else (or running too long) is a failure.
	n.watch.Add(1)
	go func() { //nolint:gosec // outlives the command on purpose: it watches the instance for its whole life
		defer n.watch.Done()
		var timeout <-chan time.Time
		if inst.ServiceType == "release" {
			timeout = time.After(releaseTimeout)
		}
		var ex runtime.Exit
		timedOut := false
		select {
		case ex = <-run.Exited():
		case <-timeout:
			timedOut = true
			_ = n.Agent.Stop(context.Background(), run)
			ex = <-run.Exited()
		}
		logs.Add("system", fmt.Sprintf("instance stopped (exit code %d)", ex.Code))
		logs.Close()
		n.mu.Lock()
		delete(n.running, id)
		n.mu.Unlock()
		q := store.New(n.Pool)
		var err error
		switch {
		case inst.ServiceType == "release" && ex.Code == 0 && !timedOut:
			err = q.MarkInstanceStopped(context.Background(), id)
		default:
			reason := fmt.Sprintf("the app exited with code %d", ex.Code)
			if timedOut {
				reason = fmt.Sprintf("it ran longer than %s and was stopped", releaseTimeout)
			}
			if inst.ServiceType == "release" {
				reason += "\n" + strings.Join(run.Output(), "\n")
			}
			err = q.MarkInstanceExited(context.Background(), store.MarkInstanceExitedParams{ID: id, Error: &reason})
		}
		if err != nil {
			n.Logger.Error("record exited instance", "instance", id, "err", err)
		}
		// Routers drop the instance now rather than at their next resync.
		_ = q.NotifyRoutes(context.Background())
	}()
	return nil
}

func (n *Node) stop(ctx context.Context, id string) error {
	n.mu.Lock()
	run, ok := n.running[id]
	n.mu.Unlock()
	var err error
	if ok {
		err = n.Agent.Stop(ctx, run)
	} else {
		err = n.Agent.StopID(ctx, id)
	}
	if err != nil {
		return fmt.Errorf("stop instance %s: %w", id, err)
	}
	q := store.New(n.Pool)
	if err := q.MarkInstanceStopped(ctx, id); err != nil {
		return fmt.Errorf("record stopped instance %s: %w", id, err)
	}
	return q.NotifyRoutes(ctx)
}

// pause freezes an idle instance (F-31): it keeps its memory and stops using CPU. Only a running instance pauses.
func (n *Node) pause(ctx context.Context, id string) error {
	q := store.New(n.Pool)
	inst, err := q.GetInstance(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && inst.State != "running" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load instance %s: %w", id, err)
	}
	if err := n.Agent.Driver.Pause(ctx, id); err != nil {
		return fmt.Errorf("pause instance %s: %w", id, err)
	}
	if _, err := q.MarkInstancePaused(ctx, id); err != nil {
		return fmt.Errorf("record paused instance %s: %w", id, err)
	}
	return q.NotifyRoutes(ctx)
}

// resume unfreezes a paused instance; it carries on exactly where it stopped (F-80).
func (n *Node) resume(ctx context.Context, id string) error {
	q := store.New(n.Pool)
	inst, err := q.GetInstance(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && inst.State != "paused" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load instance %s: %w", id, err)
	}
	if err := n.Agent.Driver.Resume(ctx, id); err != nil {
		return fmt.Errorf("resume instance %s: %w", id, err)
	}
	if _, err := q.MarkInstanceResumed(ctx, id); err != nil {
		return fmt.Errorf("record resumed instance %s: %w", id, err)
	}
	return q.NotifyRoutes(ctx)
}
