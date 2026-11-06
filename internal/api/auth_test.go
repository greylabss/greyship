package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/mail"
	"greyship.tech/internal/store"
)

type fakeAuth struct {
	startErr  error
	verifyErr error
	pollErr   error
	sessions  map[string]store.User
	tokens    map[string]store.User
	loggedOut []string
	revoked   []string
}

func (f *fakeAuth) StartEmail(context.Context, string, string) error { return f.startErr }

func (f *fakeAuth) VerifyEmail(_ context.Context, email, _, _ string) (string, store.User, error) {
	if f.verifyErr != nil {
		return "", store.User{}, f.verifyErr
	}
	return "tok-1", store.User{ID: "u1", Email: email}, nil
}

func (f *fakeAuth) CompleteOAuth(context.Context, auth.Identity, string) (string, store.User, error) {
	return "tok-oauth", store.User{ID: "u1"}, nil
}

func (f *fakeAuth) SessionUser(_ context.Context, token string) (store.User, error) {
	if u, ok := f.sessions[token]; ok {
		return u, nil
	}
	return store.User{}, auth.ErrNoSession
}

func (f *fakeAuth) Logout(_ context.Context, token string) error {
	f.loggedOut = append(f.loggedOut, token)
	return nil
}

func (f *fakeAuth) Teams(context.Context, string) ([]store.ListUserTeamsRow, error) {
	return []store.ListUserTeamsRow{{ID: "t1", Slug: "haki", Name: "haki", Plan: "free", Role: "owner"}}, nil
}

func (f *fakeAuth) Authenticate(_ context.Context, token string) (store.User, store.Token, error) {
	if u, ok := f.tokens[token]; ok {
		return u, store.Token{ID: "tok-id-" + token, UserID: u.ID, TeamID: "t1"}, nil
	}
	return store.User{}, store.Token{}, auth.ErrNoToken
}

func (f *fakeAuth) CreateToken(_ context.Context, _, team, name string) (string, store.Token, error) {
	if team == "someone-else" {
		return "", store.Token{}, auth.ErrNoTeam
	}
	return "gs_new", store.Token{ID: "t9", Name: name, Prefix: "gs_new"}, nil
}

func (f *fakeAuth) ListTokens(context.Context, string) ([]store.ListTokensRow, error) {
	return []store.ListTokensRow{{ID: "tok-id-cli", Name: "grey login", Prefix: "gs_abc", TeamSlug: "haki"}, {ID: "other", Name: "ci", Prefix: "gs_def", TeamSlug: "haki"}}, nil
}

func (f *fakeAuth) RevokeToken(_ context.Context, _, id string) error {
	f.revoked = append(f.revoked, id)
	return nil
}

func (f *fakeAuth) StartDevice(context.Context, string, string) (auth.DeviceStart, error) {
	return auth.DeviceStart{DeviceCode: "dev-secret", UserCode: "KQWD-7HXP", ExpiresIn: 10 * time.Minute, Interval: 5 * time.Second}, nil
}

func (f *fakeAuth) DeviceInfo(context.Context, string) (store.DeviceCode, error) {
	return store.DeviceCode{UserCode: "KQWD-7HXP", ClientName: "grey CLI", Status: "pending"}, nil
}

func (f *fakeAuth) DecideDevice(context.Context, string, string, string, bool) error { return nil }

func (f *fakeAuth) PollDevice(context.Context, string) (string, store.User, error) {
	if f.pollErr != nil {
		return "", store.User{}, f.pollErr
	}
	return "gs_cli", store.User{ID: "u1", Email: "a@b.co"}, nil
}

