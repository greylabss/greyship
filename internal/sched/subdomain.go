package sched

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"greyship.tech/internal/slug"
	"greyship.tech/internal/store"
)

const maxLabel = 63

// Label is a service's subdomain label: <service>-<project>, shortened with a hash when over 63 characters
// (F-50b), with suffix appended when the plain label is taken (F-50a).
func Label(service, project, suffix string) string {
	base := slug.Make(service+"-"+project, 200)
	room := maxLabel
	if suffix != "" {
		room -= len(suffix) + 1
	}
	if len(base) > room {
		sum := sha256.Sum256([]byte(base))
		base = strings.TrimRight(base[:room-5], "-") + "-" + hex.EncodeToString(sum[:])[:4]
	}
	if suffix != "" {
		base += "-" + suffix
	}
	return base
}

const suffixAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func randomSuffix() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	for i := range b {
		b[i] = suffixAlphabet[int(b[i])%len(suffixAlphabet)]
	}
	return string(b[:])
}

// ensureSubdomain gives a web service its subdomain the first time it is released. Services keep theirs after that.
func ensureSubdomain(ctx context.Context, q *store.Queries, svc store.Service, projectSlug string) (store.Service, error) {
	if svc.Type != "web" || svc.Subdomain != nil {
		return svc, nil
	}
	for try := 0; try < 10; try++ {
		suffix := ""
		if try > 0 {
			suffix = randomSuffix()
		}
		got, err := q.SetServiceSubdomain(ctx, store.SetServiceSubdomainParams{ID: svc.ID, Subdomain: ptr(Label(svc.Name, projectSlug, suffix))})
		var pgErr *pgconn.PgError
		switch {
		case err == nil:
			return got, nil
		case errors.As(err, &pgErr) && pgErr.Code == "23505":
			continue
		case errors.Is(err, pgx.ErrNoRows):
			// Another release assigned it meanwhile.
			return q.GetService(ctx, svc.ID)
		default:
			return svc, fmt.Errorf("assign subdomain to %s: %w", svc.Name, err)
		}
	}
	return svc, fmt.Errorf("no free subdomain for %s", svc.Name)
}

func ptr(s string) *string { return &s }
