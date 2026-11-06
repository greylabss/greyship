// greyship-migrate applies the database migrations built into it. Servers have no Go toolchain, so releases
// ship this binary and run it before restarting the services.
package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"

	"greyship.tech/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "greyship-migrate:", err)
		os.Exit(1)
	}
}

func run() error {
	url := os.Getenv("GREYSHIP_DATABASE_URL")
	if url == "" {
		return fmt.Errorf("GREYSHIP_DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(db, ".")
}
