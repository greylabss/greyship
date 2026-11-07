package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/store"
)

const (
	// The production session cookie is __Host-gs_session: Secure, Path=/ and no Domain, so no other host, not even a
	// subdomain, can set or overwrite it. Development (plain http) can't use the prefix.
	sessionCookie    = "__Host-gs_session"
	devSessionCookie = "gs_session"
	sessionMaxAge    = 30 * 24 * time.Hour
)

type userJSON struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
	// IsOwner: owns this instance and may open the admin view (decisions.md 050).
	IsOwner bool `json:"is_owner"`
	// HasPassword: the account can sign in with a password (decisions.md 051). Never the hash.
	HasPassword bool       `json:"has_password"`
	Teams       []teamJSON `json:"teams,omitempty"`
	TokenTeam   string     `json:"token_team,omitempty"`
}

type teamJSON struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role"`
}

func toUserJSON(u store.User) userJSON {
	return userJSON{ID: u.ID, Email: u.Email, Name: u.Name, AvatarURL: u.AvatarUrl, IsOwner: u.IsOwner, HasPassword: u.PasswordHash != ""}
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(s.sessionName()); err == nil {
		if err := s.Auth.Logout(r.Context(), c.Value); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	http.SetCookie(w, s.cookie(s.sessionName(), "", "/", -1))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	teams, err := s.Auth.Teams(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := toUserJSON(user)
	tok, usedToken := tokenFrom(r.Context())
	for _, t := range teams {
		out.Teams = append(out.Teams, teamJSON{ID: t.ID, Slug: t.Slug, Name: t.Name, Role: t.Role})
		if usedToken && t.ID == tok.TeamID {
			out.TokenTeam = t.Slug
		}
	}
	writeJSON(w, http.StatusOK, map[string]userJSON{"user": out})
}

func (s *Server) setSession(w http.ResponseWriter, token string) {
	http.SetCookie(w, s.cookie(s.sessionName(), token, "/", int(sessionMaxAge.Seconds())))
}

func (s *Server) sessionName() string {
	if s.SecureCookie {
		return sessionCookie
	}
	return devSessionCookie
}

// cookie builds every cookie the API sets: httpOnly, SameSite=Lax, and Secure except in development (plain http://localhost).
func (s *Server) cookie(name, value, path string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: path, MaxAge: maxAge, HttpOnly: true, Secure: s.SecureCookie, SameSite: http.SameSiteLaxMode} //nolint:gosec // Secure is false only in development or before the instance has a domain (HTTPOnly)
}

type userKey struct{}

func userFrom(ctx context.Context) store.User {
	u, _ := ctx.Value(userKey{}).(store.User)
	return u
}

type tokenKey struct{}

// tokenFrom returns the API token that authenticated the request, if it used one.
func tokenFrom(ctx context.Context) (store.Token, bool) {
	t, ok := ctx.Value(tokenKey{}).(store.Token)
	return t, ok
}

// requireUser accepts a bearer token (CLI, CI, scripts) or the dashboard session cookie.
func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			user, tok, err := s.Auth.Authenticate(ctx, strings.TrimSpace(bearer))
			if errors.Is(err, auth.ErrNoToken) {
				writeError(w, http.StatusUnauthorized, "invalid_token", "This token is invalid or was revoked. Run grey login.")
				return
			}
			if err != nil {
				s.internalError(w, r, err)
				return
			}
			ctx = context.WithValue(context.WithValue(ctx, userKey{}, user), tokenKey{}, tok)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		c, err := r.Cookie(s.sessionName())
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
			return
		}
		user, err := s.Auth.SessionUser(ctx, c.Value)
		if errors.Is(err, auth.ErrNoSession) {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Your session has ended. Sign in again.")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, userKey{}, user)))
	})
}
