package detect

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestRuntimeReads(t *testing.T) {
	src := fstest.MapFS{
		"go.mod":             {Data: []byte("module x\n")},
		"scripts/install.sh": {Data: []byte("#!/bin/sh")},
		"templates/a.html":   {Data: []byte("a")},
		"static/app.css":     {Data: []byte("b")},
		"cmd/api/main.go": {Data: []byte(`package main

import (
	"html/template"
	"net/http"
	"os"
)

func main() {
	_, _ = os.ReadFile("scripts/install.sh")
	_, _ = os.ReadFile("./scripts/install.sh")
	template.Must(template.ParseGlob("templates/*.html"))
	http.Handle("/", http.FileServer(http.Dir("static")))
	_, _ = os.ReadFile("/etc/hosts")
	_, _ = os.ReadFile("not/in/repo.txt")
	_, _ = os.ReadFile(os.Getenv("FILE"))
}
`)},
		"cmd/api/main_test.go": {Data: []byte("package main\n\nimport \"os\"\n\nfunc f() { os.ReadFile(\"scripts/install.sh\") }\n")},
	}
	got, err := runtimeReads(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`cmd/api/main.go:10 reads scripts/install.sh while running`,
		`cmd/api/main.go:12 reads templates/*.html while running, but only the binary is shipped; add it to greyship.toml (files = ["templates/"])`,
		`cmd/api/main.go:13 reads static/ while running`,
	}
	if len(got) != len(want) {
		t.Fatalf("warnings = %q", got)
	}
	for i, w := range want {
		if !strings.HasPrefix(got[i], w) {
			t.Errorf("warning %d = %q, want it to start with %q", i, got[i], w)
		}
	}
	if got, _ := runtimeReads(src, []string{"scripts/", "templates/", "static/"}); len(got) != 0 {
		t.Fatalf("shipped paths still warned: %q", got)
	}
}
