// Package guestinit is PID 1 inside every instance (architecture §2.5). It reads the instance's config from
// stdin, starts the app with exactly that environment, forwards stop signals and reaps orphaned processes.
//
// Config comes over stdin rather than as container environment because the container runtime stores its
// environment on disk, and decrypted secrets must never be written anywhere.
package guestinit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"syscall"
)

const maxConfig = 1 << 20

type Config struct {
	Env  map[string]string `json:"env"`
	Args []string          `json:"args"`
	// Serve is the port the router reaches for a web service. The app may listen on any port; see doorman.go.
	Serve int `json:"serve,omitempty"`
}

// ReadConfig reads the one JSON line the agent writes before anything else.
func ReadConfig(r io.Reader) (Config, error) {
	line, err := bufio.NewReaderSize(io.LimitReader(r, maxConfig), 64<<10).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(line, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	return cfg, nil
}

func (c Config) environ() []string {
	keys := make([]string, 0, len(c.Env))
	for k := range c.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+c.Env[k])
	}
	return out
}

var forwarded = []os.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT}

// Run starts app and waits for it, passing stop signals on. It returns the exit code the container should end with.
func Run(app string, cfg Config, stdout, stderr io.Writer) (int, error) {
	cmd := exec.Command(app, cfg.Args...) //nolint:gosec // app is the fixed /app path; args come from the agent
	cmd.Env = cfg.environ()
	cmd.Stdout, cmd.Stderr = stdout, stderr

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, forwarded...)
	defer signal.Stop(sigs)

	if err := cmd.Start(); err != nil {
		return 127, fmt.Errorf("start %s: %w", app, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	doorDone := make(chan struct{})
	go func() {
		defer close(doorDone)
		if cfg.Serve > 0 {
			newDoorman(cfg.Serve, cfg.Env["GREYSHIP_SERVICE"], stdout).run(ctx)
		}
	}()
	defer func() {
		cancel()
		<-doorDone
	}()

	done := make(chan int, 1)
	go func() { done <- wait(cmd) }()
	for {
		select {
		case code := <-done:
			return code, nil
		case s := <-sigs:
			_ = cmd.Process.Signal(s)
		}
	}
}

func exitCode(state *os.ProcessState) int {
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return state.ExitCode()
}
