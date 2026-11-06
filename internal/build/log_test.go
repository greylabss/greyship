package build

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"greyship.tech/internal/store"
)

type memSink struct {
	mu      sync.Mutex
	batches [][]store.AppendBuildLogParams
}

func (m *memSink) AppendBuildLog(_ context.Context, lines []store.AppendBuildLogParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batches = append(m.batches, append([]store.AppendBuildLogParams(nil), lines...))
	return int64(len(lines)), nil
}

func (m *memSink) all() []store.AppendBuildLogParams {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.AppendBuildLogParams
	for _, b := range m.batches {
		out = append(out, b...)
	}
	return out
}

func TestLogBatchesAndNumbers(t *testing.T) {
	sink := &memSink{}
	l := NewLog(sink, "d1", 0, time.Now)
	l.System("building api")
	for i := 0; i < 120; i++ {
		l.Build("line")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	lines := sink.all()
	if len(lines) != 121 {
		t.Fatalf("stored %d lines, want 121", len(lines))
	}
	for i, ln := range lines {
		if ln.Seq != int32(i+1) || ln.DeploymentID != "d1" {
			t.Fatalf("line %d = %+v", i, ln)
		}
	}
	if lines[0].Stream != "system" || lines[1].Stream != "build" {
		t.Fatalf("streams = %s, %s", lines[0].Stream, lines[1].Stream)
	}
	if len(sink.batches) > 4 {
		t.Fatalf("%d inserts for 121 lines, want batches of 50", len(sink.batches))
	}
}

func TestLogFlushesOnTimer(t *testing.T) {
	sink := &memSink{}
	l := NewLog(sink, "d1", 0, time.Now)
	defer func() { _ = l.Close() }()
	l.Build("go: downloading chi")
	deadline := time.Now().Add(2 * time.Second)
	for len(sink.all()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("a single line was never flushed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLogLimits(t *testing.T) {
	sink := &memSink{}
	l := NewLog(sink, "d1", 0, time.Now)
	l.Build(strings.Repeat("é", maxLineBytes))
	for i := 0; i < maxLines+50; i++ {
		l.Build("x")
	}
	_ = l.Close()
	lines := sink.all()
	if n := len(lines[0].Line); n > maxLineBytes+4 || !strings.HasSuffix(lines[0].Line, " …") {
		t.Fatalf("long line kept %d bytes", n)
	}
	if len(lines) != maxLines+1 || lines[len(lines)-1].Line != "log truncated: more than 10000 lines" {
		t.Fatalf("stored %d lines, last %q", len(lines), lines[len(lines)-1].Line)
	}
}

func TestLineWriter(t *testing.T) {
	var got []string
	w := &lineWriter{emit: func(s string) { got = append(got, s) }}
	_, _ = w.Write([]byte("go: down"))
	_, _ = w.Write([]byte("loading chi\nbuild"))
	_, _ = w.Write([]byte("ing api\n"))
	_, _ = w.Write([]byte("no newline"))
	w.Close()
	want := []string{"go: downloading chi", "building api", "no newline"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %q", got)
	}
}

func TestLogContinuesNumbering(t *testing.T) {
	sink := &memSink{}
	l := NewLog(sink, "d1", 41, time.Now)
	l.System("starting api on dev-1")
	_ = l.Close()
	if got := sink.all(); len(got) != 1 || got[0].Seq != 42 {
		t.Fatalf("lines = %+v, want seq 42", got)
	}
}
