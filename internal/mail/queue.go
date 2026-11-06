package mail

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

var ErrQueueFull = errors.New("mail queue is full")

type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Queue sends mail in the background so requests don't wait on the provider.
// It is in memory on purpose: login codes must never be written to disk in plain text,
// and a code lost to a restart is recovered by the user tapping "Resend code".
type Queue struct {
	next     Sender
	ch       chan Message
	logger   *slog.Logger
	attempts int
	backoff  func(attempt int) time.Duration
	timeout  time.Duration
}

func NewQueue(next Sender, size int, logger *slog.Logger) *Queue {
	return &Queue{
		next:     next,
		ch:       make(chan Message, size),
		logger:   logger,
		attempts: 3,
		backoff:  func(attempt int) time.Duration { return time.Duration(attempt*attempt) * time.Second },
		timeout:  15 * time.Second,
	}
}

// Send enqueues and returns immediately; it fails only when the queue is full.
func (q *Queue) Send(_ context.Context, m Message) error {
	select {
	case q.ch <- m:
		return nil
	default:
		return ErrQueueFull
	}
}

// Run delivers queued mail until ctx is cancelled, then spends up to drain finishing what's queued.
func (q *Queue) Run(ctx context.Context, drain time.Duration) {
	for {
		select {
		case m := <-q.ch:
			q.deliver(ctx, m)
		case <-ctx.Done():
			q.drain(drain)
			return
		}
	}
}

func (q *Queue) drain(limit time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	for {
		select {
		case m := <-q.ch:
			q.deliver(ctx, m)
		default:
			return
		}
		if ctx.Err() != nil {
			q.logger.Warn("mail queue: dropped unsent mail at shutdown", "remaining", len(q.ch))
			return
		}
	}
}

func (q *Queue) deliver(ctx context.Context, m Message) {
	for attempt := 1; attempt <= q.attempts; attempt++ {
		sendCtx, cancel := context.WithTimeout(ctx, q.timeout)
		err := q.next.Send(sendCtx, m)
		cancel()
		if err == nil {
			return
		}
		// Never log the message body or the full address: the body holds the login code.
		q.logger.Warn("mail send failed", "to_domain", domainOf(m.To), "attempt", attempt, "err", err)
		if attempt == q.attempts {
			return
		}
		select {
		case <-time.After(q.backoff(attempt)):
		case <-ctx.Done():
			return
		}
	}
}

func domainOf(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		return addr[i+1:]
	}
	return ""
}
