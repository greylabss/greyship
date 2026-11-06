package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"greyship.tech/internal/project"
)

// Projects is the slice of project.Service the handlers use; tests swap in a fake.
type Projects interface {
	Create(ctx context.Context, c project.Caller, teamSlug, name string) (project.Project, error)
	List(ctx context.Context, c project.Caller, teamSlug string) ([]project.Project, error)
	Get(ctx context.Context, c project.Caller, id string) (project.Project, error)
	BySlug(ctx context.Context, c project.Caller, teamSlug, slug string) (project.Project, error)
	Rename(ctx context.Context, c project.Caller, id, name string) (project.Project, error)
}

type projectJSON struct {
	ID        string `json:"id"`
	Team      string `json:"team"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	CreatedAt string `json:"created_at"`
}

func toProjectJSON(p project.Project) projectJSON {
	return projectJSON{ID: p.ID, Team: p.TeamSlug, Name: p.Name, Slug: p.Slug, CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339)}
}

func callerFrom(ctx context.Context) project.Caller {
	c := project.Caller{UserID: userFrom(ctx).ID}
	if tok, ok := tokenFrom(ctx); ok {
		c.TokenTeamID = tok.TeamID
	}
	return c
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Projects.List(r.Context(), callerFrom(r.Context()), r.PathValue("team"))
	if err != nil {
		s.projectError(w, r, err)
		return
	}
	out := make([]projectJSON, 0, len(ps))
	for _, p := range ps {
		out = append(out, toProjectJSON(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	p, err := s.Projects.Create(r.Context(), callerFrom(r.Context()), r.PathValue("team"), body.Name)
	if err != nil {
		s.projectError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]projectJSON{"project": toProjectJSON(p)})
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.Projects.Get(r.Context(), callerFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		s.projectError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]projectJSON{"project": toProjectJSON(p)})
}

func (s *Server) projectBySlug(w http.ResponseWriter, r *http.Request) {
	p, err := s.Projects.BySlug(r.Context(), callerFrom(r.Context()), r.PathValue("team"), r.PathValue("slug"))
	if err != nil {
		s.projectError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]projectJSON{"project": toProjectJSON(p)})
}

func (s *Server) renameProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	p, err := s.Projects.Rename(r.Context(), callerFrom(r.Context()), r.PathValue("id"), body.Name)
	if err != nil {
		s.projectError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]projectJSON{"project": toProjectJSON(p)})
}

func (s *Server) projectError(w http.ResponseWriter, r *http.Request, err error) {
	var taken *project.TakenError
	var nameErr *project.NameError
	switch {
	case errors.As(err, &taken):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   apiError{Code: "slug_taken", Message: "A project named " + taken.Existing.Slug + " already exists in this team."},
			"project": toProjectJSON(taken.Existing),
		})
	case errors.As(err, &nameErr):
		writeError(w, http.StatusBadRequest, "invalid_name", "That project name can't be used: "+nameErr.Reason+".")
	case errors.Is(err, project.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such project or team.")
	case errors.Is(err, project.ErrWrongTeam):
		writeError(w, http.StatusForbidden, "wrong_team", "This token belongs to a different team. Create a token for this team or run grey login again.")
	case errors.Is(err, project.ErrReadOnly):
		writeError(w, http.StatusForbidden, "read_only", "Viewers can't change projects.")
	default:
		s.internalError(w, r, err)
	}
}
