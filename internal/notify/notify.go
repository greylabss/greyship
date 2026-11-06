// Package notify tells people when a deploy succeeds or fails and when a project is created (decisions.md 046):
// a bell entry in the dashboard, an email, and a browser push, each per the person's settings. It handles the
// notify jobs that the scheduler, builder and project service enqueue in the same transaction as the event.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/mail"
	"greyship.tech/internal/store"
	"greyship.tech/internal/webpush"
)

type Pusher interface {
	Send(ctx context.Context, sub webpush.Subscription, payload []byte) error
}

type Notifier struct {
	Pool         *pgxpool.Pool
	Mail         mail.Sender
	From         string // the sender address for notification emails
	Push         Pusher // nil when browser push isn't configured
	DashboardURL string
	Logger       *slog.Logger
}

// event is one notification before it's addressed: what happened, for whom, and where to look.
type event struct {
	kind, subject, user, team string
	project, deployment       *string
	title, body, link         string
}

// Handle sends one notification. A retried job finds the row it already made and only sends what's still unsent.
func (n *Notifier) Handle(ctx context.Context, job bus.Job) error {
	var payload struct {
		Kind    string `json:"kind"`
		Subject string `json:"subject"`
		User    string `json:"user"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("decode job %d: %w", job.ID, err)
	}
	q := store.New(n.Pool)
	ev, ok, err := n.compose(ctx, q, payload.Kind, payload.Subject, payload.User)
	if err != nil || !ok {
		return err
	}
	to, err := q.MemberEmail(ctx, store.MemberEmailParams{UserID: ev.user, TeamID: ev.team})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // no longer on the team
	}
	if err != nil {
		return fmt.Errorf("load recipient: %w", err)
	}
	row, err := q.CreateNotification(ctx, store.CreateNotificationParams{
		UserID: ev.user, TeamID: ev.team, Kind: ev.kind, SubjectID: ev.subject, ProjectID: ev.project, DeploymentID: ev.deployment,
		Title: ev.title, Body: ev.body, Link: ev.link,
	})
	if err != nil {
		return fmt.Errorf("record notification: %w", err)
	}
	settings, err := q.NotificationSetting(ctx, store.NotificationSettingParams{UserID: ev.user, Kind: ev.kind})
	if err != nil {
		return fmt.Errorf("load notification settings: %w", err)
	}
	if settings.Email && row.EmailedAt == nil && n.Mail != nil {
		msg := mail.Message{From: n.From, To: to, Subject: ev.title, Text: ev.body + "\n\n" + n.DashboardURL + ev.link + "\n\n" + footer}
		if err := n.Mail.Send(ctx, msg); err != nil {
			return fmt.Errorf("email notification %s: %w", row.ID, err)
		}
		if err := q.MarkNotificationEmailed(ctx, row.ID); err != nil {
			return err
		}
	}
	if settings.Push && row.PushedAt == nil && n.Push != nil {
		n.push(ctx, q, ev)
		if err := q.MarkNotificationPushed(ctx, row.ID); err != nil {
			return err
		}
	}
	return nil
}

const footer = "You get these for deploys you start and projects you create on Greyship. Turn them off in Settings > Notifications, or with grey notifications email off."

// push sends to every browser the person turned notifications on in. One broken browser doesn't hold up the
// others or retry the job; a gone one is forgotten.
func (n *Notifier) push(ctx context.Context, q *store.Queries, ev event) {
	subs, err := q.ListPushSubscriptions(ctx, ev.user)
	if err != nil {
		n.Logger.Error("list push subscriptions", "err", err)
		return
	}
	payload, _ := json.Marshal(map[string]string{"title": ev.title, "body": ev.body, "url": n.DashboardURL + ev.link, "tag": ev.kind + ":" + ev.subject})
	for _, s := range subs {
		err := n.Push.Send(ctx, webpush.Subscription{Endpoint: s.Endpoint, P256dh: s.P256dh, Auth: s.Auth}, payload)
		switch {
		case errors.Is(err, webpush.ErrGone):
			_ = q.DeletePushSubscriptionByEndpoint(ctx, s.Endpoint)
		case err != nil:
			n.Logger.Info("push failed", "subscription", s.ID, "err", err)
		}
	}
}

func (n *Notifier) compose(ctx context.Context, q *store.Queries, kind, subject, user string) (event, bool, error) {
	switch kind {
	case bus.DeploySucceeded, bus.DeployFailed:
		return composeDeploy(ctx, q, kind, subject)
	case bus.ProjectCreated:
		p, err := q.ProjectEvent(ctx, subject)
		if errors.Is(err, pgx.ErrNoRows) {
			return event{}, false, nil
		}
		if err != nil {
			return event{}, false, fmt.Errorf("load project %s: %w", subject, err)
		}
		body := fmt.Sprintf("%s was created in %s.", p.Slug, p.TeamSlug)
		if p.Repo != "" {
			body = fmt.Sprintf("%s was created in %s from github.com/%s.", p.Slug, p.TeamSlug, p.Repo)
		}
		return event{kind: kind, subject: p.ID, user: user, team: p.TeamID, project: &p.ID,
			title: "New project " + p.Slug, body: body, link: "/" + p.TeamSlug + "/" + p.Slug}, user != "", nil
	}
	return event{}, false, fmt.Errorf("unknown notification kind %q", kind)
}

func composeDeploy(ctx context.Context, q *store.Queries, kind, id string) (event, bool, error) {
	d, err := q.DeploymentEvent(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return event{}, false, nil
	}
	if err != nil {
		return event{}, false, fmt.Errorf("load deployment %s: %w", id, err)
	}
	user := d.GithubUser
	if d.CreatedBy != nil {
		user = *d.CreatedBy
	}
	if user == "" {
		return event{}, false, nil
	}
	what := "Deployment " + short(d.ID, 8)
	if d.CommitSha != nil && *d.CommitSha != "" {
		what += " (commit " + short(*d.CommitSha, 7) + ")"
	}

	ev := event{kind: kind, subject: d.ID, user: user, team: d.TeamID, project: &d.ProjectID, deployment: &d.ID,
		link: fmt.Sprintf("/%s/%s/deployments/%s", d.TeamSlug, d.ProjectSlug, d.ID)}
	if kind == bus.DeploySucceeded {
		ev.title = d.ProjectSlug + " is live"
		ev.body = what + " is live" + took(d) + "."
		if d.RollbackOf != nil {
			ev.body += " It runs the binaries of " + short(*d.RollbackOf, 8) + "; nothing was rebuilt."
		}
		return ev, true, nil
	}
	reason := "see the build log"
	if d.Error != nil && *d.Error != "" {
		reason = *d.Error
	}
	ev.title = d.ProjectSlug + ": deploy failed"
	ev.body = fmt.Sprintf("%s failed: %s.\n%s", what, strings.TrimSuffix(reason, "."), keepsServing(ctx, q, d.ProjectID, d.ID))
	return ev, true, nil
}

func keepsServing(ctx context.Context, q *store.Queries, projectID, failed string) string {
	rel, err := q.GetRelease(ctx, store.GetReleaseParams{ProjectID: projectID, Env: "production"})
	if err != nil || rel.DeploymentID == failed {
		return "Nothing changed: this project has no live version yet."
	}
	return "Nothing changed: the live version (deployment " + short(rel.DeploymentID, 8) + ") keeps serving."
}

func took(d store.DeploymentEventRow) string {
	if d.FinishedAt == nil {
		return ""
	}
	s := int(d.FinishedAt.Sub(d.CreatedAt).Seconds())
	if s < 60 {
		return fmt.Sprintf(" after %ds", s)
	}
	return fmt.Sprintf(" after %dm %ds", s/60, s%60)
}

func short(s string, n int) string { return s[:min(n, len(s))] }
