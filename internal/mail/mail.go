// Package mail sends transactional email: Resend in production, a local outbox folder in development.
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Message struct {
	From    string // optional; the sender's default otherwise
	To      string
	Subject string
	Text    string
}

// Sender sends one message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Resend sends through https://resend.com's HTTP API.
type Resend struct {
	APIKey string
	From   string
	Client *http.Client
	// Endpoint is overridable for tests.
	Endpoint string
}

func NewResend(apiKey, from string) *Resend {
	return &Resend{APIKey: apiKey, From: from, Client: &http.Client{Timeout: 10 * time.Second}, Endpoint: "https://api.resend.com/emails"}
}

func (r *Resend) Send(ctx context.Context, m Message) error {
	from := r.From
	if m.From != "" {
		from = m.From
	}
	body, err := json.Marshal(map[string]any{"from": from, "to": []string{m.To}, "subject": m.Subject, "text": m.Text})
	if err != nil {
		return fmt.Errorf("encode email: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build resend request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.Client.Do(req)
	if err != nil {
		return fmt.Errorf("send via resend: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		// The response body explains the failure and never contains the message content.
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("resend returned %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

// Outbox writes each message to a file so development needs no mail provider.
// Codes are secrets, so they go to files the developer opens, never to logs.
type Outbox struct {
	Dir string
	Now func() time.Time
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9@._-]`)

func (o *Outbox) Send(_ context.Context, m Message) error {
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return fmt.Errorf("create outbox %s: %w", o.Dir, err)
	}
	name := fmt.Sprintf("%s-%s.eml", o.Now().UTC().Format("20060102T150405.000"), unsafeChars.ReplaceAllString(m.To, "_"))
	content := fmt.Sprintf("To: %s\r\nSubject: %s\r\n\r\n%s\r\n", m.To, m.Subject, m.Text)
	if err := os.WriteFile(filepath.Join(o.Dir, name), []byte(content), 0o600); err != nil {
		return fmt.Errorf("write outbox message: %w", err)
	}
	return nil
}
