package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"greyship.tech/internal/secrets"
)

// deployAPI fakes the API and the storage bucket in one server: presigned URLs point back at /bucket/.
type deployAPI struct {
	projectAPI
	declared  map[string]any
	uploaded  []byte
	started   bool
	uploadErr int
	buildLog  string // NDJSON the log endpoint answers; one dropped connection first when drop is set
	drop      bool
	logCalls  []string
	stored    []storedVar
	patched   map[string]any
	have      map[string]bool   // local builds: binaries the project already stores, by sha256
	binaries  map[string][]byte // local builds: uploaded gzip binaries, by service
	files     []byte            // local builds: the uploaded files bundle
}

func (f *deployAPI) server(t *testing.T) *httptest.Server {
	var srv *httptest.Server
	mux := http.NewServeMux()
	inner := f.projectAPI.server(t)
	mux.Handle("/v1/me", proxyTo(inner))
	mux.Handle("/v1/teams/", proxyTo(inner))
	mux.HandleFunc("POST /v1/projects/{id}/deployments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&f.declared)
		w.WriteHeader(http.StatusCreated)
		if bins, ok := f.declared["binaries"].([]any); ok {
			uploads := []map[string]string{}
			for _, b := range bins {
				b := b.(map[string]any)
				if !f.have[b["sha256"].(string)] {
					uploads = append(uploads, map[string]string{"service": b["service"].(string), "upload_url": srv.URL + "/bucket/bin/" + b["service"].(string) + "?X-Amz-Signature=x"})
				}
			}
			resp := map[string]any{
				"deployment": map[string]any{"id": "d1", "project_id": r.PathValue("id"), "status": "uploading", "build_mode": "local"},
				"uploads":    uploads,
			}
			if fl, ok := f.declared["files"].(map[string]any); ok && !f.have[fl["sha256"].(string)] {
				resp["files_upload_url"] = srv.URL + "/bucket/files?X-Amz-Signature=x"
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deployment": map[string]any{"id": "d1", "project_id": r.PathValue("id"), "status": "uploading"},
			"upload_url": srv.URL + "/bucket/d1.tar.gz?X-Amz-Signature=x",
		})
	})
	mux.HandleFunc("GET /v1/projects/{id}/env", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"vars": f.stored})
	})
	mux.HandleFunc("PATCH /v1/projects/{id}/env", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&f.patched)
		_, _ = w.Write([]byte(`{"added":[],"updated":[],"removed":[]}`))
	})
	mux.HandleFunc("PUT /bucket/files", func(_ http.ResponseWriter, r *http.Request) {
		f.files, _ = io.ReadAll(r.Body)
	})
	mux.HandleFunc("PUT /bucket/d1.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		if f.uploadErr != 0 {
			w.WriteHeader(f.uploadErr)
			_, _ = w.Write([]byte("<Error><Code>SignatureDoesNotMatch</Code></Error>"))
			return
		}
		f.uploaded, _ = io.ReadAll(r.Body)
		if r.ContentLength != int64(len(f.uploaded)) {
			t.Errorf("Content-Length %d, body %d", r.ContentLength, len(f.uploaded))
		}
	})
	mux.HandleFunc("GET /v1/build-target", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"goos":"linux","goarch":"amd64"}`))
	})
	mux.HandleFunc("PUT /bucket/bin/{service}", func(w http.ResponseWriter, r *http.Request) {
		if f.binaries == nil {
			f.binaries = map[string][]byte{}
		}
		f.binaries[r.PathValue("service")], _ = io.ReadAll(r.Body)
	})
	mux.HandleFunc("POST /v1/deployments/d1/start", func(w http.ResponseWriter, _ *http.Request) {
		f.started = true
		_, _ = w.Write([]byte(`{"deployment":{"id":"d1","status":"queued"}}`))
	})
	mux.HandleFunc("GET /v1/deployments/d1/logs", func(w http.ResponseWriter, r *http.Request) {
		f.logCalls = append(f.logCalls, r.URL.RawQuery)
		if f.drop {
			f.drop = false
			_, _ = io.WriteString(w, `{"line":{"seq":1,"stream":"build","line":"go: downloading chi"}}`+"\n")
			return
		}
		log := f.buildLog
		if log == "" {
			log = `{"line":{"seq":1,"stream":"build","line":"go: downloading chi"}}` + "\n" +
				`{"line":{"seq":2,"stream":"build","line":"building api"}}` + "\n" +
				`{"end":{"status":"ready","artifacts":[{"service":"api","type":"web","size_bytes":3355443},{"service":"worker","type":"worker","size_bytes":1048576}],"urls":[{"service":"api","url":"https://api-shop.greyship.tech"}],"build":{"seconds":42}}}` + "\n"
		}
		_, _ = io.WriteString(w, log)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func proxyTo(target *httptest.Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequestWithContext(r.Context(), r.Method, target.URL+r.URL.Path, r.Body)
		req.Header = r.Header
		resp, err := target.Client().Do(req)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func tarNames(t *testing.T, b []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	sort.Strings(names)
	return names
}

func TestDeploy(t *testing.T) {
	f := &deployAPI{}
	srv := f.server(t)
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod":             "module github.com/acme/shop\n\ngo 1.24\n",
		"cmd/api/main.go":    "package main\n\nimport \"net/http\"\n\nfunc main() { http.ListenAndServe(\":8080\", nil) }\n",
		"cmd/worker/main.go": "package main\n\nfunc main() { select {} }\n",
		".env.prod":          "STRIPE_KEY=sk_live_secret\n",
		".gitignore":         "tmp/\n",
		"tmp/cache.bin":      "junk",
	})

	_, err := runIn(t, srv, filepath.Join(root, "cmd", "api"), "", false, "deploy")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("an env change without a terminal must need --yes, got %v", err)
	}
	if f.started {
		t.Fatal("deployed without confirming the env change")
	}
	out, err := runIn(t, srv, filepath.Join(root, "cmd", "api"), "", false, "deploy", "--yes")
	if err != nil {
		t.Fatalf("deploy: %v\n%s", err, out)
	}
	if !strings.Contains(out, "+ STRIPE_KEY") || !strings.Contains(out, "only in file → will be added") {
		t.Fatalf("no key-only diff:\n%s", out)
	}
	if set, _ := f.patched["set"].(map[string]any); set["STRIPE_KEY"] != "sk_live_secret" || f.patched["restart"] != false {
		t.Fatalf("env sent = %v, want the new value and no separate restart (the deploy rolls everything)", f.patched)
	}
	for _, want := range []string{"Deploying to haki/shop", "api", "web", "worker", "Packed", "Deployment d1 queued", "│ building api", "build: 42s", "✓ Live", "api     https://api-shop.greyship.tech", "worker  (worker, no URL)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sk_live_secret") {
		t.Fatal("deploy printed an env value")
	}
	if !f.started {
		t.Fatal("deployment was never started")
	}
	sum := sha256.Sum256(f.uploaded)
	if f.declared["sha256"] != hex.EncodeToString(sum[:]) || f.declared["size_bytes"] != float64(len(f.uploaded)) {
		t.Fatalf("declared %v doesn't match the %d uploaded bytes", f.declared, len(f.uploaded))
	}
	want := []string{".gitignore", ".greyship/project.json", "cmd/api/main.go", "cmd/worker/main.go", "go.mod"}
	if got := tarNames(t, f.uploaded); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("uploaded %v, want %v", got, want)
	}
	if l := readLink(t, root); l.Project != "shop" {
		t.Fatalf("link = %+v", l)
	}
}

func TestDeployErrors(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		uploadErr int
		want      string
	}{
		{"no module", map[string]string{"main.go": "package main"}, 0, "no go.mod"},
		{"no services", map[string]string{"go.mod": "module x\n", "lib.go": "package x\n"}, 0, "no services found"},
		{"storage refuses", map[string]string{"go.mod": "module x\n", "main.go": "package main\n\nfunc main() {}\n"}, http.StatusForbidden, "storage answered 403"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &deployAPI{uploadErr: tt.uploadErr}
			srv := f.server(t)
			root := t.TempDir()
			writeFiles(t, root, tt.files)
			out, err := runIn(t, srv, root, "", false, "deploy")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q\n%s", err, tt.want, out)
			}
			if f.started {
				t.Fatal("a failed deploy must not start the deployment")
			}
		})
	}
}

func TestDeployBuildOutcomes(t *testing.T) {
	files := map[string]string{"go.mod": "module x\n", "main.go": "package main\n\nfunc main() {}\n"}
	failed := `{"line":{"seq":1,"stream":"build","line":"./main.go:3:1: syntax error"}}` + "\n" +
		`{"end":{"status":"failed","error":"go build ./cmd/x: exit status 1","artifacts":[]}}` + "\n"

	t.Run("failed build exits with an error", func(t *testing.T) {
		f := &deployAPI{buildLog: failed}
		root := t.TempDir()
		writeFiles(t, root, files)
		out, err := runIn(t, f.server(t), root, "", false, "deploy")
		if err == nil || !strings.Contains(err.Error(), "deploy failed: go build ./cmd/x: exit status 1") {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(out, "│ ./main.go:3:1: syntax error") {
			t.Fatalf("compiler error not shown:\n%s", out)
		}
	})

	t.Run("failed release exits with an error", func(t *testing.T) {
		release := `{"line":{"seq":9,"stream":"system","line":"starting api on dev-1"}}` + "\n" +
			`{"line":{"seq":10,"stream":"build","line":"boom: missing DATABASE_URL"}}` + "\n" +
			`{"end":{"status":"failed","error":"api didn't start on dev-1: health check failed: the app exited with code 2","artifacts":[]}}` + "\n"
		f := &deployAPI{buildLog: release}
		root := t.TempDir()
		writeFiles(t, root, files)
		out, err := runIn(t, f.server(t), root, "", false, "deploy")
		if err == nil || !strings.Contains(err.Error(), "api didn't start on dev-1") {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(out, "│ boom: missing DATABASE_URL") {
			t.Fatalf("app output not shown:\n%s", out)
		}
	})

	t.Run("dropped stream resumes after the last line", func(t *testing.T) {
		f := &deployAPI{drop: true}
		root := t.TempDir()
		writeFiles(t, root, files)
		out, err := runIn(t, f.server(t), root, "", false, "deploy")
		if err != nil {
			t.Fatalf("deploy: %v\n%s", err, out)
		}
		if len(f.logCalls) != 2 || f.logCalls[0] != "follow=1&after=0" || f.logCalls[1] != "follow=1&after=1" {
			t.Fatalf("log requests = %v", f.logCalls)
		}
	})
}

func TestDiffEnv(t *testing.T) {
	local := map[string]string{"A": "1", "B": "changed", "C": "3"}
	stored := []storedVar{
		{Key: "B", Hash: secrets.Hash("p1", "B", "old")},
		{Key: "C", Hash: secrets.Hash("p1", "C", "3")},
		{Key: "D", Hash: secrets.Hash("p1", "D", "4")},
	}
	d := diffEnv("p1", local, stored)
	if strings.Join(d.Added, ",") != "A" || strings.Join(d.Changed, ",") != "B" || d.Same != 1 || strings.Join(d.OnlyStored, ",") != "D" {
		t.Fatalf("diff = %+v", d)
	}
	if !d.changes(false) || (envDiff{Same: 2, OnlyStored: []string{"X"}}).changes(false) {
		t.Fatal("only-stored keys are kept, so on their own they aren't a change unless pruning")
	}
}
