// Package github talks to GitHub as the Greyship GitHub App (decisions.md 042): installation tokens for reading
// repos and setting commit statuses, user tokens only to prove who installed the app, and webhook signatures.
package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// App is the Greyship GitHub App. API and Web default to github.com; tests point them at a fake.
type App struct {
	ID           int64
	Slug         string
	ClientID     string
	ClientSecret string
	Key          *rsa.PrivateKey
	API          string // https://api.github.com
	Web          string // https://github.com
	Codeload     string // https://codeload.github.com, public repo archives
	HTTP         *http.Client
	Now          func() time.Time

	mu     sync.Mutex
	tokens map[int64]cached
}

type cached struct {
	token   string
	expires time.Time
}

type Installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
	} `json:"account"`
}

type Repo struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

var (
	ErrNotFound = errors.New("not found on GitHub")
	fullName    = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

// ValidRepo reports whether s is an owner/name pair that is safe to put in an API path.
func ValidRepo(s string) bool { return fullName.MatchString(s) && !strings.Contains(s, "..") }

// ParseKey reads the app's private key as GitHub hands it out (PKCS#1), or as PKCS#8.
func ParseKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("the GitHub App key isn't PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse GitHub App key: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the GitHub App key isn't an RSA key")
	}
	return rk, nil
}

// InstallURL is where a person installs the app; GitHub hands state back to the callback.
func (a *App) InstallURL(state string) string {
	return strings.TrimRight(a.Web, "/") + "/apps/" + url.PathEscape(a.Slug) + "/installations/new?state=" + url.QueryEscape(state)
}

// jwt is the app's own short-lived credential, used only to mint installation tokens.
func (a *App) jwt() (string, error) {
	now := a.Now()
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": strconv.FormatInt(a.ID, 10)})
	if err != nil {
		return "", err
	}
	signing := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.Key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign app token: %w", err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// InstallationToken returns a token for one installation, kept in memory until shortly before it expires.
func (a *App) InstallationToken(ctx context.Context, installation int64) (string, error) {
	a.mu.Lock()
	if c, ok := a.tokens[installation]; ok && a.Now().Before(c.expires.Add(-5*time.Minute)) {
		a.mu.Unlock()
		return c.token, nil
	}
	a.mu.Unlock()
	jwt, err := a.jwt()
	if err != nil {
		return "", err
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	path := fmt.Sprintf("/app/installations/%d/access_tokens", installation)
	if err := a.call(ctx, http.MethodPost, path, "Bearer "+jwt, nil, &out); err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.tokens == nil {
		a.tokens = map[int64]cached{}
	}
	a.tokens[installation] = cached{token: out.Token, expires: out.ExpiresAt}
	a.mu.Unlock()
	return out.Token, nil
}

// ExchangeCode turns the code from the install callback into a user token, used once to list what that user
// can access.
func (a *App) ExchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{"client_id": {a.ClientID}, "client_secret": {a.ClientSecret}, "code": {code}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.Web, "/")+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("exchange GitHub code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return "", fmt.Errorf("read GitHub code exchange: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("GitHub refused the code: %s", out.Error)
	}
	return out.AccessToken, nil
}

// UserInstallations lists the app's installations a user can access.
func (a *App) UserInstallations(ctx context.Context, userToken string) ([]Installation, error) {
	var all []Installation
	for page := 1; page <= 10; page++ {
		var out struct {
			Installations []Installation `json:"installations"`
		}
		if err := a.call(ctx, http.MethodGet, fmt.Sprintf("/user/installations?per_page=100&page=%d", page), "Bearer "+userToken, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Installations...)
		if len(out.Installations) < 100 {
			break
		}
	}
	return all, nil
}

// Repos lists the repositories an installation was given.
func (a *App) Repos(ctx context.Context, installation int64) ([]Repo, error) {
	tok, err := a.InstallationToken(ctx, installation)
	if err != nil {
		return nil, err
	}
	var all []Repo
	for page := 1; page <= 10; page++ {
		var out struct {
			Repositories []Repo `json:"repositories"`
		}
		if err := a.call(ctx, http.MethodGet, fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page), "token "+tok, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Repositories...)
		if len(out.Repositories) < 100 {
			break
		}
	}
	return all, nil
}

// BranchHead is the commit a branch points at.
func (a *App) BranchHead(ctx context.Context, installation int64, repo, branch string) (string, error) {
	tok, err := a.InstallationToken(ctx, installation)
	if err != nil {
		return "", err
	}
	var out struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := a.call(ctx, http.MethodGet, "/repos/"+repo+"/branches/"+url.PathEscape(branch), "token "+tok, nil, &out); err != nil {
		return "", err
	}
	return out.Commit.SHA, nil
}

// Tarball downloads a repo at a commit, refusing archives larger than limit.
func (a *App) Tarball(ctx context.Context, installation int64, repo, sha string, limit int64) ([]byte, error) {
	tok, err := a.InstallationToken(ctx, installation)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.API, "/")+"/repos/"+repo+"/tarball/"+url.PathEscape(sha), nil)
	if err != nil {
		return nil, err
	}
	a.headers(req, "token "+tok)
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s@%s: %w", repo, sha, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s@%s: GitHub answered %d", repo, sha, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("download %s@%s: %w", repo, sha, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("the repository archive is larger than %d MB", limit>>20)
	}
	return body, nil
}

// SetStatus sets the commit status Greyship shows on GitHub.
func (a *App) SetStatus(ctx context.Context, installation int64, repo, sha, state, target, description string) error {
	tok, err := a.InstallationToken(ctx, installation)
	if err != nil {
		return err
	}
	body := map[string]string{"state": state, "target_url": target, "description": description, "context": "Greyship"}
	return a.call(ctx, http.MethodPost, "/repos/"+repo+"/statuses/"+url.PathEscape(sha), "token "+tok, body, nil)
}

func (a *App) headers(req *http.Request, auth string) {
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "greyship")
}

func (a *App) call(ctx context.Context, method, path, auth string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.API, "/")+path, r)
	if err != nil {
		return err
	}
	a.headers(req, auth)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("github %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("github %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

// ValidSignature checks a webhook delivery against the app's webhook secret (X-Hub-Signature-256).
func ValidSignature(secret string, body []byte, header string) bool {
	got, ok := strings.CutPrefix(header, "sha256=")
	if !ok || secret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(got), []byte(want))
}

// NewApp is the app on github.com.
func NewApp(id int64, slug, clientID, clientSecret string, keyPEM []byte) (*App, error) {
	key, err := ParseKey(keyPEM)
	if err != nil {
		return nil, err
	}
	return &App{
		ID: id, Slug: slug, ClientID: clientID, ClientSecret: clientSecret, Key: key,
		API: "https://api.github.com", Web: "https://github.com", Codeload: "https://codeload.github.com", HTTP: &http.Client{Timeout: 2 * time.Minute}, Now: time.Now,
	}, nil
}
