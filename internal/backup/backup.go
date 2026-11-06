// Package backup stores a nightly Postgres dump in object storage (R2 in production, decisions.md 040), so
// losing the server never loses accounts, projects or encrypted env vars.
package backup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

type Storage interface {
	Put(ctx context.Context, key string, body io.Reader, size int64) error
}

// pg_dump's custom format starts with these bytes; anything else is a failed or empty dump.
const dumpMagic = "PGDMP"

// Upload reads a pg_dump -Fc stream and stores it as backups/postgres/<date>.dump. It buffers to a temp file
// first because uploads need the size up front.
func Upload(ctx context.Context, s Storage, dump io.Reader, now time.Time) (string, int64, error) {
	r := bufio.NewReader(dump)
	head, err := r.Peek(len(dumpMagic))
	if err != nil || string(head) != dumpMagic {
		return "", 0, errors.New("the input isn't a pg_dump custom-format dump; did pg_dump fail?")
	}
	f, err := os.CreateTemp("", "greyship-backup-*.dump")
	if err != nil {
		return "", 0, fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()
	size, err := io.Copy(f, r)
	if err != nil {
		return "", 0, fmt.Errorf("read dump: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	key := "backups/postgres/" + now.UTC().Format("2006-01-02") + ".dump"
	if err := s.Put(ctx, key, f, size); err != nil {
		return "", 0, fmt.Errorf("upload %s: %w", key, err)
	}
	return key, size, nil
}
