package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// APIError is the /v1 error body: {"error":{"code","message"}}.
type APIError struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
	body    []byte
}

func (e *APIError) Error() string { return e.Message }

// Decode reads extra fields the API sent next to the error, such as the existing project on a 409.
func (e *APIError) Decode(v any) error { return json.Unmarshal(e.body, v) }

type client struct {
	base  string
	token string
	http  *http.Client
	agent string
}

func (c *client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.base, "/")+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", c.agent)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("can't reach %s: %w", c.base, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		var e struct {
			Error APIError `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = json.Unmarshal(raw, &e)
		e.Error.Status, e.Error.body = resp.StatusCode, raw
		if e.Error.Message == "" {
			e.Error.Message = fmt.Sprintf("%s %s failed with status %d", method, path, resp.StatusCode)
		}
		return &e.Error
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode %s response: %w", path, err)
	}
	return nil
}

// stream opens a long-lived GET (a build log) without the client's overall timeout; only ctx ends it.
func (c *client) stream(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.base, "/")+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", c.agent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := (&http.Client{Transport: c.http.Transport}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("can't reach %s: %w", c.base, err)
	}
	if resp.StatusCode >= 400 {
		defer func() { _ = resp.Body.Close() }()
		var e struct {
			Error APIError `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = json.Unmarshal(raw, &e)
		e.Error.Status, e.Error.body = resp.StatusCode, raw
		if e.Error.Message == "" {
			e.Error.Message = fmt.Sprintf("GET %s failed with status %d", path, resp.StatusCode)
		}
		return nil, &e.Error
	}
	return resp, nil
}
