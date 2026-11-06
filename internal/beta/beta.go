// Package beta asks the private-beta app (beta/, decisions.md 049) whether an email is on the tester list. During
// the private beta that list decides who may create an account, and its people get a beta tester badge.
package beta

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client calls the beta app's POST /api/testers/check with the shared secret.
type Client struct {
	URL    string // e.g. https://forms.greyship.tech
	Secret string
	HTTP   *http.Client
}

func New(url, secret string) *Client {
	return &Client{URL: strings.TrimRight(url, "/"), Secret: secret, HTTP: &http.Client{Timeout: 5 * time.Second}}
}

// IsTester reports whether email is on the beta list (anyone who applied, unless declined).
func (c *Client) IsTester(ctx context.Context, email string) (bool, error) {
	body, err := json.Marshal(map[string]string{"email": email})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/api/testers/check", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, fmt.Errorf("ask the beta list: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("the beta list answered %d", resp.StatusCode)
	}
	var out struct {
		Tester bool `json:"tester"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out); err != nil {
		return false, fmt.Errorf("read the beta list's answer: %w", err)
	}
	return out.Tester, nil
}
