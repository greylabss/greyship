package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"greyship.tech/internal/store"
)

const (
	appLineBytes   = 4 << 10
	appFlushEvery  = 250 * time.Millisecond
	appFlushLines  = 100
	appLinesPerSec = 1000 // above this an instance's lines are counted, not stored, until the next second
)

// AppLogSink stores a batch of app log lines.
type AppLogSink interface {
	AppendAppLogs(ctx context.Context, lines []store.AppendAppLogsParams) (int64, error)
}

// AppLog collects one instance's output and writes it in batches (F-60). A flood of output is capped per
// second, with a note saying how much was dropped, so one chatty app can't swamp the database.
type AppLog struct {
	sink                       AppLogSink
	project, service, instance string
	now                        func() time.Time

	mu      sync.Mutex
	pending []store.AppendAppLogsParams
	second  int64
	count   int
	dropped int

	stop chan struct{}
	done chan struct{}
}

func NewAppLog(sink AppLogSink, project, service, instance string, now func() time.Time) *AppLog {
	l := &AppLog{sink: sink, project: project, service: service, instance: instance, now: now, stop: make(chan struct{}), done: make(chan struct{})}
	go l.run()
	return l
}

// Add records one line; stream is stdout, stderr or system (Greyship's own notes about the instance).
func (l *AppLog) Add(stream, line string) {
	line = strings.TrimRight(line, "\r\n")
	if len(line) > appLineBytes {
		n := appLineBytes
		for n > 0 && !utf8.RuneStart(line[n]) {
			n--
		}
		line = line[:n] + " …"
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if sec := now.Unix(); sec != l.second {
		if l.dropped > 0 {
			l.pending = append(l.pending, l.row("system", fmt.Sprintf("%d lines dropped: more than %d lines a second", l.dropped, appLinesPerSec), now))
		}
		l.second, l.count, l.dropped = sec, 0, 0
	}
	if l.count >= appLinesPerSec {
		l.dropped++
		return
	}
	l.count++
	l.pending = append(l.pending, l.row(stream, line, now))
	if len(l.pending) >= appFlushLines {
		l.flushLocked()
	}
}

func (l *AppLog) row(stream, line string, ts time.Time) store.AppendAppLogsParams {
	return store.AppendAppLogsParams{ProjectID: l.project, ServiceID: l.service, InstanceID: l.instance, Stream: stream, Line: line, Ts: ts}
}

func (l *AppLog) run() {
	defer close(l.done)
	t := time.NewTicker(appFlushEvery)
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

func (l *AppLog) flushLocked() {
	if len(l.pending) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Logs are best effort: a failed write drops that batch rather than holding the app up.
	_, _ = l.sink.AppendAppLogs(ctx, l.pending)
	l.pending = nil
}

// Close writes what is left and stops the flusher.
func (l *AppLog) Close() {
	close(l.stop)
	<-l.done
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dropped > 0 {
		l.pending = append(l.pending, l.row("system", fmt.Sprintf("%d lines dropped: more than %d lines a second", l.dropped, appLinesPerSec), l.now()))
		l.dropped = 0
	}
	l.flushLocked()
}
