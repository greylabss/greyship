//go:build integration

package bus_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/store"
	"greyship.tech/internal/testdb"
)

func enqueue(t *testing.T, pool *pgxpool.Pool, kind string) bus.Job {
	t.Helper()
	job, err := bus.Enqueue(context.Background(), store.New(pool), kind, map[string]string{"deployment_id": "d1"})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func get(t *testing.T, pool *pgxpool.Pool, id int64) bus.Job {
	t.Helper()
	job, err := store.New(pool).GetJob(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

// The step 7 "done when": one job, many workers racing, exactly one gets it.
func TestExactlyOneWorkerClaims(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := bus.NewQueue(pool)
	job := enqueue(t, pool, "test.race")

	var winners atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := q.Claim(ctx, "test.race", fmt.Sprintf("worker-%d", i), time.Minute)
			if errors.Is(err, bus.ErrNoJob) {
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			if got.ID != job.ID {
				t.Errorf("claimed job %d, want %d", got.ID, job.ID)
			}
			winners.Add(1)
		}()
	}
	close(start)
	wg.Wait()
	if n := winners.Load(); n != 1 {
		t.Fatalf("%d workers claimed the job, want exactly 1", n)
	}
	if _, err := q.Claim(ctx, "test.race", "late", time.Minute); !errors.Is(err, bus.ErrNoJob) {
		t.Fatalf("claim after it was taken = %v, want ErrNoJob", err)
	}
}

func TestCrashedWorkerLosesJob(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := bus.NewQueue(pool)
	job := enqueue(t, pool, "test.crash")

	first, err := q.Claim(ctx, "test.crash", "crashed", 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Claim(ctx, "test.crash", "other", time.Minute); !errors.Is(err, bus.ErrNoJob) {
		t.Fatalf("claim during a live lease = %v, want ErrNoJob", err)
	}
	time.Sleep(400 * time.Millisecond)

	second, err := q.Claim(ctx, "test.crash", "rescuer", time.Minute)
	if err != nil {
		t.Fatalf("claim after the lease ran out: %v", err)
	}
	if second.ID != job.ID || second.Attempts != 2 {
		t.Fatalf("reclaimed %+v, want job %d on attempt 2", second, job.ID)
	}
	if err := q.Complete(ctx, first, "crashed"); !errors.Is(err, bus.ErrLeaseLost) {
		t.Fatalf("late complete by the crashed worker = %v, want ErrLeaseLost", err)
	}
	if err := q.Complete(ctx, second, "rescuer"); err != nil {
		t.Fatal(err)
	}
	if got := get(t, pool, job.ID); got.Status != "done" || got.FinishedAt == nil {
		t.Fatalf("job = %+v, want done", got)
	}
}

func TestRetriesThenFails(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := bus.NewQueue(pool)
	job := enqueue(t, pool, "test.retry")
	if _, err := pool.Exec(ctx, "UPDATE jobs SET max_attempts = 2 WHERE id = $1", job.ID); err != nil {
		t.Fatal(err)
	}

	claimed, err := q.Claim(ctx, "test.retry", "w", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Fail(ctx, claimed, "w", errors.New("go build: exit 1")); err != nil {
		t.Fatal(err)
	}
	got := get(t, pool, job.ID)
	if got.Status != "queued" || !got.RunAfter.After(time.Now()) || *got.LastError != "go build: exit 1" {
		t.Fatalf("after first failure = %+v, want queued for later", got)
	}
	if _, err := q.Claim(ctx, "test.retry", "w", time.Minute); !errors.Is(err, bus.ErrNoJob) {
		t.Fatalf("claim during backoff = %v, want ErrNoJob", err)
	}

	if _, err := pool.Exec(ctx, "UPDATE jobs SET run_after = now() WHERE id = $1", job.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = q.Claim(ctx, "test.retry", "w", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Fail(ctx, claimed, "w", errors.New("still broken")); err != nil {
		t.Fatal(err)
	}
	if got := get(t, pool, job.ID); got.Status != "failed" || got.Attempts != 2 || got.FinishedAt == nil {
		t.Fatalf("after last attempt = %+v, want failed", got)
	}
}

func TestLeaseExpiredOnLastAttemptIsNotRunAgain(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := bus.NewQueue(pool)
	job := enqueue(t, pool, "test.exhaust")
	if _, err := pool.Exec(ctx, "UPDATE jobs SET max_attempts = 1 WHERE id = $1", job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Claim(ctx, "test.exhaust", "crashed", 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := q.Claim(ctx, "test.exhaust", "next", time.Minute); !errors.Is(err, bus.ErrNoJob) {
		t.Fatalf("claim = %v, want ErrNoJob", err)
	}
	if got := get(t, pool, job.ID); got.Status != "failed" || got.LastError == nil {
		t.Fatalf("job = %+v, want failed with a reason", got)
	}
}

func TestEnqueueRolledBack(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Enqueue(ctx, store.New(tx), "test.tx", nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.NewQueue(pool).Claim(ctx, "test.tx", "w", time.Minute); !errors.Is(err, bus.ErrNoJob) {
		t.Fatalf("claim after rollback = %v, want ErrNoJob", err)
	}
}

func TestWorkerRun(t *testing.T) {
	pool := testdb.New(t)
	ctx, stop := context.WithCancel(context.Background())
	w := &bus.Worker{
		Queue: bus.NewQueue(pool), Pool: pool, ID: bus.WorkerID(), Kind: "test.run",
		Lease: 300 * time.Millisecond, Poll: time.Minute, Logger: slog.New(slog.DiscardHandler),
	}
	got := make(chan bus.Job, 4)
	done := make(chan error, 1)
	go func() {
		done <- w.Run(ctx, func(ctx context.Context, job bus.Job) error {
			// Longer than the lease: the heartbeat has to keep it.
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return ctx.Err()
			}
			got <- job
			return nil
		})
	}()
	// Let the worker reach its LISTEN before enqueueing; the poll is a minute, so only NOTIFY can wake it.
	time.Sleep(200 * time.Millisecond)
	job := enqueue(t, pool, "test.run")

	select {
	case j := <-got:
		if j.ID != job.ID {
			t.Fatalf("handled job %d, want %d", j.ID, job.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker didn't pick up the job; NOTIFY didn't wake it")
	}
	deadline := time.Now().Add(2 * time.Second)
	for get(t, pool, job.ID).Status != "done" {
		if time.Now().After(deadline) {
			t.Fatalf("job = %+v, want done", get(t, pool, job.ID))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if a := get(t, pool, job.ID).Attempts; a != 1 {
		t.Fatalf("attempts = %d, want 1 (the lease must not run out while the handler works)", a)
	}

	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker didn't stop when its context was cancelled")
	}
}

func TestWorkerRecordsFailureAndPanic(t *testing.T) {
	pool := testdb.New(t)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	w := &bus.Worker{
		Queue: bus.NewQueue(pool), Pool: pool, ID: "w", Kind: "test.panic",
		Lease: time.Minute, Poll: 50 * time.Millisecond, Logger: slog.New(slog.DiscardHandler),
	}
	job := enqueue(t, pool, "test.panic")
	go func() { _ = w.Run(ctx, func(context.Context, bus.Job) error { panic("boom") }) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		got := get(t, pool, job.ID)
		if got.Status == "queued" && got.LastError != nil {
			if *got.LastError != "handler panicked: boom" {
				t.Fatalf("last error = %q", *got.LastError)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job = %+v, want re-queued with the panic recorded", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Many poll timeouts in a row must leave the worker able to listen and claim.
func TestWorkerSurvivesPollTimeouts(t *testing.T) {
	pool := testdb.New(t)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	w := &bus.Worker{
		Queue: bus.NewQueue(pool), Pool: pool, ID: "w", Kind: "test.poll",
		Lease: time.Minute, Poll: 20 * time.Millisecond, Logger: slog.New(slog.DiscardHandler),
	}
	got := make(chan int64, 1)
	go func() {
		_ = w.Run(ctx, func(_ context.Context, job bus.Job) error {
			got <- job.ID
			return nil
		})
	}()
	time.Sleep(300 * time.Millisecond)
	job := enqueue(t, pool, "test.poll")
	select {
	case id := <-got:
		if id != job.ID {
			t.Fatalf("handled %d, want %d", id, job.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker stopped picking up jobs after poll timeouts")
	}
}

func enqueueGrouped(t *testing.T, pool *pgxpool.Pool, kind, group string) bus.Job {
	t.Helper()
	job, err := bus.EnqueueGrouped(context.Background(), store.New(pool), kind, bus.Group{Key: group, Limit: 1}, map[string]string{"group": group})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

// One job per group at a time: a team's second build waits while another team's goes ahead.
func TestOneRunningJobPerGroup(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := bus.NewQueue(pool)
	a1 := enqueueGrouped(t, pool, "test.group", "team-a")
	a2 := enqueueGrouped(t, pool, "test.group", "team-a")
	b1 := enqueueGrouped(t, pool, "test.group", "team-b")

	first, err := q.Claim(ctx, "test.group", "w1", time.Minute)
	if err != nil || first.ID != a1.ID {
		t.Fatalf("first claim = %v %v, want team-a's first job", first.ID, err)
	}
	second, err := q.Claim(ctx, "test.group", "w2", time.Minute)
	if err != nil || second.ID != b1.ID {
		t.Fatalf("second claim = %v %v, want team-b's job, not team-a's second", second.ID, err)
	}
	if _, err := q.Claim(ctx, "test.group", "w3", time.Minute); !errors.Is(err, bus.ErrNoJob) {
		t.Fatalf("third claim = %v, want nothing while team-a's first job runs", err)
	}
	if err := q.Complete(ctx, first, "w1"); err != nil {
		t.Fatal(err)
	}
	third, err := q.Claim(ctx, "test.group", "w3", time.Minute)
	if err != nil || third.ID != a2.ID {
		t.Fatalf("after team-a's first finished = %v %v, want its second", third.ID, err)
	}
}

// A plan's concurrency lets a group run several jobs, and a higher priority is claimed first.
func TestGroupLimitAndPriority(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := bus.NewQueue(pool)
	s := store.New(pool)
	add := func(group string, limit, priority int32) bus.Job {
		job, err := bus.EnqueueGrouped(ctx, s, "test.plan", bus.Group{Key: group, Limit: limit, Priority: priority}, map[string]string{})
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	free := add("free-team", 1, 0)
	pro1 := add("pro-team", 2, 10)
	pro2 := add("pro-team", 2, 10)
	pro3 := add("pro-team", 2, 10)

	for i, want := range []int64{pro1.ID, pro2.ID, free.ID} {
		got, err := q.Claim(ctx, "test.plan", "w", time.Minute)
		if err != nil || got.ID != want {
			t.Fatalf("claim %d = %v %v, want %d (paid first, two at once for pro)", i, got.ID, err, want)
		}
	}
	if _, err := q.Claim(ctx, "test.plan", "w", time.Minute); !errors.Is(err, bus.ErrNoJob) {
		t.Fatalf("pro's third job ran past its limit of 2: %v (job %d)", err, pro3.ID)
	}
}

// Even when many workers claim at the same instant, one group never gets two jobs running.
func TestGroupLimitHoldsUnderRace(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := bus.NewQueue(pool)
	for i := 0; i < 5; i++ {
		enqueueGrouped(t, pool, "test.grouprace", "team-a")
	}
	var claimed atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := q.Claim(ctx, "test.grouprace", fmt.Sprintf("w%d", i), time.Minute); err == nil {
				claimed.Add(1)
			} else if !errors.Is(err, bus.ErrNoJob) {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if n := claimed.Load(); n != 1 {
		t.Fatalf("%d jobs of one group running at once, want 1", n)
	}
}
