package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// projectAPI answers /v1/me and the project endpoints; existing holds slugs that already exist in the team.
type projectAPI struct {
	existing map[string]bool
	created  []string
}

func (f *projectAPI) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/me", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"user":{"email":"a@b.co","token_team":"haki"}}`))
	})
	mux.HandleFunc("POST /v1/teams/haki/projects", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Name string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		slug := strings.ToLower(strings.ReplaceAll(body.Name, " ", "-"))
		p := map[string]string{"id": "id-" + slug, "team": "haki", "name": body.Name, "slug": slug, "created_at": "2026-10-26T10:00:00Z"}
		if f.existing[slug] {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "slug_taken", "message": "taken"}, "project": p})
			return
		}
		f.created = append(f.created, slug)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"project": p})
	})
	mux.HandleFunc("GET /v1/teams/haki/projects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"id-acme-api","team":"haki","name":"Acme API","slug":"acme-api","created_at":"2026-10-26T10:00:00Z"},{"id":"id-bot","team":"haki","name":"bot","slug":"bot","created_at":"2026-10-20T10:00:00Z"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func runIn(t *testing.T, srv *httptest.Server, dir, stdin string, interactive bool, args ...string) (string, error) {
	t.Helper()
	return runWithGo(t, srv, dir, stdin, interactive, "", args...)
}

// runWithGo is runIn on a machine where goTool is the go command; runIn behaves as if Go isn't installed.
func runWithGo(t *testing.T, srv *httptest.Server, dir, stdin string, interactive bool, goTool string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	env := map[string]string{"GREYSHIP_API_URL": srv.URL, "GREYSHIP_TOKEN": "gs_ci"}
	a := &App{
		Out: &out, Err: &out, In: strings.NewReader(stdin), Interactive: interactive, Dir: dir,
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		Getenv:     func(k string) string { return env[k] },
		HTTP:       srv.Client(),
		Sleep:      func(context.Context, time.Duration) error { return nil },
		Version:    "test",
		GoTool:     goTool,
	}
	root := NewRoot(a)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func readLink(t *testing.T, dir string) Link {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, linkPath))
	if err != nil {
		t.Fatal(err)
	}
	var l Link
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestInit(t *testing.T) {
	tests := []struct {
		name        string
		folder      string
		existing    map[string]bool
		stdin       string
		interactive bool
		args        []string
		wantErr     string
		wantLink    string
		wantCreated bool
	}{
		{name: "folder name by default", folder: "bot", args: []string{"init"}, wantLink: "id-bot", wantCreated: true},
		{name: "--name wins", folder: "x", args: []string{"init", "--name", "Acme API"}, wantLink: "id-acme-api", wantCreated: true},
		{name: "prompt edits the name", folder: "x", stdin: "Orders\n", interactive: true, args: []string{"init"}, wantLink: "id-orders", wantCreated: true},
		{name: "prompt keeps suggestion on enter", folder: "bot", stdin: "\n", interactive: true, args: []string{"init"}, wantLink: "id-bot", wantCreated: true},
		{name: "taken, non-interactive fails", folder: "bot", existing: map[string]bool{"bot": true}, args: []string{"init"}, wantErr: "--yes"},
		{name: "taken, --yes links", folder: "bot", existing: map[string]bool{"bot": true}, args: []string{"init", "--yes"}, wantLink: "id-bot"},
		{name: "taken, person agrees", folder: "bot", existing: map[string]bool{"bot": true}, stdin: "\ny\n", interactive: true, args: []string{"init"}, wantLink: "id-bot"},
		{name: "empty stdin keeps the suggestion", folder: "bot", interactive: true, args: []string{"init"}, wantLink: "id-bot", wantCreated: true},
		{name: "taken, empty stdin fails like CI", folder: "bot", existing: map[string]bool{"bot": true}, interactive: true, args: []string{"init"}, wantErr: "--yes"},
		{name: "taken, person declines", folder: "bot", existing: map[string]bool{"bot": true}, stdin: "\nn\n", interactive: true, args: []string{"init"}, wantErr: "nothing changed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &projectAPI{existing: tt.existing}
			dir := filepath.Join(t.TempDir(), tt.folder)
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			out, err := runIn(t, f.server(t), dir, tt.stdin, tt.interactive, tt.args...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				if _, statErr := os.Stat(filepath.Join(dir, linkPath)); statErr == nil {
					t.Fatal("a failed init must not write a link")
				}
				return
			}
			if err != nil {
				t.Fatalf("init: %v\n%s", err, out)
			}
			if l := readLink(t, dir); l.ID != tt.wantLink || l.Team != "haki" {
				t.Fatalf("link = %+v, want id %s in haki", l, tt.wantLink)
			}
			if got := len(f.created) == 1; got != tt.wantCreated {
				t.Fatalf("created = %v, want %v", f.created, tt.wantCreated)
			}
		})
	}
}

