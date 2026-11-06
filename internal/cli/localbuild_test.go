package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"io"
	"os/exec"
	"strings"
	"testing"
)

func goTool(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not installed")
	}
	return path
}

var localFiles = map[string]string{
	"go.mod":              "module github.com/acme/shop\n\ngo 1.24\n",
	"cmd/api/main.go":     "package main\n\nimport \"net/http\"\n\nfunc main() { _ = http.ListenAndServe(\":8080\", nil) }\n",
	"cmd/worker/main.go":  "package main\n\nfunc main() { select {} }\n",
	"cmd/migrate/main.go": "package main\n\nfunc main() {}\n",
}

func TestDeployLocal(t *testing.T) {
	gt := goTool(t)
	root := t.TempDir()
	writeFiles(t, root, localFiles)
	gitCommit(t, root) // a real repo, so a binary stamped with git info would show up below

	f := &deployAPI{have: map[string]bool{}}
	out, err := runWithGo(t, f.server(t), root, "", false, gt, "deploy")
	if err != nil {
		t.Fatalf("deploy: %v\n%s", err, out)
	}
	for _, want := range []string{"Building here with go", "for linux/amd64", "Uploaded 3 binaries", "Deployment d1 queued", "✓ Live"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if f.uploaded != nil || f.declared["size_bytes"] != nil {
		t.Fatal("a local build must not upload the source")
	}
	bins := f.declared["binaries"].([]any)
	types := map[string]string{}
	for _, b := range bins {
		b := b.(map[string]any)
		types[b["service"].(string)] = b["type"].(string) + " " + b["path"].(string)
		checkUploadedBinary(t, b, f.binaries[b["service"].(string)])
	}
	want := map[string]string{"api": "web ./cmd/api", "worker": "worker ./cmd/worker", "release": "release ./cmd/migrate"}
	for name, w := range want {
		if types[name] != w {
			t.Errorf("%s = %q, want %q", name, types[name], w)
		}
	}
	if !f.started {
		t.Fatal("deployment was never started")
	}

	t.Run("redeploy uploads only what changed", func(t *testing.T) {
		g := &deployAPI{have: map[string]bool{}}
		for _, b := range bins {
			b := b.(map[string]any)
			if b["service"] != "api" {
				g.have[b["sha256"].(string)] = true
			}
		}
		out, err := runWithGo(t, g.server(t), root, "", false, gt, "deploy")
		if err != nil {
			t.Fatalf("deploy: %v\n%s", err, out)
		}
		if len(g.binaries) != 1 || g.binaries["api"] == nil || !strings.Contains(out, "Uploaded 1 of 3 binaries") {
			t.Fatalf("uploaded %d binaries:\n%s", len(g.binaries), out)
		}
	})
}

func TestDeployLocalFiles(t *testing.T) {
	gt := goTool(t)
	root := t.TempDir()
	files := map[string]string{"greyship.toml": "files = [\"templates/\", \"scripts/install.sh\"]\n", "templates/home.html": "<h1>hi</h1>", "scripts/install.sh": "#!/bin/sh\n"}
	for k, v := range localFiles {
		files[k] = v
	}
	writeFiles(t, root, files)

	f := &deployAPI{have: map[string]bool{}}
	out, err := runWithGo(t, f.server(t), root, "", false, gt, "deploy")
	if err != nil {
		t.Fatalf("deploy: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Files: templates/ (1 file), scripts/install.sh") {
		t.Fatalf("output:\n%s", out)
	}
	declared := f.declared["files"].(map[string]any)
	sum := sha256.Sum256(f.files)
	if len(f.files) == 0 || hex.EncodeToString(sum[:]) != declared["sha256"] || int64(len(f.files)) != int64(declared["size_bytes"].(float64)) {
		t.Fatalf("uploaded %d bytes of files, declared %v", len(f.files), declared)
	}

	writeFiles(t, root, map[string]string{"greyship.toml": "", "cmd/api/main.go": "package main\n\nimport (\n\t\"net/http\"\n\t\"os\"\n)\n\nfunc main() {\n\t_, _ = os.ReadFile(\"scripts/install.sh\")\n\t_ = http.ListenAndServe(\":8080\", nil)\n}\n"})
	out, err = runWithGo(t, (&deployAPI{have: map[string]bool{}}).server(t), root, "", false, gt, "deploy")
	if err != nil || !strings.Contains(out, "warning: cmd/api/main.go:9 reads scripts/install.sh while running") {
		t.Fatalf("an unshipped runtime read must be warned about, not fail: %v\n%s", err, out)
	}

	writeFiles(t, root, map[string]string{"greyship.toml": "files = [\"static/\"]\n"})
	out, err = runWithGo(t, (&deployAPI{have: map[string]bool{}}).server(t), root, "", false, gt, "deploy")
	if err == nil || !strings.Contains(err.Error(), `"static/"`) {
		t.Fatalf("a missing files entry must fail the deploy: %v\n%s", err, out)
	}
}

// checkUploadedBinary inflates an upload and checks it is what the server's verifier accepts.
func checkUploadedBinary(t *testing.T, declared map[string]any, gz []byte) {
	t.Helper()
	if int64(len(gz)) != int64(declared["gz_bytes"].(float64)) {
		t.Fatalf("%s: uploaded %d bytes, declared %v", declared["service"], len(gz), declared["gz_bytes"])
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != declared["sha256"] || int64(len(raw)) != int64(declared["size_bytes"].(float64)) {
		t.Fatalf("%s: checksum or size doesn't match", declared["service"])
	}
	info, err := buildinfo.Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	if settings["vcs"] != "" || settings["vcs.revision"] != "" {
		t.Fatalf("%s carries git info (%v); a commit would change every binary", declared["service"], settings)
	}
	if settings["GOOS"] != "linux" || settings["GOARCH"] != "amd64" || settings["CGO_ENABLED"] != "0" || settings["-trimpath"] != "true" {
		t.Fatalf("%s built with %v", declared["service"], settings)
	}
}

func TestDeployLocalModes(t *testing.T) {
	gt := goTool(t)

	t.Run("compile error stops before anything is created", func(t *testing.T) {
		root := t.TempDir()
		writeFiles(t, root, map[string]string{"go.mod": "module x\n\ngo 1.24\n", "main.go": "package main\n\nfunc main() { undefined() }\n"})
		f := &deployAPI{}
		out, err := runWithGo(t, f.server(t), root, "", false, gt, "deploy")
		if err == nil || !strings.Contains(err.Error(), "go build . failed") || !strings.Contains(out, "undefined") {
			t.Fatalf("err = %v\n%s", err, out)
		}
		if f.declared != nil || f.started {
			t.Fatal("a compile error must stop before the deployment is created")
		}
	})

	t.Run("--remote uploads the source", func(t *testing.T) {
		root := t.TempDir()
		writeFiles(t, root, localFiles)
		f := &deployAPI{}
		out, err := runWithGo(t, f.server(t), root, "", false, gt, "deploy", "--remote")
		if err != nil {
			t.Fatalf("deploy: %v\n%s", err, out)
		}
		if f.uploaded == nil || f.declared["binaries"] != nil || strings.Contains(out, "Go isn't installed") {
			t.Fatalf("--remote didn't build on Greyship:\n%s", out)
		}
	})

	t.Run("without go the source is uploaded", func(t *testing.T) {
		root := t.TempDir()
		writeFiles(t, root, localFiles)
		f := &deployAPI{}
		out, err := runIn(t, f.server(t), root, "", false, "deploy")
		if err != nil || f.uploaded == nil || !strings.Contains(out, "Go isn't installed here") {
			t.Fatalf("deploy: %v\n%s", err, out)
		}
	})
}

func TestVetWarningsAreShown(t *testing.T) {
	a := &App{GoTool: goTool(t), Getenv: func(string) string { return "" }}
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"go.mod": "module x\n\ngo 1.24\n", "main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Printf(\"%d\", \"x\") }\n"})
	v := a.startVet(context.Background(), root, a.buildEnv("amd64"))
	<-v.done
	var out bytes.Buffer
	a.Out = &out
	v.print(a)
	v.stop()
	if !strings.Contains(out.String(), "go vet warnings") || !strings.Contains(out.String(), "Printf") {
		t.Fatalf("vet output:\n%s", out.String())
	}
}

// gitCommit makes root a git repository with one commit and an uncommitted change, like a developer's checkout.
func gitCommit(t *testing.T, root string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "x"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeFiles(t, root, map[string]string{"README.md": "dirty"})
}
