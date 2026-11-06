package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/store"
)

// Accounts is signing in with a password and the instance admin's user management (decisions.md 051).
type Accounts interface {
	LoginPassword(ctx context.Context, email, password, ip, userAgent string) (string, store.User, error)
	CreateUser(ctx context.Context, in auth.NewUser) (store.User, error)
	SetPassword(ctx context.Context, userID, current, next string) error
	AdminSetPassword(ctx context.Context, userID, next string) error
	SetAdmin(ctx context.Context, userID string, admin bool) error
}

func (s *Server) passwordLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	token, user, err := s.Accounts.LoginPassword(r.Context(), body.Email, body.Password, s.clientIP(r), r.UserAgent())
	if rl, ok := isRateLimit(err); ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(rl.RetryAfter.Seconds())))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many wrong attempts. Try again in a few minutes.")
		return
	}
	if errors.Is(err, auth.ErrInvalidLogin) {
		writeError(w, http.StatusUnauthorized, "invalid_login", "Wrong email or password.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.setSession(w, token)
	writeJSON(w, http.StatusOK, map[string]userJSON{"user": toUserJSON(user)})
}

// accountError answers the errors account operations share; it reports whether it answered.
func (s *Server) accountError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, "weak_password", "A password needs 10 to 128 characters.")
	case errors.Is(err, auth.ErrInvalidEmail):
		writeError(w, http.StatusBadRequest, "invalid_email", "Enter a valid email address.")
	case errors.Is(err, auth.ErrUserExists):
		writeError(w, http.StatusConflict, "user_exists", "An account with that email already exists.")
	case errors.Is(err, auth.ErrLastAdmin):
		writeError(w, http.StatusConflict, "last_admin", "There must be at least one admin.")
	case errors.Is(err, auth.ErrWrongPassword):
		writeError(w, http.StatusForbidden, "wrong_password", "The current password is wrong.")
	case errors.Is(err, auth.ErrNoUser):
		writeError(w, http.StatusNotFound, "not_found", "No such user.")
	default:
		return false
	}
	return true
}

func (s *Server) changeOwnPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	err := s.Accounts.SetPassword(r.Context(), userFrom(r.Context()).ID, body.Current, body.New)
	if s.accountError(w, err) {
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
		Admin    bool   `json:"admin"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	user, err := s.Accounts.CreateUser(r.Context(), auth.NewUser{Email: body.Email, Name: body.Name, Password: body.Password, Admin: body.Admin})
	if s.accountError(w, err) {
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.Logger.Info("admin created a user", "by", userFrom(r.Context()).ID, "user", user.ID, "admin", user.IsOwner, "has_password", body.Password != "")
	writeJSON(w, http.StatusCreated, map[string]userJSON{"user": toUserJSON(user)})
}

func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Admin    *bool   `json:"admin"`
		Password *string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Admin == nil && body.Password == nil {
		writeError(w, http.StatusBadRequest, "nothing_to_change", "Send admin or password.")
		return
	}
	id := r.PathValue("id")
	if body.Password != nil {
		if err := s.Accounts.AdminSetPassword(r.Context(), id, *body.Password); s.accountError(w, err) {
			return
		} else if err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	if body.Admin != nil {
		if err := s.Accounts.SetAdmin(r.Context(), id, *body.Admin); s.accountError(w, err) {
			return
		} else if err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	s.Logger.Info("admin changed a user", "by", userFrom(r.Context()).ID, "user", id, "password", body.Password != nil, "admin", body.Admin != nil)
	w.WriteHeader(http.StatusNoContent)
}
