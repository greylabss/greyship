package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/notify"
	"greyship.tech/internal/store"
	"greyship.tech/internal/webpush"
)

type fakeNotifications struct {
	user string
	set  []notify.Setting
}

func (f *fakeNotifications) List(_ context.Context, user string, _ int32) ([]store.Notification, int64, error) {
	f.user = user
	return []store.Notification{{ID: "n1", Kind: "deploy.failed", Title: "shop: deploy failed", Body: "b", Link: "/acme/shop/deployments/d1", CreatedAt: time.Unix(0, 0)}}, 1, nil
}
func (f *fakeNotifications) MarkRead(_ context.Context, user string) error { f.user = user; return nil }
func (f *fakeNotifications) Settings(_ context.Context, user string) ([]notify.Setting, error) {
	f.user = user
	return []notify.Setting{{Kind: "deploy.succeeded", Email: true, Push: true}}, nil
}
func (f *fakeNotifications) Set(_ context.Context, _ string, s notify.Setting) error {
	if s.Kind != "deploy.succeeded" {
		return notify.ErrBadKind
	}
	f.set = append(f.set, s)
	return nil
}
func (f *fakeNotifications) Subscribe(_ context.Context, user string, sub webpush.Subscription, _ string) (store.PushSubscription, error) {
	if err := webpush.CheckEndpoint(sub.Endpoint); err != nil {
		return store.PushSubscription{}, notify.ErrBadSubscription
	}
	f.user = user
	return store.PushSubscription{ID: "s1", CreatedAt: time.Unix(0, 0)}, nil
}
func (f *fakeNotifications) Subscriptions(context.Context, string) ([]store.PushSubscription, error) {
	return nil, nil
}
func (f *fakeNotifications) Unsubscribe(_ context.Context, _, id string) error {
	if id != "s1" {
		return notify.ErrNoSubscription
	}
	return nil
}

func TestNotificationEndpoints(t *testing.T) {
	tests := []struct {
		name, method, path, body string
		pushKey                  string
		auth                     bool
		status                   int
		want                     string
	}{
		{"list needs auth", "GET", "/v1/notifications", "", "k", false, http.StatusUnauthorized, "unauthenticated"},
		{"list", "GET", "/v1/notifications", "", "k", true, http.StatusOK, `"unread":1`},
		{"bad limit", "GET", "/v1/notifications?limit=0", "", "k", true, http.StatusBadRequest, "invalid_limit"},
		{"read", "POST", "/v1/notifications/read", "", "k", true, http.StatusNoContent, ""},
		{"settings", "GET", "/v1/me/notifications/settings", "", "k", true, http.StatusOK, `"push_available":true`},
		{"set", "PATCH", "/v1/me/notifications/settings", `{"settings":[{"kind":"deploy.succeeded","email":false,"push":true}]}`, "k", true, http.StatusOK, `"settings"`},
		{"set unknown kind", "PATCH", "/v1/me/notifications/settings", `{"settings":[{"kind":"x"}]}`, "k", true, http.StatusBadRequest, "invalid_kind"},
		{"push key", "GET", "/v1/push/key", "", "k", true, http.StatusOK, `"public_key":"k"`},
		{"push off", "GET", "/v1/push/key", "", "", true, http.StatusNotFound, "push_off"},
		{"subscribe", "POST", "/v1/push/subscriptions", `{"endpoint":"https://fcm.googleapis.com/fcm/send/x","keys":{"p256dh":"p","auth":"a"}}`, "k", true, http.StatusCreated, `"id":"s1"`},
		{"subscribe elsewhere", "POST", "/v1/push/subscriptions", `{"endpoint":"https://169.254.169.254/","keys":{"p256dh":"p","auth":"a"}}`, "k", true, http.StatusBadRequest, "invalid_subscription"},
		{"subscribe with push off", "POST", "/v1/push/subscriptions", `{"endpoint":"https://fcm.googleapis.com/x","keys":{}}`, "", true, http.StatusNotFound, "push_off"},
		{"unsubscribe other", "DELETE", "/v1/push/subscriptions/nope", "", "k", true, http.StatusNotFound, "not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := &fakeNotifications{}
			s := New(Options{
				Auth:          &fakeAuth{tokens: map[string]store.User{"cli": {ID: "u1"}}},
				Notifications: fn,
				PushKey:       tt.pushKey,
				Signer:        auth.Signer{Key: []byte(strings.Repeat("k", 32)), Now: time.Now},
				DashboardURL:  "http://dash.test",
				Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tt.auth {
				req.Header.Set("Authorization", "Bearer cli")
			}
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("got %d %s, want %d containing %s", rec.Code, rec.Body.String(), tt.status, tt.want)
			}
			if tt.status < 300 && fn.user != "" && fn.user != "u1" {
				t.Fatalf("acted for %q, want the caller only", fn.user)
			}
		})
	}
}
