package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"greyship.tech/internal/admin"
	"greyship.tech/internal/store"
)

// Admin is the operator's read-only view across every team (decisions.md 048). Its JSON mirrors the database
// rows, field names as in the store package, because only the admin pages read it.
type Admin interface {
	Overview(ctx context.Context) (admin.Overview, error)
	Deployments(ctx context.Context, status, query string, limit int32) ([]store.AdminDeploymentsRow, error)
	Deployment(ctx context.Context, idOrPrefix string) (admin.Detail, error)
	Users(ctx context.Context, limit int32) ([]store.AdminUsersRow, error)
}

// requireAdmin lets through only an instance owner, signed in to the dashboard. API tokens never count, so a leaked
// CLI token can't read other teams' data. Everyone else gets 404, so the endpoints don't reveal they exist.
func (s *Server) requireAdmin(next http.HandlerFunc) http.Handler {
	return s.requireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, viaToken := tokenFrom(r.Context()); viaToken || !userFrom(r.Context()).IsOwner {
			writeError(w, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		next(w, r)
	}))
}

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	o, err := s.Admin.Overview(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) adminDeployments(w http.ResponseWriter, r *http.Request) {
	limit := int32(100)
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 1 || n > 500 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 500")
			return
		}
		limit = int32(n)
	}
	rows, err := s.Admin.Deployments(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("q"), limit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if rows == nil {
		rows = []store.AdminDeploymentsRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"deployments": rows})
}

func (s *Server) adminDeployment(w http.ResponseWriter, r *http.Request) {
	d, err := s.Admin.Deployment(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, admin.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No deployment has that id.")
	case errors.Is(err, admin.ErrAmbiguous):
		writeError(w, http.StatusConflict, "ambiguous", "Several deployments start with that; give more of the id.")
	case err != nil:
		s.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, d)
	}
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Admin.Users(r.Context(), 500)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": rows})
}
