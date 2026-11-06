package cli

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"greyship.tech/internal/detect"
	"greyship.tech/internal/pack"
)

type localOptions struct {
	asJSON, yes, prune bool
	in                 *bufio.Reader
}

// localBinary is one service compiled on this machine, ready to upload.
type localBinary struct {
	Service   string `json:"service"`
	Path      string `json:"path"`
	Type      string `json:"type"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	GzBytes   int64  `json:"gz_bytes"`
	gz        []byte
}

// deployLocal compiles the services here for the platform's Linux CPU and uploads only binaries the project
// doesn't have yet (decisions.md 034). The server checks them and deploys them exactly like its own builds.
func (a *App) deployLocal(ctx context.Context, api *client, l Link, created bool, found detect.Result, root string, o localOptions) error {
	var target struct {
		GOOS   string `json:"goos"`
		GOARCH string `json:"goarch"`
	}
	if err := api.do(ctx, "GET", "/v1/build-target", nil, &target); err != nil {
		return friendly(err)
	}
	env := a.buildEnv(target.GOARCH)
	if !o.asJSON {
		a.printPlan(l, created, found)
		fmt.Fprintf(a.Out, "\nBuilding here with %s for linux/%s\n", a.goVersion(ctx, root, env), target.GOARCH)
	}

	// The files bundle comes first: a typo in greyship.toml's files fails before anything compiles.
	var files []byte
	if len(found.Files) > 0 {
		var buf bytes.Buffer
		res, err := pack.BundleDir(root, found.Files, &buf)
		if err != nil {
			return err
		}
		files = buf.Bytes()
		if !o.asJSON {
			fmt.Fprintf(a.Out, "Files: %s\n", res.Summary())
		}
	}

	vet := a.startVet(ctx, root, env)
	defer vet.stop()

	dir, err := os.MkdirTemp("", "grey-build-*")
	if err != nil {
		return fmt.Errorf("create build folder: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	bins, err := a.compile(ctx, root, dir, env, found)
	if err != nil {
		return err
	}

	if err := a.syncDeployEnv(ctx, api, l, root, created, o.yes, o.prune, o.in); err != nil {
		return err
	}
	var up struct {
		Deployment deployment `json:"deployment"`
		Uploads    []struct {
			Service   string `json:"service"`
			UploadURL string `json:"upload_url"`
		} `json:"uploads"`
		FilesUploadURL string `json:"files_upload_url"`
	}
	body := map[string]any{"binaries": bins}
	if files != nil {
		sum := sha256.Sum256(files)
		body["files"] = map[string]any{"sha256": hex.EncodeToString(sum[:]), "size_bytes": len(files)}
	}
	if sha := commitSHA(ctx, root); sha != "" {
		body["commit_sha"] = sha
	}
	if err := api.do(ctx, "POST", "/v1/projects/"+url.PathEscape(l.ID)+"/deployments", body, &up); err != nil {
		return linkedElsewhere(err, l)
	}
	byName := map[string]localBinary{}
	for _, b := range bins {
		byName[b.Service] = b
	}
	var sent int64
	for _, u := range up.Uploads {
		b, ok := byName[u.Service]
		if !ok {
			return fmt.Errorf("the API asked for a binary this deploy doesn't have: %s", u.Service)
		}
		if err := a.put(ctx, u.UploadURL, b.Service, bytes.NewReader(b.gz), b.GzBytes); err != nil {
			return err
		}
		sent += b.GzBytes
	}
	if up.FilesUploadURL != "" {
		if err := a.put(ctx, up.FilesUploadURL, "files", bytes.NewReader(files), int64(len(files))); err != nil {
			return err
		}
		sent += int64(len(files))
	}
	if !o.asJSON {
		a.printUploaded(len(up.Uploads), len(bins), sent)
	}

	var started struct{ Deployment deployment }
	if err := api.do(ctx, "POST", "/v1/deployments/"+url.PathEscape(up.Deployment.ID)+"/start", nil, &started); err != nil {
		return friendly(err)
	}
	if !o.asJSON {
		fmt.Fprintf(a.Out, "\nDeployment %s queued:\n", started.Deployment.ID)
	}
	end, err := a.followLog(ctx, api, started.Deployment.ID, !o.asJSON)
	if err != nil {
		return err
	}
	if o.asJSON {
		return a.printJSON(map[string]any{"deployment": started.Deployment, "build": end, "services": found.Services, "release": found.Release, "skipped": found.Skipped})
	}
	vet.print(a)
	return a.printBuildEnd(end)
}

// buildEnv is the environment for go build and go vet: static Linux binaries without local paths, which is what
// the server checks for. The developer's own GOFLAGS (for example -mod=vendor) are kept. -buildvcs=false keeps
// git's commit and dirty flag out of the binaries, which would otherwise change every binary on every commit and
// defeat uploading only the services that changed.
func (a *App) buildEnv(goarch string) []string {
	flags := strings.TrimSpace(a.Getenv("GOFLAGS") + " -trimpath -buildvcs=false")
	return append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+goarch, "GOFLAGS="+flags)
}

// goVersion asks go which toolchain this module builds with; GOTOOLCHAIN may switch to the one go.mod names.
func (a *App) goVersion(ctx context.Context, root string, env []string) string {
	cmd := exec.CommandContext(ctx, a.GoTool, "env", "GOVERSION") //nolint:gosec // the go command found on PATH, fixed arguments
	cmd.Dir, cmd.Env = root, env
	out, err := cmd.Output()
	if err != nil {
		return "Go"
	}
	return strings.TrimSpace(string(out))
}

func (a *App) compile(ctx context.Context, root, dir string, env []string, found detect.Result) ([]localBinary, error) {
	var bins []localBinary
	for _, svc := range found.Services {
		b, err := a.compileOne(ctx, root, dir, env, svc.Name, svc.Path, svc.Type)
		if err != nil {
			return nil, err
		}
		bins = append(bins, b)
	}
	if found.Release != nil {
		b, err := a.compileOne(ctx, root, dir, env, "release", found.Release.Path, "release")
		if err != nil {
			return nil, err
		}
		bins = append(bins, b)
	}
	return bins, nil
}

func (a *App) compileOne(ctx context.Context, root, dir string, env []string, name, path, typ string) (localBinary, error) {
	out := filepath.Join(dir, name)
	cmd := exec.CommandContext(ctx, a.GoTool, "build", "-ldflags=-s -w", "-o", out, path) //nolint:gosec // the go command found on PATH; arguments are passed separately
	cmd.Dir, cmd.Env = root, env
	cmd.Stdout, cmd.Stderr = a.Err, a.Err
	if err := cmd.Run(); err != nil {
		return localBinary{}, fmt.Errorf("go build %s failed; fix the errors above and deploy again", path)
	}
	raw, err := os.ReadFile(out) //nolint:gosec // our own temporary build folder
	if err != nil {
		return localBinary{}, fmt.Errorf("read %s binary: %w", name, err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(raw); err != nil {
		return localBinary{}, fmt.Errorf("compress %s: %w", name, err)
	}
	if err := zw.Close(); err != nil {
		return localBinary{}, fmt.Errorf("compress %s: %w", name, err)
	}
	sum := sha256.Sum256(raw)
	return localBinary{
		Service: name, Path: path, Type: typ, SHA256: hex.EncodeToString(sum[:]),
		SizeBytes: int64(len(raw)), GzBytes: int64(gz.Len()), gz: gz.Bytes(),
	}, nil
}

func (a *App) printUploaded(sent, total int, size int64) {
	switch sent {
	case 0:
		fmt.Fprintln(a.Out, "No binaries changed; nothing to upload")
	case total:
		fmt.Fprintf(a.Out, "Uploaded %d binaries (%s)\n", sent, humanBytes(size))
	default:
		fmt.Fprintf(a.Out, "Uploaded %d of %d binaries (%s); the rest are unchanged\n", sent, total, humanBytes(size))
	}
}

// vetRun is go vet running beside the deploy. Its warnings are shown but never block or delay it (F-23).
type vetRun struct {
	cancel   context.CancelFunc
	done     chan struct{}
	warnings string
}

func (a *App) startVet(ctx context.Context, root string, env []string) *vetRun {
	ctx, cancel := context.WithCancel(ctx)
	v := &vetRun{cancel: cancel, done: make(chan struct{})}
	cmd := exec.CommandContext(ctx, a.GoTool, "vet", "./...") //nolint:gosec // the go command found on PATH, fixed arguments
	cmd.Dir, cmd.Env = root, env
	go func() {
		defer close(v.done)
		out, err := cmd.CombinedOutput()
		if err != nil && ctx.Err() == nil {
			v.warnings = strings.TrimSpace(string(out))
		}
	}()
	return v
}

// print shows vet's warnings if it has finished; a vet still running is not waited for.
func (v *vetRun) print(a *App) {
	select {
	case <-v.done:
	default:
		return
	}
	if v.warnings == "" {
		return
	}
	fmt.Fprintln(a.Out, "\ngo vet warnings (they don't block the deploy):")
	for _, line := range strings.Split(v.warnings, "\n") {
		fmt.Fprintf(a.Out, "  %s\n", line)
	}
}

func (v *vetRun) stop() {
	v.cancel()
	<-v.done
}