func newTestServer(fa *fakeAuth) *Server {
	return New(Options{
		Auth:         fa,
		Providers:    map[string]auth.Provider{"github": auth.GitHub("cid", "secret", "http://api.test/v1/auth/oauth/github/callback", auth.GitHubEndpoints)},
		Signer:       auth.Signer{Key: []byte(strings.Repeat("k", 32)), Now: time.Now},
		DashboardURL: "http://dash.test",
		AppsDomain:   "greyship.tech",
		SecureCookie: true,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func do(s *Server, method, path, contentType, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	// Requests with a session cookie are what the dashboard sends, so they carry its Origin.
	if len(cookies) > 0 {
		req.Header.Set("Origin", "http://dash.test")
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestCrossSiteRequestsAreRefused(t *testing.T) {
	fa := &fakeAuth{sessions: map[string]store.User{"good": {ID: "u1"}}, tokens: map[string]store.User{"cli": {ID: "u1"}}}
	s := newTestServer(fa)
	session := &http.Cookie{Name: sessionCookie, Value: "good"}
	tests := []struct {
		name, method, origin, bearer string
		cookie                       bool
		want                         int
	}{
		{"forged post from an app", "POST", "https://evil-app.greyshipapp.tech", "", true, http.StatusForbidden},
		{"cookie post without origin", "POST", "", "", true, http.StatusForbidden},
		{"dashboard post", "POST", "http://dash.test", "", true, http.StatusNoContent},
		{"cli bearer without origin", "POST", "", "cli", false, http.StatusNoContent},
		{"forged get is only a read", "GET", "https://evil-app.greyshipapp.tech", "", true, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "/v1/auth/logout"
			if tt.method == "GET" {
				path = "/v1/me"
			}
			req := httptest.NewRequest(tt.method, path, nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.cookie {
				req.AddCookie(session)
			}
			if tt.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tt.bearer)
			}
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("%s %s = %d %s, want %d", tt.method, path, rec.Code, rec.Body, tt.want)
			}
		})
	}
	if rec := do(s, "GET", "/v1/me", "", "", &http.Cookie{Name: devSessionCookie, Value: "good"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a cookie without the __Host- prefix must not sign anyone in over https: %d", rec.Code)
	}
}

func TestStartEmail(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		err         error
		wantStatus  int
		wantCode    string
	}{
		{"sent", "application/json", `{"email":"a@b.co"}`, nil, http.StatusAccepted, `"code_sent"`},
		{"form post refused", "application/x-www-form-urlencoded", "email=a@b.co", nil, http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"unknown field", "application/json", `{"email":"a@b.co","admin":true}`, nil, http.StatusBadRequest, "invalid_json"},
		{"invalid email", "application/json", `{"email":"nope"}`, auth.ErrInvalidEmail, http.StatusBadRequest, "invalid_email"},
		{"rate limited", "application/json", `{"email":"a@b.co"}`, &auth.RateLimitError{RetryAfter: time.Minute}, http.StatusTooManyRequests, "rate_limited"},
		{"mail queue full", "application/json", `{"email":"a@b.co"}`, mail.ErrQueueFull, http.StatusServiceUnavailable, "busy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(newTestServer(&fakeAuth{startErr: tt.err}), "POST", "/v1/auth/email/start", tt.contentType, tt.body)
			if rec.Code != tt.wantStatus || !strings.Contains(rec.Body.String(), tt.wantCode) {
				t.Fatalf("status %d body %s; want %d containing %s", rec.Code, rec.Body, tt.wantStatus, tt.wantCode)
			}
			if tt.wantStatus == http.StatusTooManyRequests && rec.Header().Get("Retry-After") != "60" {
				t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
			}
		})
	}
}

func TestVerifyEmailSetsSecureSession(t *testing.T) {
	rec := do(newTestServer(&fakeAuth{}), "POST", "/v1/auth/email/verify", "application/json", `{"email":"a@b.co","code":"123456"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	c := rec.Result().Cookies()
	if len(c) != 1 || c[0].Name != sessionCookie || c[0].Value != "tok-1" || !c[0].HttpOnly || !c[0].Secure || c[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %+v", c)
	}

	rec = do(newTestServer(&fakeAuth{verifyErr: auth.ErrInvalidCode}), "POST", "/v1/auth/email/verify", "application/json", `{"email":"a@b.co","code":"000000"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_code") || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("wrong code: status %d body %s cookies %v", rec.Code, rec.Body, rec.Result().Cookies())
	}
}

func TestMeAndLogout(t *testing.T) {
	fa := &fakeAuth{sessions: map[string]store.User{"good": {ID: "u1", Email: "a@b.co"}}}
	s := newTestServer(fa)

	if rec := do(s, "GET", "/v1/me", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: %d", rec.Code)
	}
	if rec := do(s, "GET", "/v1/me", "", "", &http.Cookie{Name: sessionCookie, Value: "stale"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("stale cookie: %d", rec.Code)
	}
	rec := do(s, "GET", "/v1/me", "", "", &http.Cookie{Name: sessionCookie, Value: "good"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"slug":"haki"`) {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}

	rec = do(s, "POST", "/v1/auth/logout", "", "", &http.Cookie{Name: sessionCookie, Value: "good"})
	if rec.Code != http.StatusNoContent || len(fa.loggedOut) != 1 || rec.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("logout: %d %v %+v", rec.Code, fa.loggedOut, rec.Result().Cookies())
	}
}

func TestOAuthStart(t *testing.T) {
	s := newTestServer(&fakeAuth{})
	if rec := do(s, "GET", "/v1/auth/oauth/myspace/start", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown provider: %d", rec.Code)
	}
	rec := do(s, "GET", "/v1/auth/oauth/github/start", "", "")
	if rec.Code != http.StatusFound {
		t.Fatalf("status %d", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	q := loc.Query()
	if loc.Host != "github.com" || q.Get("state") == "" || q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "cid" {
		t.Fatalf("redirect = %s", loc)
	}
	c := rec.Result().Cookies()
	if len(c) != 1 || c[0].Name != stateCookie || !c[0].HttpOnly || c[0].Path != "/v1/auth/oauth" {
		t.Fatalf("state cookie = %+v", c)
	}
}

func TestOAuthCallbackRejectsBadState(t *testing.T) {
	s := newTestServer(&fakeAuth{})
	start := do(s, "GET", "/v1/auth/oauth/github/start", "", "")
	stateCookieValue := start.Result().Cookies()[0]

	tests := []struct {
		name   string
		query  string
		cookie *http.Cookie
	}{
		{"no cookie", "?state=x&code=y", nil},
		{"state mismatch", "?state=forged&code=y", stateCookieValue},
		{"tampered cookie", "?state=x&code=y", &http.Cookie{Name: stateCookie, Value: stateCookieValue.Value + "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cookies []*http.Cookie
			if tt.cookie != nil {
				cookies = append(cookies, tt.cookie)
			}
			rec := do(s, "GET", "/v1/auth/oauth/github/callback"+tt.query, "", "", cookies...)
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != "http://dash.test/signin?error=state" {
				t.Fatalf("status %d location %s", rec.Code, rec.Header().Get("Location"))
			}
		})
	}
}

func TestBearerTokens(t *testing.T) {
	fa := &fakeAuth{tokens: map[string]store.User{"cli": {ID: "u1", Email: "a@b.co"}}}
	s := newTestServer(fa)
	bearer := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}

	if rec := bearer("GET", "/v1/me", "cli"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "a@b.co") || !strings.Contains(rec.Body.String(), `"token_team":"haki"`) {
		t.Fatalf("me with token: %d %s", rec.Code, rec.Body)
	}
	if rec := bearer("GET", "/v1/me", "revoked"); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "invalid_token") {
		t.Fatalf("bad token: %d %s", rec.Code, rec.Body)
	}
	rec := bearer("GET", "/v1/tokens", "cli")
	if !strings.Contains(rec.Body.String(), `"id":"tok-id-cli","name":"grey login","prefix":"gs_abc","team":"haki"`) || !strings.Contains(rec.Body.String(), `"current":true`) {
		t.Fatalf("list marks current token: %s", rec.Body)
	}
	if rec := bearer("DELETE", "/v1/tokens/current", "cli"); rec.Code != http.StatusNoContent || len(fa.revoked) != 1 || fa.revoked[0] != "tok-id-cli" {
		t.Fatalf("revoke current: %d %v", rec.Code, fa.revoked)
	}
	if rec := do(s, "POST", "/v1/tokens", "application/json", `{"name":"ci","team":"someone-else"}`, &http.Cookie{Name: sessionCookie, Value: "x"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("create without valid session: %d", rec.Code)
	}
}

func TestDeviceFlowResponses(t *testing.T) {
	rec := do(newTestServer(&fakeAuth{}), "POST", "/v1/auth/device/code", "application/json", `{"client_name":"grey on mac"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"verification_uri_complete":"http://dash.test/device?code=KQWD-7HXP"`) || !strings.Contains(rec.Body.String(), `"interval":5`) {
		t.Fatalf("device code: %d %s", rec.Code, rec.Body)
	}

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"pending", auth.ErrPending, `"authorization_pending"`},
		{"too fast", auth.ErrSlowDown, `"slow_down"`},
		{"denied", auth.ErrDenied, `"access_denied"`},
		{"expired", auth.ErrExpiredDev, `"expired_token"`},
		{"approved", nil, `"access_token":"gs_cli"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(newTestServer(&fakeAuth{pollErr: tt.err}), "POST", "/v1/auth/device/token", "application/json", `{"device_code":"dev-secret"}`)
			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("poll: %d %s, want %s", rec.Code, rec.Body, tt.want)
			}
		})
	}

	if rec := do(newTestServer(&fakeAuth{}), "POST", "/v1/auth/device/approve", "application/json", `{"user_code":"KQWD-7HXP"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("approve without sign-in: %d", rec.Code)
	}
}

func TestCORS(t *testing.T) {
	s := newTestServer(&fakeAuth{})
	req := httptest.NewRequest("OPTIONS", "/v1/auth/email/start", nil)
	req.Header.Set("Origin", "http://dash.test")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "http://dash.test" || rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("preflight: %d %v", rec.Code, rec.Header())
	}

	req = httptest.NewRequest("OPTIONS", "/v1/auth/email/start", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("foreign origin got CORS headers: %v", rec.Header())
	}
}

func TestClientIP(t *testing.T) {
	s := newTestServer(&fakeAuth{})
	s.ProxySecret = "shh"
	tests := []struct {
		name, remote string
		headers      map[string]string
		want         string
	}{
		{"direct connection", "203.0.113.7:5000", map[string]string{"X-Forwarded-For": "1.2.3.4"}, "203.0.113.7"},
		{"behind the router", "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "198.51.100.2"}, "198.51.100.2"},
		{"dashboard proxy with secret", "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "76.76.21.1", "X-Greyship-Proxy-Secret": "shh", "X-Greyship-Client-IP": "198.51.100.9"}, "198.51.100.9"},
		{"forged client ip", "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "198.51.100.2", "X-Greyship-Proxy-Secret": "guess", "X-Greyship-Client-IP": "10.0.0.1"}, "198.51.100.2"},
		{"garbage", "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "not an ip"}, "127.0.0.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", nil)
			r.RemoteAddr = tt.remote
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}
			if got := s.clientIP(r); got != tt.want {
				t.Fatalf("clientIP = %s, want %s", got, tt.want)
			}
		})
	}
}
