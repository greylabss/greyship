package router

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxBody = 10 << 20 // F-38

// Commands asks the rest of the platform to change instances: the node agent pauses and resumes, the scheduler
// starts new ones.
type Commands interface {
	Pause(ctx context.Context, instanceID, nodeID string) error
	Resume(ctx context.Context, instanceID, nodeID string) error
	ScaleUp(ctx context.Context, serviceID, reason string) error
}

const (
	holdFor     = 30 * time.Second // F-32
	askAgain    = 5 * time.Second  // a pause, resume or scale-up asked for and not yet visible is not asked again sooner
	sweepPeriod = time.Second
)

type Router struct {
	Table  *Table
	Domain string // apps domain, e.g. greyshipapp.tech
	// PlatformDomain holds Greyship's own hostnames (api., s3.). It is a different registrable domain from the
	// apps domain in production, so no app is ever the same site as the API or dashboard (decisions.md 037).
	PlatformDomain string
	Transport      http.RoundTripper
	Commands       Commands
	Logger         *slog.Logger
	// Platform maps platform labels to fixed addresses on the node, e.g. "api" → 127.0.0.1:8080 and
	// "s3" → 127.0.0.1:3900 on a single server. App labels always contain a hyphen, so the two never clash.
	// Platform routes skip the app limits: source uploads are larger than an app request may be.
	Platform map[string]string

	mu     sync.Mutex
	scaled map[string]time.Time // service id → when a scale-up was last asked for
}

// NewTransport is the router's connection pool to instances. Instances are on the node, so connecting is
// quick; the response header timeout is the 30s response limit (F-38), and it doesn't cut off streams.
func NewTransport() *http.Transport {
	return &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}

// platformAddr is the node address behind host when host is one of Greyship's own hostnames.
func (rt *Router) platformAddr(host string) (string, bool) {
	domain := rt.PlatformDomain
	if domain == "" {
		domain = rt.Domain
	}
	// "@" is the platform domain itself, where a self-hosted instance serves its dashboard.
	if addr, ok := rt.Platform["@"]; ok && sameHost(host, domain) {
		return addr, true
	}
	label, ok := Label(host, domain)
	if !ok {
		return "", false
	}
	addr, ok := rt.Platform[label]
	return addr, ok
}

func sameHost(host, domain string) bool {
	host = strings.ToLower(host)
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	return strings.TrimSuffix(host, ".") == strings.ToLower(domain)
}

// LabelOf is the service label host reaches: a custom hostname's service, or the label under the apps domain.
func (rt *Router) LabelOf(host string) (string, bool) {
	if label, ok := rt.Table.HostLabel(host); ok {
		return label, true
	}
	return Label(host, rt.Domain)
}

// Serves reports whether host is a Greyship hostname, a custom hostname or a routed app; only those get certificates.
func (rt *Router) Serves(host string) bool {
	if _, ok := rt.platformAddr(host); ok {
		return true
	}
	label, ok := rt.LabelOf(host)
	return ok && rt.Table.Has(label)
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if addr, ok := rt.platformAddr(r.Host); ok {
		rt.platform(w, r, addr)
		return
	}
	label, ok := rt.LabelOf(r.Host)
	if !ok {
		http.Error(w, "No app here.", http.StatusNotFound)
		return
	}
	route, _, ok := rt.Table.lookup(label)
	if !ok {
		http.Error(w, "No app here.", http.StatusNotFound)
		return
	}
	if r.ContentLength > maxBody {
		http.Error(w, "Request body too large.", http.StatusRequestEntityTooLarge)
		return
	}
	b, busy := route.pick()
	if b == nil {
		if b, route = rt.hold(r.Context(), label); b == nil {
			w.Header().Set("Retry-After", "5")
			http.Error(w, "This app is starting. Try again in a few seconds.", http.StatusServiceUnavailable)
			return
		}
	} else if busy && len(route.backends) < route.maxInst {
		rt.scaleUp(r.Context(), route, "busy")
	}

	b.inflight.Add(1)
	defer func() {
		b.touch(rt.Table.now())
		b.inflight.Add(-1)
	}()
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	proxy := &httputil.ReverseProxy{
		// Rewrite (rather than Director) drops any X-Forwarded-* the client sent before setting our own.
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "http", Host: b.addr})
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Proto", "https")
		},
		Transport: rt.Transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				http.Error(w, "Request body too large.", http.StatusRequestEntityTooLarge)
				return
			}
			if r.Context().Err() == nil {
				rt.Logger.Warn("proxy to instance", "service", route.serviceID, "err", err)
			}
			// The client learns nothing about our internals, only that the app didn't answer.
			http.Error(w, "The app didn't answer.", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// platform forwards to one of Greyship's own services on the node.
func (rt *Router) platform(w http.ResponseWriter, r *http.Request, addr string) {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "http", Host: addr})
			// The storage endpoint checks presigned signatures against the Host the client signed for.
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Proto", "https")
		},
		Transport: rt.Transport,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			rt.Logger.Warn("proxy to platform service", "addr", addr, "err", err)
			http.Error(w, "Greyship is having trouble. Try again shortly.", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// hold wakes a sleeping service and waits, at most 30 seconds, for an instance to run (F-32). Every table reload
// is a chance: the resume or the new instance shows up there.
func (rt *Router) hold(ctx context.Context, label string) (*backend, *route) {
	deadline := time.NewTimer(holdFor)
	defer deadline.Stop()
	for {
		route, changed, ok := rt.Table.lookup(label)
		if !ok {
			return nil, nil
		}
		if b, _ := route.pick(); b != nil {
			return b, route
		}
		rt.wake(ctx, route)
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, nil
		case <-deadline.C:
			return nil, nil
		}
	}
}

