package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"greyship.tech/internal/store"
)

type memLogs struct {
	mu    sync.Mutex
	lines []store.AppendAppLogsParams
	calls int
}

func (m *memLogs) AppendAppLogs(_ context.Context, lines []store.AppendAppLogsParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.lines = append(m.lines, lines...)
	return int64(len(lines)), nil
}

func TestAppLogBatchesAndCapsFloods(t *testing.T) {
	sink := &memLogs{}
	now := time.Unix(100, 0)
	l := NewAppLog(sink, "p1", "s1", "i1", func() time.Time { return now })
	for i := 0; i < appLinesPerSec+250; i++ {
		l.Add("stdout", "line")
	}
	now = now.Add(time.Second)
	l.Add("stderr", strings.Repeat("x", appLineBytes+10))
	l.Close()

	if got := len(sink.lines); got != appLinesPerSec+2 {
		t.Fatalf("stored %d lines, want the cap, a dropped-lines note and the next second's line", got)
	}
	note := sink.lines[appLinesPerSec]
	if note.Stream != "system" || note.Line != "250 lines dropped: more than 1000 lines a second" {
		t.Fatalf("note = %+v", note)
	}
	last := sink.lines[len(sink.lines)-1]
	if last.Stream != "stderr" || len(last.Line) > appLineBytes+4 || last.ProjectID != "p1" || last.InstanceID != "i1" {
		t.Fatalf("last = %+v", last)
	}
	if sink.calls > 12 {
		t.Fatalf("%d inserts, want batches of 100", sink.calls)
	}
}
