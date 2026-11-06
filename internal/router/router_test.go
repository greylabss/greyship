package router

import (
	"bufio"
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"greyship.tech/internal/store"
)

func upstream(t *testing.T, name string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			conn, rw, _ := w.(http.Hijacker).Hijack()
			defer func() { _ = conn.Close() }()
			_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
			_ = rw.Flush()
			line, _ := rw.ReadString('\n')
			_, _ = rw.WriteString("echo " + line)
			_ = rw.Flush()
			return
		}
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s host=%s xff=%s proto=%s len=%d", name, r.Host, r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Forwarded-Proto"), len(body))
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func ptr(s string) *string { return &s }

// row is a routing table row for an instance in state ("running" or "paused") at addr.
func row(service, label, instance, addr, state string) store.RoutingTableRow {
	r := store.RoutingTableRow{ServiceID: service, Subdomain: label, Concurrency: 100, MaxInstances: 2, GraceSeconds: 30}
	if instance != "" {
		r.InstanceID, r.Addr, r.NodeID, r.InstanceState = ptr(instance), ptr(addr), ptr("n1"), ptr(state)
	}
	return r
}

type fakeCommands struct {
	mu      sync.Mutex
	paused  []string
	resumed []string
	scaled  []string
}

func (f *fakeCommands) Pause(_ context.Context, id, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = append(f.paused, id)
	return nil
}

func (f *fakeCommands) Resume(_ context.Context, id, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumed = append(f.resumed, id)
	return nil
}

func (f *fakeCommands) ScaleUp(_ context.Context, service, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scaled = append(f.scaled, service+":"+reason)
	return nil
}

func (f *fakeCommands) got() (paused, resumed, scaled []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paused...), append([]string(nil), f.resumed...), append([]string(nil), f.scaled...)
}

func newRouter(rows []store.RoutingTableRow) *Router {
	t := &Table{}
	t.Set(rows)
	return &Router{Table: t, Domain: "greyship.tech", Transport: NewTransport(), Commands: &fakeCommands{}, Logger: slog.New(slog.DiscardHandler)}
}

func do(rt http.Handler, host string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "https://"+host+"/", nil)
	req.Host = host
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	rt.ServeHTTP(rec, req)
	return rec
}

func TestRouting(t *testing.T) {
	a, b := upstream(t, "a"), upstream(t, "b")
	rt := newRouter([]store.RoutingTableRow{
		row("s1", "api-shop", "i1", a, "running"),
		row("s1", "api-shop", "i2", b, "running"),
	})

	seen := map[string]int{}
	for i := 0; i < 4; i++ {
		rec := do(rt, "API-shop.greyship.tech:443", nil)
		if rec.Code != 200 {
			t.Fatalf("status %d %s", rec.Code, rec.Body.String())
		}
		seen[rec.Body.String()[:1]]++
	}
	if seen["a"] != 2 || seen["b"] != 2 {
		t.Fatalf("requests per instance = %v, want an even spread", seen)
	}

	tests := []struct {
		host string
		code int
		body string
	}{
		{"nope-shop.greyship.tech", 404, "No app here"},
		{"api-shop.evil.com", 404, "No app here"},
		{"a.b.greyship.tech", 404, "No app here"},
		{"greyship.tech", 404, "No app here"},
	}
	for _, tt := range tests {
		if rec := do(rt, tt.host, nil); rec.Code != tt.code || !strings.Contains(rec.Body.String(), tt.body) {
			t.Errorf("%s = %d %q, want %d", tt.host, rec.Code, rec.Body.String(), tt.code)
		}
	}
}

func TestForwardedHeadersCantBeSpoofed(t *testing.T) {
	rt := newRouter([]store.RoutingTableRow{row("s1", "api-shop", "i1", upstream(t, "a"), "running")})
	rec := do(rt, "api-shop.greyship.tech", func(r *http.Request) {
		r.RemoteAddr = "203.0.113.9:5555"
		r.Header.Set("X-Forwarded-For", "10.0.0.1")
		r.Header.Set("X-Forwarded-Proto", "http")
	})
	if got := rec.Body.String(); got != "a host=api-shop.greyship.tech xff=203.0.113.9 proto=https len=0" {
		t.Fatalf("app saw %q", got)
	}
}

func TestBodyLimitAndBadGateway(t *testing.T) {
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	deadAddr := dead.Addr().String()
	_ = dead.Close()
	rt := newRouter([]store.RoutingTableRow{
		row("s1", "api-shop", "i1", upstream(t, "a"), "running"),
		row("s2", "down-shop", "i2", deadAddr, "running"),
	})
	rec := do(rt, "api-shop.greyship.tech", func(r *http.Request) {
		r.Method = "POST"
		r.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", maxBody+1)))
		r.ContentLength = -1
	})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d", rec.Code)
	}
	rec = do(rt, "down-shop.greyship.tech", nil)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "127.0.0.1") || strings.Contains(rec.Body.String(), "refused") {
		t.Fatalf("dead instance = %d %q; want 502 without internals", rec.Code, rec.Body.String())
	}
}

