package guestinit

import (
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Captured inside a gVisor sandbox: 127.0.0.1:4000, :3000 (IPv6 wildcard) and [::]:5000, plus an open connection.
const tcp4 = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   4: 0100007F:0FA0 00000000:0000 0A 00000000:00000000 00:00000000 00000000 65534        0        5 1 0000000000000000 0 0 0 0 -1
   7: 0200120A:9C40 0101A8C0:01BB 01 00000000:00000000 00:00000000 00000000 65534        0        9 1 0000000000000000 0 0 0 0 -1
`

const tcp6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   3: 00000000000000000000000000000000:0BB8 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000 65534        0        4 1 0000000000000000 0 0 0 0 -1
   5: 00000000000000000000000001000000:1388 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000 65534        0        6 1 0000000000000000 0 0 0 0 -1
`

func TestParseListeners(t *testing.T) {
	got := append(parseListeners([]byte(tcp4)), parseListeners([]byte(tcp6))...)
	want := []string{"127.0.0.1:4000", "[::]:3000", "[::1]:5000"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v (the established connection must be skipped)", got, want)
	}
	for i, l := range got {
		if s := net.JoinHostPort(l.ip.String(), strconv.Itoa(l.port)); s != want[i] {
			t.Errorf("listener %d = %s, want %s", i, s, want[i])
		}
	}
	if len(parseListeners([]byte("header\n   1: zz:zz 0:0 0A\n"))) != 0 {
		t.Error("a malformed line was accepted")
	}
}

func TestChoose(t *testing.T) {
	any4, local := net.IPv4zero, net.IPv4(127, 0, 0, 1)
	tests := []struct {
		name   string
		ls     []listener
		port   int
		direct bool
	}{
		{"reads PORT", []listener{{any4, 8080}}, 8080, true},
		{"hard-coded", []listener{{any4, 3000}}, 3000, false},
		{"8080 on localhost", []listener{{local, 8080}, {any4, 3000}}, 8080, false},
		{"lowest of several", []listener{{any4, 6060}, {local, 4000}}, 4000, false},
		{"8080 wins over others", []listener{{any4, 3000}, {net.IPv6unspecified, 8080}}, 8080, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, direct := choose(tt.ls, 8080)
			if l.port != tt.port || direct != tt.direct {
				t.Fatalf("choose = %d direct=%v, want %d direct=%v", l.port, direct, tt.port, tt.direct)
			}
		})
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// The doorman serves the app's port on the serve port and passes requests and replies through unchanged.
func TestDoormanPassesConnections(t *testing.T) {
	app, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	go func() {
		for {
			c, err := app.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				req, _ := io.ReadAll(c) // the client half-closes after sending
				_, _ = c.Write(append([]byte("echo:"), req...))
			}()
		}
	}()
	appPort := app.Addr().(*net.TCPAddr).Port

	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serve := free.Addr().(*net.TCPAddr).Port
	_ = free.Close()

	log := &syncBuf{}
	d := &doorman{
		serve: serve, service: "api", log: log,
		table: func() []listener { return []listener{{net.IPv4(127, 0, 0, 1), appPort}} },
		bind:  func() []string { return []string{"127.0.0.1"} },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.run(ctx); close(done) }()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(serve))
	var c net.Conn
	for deadline := time.Now().Add(3 * time.Second); c == nil && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		c, _ = net.Dial("tcp", addr)
	}
	if c == nil {
		t.Fatal("the doorman never opened the serve port")
	}
	_, _ = c.Write([]byte("GET / HTTP/1.0"))
	_ = c.(*net.TCPConn).CloseWrite()
	reply, _ := io.ReadAll(c)
	_ = c.Close()
	if string(reply) != "echo:GET / HTTP/1.0" {
		t.Fatalf("reply = %q", reply)
	}
	if !strings.Contains(log.String(), "greyship: api listens on port "+strconv.Itoa(appPort)) {
		t.Fatalf("log = %q", log.String())
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the doorman didn't stop with its context")
	}
	if c, err := net.Dial("tcp", addr); err == nil {
		_ = c.Close()
		t.Fatal("the serve port is still open after stopping")
	}
}
