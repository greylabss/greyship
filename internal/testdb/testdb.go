// Package testdb gives integration tests a freshly migrated Postgres database.
package testdb

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"greyship.tech/migrations"
)

// New drops and re-migrates the schema, then returns a pool. Packages share one database, so `make test-int` runs them with -p 1.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("GREYSHIP_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("GREYSHIP_TEST_DATABASE_URL not set; run make test-int")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)

	db := stdlib.OpenDBFromPool(pool)
	defer func(db *sql.DB) { _ = db.Close() }(db)
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}
