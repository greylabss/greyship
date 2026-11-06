// A background worker: no port, no URL. It just does a little job every 10 seconds.
package main

import (
	"log/slog"
	"os"
	"time"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	for i := 1; ; i++ {
		log.Info("worker tick", "n", i)
		time.Sleep(10 * time.Second)
	}
}
