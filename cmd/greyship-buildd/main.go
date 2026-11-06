// greyship-buildd runs one build on a Fly build machine and exits, which stops the machine (decisions.md 039).
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"

	"greyship.tech/internal/build/fly"
	"greyship.tech/internal/buildd"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	var job fly.MachineJob
	if err := json.Unmarshal([]byte(os.Getenv("GREYSHIP_BUILD_JOB")), &job); err != nil {
		logger.Error("no build job in GREYSHIP_BUILD_JOB", "err", err)
		os.Exit(2)
	}
	cache := ""
	if fi, err := os.Stat("/cache"); err == nil && fi.IsDir() {
		cache = "/cache"
	}
	b := &buildd.Builder{Work: "/work", Cache: cache, Go: "go", HTTP: &http.Client{Timeout: 10 * time.Minute}, Now: time.Now}
	if err := b.Run(context.Background(), job); err != nil {
		logger.Error("build", "deployment", job.Deployment, "err", err)
		os.Exit(1)
	}
}