func TestInitAlreadyLinkedFromSubfolder(t *testing.T) {
	f := &projectAPI{}
	srv := f.server(t)
	root := filepath.Join(t.TempDir(), "bot")
	sub := filepath.Join(root, "cmd", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runIn(t, srv, root, "", false, "init"); err != nil {
		t.Fatal(err)
	}
	out, err := runIn(t, srv, sub, "", false, "init")
	if err != nil || !strings.Contains(out, "Already linked to haki/bot") {
		t.Fatalf("second init: %v %q", err, out)
	}
	if len(f.created) != 1 {
		t.Fatalf("created %v, want exactly one project", f.created)
	}
	if _, err := os.Stat(filepath.Join(sub, linkPath)); err == nil {
		t.Fatal("init in a subfolder must not write a second link")
	}
}

func TestProjects(t *testing.T) {
	srv := (&projectAPI{}).server(t)
	dir := t.TempDir()
	if _, err := writeLink(dir, Link{Team: "haki", Project: "bot", ID: "id-bot"}); err != nil {
		t.Fatal(err)
	}
	out, err := runIn(t, srv, dir, "", false, "projects")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NAME", "Acme API", "acme-api", "2026-10", "bot", "← this folder"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "← this folder") != 1 {
		t.Fatalf("only the linked project should be marked:\n%s", out)
	}

	out, err = runIn(t, srv, dir, "", false, "projects", "--json")
	var list []project
	if err != nil || json.Unmarshal([]byte(out), &list) != nil || len(list) != 2 {
		t.Fatalf("--json: %v %q", err, out)
	}
}

func TestInferName(t *testing.T) {
	tests := []struct {
		name  string
		gomod string
		want  string
	}{
		{"github path", "module github.com/acme/orders-api\n\ngo 1.27\n", "orders-api"},
		{"major version suffix", "module github.com/acme/orders-api/v2\n", "orders-api"},
		{"single element", "module billing\n", "billing"},
		{"quoted with comment", "// my service\nmodule \"example.com/x/payments\" // prod\n", "payments"},
		{"no go.mod", "", "my-folder"},
		{"no module line", "go 1.27\n", "my-folder"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "my-folder")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.gomod != "" {
				if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(tt.gomod), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := inferName(dir); got != tt.want {
				t.Fatalf("inferName = %q, want %q", got, tt.want)
			}
		})
	}
}

// newModule makes a repo with go.mod and a cmd/api subfolder, returning both paths.
func newModule(t *testing.T, module string) (root, sub string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "checkout")
	sub = filepath.Join(root, "cmd", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+module+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, sub
}

func TestInitUsesModuleNameAndRoot(t *testing.T) {
	f := &projectAPI{}
	root, sub := newModule(t, "github.com/acme/orders-api")
	if _, err := runIn(t, f.server(t), sub, "", false, "init"); err != nil {
		t.Fatal(err)
	}
	if l := readLink(t, root); l.Project != "orders-api" {
		t.Fatalf("link = %+v, want orders-api at the module root", l)
	}
	if _, err := os.Stat(filepath.Join(sub, linkPath)); err == nil {
		t.Fatal("the link belongs next to go.mod, not in the subfolder")
	}
}

func TestEnsureProject(t *testing.T) {
	tests := []struct {
		name        string
		existing    map[string]bool
		linked      bool
		stdin       string
		interactive bool
		wantErr     string
		wantProject string
		wantCreated bool
	}{
		{name: "already linked", linked: true, wantProject: "linked"},
		{name: "unlinked creates from go.mod", wantProject: "orders-api", wantCreated: true},
		{name: "clash in CI fails", existing: map[string]bool{"orders-api": true}, wantErr: "grey init --yes"},
		{name: "clash, empty stdin fails", existing: map[string]bool{"orders-api": true}, interactive: true, wantErr: "grey init --yes"},
		{name: "clash, enter means no", existing: map[string]bool{"orders-api": true}, stdin: "\n", interactive: true, wantProject: "orders-api-2", wantCreated: true},
		{name: "clash, no skips taken numbers", existing: map[string]bool{"orders-api": true, "orders-api-2": true}, stdin: "n\n", interactive: true, wantProject: "orders-api-3", wantCreated: true},
		{name: "clash, yes deploys to it", existing: map[string]bool{"orders-api": true}, stdin: "y\n", interactive: true, wantProject: "orders-api"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &projectAPI{existing: tt.existing}
			srv := f.server(t)
			root, sub := newModule(t, "github.com/acme/orders-api")
			if tt.linked {
				if _, err := writeLink(root, Link{Team: "haki", Project: "linked", ID: "id-linked"}); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			a := &App{Out: &out, Err: &out, Interactive: tt.interactive, Dir: sub, HTTP: srv.Client(), Version: "test"}
			api := &client{base: srv.URL, token: "gs_ci", http: srv.Client(), agent: "test"}
			l, created, err := a.ensureProject(context.Background(), api, bufio.NewReader(strings.NewReader(tt.stdin)))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				if _, statErr := os.Stat(filepath.Join(root, linkPath)); statErr == nil {
					t.Fatal("a failed resolve must not write a link")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if l.Project != tt.wantProject || created != tt.wantCreated {
				t.Fatalf("got %s created=%v, want %s created=%v", l.Project, created, tt.wantProject, tt.wantCreated)
			}
			if saved := readLink(t, root); saved != l {
				t.Fatalf("saved link %+v, returned %+v", saved, l)
			}
		})
	}
}
