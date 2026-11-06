package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"greyship.tech/internal/cli"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

func main() {
	path, err := cli.DefaultConfigPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "grey:", err)
		os.Exit(1)
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "grey:", err)
		os.Exit(1)
	}
	host, _ := os.Hostname()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	app := cli.DefaultApp(os.Stdout, os.Stderr, os.Getenv, path, host, version)
	app.Dir = dir
	if err := cli.NewRoot(app).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "grey:", err)
		os.Exit(1)
	}
}
