package notify

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/store"
	"greyship.tech/internal/webpush"
)

// Kinds are the events someone can be notified about, in the order settings show them.
var Kinds = []string{bus.DeploySucceeded, bus.DeployFailed, bus.ProjectCreated}

var (
	ErrBadKind         = errors.New("unknown notification kind")
	ErrBadSubscription = errors.New("invalid push subscription")
	ErrNoSubscription  = errors.New("no such push subscription")
)

// Setting is one row of a person's notification settings.
type Setting struct {
	Kind  string
	Email bool
	Push  bool
}

// Inbox is a person's own notifications, settings and browsers. Every method acts on the given user only.
type Inbox struct {
	Pool *pgxpool.Pool
	// PushKey is the VAPID public key browsers subscribe with; empty when push isn't configured.
	PushKey string
}

func (b *Inbox) List(ctx context.Context, userID string, limit int32) ([]store.Notification, int64, error) {
	q := store.New(b.Pool)
	rows, err := q.ListNotifications(ctx, store.ListNotificationsParams{UserID: userID, Limit: limit})
	if err != nil {
		return nil, 0, fmt.Errorf("list notifications: %w", err)
	}
	unread, err := q.CountUnreadNotifications(ctx, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("count notifications: %w", err)
	}
	return rows, unread, nil
}

func (b *Inbox) MarkRead(ctx context.Context, userID string) error {
	return store.New(b.Pool).MarkNotificationsRead(ctx, userID)
}

// Settings lists every kind; kinds without a stored row are on.
func (b *Inbox) Settings(ctx context.Context, userID string) ([]Setting, error) {
	rows, err := store.New(b.Pool).ListNotificationSettings(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list notification settings: %w", err)
	}
	stored := map[string]store.NotificationSetting{}
	for _, r := range rows {
		stored[r.Kind] = r
	}
	out := make([]Setting, 0, len(Kinds))
	for _, k := range Kinds {
		s := Setting{Kind: k, Email: true, Push: true}
		if r, ok := stored[k]; ok {
			s.Email, s.Push = r.Email, r.Push
		}
		out = append(out, s)
	}
	return out, nil
}

func (b *Inbox) Set(ctx context.Context, userID string, s Setting) error {
	known := false
	for _, k := range Kinds {
		known = known || k == s.Kind
	}
	if !known {
		return ErrBadKind
	}
	return store.New(b.Pool).SetNotificationSetting(ctx, store.SetNotificationSettingParams{UserID: userID, Kind: s.Kind, Email: s.Email, Push: s.Push})
}

// Subscribe records a browser. Only real push services are accepted as endpoints (see webpush.CheckEndpoint).
func (b *Inbox) Subscribe(ctx context.Context, userID string, sub webpush.Subscription, userAgent string) (store.PushSubscription, error) {
	if err := webpush.CheckEndpoint(sub.Endpoint); err != nil || len(sub.Endpoint) > 2048 || len(sub.P256dh) < 80 || len(sub.P256dh) > 100 || len(sub.Auth) < 20 || len(sub.Auth) > 30 {
		return store.PushSubscription{}, ErrBadSubscription
	}
	if len(userAgent) > 200 {
		userAgent = userAgent[:200]
	}
	row, err := store.New(b.Pool).AddPushSubscription(ctx, store.AddPushSubscriptionParams{UserID: userID, Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth, UserAgent: userAgent})
	if err != nil {
		return store.PushSubscription{}, fmt.Errorf("add push subscription: %w", err)
	}
	return row, nil
}

func (b *Inbox) Subscriptions(ctx context.Context, userID string) ([]store.PushSubscription, error) {
	return store.New(b.Pool).ListPushSubscriptions(ctx, userID)
}

func (b *Inbox) Unsubscribe(ctx context.Context, userID, id string) error {
	if !uuidLike(id) {
		return ErrNoSubscription
	}
	n, err := store.New(b.Pool).DeletePushSubscription(ctx, store.DeletePushSubscriptionParams{ID: id, UserID: userID})
	if err != nil {
		return fmt.Errorf("delete push subscription: %w", err)
	}
	if n == 0 {
		return ErrNoSubscription
	}
	return nil
}

func uuidLike(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case (c < '0' || c > '9') && (c < 'a' || c > 'f'):
			return false
		}
	}
	return true
}
