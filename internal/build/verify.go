package build

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"

	"greyship.tech/internal/bus"
	"greyship.tech/internal/store"
)

// MaxBinary is the largest binary a deployment may contain, uncompressed.
const MaxBinary = 200 << 20

// Inflate decompresses an uploaded binary, refusing anything that grows past MaxBinary.
func Inflate(gz []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, errors.New("the upload isn't gzip-compressed")
	}
	bin, err := io.ReadAll(io.LimitReader(r, MaxBinary+1))
	if err != nil {
		return nil, errors.New("the upload is damaged")
	}
	if len(bin) > MaxBinary {
		return nil, fmt.Errorf("the binary is larger than %d MB", MaxBinary>>20)
	}
	return bin, nil
}

// CheckBinary looks at a binary built on a developer's machine and returns the Go version it was built with.
// It checks what a local build can get wrong: built for the developer's own OS or CPU, with cgo, or without
// -trimpath. It is not a security boundary (build info can be faked); every binary runs in gVisor regardless.
func CheckBinary(name string, bin []byte, sha string, goarch string) (goVersion string, err error) {
	if sum := sha256.Sum256(bin); hex.EncodeToString(sum[:]) != sha {
		return "", fmt.Errorf("%s doesn't match its checksum; deploy again", name)
	}
	// The parser reads attacker-controlled bytes; a malformed file must fail the check, not the builder.
	defer func() {
		if p := recover(); p != nil {
			goVersion, err = "", fmt.Errorf("%s isn't a Go binary Greyship can read", name)
		}
	}()
	info, err := buildinfo.Read(bytes.NewReader(bin))
	if err != nil {
		return "", fmt.Errorf("%s isn't a Go binary (built with go build?)", name)
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	goos, arch := settings["GOOS"], settings["GOARCH"]
	switch {
	case goos != "linux" || arch != goarch:
		return "", fmt.Errorf("%s was built for %s/%s; Greyship runs linux/%s (grey deploy sets this for you)", name, goos, arch, goarch)
	case settings["CGO_ENABLED"] != "0":
		return "", fmt.Errorf("%s was built with cgo; Greyship runs static binaries (CGO_ENABLED=0)", name)
	case settings["-trimpath"] != "true":
		return "", fmt.Errorf("%s was built without -trimpath", name)
	}
	return strings.TrimPrefix(info.GoVersion, "go"), nil
}

// Verify handles a build.verify job: the deployment's binaries were built on the developer's machine
// (decisions.md 034). Each one is downloaded, checked and stored as an artifact; then the deployment is
// handed to the scheduler exactly like a build made here. Running it twice is safe.
func (b *Builder) Verify(ctx context.Context, job bus.Job) error {
	var payload struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("decode job %d: %w", job.ID, err)
	}
	q := store.New(b.Pool)
	d, err := q.StartVerify(ctx, payload.DeploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("start check of %s: %w", payload.DeploymentID, err)
	}
	if err := q.DeleteArtifacts(ctx, d.ID); err != nil {
		return fmt.Errorf("clear artifacts of %s: %w", d.ID, err)
	}
	if err := q.DeleteBuildLogs(ctx, d.ID); err != nil {
		return fmt.Errorf("clear build log of %s: %w", d.ID, err)
	}

	log := NewLog(q, d.ID, 0, b.Now)
	err = b.verify(ctx, q, d, log)
	var userErr *userError
	switch {
	case err == nil:
	case errors.As(err, &userErr):
		log.System("deploy failed: " + userErr.msg)
		log.System(KeepsServing(ctx, q, d.ProjectID))
	case job.Attempts >= job.MaxAttempts:
		log.System("deploy failed: an internal error stopped the check; deploy again")
		log.System(KeepsServing(ctx, q, d.ProjectID))
	default:
		log.System("checking was interrupted by an internal error; retrying")
	}
	if lerr := log.Close(); lerr != nil {
		b.Logger.Error("write build log", "deployment", d.ID, "err", lerr)
	}
	switch {
	case err == nil:
		return b.finish(ctx, d.ID)
	case errors.As(err, &userErr):
		return b.fail(ctx, q, d.ID, userErr.msg)
	case job.Attempts >= job.MaxAttempts:
		if ferr := b.fail(ctx, q, d.ID, "internal error while checking the binaries"); ferr != nil {
			return ferr
		}
		return err
	default:
		return err
	}
}

func (b *Builder) verify(ctx context.Context, q *store.Queries, d store.Deployment, log *Log) error {
	bins, err := q.ListUploadedBinaries(ctx, d.ID)
	if err != nil {
		return fmt.Errorf("list binaries of %s: %w", d.ID, err)
	}
	log.System(fmt.Sprintf("using the %d binaries built on your machine", len(bins)))
	for _, bin := range bins {
		// The same bytes were checked when this project first uploaded them, and they're stored under their own
		// artifact key, so an unchanged service needs no download or check (docs/test-runs/2026-11-04-testapi.md).
		prev, err := q.ProjectArtifactBySHA(ctx, store.ProjectArtifactBySHAParams{ProjectID: d.ProjectID, Sha256: bin.Sha256, Goarch: b.GOARCH})
		if err == nil {
			if _, err := q.CreateArtifact(ctx, store.CreateArtifactParams{
				DeploymentID: d.ID, ServiceName: bin.ServiceName, ServicePath: bin.ServicePath, ServiceType: bin.ServiceType,
				ObjectKey: prev.ObjectKey, Sha256: bin.Sha256, SizeBytes: bin.SizeBytes, GoVersion: prev.GoVersion, Goarch: b.GOARCH,
			}); err != nil {
				return fmt.Errorf("record %s artifact: %w", bin.ServiceName, err)
			}
			log.System(fmt.Sprintf("unchanged %s: Go %s, linux/%s (already checked)", bin.ServiceName, prev.GoVersion, b.GOARCH))
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("look up %s artifact: %w", bin.ServiceName, err)
		}
		gz, err := b.Storage.Get(ctx, bin.ObjectKey, bin.GzBytes)
		if err != nil {
			return fmt.Errorf("download %s: %w", bin.ServiceName, err)
		}
		raw, err := Inflate(gz)
		if err != nil {
			return failf("%s: %s", bin.ServiceName, err)
		}
		if int64(len(raw)) != bin.SizeBytes {
			return failf("%s isn't the size it was declared as; deploy again", bin.ServiceName)
		}
		goVersion, err := CheckBinary(bin.ServiceName, raw, bin.Sha256, b.GOARCH)
		if err != nil {
			return failf("%s", err)
		}
		key := fmt.Sprintf("artifacts/%s/%s", d.ID, bin.ServiceName)
		if err := b.Storage.Put(ctx, key, bytes.NewReader(raw), int64(len(raw))); err != nil {
			return fmt.Errorf("store %s binary: %w", bin.ServiceName, err)
		}
		if _, err := q.CreateArtifact(ctx, store.CreateArtifactParams{
			DeploymentID: d.ID, ServiceName: bin.ServiceName, ServicePath: bin.ServicePath, ServiceType: bin.ServiceType,
			ObjectKey: key, Sha256: bin.Sha256, SizeBytes: bin.SizeBytes, GoVersion: goVersion, Goarch: b.GOARCH,
		}); err != nil {
			return fmt.Errorf("record %s artifact: %w", bin.ServiceName, err)
		}
		log.System(fmt.Sprintf("checked %s: Go %s, linux/%s (%.1f MB)", bin.ServiceName, goVersion, b.GOARCH, float64(len(raw))/(1<<20)))
	}
	return nil
}
