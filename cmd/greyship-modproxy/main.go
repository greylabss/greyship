package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"greyship.tech/internal/modproxy"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("greyship-modproxy stopped", "err", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	proxy := &modproxy.Proxy{
		Upstream: env("GREYSHIP_MODPROXY_UPSTREAM", "https://proxy.golang.org"),
		SumDB:    env("GREYSHIP_MODPROXY_SUMDB", "https://sum.golang.org"),
		CacheDir: env("GREYSHIP_MODPROXY_CACHE", "/cache"),
		HTTP:     &http.Client{Timeout: 5 * time.Minute},
		Logger:   logger,
	}
	addr := env("GREYSHIP_MODPROXY_ADDR", ":8080")
	srv := &http.Server{Addr: addr, Handler: proxy, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() {
		logger.Info("greyship-modproxy listening", "addr", addr, "upstream", proxy.Upstream)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
	return nil
}
