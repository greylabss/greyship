package guestinit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A web service may listen on any port (docs/plans/any-port.md). The router only ever reaches the instance's
// serve port, which is fixed when the sandbox is created, so when the app opens a different port the doorman
// listens on the serve port and passes every connection through to the app's, byte for byte.

const (
	watchEvery = 100 * time.Millisecond
	// settle gives an app that opens several ports (its server and pprof, say) time to open them all before
	// one is chosen.
	settle      = 300 * time.Millisecond
	dialTimeout = 5 * time.Second
)

type listener struct {
	ip   net.IP
	port int
}

func (l listener) local() bool { return l.ip.IsLoopback() }

// targets are the addresses the doorman dials for this listener: its own address, or loopback for a wildcard
// one. An IPv6 wildcard usually accepts IPv4 too, so 127.0.0.1 is tried before ::1.
func (l listener) targets() []string {
	p := strconv.Itoa(l.port)
	switch {
	case l.ip.IsUnspecified() && l.ip.To4() != nil:
		return []string{net.JoinHostPort("127.0.0.1", p)}
	case l.ip.IsUnspecified():
		return []string{net.JoinHostPort("127.0.0.1", p), net.JoinHostPort("::1", p)}
	default:
		return []string{net.JoinHostPort(l.ip.String(), p)}
	}
}

// parseListeners reads the listening sockets out of a /proc/net/tcp or /proc/net/tcp6 table.
func parseListeners(table []byte) []listener {
	var out []listener
	sc := bufio.NewScanner(bytes.NewReader(table))
	sc.Scan() // header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[3] != "0A" { // 0A is TCP_LISTEN
			continue
		}
		host, port, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		ip, err := procIP(host)
		if err != nil {
			continue
		}
		n, err := strconv.ParseUint(port, 16, 16)
		if err != nil {
			continue
		}
		out = append(out, listener{ip: ip, port: int(n)})
	}
	return out
}

// procIP decodes the kernel's hex address: 32-bit words in host (little-endian) byte order.
func procIP(s string) (net.IP, error) {
	b, err := hex.DecodeString(s)
	if err != nil || (len(b) != 4 && len(b) != 16) {
		return nil, errors.New("bad address")
	}
	for i := 0; i < len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
	}
	return net.IP(b), nil
}

func readListeners() []listener {
	var out []listener
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		if b, err := os.ReadFile(f); err == nil { //nolint:gosec // two fixed /proc paths
			out = append(out, parseListeners(b)...)
		}
	}
	return out
}

// choose picks the app's port. Listening on the serve port on every address means the router reaches the app
// directly. Otherwise the serve port (only on loopback) or the lowest port is served through the doorman.
func choose(ls []listener, serve int) (l listener, direct bool) {
	slices.SortFunc(ls, func(a, b listener) int { return a.port - b.port })
	for _, x := range ls {
		if x.port == serve && !x.local() {
			return x, true
		}
	}
	for _, x := range ls {
		if x.port == serve {
			return x, false
		}
	}
	return ls[0], false
}

type doorman struct {
	serve   int
	service string
	log     io.Writer
	table   func() []listener
	// bind lists the addresses to listen on: the sandbox's own, never loopback, so an app on 127.0.0.1 keeps
	// its port.
	bind func() []string
}

func newDoorman(serve int, service string, log io.Writer) *doorman {
	return &doorman{serve: serve, service: service, log: log, table: readListeners, bind: sandboxAddrs}
}

func sandboxAddrs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			out = append(out, n.IP.String())
		}
	}
	return out
}

// run waits for the app to open a port, then serves it until ctx ends.
func (d *doorman) run(ctx context.Context) {
	var ls []listener
	for len(ls) == 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(watchEvery):
		}
		ls = d.table()
	}
	if _, direct := choose(ls, d.serve); !direct {
		select {
		case <-ctx.Done():
			return
		case <-time.After(settle):
		}
		if ls = d.table(); len(ls) == 0 {
			return
		}
	}
	l, direct := choose(ls, d.serve)
	if direct {
		return
	}
	var lns []net.Listener
	for _, a := range d.bind() {
		ln, err := net.Listen("tcp", net.JoinHostPort(a, strconv.Itoa(d.serve)))
		if err != nil {
			fmt.Fprintf(d.log, "greyship: can't serve %s: %v\n", d.service, err)
			continue
		}
		lns = append(lns, ln)
	}
	if len(lns) == 0 {
		return
	}
	fmt.Fprintf(d.log, "greyship: %s listens on port %d; serving it on its URL%s\n", d.service, l.port, others(ls, l))

	var wg sync.WaitGroup
	for _, ln := range lns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.accept(ctx, ln, l)
		}()
	}
	<-ctx.Done()
	for _, ln := range lns {
		_ = ln.Close()
	}
	wg.Wait()
}

func others(ls []listener, chosen listener) string {
	var ports []string
	for _, x := range ls {
		if x.port != chosen.port && !slices.Contains(ports, strconv.Itoa(x.port)) {
			ports = append(ports, strconv.Itoa(x.port))
		}
	}
	if len(ports) == 0 {
		return ""
	}
	return " (it also listens on " + strings.Join(ports, ", ") + ", which isn't served)"
}

func (d *doorman) accept(ctx context.Context, ln net.Listener, to listener) {
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			pass(ctx, c, to)
		}()
	}
}

// pass copies one connection both ways until either side hangs up.
func pass(ctx context.Context, in net.Conn, to listener) {
	defer func() { _ = in.Close() }()
	var out net.Conn
	for _, t := range to.targets() {
		var err error
		if out, err = (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", t); err == nil {
			break
		}
	}
	if out == nil {
		return
	}
	defer func() { _ = out.Close() }()
	stop := context.AfterFunc(ctx, func() {
		_ = in.Close()
		_ = out.Close()
	})
	defer stop()
	done := make(chan struct{}, 2)
	go func() { copyHalf(out, in); done <- struct{}{} }()
	go func() { copyHalf(in, out); done <- struct{}{} }()
	<-done
	<-done
}

// copyHalf copies until src ends, then tells dst no more is coming, so a client that sends a request and waits
// for the reply still gets it.
func copyHalf(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	if cw, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	} else {
		_ = dst.Close()
	}
}
