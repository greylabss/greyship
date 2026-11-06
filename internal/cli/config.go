package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const DefaultAPIURL = "https://api.greyship.tech"

// Config is what `grey login` saves. The token is a secret, so the file is private to the user (0600).
type Config struct {
	APIURL string `json:"api_url,omitempty"`
	Token  string `json:"token,omitempty"`
	Email  string `json:"email,omitempty"`
}

func DefaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}
	return filepath.Join(dir, "greyship", "config.json"), nil
}

func loadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path) //nolint:gosec // path is the user's own config file, not request input
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

func saveConfig(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	// Write then rename so a crash never leaves a half-written file with a token in it.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	return nil
}

// normalizeServer turns what someone typed after grey login into an API base URL: https unless they typed http://
// (a fresh install without a domain yet), no path, no trailing slash. A self-hosted Greyship serves /v1 on its dashboard's address, so either the
// dashboard's or the API's address works.
func normalizeServer(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return "", fmt.Errorf("%q isn't a server address; use something like https://greyship.example.com", raw)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("%q: use https:// (or http:// for a server that has no domain yet)", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

// unencrypted reports whether talking to base sends the token in the clear over a network.
func unencrypted(base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" {
		return false
	}
	return u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1"
}
