package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/mail"
	"greyship.tech/internal/store"
)

const (
	// The production session cookie is __Host-gs_session: Secure, Path=/ and no Domain, so no other host, not even a
	// subdomain, can set or overwrite it. Development (plain http) can't use the prefix.
	sessionCookie    = "__Host-gs_session"
	devSessionCookie = "gs_session"
	stateCookie      = "gs_oauth"
	sessionMaxAge    = 30 * 24 * time.Hour
)

type userJSON struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
	// BetaTester: on the private-beta list (decisions.md 049); the dashboard shows a badge.
	BetaTester bool `json:"beta_tester"`
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
	Plan string `json:"plan"`
	Role string `json:"role"`
}

func toUserJSON(u store.User) userJSON {
	return userJSON{ID: u.ID, Email: u.Email, Name: u.Name, AvatarURL: u.AvatarUrl, BetaTester: u.BetaTester, IsOwner: u.IsOwner, HasPassword: u.PasswordHash != ""}
}

func (s *Server) startEmail(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	err := s.Auth.StartEmail(r.Context(), body.Email, s.clientIP(r))
	if errors.Is(err, auth.ErrInvalidEmail) {
		writeError(w, http.StatusBadRequest, "invalid_email", "Enter a valid email address.")
		return
	}
	if errors.Is(err, mail.ErrQueueFull) {
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "busy", "We're sending a lot of emails right now. Try again in a few seconds.")
		return
	}
	if rl, ok := isRateLimit(err); ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(rl.RetryAfter.Seconds())))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many codes requested. Wait a moment and try again.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// Same answer whether or not an account exists, so the endpoint can't be used to probe emails.
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "code_sent"})
}

func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	token, user, err := s.Auth.VerifyEmail(r.Context(), body.Email, body.Code, r.UserAgent())
	if errors.Is(err, auth.ErrNotInvited) {
		writeError(w, http.StatusForbidden, "not_invited", "This email isn't invited to this Greyship yet. Ask its owner to add it.")
		return
	}
	if errors.Is(err, auth.ErrInviteCheck) {
		s.Logger.Error("beta list check failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "invite_check", "We couldn't check the invite list just now. Try again in a minute.")
		return
	}
	if errors.Is(err, auth.ErrInvalidCode) {
		writeError(w, http.StatusBadRequest, "invalid_code", "That code is wrong or has expired. Request a new one.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.setSession(w, token)
	writeJSON(w, http.StatusOK, map[string]userJSON{"user": toUserJSON(user)})
}

type oauthState struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Verifier string `json:"v"`
}

func (s *Server) startOAuth(w http.ResponseWriter, r *http.Request) {
	p, ok := s.Providers[r.PathValue("provider")]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_provider", "Sign-in with this provider isn't available.")
		return
	}
	st := oauthState{Provider: p.Name, State: oauth2.GenerateVerifier(), Verifier: oauth2.GenerateVerifier()}
	value, err := s.Signer.Sign(st, 10*time.Minute)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	http.SetCookie(w, s.cookie(stateCookie, value, "/v1/auth/oauth", 600))
	http.Redirect(w, r, p.Config.AuthCodeURL(st.State, oauth2.S256ChallengeOption(st.Verifier)), http.StatusFound)
}

func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	p, ok := s.Providers[name]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_provider", "Sign-in with this provider isn't available.")
		return
	}
	// The state cookie is single-use whatever happens next.
	http.SetCookie(w, s.cookie(stateCookie, "", "/v1/auth/oauth", -1))

	fail := func(reason string, err error) {
		s.Logger.Warn("oauth sign-in failed", "provider", name, "reason", reason, "err", err)
		http.Redirect(w, r, s.DashboardURL+"/signin?error="+reason, http.StatusFound)
	}

	c, err := r.Cookie(stateCookie)
	var st oauthState
	if err != nil || s.Signer.Verify(c.Value, &st) != nil || st.Provider != name || st.State != r.URL.Query().Get("state") {
		fail("state", err)
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		fail("denied", errors.New(e))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tok, err := p.Config.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		fail("exchange", err)
		return
	}
	id, err := p.Identify(ctx, p.Config.Client(ctx, tok))
	if err != nil {
		fail("profile", err)
		return
	}
	token, _, err := s.Auth.CompleteOAuth(ctx, id, r.UserAgent())
	if errors.Is(err, auth.ErrEmailNotProven) {
		fail("unverified_email", err)
		return
	}
	if errors.Is(err, auth.ErrNotInvited) {
		fail("not_invited", err)
		return
	}
	if errors.Is(err, auth.ErrInviteCheck) {
		fail("invite_check", err)
		return
	}
	if err != nil {
		fail("internal", err)
		return
	}
	s.setSession(w, token)
	// /continue sends the person on to their first team.
	http.Redirect(w, r, s.DashboardURL+"/continue", http.StatusFound)
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
		out.Teams = append(out.Teams, teamJSON{ID: t.ID, Slug: t.Slug, Name: t.Name, Plan: t.Plan, Role: t.Role})
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
