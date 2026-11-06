// Package agent is the node agent's instance logic (architecture §2.4): turn an artifact into a running,
// health-checked instance with the right environment and limits, and stop it again.
package agent

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"greyship.tech/internal/pack"
	"greyship.tech/internal/runtime"
)

const (
	// The app's port inside its sandbox. Every instance has its own network namespace, so it is always the same.
	appPort       = 8080
	webDeadline   = 30 * time.Second // F-25
	workerSettle  = 3 * time.Second
	maxArtifact   = 512 << 20
	DefaultMemory = 128 // MB, the free plan (spec §6)
	DefaultCPU    = 0.25
)

type Storage interface {
	Get(ctx context.Context, key string, limit int64) ([]byte, error)
}

// Images turns guest init plus an app binary into an instance image, reusing it if it already exists.
type Images interface {
	EnsureImage(ctx context.Context, tag string, rootfs func() ([]byte, error)) error
}

type Agent struct {
	Driver  runtime.Driver
	Images  Images
	Storage Storage
	Init    []byte // greyship-init for this node's CPU
	Region  string
	Logger  *slog.Logger
	NewID   func() (string, error)
}

type Artifact struct {
	ObjectKey string
	SHA256    string
}

// filesDir is where a deployment's files bundle sits in the image, and the app's working folder when there is
// one, so the repo-relative paths in greyship.toml's files resolve as they do on the developer's machine.
const filesDir = "srv"

type Instance struct {
	ID           string // the scheduler's instance id; a new one is made when empty
	DeploymentID string
	Service      string
	Type         string // web | worker | cron | release
	Artifact     Artifact
	Files        *Artifact // the deployment's files bundle (greyship.toml's files), if it has one
	MemoryMB     int
	CPU          float64
	Env          map[string]string // the user's variables, already decrypted
}

type Running struct {
	ID   string
	Addr string // where the node reaches a web instance; empty for workers
	inst *runtime.Instance
	tail *lastLines
}

// Output is the instance's last lines, to explain an exit.
func (r Running) Output() []string { return r.tail.lines() }

// Exited reports when the instance stops on its own.
func (r Running) Exited() <-chan runtime.Exit { return r.inst.Exited }

// HealthError means the instance started but didn't become healthy; it has been stopped.
type HealthError struct {
	Reason string
	Output []string // the app's last lines, to show the user why
}

func (e *HealthError) Error() string { return "health check failed: " + e.Reason }

// Env is the instance's environment (F-39, F-82). The user's GOMAXPROCS and GOMEMLIMIT win; PORT and
// GREYSHIP_* are the platform's and can't be overridden.
func Env(in Instance, region string) map[string]string {
	env := map[string]string{
		"GOMAXPROCS": strconv.Itoa(max(1, int(math.Ceil(in.CPU)))),
		"GOMEMLIMIT": strconv.Itoa(in.MemoryMB*9/10) + "MiB",
	}
	for k, v := range in.Env {
		if k == "PORT" || strings.HasPrefix(k, "GREYSHIP_") {
			continue
		}
		env[k] = v
	}
	env["GREYSHIP_SERVICE"] = in.Service
	env["GREYSHIP_DEPLOYMENT_ID"] = in.DeploymentID
	env["GREYSHIP_REGION"] = region
	if in.Type == "web" {
		env["PORT"] = strconv.Itoa(appPort)
	}
	return env
}

// Start runs the instance and returns once it is healthy. On failure nothing is left running.
func (a *Agent) Start(ctx context.Context, in Instance, logs runtime.LogFunc) (Running, error) {
	if in.MemoryMB == 0 {
		in.MemoryMB = DefaultMemory
	}
	if in.CPU == 0 {
		in.CPU = DefaultCPU
	}
	image, err := a.image(ctx, in.Artifact, in.Files)
	if err != nil {
		return Running{}, err
	}
	id := in.ID
	if id == "" {
		if id, err = a.NewID(); err != nil {
			return Running{}, err
		}
	}
	initCfg := map[string]any{"env": Env(in, a.Region), "args": []string{}}
	if in.Type == "web" {
		initCfg["serve"] = appPort
	}
	cfg, err := json.Marshal(initCfg)
	if err != nil {
		return Running{}, fmt.Errorf("encode instance config: %w", err)
	}
	spec := runtime.Spec{ID: id, Image: image, MemoryMB: in.MemoryMB, CPU: in.CPU, Config: cfg}
	if in.Files != nil {
		spec.WorkDir = "/" + filesDir
	}
	if in.Type == "web" {
		spec.Port = appPort
	}

	tail := &lastLines{n: 20}
	inst, err := a.Driver.Start(ctx, spec, func(stream, line string) {
		tail.add(line)
		logs(stream, line)
	})
	if err != nil {
		return Running{}, fmt.Errorf("start %s: %w", in.Service, err)
	}
	run := Running{ID: id, Addr: inst.Addr, inst: inst, tail: tail}
	if err := a.healthy(ctx, in.Type, inst); err != nil {
		_ = a.Driver.Stop(context.Background(), id)
		var he *HealthError
		if errors.As(err, &he) {
			he.Output = tail.lines()
		}
		return Running{}, err
	}
	a.Logger.Info("instance healthy", "instance", id, "service", in.Service, "deployment", in.DeploymentID)
	return run, nil
}

