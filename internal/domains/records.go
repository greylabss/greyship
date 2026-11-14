package domains

import (
	"context"
	"net"
	"sort"
	"strings"
	"time"

	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
)

// Record is one DNS record to create at the provider. Name is relative to the zone ("api", or "@" for the zone itself).
type Record struct {
	Type  string
	Name  string
	Value string
}

func (s *Service) customEntry(ctx context.Context, hostname, service, defaultAddr string, server map[string]bool) Entry {
	zone := s.zoneOf(ctx, hostname)
	return Entry{
		Hostname: hostname,
		Service:  service,
		Custom:   true,
		DNS:      s.check(ctx, hostname, server),
		Records:  recordsFor(hostname, zone, defaultAddr, server),
	}
}

// zoneOf finds the DNS zone a hostname lives in by asking for name servers from the full name upward: the first name
// that has its own NS records is a zone. Without an answer it assumes the last two labels, which is right for most names.
func (s *Service) zoneOf(ctx context.Context, hostname string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	parts := strings.Split(hostname, ".")
	for i := 0; i+2 <= len(parts); i++ {
		candidate := strings.Join(parts[i:], ".")
		if ns, err := s.Resolver.LookupNS(ctx, candidate); err == nil && len(ns) > 0 {
			return candidate
		}
	}
	return strings.Join(parts[max(0, len(parts)-2):], ".")
}

// recordsFor is a CNAME to the service's default address for a name inside a zone, and an A record to this server
// for the zone's own name, because a bare domain can't be a CNAME.
func recordsFor(hostname, zone, defaultAddr string, server map[string]bool) []Record {
	if hostname == zone {
		return []Record{{Type: "A", Name: "@", Value: firstIPv4(server)}}
	}
	if defaultAddr == "" {
		return nil
	}
	return []Record{{Type: "CNAME", Name: strings.TrimSuffix(hostname, "."+zone), Value: defaultAddr}}
}

func firstIPv4(addrs map[string]bool) string {
	var v4 []string
	for a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			v4 = append(v4, a)
		}
	}
	sort.Strings(v4)
	if len(v4) == 0 {
		return ""
	}
	return v4[0]
}

// Verify checks a custom hostname's DNS again. The first time it points here, the router is asked to get the
// certificate right away, so the first real visitor doesn't wait for it.
func (s *Service) Verify(ctx context.Context, c project.Caller, projectID, rawHostname string) (Entry, error) {
	p, err := s.Projects.Access(ctx, c, projectID, true)
	if err != nil {
		return Entry{}, err
	}
	hostname := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(rawHostname)), ".")
	rows, err := store.New(s.Pool).ListProjectDomains(ctx, p.ID)
	if err != nil {
		return Entry{}, err
	}
	for _, r := range rows {
		if r.Hostname != hostname {
			continue
		}
		e := s.customEntry(ctx, hostname, r.ServiceName, r.Subdomain+"."+s.AppsDomain, s.serverAddrs(ctx))
		if e.DNS == Pointing {
			s.warm(ctx, hostname)
		}
		return e, nil
	}
	return Entry{}, ErrNotFound
}

func (s *Service) warm(ctx context.Context, hostname string) {
	if s.Warm == nil {
		return
	}
	s.mu.Lock()
	if s.warmed == nil {
		s.warmed = map[string]bool{}
	}
	done := s.warmed[hostname]
	s.warmed[hostname] = true
	s.mu.Unlock()
	if done {
		return
	}
	// The request that noticed the DNS change must not wait for the certificate (it can take several seconds).
	go func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if err := s.Warm(ctx, hostname); err != nil {
			s.mu.Lock()
			delete(s.warmed, hostname)
			s.mu.Unlock()
		}
	}(context.WithoutCancel(ctx))
}
