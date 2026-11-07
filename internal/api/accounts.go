package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/store"
)

// Accounts is signing in with a password, creating the owner of a new instance, and the admin's user management
// (decisions.md 051, 053).
type Accounts interface {
	NeedsSetup(ctx context.Context) (bool, error)
	Setup(ctx context.Context, in auth.NewUser, userAgent string) (string, store.User, error)
	LoginPassword(ctx context.Context, email, password, ip, userAgent string) (string, store.User, error)
	CreateUser(ctx context.Context, in auth.NewUser) (store.User, error)
	SetPassword(ctx context.Context, userID, current, next string) error
	AdminSetPassword(ctx context.Context, userID, next string) error
	SetAdmin(ctx context.Context, userID string, admin bool) error
	SetLimits(ctx context.Context, userID string, l auth.Limits) error
	Usage(ctx context.Context, userID string) (auth.Usage, error)
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

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	needed, err := s.Accounts.NeedsSetup(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"setup_needed": needed})
}

// setup creates the owner of an instance that has no accounts, and signs them in.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	token, user, err := s.Accounts.Setup(r.Context(), auth.NewUser{Email: body.Email, Name: body.Name, Password: body.Password}, r.UserAgent())
	if errors.Is(err, auth.ErrSetupDone) {
		writeError(w, http.StatusConflict, "setup_done", "This instance already has an owner. Sign in, or ask them to add you.")
		return
	}
	if errors.Is(err, auth.ErrNotOwnerEmail) {
		writeError(w, http.StatusForbidden, "not_owner_email", "This instance only accepts its owner's email for the first account.")
		return
	}
	if s.accountError(w, err) {
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.setSession(w, token)
	writeJSON(w, http.StatusCreated, map[string]userJSON{"user": toUserJSON(user)})
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
	case errors.Is(err, auth.ErrInvalidLimits):
		writeError(w, http.StatusBadRequest, "invalid_limits", "Limits must be positive numbers. Leave a field empty for no limit.")
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
		// Limits replaces all three limits at once; a null field means no limit.
		Limits *struct {
			MemoryMB         *int32   `json:"memory_mb"`
			CPUCores         *float64 `json:"cpu_cores"`
			ConcurrentBuilds *int32   `json:"concurrent_builds"`
		} `json:"limits"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Admin == nil && body.Password == nil && body.Limits == nil {
		writeError(w, http.StatusBadRequest, "nothing_to_change", "Send admin, password or limits.")
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
	if body.Limits != nil {
		l := auth.Limits{MemoryMB: body.Limits.MemoryMB, ConcurrentBuilds: body.Limits.ConcurrentBuilds}
		if c := body.Limits.CPUCores; c != nil {
			millis := int32(min(*c*1000, 1<<30)) //nolint:gosec // capped, and SetLimits rejects anything out of range
			l.CPUMillis = &millis
		}
		if err := s.Accounts.SetLimits(r.Context(), id, l); s.accountError(w, err) {
			return
		} else if err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	s.Logger.Info("admin changed a user", "by", userFrom(r.Context()).ID, "user", id, "password", body.Password != nil, "admin", body.Admin != nil, "limits", body.Limits != nil)
	w.WriteHeader(http.StatusNoContent)
}

// usageJSON is a user's running resources next to the limits their admin set. A missing max means no limit.
type usageJSON struct {
	MemoryUsedMB     int64    `json:"memory_used_mb"`
	MemoryMaxMB      *int32   `json:"memory_max_mb"`
	CPUUsedCores     float64  `json:"cpu_used_cores"`
	CPUMaxCores      *float64 `json:"cpu_max_cores"`
	ConcurrentBuilds *int32   `json:"concurrent_builds_max"`
}

func (s *Server) myUsage(w http.ResponseWriter, r *http.Request) {
	u, err := s.Accounts.Usage(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := usageJSON{MemoryUsedMB: u.MemoryUsedMB, MemoryMaxMB: u.MemoryMB, CPUUsedCores: float64(u.CPUUsedMillis) / 1000, ConcurrentBuilds: u.ConcurrentBuilds}
	if u.CPUMillis != nil {
		c := float64(*u.CPUMillis) / 1000
		out.CPUMaxCores = &c
	}
	writeJSON(w, http.StatusOK, out)
}
