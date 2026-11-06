package fly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/auth"
	"greyship.tech/internal/store"
)

type Presigner interface {
	PresignPut(key string, size int64, ttl time.Duration) (string, error)
}

// Reports takes what build machines send through the API: upload URL requests and log and outcome reports.
// Machines are trusted only as far as their token: one deployment, while it builds.
type Reports struct {
	Pool    *pgxpool.Pool
	Storage Presigner
	Signer  auth.Signer
}

var (
	ErrBadReport = errors.New("invalid build report")
	targetName   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

const (
	maxReportLines = 1000
	maxLineBytes   = 4 << 10
)

// UploadURL signs a PUT for one binary of a building deployment, at the size the machine declares.
func (r *Reports) UploadURL(ctx context.Context, token, deployment string, req UploadRequest) (string, error) {
	if err := r.check(ctx, token, deployment); err != nil {
		return "", err
	}
	if !targetName.MatchString(req.Target) || req.Size <= 0 || req.Size > MaxBinary {
		return "", ErrBadReport
	}
	return r.Storage.PresignPut(OutputKey(deployment, req.Target), req.Size, 15*time.Minute)
}

// Add stores a report's lines and outcome for the builder to read.
func (r *Reports) Add(ctx context.Context, token, deployment string, rep Report) error {
	if err := r.check(ctx, token, deployment); err != nil {
		return err
	}
	if len(rep.Lines) > maxReportLines {
		return ErrBadReport
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := store.New(tx)
	for _, l := range rep.Lines {
		stream := "build"
		if l.Stream == "system" {
			stream = "system"
		}
		if err := q.AddBuildReport(ctx, store.AddBuildReportParams{DeploymentID: deployment, Stream: stream, Line: clip(l.Text)}); err != nil {
			return fmt.Errorf("store report line: %w", err)
		}
	}
	if rep.Outcome != nil {
		b, err := json.Marshal(rep.Outcome)
		if err != nil {
			return err
		}
		if err := q.AddBuildReport(ctx, store.AddBuildReportParams{DeploymentID: deployment, Stream: "system", Result: b}); err != nil {
			return fmt.Errorf("store outcome: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// check accepts a token only for its deployment, and only while that deployment is building.
func (r *Reports) check(ctx context.Context, token, deployment string) error {
	if err := CheckToken(r.Signer, token, deployment); err != nil {
		return err
	}
	d, err := store.New(r.Pool).GetDeployment(ctx, deployment)
	if err != nil || d.Status != "building" {
		return ErrBadToken
	}
	return nil
}

// OutputKey is where a server build's binary goes; the agent reads artifacts from the same place.
func OutputKey(deployment, target string) string { return "artifacts/" + deployment + "/" + target }

func clip(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if len(s) <= maxLineBytes {
		return s
	}
	s = s[:maxLineBytes]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + " …"
}
