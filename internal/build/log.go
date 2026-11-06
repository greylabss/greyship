package build

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"greyship.tech/internal/store"
)

const (
	maxLineBytes = 4 << 10
	maxLines     = 10_000
	flushEvery   = 250 * time.Millisecond
	flushLines   = 50
)

// LogSink stores a batch of build log lines.
type LogSink interface {
	AppendBuildLog(ctx context.Context, lines []store.AppendBuildLogParams) (int64, error)
}

// Log collects a deployment's build log and writes it in batches, so a chatty build costs a few inserts
// per second, not one per line. Lines are capped in length and number.
type Log struct {
	sink       LogSink
	deployment string
	now        func() time.Time

	mu        sync.Mutex
	seq       int32
	pending   []store.AppendBuildLogParams
	truncated bool
	err       error

	stop chan struct{}
	done chan struct{}
}

// NewLog starts a log whose next line is number after+1, so a later stage (the release) can continue a build's log.
func NewLog(sink LogSink, deployment string, after int32, now func() time.Time) *Log {
	l := &Log{sink: sink, deployment: deployment, seq: after, now: now, stop: make(chan struct{}), done: make(chan struct{})}
	go l.run()
	return l
}

// System records a line from Greyship itself ("building api"); Build records output of the go command.
func (l *Log) System(line string) { l.add("system", line) }

func (l *Log) Build(line string) { l.add("build", line) }

func (l *Log) add(stream, line string) {
	line = strings.TrimRight(line, "\r\n")
	if len(line) > maxLineBytes {
		line = truncateUTF8(line, maxLineBytes) + " …"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.truncated {
		return
	}
	if l.seq >= maxLines {
		l.truncated = true
		stream, line = "system", "log truncated: more than 10000 lines"
	}
	l.seq++
	l.pending = append(l.pending, store.AppendBuildLogParams{DeploymentID: l.deployment, Seq: l.seq, Stream: stream, Line: line, Ts: l.now()})
	if len(l.pending) >= flushLines {
		l.flushLocked()
	}
}

func (l *Log) run() {
	defer close(l.done)
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			l.mu.Lock()
			l.flushLocked()
			l.mu.Unlock()
		}
	}
}

func (l *Log) flushLocked() {
	if len(l.pending) == 0 || l.err != nil {
		return
	}
	// Writes outlive the job's context on purpose: the last lines explain why a build stopped.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := l.sink.AppendBuildLog(ctx, l.pending); err != nil {
		l.err = err
	}
	l.pending = nil
}

// Close writes what is left and stops the flusher. It returns the first write error, if any.
func (l *Log) Close() error {
	close(l.stop)
	<-l.done
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushLocked()
	return l.err
}

func truncateUTF8(s string, n int) string {
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// lineWriter turns a byte stream (the sandbox's stderr) into whole lines.
type lineWriter struct {
	emit func(string)
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		w.emit(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	// A line with no newline in sight is flushed in pieces rather than held forever.
	if len(w.buf) > maxLineBytes {
		w.emit(string(w.buf))
		w.buf = nil
	}
	return len(p), nil
}

func (w *lineWriter) Close() {
	if len(w.buf) > 0 {
		w.emit(string(w.buf))
		w.buf = nil
	}
}

// KeepsServing is the line that ends a failed deploy's log, saying what visitors get now: a failed deploy never
// changes what's live (F-24).
func KeepsServing(ctx context.Context, q *store.Queries, projectID string) string {
	rel, err := q.GetRelease(ctx, store.GetReleaseParams{ProjectID: projectID, Env: "production"})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "nothing changed: this project has no live version yet"
	case err != nil:
		return "nothing changed: the previous version, if there is one, keeps serving"
	}
	return fmt.Sprintf("nothing changed: the live version (deployment %s) keeps serving", rel.DeploymentID[:min(8, len(rel.DeploymentID))])
}
