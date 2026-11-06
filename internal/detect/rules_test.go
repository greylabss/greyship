package detect

import (
	"go/parser"
	"go/token"
	"maps"
	"os"
	"testing"
)

func TestMatchesTarget(t *testing.T) {
	tests := map[string]bool{
		"main.go": true, "server_linux.go": true, "server_amd64.go": true, "server_linux_amd64.go": true,
		"server_windows.go": false, "server_arm64.go": false, "server_darwin_amd64.go": false, "server_linux_arm64.go": false,
		"my_service.go": true, "http_handler.go": true,
	}
	for name, want := range tests {
		if got := matchesTarget(name); got != want {
			t.Errorf("matchesTarget(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestConstraints(t *testing.T) {
	tests := []struct {
		src  string
		want bool
	}{
		{"package main\n", true},
		{"//go:build linux\n\npackage main\n", true},
		{"//go:build ignore\n\npackage main\n", false},
		{"//go:build tools\n\npackage main\n", false},
		{"//go:build !windows && amd64\n\npackage main\n", true},
		{"//go:build cgo\n\npackage main\n", false},
		{"//go:build go1.21\n\npackage main\n", true},
		{"// +build darwin\n\npackage main\n", false},
		{"// Package main does things.\n//go:build linux || darwin\n\npackage main\n", true},
		{"package main\n\n//go:build ignore\n", true},
	}
	for _, tt := range tests {
		f, err := parser.ParseFile(token.NewFileSet(), "x.go", tt.src, parser.PackageClauseOnly|parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		if got := satisfiesConstraints(f.Comments, f.Package); got != tt.want {
			t.Errorf("%q: got %v, want %v", tt.src, got, tt.want)
		}
	}
}

func TestNameServices(t *testing.T) {
	tests := []struct {
		paths []string
		want  []string
	}{
		{[]string{".", "./cmd/worker"}, []string{"acme", "worker"}},
		{[]string{"./cmd/API_Server"}, []string{"api-server"}},
		{[]string{"./services/orders/cmd/server", "./services/payments/cmd/server"}, []string{"orders-server", "payments-server"}},
		{[]string{"./a/x/api", "./b/x/api"}, []string{"a-x-api", "b-x-api"}},
		{[]string{"./cmd/api", "./internal/api"}, []string{"api", "api"}}, // left for greyship.toml; Detect reports it
	}
	for _, tt := range tests {
		services := make([]Service, len(tt.paths))
		for i, p := range tt.paths {
			services[i].Path = p
		}
		nameServices(services, "acme")
		for i, want := range tt.want {
			if services[i].Name != want {
				t.Errorf("%v: service %d named %q, want %q", tt.paths, i, services[i].Name, want)
			}
		}
	}
}

func TestImportName(t *testing.T) {
	a := newAnalyzer(os.DirFS("."), "example.com/m")
	tests := map[string]string{
		"net/http": "http", "github.com/labstack/echo/v4": "echo", "github.com/nats-io/nats.go": "nats",
		"gopkg.in/yaml.v3": "yaml", "github.com/go-redis/redis/v9": "redis", "github.com/redis/go-redis/v9": "redis",
	}
	for imp, want := range tests {
		if got := a.importName(imp); got != want {
			t.Errorf("importName(%q) = %q, want %q", imp, got, want)
		}
	}
}

// TestOwnRepo runs detection on Greyship itself as a smoke test of a real, larger module.
func TestOwnRepo(t *testing.T) {
	res, err := Detect(os.DirFS("../.."), "greyship")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range res.Services {
		got[s.Name] = s.Type
		t.Logf("%-14s %-7s %-22s %s", s.Name, s.Type, s.Path, s.Reason)
	}
	// Every folder in cmd/ is a service; the ones that listen for connections are web, the rest are workers.
	// greyship-init listens too: its doorman (internal/guestinit) serves an app's port.
	dirs, err := os.ReadDir("../../cmd")
	if err != nil {
		t.Fatal(err)
	}
	web := map[string]bool{"greyship-api": true, "greyship-init": true, "greyship-modproxy": true, "greyship-router": true}
	want := map[string]string{}
	for _, d := range dirs {
		want[d.Name()] = Worker
		if web[d.Name()] {
			want[d.Name()] = Web
		}
	}
	if !maps.Equal(got, want) {
		t.Fatalf("services = %v, want %v", got, want)
	}
}
