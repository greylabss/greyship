// Package gvisor runs instances as runsc containers through the docker CLI (architecture §5, Stage 0–1).
package gvisor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"greyship.tech/internal/runtime"
)

type Driver struct {
	Docker string // path to the docker binary
	Host   string // DOCKER_HOST of the node's docker, which has runsc
	// Shared is a folder on the Docker host holding resolv.conf and ca-certificates.crt (deploy/sandbox-init.sh),
	// mounted read-only into every instance. Docker's embedded DNS (127.0.0.11) doesn't answer under gVisor, even
	// with --dns, and the image holds only guest init and the app, so without these apps can't resolve or verify TLS.
	Shared string

	mu      sync.Mutex
	running map[string]chan struct{} // closed when the instance's docker run process has exited
}

var safeID = regexp.MustCompile(`^[0-9a-f-]{36}$`)

func name(id string) string { return "gs-i-" + id }

func (d *Driver) docker(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, d.Docker, args...) //nolint:gosec // fixed binary; arguments are separate and validated
	cmd.Env = []string{"DOCKER_HOST=" + d.Host, "HOME=/tmp"}
	return cmd
}

func (d *Driver) run(ctx context.Context, args ...string) (string, error) {
	var stderr bytes.Buffer
	cmd := d.docker(ctx, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// args are the docker run flags for one instance. Each instance gets its own bridge network, so instances can't
// reach each other; its port is published on the node's loopback only.
func (d *Driver) args(spec runtime.Spec) []string {
	args := []string{
		"run", "-i", "--rm", "--name", name(spec.ID),
		"--runtime=runsc",
		"--network", name(spec.ID),
		"--mount", "type=bind,source=" + d.Shared + "/resolv.conf,target=/etc/resolv.conf,readonly",
		"--mount", "type=bind,source=" + d.Shared + "/ca-certificates.crt,target=/etc/ssl/certs/ca-certificates.crt,readonly",
		"--read-only", "--tmpfs", "/tmp:rw,size=64m",
		"--user", "65534:65534",
		"--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--memory=" + strconv.Itoa(spec.MemoryMB) + "m", "--memory-swap=" + strconv.Itoa(spec.MemoryMB) + "m",
		"--cpus=" + strconv.FormatFloat(spec.CPU, 'f', 2, 64),
		"--pids-limit=256",
		"--label", "greyship=instance",
	}
	if spec.Port > 0 {
		args = append(args, "-p", "127.0.0.1::"+strconv.Itoa(spec.Port))
	}
	if spec.WorkDir != "" {
		args = append(args, "--workdir", spec.WorkDir)
	}
	return append(args, spec.Image, "/greyship-init")
}

func (d *Driver) Start(ctx context.Context, spec runtime.Spec, logs runtime.LogFunc) (*runtime.Instance, error) {
	if !safeID.MatchString(spec.ID) {
		return nil, errors.New("invalid instance id")
	}
	if spec.MemoryMB <= 0 || spec.CPU <= 0 {
		return nil, errors.New("instance needs memory and CPU limits")
	}
	// Left over from a crash: same id, so remove before starting again.
	_, _ = d.run(ctx, "rm", "-f", name(spec.ID))
	_, _ = d.run(ctx, "network", "rm", name(spec.ID))
	if _, err := d.run(ctx, "network", "create", "--label", "greyship=instance", name(spec.ID)); err != nil {
		return nil, err
	}

	// The instance outlives the request that started it, so it doesn't use ctx. Stop ends it.
	cmd := d.docker(context.Background(), d.args(spec)...)
	cmd.Stdin = bytes.NewReader(append(append([]byte(nil), spec.Config...), '\n'))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_, _ = d.run(ctx, "network", "rm", name(spec.ID))
		return nil, fmt.Errorf("start instance %s: %w", spec.ID, err)
	}

	var pipes sync.WaitGroup
	for stream, r := range map[string]io.Reader{"stdout": stdout, "stderr": stderr} {
		pipes.Add(1)
		go func() {
			defer pipes.Done()
			sc := bufio.NewScanner(r)
			sc.Buffer(make([]byte, 64<<10), 1<<20)
			for sc.Scan() {
				logs(stream, sc.Text())
			}
		}()
	}
	exited := make(chan runtime.Exit, 1)
	done := make(chan struct{})
	d.mu.Lock()
	if d.running == nil {
		d.running = map[string]chan struct{}{}
	}
	d.running[spec.ID] = done
	d.mu.Unlock()
	// The waiter outlives Start: it belongs to the instance, which Stop ends.
	go func() { //nolint:gosec // detached from the request on purpose
		pipes.Wait()
		err := cmd.Wait()
		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code, err = exitErr.ExitCode(), nil
		}
		_, _ = d.run(context.Background(), "network", "rm", name(spec.ID))
		d.mu.Lock()
		delete(d.running, spec.ID)
		d.mu.Unlock()
		close(done)
		exited <- runtime.Exit{Code: code, Err: err}
	}()

	inst := &runtime.Instance{ID: spec.ID, Exited: exited}
	if spec.Port > 0 {
		addr, err := d.publishedAddr(ctx, spec, done)
		switch {
		case errors.Is(err, errExited):
			// Not the driver's failure to report: Exited says how the app ended, and the caller has its output.
		case err != nil:
			_ = d.Stop(context.Background(), spec.ID)
			return nil, err
		}
		inst.Addr = addr
	}
	return inst, nil
}