func TestWebSocketPassesThrough(t *testing.T) {
	rt := newRouter([]store.RoutingTableRow{row("s1", "api-shop", "i1", upstream(t, "a"), "running")})
	srv := httptest.NewServer(rt)
	defer srv.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	fmt.Fprint(conn, "GET /ws HTTP/1.1\r\nHost: api-shop.greyship.tech\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade = %v %v", resp, err)
	}
	defer func() { _ = resp.Body.Close() }()
	fmt.Fprint(conn, "hello\n")
	if line, _ := br.ReadString('\n'); line != "echo hello\n" {
		t.Fatalf("websocket echo = %q", line)
	}
}

func TestRedirect(t *testing.T) {
	h := Redirect("8443", "localhost")
	rec := do(h, "api-shop.localhost:8081", func(r *http.Request) { r.URL.Path = "/a"; r.URL.RawQuery = "b=1" })
	if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "https://api-shop.localhost:8443/a?b=1" {
		t.Fatalf("redirect = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := do(Redirect("", "greyshipapp.tech", "greyship.tech"), "evil.com", nil); rec.Code != 404 {
		t.Fatalf("foreign host redirected: %d", rec.Code)
	}
}

func TestPlatformHostsOnlyUnderThePlatformDomain(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "api") }))
	defer api.Close()
	rt := newRouter(nil)
	rt.Domain, rt.PlatformDomain = "greyshipapp.tech", "greyship.tech"
	rt.Platform = map[string]string{"api": strings.TrimPrefix(api.URL, "http://")}

	if rec := do(rt, "api.greyship.tech", nil); rec.Code != 200 || rec.Body.String() != "api" {
		t.Fatalf("api.greyship.tech = %d %q", rec.Code, rec.Body.String())
	}
	// An app domain lookalike must never reach the API: apps and the platform are different sites.
	if rec := do(rt, "api.greyshipapp.tech", nil); rec.Code != 404 {
		t.Fatalf("api.greyshipapp.tech = %d, want 404", rec.Code)
	}
	if !rt.Serves("api.greyship.tech") || rt.Serves("api.greyshipapp.tech") || rt.Serves("evil.greyship.tech") {
		t.Fatal("certificates must be issued only for served hostnames")
	}
}

// A self-hosted instance serves its dashboard on the platform domain itself ("@"), and nothing else gets it.
func TestPlatformApex(t *testing.T) {
	dash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "dashboard") }))
	defer dash.Close()
	rt := newRouter(nil)
	rt.Domain, rt.PlatformDomain = "apps.example.net", "greyship.example.com"
	rt.Platform = map[string]string{"@": strings.TrimPrefix(dash.URL, "http://")}

	for _, host := range []string{"greyship.example.com", "Greyship.Example.com:443", "greyship.example.com."} {
		if rec := do(rt, host, nil); rec.Code != 200 || rec.Body.String() != "dashboard" {
			t.Fatalf("%s = %d %q, want the dashboard", host, rec.Code, rec.Body.String())
		}
	}
	for _, host := range []string{"apps.example.net", "evil.greyship.example.com", "greyship.example.com.evil.net", "example.com"} {
		if rec := do(rt, host, nil); rec.Code != 404 {
			t.Fatalf("%s = %d, want 404", host, rec.Code)
		}
	}
	if !rt.Serves("greyship.example.com") || rt.Serves("example.com") {
		t.Fatal("a certificate is for the platform domain only")
	}
}

func TestLabel(t *testing.T) {
	tests := []struct {
		host, label string
		ok          bool
	}{
		{"api-shop.greyship.tech", "api-shop", true},
		{"API-Shop.greyship.tech.", "api-shop", true},
		{"api-shop.greyship.tech:8443", "api-shop", true},
		{"greyship.tech", "", false},
		{"x.api-shop.greyship.tech", "", false},
		{"api-shop.greyship.tech.evil.com", "", false},
	}
	for _, tt := range tests {
		if got, ok := Label(tt.host, "greyship.tech"); got != tt.label || ok != tt.ok {
			t.Errorf("Label(%q) = %q %v", tt.host, got, ok)
		}
	}
}

func TestDevCA(t *testing.T) {
	dir := t.TempDir()
	ca, err := LoadDevCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadDevCA(dir)
	if err != nil || !again.cert.Equal(ca.cert) {
		t.Fatal("the CA must be created once and reused")
	}
	leaf, err := ca.Certificate("api-shop.localhost")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)
	if _, err := leaf.Leaf.Verify(x509.VerifyOptions{DNSName: "api-shop.localhost", Roots: roots}); err != nil {
		t.Fatalf("leaf doesn't verify against the CA: %v", err)
	}
	// The name constraint means even a trusted dev CA can't vouch for real sites.
	evil, err := ca.Certificate("bank.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evil.Leaf.Verify(x509.VerifyOptions{DNSName: "bank.com", Roots: roots}); err == nil {
		t.Fatal("a certificate for bank.com verified against the dev CA")
	}
}

