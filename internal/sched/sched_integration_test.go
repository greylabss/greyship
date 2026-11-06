//go:build integration

package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/sched"
	"greyship.tech/internal/store"
	"greyship.tech/internal/testdb"
)

type world struct {
	t       *testing.T
	pool    *pgxpool.Pool
	q       *store.Queries
	project store.Project
	node    store.Node
	seq     int
}

func newWorld(t *testing.T, nodeMB int32) *world {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	q := store.New(pool)
	team, err := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "acme", Name: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := q.CreateProject(ctx, store.CreateProjectParams{TeamID: team.ID, Name: "shop", Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := q.UpsertNode(ctx, store.UpsertNodeParams{Name: "dev-1", Region: "dev", TotalMb: nodeMB})
	if err != nil {
		t.Fatal(err)
	}
	return &world{t: t, pool: pool, q: q, project: p, node: node}
}

// built makes a deployment in "built" with an api (web) and a worker artifact.
func (w *world) built() store.Deployment {
	w.t.Helper()
	ctx := context.Background()
	w.seq++
	id := fmt.Sprintf("00000000-0000-4000-8000-%012d", w.seq)
	d, err := w.q.CreateDeployment(ctx, store.CreateDeploymentParams{ID: id, ProjectID: w.project.ID, Source: "cli", SourceKey: ptr("k"), SourceSize: ptr(int64(1)), SourceSha256: ptr(strings.Repeat("a", 64))})
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.pool.Exec(ctx, "UPDATE deployments SET status = 'built' WHERE id = $1", id); err != nil {
		w.t.Fatal(err)
	}
	for _, svc := range []struct{ name, typ string }{{"api", "web"}, {"worker", "worker"}, {"release", "release"}} {
		_, err := w.q.CreateArtifact(ctx, store.CreateArtifactParams{DeploymentID: id, ServiceName: svc.name, ServicePath: "./cmd/" + svc.name, ServiceType: svc.typ, ObjectKey: "artifacts/" + id + "/" + svc.name, Sha256: strings.Repeat("b", 64), SizeBytes: 1, GoVersion: "1.24", Goarch: "arm64"})
		if err != nil {
			w.t.Fatal(err)
		}
	}
	return d
}

func (w *world) status(id string) string {
	d, err := w.q.GetDeployment(context.Background(), id)
	if err != nil {
		w.t.Fatal(err)
	}
	return d.Status
}

func (w *world) instances(id string) []store.ListDeploymentInstancesRow {
	rows, err := w.q.ListDeploymentInstances(context.Background(), id)
	if err != nil {
		w.t.Fatal(err)
	}
	return rows
}

// fakeAgent carries out node commands the way greyship-agent does, minus the sandboxes. fail names a service
// whose instances fail their health check.
type fakeAgent struct {
	w           *world
	mu          sync.Mutex
	fail        string
	failRelease bool
	starts      int
}

func (a *fakeAgent) run(ctx context.Context) {
	worker := &bus.Worker{Queue: bus.NewQueue(a.w.pool), Pool: a.w.pool, ID: "fake-agent", Kind: bus.NodeCommands(a.w.node.ID), Lease: time.Minute, Poll: 50 * time.Millisecond, Logger: slog.New(slog.DiscardHandler)}
	_ = worker.Run(ctx, func(ctx context.Context, job bus.Job) error {
		var cmd sched.Command
		_ = json.Unmarshal(job.Payload, &cmd)
		q := store.New(a.w.pool)
		inst, err := q.InstanceDetail(ctx, cmd.InstanceID)
		if err != nil {
			return err
		}
		switch cmd.Action {
		case "instance.start":
			a.mu.Lock()
			a.starts++
			fail := a.fail
			a.mu.Unlock()
			if inst.ServiceType == "release" {
				addr := ""
				if err := q.MarkInstanceRunning(ctx, store.MarkInstanceRunningParams{ID: inst.ID, Addr: &addr}); err != nil {
					return err
				}
				a.mu.Lock()
				failRelease := a.failRelease
				a.mu.Unlock()
				if failRelease {
					reason := "the app exited with code 1\nmigrate: relation \"users\" already exists"
					return q.MarkInstanceExited(ctx, store.MarkInstanceExitedParams{ID: inst.ID, Error: &reason})
				}
				return q.MarkInstanceStopped(ctx, inst.ID)
			}
			if inst.ServiceName == fail {
				reason := "health check failed: the app exited with code 2\nboom: missing DATABASE_URL"
				return q.MarkInstanceFailed(ctx, store.MarkInstanceFailedParams{ID: inst.ID, Error: &reason})
			}
			addr := "127.0.0.1:40000"
			return q.MarkInstanceRunning(ctx, store.MarkInstanceRunningParams{ID: inst.ID, Addr: &addr})
		case "instance.stop":
			return q.MarkInstanceStopped(ctx, inst.ID)
		}
		return nil
	})
}

func (w *world) release(ctx context.Context, d store.Deployment) error {
	s := &sched.Scheduler{Pool: w.pool, Logger: slog.New(slog.DiscardHandler), Now: time.Now, Poll: 50 * time.Millisecond}
	payload, _ := json.Marshal(map[string]string{"deployment_id": d.ID})
	return s.Release(ctx, bus.Job{Payload: payload})
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestReleaseReplacesPreviousDeployment(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	w := newWorld(t, 2048)
	agent := &fakeAgent{w: w}
	go agent.run(ctx)

	first := w.built()
	if err := w.release(ctx, first); err != nil {
		t.Fatal(err)
	}
	if got := w.status(first.ID); got != "ready" {
		t.Fatalf("first deployment = %s, want ready", got)
	}
	insts := w.instances(first.ID)
	if len(insts) != 3 || insts[0].ServiceName != "api" || insts[1].ServiceName != "release" || insts[2].ServiceName != "worker" || insts[0].State != "running" || insts[1].State != "stopped" {
		t.Fatalf("instances = %+v, want api and worker running and the release command run once", insts)
	}

	second := w.built()
	if err := w.release(ctx, second); err != nil {
		t.Fatal(err)
	}
	if w.status(second.ID) != "ready" || w.status(first.ID) != "superseded" {
		t.Fatalf("statuses = %s / %s, want ready / superseded", w.status(second.ID), w.status(first.ID))
	}
	rel, _ := w.q.GetRelease(ctx, store.GetReleaseParams{ProjectID: w.project.ID, Env: "production"})
	if rel.DeploymentID != second.ID {
		t.Fatalf("live deployment = %s, want the second", rel.DeploymentID)
	}
	waitFor(t, "old instances to stop", func() bool {
		for _, i := range w.instances(first.ID) {
			if i.State != "stopped" {
				return false
			}
		}
		return true
	})

	// Routing: one public service with a stable subdomain, pointing only at the live deployment's instance.
	routes, err := w.q.RoutingTable(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Subdomain != "api-shop" || routes[0].InstanceID == nil {
		t.Fatalf("routes = %+v, want api-shop only", routes)
	}
	for _, i := range w.instances(second.ID) {
		if i.ServiceName == "api" && i.ID != *routes[0].InstanceID {
			t.Fatal("the route points at an instance that isn't the live deployment's")
		}
	}

	// Releasing again (a repeated job) changes nothing.
	if err := w.release(ctx, second); err != nil {
		t.Fatal(err)
	}
	if n := len(w.instances(second.ID)); n != 3 {
		t.Fatalf("second release run made %d instances, want 3 (api, worker, the release command once)", n)
	}
	lines, _ := w.q.BuildLogsAfter(ctx, store.BuildLogsAfterParams{DeploymentID: second.ID, Seq: 0, Limit: 100})
	var log []string
	for _, l := range lines {
		log = append(log, l.Line)
	}
	if text := strings.Join(log, "\n"); !strings.Contains(text, "starting api on dev-1") || !strings.Contains(text, "stopping 2 instance(s) of the previous version") {
		t.Fatalf("release log:\n%s", text)
	}
}

func TestFailedReleaseLeavesLiveDeploymentAlone(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	w := newWorld(t, 2048)
	agent := &fakeAgent{w: w}
	go agent.run(ctx)

	live := w.built()
	if err := w.release(ctx, live); err != nil {
		t.Fatal(err)
	}
	agent.mu.Lock()
	agent.fail = "api"
	agent.mu.Unlock()
	broken := w.built()
	if err := w.release(ctx, broken); err != nil {
		t.Fatal(err)
	}

	d, _ := w.q.GetDeployment(ctx, broken.ID)
	if d.Status != "failed" || d.Error == nil || !strings.Contains(*d.Error, "api didn't start on dev-1: health check failed") {
		t.Fatalf("broken deployment = %s %v", d.Status, d.Error)
	}
	if strings.Contains(*d.Error, "boom") {
		t.Fatal("the deployment error should be the summary; the app's output belongs in the log")
	}
	if w.status(live.ID) != "ready" {
		t.Fatalf("live deployment = %s, want still ready", w.status(live.ID))
	}
	for _, i := range w.instances(live.ID) {
		if i.ServiceName != "release" && i.State != "running" {
			t.Fatalf("live instance %s = %s, want running", i.ServiceName, i.State)
		}
	}
	waitFor(t, "the broken deployment's healthy instances to stop", func() bool {
		for _, i := range w.instances(broken.ID) {
			if i.State == "running" || i.State == "stopping" {
				return false
			}
		}
		return true
	})
	lines, _ := w.q.BuildLogsAfter(ctx, store.BuildLogsAfterParams{DeploymentID: broken.ID, Seq: 0, Limit: 100})
	var log []string
	for _, l := range lines {
		log = append(log, l.Line)
	}
	if text := strings.Join(log, "\n"); !strings.Contains(text, "boom: missing DATABASE_URL") || !strings.Contains(text, "deploy failed: api didn't start") || !strings.Contains(text, "nothing changed: the live version (deployment "+live.ID[:8]+") keeps serving") {
		t.Fatalf("release log:\n%s", text)
	}
}

func TestReleaseResumesWithoutDuplicates(t *testing.T) {
	w := newWorld(t, 2048)
	d := w.built()

	// No agent yet: the release creates its instances, then the scheduler "crashes" while waiting.
	crashCtx, crash := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer crash()
	if err := w.release(crashCtx, d); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first run = %v, want it cut off", err)
	}
	if n := len(w.instances(d.ID)); n != 1 {
		t.Fatalf("instances after the crash = %d, want just the release command (services start after it)", n)
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	agent := &fakeAgent{w: w}
	go agent.run(ctx)
	if err := w.release(ctx, d); err != nil {
		t.Fatal(err)
	}
	if w.status(d.ID) != "ready" || len(w.instances(d.ID)) != 3 {
		t.Fatalf("after resume: %s with %d instances", w.status(d.ID), len(w.instances(d.ID)))
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.starts != 3 {
		t.Fatalf("agent got %d start commands, want 3 (the release command only once)", agent.starts)
	}
}

func TestNoCapacity(t *testing.T) {
	w := newWorld(t, 100)
	d := w.built()
	if err := w.release(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	got, _ := w.q.GetDeployment(context.Background(), d.ID)
	if got.Status != "failed" || got.Error == nil || !strings.Contains(*got.Error, "no capacity") {
		t.Fatalf("deployment = %s %v", got.Status, got.Error)
	}
}

func TestStaleNodeGetsNoWork(t *testing.T) {
	w := newWorld(t, 2048)
	if _, err := w.pool.Exec(context.Background(), "UPDATE nodes SET last_heartbeat = now() - interval '5 minutes'"); err != nil {
		t.Fatal(err)
	}
	d := w.built()
	if err := w.release(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if got := w.status(d.ID); got != "failed" {
		t.Fatalf("deployment = %s, want failed: the only node is silent", got)
	}
}

func TestOneLeader(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	first, err := sched.Lead(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	second := make(chan error, 1)
	go func() {
		conn, err := sched.Lead(ctx, pool)
		if err == nil {
			conn.Release()
		}
		second <- err
	}()
	select {
	case <-second:
		t.Fatal("a second scheduler became leader while the first held the lock")
	case <-time.After(300 * time.Millisecond):
	}
	// Destroying the connection ends the session and frees the lock, as a crashed leader would.
	if err := first.Conn().Close(ctx); err != nil {
		t.Fatal(err)
	}
	first.Release()
	select {
	case err := <-second:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the standby never took over")
	}
}

func (w *world) scale(ctx context.Context, serviceID, reason string) {
	w.t.Helper()
	s := &sched.Scheduler{Pool: w.pool, Logger: slog.New(slog.DiscardHandler), Now: time.Now, Poll: 50 * time.Millisecond}
	payload, _ := json.Marshal(sched.ScaleRequest{ServiceID: serviceID, Reason: reason})
	if err := s.ScaleUp(ctx, bus.Job{Payload: payload}); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) live(serviceID string) []store.Instance {
	w.t.Helper()
	rows, err := w.q.LiveServiceInstances(context.Background(), serviceID)
	if err != nil {
		w.t.Fatal(err)
	}
	return rows
}

func TestScaleUp(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	w := newWorld(t, 2048)
	agent := &fakeAgent{w: w}
	go agent.run(ctx)
	d := w.built()
	if err := w.release(ctx, d); err != nil {
		t.Fatal(err)
	}
	api := w.instances(d.ID)[0]
	if api.ServiceName != "api" {
		t.Fatal("expected api first")
	}

	w.scale(ctx, api.ServiceID, "wake")
	if n := len(w.live(api.ServiceID)); n != 1 {
		t.Fatalf("wake with an instance up made %d instances, want still 1", n)
	}

	w.scale(ctx, api.ServiceID, "busy")
	waitFor(t, "a second instance", func() bool {
		live := w.live(api.ServiceID)
		return len(live) == 2 && live[0].State == "running" && live[1].State == "running"
	})
	w.scale(ctx, api.ServiceID, "busy")
	if n := len(w.live(api.ServiceID)); n != 2 {
		t.Fatalf("busy at max_instances made %d instances, want 2", n)
	}

	// Idle long enough to be stopped: the next request has nothing to resume and asks for a fresh instance.
	for _, i := range w.live(api.ServiceID) {
		if err := w.q.MarkInstanceStopped(ctx, i.ID); err != nil {
			t.Fatal(err)
		}
	}
	w.scale(ctx, api.ServiceID, "wake")
	waitFor(t, "a fresh instance after a full stop", func() bool {
		live := w.live(api.ServiceID)
		return len(live) == 1 && live[0].State == "running"
	})
}

func TestIdlePausedInstancesAreStopped(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	w := newWorld(t, 2048)
	agent := &fakeAgent{w: w}
	go agent.run(ctx)
	d := w.built()
	if err := w.release(ctx, d); err != nil {
		t.Fatal(err)
	}
	api := w.instances(d.ID)[0]
	if _, err := w.pool.Exec(ctx, "UPDATE instances SET state = 'paused', paused_at = now() - interval '25 hours' WHERE id = $1", api.ID); err != nil {
		t.Fatal(err)
	}
	s := &sched.Scheduler{Pool: w.pool, Logger: slog.New(slog.DiscardHandler), Now: time.Now, Poll: 50 * time.Millisecond}
	sweepCtx, stopSweep := context.WithCancel(ctx)
	go s.SweepIdle(sweepCtx, 50*time.Millisecond)
	defer stopSweep()
	waitFor(t, "the idle instance to stop", func() bool {
		i, _ := w.q.GetInstance(ctx, api.ID)
		return i.State == "stopped"
	})
	if i, _ := w.q.GetInstance(ctx, w.instances(d.ID)[2].ID); i.State != "running" {
		t.Fatalf("the worker (not paused) = %s, want running", i.State)
	}
}

func TestFailedReleaseCommandStopsTheDeploy(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	w := newWorld(t, 2048)
	agent := &fakeAgent{w: w}
	go agent.run(ctx)
	live := w.built()
	if err := w.release(ctx, live); err != nil {
		t.Fatal(err)
	}

	agent.mu.Lock()
	agent.failRelease = true
	agent.mu.Unlock()
	broken := w.built()
	if err := w.release(ctx, broken); err != nil {
		t.Fatal(err)
	}
	d, _ := w.q.GetDeployment(ctx, broken.ID)
	if d.Status != "failed" || d.Error == nil || *d.Error != "release command failed: the app exited with code 1" {
		t.Fatalf("deployment = %s %v", d.Status, d.Error)
	}
	for _, i := range w.instances(broken.ID) {
		if i.ServiceName != "release" {
			t.Fatalf("%s was started although the release command failed", i.ServiceName)
		}
	}
	if w.status(live.ID) != "ready" {
		t.Fatal("the live deployment must keep serving")
	}
	lines, _ := w.q.BuildLogsAfter(ctx, store.BuildLogsAfterParams{DeploymentID: broken.ID, Seq: 0, Limit: 100})
	var log []string
	for _, l := range lines {
		log = append(log, l.Line)
	}
	if text := strings.Join(log, "\n"); !strings.Contains(text, "running the release command (./cmd/release)") || !strings.Contains(text, `relation "users" already exists`) {
		t.Fatalf("log:\n%s", text)
	}
}

func ptr[T any](v T) *T { return &v }

// One instance's commands run in order; different instances' commands run side by side.
func TestCommandsRunInOrderPerInstance(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := store.New(pool)
	queue := bus.NewQueue(pool)
	kind := bus.NodeCommands("node-1")
	a1 := sched.Command{Action: "instance.start", InstanceID: "a"}
	a2 := sched.Command{Action: "instance.stop", InstanceID: "a"}
	b1 := sched.Command{Action: "instance.start", InstanceID: "b"}
	for _, c := range []sched.Command{a1, a2, b1} {
		if err := sched.SendCommand(ctx, q, "node-1", c); err != nil {
			t.Fatal(err)
		}
	}
	decode := func(j bus.Job) sched.Command {
		var c sched.Command
		_ = json.Unmarshal(j.Payload, &c)
		return c
	}
	first, err := queue.Claim(ctx, kind, "w1", time.Minute)
	if err != nil || decode(first) != a1 {
		t.Fatalf("first claim = %s %v, want a's start", first.Payload, err)
	}
	second, err := queue.Claim(ctx, kind, "w2", time.Minute)
	if err != nil || decode(second) != b1 {
		t.Fatalf("second claim = %s %v, want b's start beside a's, not a's stop", second.Payload, err)
	}
	if _, err := queue.Claim(ctx, kind, "w3", time.Minute); !errors.Is(err, bus.ErrNoJob) {
		t.Fatalf("a's stop must wait for a's start: %v", err)
	}
	if err := queue.Complete(ctx, first, "w1"); err != nil {
		t.Fatal(err)
	}
	third, err := queue.Claim(ctx, kind, "w3", time.Minute)
	if err != nil || decode(third) != a2 {
		t.Fatalf("after a's start: %s %v", third.Payload, err)
	}
}

// A rollback is a new deployment running an earlier one's binaries; its release command doesn't run again.
func TestRollbackSkipsTheReleaseCommand(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	w := newWorld(t, 2048)
	agent := &fakeAgent{w: w}
	go agent.run(ctx)
	first, second := w.built(), w.built()
	for _, d := range []store.Deployment{first, second} {
		if err := w.release(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	filesKey, filesSha, filesSize := "files/p/x.tar.gz", strings.Repeat("c", 64), int64(10)
	if err := w.q.SetDeploymentFiles(ctx, store.SetDeploymentFilesParams{ID: first.ID, FilesKey: &filesKey, FilesSha256: &filesSha, FilesSize: &filesSize}); err != nil {
		t.Fatal(err)
	}
	back, err := w.q.CreateRedeploy(ctx, store.CreateRedeployParams{ID: "00000000-0000-4000-8000-000000000099", Source: "cli", Of: first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.q.CopyArtifacts(ctx, store.CopyArtifactsParams{ToDeployment: back.ID, FromDeployment: first.ID}); err != nil {
		t.Fatal(err)
	}
	s := &sched.Scheduler{Pool: w.pool, Logger: slog.New(slog.DiscardHandler), Now: time.Now, Poll: 50 * time.Millisecond}
	payload, _ := json.Marshal(map[string]any{"deployment_id": back.ID, "skip_release_command": true})
	if err := s.Release(ctx, bus.Job{Payload: payload}); err != nil {
		t.Fatal(err)
	}

	if w.status(back.ID) != "ready" || w.status(second.ID) != "superseded" {
		t.Fatalf("statuses = %s / %s, want ready / superseded", w.status(back.ID), w.status(second.ID))
	}
	if back.FilesKey == nil || *back.FilesKey != filesKey || *back.FilesSha256 != filesSha {
		t.Fatalf("rollback files = %v, want the first deployment's", back.FilesKey)
	}
	if back.RollbackOf == nil || *back.RollbackOf != first.ID || back.SourceKey == nil || *back.SourceKey != "k" {
		t.Fatalf("rollback = %+v, want it to point at the first deployment and keep its source", back)
	}
	insts := w.instances(back.ID)
	if len(insts) != 2 {
		t.Fatalf("instances = %+v, want api and worker only", insts)
	}
	for _, i := range insts {
		if i.ServiceName == "release" {
			t.Fatal("the release command ran again on a rollback")
		}
	}
	arts, err := w.q.ListArtifacts(ctx, back.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 3 || !strings.HasPrefix(arts[0].ObjectKey, "artifacts/"+first.ID) {
		t.Fatalf("artifacts = %+v, want the first deployment's binaries", arts)
	}
}