// wake resumes a paused instance if the service has one (milliseconds, F-80), or asks for a new one.
func (rt *Router) wake(ctx context.Context, route *route) {
	if b := route.firstPaused(); b != nil {
		if rt.ask(b) {
			if err := rt.Commands.Resume(ctx, b.id, b.nodeID); err != nil {
				rt.Logger.Warn("resume instance", "instance", b.id, "err", err)
			}
		}
		return
	}
	rt.scaleUp(ctx, route, "wake")
}

// Warm starts waking label's service during the TLS handshake, before its first request arrives (F-81).
func (rt *Router) Warm(label string) {
	route, _, ok := rt.Table.lookup(label)
	if !ok {
		return
	}
	if b, _ := route.pick(); b == nil {
		rt.wake(context.Background(), route)
	}
}

func (rt *Router) scaleUp(ctx context.Context, route *route, reason string) {
	now := rt.Table.now()
	rt.mu.Lock()
	if rt.scaled == nil {
		rt.scaled = map[string]time.Time{}
	}
	last := rt.scaled[route.serviceID]
	fresh := now.Sub(last) > askAgain
	if fresh {
		rt.scaled[route.serviceID] = now
	}
	rt.mu.Unlock()
	if !fresh {
		return
	}
	if err := rt.Commands.ScaleUp(ctx, route.serviceID, reason); err != nil {
		rt.Logger.Warn("scale up", "service", route.serviceID, "err", err)
	}
}

// ask reports whether a pause or resume should be sent for b now, recording that it was.
func (rt *Router) ask(b *backend) bool {
	now := rt.Table.now().UnixNano()
	last := b.asked.Load()
	return now-last > int64(askAgain) && b.asked.CompareAndSwap(last, now)
}

// PauseIdle pauses instances whose grace window has passed with no requests (F-31), until ctx ends.
func (rt *Router) PauseIdle(ctx context.Context) {
	t := time.NewTicker(sweepPeriod)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, b := range rt.Table.idle() {
				if !rt.ask(b) {
					continue
				}
				if err := rt.Commands.Pause(ctx, b.id, b.nodeID); err != nil && ctx.Err() == nil {
					rt.Logger.Warn("pause instance", "instance", b.id, "err", err)
				}
			}
		}
	}
}

// Redirect sends plain HTTP to HTTPS (F-53), keeping the host and path. Only hosts under domains, and custom hostnames
// in the table, are redirected, so the router can't be used to bounce visitors elsewhere. port is appended when not 443.
func Redirect(port string, table *Table, domains ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		known := false
		if table != nil {
			_, known = table.HostLabel(r.Host)
		}
		for _, d := range domains {
			if _, ok := Label(r.Host, d); ok {
				known = true
			}
		}
		if !known {
			http.Error(w, "No app here.", http.StatusNotFound)
			return
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if port != "" && port != "443" {
			host = net.JoinHostPort(host, port)
		}
		u := url.URL{Scheme: "https", Host: host, Path: r.URL.Path, RawQuery: r.URL.RawQuery}
		http.Redirect(w, r, u.String(), http.StatusPermanentRedirect)
	})
}
