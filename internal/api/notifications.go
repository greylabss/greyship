package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"greyship.tech/internal/notify"
	"greyship.tech/internal/store"
	"greyship.tech/internal/webpush"
)

// Notifications is the signed-in person's own notifications, settings and browsers (decisions.md 046).
type Notifications interface {
	List(ctx context.Context, userID string, limit int32) ([]store.Notification, int64, error)
	MarkRead(ctx context.Context, userID string) error
	Settings(ctx context.Context, userID string) ([]notify.Setting, error)
	Set(ctx context.Context, userID string, s notify.Setting) error
	Subscribe(ctx context.Context, userID string, sub webpush.Subscription, userAgent string) (store.PushSubscription, error)
	Subscriptions(ctx context.Context, userID string) ([]store.PushSubscription, error)
	Unsubscribe(ctx context.Context, userID, id string) error
}

type notificationJSON struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Link      string `json:"link"`
	Read      bool   `json:"read"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	limit := int32(30)
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 1 || n > 100 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 100")
			return
		}
		limit = int32(n)
	}
	rows, unread, err := s.Notifications.List(r.Context(), userFrom(r.Context()).ID, limit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]notificationJSON, 0, len(rows))
	for _, n := range rows {
		out = append(out, notificationJSON{ID: n.ID, Kind: n.Kind, Title: n.Title, Body: n.Body, Link: n.Link, Read: n.ReadAt != nil, CreatedAt: n.CreatedAt.UTC().Format(time.RFC3339)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": out, "unread": unread})
}

func (s *Server) readNotifications(w http.ResponseWriter, r *http.Request) {
	if err := s.Notifications.MarkRead(r.Context(), userFrom(r.Context()).ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type settingJSON struct {
	Kind  string `json:"kind"`
	Email bool   `json:"email"`
	Push  bool   `json:"push"`
}

func (s *Server) notificationSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.Notifications.Settings(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]settingJSON, 0, len(settings))
	for _, st := range settings {
		out = append(out, settingJSON{Kind: st.Kind, Email: st.Email, Push: st.Push})
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": out, "push_available": s.PushKey != ""})
}

func (s *Server) setNotificationSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Settings []settingJSON `json:"settings"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.Settings) == 0 || len(body.Settings) > 10 {
		writeError(w, http.StatusBadRequest, "invalid_settings", "send 1 to 10 settings")
		return
	}
	user := userFrom(r.Context()).ID
	for _, st := range body.Settings {
		err := s.Notifications.Set(r.Context(), user, notify.Setting{Kind: st.Kind, Email: st.Email, Push: st.Push})
		if errors.Is(err, notify.ErrBadKind) {
			writeError(w, http.StatusBadRequest, "invalid_kind", "kind must be deploy.succeeded, deploy.failed or project.created")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	s.notificationSettings(w, r)
}

func (s *Server) pushKey(w http.ResponseWriter, _ *http.Request) {
	if s.PushKey == "" {
		writeError(w, http.StatusNotFound, "push_off", "Browser notifications aren't set up on this Greyship.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"public_key": s.PushKey})
}

type subscriptionJSON struct {
	ID        string `json:"id"`
	UserAgent string `json:"user_agent"`
	CreatedAt string `json:"created_at"`
}

func toSubscriptionJSON(p store.PushSubscription) subscriptionJSON {
	return subscriptionJSON{ID: p.ID, UserAgent: p.UserAgent, CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339)}
}

func (s *Server) addPushSubscription(w http.ResponseWriter, r *http.Request) {
	if s.PushKey == "" {
		writeError(w, http.StatusNotFound, "push_off", "Browser notifications aren't set up on this Greyship.")
		return
	}
	// The shape browsers give from PushSubscription.toJSON().
	var body struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
		ExpirationTime *float64 `json:"expirationTime"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sub, err := s.Notifications.Subscribe(r.Context(), userFrom(r.Context()).ID, webpush.Subscription{Endpoint: body.Endpoint, P256dh: body.Keys.P256dh, Auth: body.Keys.Auth}, r.UserAgent())
	if errors.Is(err, notify.ErrBadSubscription) {
		writeError(w, http.StatusBadRequest, "invalid_subscription", "That isn't a browser push subscription.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"subscription": toSubscriptionJSON(sub)})
}

func (s *Server) listPushSubscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := s.Notifications.Subscriptions(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]subscriptionJSON, 0, len(subs))
	for _, p := range subs {
		out = append(out, toSubscriptionJSON(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": out})
}

func (s *Server) deletePushSubscription(w http.ResponseWriter, r *http.Request) {
	err := s.Notifications.Unsubscribe(r.Context(), userFrom(r.Context()).ID, r.PathValue("id"))
	if errors.Is(err, notify.ErrNoSubscription) {
		writeError(w, http.StatusNotFound, "not_found", "No such browser.")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
