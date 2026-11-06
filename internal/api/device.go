package api

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"greyship.tech/internal/auth"
)

// startDevice begins `grey login` (F-2). Field names follow RFC 8628 so standard clients understand them.
func (s *Server) startDevice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientName string `json:"client_name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ClientName == "" {
		body.ClientName = "grey CLI"
	}
	d, err := s.Auth.StartDevice(r.Context(), body.ClientName, s.clientIP(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	verify := s.DashboardURL + "/device"
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":               d.DeviceCode,
		"user_code":                 d.UserCode,
		"verification_uri":          verify,
		"verification_uri_complete": verify + "?code=" + url.QueryEscape(d.UserCode),
		"expires_in":                int(d.ExpiresIn.Seconds()),
		"interval":                  int(d.Interval.Seconds()),
	})
}

func (s *Server) pollDevice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceCode string `json:"device_code"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	token, user, err := s.Auth.PollDevice(r.Context(), body.DeviceCode)
	for _, known := range []struct {
		err     error
		message string
	}{
		{auth.ErrPending, "Waiting for you to approve the login in the browser."},
		{auth.ErrSlowDown, "Polling too fast; wait a few seconds between requests."},
		{auth.ErrDenied, "The login was denied in the browser."},
		{auth.ErrExpiredDev, "The login code expired. Run grey login again."},
	} {
		if errors.Is(err, known.err) {
			writeError(w, http.StatusBadRequest, known.err.Error(), known.message)
			return
		}
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": token, "token_type": "bearer", "user": toUserJSON(user)})
}

func (s *Server) deviceInfo(w http.ResponseWriter, r *http.Request) {
	d, err := s.Auth.DeviceInfo(r.Context(), r.URL.Query().Get("user_code"))
	if errors.Is(err, auth.ErrNoDevice) {
		writeError(w, http.StatusNotFound, "unknown_code", "This code is wrong or has expired. Run grey login again.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_code":   d.UserCode,
		"client_name": d.ClientName,
		"ip":          d.Ip,
		"status":      d.Status,
		"created_at":  d.CreatedAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) decideDevice(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			UserCode string `json:"user_code"`
			Team     string `json:"team"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		err := s.Auth.DecideDevice(r.Context(), userFrom(r.Context()).ID, body.UserCode, body.Team, approve)
		switch {
		case errors.Is(err, auth.ErrNoDevice):
			writeError(w, http.StatusNotFound, "unknown_code", "This code is wrong, expired or already used. Run grey login again.")
		case errors.Is(err, auth.ErrNoTeam):
			writeError(w, http.StatusForbidden, "not_a_member", "You're not a member of that team.")
		case err != nil:
			s.internalError(w, r, err)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

type tokenJSON struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	Team       string  `json:"team,omitempty"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
	Current    bool    `json:"current,omitempty"`
}

func toTokenJSON(id, name, prefix, team string, created time.Time, used *time.Time) tokenJSON {
	t := tokenJSON{ID: id, Name: name, Prefix: prefix, Team: team, CreatedAt: created.UTC().Format(time.RFC3339)}
	if used != nil {
		v := used.UTC().Format(time.RFC3339)
		t.LastUsedAt = &v
	}
	return t
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Auth.ListTokens(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	current, _ := tokenFrom(r.Context())
	out := make([]tokenJSON, 0, len(rows))
	for _, t := range rows {
		j := toTokenJSON(t.ID, t.Name, t.Prefix, t.TeamSlug, t.CreatedAt, t.LastUsedAt)
		j.Current = t.ID == current.ID
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Team string `json:"team"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	raw, t, err := s.Auth.CreateToken(r.Context(), userFrom(r.Context()).ID, body.Team, body.Name)
	if errors.Is(err, auth.ErrNoTeam) {
		writeError(w, http.StatusForbidden, "not_a_member", "You're not a member of that team.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": raw, "info": toTokenJSON(t.ID, t.Name, t.Prefix, body.Team, t.CreatedAt, nil)})
}

// revokeToken accepts an id, or "current" to revoke the token making the request (used by grey logout).
func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "current" {
		tok, ok := tokenFrom(r.Context())
		if !ok {
			writeError(w, http.StatusBadRequest, "no_token", "This request didn't use an API token.")
			return
		}
		id = tok.ID
	}
	err := s.Auth.RevokeToken(r.Context(), userFrom(r.Context()).ID, id)
	if errors.Is(err, auth.ErrNoToken) {
		writeError(w, http.StatusNotFound, "not_found", "No such token.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
