package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPickDeployment(t *testing.T) {
	list := []deployment{
		{ID: "d5", Status: "failed"},
		{ID: "d4", Status: "ready"},
		{ID: "d3", Status: "failed"},
		{ID: "d2", Status: "superseded"},
		{ID: "e1", Status: "superseded"},
	}
	tests := []struct {
		name     string
		list     []deployment
		target   string
		rollback bool
		want     string
		wantErr  string
	}{
		{"redeploy picks the live one", list, "", false, "d4", ""},
		{"rollback skips failed builds", list, "", true, "d2", ""},
		{"by id prefix", list, "e", true, "e1", ""},
		{"ambiguous prefix", list, "d", true, "", "matches 4 deployments"},
		{"unknown id", list, "zz", true, "", "no recent deployment"},
		{"nothing live", list[:1], "", false, "", "nothing is live yet"},
		{"nothing earlier", list[:2], "", true, "", "no earlier deployment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pickDeployment(tt.list, tt.target, tt.rollback)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got.ID != tt.want {
				t.Fatalf("got %s, %v; want %s", got.ID, err, tt.want)
			}
		})
	}
}

func TestRollbackCommand(t *testing.T) {
	var redeployed string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/me", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"user":{"id":"u1","email":"a@b.co","token_team":"haki"}}`))
	})
	mux.HandleFunc("GET /v1/projects/p1/deployments", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"deployments":[{"id":"aaaa2222","status":"ready"},{"id":"aaaa1111","status":"superseded"}]}`))
	})
	mux.HandleFunc("POST /v1/deployments/{id}/redeploy", func(w http.ResponseWriter, r *http.Request) {
		redeployed = r.PathValue("id")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"deployment":{"id":"aaaa3333","status":"built","rollback_of":"` + redeployed + `"}}`))
	})
	mux.HandleFunc("GET /v1/deployments/aaaa3333/logs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"line":{"seq":1,"stream":"system","line":"redeploying the same binaries"}}` + "\n" + `{"end":{"status":"ready","artifacts":[],"urls":[]}}` + "\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"go.mod": "module x\n", ".greyship/project.json": `{"team":"haki","project":"temp","id":"p1"}`})

	out, err := runIn(t, srv, dir, "", false, "rollback")
	if err != nil || redeployed != "aaaa1111" || !strings.Contains(out, "Rolling back to aaaa1111") || !strings.Contains(out, "✓ Live") {
		t.Fatalf("rollback: %v %s\n%s", err, redeployed, out)
	}
	out, err = runIn(t, srv, dir, "", false, "redeploy")
	if err != nil || redeployed != "aaaa2222" || !strings.Contains(out, "Redeploying aaaa2222") {
		t.Fatalf("redeploy: %v %s\n%s", err, redeployed, out)
	}
}
