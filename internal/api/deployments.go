package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"greyship.tech/internal/deploy"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
)

// Deployments is the slice of deploy.Service the handlers use; tests swap in a fake.
type Deployments interface {
	Create(ctx context.Context, c project.Caller, projectID string, src deploy.Source) (deploy.Upload, error)
	Start(ctx context.Context, c project.Caller, id string) (deploy.Deployment, error)
	Get(ctx context.Context, c project.Caller, id string) (deploy.Deployment, error)
	List(ctx context.Context, c project.Caller, projectID string, limit int32) ([]deploy.Deployment, error)
	Logs(ctx context.Context, c project.Caller, id string, after, limit int32) (deploy.Deployment, []store.BuildLog, []store.Artifact, error)
	Instances(ctx context.Context, c project.Caller, id string) ([]store.ListDeploymentInstancesRow, error)
	URLs(ctx context.Context, c project.Caller, id string) ([]store.ServiceURLsRow, error)
	AppLogs(ctx context.Context, c project.Caller, projectID, service string, after int64, tail int32) ([]store.AppLogsAfterRow, error)
	BuildsAhead(ctx context.Context, c project.Caller, id string) (int64, error)
	Services(ctx context.Context, c project.Caller, projectID string) ([]store.LiveServiceStatesRow, error)
	Redeploy(ctx context.Context, c project.Caller, id string) (deploy.Deployment, error)
	CreateLocal(ctx context.Context, c project.Caller, projectID, commitSHA string, bins []deploy.Binary, files *deploy.Files) (deploy.LocalUpload, error)
}

