package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/detect"
	"greyship.tech/internal/github"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
)

// GitHubDeploys connects repos to projects and turns pushes into deployments (spec F-22, decisions.md 042).
type GitHubDeploys interface {
	InstallURL(ctx context.Context, c project.Caller, team string) (string, error)
	Callback(ctx context.Context, c project.Caller, state, code string, installation int64) (string, error)
	Repos(ctx context.Context, c project.Caller, team string) ([]github.TeamRepo, error)
	Import(ctx context.Context, c project.Caller, team, repo, branch string, deploy bool) (project.Project, string, error)
	Connect(ctx context.Context, c project.Caller, projectID, repo, branch string) (store.ProjectRepo, error)
	Repo(ctx context.Context, c project.Caller, projectID string) (store.ProjectRepo, error)
	Push(ctx context.Context, team string, body []byte) (int, error)
	AppStatus(ctx context.Context, c project.Caller, team string) (github.AppInfo, error)
	StartApp(ctx context.Context, c project.Caller, team, org, name string) (github.AppStart, error)
	FinishApp(ctx context.Context, c project.Caller, state, code string) (string, error)
	DeleteApp(ctx context.Context, c project.Caller, team string) error
	WebhookSecret(ctx context.Context, team string) (string, error)
	ImportPublic(ctx context.Context, c project.Caller, team, link, branch string, deploy bool) (project.Project, string, error)
	DeployLatest(ctx context.Context, c project.Caller, projectID string) (string, error)
	Preview(ctx context.Context, c project.Caller, projectID string) (github.Preview, error)
}

// deployFlag is the import body's "deploy": true unless the caller says false (the dashboard previews first).
func deployFlag(v *bool) bool { return v == nil || *v }

type previewServiceJSON struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// githubPreview shows what deploying the connected branch would run, before anything is deployed.
func (s *Server) githubPreview(w http.ResponseWriter, r *http.Request) {
	pv, err := s.GitHub.Preview(r.Context(), callerFrom(r.Context()), r.PathValue("id"))
	var cfgErr *detect.ConfigError
	if errors.As(err, &cfgErr) {
		writeJSON(w, http.StatusOK, map[string]any{"branch": pv.Branch, "commit": pv.Commit, "problem": map[string]string{"message": cfgErr.Msg, "fix": cfgErr.Fix}})
		return
	}
	if err != nil {
		s.githubError(w, r, err)
		return
	}
	services := make([]previewServiceJSON, 0, len(pv.Detected.Services))
	for _, svc := range pv.Detected.Services {
		services = append(services, previewServiceJSON{Name: svc.Name, Type: svc.Type, Path: svc.Path, Reason: svc.Reason})
	}
	var release *previewServiceJSON
	if rel := pv.Detected.Release; rel != nil {
		release = &previewServiceJSON{Name: "release", Type: "release", Path: rel.Path, Reason: rel.Reason}
	}
	env := pv.EnvExample
	if env == nil {
		env = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"branch": pv.Branch, "commit": pv.Commit, "go_version": pv.Detected.GoVersion,
		"services": services, "release": release, "skipped": pv.Detected.Skipped, "env_example": env,
	})
}

type repoJSON struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Private       bool   `json:"private"`
}

type connectedJSON struct {
	FullName string `json:"full_name"`
	Branch   string `json:"branch"`
	Public   bool   `json:"public"` // connected by URL: deploys only on demand
}

func toConnectedJSON(r store.ProjectRepo) connectedJSON {
	return connectedJSON{FullName: r.FullName, Branch: r.Branch, Public: r.InstallationID == nil}
}

