// Package bus is how components hand work to each other (architecture §2.7). Stage 0 is Postgres: a jobs table
// claimed with FOR UPDATE SKIP LOCKED, and LISTEN/NOTIFY to wake workers (decisions.md 005).
package bus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/store"
)

// Job kinds, named after the bus subjects in architecture §2.7.
const (
	BuildRequested    = "build.requested"
	BuildVerify       = "build.verify" // checks binaries a developer built locally
	DeploymentRelease = "deployment.release"
	ScaleUp           = "service.scale_up"
	EnvChanged        = "env.changed"
	Notify            = "notify"         // a deploy ended or a project was made; see internal/notify
	ProjectDelete     = "project.delete" // stops a deleted project's instances, then removes it and its stored objects
)

// Notification event kinds, as Notify payloads carry them.
const (
	DeploySucceeded = "deploy.succeeded"
	DeployFailed    = "deploy.failed"
	ProjectCreated  = "project.created"
)

// EnqueueNotify asks for a notification inside the caller's transaction, so one is sent only for something that
// really happened. subject is the deployment, or the project for ProjectCreated; user is who to tell, when the
// subject doesn't record it.
func EnqueueNotify(ctx context.Context, q *store.Queries, kind, subject, user string) error {
	_, err := Enqueue(ctx, q, Notify, map[string]string{"kind": kind, "subject": subject, "user": user})
	return err
}

// NodeCommands is the job kind a node's agent takes commands from.
func NodeCommands(nodeID string) string { return "node." + nodeID }

var (
	ErrNoJob = errors.New("no job ready")
	// ErrLeaseLost means the lease ran out and another worker may own the job now; drop the result.
	ErrLeaseLost = errors.New("job lease lost")
)

type Job = store.Job

// Enqueue adds a job through q, so it can share the caller's transaction: the job exists only if the
// transaction commits, and waiting workers are notified at commit.
func Enqueue(ctx context.Context, q *store.Queries, kind string, payload any) (Job, error) {
	return EnqueueGrouped(ctx, q, kind, Group{}, payload)
}

// EnqueueGrouped adds a job that runs only while no other job of its kind and group is running: one build per
// team at a time, for example. An empty group means no such limit.
// Group puts a job in a group that runs at most Limit jobs of its kind at once; Priority orders claims.
type Group struct {
	Key      string
	Limit    int32
	Priority int32
}

func EnqueueGrouped(ctx context.Context, q *store.Queries, kind string, group Group, payload any) (Job, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Job{}, fmt.Errorf("encode %s payload: %w", kind, err)
	}
	var job Job
	if group.Key == "" {
		job, err = q.EnqueueJob(ctx, store.EnqueueJobParams{Kind: kind, Payload: b})
	} else {
		job, err = q.EnqueueGroupedJob(ctx, store.EnqueueGroupedJobParams{
			Kind: kind, Payload: b, GroupKey: &group.Key, GroupLimit: max(group.Limit, 1), Priority: group.Priority,
		})
	}
	if err != nil {
		return Job{}, fmt.Errorf("enqueue %s: %w", kind, err)
	}
	if err := q.NotifyJobs(ctx, kind); err != nil {
		return Job{}, fmt.Errorf("notify %s: %w", kind, err)
	}
	return job, nil
}

type Queue struct {
	pool *pgxpool.Pool
}

func NewQueue(pool *pgxpool.Pool) *Queue {
	return &Queue{pool: pool}
}

// Claim leases the next ready job of kind to worker. A job whose earlier lease ran out on its last attempt
// is failed instead of handed out again.
func (q *Queue) Claim(ctx context.Context, kind, worker string, lease time.Duration) (Job, error) {
	s := store.New(q.pool)
	for {
		job, err := q.claimOne(ctx, kind, worker, lease)
		if errors.Is(err, pgx.ErrNoRows) {
			return Job{}, ErrNoJob
		}
		if err != nil {
			return Job{}, fmt.Errorf("claim %s: %w", kind, err)
		}
		if job.Attempts <= job.MaxAttempts {
			return job, nil
		}
		if err := s.GiveUpJob(ctx, store.GiveUpJobParams{ID: job.ID, Worker: worker}); err != nil {
			return Job{}, fmt.Errorf("give up job %d: %w", job.ID, err)
		}
	}
}

// claimOne claims inside a transaction that holds the kind's claim lock, so group limits hold even when several
// workers claim at the same moment. Claims are quick; taking turns costs nothing noticeable.
func (q *Queue) claimOne(ctx context.Context, kind, worker string, lease time.Duration) (Job, error) {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	s := store.New(tx)
	if err := s.LockClaims(ctx, kind); err != nil {
		return Job{}, err
	}
	job, err := s.ClaimJob(ctx, store.ClaimJobParams{Kind: kind, Worker: worker, LeaseSeconds: lease.Seconds()})
	if err != nil {
		return Job{}, err
	}
	return job, tx.Commit(ctx)
}

func (q *Queue) Complete(ctx context.Context, job Job, worker string) error {
	_, err := store.New(q.pool).CompleteJob(ctx, store.CompleteJobParams{ID: job.ID, Worker: worker})
	return leaseErr(err, "complete", job.ID)
}

// Fail records the error; the job is retried later unless that was its last attempt.
func (q *Queue) Fail(ctx context.Context, job Job, worker string, cause error) error {
	_, err := store.New(q.pool).FailJob(ctx, store.FailJobParams{ID: job.ID, Worker: worker, LastError: cause.Error()})
	return leaseErr(err, "fail", job.ID)
}

func (q *Queue) Extend(ctx context.Context, job Job, worker string, lease time.Duration) error {
	_, err := store.New(q.pool).ExtendJobLease(ctx, store.ExtendJobLeaseParams{ID: job.ID, Worker: worker, LeaseSeconds: lease.Seconds()})
	return leaseErr(err, "extend lease of", job.ID)
}

func leaseErr(err error, action string, id int64) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("%s job %d: %w", action, id, err)
	}
	return nil
}

// WorkerID names a worker in leases: host, process and a random suffix, so two workers never share one.
func WorkerID() string {
	host, _ := os.Hostname()
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(b[:]))
}
