package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/secrets"
	"greyship.tech/internal/store"
)

const (
	keyLabel     = "mail:resend"
	refreshEvery = 30 * time.Second
)

var (
	ErrInvalidDomain = errors.New("enter the domain only, like mail.example.com")
	ErrInvalidKey    = errors.New("a Resend API key starts with re_")
	ErrInvalidName   = errors.New("the sender name can't be empty, longer than 64 characters or contain < > \" or line breaks")
	ErrNeedKey       = errors.New("save a Resend API key before turning email on")
	ErrNeedDomain    = errors.New("enter the sending domain before turning email on")
)

var domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// Source says where the active email setup comes from.
const (
	SourceDashboard   = "dashboard"
	SourceEnvironment = "environment"
	SourceNone        = "none"
)

// Dynamic sends through whatever the instance admin configured last (decisions.md 052). Settings saved in the
// dashboard win; with none saved, GREYSHIP_RESEND_API_KEY applies; with neither, mail goes to the outbox folder and
// notification emails are skipped (Dev keeps them as files).
type Dynamic struct {
	Pool   *pgxpool.Pool
	Keys   *secrets.Keyring
	Env    Sender // from GREYSHIP_RESEND_API_KEY; nil when unset
	Outbox Sender
	Dev    bool
	Now    func() time.Time
	Logger *slog.Logger
	// Endpoint overrides Resend's address in tests.
	Endpoint string

	mu       sync.Mutex
	loaded   bool
	loadedAt time.Time
	cur      state
}

type state struct {
	sender  Sender
	source  string
	enabled bool // notification emails go out
	domain  string
	name    string
	toggle  bool // the dashboard switch
	keySet  bool
	saved   bool // a dashboard row exists
}

// Status is what the admin page shows. It never carries the key.
type Status struct {
	Enabled       bool   `json:"enabled"` // the dashboard switch
	Active        bool   `json:"active"`  // email is really being sent
	Source        string `json:"source"`
	SendingDomain string `json:"sending_domain"`
	FromName      string `json:"from_name"`
	KeySet        bool   `json:"key_set"`
}

// Settings is what the admin saves. An empty APIKey keeps the stored one.
type Settings struct {
	Enabled       bool
	SendingDomain string
	FromName      string
	APIKey        string
}

func (d *Dynamic) current(ctx context.Context) state {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.loaded && d.Now().Sub(d.loadedAt) < refreshEvery {
		return d.cur
	}
	st, err := d.load(ctx)
	if err != nil {
		d.Logger.Warn("load email settings", "err", err)
		if d.loaded {
			return d.cur
		}
		return d.fallback()
	}
	d.cur, d.loaded, d.loadedAt = st, true, d.Now()
	return st
}

func (d *Dynamic) fallback() state {
	switch {
	case d.Env != nil:
		return state{sender: d.Env, source: SourceEnvironment, enabled: true}
	default:
		return state{sender: d.Outbox, source: SourceNone, enabled: d.Dev}
	}
}

func (d *Dynamic) load(ctx context.Context) (state, error) {
	row, err := store.New(d.Pool).GetMailSettings(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return d.fallback(), nil
	}
	if err != nil {
		return state{}, fmt.Errorf("read mail settings: %w", err)
	}
	st := state{sender: d.Outbox, source: SourceNone, domain: row.SendingDomain, name: row.FromName, toggle: row.Enabled, keySet: len(row.ApiKeyEnc) > 0, saved: true}
	if !row.Enabled || !st.keySet || row.SendingDomain == "" {
		return st, nil
	}
	key, err := d.Keys.Open(keyLabel, row.ApiKeyEnc)
	if err != nil {
		d.Logger.Warn("can't decrypt the saved email API key; email is off until it is saved again", "err", err)
		return st, nil
	}
	r := NewResend(string(key), "")
	if d.Endpoint != "" {
		r.Endpoint = d.Endpoint
	}
	st.sender, st.source, st.enabled = r, SourceDashboard, true
	return st, nil
}

func (d *Dynamic) forget() {
	d.mu.Lock()
	d.loaded = false
	d.mu.Unlock()
}

// Enabled reports whether notification emails should be sent.
func (d *Dynamic) Enabled(ctx context.Context) bool { return d.current(ctx).enabled }

// Send picks the sender at the moment of sending. Dashboard settings send from login@ (codes) or
// notifications@ (messages that name their own sender) on the sending domain.
func (d *Dynamic) Send(ctx context.Context, m Message) error {
	st := d.current(ctx)
	if st.source == SourceDashboard {
		local := "login"
		if m.From != "" {
			local = "notifications"
		}
		m.From = fmt.Sprintf("%s <%s@%s>", st.name, local, st.domain)
	}
	return st.sender.Send(ctx, m)
}

// Status reports the current setup.
func (d *Dynamic) Status(ctx context.Context) Status {
	st := d.current(ctx)
	name := st.name
	if name == "" {
		name = "Greyship"
	}
	return Status{Enabled: st.toggle, Active: st.enabled && st.source != SourceNone, Source: st.source, SendingDomain: st.domain, FromName: name, KeySet: st.keySet}
}

// Save validates and stores settings, and applies them at once.
func (d *Dynamic) Save(ctx context.Context, in Settings) error {
	domain := strings.ToLower(strings.TrimSpace(in.SendingDomain))
	if domain != "" && !domainPattern.MatchString(domain) {
		return ErrInvalidDomain
	}
	name := strings.TrimSpace(in.FromName)
	if name == "" {
		name = "Greyship"
	}
	if len(name) > 64 || strings.ContainsAny(name, "<>\"\r\n") {
		return ErrInvalidName
	}
	key := strings.TrimSpace(in.APIKey)
	if key != "" && (!strings.HasPrefix(key, "re_") || len(key) > 200 || strings.ContainsAny(key, " \t\r\n")) {
		return ErrInvalidKey
	}
	q := store.New(d.Pool)
	var sealed []byte
	if key != "" {
		var err error
		if sealed, err = d.Keys.Seal(keyLabel, []byte(key)); err != nil {
			return fmt.Errorf("seal email API key: %w", err)
		}
	}
	if in.Enabled {
		stored := key != ""
		if !stored {
			row, err := q.GetMailSettings(ctx)
			stored = err == nil && len(row.ApiKeyEnc) > 0
		}
		if !stored {
			return ErrNeedKey
		}
		if domain == "" {
			return ErrNeedDomain
		}
	}
	err := q.SaveMailSettings(ctx, store.SaveMailSettingsParams{Enabled: in.Enabled, ApiKeyEnc: sealed, SendingDomain: domain, FromName: name})
	if err != nil {
		return fmt.Errorf("save mail settings: %w", err)
	}
	d.forget()
	return nil
}

// SendTest sends one message to the admin and returns the provider's error, so a wrong key or an unverified domain
// shows up at once. It bypasses the queue.
func (d *Dynamic) SendTest(ctx context.Context, to string) error {
	st := d.current(ctx)
	if st.source == SourceNone {
		return errors.New("email is off: turn it on and save first")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return d.Send(ctx, Message{To: to, Subject: "Greyship test email", Text: "Email works on your Greyship instance."})
}
