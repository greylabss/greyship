package api

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"greyship.tech/internal/github"
)

// githubAppWarnings are things that would stop pushes from deploying: GitHub can only deliver webhooks to a public
// address.
func (s *Server) githubAppWarnings() []string {
	u, err := url.Parse(s.DashboardURL)
	if err != nil {
		return []string{}
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || (ip != nil && (ip.IsLoopback() || ip.IsPrivate())) {
		return []string{"This dashboard's address (" + host + ") isn't reachable from the internet, so GitHub can't tell Greyship about pushes. Importing a repository and Deploy latest still work; push-to-deploy needs a public address."}
	}
	return []string{}
}

// githubApp shows the team's GitHub App, if it has one.
func (s *Server) githubApp(w http.ResponseWriter, r *http.Request) {
	info, err := s.GitHub.AppStatus(r.Context(), callerFrom(r.Context()), r.PathValue("team"))
	if err != nil {
		s.githubError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": info.Configured, "name": info.Name, "slug": info.Slug, "html_url": info.HTMLURL,
		"webhook_url": info.WebhookURL, "default_name": info.DefaultName, "warnings": s.githubAppWarnings(),
	})
}

// githubAppManifest starts creating the team's app: the manifest and the GitHub page to post it to. The dashboard
// posts it from the person's own browser.
func (s *Server) githubAppManifest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Org  string `json:"org"`
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	start, err := s.GitHub.StartApp(r.Context(), callerFrom(r.Context()), r.PathValue("team"), body.Org, body.Name)
	if errors.Is(err, github.ErrBadInput) {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error()+".")
		return
	}
	if err != nil {
		s.githubError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"action": start.Action, "manifest": start.Manifest})
}

func (s *Server) githubAppDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.GitHub.DeleteApp(r.Context(), callerFrom(r.Context()), r.PathValue("team")); err != nil {
		s.githubError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// githubManifestCallback is where GitHub sends the browser after the person confirmed. It's a browser page, so the
// outcome is a redirect to the dashboard.
func (s *Server) githubManifestCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	team, err := s.GitHub.FinishApp(r.Context(), callerFrom(r.Context()), q.Get("state"), q.Get("code"))
	if err != nil {
		s.Logger.Warn("github app creation", "user", userFrom(r.Context()).ID, "err", err)
		reason := "failed"
		switch {
		case errors.Is(err, github.ErrBadState):
			reason = "expired"
		case errors.Is(err, github.ErrRefused):
			reason = "github_refused"
		}
		dest := "/continue"
		if team != "" {
			dest = "/" + url.PathEscape(team) + "/new"
		}
		http.Redirect(w, r, s.DashboardURL+dest+"?github_app_error="+reason, http.StatusFound)
		return
	}
	http.Redirect(w, r, s.DashboardURL+"/"+url.PathEscape(team)+"/new?github_app=created", http.StatusFound)
}
