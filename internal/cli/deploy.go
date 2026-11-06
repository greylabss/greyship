package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"greyship.tech/internal/detect"
	"greyship.tech/internal/pack"
)

type deployment struct {
	ID         string  `json:"id"`
	ProjectID  string  `json:"project_id"`
	Team       string  `json:"team"`
	Project    string  `json:"project"`
	Status     string  `json:"status"`
	CommitSHA  *string `json:"commit_sha"`
	SourceSize int64   `json:"source_size,omitempty"`
	BuildMode  string  `json:"build_mode"`
	RollbackOf *string `json:"rollback_of,omitempty"`
	CreatedAt  string  `json:"created_at"`
}

func deployCmd(a *App, asJSON *bool) *cobra.Command {
	var yes, prune, remote bool
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Build this folder and deploy it",
		Long: "Builds the Go module in this folder and deploys it. With Go installed, the services are compiled here for Linux " +
			"and only changed binaries are uploaded. Without Go, or with --remote, the source is packed (honouring .gitignore and " +
			".greyshipignore; .env files and .git are never uploaded) and built on Greyship. Without grey init, the project is " +
			"created from the go.mod name.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.deploy(cmd.Context(), *asJSON, yes, prune, remote)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "apply .env.prod changes without asking (for CI)")
	cmd.Flags().BoolVar(&prune, "prune", false, "remove stored variables that aren't in .env.prod")
	cmd.Flags().BoolVar(&remote, "remote", false, "upload the source and build on Greyship, even with Go installed")
	return cmd
}

func (a *App) deploy(ctx context.Context, asJSON, yes, prune, remote bool) error {
	s, err := a.session()
	if err != nil {
		return err
	}
	root := moduleRoot(a.Dir)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return errors.New("no go.mod here or in any parent folder; run grey deploy inside a Go module")
	}
	in := bufio.NewReader(a.In)
	l, created, err := a.ensureProject(ctx, s.api, in)
	if err != nil {
		return err
	}

	// The builder detects again from the uploaded source; this run shows the result before anything is sent.
	found, err := detect.Detect(os.DirFS(root), l.Project)
	if err != nil {
		return err
	}
	if len(found.Services) == 0 {
		return errors.New("no services found: add a package main (for example cmd/api/main.go) and deploy again")
	}
	if !remote && a.GoTool != "" {
		return a.deployLocal(ctx, s.api, l, created, found, root, localOptions{asJSON: asJSON, yes: yes, prune: prune, in: in})
	}

	tarball, err := os.CreateTemp("", "grey-source-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	defer func() {
		_ = tarball.Close()
		_ = os.Remove(tarball.Name())
	}()
	sum := sha256.New()
	res, err := pack.Write(root, io.MultiWriter(tarball, sum))
	var tooLarge *pack.ErrTooLarge
	if errors.As(err, &tooLarge) {
		return tooLargeError(tooLarge)
	}
	if err != nil {
		return err
	}
	size, err := tarball.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("measure tarball: %w", err)
	}

	if !asJSON {
		a.printPlan(l, created, found)
		a.printPacked(res, size, remote)
	}
	// Variables are settled before the build, so the new instances start with them (F-40a).
	if err := a.syncDeployEnv(ctx, s.api, l, root, created, yes, prune, in); err != nil {
		return err
	}

	var up struct {
		Deployment deployment `json:"deployment"`
		UploadURL  string     `json:"upload_url"`
	}
	body := map[string]any{"size_bytes": size, "sha256": hex.EncodeToString(sum.Sum(nil))}
	if sha := commitSHA(ctx, root); sha != "" {
		body["commit_sha"] = sha
	}
	if err := s.api.do(ctx, "POST", "/v1/projects/"+url.PathEscape(l.ID)+"/deployments", body, &up); err != nil {
		return linkedElsewhere(err, l)
	}
	if err := a.upload(ctx, up.UploadURL, tarball, size); err != nil {
		return err
	}
	var started struct{ Deployment deployment }
	if err := s.api.do(ctx, "POST", "/v1/deployments/"+url.PathEscape(up.Deployment.ID)+"/start", nil, &started); err != nil {
		return friendly(err)
	}

	if !asJSON {
		fmt.Fprintf(a.Out, "\nDeployment %s queued:\n", started.Deployment.ID)
	}
	end, err := a.followLog(ctx, s.api, started.Deployment.ID, !asJSON)
	if err != nil {
		return err
	}
	if asJSON {
		return a.printJSON(map[string]any{"deployment": started.Deployment, "build": end, "services": found.Services, "release": found.Release, "skipped": found.Skipped})
	}
	return a.printBuildEnd(end)
}

