package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/mail"
	"greyship.tech/internal/store"
)

type fakeAccounts struct {
	err   error
	calls []string
}

func (f *fakeAccounts) LoginPassword(_ context.Context, email, _, _, _ string) (string, store.User, error) {
	f.calls = append(f.calls, "login")
	if f.err != nil {
		return "", store.User{}, f.err
	}
	return "tok-pw", store.User{ID: "u9", Email: email}, nil
}

func (f *fakeAccounts) CreateUser(_ context.Context, in auth.NewUser) (store.User, error) {
	f.calls = append(f.calls, "create")
	if f.err != nil {
		return store.User{}, f.err
	}
	return store.User{ID: "u10", Email: in.Email, IsOwner: in.Admin}, nil
}

func (f *fakeAccounts) SetPassword(context.Context, string, string, string) error {
	f.calls = append(f.calls, "set-password")
	return f.err
}

func (f *fakeAccounts) AdminSetPassword(context.Context, string, string) error {
	f.calls = append(f.calls, "admin-set-password")
	return f.err
}

func (f *fakeAccounts) SetAdmin(context.Context, string, bool) error {
	f.calls = append(f.calls, "set-admin")
	return f.err
}

func TestAccountEndpoints(t *testing.T) {
	fa := &fakeAuth{
		sessions: map[string]store.User{"admin": {ID: "u1", IsOwner: true}, "member": {ID: "u2"}},
		tokens:   map[string]store.User{"admintoken": {ID: "u1", IsOwner: true}},
	}
	tests := []struct {
		name, method, path, body string
		cookie, bearer           string
		err                      error
		status                   int
		want                     string
		calls                    string
	}{
		{"admin creates a user", "POST", "/v1/admin/users", `{"email":"beta@example.com","password":"greylabs-greylabs","admin":true}`, "admin", "", nil, 201, `"email":"beta@example.com"`, "create"},
		{"duplicate email", "POST", "/v1/admin/users", `{"email":"beta@example.com"}`, "admin", "", auth.ErrUserExists, 409, "user_exists", "create"},
		{"weak password", "POST", "/v1/admin/users", `{"email":"b@example.com","password":"x"}`, "admin", "", auth.ErrWeakPassword, 400, "weak_password", "create"},
		{"member can't create", "POST", "/v1/admin/users", `{"email":"b@example.com"}`, "member", "", nil, 404, "not_found", ""},
		{"admin's token can't create", "POST", "/v1/admin/users", `{"email":"b@example.com"}`, "", "admintoken", nil, 404, "not_found", ""},
		{"nobody can't create", "POST", "/v1/admin/users", `{"email":"b@example.com"}`, "", "", nil, 401, "unauthenticated", ""},
		{"admin resets a password", "PATCH", "/v1/admin/users/u2", `{"password":"a long enough password"}`, "admin", "", nil, 204, "", "admin-set-password"},
		{"admin promotes", "PATCH", "/v1/admin/users/u2", `{"admin":true}`, "admin", "", nil, 204, "", "set-admin"},
		{"last admin stays", "PATCH", "/v1/admin/users/u1", `{"admin":false}`, "admin", "", auth.ErrLastAdmin, 409, "last_admin", "set-admin"},
		{"unknown user", "PATCH", "/v1/admin/users/nobody", `{"admin":true}`, "admin", "", auth.ErrNoUser, 404, "not_found", "set-admin"},
		{"empty update", "PATCH", "/v1/admin/users/u2", `{}`, "admin", "", nil, 400, "nothing_to_change", ""},
		{"member can't update", "PATCH", "/v1/admin/users/u2", `{"admin":true}`, "member", "", nil, 404, "not_found", ""},
		{"password login", "POST", "/v1/auth/password/login", `{"email":"beta@example.com","password":"greylabs-greylabs"}`, "", "", nil, 200, "beta@example.com", "login"},
		{"wrong password", "POST", "/v1/auth/password/login", `{"email":"beta@example.com","password":"nope"}`, "", "", auth.ErrInvalidLogin, 401, "invalid_login", "login"},
		{"locked out", "POST", "/v1/auth/password/login", `{"email":"beta@example.com","password":"nope"}`, "", "", &auth.RateLimitError{RetryAfter: time.Minute}, 429, "rate_limited", "login"},
		{"member changes own password", "POST", "/v1/me/password", `{"current":"old password here","new":"new password here"}`, "member", "", nil, 204, "", "set-password"},
		{"wrong current password", "POST", "/v1/me/password", `{"current":"x","new":"new password here"}`, "member", "", auth.ErrWrongPassword, 403, "wrong_password", "set-password"},
		{"signed out can't change a password", "POST", "/v1/me/password", `{"new":"new password here"}`, "", "", nil, 401, "unauthenticated", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := &fakeAccounts{err: tt.err}
			s := New(Options{Auth: fa, Accounts: acc, Signer: auth.Signer{Key: []byte(strings.Repeat("k", 32)), Now: time.Now}, DashboardURL: "http://dash.test", SecureCookie: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tt.cookie})
				req.Header.Set("Origin", "http://dash.test")
			}
			if tt.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tt.bearer)
			}
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("got %d %s, want %d containing %q", rec.Code, rec.Body.String(), tt.status, tt.want)
			}
			if got := strings.Join(acc.calls, ","); got != tt.calls {
				t.Fatalf("service calls = %q, want %q", got, tt.calls)
			}
			if tt.name == "password login" && !strings.Contains(rec.Header().Get("Set-Cookie"), "tok-pw") {
				t.Fatal("a password login sets no session cookie")
			}
		})
	}
}

