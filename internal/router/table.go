// Package router is the edge router (architecture §2.6): it terminates TLS, maps a request's host to a
// service, forwards it to one of that service's instances, and drives scale to zero: idle instances pause
// after their grace window and the next request wakes them (F-31–F-33, F-80).
package router

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/store"
)

// backend is one instance. The same backend is kept across table reloads, so requests in flight, the
// request count and the idle clock all stay with it; only paused changes.
type backend struct {
	id     string
	addr   string
	nodeID string
	paused atomic.Bool

	inflight   atomic.Int64
	lastActive atomic.Int64 // unix nanoseconds of the last request ending (or of the instance appearing)
	asked      atomic.Int64 // unix nanoseconds of the last pause/resume command, so it is sent once
}

func (b *backend) touch(now time.Time) { b.lastActive.Store(now.UnixNano()) }

// route is one public service and its instances in the live deployment.
type route struct {
	serviceID   string
	concurrency int64
	maxInst     int
	grace       time.Duration
	backends    []*backend
	next        atomic.Uint64
}

// pick returns the running instance with the fewest requests in flight, and whether every running instance
// is already at the service's concurrency (time to ask for another, F-33).
func (r *route) pick() (*backend, bool) {
	var best *backend
	// Start at a different instance each time, so equally idle instances take turns.
	start := int(r.next.Add(1) % uint64(max(1, len(r.backends)))) //nolint:gosec // the modulo keeps it below len(r.backends)
	for i := range r.backends {
		b := r.backends[(start+i)%len(r.backends)]
		if b.paused.Load() {
			continue
		}
		if best == nil || b.inflight.Load() < best.inflight.Load() {
			best = b
		}
	}
	if best == nil {
		return nil, false
	}
	return best, best.inflight.Load() >= r.concurrency
}

func (r *route) firstPaused() *backend {
	for _, b := range r.backends {
		if b.paused.Load() {
			return b
		}
	}
	return nil
}

// Table maps subdomain labels to routes. It is rebuilt whole and swapped on every reload; waiters learn about
// a reload through the changed channel, which is closed and replaced each time.
type Table struct {
	Now func() time.Time

	mu       sync.RWMutex
	routes   map[string]*route
	backends map[string]*backend
	hosts    map[string]string // custom hostname → the subdomain label of the service it points at
	changed  chan struct{}
}

func (t *Table) now() time.Time {
	if t.Now == nil {
		return time.Now()
	}
	return t.Now()
}

func (t *Table) lookup(label string) (*route, <-chan struct{}, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	r, ok := t.routes[label]
	return r, t.changedLocked(), ok
}

func (t *Table) changedLocked() chan struct{} {
	if t.changed == nil {
		t.changed = make(chan struct{})
	}
	return t.changed
}

// Has reports whether label is a routed service; certificates are only ever issued for those.
func (t *Table) Has(label string) bool {
	_, _, ok := t.lookup(label)
	return ok
}

// Set replaces the table from database rows: one per service, plus one per running or paused instance.
func (t *Table) Set(rows []store.RoutingTableRow) {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	routes := map[string]*route{}
	backends := map[string]*backend{}
	for _, row := range rows {
		r, ok := routes[row.Subdomain]
		if !ok {
			r = &route{serviceID: row.ServiceID, concurrency: int64(row.Concurrency), maxInst: int(row.MaxInstances), grace: time.Duration(row.GraceSeconds) * time.Second}
			routes[row.Subdomain] = r
		}
		if row.InstanceID == nil || row.Addr == nil {
			continue
		}
		paused := row.InstanceState != nil && *row.InstanceState == "paused"
		b, ok := t.backends[*row.InstanceID]
		if !ok {
			b = &backend{id: *row.InstanceID, addr: *row.Addr}
			if row.NodeID != nil {
				b.nodeID = *row.NodeID
			}
			// A new instance gets a full grace window before it can be paused.
			b.touch(now)
		}
		if was := b.paused.Swap(paused); was != paused {
			// The pause or resume asked for has happened; a later one may be asked again.
			b.asked.Store(0)
			if !paused {
				b.touch(now)
			}
		}
		backends[b.id] = b
		r.backends = append(r.backends, b)
	}
	t.routes, t.backends = routes, backends
	if t.changed != nil {
		close(t.changed)
	}
	t.changed = make(chan struct{})
}

// idle lists running instances with no request in flight for longer than their service's grace window.
func (t *Table) idle() []*backend {
	now := t.now()
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []*backend
	for _, r := range t.routes {
		for _, b := range r.backends {
			if b.paused.Load() || b.inflight.Load() > 0 {
				continue
			}
			if now.Sub(time.Unix(0, b.lastActive.Load())) >= r.grace && now.Sub(time.Unix(0, b.asked.Load())) > 30*time.Second {
				out = append(out, b)
			}
		}
	}
	return out
}

// Watch keeps the table current until ctx ends: it reloads when the scheduler or an agent sends NOTIFY routes,
// and every resync in case a notification was missed.
func (t *Table) Watch(ctx context.Context, pool *pgxpool.Pool, resync time.Duration, logger *slog.Logger) error {
	if err := t.reload(ctx, pool); err != nil {
		return err
	}
	for ctx.Err() == nil {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return fmt.Errorf("acquire listen connection: %w", err)
		}
		if _, err := conn.Exec(ctx, "LISTEN routes"); err != nil {
			conn.Release()
			return fmt.Errorf("listen for route changes: %w", err)
		}
		for ctx.Err() == nil {
			waitCtx, cancel := context.WithTimeout(ctx, resync)
			_, err := conn.Conn().WaitForNotification(waitCtx)
			cancel()
			if ctx.Err() != nil {
				break
			}
			if rerr := t.reload(ctx, pool); rerr != nil {
				logger.Warn("reload routes", "err", rerr)
			}
			if err != nil && conn.Conn().IsClosed() {
				break
			}
		}
		conn.Release()
	}
	return nil
}

func (t *Table) reload(ctx context.Context, pool *pgxpool.Pool) error {
	q := store.New(pool)
	rows, err := q.RoutingTable(ctx)
	if err != nil {
		return fmt.Errorf("load routes: %w", err)
	}
	domains, err := q.RoutingDomains(ctx)
	if err != nil {
		return fmt.Errorf("load custom domains: %w", err)
	}
	t.Set(rows)
	t.SetDomains(domains)
	return nil
}

// SetDomains replaces the custom hostnames: each points at the subdomain label of a service.
func (t *Table) SetDomains(rows []store.RoutingDomainsRow) {
	hosts := make(map[string]string, len(rows))
	for _, r := range rows {
		hosts[strings.ToLower(r.Hostname)] = r.Subdomain
	}
	t.mu.Lock()
	t.hosts = hosts
	t.mu.Unlock()
}

// HostLabel is the service label a custom hostname points at.
func (t *Table) HostLabel(host string) (string, bool) {
	host = strings.TrimSuffix(strings.ToLower(hostOnly(host)), ".")
	t.mu.RLock()
	defer t.mu.RUnlock()
	label, ok := t.hosts[host]
	return label, ok
}

func hostOnly(host string) string {
	if h, _, ok := strings.Cut(host, ":"); ok {
		return h
	}
	return host
}

// Label returns the subdomain label of host under domain ("api-shop" for "api-shop.greyship.tech:443"), or false.
func Label(host, domain string) (string, bool) {
	host = strings.ToLower(host)
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	label, ok := strings.CutSuffix(host, "."+domain)
	if !ok || label == "" || strings.Contains(label, ".") {
		return "", false
	}
	return label, true
}