func (s *Server) githubImportURL(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL    string `json:"url"`
		Branch string `json:"branch"`
		Deploy *bool  `json:"deploy"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	p, dep, err := s.GitHub.ImportPublic(r.Context(), callerFrom(r.Context()), r.PathValue("team"), body.URL, body.Branch, deployFlag(body.Deploy))
	if err != nil {
		s.githubError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project": toProjectJSON(p), "deployment_id": dep})
}

func (s *Server) githubDeployLatest(w http.ResponseWriter, r *http.Request) {
	dep, err := s.GitHub.DeployLatest(r.Context(), callerFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		s.githubError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"deployment_id": dep})
}

func (s *Server) githubInstall(w http.ResponseWriter, r *http.Request) {
	u, err := s.GitHub.InstallURL(r.Context(), callerFrom(r.Context()), r.PathValue("team"))
	if err != nil {
		s.githubError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": u})
}

// githubCallback is where GitHub sends the person after they install the app. It's a browser page, so a
// missing session goes to sign in and comes back, and the outcome is a redirect to the dashboard.
func (s *Server) githubCallback(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(s.sessionName())
	var user store.User
	if err == nil {
		user, err = s.Auth.SessionUser(r.Context(), c.Value)
	}
	if err != nil {
		if errors.Is(err, http.ErrNoCookie) || errors.Is(err, auth.ErrNoSession) {
			http.Redirect(w, r, s.DashboardURL+"/signin?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
		s.internalError(w, r, err)
		return
	}
	q := r.URL.Query()
	installation, _ := strconv.ParseInt(q.Get("installation_id"), 10, 64)
	team, err := s.GitHub.Callback(r.Context(), project.Caller{UserID: user.ID}, q.Get("state"), q.Get("code"), installation)
	if err != nil {
		s.Logger.Warn("github install callback", "user", user.ID, "err", err)
		reason := "failed"
		switch {
		case errors.Is(err, github.ErrBadState):
			reason = "expired"
		case errors.Is(err, github.ErrNoAccess):
			reason = "no_access"
		}
		http.Redirect(w, r, s.DashboardURL+"/continue?github_error="+reason, http.StatusFound)
		return
	}
	http.Redirect(w, r, s.DashboardURL+"/"+url.PathEscape(team)+"/new", http.StatusFound)
}

func (s *Server) githubRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.GitHub.Repos(r.Context(), callerFrom(r.Context()), r.PathValue("team"))
	if err != nil {
		s.githubError(w, r, err)
		return
	}
	out := make([]repoJSON, 0, len(repos))
	for _, repo := range repos {
		out = append(out, repoJSON{FullName: repo.FullName, DefaultBranch: repo.DefaultBranch, Private: repo.Private})
	}
	writeJSON(w, http.StatusOK, map[string][]repoJSON{"repos": out})
}

func (s *Server) githubImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Repo   string `json:"repo"`
		Branch string `json:"branch"`
		Deploy *bool  `json:"deploy"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	p, dep, err := s.GitHub.Import(r.Context(), callerFrom(r.Context()), r.PathValue("team"), body.Repo, body.Branch, deployFlag(body.Deploy))
	if err != nil {
		s.githubError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project": toProjectJSON(p), "deployment_id": dep})
}

func (s *Server) githubConnect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Repo   string `json:"repo"`
		Branch string `json:"branch"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	repo, err := s.GitHub.Connect(r.Context(), callerFrom(r.Context()), r.PathValue("id"), body.Repo, body.Branch)
	if err != nil {
		s.githubError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]connectedJSON{"repo": toConnectedJSON(repo)})
}

func (s *Server) githubProjectRepo(w http.ResponseWriter, r *http.Request) {
	repo, err := s.GitHub.Repo(r.Context(), callerFrom(r.Context()), r.PathValue("id"))
	if errors.Is(err, github.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"repo": nil})
		return
	}
	if err != nil {
		s.githubError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]connectedJSON{"repo": toConnectedJSON(repo)})
}

// githubWebhook takes GitHub's deliveries. Nothing is trusted before the signature checks out.
func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 25<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Couldn't read the delivery.")
		return
	}
	team := r.PathValue("team")
	secret, err := s.GitHub.WebhookSecret(r.Context(), team)
	if errors.Is(err, github.ErrOff) {
		writeError(w, http.StatusNotFound, "github_off", "This team has no GitHub App yet.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !github.ValidSignature(secret, body, r.Header.Get("X-Hub-Signature-256")) {
		writeError(w, http.StatusUnauthorized, "invalid_signature", "The delivery isn't signed with the webhook secret.")
		return
	}
	if r.Header.Get("X-GitHub-Event") != "push" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	n, err := s.GitHub.Push(r.Context(), team, body)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int{"deployments": n})
}

func (s *Server) githubError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, github.ErrOff):
		writeError(w, http.StatusNotFound, "github_off", "Create your GitHub App first: it is a button on New project and in Settings.")
	case errors.Is(err, github.ErrNotInstalled):
		writeError(w, http.StatusConflict, "github_not_installed", "Install the Greyship GitHub app for this team first.")
	case errors.Is(err, github.ErrUnknownRepo):
		writeError(w, http.StatusNotFound, "unknown_repo", err.Error())
	case errors.Is(err, github.ErrNoAccess):
		writeError(w, http.StatusForbidden, "github_no_access", err.Error())
	case errors.Is(err, github.ErrBadURL):
		writeError(w, http.StatusBadRequest, "bad_url", err.Error())
	case errors.Is(err, github.ErrNotPublic):
		writeError(w, http.StatusBadRequest, "not_public", err.Error())
	case errors.Is(err, github.ErrUnknownBranch):
		writeError(w, http.StatusNotFound, "unknown_branch", err.Error())
	case errors.Is(err, github.ErrAlreadyDeployed):
		writeError(w, http.StatusConflict, "already_deployed", err.Error())
	case errors.Is(err, github.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_connected", "This project isn't connected to a GitHub repository.")
	default:
		s.projectError(w, r, err)
	}
}
