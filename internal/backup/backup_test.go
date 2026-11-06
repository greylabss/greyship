package backup

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

type mem map[string]string

func (m mem) Put(_ context.Context, key string, body io.Reader, size int64) error {
	b, _ := io.ReadAll(body)
	if int64(len(b)) != size {
		panic("size mismatch")
	}
	m[key] = string(b)
	return nil
}

func TestUpload(t *testing.T) {
	m := mem{}
	key, size, err := Upload(context.Background(), m, strings.NewReader("PGDMP…data"), time.Date(2026, 11, 5, 3, 30, 0, 0, time.UTC))
	if err != nil || key != "backups/postgres/2026-11-05.dump" || m[key] != "PGDMP…data" || size != int64(len("PGDMP…data")) {
		t.Fatalf("key %s size %d err %v stored %q", key, size, err, m[key])
	}
	for _, bad := range []string{"", "pg_dump: error: connection refused"} {
		if _, _, err := Upload(context.Background(), m, strings.NewReader(bad), time.Now()); err == nil {
			t.Fatalf("stored a failed dump %q", bad)
		}
	}
}
