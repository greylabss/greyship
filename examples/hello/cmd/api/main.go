// The web service: a tiny JSON API. Greyship gives it PORT, and the variables from .env.prod.
package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	started := time.Now()
	var visits atomic.Int64

	http.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		n := visits.Add(1)
		log.Info("visit", "path", r.URL.Path, "n", n)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": os.Getenv("GREETING"),
			"visits":  n, // survives sleeping: a paused app keeps its memory
			"service": os.Getenv("GREYSHIP_SERVICE"),
			"up_for":  time.Since(started).Round(time.Second).String(),
		})
	})
	log.Info("listening", "port", os.Getenv("PORT"))
	if err := http.ListenAndServe(":"+os.Getenv("PORT"), nil); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