// A paused service: the request is held, the instance resumed, and the request served once the table says so.
func TestWakesPausedInstance(t *testing.T) {
	addr := upstream(t, "a")
	rt := newRouter([]store.RoutingTableRow{row("s1", "api-shop", "i1", addr, "paused")})
	cmds := rt.Commands.(*fakeCommands)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- do(rt, "api-shop.greyship.tech", nil) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, resumed, _ := cmds.got(); len(resumed) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the router never asked for a resume")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("the request was answered before the instance was running")
	case <-time.After(50 * time.Millisecond):
	}
	rt.Table.Set([]store.RoutingTableRow{row("s1", "api-shop", "i1", addr, "running")})
	select {
	case rec := <-done:
		if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "a ") {
			t.Fatalf("held request = %d %q", rec.Code, rec.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the held request never went through")
	}
	if _, resumed, _ := cmds.got(); len(resumed) != 1 {
		t.Fatalf("resume asked %d times, want once", len(resumed))
	}
}

// A service with nothing running asks the scheduler for an instance, then serves when one appears.
func TestWakesStoppedService(t *testing.T) {
	addr := upstream(t, "a")
	rt := newRouter([]store.RoutingTableRow{row("s1", "api-shop", "", "", "")})
	cmds := rt.Commands.(*fakeCommands)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- do(rt, "api-shop.greyship.tech", nil) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, _, scaled := cmds.got(); len(scaled) == 1 && scaled[0] == "s1:wake" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the router never asked for an instance")
		}
		time.Sleep(5 * time.Millisecond)
	}
	rt.Table.Set([]store.RoutingTableRow{row("s1", "api-shop", "i9", addr, "running")})
	if rec := <-done; rec.Code != 200 {
		t.Fatalf("held request = %d", rec.Code)
	}
}

func TestHoldGivesUpAfterTheClientLeaves(t *testing.T) {
	rt := newRouter([]store.RoutingTableRow{row("s1", "api-shop", "", "", "")})
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "https://api-shop.greyship.tech/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	cancel()
	rt.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// Instances pause once their grace window passes with nothing in flight, and only once.
func TestIdleInstancesPause(t *testing.T) {
	now := time.Unix(1000, 0)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }

	tbl := &Table{Now: clock}
	tbl.Set([]store.RoutingTableRow{row("s1", "api-shop", "i1", upstream(t, "a"), "running")})
	cmds := &fakeCommands{}
	rt := &Router{Table: tbl, Domain: "greyship.tech", Transport: NewTransport(), Commands: cmds, Logger: slog.New(slog.DiscardHandler)}

	if rec := do(rt, "api-shop.greyship.tech", nil); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	advance(29 * time.Second)
	if idle := tbl.idle(); len(idle) != 0 {
		t.Fatal("paused inside the grace window")
	}
	advance(2 * time.Second)
	idle := tbl.idle()
	if len(idle) != 1 || !rt.ask(idle[0]) || rt.ask(idle[0]) {
		t.Fatal("an idle instance should be paused exactly once")
	}
	// A reload must not reset the idle clock or forget the request count.
	tbl.Set([]store.RoutingTableRow{row("s1", "api-shop", "i1", "x", "running")})
	route, _, _ := tbl.lookup("api-shop")
	if b := route.backends[0]; !time.Unix(0, b.lastActive.Load()).Equal(time.Unix(1000, 0)) {
		t.Fatalf("reload moved the idle clock to %v", time.Unix(0, b.lastActive.Load()))
	}
}

// Every instance at its concurrency limit: the router asks for one more, up to max_instances.
func TestBusyServiceScalesUp(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { time.Sleep(200 * time.Millisecond) }))
	defer slow.Close()
	r := row("s1", "api-shop", "i1", strings.TrimPrefix(slow.URL, "http://"), "running")
	r.Concurrency = 1
	rt := newRouter([]store.RoutingTableRow{r})
	cmds := rt.Commands.(*fakeCommands)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); do(rt, "api-shop.greyship.tech", nil) }()
		time.Sleep(20 * time.Millisecond)
	}
	wg.Wait()
	if _, _, scaled := cmds.got(); len(scaled) != 1 || scaled[0] != "s1:busy" {
		t.Fatalf("scale-ups = %v, want one busy request", scaled)
	}
}

func TestPlatformRoutes(t *testing.T) {
	rt := newRouter(nil)
	rt.Platform = map[string]string{"api": upstream(t, "platform")}
	rec := do(rt, "api.greyship.tech", func(r *http.Request) {
		r.Method = "PUT"
		r.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", maxBody+1)))
		r.ContentLength = int64(maxBody + 1)
	})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "platform host=api.greyship.tech") || !strings.Contains(rec.Body.String(), fmt.Sprintf("len=%d", maxBody+1)) {
		t.Fatalf("platform route = %d %q; want the full upload passed with its host", rec.Code, rec.Body.String())
	}
	if rec := do(rt, "s3.greyship.tech", nil); rec.Code != 404 {
		t.Fatalf("an unconfigured platform label = %d, want 404", rec.Code)
	}
}