type binaryJSON struct {
	Service   string `json:"service"`
	Path      string `json:"path"`
	Type      string `json:"type"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	GzBytes   int64  `json:"gz_bytes"`
}

// buildTarget tells the CLI what to compile for when it builds locally (decisions.md 034).
func (s *Server) buildTarget(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"goos": "linux", "goarch": s.BuildGOARCH})
}

type urlJSON struct {
	Service string `json:"service"`
	URL     string `json:"url"`
}

// appURL is a service's public address (F-50): https://<label>.<apps domain>, with the port in development.
type serviceJSON struct {
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	Path         string  `json:"path"`
	URL          *string `json:"url"`
	State        string  `json:"state"` // running | starting | sleeping
	Instances    int32   `json:"instances"`
	DeploymentID string  `json:"deployment_id"`
	ReleasedAt   *string `json:"released_at"`
	MemoryMB     int32   `json:"memory_mb"`
	MaxInstances int32   `json:"max_instances"`
	Concurrency  int32   `json:"concurrency"`
	GraceSeconds int32   `json:"grace_seconds"`
}

// listServices is the project overview: what the live deployment runs and whether it's awake. Paused and stopped
// instances both read as sleeping, since either wakes on the next request.
func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Deployments.Services(r.Context(), callerFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		s.deploymentError(w, r, err)
		return
	}
	out := make([]serviceJSON, 0, len(rows))
	for _, row := range rows {
		svc := serviceJSON{
			Name: row.Name, Type: row.Type, Path: row.Path, State: "sleeping", Instances: row.Running, DeploymentID: row.DeploymentID,
			MemoryMB: row.MemoryMb, MaxInstances: row.MaxInstances, Concurrency: row.Concurrency, GraceSeconds: row.GraceSeconds,
		}
		switch {
		case row.Running > 0:
			svc.State = "running"
		case row.Starting > 0:
			svc.State = "starting"
		}
		if row.Subdomain != nil {
			u := s.appURL(*row.Subdomain)
			svc.URL = &u
		}
		if row.ReleasedAt != nil {
			t := row.ReleasedAt.UTC().Format(time.RFC3339)
			svc.ReleasedAt = &t
		}
		out = append(out, svc)
	}
	writeJSON(w, http.StatusOK, map[string][]serviceJSON{"services": out})
}

func (s *Server) appURL(label string) string {
	host := label + "." + s.AppsDomain
	scheme, std := "https", "443"
	if s.HTTPOnly {
		scheme, std = "http", "80"
	}
	if s.AppsPort != "" && s.AppsPort != std {
		host += ":" + s.AppsPort
	}
	return scheme + "://" + host
}

func (s *Server) urls(r *http.Request, id string) ([]urlJSON, error) {
	rows, err := s.Deployments.URLs(r.Context(), callerFrom(r.Context()), id)
	if err != nil {
		return nil, err
	}
	out := make([]urlJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, urlJSON{Service: row.Name, URL: s.appURL(row.Subdomain)})
	}
	return out, nil
}

// instanceJSON leaves out the node-internal address: it is for the router, not for clients.
type instanceJSON struct {
	ID      string  `json:"id"`
	Service string  `json:"service"`
	Node    string  `json:"node"`
	State   string  `json:"state"`
	Error   *string `json:"error"`
}

type deploymentJSON struct {
	ID         string  `json:"id"`
	Project    string  `json:"project_id"`
	Team       string  `json:"team"`
	Slug       string  `json:"project"`
	Source     string  `json:"source"`
	Status     string  `json:"status"`
	Error      *string `json:"error"`
	Env        string  `json:"env"`
	CommitSHA  *string `json:"commit_sha"`
	BuildMode  string  `json:"build_mode"`  // local (built by the CLI) or remote (built by Greyship)
	RollbackOf *string `json:"rollback_of"` // a redeploy or rollback: the deployment whose binaries it runs
	SourceSize *int64  `json:"source_size,omitempty"`
	CreatedAt  string  `json:"created_at"`
	FinishedAt *string `json:"finished_at"`
}

func toDeploymentJSON(d deploy.Deployment) deploymentJSON {
	out := deploymentJSON{
		ID: d.ID, Project: d.ProjectID, Team: d.Project.TeamSlug, Slug: d.Project.Slug,
		Source: d.Source, Status: d.Status, Error: d.Error, Env: d.Env, CommitSHA: d.CommitSha, BuildMode: d.BuildMode, SourceSize: d.SourceSize, RollbackOf: d.RollbackOf,
		CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
	}
	if d.FinishedAt != nil {
		f := d.FinishedAt.UTC().Format(time.RFC3339)
		out.FinishedAt = &f
	}
	return out
}

func (s *Server) createDeployment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SizeBytes int64        `json:"size_bytes"`
		SHA256    string       `json:"sha256"`
		CommitSHA string       `json:"commit_sha"`
		Binaries  []binaryJSON `json:"binaries"` // a local build: compiled binaries instead of source
		Files     *struct {
			SHA256    string `json:"sha256"`
			SizeBytes int64  `json:"size_bytes"`
		} `json:"files"` // a local build's files bundle (greyship.toml's files)
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Binaries != nil {
		var files *deploy.Files
		if body.Files != nil {
			files = &deploy.Files{SHA256: body.Files.SHA256, SizeBytes: body.Files.SizeBytes}
		}
		s.createLocalDeployment(w, r, body.CommitSHA, body.Binaries, files)
		return
	}
	up, err := s.Deployments.Create(r.Context(), callerFrom(r.Context()), r.PathValue("id"), deploy.Source{Size: body.SizeBytes, SHA256: body.SHA256, CommitSHA: body.CommitSHA})
	if err != nil {
		s.deploymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"deployment": toDeploymentJSON(up.Deployment),
		"upload_url": up.URL,
		"expires_at": up.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) createLocalDeployment(w http.ResponseWriter, r *http.Request, commit string, in []binaryJSON, files *deploy.Files) {
	bins := make([]deploy.Binary, 0, len(in))
	for _, b := range in {
		bins = append(bins, deploy.Binary{Service: b.Service, Path: b.Path, Type: b.Type, SHA256: b.SHA256, SizeBytes: b.SizeBytes, GzBytes: b.GzBytes})
	}
	up, err := s.Deployments.CreateLocal(r.Context(), callerFrom(r.Context()), r.PathValue("id"), commit, bins, files)
	if err != nil {
		s.deploymentError(w, r, err)
		return
	}
	uploads := make([]map[string]string, 0, len(up.Uploads))
	for _, u := range up.Uploads {
		uploads = append(uploads, map[string]string{"service": u.Service, "upload_url": u.URL})
	}
	out := map[string]any{"deployment": toDeploymentJSON(up.Deployment), "uploads": uploads}
	if up.FilesURL != "" {
		out["files_upload_url"] = up.FilesURL
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) redeployDeployment(w http.ResponseWriter, r *http.Request) {
	d, err := s.Deployments.Redeploy(r.Context(), callerFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		s.deploymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]deploymentJSON{"deployment": toDeploymentJSON(d)})
}

func (s *Server) startDeployment(w http.ResponseWriter, r *http.Request) {
	d, err := s.Deployments.Start(r.Context(), callerFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		s.deploymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]deploymentJSON{"deployment": toDeploymentJSON(d)})
}

func (s *Server) getDeployment(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r.Context())
	d, err := s.Deployments.Get(r.Context(), c, r.PathValue("id"))
	if err != nil {
		s.deploymentError(w, r, err)
		return
	}
	rows, err := s.Deployments.Instances(r.Context(), c, d.ID)
	if err != nil {
		s.deploymentError(w, r, err)
		return
	}
	instances := make([]instanceJSON, 0, len(rows))
	for _, i := range rows {
		instances = append(instances, instanceJSON{ID: i.ID, Service: i.ServiceName, Node: i.NodeName, State: i.State, Error: i.Error})
	}
	urls, err := s.urls(r, d.ID)
	if err != nil {
		s.deploymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployment": toDeploymentJSON(d), "instances": instances, "urls": urls})
}

func (s *Server) listDeployments(w http.ResponseWriter, r *http.Request) {
	limit := int32(20)
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a number from 1 to 100.")
			return
		}
		limit = int32(n) //nolint:gosec // bounded to 1..100 above
	}
	ds, err := s.Deployments.List(r.Context(), callerFrom(r.Context()), r.PathValue("id"), limit)
	if err != nil {
		s.deploymentError(w, r, err)
		return
	}
	out := make([]deploymentJSON, 0, len(ds))
	for _, d := range ds {
		out = append(out, toDeploymentJSON(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployments": out})
}

func (s *Server) deploymentError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, deploy.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "source_too_large", "The source is larger than 100 MB compressed. Add big files to .greyshipignore.")
	case errors.Is(err, deploy.ErrNotDeployable):
		writeError(w, http.StatusConflict, "not_deployable", err.Error())
	case errors.Is(err, deploy.ErrBadBinary):
		writeError(w, http.StatusBadRequest, "invalid_binaries", err.Error())
	case errors.Is(err, deploy.ErrBadSource):
		writeError(w, http.StatusBadRequest, "invalid_source", "size_bytes must be positive, sha256 64 lowercase hex characters and commit_sha 40.")
	case errors.Is(err, deploy.ErrUploadMissing):
		writeError(w, http.StatusConflict, "upload_missing", "The source upload hasn't arrived or is incomplete. Upload it to upload_url, then start again.")
	case errors.Is(err, deploy.ErrNotUploading):
		writeError(w, http.StatusConflict, "already_started", "This deployment has already been started.")
	case errors.Is(err, deploy.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such deployment.")
	default:
		s.projectError(w, r, err)
	}
}

type logLineJSON struct {
	Seq    int32  `json:"seq"`
	Stream string `json:"stream"`
	Line   string `json:"line"`
	Time   string `json:"ts"`
}

type artifactJSON struct {
	Service string `json:"service"`
	Path    string `json:"path"`
	Type    string `json:"type"`
	Size    int64  `json:"size_bytes"`
	SHA256  string `json:"sha256"`
}

type logEndJSON struct {
	Status    string         `json:"status"`
	Error     *string        `json:"error"`
	Artifacts []artifactJSON `json:"artifacts"`
	URLs      []urlJSON      `json:"urls"`
	Build     *buildTimeJSON `json:"build,omitempty"`
}

// buildTimeJSON is how long a server build took.
type buildTimeJSON struct {
	Seconds int32 `json:"seconds"`
}

const (
	logPoll     = 500 * time.Millisecond
	logPing     = 15 * time.Second
	logPageSize = 500
)

// deploymentLogs streams the build log as newline-delimited JSON: {"line":…} objects, {"ping":true} while idle,
// and a final {"end":{status, error, artifacts}} once the build is over. Without follow=1 it returns what exists and ends.
func (s *Server) deploymentLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	after, err := strconv.Atoi(r.URL.Query().Get("after"))
	if err != nil && r.URL.Query().Get("after") != "" || after < 0 || after > 1<<30 {
		writeError(w, http.StatusBadRequest, "invalid_after", "after must be a log sequence number.")
		return
	}
	follow := r.URL.Query().Get("follow") == "1"
	seq := int32(after) //nolint:gosec // bounded above

	d, lines, artifacts, err := s.Deployments.Logs(ctx, callerFrom(ctx), r.PathValue("id"), seq, logPageSize)
	if err != nil {
		s.deploymentError(w, r, err)
		return
	}
	// Builds take minutes; the server-wide write timeout must not cut the stream.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)

	lastWrite := time.Now()
	ahead := int64(-1)
	for {
		// While the deployment waits for a builder, say how many builds are ahead of it, whenever that changes.
		if d.Status == "queued" {
			if n, err := s.Deployments.BuildsAhead(ctx, callerFrom(ctx), d.ID); err == nil && n != ahead {
				_ = enc.Encode(map[string]map[string]int64{"queue": {"ahead": n}})
				ahead, lastWrite = n, time.Now()
			}
		}
		for _, l := range lines {
			_ = enc.Encode(map[string]logLineJSON{"line": {Seq: l.Seq, Stream: l.Stream, Line: l.Line, Time: l.Ts.UTC().Format(time.RFC3339Nano)}})
			seq = l.Seq
		}
		if len(lines) > 0 {
			lastWrite = time.Now()
		}
		if len(lines) < logPageSize && (deploy.Finished(d.Status) || !follow) {
			if deploy.Finished(d.Status) {
				end := logEndJSON{Status: d.Status, Error: d.Error, Artifacts: []artifactJSON{}, URLs: []urlJSON{}}
				if d.Status == "ready" {
					if end.URLs, err = s.urls(r, d.ID); err != nil {
						s.Logger.Error("list deployment URLs", "deployment", d.ID, "err", err)
						end.URLs = []urlJSON{}
					}
				}
				if d.BuildSeconds != nil {
					end.Build = &buildTimeJSON{Seconds: *d.BuildSeconds}
				}
				for _, a := range artifacts {
					end.Artifacts = append(end.Artifacts, artifactJSON{Service: a.ServiceName, Path: a.ServicePath, Type: a.ServiceType, Size: a.SizeBytes, SHA256: a.Sha256})
				}
				_ = enc.Encode(map[string]logEndJSON{"end": end})
			}
			_ = rc.Flush()
			return
		}
		if time.Since(lastWrite) >= logPing {
			_ = enc.Encode(map[string]bool{"ping": true})
			lastWrite = time.Now()
		}
		if err := rc.Flush(); err != nil {
			return
		}
		if len(lines) < logPageSize {
			select {
			case <-ctx.Done():
				return
			case <-time.After(logPoll):
			}
		}
		if d, lines, artifacts, err = s.Deployments.Logs(ctx, callerFrom(ctx), d.ID, seq, logPageSize); err != nil {
			if ctx.Err() == nil {
				s.Logger.Error("stream build log", "deployment", r.PathValue("id"), "err", err)
			}
			return
		}
	}
}
