// Package api serves the public /v1 HTTP API used by the CLI and the dashboard.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/store"
)

// Auth is the slice of auth.Service the handlers use; tests swap in a fake.
type Auth interface {
	SessionUser(ctx context.Context, token string) (store.User, error)
	Logout(ctx context.Context, token string) error
	Teams(ctx context.Context, userID string) ([]store.ListUserTeamsRow, error)

	Authenticate(ctx context.Context, token string) (store.User, store.Token, error)
	CreateToken(ctx context.Context, userID, teamSlug, name string) (string, store.Token, error)
	ListTokens(ctx context.Context, userID string) ([]store.ListTokensRow, error)
	RevokeToken(ctx context.Context, userID, tokenID string) error

	StartDevice(ctx context.Context, clientName, ip string) (auth.DeviceStart, error)
	DeviceInfo(ctx context.Context, userCode string) (store.DeviceCode, error)
	DecideDevice(ctx context.Context, userID, userCode, teamSlug string, approve bool) error
	PollDevice(ctx context.Context, deviceCode string) (string, store.User, error)
}

type Options struct {
	Auth          Auth
	Projects      Projects
	Deployments   Deployments
	Env           Env
	Builds        BuildReports
	GitHub        GitHubDeploys
	Notifications Notifications
	Admin         Admin
	Accounts      Accounts
	Mail          MailSettings
	// PushKey is the VAPID public key for browser push; empty when push isn't configured.
	PushKey string
	// WebhookSecret checks GitHub's deliveries (GREYSHIP_GITHUB_WEBHOOK_SECRET).
	WebhookSecret string
	Signer        auth.Signer
	DashboardURL  string
	AppsDomain    string // services are served at <label>.<AppsDomain> (F-50)
	AppsPort      string // HTTPS port shown in URLs; empty for 443
	BuildGOARCH   string // CPU the runtime nodes run; local builds target it
	SecureCookie  bool
	// HTTPOnly: the instance has no TLS yet, so app URLs are http:// (decisions.md 050).
	HTTPOnly bool
	// ProxySecret is shared with the dashboard, which serves /v1 on its own origin and passes the browser's
	// address in X-Greyship-Client-IP; only requests carrying this secret may set it.
	ProxySecret string
	Logger      *slog.Logger
}

type Server struct {
	Options
	mux *http.ServeMux
}

