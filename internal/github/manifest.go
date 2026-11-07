package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var (
	orgName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	appName = regexp.MustCompile(`^[A-Za-z0-9 _.-]{1,34}$`)
)

// ErrBadInput is a value the admin typed that GitHub would refuse or that can't go into a URL.
var ErrBadInput = errors.New("invalid GitHub App setting")

// Manifest is what GitHub needs to create the app in one click (docs.github.com, "Registering a GitHub App from a
// manifest"). The permissions are the ones Greyship uses: read repositories, write commit statuses.
func Manifest(dashboardURL, name, teamID string) map[string]any {
	base := strings.TrimRight(dashboardURL, "/")
	return map[string]any{
		"name":                     name,
		"url":                      base,
		"hook_attributes":          map[string]any{"url": WebhookURL(dashboardURL, teamID), "active": true},
		"redirect_url":             base + "/v1/github/manifest/callback",
		"callback_urls":            []string{base + "/v1/github/callback"},
		"setup_url":                base + "/v1/github/callback",
		"setup_on_update":          true,
		"request_oauth_on_install": true,
		"public":                   false,
		"default_permissions":      map[string]string{"contents": "read", "metadata": "read", "statuses": "write"},
		"default_events":           []string{"push"},
	}
}

// WebhookURL is where GitHub delivers a team's pushes: each team's app has its own address and secret, so a delivery
// can only speak for the team it was signed for.
func WebhookURL(dashboardURL, teamID string) string {
	return strings.TrimRight(dashboardURL, "/") + "/v1/github/webhook/" + teamID
}

// NewAppURL is the page on GitHub that receives the manifest: the admin's own settings, or an organisation's.
func NewAppURL(web, org, state string) (string, error) {
	path := "/settings/apps/new"
	if org != "" {
		if !orgName.MatchString(org) {
			return "", fmt.Errorf("%w: that isn't a GitHub organisation name", ErrBadInput)
		}
		path = "/organizations/" + url.PathEscape(org) + "/settings/apps/new"
	}
	return strings.TrimRight(web, "/") + path + "?state=" + url.QueryEscape(state), nil
}

// CleanAppName checks the name the admin picked: GitHub allows up to 34 characters and the name must be unique.
func CleanAppName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !appName.MatchString(name) {
		return "", fmt.Errorf("%w: use 1 to 34 letters, digits, spaces, dots, dashes or underscores", ErrBadInput)
	}
	return name, nil
}

// Convert trades the one-time code GitHub sends back for the new app's credentials. The code works once, for an
// hour; no authentication is needed. GitHub's error text is returned, but never anything from the response that
// could be a secret.
func Convert(ctx context.Context, client *http.Client, api, code string) (Credentials, error) {
	if code == "" || strings.ContainsAny(code, "/?#% ") {
		return Credentials{}, fmt.Errorf("%w: the code from GitHub is malformed", ErrBadInput)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(api, "/")+"/app-manifests/"+code+"/conversions", nil)
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		return Credentials{}, fmt.Errorf("ask GitHub for the new app: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		ID            int64  `json:"id"`
		Slug          string `json:"slug"`
		Name          string `json:"name"`
		HTMLURL       string `json:"html_url"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
		WebhookSecret string `json:"webhook_secret"`
		PEM           string `json:"pem"`
		Message       string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return Credentials{}, fmt.Errorf("read GitHub's answer (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode >= 300 {
		return Credentials{}, fmt.Errorf("GitHub refused to create the app (HTTP %d): %s", resp.StatusCode, out.Message)
	}
	if out.ID == 0 || out.Slug == "" || out.ClientID == "" || out.ClientSecret == "" || out.WebhookSecret == "" || out.PEM == "" {
		return Credentials{}, errors.New("GitHub's answer was missing parts of the app")
	}
	return Credentials{AppID: out.ID, Slug: out.Slug, Name: out.Name, HTMLURL: out.HTMLURL, ClientID: out.ClientID, ClientSecret: out.ClientSecret, WebhookSecret: out.WebhookSecret, PrivateKeyPEM: out.PEM}, nil
}
