package domains

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	s := &Service{AppsDomain: "greyshipapp.tech", PlatformDomain: "app.greyship.tech"}
	tests := []struct {
		in, want string
		err      error // ErrReserved, or an *InvalidError when invalid is set
		invalid  bool
	}{
		{in: "api.example.com", want: "api.example.com"},
		{in: "  API.Example.COM. ", want: "api.example.com"},
		{in: "example.com", want: "example.com"},
		{in: "my-app.example.co.uk", want: "my-app.example.co.uk"},
		{in: "", invalid: true},
		{in: "localhost", invalid: true},
		{in: "api.localhost", err: ErrReserved},
		{in: "example", invalid: true},
		{in: "1.2.3.4", invalid: true},
		{in: "::1", invalid: true},
		{in: "exa mple.com", invalid: true},
		{in: "-bad.example.com", invalid: true},
		{in: "bad-.example.com", invalid: true},
		{in: "a..example.com", invalid: true},
		{in: "https://api.example.com", invalid: true},
		{in: "api.example.com/path", invalid: true},
		{in: "api_x.example.com", invalid: true},
		{in: "example.123", invalid: true},
		{in: strings.Repeat("a", 64) + ".example.com", invalid: true},
		{in: strings.Repeat("a.", 130) + "com", invalid: true},
		{in: "greyshipapp.tech", err: ErrReserved},
		{in: "api-shop.greyshipapp.tech", err: ErrReserved},
		{in: "x.y.greyshipapp.tech", err: ErrReserved},
		{in: "app.greyship.tech", err: ErrReserved},
		{in: "api.app.greyship.tech", err: ErrReserved},
		{in: "notgreyshipapp.tech", want: "notgreyshipapp.tech"},
		{in: "greyship.tech", want: "greyship.tech"},
	}
	for _, tt := range tests {
		got, err := s.Normalize(tt.in)
		var invalid *InvalidError
		switch {
		case tt.invalid:
			if !errors.As(err, &invalid) {
				t.Errorf("Normalize(%q) = %q, %v; want an InvalidError", tt.in, got, err)
			}
		case tt.err != nil:
			if !errors.Is(err, tt.err) {
				t.Errorf("Normalize(%q) = %q, %v; want %v", tt.in, got, err, tt.err)
			}
		default:
			if err != nil || got != tt.want {
				t.Errorf("Normalize(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		}
	}
}

// fakeDNS holds addresses by host name, name servers under "NS <zone>" and text records under "TXT <name>".
type fakeDNS map[string][]string

func (f fakeDNS) LookupHost(_ context.Context, host string) ([]string, error) {
	if a, ok := f[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

func (f fakeDNS) LookupNS(_ context.Context, name string) ([]*net.NS, error) {
	var out []*net.NS
	for _, h := range f["NS "+name] {
		out = append(out, &net.NS{Host: h})
	}
	if len(out) == 0 {
		return nil, errors.New("no such zone")
	}
	return out, nil
}

func (f fakeDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	if t, ok := f["TXT "+name]; ok {
		return t, nil
	}
	return nil, errors.New("no such record")
}

func TestCheck(t *testing.T) {
	dns := fakeDNS{
		"domain-check.greyshipapp.tech": {"203.0.113.7"},
		"ours.example.com":              {"203.0.113.7"},
		"cname.example.com":             {"203.0.113.7", "203.0.113.8"},
		"theirs.example.com":            {"198.51.100.4"},
	}
	s := &Service{AppsDomain: "greyshipapp.tech", Resolver: dns}
	ctx := context.Background()
	server := s.serverAddrs(ctx)
	for host, want := range map[string]string{
		"ours.example.com": Pointing, "cname.example.com": Pointing, "theirs.example.com": NotPointing, "new.example.com": Unresolved,
	} {
		if got := s.check(ctx, host, server); got != want {
			t.Errorf("check(%s) = %s, want %s", host, got, want)
		}
	}
	// If this server's own address can't be worked out, a name that resolves is "unknown", not wrong.
	blind := &Service{AppsDomain: "nowhere.test", Resolver: dns}
	if got := blind.check(ctx, "ours.example.com", blind.serverAddrs(ctx)); got != Unknown {
		t.Errorf("without this server's address: %s, want unknown", got)
	}
}

func TestZoneAndRecords(t *testing.T) {
	dns := fakeDNS{
		"domain-check.greyshipapp.tech": {"203.0.113.7", "2001:db8::1"},
		"NS example.com":                {"ns1.example.net"},
		"NS example.co.uk":              {"ns1.example.net"},
	}
	s := &Service{AppsDomain: "greyshipapp.tech", Resolver: dns}
	ctx := context.Background()
	server := s.serverAddrs(ctx)
	tests := []struct {
		host, zone string
		want       Record
	}{
		{"api.example.com", "example.com", Record{"CNAME", "api", "api-shop.greyshipapp.tech"}},
		{"a.b.example.com", "example.com", Record{"CNAME", "a.b", "api-shop.greyshipapp.tech"}},
		{"example.com", "example.com", Record{"A", "@", "203.0.113.7"}},
		{"api.example.co.uk", "example.co.uk", Record{"CNAME", "api", "api-shop.greyshipapp.tech"}},
		{"example.co.uk", "example.co.uk", Record{"A", "@", "203.0.113.7"}},
		{"api.unregistered.test", "unregistered.test", Record{"CNAME", "api", "api-shop.greyshipapp.tech"}},
	}
	for _, tt := range tests {
		zone := s.zoneOf(ctx, tt.host)
		if zone != tt.zone {
			t.Errorf("zoneOf(%s) = %s, want %s", tt.host, zone, tt.zone)
			continue
		}
		got := recordsFor(tt.host, zone, "api-shop.greyshipapp.tech", server)
		if len(got) != 1 || got[0] != tt.want {
			t.Errorf("recordsFor(%s) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestWarmOncePerHostname(t *testing.T) {
	calls := make(chan string, 4)
	s := &Service{Warm: func(_ context.Context, host string) error { calls <- host; return nil }}
	for range 3 {
		s.warm(context.Background(), "shop.example.com")
	}
	select {
	case got := <-calls:
		if got != "shop.example.com" {
			t.Errorf("warmed %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the certificate was never requested")
	}
	select {
	case <-calls:
		t.Error("asked for the certificate twice")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestConnectLink(t *testing.T) {
	var syncURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/client/v4/dns/domainconnect/v2/example.com/settings", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"providerName":"Cloudflare","urlSyncUX":"` + syncURL + `/sync","urlAPI":"` + syncURL + `/api"}`))
	})
	mux.HandleFunc("/api/v2/domainTemplates/providers/greyship.tech/services/custom-domain-cname", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/api/v2/domainTemplates/providers/greyship.tech/services/custom-domain-a", func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	syncURL = srv.URL
	host := strings.TrimPrefix(srv.URL, "https://")
	dns := fakeDNS{
		"domain-check.greyshipapp.tech":  {"203.0.113.7"},
		"NS example.com":                 {"ns1.example.net"},
		"TXT _domainconnect.example.com": {host + "/client/v4/dns/domainconnect"},
		"NS other.test":                  {"ns1.example.net"},
		"TXT _domainconnect.other.test":  {"evil.test/client"},
	}
	s := &Service{AppsDomain: "greyshipapp.tech", Resolver: dns, HTTPClient: srv.Client(), ConnectHosts: []string{host}}
	ctx := context.Background()

	link, err := s.connectLink(ctx, "api.example.com", "api-shop.greyshipapp.tech")
	if err != nil {
		t.Fatalf("connectLink: %v", err)
	}
	u, _ := url.Parse(link.URL)
	if link.Provider != "cloudflare" || !strings.HasSuffix(u.Path, "/sync/v2/domainTemplates/providers/greyship.tech/services/custom-domain-cname/apply") {
		t.Errorf("link = %+v", link)
	}
	if q := u.Query(); q.Get("domain") != "example.com" || q.Get("host") != "api" || q.Get("target") != "api-shop.greyshipapp.tech" {
		t.Errorf("query = %v", q)
	}

	// The apex needs the A template, which this provider doesn't have yet.
	if _, err := s.connectLink(ctx, "example.com", "api-shop.greyshipapp.tech"); !errors.Is(err, ErrNoAutoSetup) {
		t.Errorf("apex without a template: %v, want ErrNoAutoSetup", err)
	}
	// A settings address that isn't on the allow-list is never requested.
	if _, err := s.connectLink(ctx, "api.other.test", "api-shop.greyshipapp.tech"); !errors.Is(err, ErrNoAutoSetup) {
		t.Errorf("foreign settings host: %v, want ErrNoAutoSetup", err)
	}
	// No _domainconnect record: not on a supported provider.
	if _, err := s.connectLink(ctx, "api.nothing.test", "api-shop.greyshipapp.tech"); !errors.Is(err, ErrNoAutoSetup) {
		t.Errorf("no TXT: %v, want ErrNoAutoSetup", err)
	}
}

func TestConnectHostDefaults(t *testing.T) {
	s := &Service{}
	for host, want := range map[string]bool{
		"api.cloudflare.com": true, "dash.cloudflare.com": true, "cloudflare.com": true,
		"evilcloudflare.com": false, "cloudflare.com.evil.test": false, "127.0.0.1": false, "localhost:8080": false,
	} {
		if got := s.connectHostOK(host); got != want {
			t.Errorf("connectHostOK(%s) = %v, want %v", host, got, want)
		}
	}
}

func TestConnectRefusesRedirectsAndPlainHTTP(t *testing.T) {
	hit := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			hit = true
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://")
	s := &Service{AppsDomain: "greyshipapp.tech", HTTPClient: srv.Client(), ConnectHosts: []string{host},
		Resolver: fakeDNS{"NS example.com": {"ns"}, "TXT _domainconnect.example.com": {host}}}
	if _, err := s.connectLink(context.Background(), "api.example.com", "x.greyshipapp.tech"); !errors.Is(err, ErrNoAutoSetup) || hit {
		t.Errorf("a redirecting settings address: err=%v, followed=%v", err, hit)
	}
}
