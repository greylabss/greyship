package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	srv := &http.Server{Addr: ":8080"}
	go func() {
		_ = srv.ListenAndServe()
	}()
	<-ctx.Done()
	_ = srv.Shutdown(context.Background())
}