type logEnd struct {
	Status    string  `json:"status"`
	Error     *string `json:"error"`
	Artifacts []struct {
		Service string `json:"service"`
		Type    string `json:"type"`
		Size    int64  `json:"size_bytes"`
	} `json:"artifacts"`
	URLs []struct {
		Service string `json:"service"`
		URL     string `json:"url"`
	} `json:"urls"`
	Build *struct {
		Seconds         int `json:"seconds"`
		UsedMinutes     int `json:"used_minutes"`
		IncludedMinutes int `json:"included_minutes"`
	} `json:"build,omitempty"`
}

// followLog prints the build log as it streams and returns how the build ended. A dropped connection resumes
// from the last line seen; the build itself keeps going on the server either way.
func (a *App) followLog(ctx context.Context, api *client, id string, print bool) (logEnd, error) {
	var after int32
	for attempt := 0; ; attempt++ {
		end, last, err := a.readLog(ctx, api, id, after, print)
		if end != nil {
			return *end, nil
		}
		if ctx.Err() != nil {
			return logEnd{}, ctx.Err()
		}
		if last > after {
			after, attempt = last, 0
		}
		if attempt >= 5 {
			return logEnd{}, fmt.Errorf("lost the build log (%w); the build continues, check it with grey deploy --json later", err)
		}
		if err := a.Sleep(ctx, time.Duration(attempt+1)*time.Second); err != nil {
			return logEnd{}, err
		}
	}
}

func (a *App) readLog(ctx context.Context, api *client, id string, after int32, print bool) (*logEnd, int32, error) {
	path := fmt.Sprintf("/v1/deployments/%s/logs?follow=1&after=%d", url.PathEscape(id), after)
	resp, err := api.stream(ctx, path)
	if err != nil {
		return nil, after, friendly(err)
	}
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var ev struct {
			Line *struct {
				Seq    int32  `json:"seq"`
				Stream string `json:"stream"`
				Line   string `json:"line"`
			} `json:"line"`
			End   *logEnd `json:"end"`
			Queue *struct {
				Ahead int `json:"ahead"`
			} `json:"queue"`
		}
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return nil, after, fmt.Errorf("read build log: %w", err)
		}
		switch {
		case ev.End != nil:
			return ev.End, after, nil
		case ev.Queue != nil && print:
			switch ev.Queue.Ahead {
			case 0:
				fmt.Fprintln(a.Out, "  │ your build is next")
			case 1:
				fmt.Fprintln(a.Out, "  │ waiting: 1 build ahead of yours")
			default:
				fmt.Fprintf(a.Out, "  │ waiting: %d builds ahead of yours\n", ev.Queue.Ahead)
			}
		case ev.Line != nil:
			after = ev.Line.Seq
			if print {
				fmt.Fprintf(a.Out, "  │ %s\n", ev.Line.Line)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, after, fmt.Errorf("read build log: %w", err)
	}
	return nil, after, errors.New("build log stream ended early")
}

