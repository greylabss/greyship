package modproxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func newProxy(t *testing.T) (*Proxy, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch {
		case strings.Contains(r.URL.Path, "missing"):
			http.Error(w, "not found: module missing", http.StatusNotFound)
		case strings.HasSuffix(r.URL.Path, "/@v/list"):
			_, _ = io.WriteString(w, "v1.0.0\nv1.1.0\n")
		default:
			_, _ = io.WriteString(w, "body of "+r.URL.Path)
		}
	}))
	t.Cleanup(upstream.Close)
	return &Proxy{Upstream: upstream.URL, SumDB: upstream.URL + "/sum", CacheDir: t.TempDir(), HTTP: upstream.Client(), Logger: slog.New(slog.DiscardHandler)}, &hits
}

func get(p *Proxy, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestAllowList(t *testing.T) {
	tests := []struct {
		path string
		want int
	}{
		{"/github.com/go-chi/chi/v5/@v/v5.1.0.zip", http.StatusOK},
		{"/github.com/go-chi/chi/v5/@v/v5.1.0.mod", http.StatusOK},
		{"/github.com/go-chi/chi/v5/@v/v5.1.0.info", http.StatusOK},
		{"/github.com/go-chi/chi/v5/@v/list", http.StatusOK},
		{"/github.com/go-chi/chi/v5/@latest", http.StatusOK},
		{"/golang.org/x/sys/@v/v0.0.0-20240101000000-abcdef123456.zip", http.StatusOK},
		{"/github.com/!burnt!sushi/toml/@v/v1.4.0.mod", http.StatusOK},
		{"/sumdb/sum.golang.org/lookup/github.com/go-chi/chi/v5@v5.1.0", http.StatusOK},
		{"/sumdb/sum.golang.org/tile/8/0/x123/456", http.StatusOK},
		{"/sumdb/sum.golang.org/tile/8/1/789.p/12", http.StatusOK},
		{"/sumdb/sum.golang.org/latest", http.StatusOK},
		{"/sumdb/sum.golang.org/supported", http.StatusOK},
		{"/example.com/missing/@v/v1.0.0.mod", http.StatusNotFound},

		{"/", http.StatusNotFound},
		{"/metadata/v1/instance", http.StatusNotFound},
		{"/github.com/a/b/@v/../../../etc/passwd", http.StatusNotFound},
		{"/github.com/a/b/@v/v1.0.0.exe", http.StatusNotFound},
		{"/sumdb/evil.example.com/latest", http.StatusNotFound},
		{"/github.com/A/b/@v/list", http.StatusNotFound},
	}
	p, _ := newProxy(t)
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if rec := get(p, http.MethodGet, tt.path); rec.Code != tt.want {
				t.Fatalf("GET %s = %d, want %d", tt.path, rec.Code, tt.want)
			}
		})
	}
	if rec := get(p, http.MethodPost, "/github.com/go-chi/chi/v5/@v/list"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d, want 405", rec.Code)
	}
}

func TestCachesOnlyFixedFiles(t *testing.T) {
	p, hits := newProxy(t)
	for i := 0; i < 3; i++ {
		rec := get(p, http.MethodGet, "/github.com/go-chi/chi/v5/@v/v5.1.0.zip")
		if rec.Code != http.StatusOK || rec.Body.String() != "body of /github.com/go-chi/chi/v5/@v/v5.1.0.zip" {
			t.Fatalf("zip = %d %q", rec.Code, rec.Body.String())
		}
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("upstream hit %d times for a fixed version file, want 1", n)
	}
	for i := 0; i < 2; i++ {
		get(p, http.MethodGet, "/github.com/go-chi/chi/v5/@v/list")
	}
	if n := hits.Load(); n != 3 {
		t.Fatalf("upstream hits = %d, want list fetched every time", n)
	}
	for i := 0; i < 2; i++ {
		get(p, http.MethodGet, "/example.com/missing/@v/v1.0.0.mod")
	}
	if n := hits.Load(); n != 5 {
		t.Fatalf("upstream hits = %d, want not-found answers left uncached", n)
	}
}

func TestSumDBGoesToItsOwnUpstream(t *testing.T) {
	p, _ := newProxy(t)
	rec := get(p, http.MethodGet, "/sumdb/sum.golang.org/lookup/github.com/go-chi/chi/v5@v5.1.0")
	if want := "body of /sum/lookup/github.com/go-chi/chi/v5@v5.1.0"; rec.Body.String() != want {
		t.Fatalf("lookup body = %q, want %q", rec.Body.String(), want)
	}
}

func TestRejectsOversizedFiles(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, maxSmall+1))
	}))
	defer upstream.Close()
	p := &Proxy{Upstream: upstream.URL, CacheDir: t.TempDir(), HTTP: upstream.Client(), Logger: slog.New(slog.DiscardHandler)}
	if rec := get(p, http.MethodGet, "/example.com/big/@v/v1.0.0.mod"); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized .mod = %d, want 413", rec.Code)
	}
	if rec := get(p, http.MethodGet, "/example.com/big/@v/v1.0.0.mod"); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized .mod was cached: %d", rec.Code)
	}
}