var errExited = errors.New("instance exited")

// publishedAddr waits for docker to assign the loopback port, which happens once the container is created.
func (d *Driver) publishedAddr(ctx context.Context, spec runtime.Spec, done <-chan struct{}) (string, error) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, err := d.run(ctx, "port", name(spec.ID), strconv.Itoa(spec.Port)+"/tcp")
		if err == nil && out != "" {
			return strings.Split(out, "\n")[0], nil
		}
		select {
		case <-done:
			return "", errExited
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("instance %s never got a port", spec.ID)
		}
	}
}

// Stop asks the app to shut down (SIGTERM through guest init), kills it after 10 seconds, and removes its network.
// Stopping an instance that isn't running is not an error.
func (d *Driver) Stop(ctx context.Context, id string) error {
	if !safeID.MatchString(id) {
		return errors.New("invalid instance id")
	}
	// A paused container can't be stopped; wake it first so it gets its SIGTERM and a chance to finish.
	_, _ = d.run(ctx, "unpause", name(id))
	_, _ = d.run(ctx, "stop", "-t", "10", name(id))
	d.mu.Lock()
	done := d.running[id]
	d.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	_, _ = d.run(ctx, "rm", "-f", name(id))
	_, _ = d.run(ctx, "network", "rm", name(id))
	return nil
}

func (d *Driver) Pause(ctx context.Context, id string) error {
	_, err := d.run(ctx, "pause", name(id))
	return err
}

func (d *Driver) Resume(ctx context.Context, id string) error {
	_, err := d.run(ctx, "unpause", name(id))
	return err
}

// EnsureImage imports the tar that rootfs returns (guest init and the app) as tag, unless the node already has
// it; rootfs is only called when needed. docker import makes a single-layer image with no base.
func (d *Driver) EnsureImage(ctx context.Context, tag string, rootfs func() ([]byte, error)) error {
	if !imageTag.MatchString(tag) {
		return errors.New("invalid image tag")
	}
	if _, err := d.run(ctx, "image", "inspect", "--format", "{{.Id}}", tag); err == nil {
		return nil
	}
	tarball, err := rootfs()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd := d.docker(ctx, "import", "-", tag)
	cmd.Stdin = bytes.NewReader(tarball)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker import: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

var imageTag = regexp.MustCompile(`^gs-app:[0-9a-f]{64}$`)

// StopLeftovers removes every instance container and network on the node. An agent calls it when it starts:
// whatever ran before belongs to a previous agent and is no longer tracked.
func (d *Driver) StopLeftovers(ctx context.Context) error {
	ids, err := d.run(ctx, "ps", "-aq", "--filter", "label=greyship=instance")
	if err != nil {
		return err
	}
	// One at a time, ignoring failures: a container may already be going away on its own (--rm), and one that
	// won't go mustn't stop the agent from starting.
	for _, id := range strings.Fields(ids) {
		// Removing a paused gVisor container can hang, so wake it first.
		_, _ = d.run(ctx, "unpause", id)
		_, _ = d.run(ctx, "rm", "-f", id)
	}
	_, _ = d.run(ctx, "network", "prune", "-f", "--filter", "label=greyship=instance")
	return nil
}