type fakeMail struct {
	saved    []mail.Settings
	saveErr  error
	testErr  error
	testedTo string
}

func (f *fakeMail) Status(context.Context) mail.Status {
	return mail.Status{Enabled: true, Active: true, Source: mail.SourceDashboard, SendingDomain: "mail.example.com", FromName: "Acme", KeySet: true}
}

func (f *fakeMail) Save(_ context.Context, in mail.Settings) error {
	f.saved = append(f.saved, in)
	return f.saveErr
}

func (f *fakeMail) SendTest(_ context.Context, to string) error {
	f.testedTo = to
	return f.testErr
}

func TestMailSettingsEndpoints(t *testing.T) {
	fa := &fakeAuth{
		sessions: map[string]store.User{"admin": {ID: "u1", Email: "boss@example.com", IsOwner: true}, "member": {ID: "u2"}},
		tokens:   map[string]store.User{"admintoken": {ID: "u1", IsOwner: true}},
	}
	tests := []struct {
		name, method, path, body string
		cookie, bearer           string
		saveErr, testErr         error
		status                   int
		want                     string
	}{
		{"admin reads", "GET", "/v1/admin/mail", "", "admin", "", nil, nil, 200, `"sending_domain":"mail.example.com"`},
		{"admin saves", "PUT", "/v1/admin/mail", `{"enabled":true,"sending_domain":"mail.example.com","api_key":"re_abc"}`, "admin", "", nil, nil, 200, `"key_set":true`},
		{"bad domain", "PUT", "/v1/admin/mail", `{"sending_domain":"nope"}`, "admin", "", mail.ErrInvalidDomain, nil, 400, "invalid_domain"},
		{"incomplete", "PUT", "/v1/admin/mail", `{"enabled":true}`, "admin", "", mail.ErrNeedKey, nil, 400, "incomplete"},
		{"admin sends a test", "POST", "/v1/admin/mail/test", "", "admin", "", nil, nil, 204, ""},
		{"provider refuses the test", "POST", "/v1/admin/mail/test", "", "admin", "", nil, errors.New("domain is not verified"), 502, "domain is not verified"},
		{"member can't read", "GET", "/v1/admin/mail", "", "member", "", nil, nil, 404, "not_found"},
		{"member can't save", "PUT", "/v1/admin/mail", `{"enabled":false}`, "member", "", nil, nil, 404, "not_found"},
		{"admin's token can't read", "GET", "/v1/admin/mail", "", "", "admintoken", nil, nil, 404, "not_found"},
		{"signed out", "GET", "/v1/admin/mail", "", "", "", nil, nil, 401, "unauthenticated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm := &fakeMail{saveErr: tt.saveErr, testErr: tt.testErr}
			s := New(Options{Auth: fa, Mail: fm, Signer: auth.Signer{Key: []byte(strings.Repeat("k", 32)), Now: time.Now}, DashboardURL: "http://dash.test", SecureCookie: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tt.cookie})
				req.Header.Set("Origin", "http://dash.test")
			}
			if tt.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tt.bearer)
			}
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("got %d %s, want %d containing %q", rec.Code, rec.Body.String(), tt.status, tt.want)
			}
			if strings.Contains(rec.Body.String(), "re_abc") {
				t.Fatal("the API key came back in a response")
			}
			if tt.status == 404 && (len(fm.saved) > 0 || fm.testedTo != "") {
				t.Fatal("a refused request reached the mail settings")
			}
			if tt.name == "admin sends a test" && fm.testedTo != "boss@example.com" {
				t.Fatalf("test sent to %q, want the admin's own address", fm.testedTo)
			}
		})
	}
}
