// Package modproxy is the Go module proxy build sandboxes talk to (architecture §2.2). It is the only host a
// sandbox can reach: it serves the GOPROXY protocol and the checksum database under /sumdb/sum.golang.org/,
// forwards to the upstream module proxy and checksum database, and caches files that never change on disk.
// Every other path is refused.
package modproxy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// MaxZip matches the Go command's own limit on module zip files.
const MaxZip = 500 << 20

const maxSmall = 16 << 20

type Proxy struct {
	Upstream string // module proxy, e.g. https://proxy.golang.org
	SumDB    string // checksum database, e.g. https://sum.golang.org
	CacheDir string
	HTTP     *http.Client
	Logger   *slog.Logger
}

var (
	// /<module>/@v/<version>.info|.mod|.zip — fixed for a given version, so cacheable.
	versionFile = regexp.MustCompile(`^/[a-z0-9.\-_~!/]+/@v/v[0-9][0-9A-Za-z.\-+]*\.(info|mod|zip)$`)
	// /<module>/@v/list and /<module>/@latest change over time; never cached.
	moving = regexp.MustCompile(`^/[a-z0-9.\-_~!/]+/(@v/list|@latest)$`)
	// Checksum database: lookups and tiles. Lookups and tiles are immutable; the signed tree head is not.
	sumdbFixed  = regexp.MustCompile(`^/sumdb/sum\.golang\.org/(lookup/[a-z0-9.\-_~!/]+@v[0-9][0-9A-Za-z.\-+]*|tile/[0-9]+/[0-9]+(/x[0-9]{3})*/[0-9]{3}(\.p/[0-9]+)?)$`)
	sumdbMoving = regexp.MustCompile(`^/sumdb/sum\.golang\.org/latest$`)
)

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Path
	if strings.Contains(path, "..") || strings.Contains(path, "//") {
		http.NotFound(w, r)
		return
	}
	switch {
	case path == "/sumdb/sum.golang.org/supported":
		// Tells the go command it may fetch the checksum database through us.
		w.WriteHeader(http.StatusOK)
	case versionFile.MatchString(path), sumdbFixed.MatchString(path):
		p.serveCached(w, r, path)
	case moving.MatchString(path), sumdbMoving.MatchString(path):
		p.forward(w, r, path)
	default:
		http.NotFound(w, r)
	}
}

func (p *Proxy) limit(path string) int64 {
	if strings.HasSuffix(path, ".zip") {
		return MaxZip
	}
	return maxSmall
}

func (p *Proxy) cachePath(path string) string {
	sum := sha256.Sum256([]byte(path))
	name := hex.EncodeToString(sum[:])
	return filepath.Join(p.CacheDir, name[:2], name)
}

func (p *Proxy) serveCached(w http.ResponseWriter, r *http.Request, path string) {
	file := p.cachePath(path)
	if f, err := os.Open(file); err == nil { //nolint:gosec // name is a hash inside CacheDir
		defer func() { _ = f.Close() }()
		http.ServeContent(w, r, "", time.Time{}, f)
		return
	}
	resp, err := p.get(r, path)
	if err != nil {
		p.upstreamError(w, path, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		relay(w, resp, maxSmall)
		return
	}
	if err := p.store(file, resp.Body, p.limit(path)); err != nil {
		p.upstreamError(w, path, err)
		return
	}
	f, err := os.Open(file) //nolint:gosec // name is a hash inside CacheDir
	if err != nil {
		p.upstreamError(w, path, err)
		return
	}
	defer func() { _ = f.Close() }()
	http.ServeContent(w, r, "", time.Time{}, f)
}

// store writes the body to a temporary file and renames it into place, so a half-written file is never served.
func (p *Proxy) store(file string, body io.Reader, limit int64) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".part-*")
	if err != nil {
		return fmt.Errorf("create cache file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	n, err := io.Copy(tmp, io.LimitReader(body, limit+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if n > limit {
		return errTooLarge
	}
	return os.Rename(tmp.Name(), file)
}

var errTooLarge = errors.New("file is larger than the module size limit")

func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, path string) {
	resp, err := p.get(r, path)
	if err != nil {
		p.upstreamError(w, path, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	relay(w, resp, maxSmall)
}

func (p *Proxy) get(r *http.Request, path string) (*http.Response, error) {
	target := strings.TrimRight(p.Upstream, "/") + path
	if rest, ok := strings.CutPrefix(path, "/sumdb/sum.golang.org"); ok {
		target = strings.TrimRight(p.SumDB, "/") + rest
	}
	// The host is always Upstream or SumDB; only paths that passed the allow-list in ServeHTTP get here.
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil) //nolint:gosec // fixed upstream host, allow-listed path
	if err != nil {
		return nil, err
	}
	return p.HTTP.Do(req) //nolint:gosec // fixed upstream host, allow-listed path
}

// relay copies an upstream answer. 404 and 410 matter: they are how the go command learns a module doesn't exist.
func relay(w http.ResponseWriter, resp *http.Response, limit int64) {
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, limit))
}

func (p *Proxy) upstreamError(w http.ResponseWriter, path string, err error) {
	p.Logger.Warn("module proxy upstream", "path", path, "err", err)
	if errors.Is(err, errTooLarge) {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "upstream module proxy unavailable", http.StatusBadGateway)
}
