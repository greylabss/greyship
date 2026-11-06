package buildd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"greyship.tech/internal/build/fly"
)

func tarball(t *testing.T, files map[string]string, typ byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		h := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: typ}
		if typ == tar.TypeSymlink {
			h.Size, h.Linkname = 0, body
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			_, _ = tw.Write([]byte(body))
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestUnpack(t *testing.T) {
	dir := t.TempDir()
	if err := unpack(tarball(t, map[string]string{"go.mod": "module x\n", "cmd/api/main.go": "package main\n"}, tar.TypeReg), dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "cmd/api/main.go")); string(b) != "package main\n" {
		t.Fatalf("unpacked %q", b)
	}

	for name, src := range map[string][]byte{
		"parent escape": tarball(t, map[string]string{"../evil": "x"}, tar.TypeReg),
		"deep escape":   tarball(t, map[string]string{"a/../../evil": "x"}, tar.TypeReg),
		"absolute path": tarball(t, map[string]string{"/etc/evil": "x"}, tar.TypeReg),
		"not a tarball": []byte("hello"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := unpack(src, t.TempDir()); err == nil {
				t.Fatal("accepted a tarball that writes outside the source folder")
			}
		})
	}

	// Symlinks are skipped, so a link can't point a later file outside the folder.
	out := t.TempDir()
	if err := unpack(tarball(t, map[string]string{"link": "/etc"}, tar.TypeSymlink), out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(out, "link")); err == nil {
		t.Fatal("a symlink was written")
	}
}

func TestEnvKeepsMachineVariablesOut(t *testing.T) {
	t.Setenv("FLY_API_TOKEN", "secret")
	b := &Builder{Work: "/work", Cache: "/cache"}
	env := strings.Join(b.env("amd64"), "\n")
	for _, want := range []string{"GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly -trimpath", "CGO_ENABLED=0", "GOMODCACHE=/cache/mod", "GOSUMDB=sum.golang.org"} {
		if !strings.Contains(env, want) {
			t.Errorf("env missing %s", want)
		}
	}
	if strings.Contains(env, "secret") {
		t.Fatal("a machine variable leaked into the build")
	}
}

// A whole build against a fake API and storage: real go build of a tiny module.
func TestBuildReportsAndUploads(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not installed")
	}
	src := tarball(t, map[string]string{"go.mod": "module x\n\ngo 1.24\n", "main.go": "package main\n\nfunc main() {}\n"}, tar.TypeReg)
	var mu sync.Mutex
	var reports []fly.Report
	uploaded := map[string]int{}
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /source", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(src) })
	mux.HandleFunc("POST /v1/internal/builds/d1/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Build tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var u fly.UploadRequest
		_ = json.NewDecoder(r.Body).Decode(&u)
		_ = json.NewEncoder(w).Encode(map[string]string{"url": srv.URL + "/put/" + u.Target})
	})
	mux.HandleFunc("PUT /put/{name}", func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		uploaded[r.PathValue("name")] = len(b)
		mu.Unlock()
	})
	mux.HandleFunc("POST /v1/internal/builds/d1/report", func(w http.ResponseWriter, r *http.Request) {
		var rep fly.Report
		_ = json.NewDecoder(r.Body).Decode(&rep)
		mu.Lock()
		reports = append(reports, rep)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	work := t.TempDir()
	b := &Builder{Work: filepath.Join(work, "w"), Cache: filepath.Join(work, "cache"), Go: gobin, HTTP: srv.Client(), Now: time.Now}
	job := fly.MachineJob{Deployment: "d1", SourceURL: srv.URL + "/source", APIURL: srv.URL, Token: "tok", GOARCH: "amd64", Targets: []fly.Target{{Name: "api", Path: "."}}}
	if err := b.Run(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	last := reports[len(reports)-1].Outcome
	if last == nil || !last.OK || last.Binaries["api"].Size == 0 || uploaded["api"] != int(last.Binaries["api"].Size) {
		t.Fatalf("outcome %+v, uploaded %v", last, uploaded)
	}
	if _, err := os.Stat(b.Work); !os.IsNotExist(err) {
		t.Fatal("the workspace must be wiped after the build")
	}
	var log []string
	for _, r := range reports {
		for _, l := range r.Lines {
			log = append(log, l.Text)
		}
	}
	if !strings.Contains(strings.Join(log, "\n"), "building api (.)") {
		t.Fatalf("log: %v", log)
	}
}
