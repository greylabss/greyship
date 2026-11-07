package domains

import (
	"context"
	"errors"
	"strings"
	"testing"
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

type fakeDNS map[string][]string

func (f fakeDNS) LookupHost(_ context.Context, host string) ([]string, error) {
	if a, ok := f[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
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
