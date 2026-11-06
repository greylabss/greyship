//go:build integration

package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/mail"
	"greyship.tech/internal/notify"
	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
	"greyship.tech/internal/testdb"
	"greyship.tech/internal/webpush"
)

type outbox struct {
	mu   sync.Mutex
	sent []mail.Message
	fail error
}

func (o *outbox) Send(_ context.Context, m mail.Message) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.fail != nil {
		return o.fail
	}
	o.sent = append(o.sent, m)
	return nil
}

type pushes struct {
	sent []string
	gone map[string]bool
}

func (p *pushes) Send(_ context.Context, sub webpush.Subscription, payload []byte) error {
	if p.gone[sub.Endpoint] {
		return webpush.ErrGone
	}
	p.sent = append(p.sent, sub.Endpoint+" "+string(payload))
	return nil
}

func job(t *testing.T, kind, subject, user string) bus.Job {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"kind": kind, "subject": subject, "user": user})
	return bus.Job{Payload: b}
}

func TestNotifications(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := store.New(pool)
	team, _ := q.CreateTeam(ctx, store.CreateTeamParams{Slug: "acme", Name: "acme"})
	u, _ := q.CreateUser(ctx, store.CreateUserParams{Email: "owner@acme.test"})
	if err := q.AddMember(ctx, store.AddMemberParams{TeamID: team.ID, UserID: u.ID, Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	owner := project.Caller{UserID: u.ID}

	// Creating a project queues its notification in the same transaction.
	p, err := project.NewService(pool).Create(ctx, owner, "acme", "shop")
	if err != nil {
		t.Fatal(err)
	}
	var queued int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind = 'notify' AND payload->>'subject' = $1", p.ID).Scan(&queued)
	if queued != 1 {
		t.Fatalf("project.created jobs = %d, want 1", queued)
	}

	box, push := &outbox{}, &pushes{gone: map[string]bool{"https://fcm.googleapis.com/gone": true}}
	n := &notify.Notifier{Pool: pool, Mail: box, From: "Greyship <n@greyship.tech>", Push: push, DashboardURL: "https://greyship.tech", Logger: slog.New(slog.DiscardHandler)}
	inbox := &notify.Inbox{Pool: pool, PushKey: "k"}
	for _, ep := range []string{"https://fcm.googleapis.com/ok", "https://fcm.googleapis.com/gone"} {
		if _, err := inbox.Subscribe(ctx, u.ID, webpush.Subscription{Endpoint: ep, P256dh: strings.Repeat("B", 87), Auth: strings.Repeat("a", 22)}, "test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := inbox.Subscribe(ctx, u.ID, webpush.Subscription{Endpoint: "https://169.254.169.254/x", P256dh: strings.Repeat("B", 87), Auth: strings.Repeat("a", 22)}, "test"); !errors.Is(err, notify.ErrBadSubscription) {
		t.Fatalf("a non-push endpoint was accepted: %v", err)
	}

	if err := n.Handle(ctx, job(t, bus.ProjectCreated, p.ID, u.ID)); err != nil {
		t.Fatal(err)
	}
	if len(box.sent) != 1 || box.sent[0].To != "owner@acme.test" || box.sent[0].Subject != "New project shop" || !strings.Contains(box.sent[0].Text, "https://greyship.tech/acme/shop") {
		t.Fatalf("emails = %+v", box.sent)
	}
	if len(push.sent) != 1 || !strings.HasPrefix(push.sent[0], "https://fcm.googleapis.com/ok ") {
		t.Fatalf("pushes = %v", push.sent)
	}
	if subs, _ := inbox.Subscriptions(ctx, u.ID); len(subs) != 1 {
		t.Fatalf("subscriptions = %d; a gone browser must be forgotten", len(subs))
	}

	// A failed deploy, started by the owner, with nothing live yet.
	key, size, sum := "k", int64(1), strings.Repeat("a", 64)
	msg := "go build failed (exit status 1)"
	d, err := q.CreateDeployment(ctx, store.CreateDeploymentParams{ID: "00000000-0000-4000-8000-000000000001", ProjectID: p.ID, Source: "cli", CreatedBy: &u.ID, SourceKey: &key, SourceSize: &size, SourceSha256: &sum})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE deployments SET status = 'failed', error = $2, commit_sha = 'abcdef0123456789abcdef0123456789abcdef01', finished_at = now() WHERE id = $1", d.ID, msg); err != nil {
		t.Fatal(err)
	}
	box.fail = errors.New("resend is down")
	if err := n.Handle(ctx, job(t, bus.DeployFailed, d.ID, "")); err == nil {
		t.Fatal("a failed email must retry the job")
	}
	box.fail = nil
	if err := n.Handle(ctx, job(t, bus.DeployFailed, d.ID, "")); err != nil {
		t.Fatal(err)
	}
	if err := n.Handle(ctx, job(t, bus.DeployFailed, d.ID, "")); err != nil {
		t.Fatal(err)
	}
	if len(box.sent) != 2 {
		t.Fatalf("emails = %d, want 2: a retried job must not email twice", len(box.sent))
	}
	got := box.sent[1]
	if got.Subject != "shop: deploy failed" || !strings.Contains(got.Text, "Deployment 00000000 (commit abcdef0) failed: go build failed (exit status 1).") || !strings.Contains(got.Text, "no live version yet") || got.From != "Greyship <n@greyship.tech>" {
		t.Fatalf("failure email = %+v", got)
	}
	if len(push.sent) != 2 {
		t.Fatalf("pushes = %d, want 2", len(push.sent))
	}

	// Settings: success emails off, push still on.
	if err := inbox.Set(ctx, u.ID, notify.Setting{Kind: bus.DeploySucceeded, Email: false, Push: true}); err != nil {
		t.Fatal(err)
	}
	if err := inbox.Set(ctx, u.ID, notify.Setting{Kind: "deploy.exploded"}); !errors.Is(err, notify.ErrBadKind) {
		t.Fatalf("unknown kind = %v", err)
	}
	ok, _ := q.CreateDeployment(ctx, store.CreateDeploymentParams{ID: "00000000-0000-4000-8000-000000000002", ProjectID: p.ID, Source: "cli", CreatedBy: &u.ID, SourceKey: &key, SourceSize: &size, SourceSha256: &sum})
	_, _ = pool.Exec(ctx, "UPDATE deployments SET status = 'ready', finished_at = created_at + interval '9 seconds' WHERE id = $1", ok.ID)
	if err := n.Handle(ctx, job(t, bus.DeploySucceeded, ok.ID, "")); err != nil {
		t.Fatal(err)
	}
	if len(box.sent) != 2 || len(push.sent) != 3 || !strings.Contains(push.sent[2], `"title":"shop is live"`) || !strings.Contains(push.sent[2], "is live after 9s") {
		t.Fatalf("emails = %d, pushes = %v", len(box.sent), push.sent)
	}

	list, unread, err := inbox.List(ctx, u.ID, 10)
	if err != nil || len(list) != 3 || unread != 3 || list[0].Title != "shop is live" {
		t.Fatalf("bell = %d entries, %d unread, %v", len(list), unread, err)
	}
	_ = inbox.MarkRead(ctx, u.ID)
	if _, unread, _ := inbox.List(ctx, u.ID, 10); unread != 0 {
		t.Fatalf("unread after reading = %d", unread)
	}

	// Someone who left the team isn't told.
	if _, err := pool.Exec(ctx, "DELETE FROM memberships WHERE user_id = $1", u.ID); err != nil {
		t.Fatal(err)
	}
	other, _ := q.CreateDeployment(ctx, store.CreateDeploymentParams{ID: "00000000-0000-4000-8000-000000000003", ProjectID: p.ID, Source: "cli", CreatedBy: &u.ID, SourceKey: &key, SourceSize: &size, SourceSha256: &sum})
	if err := n.Handle(ctx, job(t, bus.DeployFailed, other.ID, "")); err != nil {
		t.Fatal(err)
	}
	if list, _, _ := inbox.List(ctx, u.ID, 10); len(list) != 3 {
		t.Fatalf("a former member got a notification: %d entries", len(list))
	}
}
