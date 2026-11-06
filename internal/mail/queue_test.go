package mail

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type flaky struct {
	mu       sync.Mutex
	failures int
	sent     []Message
	calls    int
}

func (f *flaky) Send(_ context.Context, m Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failures {
		return errors.New("provider unavailable")
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *flaky) count() (calls, sent int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, len(f.sent)
}

func newTestQueue(f *flaky, size int, logs *bytes.Buffer) *Queue {
	q := NewQueue(f, size, slog.New(slog.NewTextHandler(logs, nil)))
	q.backoff = func(int) time.Duration { return time.Millisecond }
	return q
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestQueueDelivery(t *testing.T) {
	tests := []struct {
		name      string
		failures  int
		wantCalls int
		wantSent  int
	}{
		{"first try", 0, 1, 1},
		{"retries then succeeds", 2, 3, 1},
		{"gives up after three attempts", 5, 3, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			f := &flaky{failures: tt.failures}
			q := newTestQueue(f, 4, &logs)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); q.Run(ctx, time.Second) }()

			if err := q.Send(context.Background(), Message{To: "haki@acme.dev", Subject: "123456 is your Greyship code", Text: "code 123456"}); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { calls, _ := f.count(); return calls == tt.wantCalls })
			time.Sleep(20 * time.Millisecond)
			cancel()
			<-done

			if calls, sent := f.count(); calls != tt.wantCalls || sent != tt.wantSent {
				t.Fatalf("calls=%d sent=%d, want %d/%d", calls, sent, tt.wantCalls, tt.wantSent)
			}
			if strings.Contains(logs.String(), "123456") || strings.Contains(logs.String(), "haki@") {
				t.Fatalf("logs leak the code or address: %s", logs.String())
			}
		})
	}
}

func TestQueueFullAndDrain(t *testing.T) {
	var logs bytes.Buffer
	f := &flaky{}
	q := newTestQueue(f, 2, &logs)

	// Nothing is running yet, so the third message doesn't fit.
	for i := range 2 {
		if err := q.Send(context.Background(), Message{To: "a@b.co"}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if err := q.Send(context.Background(), Message{To: "a@b.co"}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("third send err = %v, want ErrQueueFull", err)
	}

	// Already cancelled: Run must still drain what was queued before returning.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q.Run(ctx, time.Second)
	if _, sent := f.count(); sent != 2 {
		t.Fatalf("drained %d, want 2", sent)
	}
}