func (a *Agent) Stop(ctx context.Context, r Running) error {
	return a.Driver.Stop(ctx, r.ID)
}

// StopID stops an instance known only by id, for example one started before the agent last restarted.
func (a *Agent) StopID(ctx context.Context, id string) error {
	return a.Driver.Stop(ctx, id)
}

// image builds the instance's filesystem: guest init and the app, nothing else (F-83). The tag is the
// artifact's checksum, so each binary is imported once per node and then reused.
func (a *Agent) image(ctx context.Context, art Artifact, files *Artifact) (string, error) {
	// The image holds guest init and the files bundle as well as the app, so its name covers all three: a new
	// init (after a release) or other files get a new image instead of reusing one made with the old ones.
	initSum := sha256.Sum256(a.Init)
	name := hex.EncodeToString(initSum[:]) + art.SHA256
	if files != nil {
		name += files.SHA256
	}
	tagSum := sha256.Sum256([]byte(name))
	tag := "gs-app:" + hex.EncodeToString(tagSum[:])
	err := a.Images.EnsureImage(ctx, tag, func() ([]byte, error) { return a.rootfs(ctx, art, files) })
	if err != nil {
		return "", fmt.Errorf("prepare image for %s: %w", art.ObjectKey, err)
	}
	return tag, nil
}

func (a *Agent) rootfs(ctx context.Context, art Artifact, files *Artifact) ([]byte, error) {
	bin, err := a.Storage.Get(ctx, art.ObjectKey, maxArtifact)
	if err != nil {
		return nil, fmt.Errorf("download artifact %s: %w", art.ObjectKey, err)
	}
	if sum := sha256.Sum256(bin); hex.EncodeToString(sum[:]) != art.SHA256 {
		return nil, fmt.Errorf("artifact %s doesn't match its checksum", art.ObjectKey)
	}
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for _, f := range []struct {
		name string
		data []byte
	}{{"greyship-init", a.Init}, {"app", bin}} {
		hdr := &tar.Header{Typeflag: tar.TypeReg, Name: f.name, Mode: 0o555, Size: int64(len(f.data)), Uid: 65534, Gid: 65534, ModTime: time.Unix(0, 0)}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, err
		}
	}
	if files != nil {
		bundle, err := a.Storage.Get(ctx, files.ObjectKey, pack.MaxBundleBytes)
		if err != nil {
			return nil, fmt.Errorf("download files %s: %w", files.ObjectKey, err)
		}
		if sum := sha256.Sum256(bundle); hex.EncodeToString(sum[:]) != files.SHA256 {
			return nil, fmt.Errorf("files %s don't match their checksum", files.ObjectKey)
		}
		if err := addFiles(tw, bundle); err != nil {
			return nil, fmt.Errorf("files %s: %w", files.ObjectKey, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// healthy implements F-25. A web app must accept connections on its port within 30s; a worker must still be
// running after a few seconds. An app that exits first fails at once.
func (a *Agent) healthy(ctx context.Context, typ string, inst *runtime.Instance) error {
	if typ == "release" {
		// A release command is meant to exit; its exit code is the result, not a health check.
		return nil
	}
	if typ != "web" {
		select {
		case ex := <-inst.Exited:
			return &HealthError{Reason: fmt.Sprintf("the app exited with code %d", ex.Code)}
		case <-time.After(workerSettle):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	deadline := time.Now().Add(webDeadline)
	for {
		if listening(inst.Addr) {
			return nil
		}
		select {
		case ex := <-inst.Exited:
			return &HealthError{Reason: fmt.Sprintf("the app exited with code %d before it started listening", ex.Code)}
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return &HealthError{Reason: "the app didn't start listening on any port within 30s; a web service must accept connections (any port works)"}
		}
	}
}

// listening reports whether the app accepts connections. The published port is fronted by a proxy that
// accepts on its own and closes once it finds nothing behind it, so a connection that stays open is the signal.
func listening(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, err = conn.Read(make([]byte, 1))
	var ne net.Error
	return err == nil || errors.As(err, &ne) && ne.Timeout()
}

type lastLines struct {
	n   int
	mu  sync.Mutex
	buf []string
}

func (l *lastLines) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, s)
	if len(l.buf) > l.n {
		l.buf = l.buf[len(l.buf)-l.n:]
	}
}

func (l *lastLines) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.buf...)
}