func New(o Options) *Server {
	s := &Server{Options: o, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	s.mux.HandleFunc("GET /v1/auth/setup", s.setupStatus)
	s.mux.HandleFunc("POST /v1/auth/setup", s.setup)
	s.mux.HandleFunc("POST /v1/auth/password/login", s.passwordLogin)
	s.mux.HandleFunc("POST /v1/auth/logout", s.logout)
	s.mux.Handle("GET /v1/me", s.requireUser(http.HandlerFunc(s.me)))

	s.mux.HandleFunc("POST /v1/auth/device/code", s.startDevice)
	s.mux.HandleFunc("POST /v1/auth/device/token", s.pollDevice)
	s.mux.Handle("GET /v1/auth/device", s.requireUser(http.HandlerFunc(s.deviceInfo)))
	s.mux.Handle("POST /v1/auth/device/approve", s.requireUser(s.decideDevice(true)))
	s.mux.Handle("POST /v1/auth/device/deny", s.requireUser(s.decideDevice(false)))

	s.mux.Handle("GET /v1/tokens", s.requireUser(http.HandlerFunc(s.listTokens)))
	s.mux.Handle("POST /v1/tokens", s.requireUser(http.HandlerFunc(s.createToken)))
	s.mux.Handle("DELETE /v1/tokens/{id}", s.requireUser(http.HandlerFunc(s.revokeToken)))

	{
		s.mux.Handle("GET /v1/me/usage", s.requireUser(http.HandlerFunc(s.myUsage)))
		s.mux.Handle("POST /v1/me/password", s.requireUser(http.HandlerFunc(s.changeOwnPassword)))
		s.mux.Handle("POST /v1/admin/users", s.requireAdmin(s.adminCreateUser))
		s.mux.Handle("PATCH /v1/admin/users/{id}", s.requireAdmin(s.adminUpdateUser))
	}
	if o.Mail != nil {
		s.mux.Handle("GET /v1/admin/mail", s.requireAdmin(s.adminMailGet))
		s.mux.Handle("PUT /v1/admin/mail", s.requireAdmin(s.adminMailPut))
		s.mux.Handle("POST /v1/admin/mail/test", s.requireAdmin(s.adminMailTest))
	}
	if o.Admin != nil {
		s.mux.Handle("GET /v1/admin", s.requireAdmin(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]bool{"admin": true})
		}))
		s.mux.Handle("GET /v1/admin/overview", s.requireAdmin(s.adminOverview))
		s.mux.Handle("GET /v1/admin/deployments", s.requireAdmin(s.adminDeployments))
		s.mux.Handle("GET /v1/admin/deployments/{id}", s.requireAdmin(s.adminDeployment))
		s.mux.Handle("GET /v1/admin/users", s.requireAdmin(s.adminUsers))
	}
	if o.Notifications != nil {
		s.mux.Handle("GET /v1/notifications", s.requireUser(http.HandlerFunc(s.listNotifications)))
		s.mux.Handle("POST /v1/notifications/read", s.requireUser(http.HandlerFunc(s.readNotifications)))
		s.mux.Handle("GET /v1/me/notifications/settings", s.requireUser(http.HandlerFunc(s.notificationSettings)))
		s.mux.Handle("PATCH /v1/me/notifications/settings", s.requireUser(http.HandlerFunc(s.setNotificationSettings)))
		s.mux.Handle("GET /v1/push/key", s.requireUser(http.HandlerFunc(s.pushKey)))
		s.mux.Handle("GET /v1/push/subscriptions", s.requireUser(http.HandlerFunc(s.listPushSubscriptions)))
		s.mux.Handle("POST /v1/push/subscriptions", s.requireUser(http.HandlerFunc(s.addPushSubscription)))
		s.mux.Handle("DELETE /v1/push/subscriptions/{id}", s.requireUser(http.HandlerFunc(s.deletePushSubscription)))
	}
	s.mux.Handle("GET /v1/teams/{team}/projects", s.requireUser(http.HandlerFunc(s.listProjects)))
	s.mux.Handle("POST /v1/teams/{team}/projects", s.requireUser(http.HandlerFunc(s.createProject)))
	s.mux.Handle("GET /v1/projects/{id}", s.requireUser(http.HandlerFunc(s.getProject)))
	s.mux.Handle("GET /v1/teams/{team}/projects/{slug}", s.requireUser(http.HandlerFunc(s.projectBySlug)))
	s.mux.Handle("GET /v1/projects/{id}/services", s.requireUser(http.HandlerFunc(s.listServices)))
	s.mux.Handle("PATCH /v1/projects/{id}", s.requireUser(http.HandlerFunc(s.renameProject)))

	// Build machines authenticate with a per-build token, not a user (decisions.md 039).
	// GitHub deploys exist only once the GitHub App is configured (GREYSHIP_GITHUB_APP_*).
	if s.GitHub != nil {
		s.mux.HandleFunc("POST /v1/github/webhook", s.githubWebhook)
		s.mux.HandleFunc("GET /v1/github/callback", s.githubCallback)
		s.mux.Handle("GET /v1/teams/{team}/github/install", s.requireUser(http.HandlerFunc(s.githubInstall)))
		s.mux.Handle("GET /v1/teams/{team}/github/repos", s.requireUser(http.HandlerFunc(s.githubRepos)))
		s.mux.Handle("POST /v1/teams/{team}/github/import", s.requireUser(http.HandlerFunc(s.githubImport)))
		s.mux.Handle("POST /v1/teams/{team}/github/import-url", s.requireUser(http.HandlerFunc(s.githubImportURL)))
		s.mux.Handle("POST /v1/projects/{id}/github/deploy", s.requireUser(http.HandlerFunc(s.githubDeployLatest)))
		s.mux.Handle("GET /v1/projects/{id}/github/preview", s.requireUser(http.HandlerFunc(s.githubPreview)))
		s.mux.Handle("GET /v1/projects/{id}/github", s.requireUser(http.HandlerFunc(s.githubProjectRepo)))
		s.mux.Handle("POST /v1/projects/{id}/github", s.requireUser(http.HandlerFunc(s.githubConnect)))
	}
	s.mux.HandleFunc("POST /v1/internal/builds/{id}/upload", s.buildUpload)
	s.mux.HandleFunc("POST /v1/internal/builds/{id}/report", s.buildReport)
	s.mux.Handle("GET /v1/build-target", s.requireUser(http.HandlerFunc(s.buildTarget)))
	s.mux.Handle("GET /v1/projects/{id}/deployments", s.requireUser(http.HandlerFunc(s.listDeployments)))
	s.mux.Handle("POST /v1/projects/{id}/deployments", s.requireUser(http.HandlerFunc(s.createDeployment)))
	s.mux.Handle("GET /v1/deployments/{id}", s.requireUser(http.HandlerFunc(s.getDeployment)))
	s.mux.Handle("POST /v1/deployments/{id}/start", s.requireUser(http.HandlerFunc(s.startDeployment)))
	s.mux.Handle("POST /v1/deployments/{id}/redeploy", s.requireUser(http.HandlerFunc(s.redeployDeployment)))
	s.mux.Handle("GET /v1/deployments/{id}/logs", s.requireUser(http.HandlerFunc(s.deploymentLogs)))

	s.mux.Handle("GET /v1/projects/{id}/logs", s.requireUser(http.HandlerFunc(s.projectLogs)))
	s.mux.Handle("GET /v1/projects/{id}/env", s.requireUser(http.HandlerFunc(s.listEnv)))
	s.mux.Handle("GET /v1/projects/{id}/env/values", s.requireUser(http.HandlerFunc(s.envValues)))
	s.mux.Handle("PATCH /v1/projects/{id}/env", s.requireUser(http.HandlerFunc(s.applyEnv)))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		if p := recover(); p != nil {
			s.Logger.Error("panic serving request", "method", r.Method, "path", r.URL.Path, "panic", fmt.Sprint(p))
			writeError(rec, http.StatusInternalServerError, "internal", "Something went wrong on our side.")
		}
		s.Logger.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration_ms", time.Since(start).Milliseconds())
	}()
	if s.cors(rec, r) {
		return
	}
	if !s.sameOrigin(r) {
		writeError(rec, http.StatusForbidden, "bad_origin", "This request didn't come from the Greyship dashboard.")
		return
	}
	s.mux.ServeHTTP(rec, r)
}

