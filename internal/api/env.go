package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"greyship.tech/internal/env"
	"greyship.tech/internal/project"
)

// Env is the slice of env.Service the handlers use; tests swap in a fake.
type Env interface {
	List(ctx context.Context, c project.Caller, projectID, environment string) ([]env.Var, error)
	Values(ctx context.Context, c project.Caller, projectID, environment string) (map[string]string, error)
	Apply(ctx context.Context, c project.Caller, projectID, environment string, set map[string]string, unset []string, restart bool) (env.Change, error)
}

type envVarJSON struct {
	Key       string `json:"key"`
	Hash      string `json:"hash"`
	UpdatedAt string `json:"updated_at"`
}

func environment(r *http.Request) string {
	if e := r.URL.Query().Get("env"); e != "" {
		return e
	}
	return "production"
}

// listEnv answers keys and value hashes, never values (decisions.md 020).
func (s *Server) listEnv(w http.ResponseWriter, r *http.Request) {
	vars, err := s.Env.List(r.Context(), callerFrom(r.Context()), r.PathValue("id"), environment(r))
	if err != nil {
		s.envError(w, r, err)
		return
	}
	out := make([]envVarJSON, 0, len(vars))
	for _, v := range vars {
		out = append(out, envVarJSON{Key: v.Key, Hash: v.Hash, UpdatedAt: v.UpdatedAt.UTC().Format(time.RFC3339)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"vars": out})
}

// envValues is grey env pull: the only endpoint that returns stored values.
func (s *Server) envValues(w http.ResponseWriter, r *http.Request) {
	values, err := s.Env.Values(r.Context(), callerFrom(r.Context()), r.PathValue("id"), environment(r))
	if err != nil {
		s.envError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"values": values})
}

func (s *Server) applyEnv(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Env     string            `json:"env"`
		Set     map[string]string `json:"set"`
		Unset   []string          `json:"unset"`
		Restart *bool             `json:"restart"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Env == "" {
		body.Env = "production"
	}
	restart := body.Restart == nil || *body.Restart
	change, err := s.Env.Apply(r.Context(), callerFrom(r.Context()), r.PathValue("id"), body.Env, body.Set, body.Unset, restart)
	if err != nil {
		s.envError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": nonNil(change.Added), "updated": nonNil(change.Updated), "removed": nonNil(change.Removed)})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *Server) envError(w http.ResponseWriter, r *http.Request, err error) {
	var keyErr *env.KeyError
	switch {
	case errors.As(err, &keyErr):
		writeError(w, http.StatusBadRequest, "invalid_key", keyErr.Error())
	case errors.Is(err, env.ErrBadEnv):
		writeError(w, http.StatusBadRequest, "invalid_env", "env must be production or preview.")
	case errors.Is(err, env.ErrTooMany):
		writeError(w, http.StatusBadRequest, "too_many_vars", err.Error())
	default:
		s.projectError(w, r, err)
	}
}