// printBuildEnd reports how the deployment ended: live, or failed in its build or its release.
func (a *App) printBuildEnd(end logEnd) error {
	if end.Status != "ready" {
		reason := "see the log above"
		if end.Error != nil && *end.Error != "" {
			reason = *end.Error
		}
		return fmt.Errorf("deploy failed: %s", reason)
	}
	urls := map[string]string{}
	for _, u := range end.URLs {
		urls[u.Service] = u.URL
	}
	if b := end.Build; b != nil {
		if b.IncludedMinutes == 0 { // 0 is unlimited: an instance without plans
			fmt.Fprintf(a.Out, "\nbuild: %ds\n", b.Seconds)
		} else {
			fmt.Fprintf(a.Out, "\nbuild: %ds · %d/%d min this month\n", b.Seconds, b.UsedMinutes, b.IncludedMinutes)
		}
	}
	fmt.Fprintln(a.Out, "\n✓ Live")
	tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	for _, art := range end.Artifacts {
		if art.Type == "release" {
			continue
		}
		where := urls[art.Service]
		if where == "" {
			where = "(" + art.Type + ", no URL)"
		}
		fmt.Fprintf(tw, "  %s\t%s\n", art.Service, where)
	}
	return tw.Flush()
}

func (a *App) printPlan(l Link, created bool, found detect.Result) {
	verb := "Deploying to"
	if created {
		verb = "Created project"
	}
	fmt.Fprintf(a.Out, "%s %s/%s\n\n", verb, l.Team, l.Project)
	tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	for _, svc := range found.Services {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", svc.Name, svc.Type, svc.Path, svc.Reason)
	}
	if found.Release != nil {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", "release", "once", found.Release.Path, found.Release.Reason)
	}
	_ = tw.Flush()
	for _, sk := range found.Skipped {
		fmt.Fprintf(a.Out, "  skipped %s: %s\n", sk.Path, sk.Reason)
	}
	for _, w := range found.Warnings {
		fmt.Fprintf(a.Out, "  warning: %s\n", w)
	}
}

func (a *App) printPacked(res pack.Result, size int64, remote bool) {
	if !remote {
		fmt.Fprintln(a.Out, "\nGo isn't installed here, so Greyship builds it (install Go for faster deploys)")
	}
	fmt.Fprintf(a.Out, "\nPacked %d files, %s (%s compressed)\n", res.Files, humanBytes(res.Bytes), humanBytes(size))
	if len(res.Symlinks) > 0 {
		fmt.Fprintf(a.Out, "  left out %d symlink(s): %s\n", len(res.Symlinks), strings.Join(res.Symlinks, ", "))
	}
}

func (a *App) upload(ctx context.Context, to string, f *os.File, size int64) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind tarball: %w", err)
	}
	return a.put(ctx, to, "source", f, size)
}

// put sends one object to a presigned URL. It has no overall timeout (big uploads on slow links), only the context.
func (a *App) put(ctx context.Context, to, what string, body io.Reader, size int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, to, io.NopCloser(body))
	if err != nil {
		return fmt.Errorf("build upload request: %w", err)
	}
	req.ContentLength = size
	client := &http.Client{Transport: a.HTTP.Transport}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload %s: %w", what, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return fmt.Errorf("upload %s: storage answered %d: %s", what, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// linkedElsewhere explains a 404 for a linked project: the link file names a project this token can't see,
// usually one in another team or on another Greyship.
func linkedElsewhere(err error, l Link) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return fmt.Errorf("this folder is linked to %s/%s (%s), which your login can't see; it may be in another team. Delete %s to deploy it as a new project in your team", l.Team, l.Project, linkPath, linkPath)
	}
	return friendly(err)
}

func tooLargeError(e *pack.ErrTooLarge) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s. The biggest files are:\n", e.Error())
	for _, f := range e.Largest {
		fmt.Fprintf(&b, "  %8s  %s\n", humanBytes(f.Size), f.Path)
	}
	b.WriteString("Add the ones you don't need to build to .greyshipignore and deploy again.")
	return errors.New(b.String())
}

// commitSHA is HEAD when root is in a git repository, for the deployment record. Errors just mean no commit.
func commitSHA(ctx context.Context, root string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD").Output() //nolint:gosec // fixed binary and arguments; root is the local module folder
	if err != nil {
		return ""
	}
	sha := strings.TrimSpace(string(out))
	if len(sha) != 40 {
		return ""
	}
	return sha
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
