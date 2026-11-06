package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubCommands(t *testing.T) {
	var connected map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/me", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"user":{"id":"u1","email":"a@b.co","token_team":"haki"}}`))
	})
	mux.HandleFunc("GET /v1/teams/haki/github/repos", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"repos":[{"full_name":"haki/temp","default_branch":"main","private":true}]}`))
	})
	mux.HandleFunc("GET /v1/teams/haki/github/install", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"url":"https://github.com/apps/greyship/installations/new?state=s"}`))
	})
	mux.HandleFunc("POST /v1/projects/p1/github", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&connected)
		_, _ = w.Write([]byte(`{"repo":{"full_name":"haki/temp","branch":"main"}}`))
	})
	mux.HandleFunc("POST /v1/teams/haki/github/import-url", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"project":{"id":"p9","team":"haki","slug":"pub"},"deployment_id":"d1"}`))
	})
	mux.HandleFunc("POST /v1/projects/p1/github/deploy", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"deployment_id":"d1"}`))
	})
	mux.HandleFunc("GET /v1/deployments/d1/logs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"line":{"seq":1,"stream":"system","line":"fetching source"}}` + "\n" + `{"end":{"status":"ready","artifacts":[],"urls":[]}}` + "\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := runIn(t, srv, t.TempDir(), "", false, "github", "repos")
	if err != nil || !strings.Contains(out, "haki/temp") || !strings.Contains(out, "private") {
		t.Fatalf("repos: %v\n%s", err, out)
	}
	out, err = runIn(t, srv, t.TempDir(), "", false, "github", "install")
	if err != nil || !strings.Contains(out, "github.com/apps/greyship/installations/new") {
		t.Fatalf("install: %v\n%s", err, out)
	}

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"go.mod": "module x\n", ".greyship/project.json": `{"team":"haki","project":"temp","id":"p1"}`})
	out, err = runIn(t, srv, dir, "", false, "github", "connect", "haki/temp")
	if err != nil || connected["repo"] != "haki/temp" || !strings.Contains(out, "deploys on every push to main of haki/temp") {
		t.Fatalf("connect: %v %v\n%s", err, connected, out)
	}

	fresh := t.TempDir()
	out, err = runIn(t, srv, fresh, "", false, "github", "import", "https://github.com/haki/pub")
	if err != nil || !strings.Contains(out, "Created haki/pub") || !strings.Contains(out, "✓ Live") {
		t.Fatalf("import: %v\n%s", err, out)
	}
	if l := readLink(t, fresh); l.ID != "p9" {
		t.Fatalf("the folder should now be linked to the new project: %+v", l)
	}
	out, err = runIn(t, srv, dir, "", false, "github", "deploy")
	if err != nil || !strings.Contains(out, "│ fetching source") {
		t.Fatalf("deploy latest: %v\n%s", err, out)
	}
}