// cors lets the dashboard (a different origin in development and production) call the API with its session cookie.
// It reports true when it fully answered a preflight request.
func (s *Server) cors(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || origin != s.DashboardURL {
		return false
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Allow-Credentials", "true")
	h.Add("Vary", "Origin")
	if r.Method != http.MethodOptions {
		return false
	}
	h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	h.Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
	return true
}

// sameOrigin refuses cross-site request forgery: a request that changes something and could ride on the browser's
// session cookie must come from the dashboard. Browsers always send Origin on such requests; the CLI and scripts
// authenticate with a bearer token, which a forged request can't carry, so they need no Origin.
func (s *Server) sameOrigin(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return true
	}
	origin := r.Header.Get("Origin")
	if _, err := r.Cookie(s.sessionName()); err != nil && origin == "" {
		return true // no session to abuse, and not a browser (logging in from a script, the device flow)
	}
	return origin == s.DashboardURL
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

// Unwrap lets http.ResponseController reach the real writer (flushing, per-request deadlines).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON requires application/json, which a cross-site HTML form cannot send without a CORS preflight.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Send a JSON body with Content-Type: application/json.")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON for this endpoint.")
		return false
	}
	return true
}

// clientIP is the address per-IP limits count against. Behind the router (a loopback connection) it is the
// address the router saw, which the router sets in X-Forwarded-For after dropping any the client sent; when that
// is the dashboard's proxy, the browser's own address comes in X-Greyship-Client-IP with the shared secret.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return host
	}
	secret := r.Header.Get("X-Greyship-Proxy-Secret")
	if s.ProxySecret != "" && subtle.ConstantTimeCompare([]byte(secret), []byte(s.ProxySecret)) == 1 {
		if ip := net.ParseIP(r.Header.Get("X-Greyship-Client-IP")); ip != nil {
			return ip.String()
		}
	}
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Forwarded-For"))); ip != nil {
		return ip.String()
	}
	return host
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.Logger.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "Something went wrong on our side.")
}

func isRateLimit(err error) (*auth.RateLimitError, bool) {
	var rl *auth.RateLimitError
	return rl, errors.As(err, &rl)
}
