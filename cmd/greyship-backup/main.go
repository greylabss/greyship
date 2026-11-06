// greyship-backup uploads a pg_dump from stdin to object storage; a systemd timer runs it nightly.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"greyship.tech/internal/backup"
	"greyship.tech/internal/config"
	"greyship.tech/internal/objstore"
)

func main() {
	cfg, err := config.LoadBackup(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	store := &objstore.Store{
		Endpoint: cfg.Endpoint, Region: cfg.Region, Bucket: cfg.Bucket, AccessKey: cfg.AccessKey, SecretKey: cfg.SecretKey,
		HTTP: &http.Client{Timeout: 30 * time.Minute}, Now: time.Now,
	}
	key, size, err := backup.Upload(context.Background(), store, os.Stdin, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "backup failed:", err)
		os.Exit(1)
	}
	fmt.Printf("stored %s (%d bytes)\n", key, size)
}
