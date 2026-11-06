//go:build integration

package mail_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"greyship.tech/internal/mail"
	"greyship.tech/internal/secrets"
	"greyship.tech/internal/testdb"
)

type sink struct {
	mu   sync.Mutex
	msgs []mail.Message
}

func (s *sink) Send(_ context.Context, m mail.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, m)
	return nil
}

func (s *sink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.msgs)
}

func TestDynamicSender(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	keys, err := secrets.NewKeyring(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	var gotAuth, gotFrom string
	resend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var sent struct{ From string }
		_ = json.Unmarshal(b, &sent)
		gotAuth, gotFrom = r.Header.Get("Authorization"), sent.From
		w.WriteHeader(http.StatusOK)
	}))
	defer resend.Close()

	outbox, env := &sink{}, &sink{}
	d := &mail.Dynamic{Pool: pool, Keys: keys, Outbox: outbox, Now: time.Now, Logger: slog.New(slog.DiscardHandler), Endpoint: resend.URL}

	// Nothing saved, no environment key: outbox only, notification emails off.
	if err := d.Send(ctx, mail.Message{To: "a@example.com"}); err != nil || outbox.count() != 1 || d.Enabled(ctx) || d.Status(ctx).Source != mail.SourceNone {
		t.Fatalf("unconfigured: outbox=%d enabled=%v status=%+v err=%v", outbox.count(), d.Enabled(ctx), d.Status(ctx), err)
	}

	// The environment key applies until something is saved.
	d.Env = env
	d.Save(ctx, mail.Settings{FromName: "Greyship"})
	if err := d.Send(ctx, mail.Message{To: "a@example.com"}); err != nil || env.count() != 0 {
		t.Fatalf("a saved but switched-off setup should replace the environment key: env=%d err=%v", env.count(), err)
	}
	d.Env = nil

	// Validation.
	for name, in := range map[string]mail.Settings{
		"turning on without a key":    {Enabled: true, SendingDomain: "mail.example.com"},
		"turning on without a domain": {Enabled: true, APIKey: "re_abc"},
	} {
		if err := d.Save(ctx, in); err == nil {
			t.Errorf("%s: saved", name)
		}
	}
	if err := d.Save(ctx, mail.Settings{SendingDomain: "https://mail.example.com/"}); !errors.Is(err, mail.ErrInvalidDomain) {
		t.Errorf("a URL as domain = %v", err)
	}
	if err := d.Save(ctx, mail.Settings{APIKey: "sk_nope"}); !errors.Is(err, mail.ErrInvalidKey) {
		t.Errorf("a key without the re_ prefix = %v", err)
	}
	if err := d.Save(ctx, mail.Settings{FromName: "Evil <x@y.z>"}); !errors.Is(err, mail.ErrInvalidName) {
		t.Errorf("an injected sender name = %v", err)
	}

	// Saved and on: sends through Resend with the saved key, from the sending domain; the key is encrypted at rest.
	if err := d.Save(ctx, mail.Settings{Enabled: true, SendingDomain: "Mail.Example.com", FromName: "Acme", APIKey: "re_live_secret"}); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	_ = pool.QueryRow(ctx, "SELECT api_key_enc FROM mail_settings").Scan(&stored)
	if len(stored) == 0 || bytes.Contains(stored, []byte("re_live_secret")) {
		t.Fatal("the API key is stored in plain text")
	}
	if err := d.Send(ctx, mail.Message{To: "a@example.com", Subject: "code"}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer re_live_secret" || gotFrom != "Acme <login@mail.example.com>" {
		t.Fatalf("resend got %q from %q", gotAuth, gotFrom)
	}
	if err := d.Send(ctx, mail.Message{From: "Greyship <n@greyship.tech>", To: "a@example.com"}); err != nil || gotFrom != "Acme <notifications@mail.example.com>" {
		t.Fatalf("notification sender = %q, %v", gotFrom, err)
	}
	st := d.Status(ctx)
	if !st.Active || !st.Enabled || !st.KeySet || st.Source != mail.SourceDashboard || st.SendingDomain != "mail.example.com" {
		t.Fatalf("status = %+v", st)
	}
	if err := d.SendTest(ctx, "me@example.com"); err != nil {
		t.Fatalf("test send: %v", err)
	}

	// Saving without a key keeps the stored one; switching off stops it.
	if err := d.Save(ctx, mail.Settings{Enabled: true, SendingDomain: "mail.example.com", FromName: "Acme"}); err != nil {
		t.Fatalf("save without a key: %v", err)
	}
	if err := d.Save(ctx, mail.Settings{Enabled: false, SendingDomain: "mail.example.com", FromName: "Acme"}); err != nil {
		t.Fatal(err)
	}
	if d.Enabled(ctx) || d.Status(ctx).Active || !d.Status(ctx).KeySet {
		t.Fatalf("switched off: %+v", d.Status(ctx))
	}
	if err := d.SendTest(ctx, "me@example.com"); err == nil {
		t.Fatal("a test send while email is off should fail")
	}
}
