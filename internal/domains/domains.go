// Package domains lets people point their own hostnames at a web service (spec F-51, decisions.md 057). A hostname
// belongs to one service; the routers serve it and get its certificate on demand once it is in the table.
package domains

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
)

var (
	ErrReserved        = errors.New("that name belongs to Greyship or its apps domain")
	ErrTaken           = errors.New("that hostname is already used by another service")
	ErrNoWebService    = errors.New("this project has no public web service yet; deploy it first")
	ErrServiceRequired = errors.New("this project has several web services: say which one")
	ErrUnknownService  = errors.New("the project has no such web service")
	ErrNotFound        = errors.New("no such domain on this project")
)

// InvalidError says why a hostname can't be used; Reason is written for people.
type InvalidError struct{ Reason string }

func (e *InvalidError) Error() string { return "invalid hostname: " + e.Reason }

// Resolver looks hostnames up in DNS; tests replace it.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// DNS states of a custom hostname.
const (
	Pointing    = "pointing"     // it resolves to this server
	NotPointing = "not_pointing" // it resolves, but somewhere else
	Unresolved  = "unresolved"   // it doesn't resolve yet
	Unknown     = "unknown"      // this server's address couldn't be worked out
)

type Service struct {
	Pool     *pgxpool.Pool
	Projects *project.Service
	// AppsDomain and PlatformDomain are Greyship's own: custom hostnames can't be under them.
	AppsDomain, PlatformDomain string
	Resolver                   Resolver
}

// Entry is one hostname of a service. Custom is false for the address Greyship gives every web service.
type Entry struct {
	Hostname string
	Service  string
	Custom   bool
	DNS      string
}

var label = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Normalize checks a hostname someone typed and returns it lowercase, without a trailing dot.
func (s *Service) Normalize(raw string) (string, error) {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if h == "" {
		return "", &InvalidError{"enter a hostname such as api.example.com"}
	}
	if len(h) > 253 {
		return "", &InvalidError{"it is longer than 253 characters"}
	}
	if net.ParseIP(h) != nil {
		return "", &InvalidError{"use a name, not an IP address"}
	}
	parts := strings.Split(h, ".")
	if len(parts) < 2 {
		return "", &InvalidError{"use a full name such as api.example.com"}
	}
	for _, p := range parts {
		if !label.MatchString(p) {
			return "", &InvalidError{"use letters, digits and dashes, with dots between the parts"}
		}
	}
	if tld := parts[len(parts)-1]; strings.Trim(tld, "0123456789") == "" {
		return "", &InvalidError{"the last part can't be only digits"}
	}
	for _, d := range []string{s.AppsDomain, s.PlatformDomain} {
		d = strings.ToLower(d)
		if d != "" && (h == d || strings.HasSuffix(h, "."+d)) {
			return "", ErrReserved
		}
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return "", ErrReserved
	}
	return h, nil
}

// List is the project's web services with their default address and custom hostnames, each with its DNS state.
func (s *Service) List(ctx context.Context, c project.Caller, projectID string) ([]Entry, error) {
	p, err := s.Projects.Access(ctx, c, projectID, false)
	if err != nil {
		return nil, err
	}
	q := store.New(s.Pool)
	services, err := q.ProjectWebServices(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("list services of %s: %w", p.ID, err)
	}
	var out []Entry
	for _, svc := range services {
		if svc.Subdomain != nil {
			out = append(out, Entry{Hostname: *svc.Subdomain + "." + s.AppsDomain, Service: svc.Name})
		}
	}
	rows, err := q.ListProjectDomains(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("list domains of %s: %w", p.ID, err)
	}
	server := s.serverAddrs(ctx)
	for _, r := range rows {
		out = append(out, Entry{Hostname: r.Hostname, Service: r.ServiceName, Custom: true, DNS: s.check(ctx, r.Hostname, server)})
	}
	return out, nil
}

// Add points a hostname at one of the project's web services. A project with a single web service needs no name.
func (s *Service) Add(ctx context.Context, c project.Caller, projectID, rawHostname, serviceName string) (Entry, error) {
	p, err := s.Projects.Access(ctx, c, projectID, true)
	if err != nil {
		return Entry{}, err
	}
	hostname, err := s.Normalize(rawHostname)
	if err != nil {
		return Entry{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Entry{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	services, err := q.ProjectWebServices(ctx, p.ID)
	if err != nil {
		return Entry{}, fmt.Errorf("list services of %s: %w", p.ID, err)
	}
	var target *store.Service
	switch {
	case len(services) == 0:
		return Entry{}, ErrNoWebService
	case serviceName == "" && len(services) == 1:
		target = &services[0]
	case serviceName == "":
		return Entry{}, ErrServiceRequired
	default:
		for i := range services {
			if services[i].Name == serviceName {
				target = &services[i]
			}
		}
		if target == nil {
			return Entry{}, ErrUnknownService
		}
	}
	err = q.AddDomain(ctx, store.AddDomainParams{Hostname: hostname, ServiceID: target.ID, CreatedBy: &c.UserID})
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Entry{}, ErrTaken
	}
	if err != nil {
		return Entry{}, fmt.Errorf("add domain %s: %w", hostname, err)
	}
	if err := q.NotifyRoutes(ctx); err != nil {
		return Entry{}, fmt.Errorf("notify routers: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Entry{}, fmt.Errorf("commit domain %s: %w", hostname, err)
	}
	return Entry{Hostname: hostname, Service: target.Name, Custom: true, DNS: s.check(ctx, hostname, s.serverAddrs(ctx))}, nil
}

// Remove stops serving a custom hostname.
func (s *Service) Remove(ctx context.Context, c project.Caller, projectID, rawHostname string) error {
	p, err := s.Projects.Access(ctx, c, projectID, true)
	if err != nil {
		return err
	}
	hostname := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(rawHostname)), ".")
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	n, err := q.DeleteProjectDomain(ctx, store.DeleteProjectDomainParams{ProjectID: p.ID, Hostname: hostname})
	if err != nil {
		return fmt.Errorf("remove domain %s: %w", hostname, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	if err := q.NotifyRoutes(ctx); err != nil {
		return fmt.Errorf("notify routers: %w", err)
	}
	return tx.Commit(ctx)
}

// serverAddrs is where this server's apps resolve to: a name under the apps domain that no app uses. Every
// installation has a wildcard record for it (or sslip.io, which makes one from the IP in the name).
func (s *Service) serverAddrs(ctx context.Context) map[string]bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := s.Resolver.LookupHost(ctx, "domain-check."+s.AppsDomain)
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, a := range addrs {
		set[a] = true
	}
	return set
}

func (s *Service) check(ctx context.Context, hostname string, server map[string]bool) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := s.Resolver.LookupHost(ctx, hostname)
	if err != nil || len(addrs) == 0 {
		return Unresolved
	}
	if len(server) == 0 {
		return Unknown
	}
	for _, a := range addrs {
		if server[a] {
			return Pointing
		}
	}
	return NotPointing
}
