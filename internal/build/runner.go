package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Job is one build handed to a runner: what to build, the source, and where each binary must end up in storage.
type Job struct {
	Spec       Spec
	Source     []byte            // the uploaded tarball, for runners that build here
	SourceKey  string            // where the tarball is stored, for runners that fetch it themselves
	OutputKeys map[string]string // target name → storage key for its binary
}

type Built struct {
	SHA256 string
	Size   int64
}

// Result is what a runner built: every target's binary, already stored at its output key.
type Result struct {
	Binaries    map[string]Built
	Seconds     float64 // metered build time
	Runner      string  // gvisor | fly
	MachineSize string  // the plan's build machine, when the runner has sizes
}

// Failed is a build the user's code failed (it doesn't compile), with the go command's exit code. Runners that
// can't hand back an *exec.ExitError report this instead.
type Failed struct{ Code int }

func (e *Failed) Error() string { return fmt.Sprintf("go build failed (exit status %d)", e.Code) }

// Runner runs a build somewhere: in gVisor on this machine, or on a Fly build machine (decisions.md 039).
// Nothing else in the builder knows which. Errors from the build itself (*exec.ExitError, ErrTimeout) come back
// unwrapped so the builder can tell the user's mistakes from ours.
type Runner interface {
	Run(ctx context.Context, job Job, logLine func(stream, line string)) (Result, error)
}

// Compiler builds the targets from a source tarball and returns the binaries; the gVisor Sandbox is one.
type Compiler interface {
	Run(ctx context.Context, spec Spec, source []byte, logLine func(stream, line string)) (map[string][]byte, error)
}

// LocalRunner builds in a sandbox on this machine and stores the binaries itself.
type LocalRunner struct {
	Compiler Compiler
	Storage  Storage
	Now      func() time.Time
}

func (l *LocalRunner) Run(ctx context.Context, job Job, logLine func(stream, line string)) (Result, error) {
	start := l.Now()
	bins, err := l.Compiler.Run(ctx, job.Spec, job.Source, logLine)
	if err != nil {
		return Result{}, err
	}
	res := Result{Binaries: map[string]Built{}, Seconds: l.Now().Sub(start).Seconds(), Runner: "gvisor"}
	for _, t := range job.Spec.Targets {
		bin := bins[t.Name]
		if err := l.Storage.Put(ctx, job.OutputKeys[t.Name], bytes.NewReader(bin), int64(len(bin))); err != nil {
			return Result{}, fmt.Errorf("store %s binary: %w", t.Name, err)
		}
		sum := sha256.Sum256(bin)
		res.Binaries[t.Name] = Built{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(bin))}
	}
	return res, nil
}
