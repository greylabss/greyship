package fly

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client calls the Fly Machines REST API for one app. The token is sent only to Fly and never logged.
type Client struct {
	Base   string // https://api.machines.dev/v1
	App    string
	Token  string
	Region string // e.g. fra
	HTTP   *http.Client
}

type Guest struct {
	CPUKind  string `json:"cpu_kind"`
	CPUs     int    `json:"cpus"`
	MemoryMB int    `json:"memory_mb"`
}

type Mount struct {
	Volume string `json:"volume"`
	Path   string `json:"path"`
}

type MachineConfig struct {
	Image       string            `json:"image"`
	Env         map[string]string `json:"env"`
	Guest       Guest             `json:"guest"`
	Mounts      []Mount           `json:"mounts,omitempty"`
	Restart     map[string]string `json:"restart"`
	AutoDestroy bool              `json:"auto_destroy"`
}

type Machine struct {
	ID    string `json:"id"`
	State string `json:"state"` // created | starting | started | stopping | stopped | destroyed | …
}

func (c *Client) CreateVolume(ctx context.Context, name string, sizeGB int) (string, error) {
	var v struct {
		ID string `json:"id"`
	}
	err := c.do(ctx, http.MethodPost, "/volumes", map[string]any{"name": name, "region": c.Region, "size_gb": sizeGB}, &v)
	return v.ID, err
}

func (c *Client) DeleteVolume(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/volumes/"+id, nil, nil)
}

// CreateMachine creates and starts a machine.
func (c *Client) CreateMachine(ctx context.Context, cfg MachineConfig) (Machine, error) {
	var m Machine
	err := c.do(ctx, http.MethodPost, "/machines", map[string]any{"region": c.Region, "config": cfg}, &m)
	return m, err
}

// UpdateMachine replaces a stopped machine's config for its next run.
func (c *Client) UpdateMachine(ctx context.Context, id string, cfg MachineConfig) (Machine, error) {
	var m Machine
	err := c.do(ctx, http.MethodPost, "/machines/"+id, map[string]any{"config": cfg}, &m)
	return m, err
}

func (c *Client) GetMachine(ctx context.Context, id string) (Machine, error) {
	var m Machine
	err := c.do(ctx, http.MethodGet, "/machines/"+id, nil, &m)
	return m, err
}

func (c *Client) StartMachine(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/machines/"+id+"/start", nil, nil)
}

func (c *Client) StopMachine(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/machines/"+id+"/stop", nil, nil)
}

func (c *Client) DestroyMachine(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/machines/"+id+"?force=true", nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Base, "/")+"/apps/"+c.App+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", authorization(c.Token))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("fly %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("fly %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// authorization is the header value for a token: fly tokens create makes "FlyV1 …" tokens, which carry their
// own scheme; older personal tokens are bearer tokens.
func authorization(token string) string {
	token = strings.TrimSpace(token)
	if strings.HasPrefix(token, "FlyV1 ") {
		return token
	}
	return "Bearer " + token
}
