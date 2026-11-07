package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"greyship.tech/internal/config"
	"greyship.tech/internal/router"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("greyship-router stopped", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.LoadRouter(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pgcfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	// The route watcher holds a connection for LISTEN; leave enough for wake-up commands and certificates.
	pgcfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, pgcfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	table := &router.Table{}
	rt := &router.Router{Table: table, Domain: cfg.AppsDomain, PlatformDomain: cfg.PlatformDomain, Transport: router.NewTransport(), Commands: router.QueueCommands{Pool: pool}, Logger: logger, Platform: cfg.Platform}
	if cfg.HTTPOnly {
		return serveHTTPOnly(ctx, cfg, pool, table, rt, logger)
	}
	redirect := router.Redirect(cfg.AppsPort, table, cfg.AppsDomain, cfg.PlatformDomain)
	tlsConfig, redirect, err := certificates(cfg, table, rt, redirect)
	if err != nil {
		return err
	}

	httpsSrv := &http.Server{Addr: cfg.HTTPSAddr, Handler: rt, TLSConfig: tlsConfig, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	httpSrv := &http.Server{Addr: cfg.HTTPAddr, Handler: redirect, ReadHeaderTimeout: 10 * time.Second}

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return table.Watch(ctx, pool, 10*time.Second, logger) })
	g.Go(func() error { rt.PauseIdle(ctx); return nil })
	g.Go(func() error { return serve(httpsSrv.ListenAndServeTLS("", "")) })
	g.Go(func() error { return serve(httpSrv.ListenAndServe()) })
	g.Go(func() error {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return errors.Join(httpsSrv.Shutdown(shutdown), httpSrv.Shutdown(shutdown))
	})
	logger.Info("greyship-router listening", "https", cfg.HTTPSAddr, "http", cfg.HTTPAddr, "domain", cfg.AppsDomain, "platform_domain", cfg.PlatformDomain, "dev_ca", cfg.DevCADir != "")
	return g.Wait()
}

// serveHTTPOnly is a fresh install before it has a domain: everything on plain HTTP (decisions.md 050). Nothing
// here is encrypted, so the installer tells the owner to add a domain with greyship-admin domain.
func serveHTTPOnly(ctx context.Context, cfg config.Router, pool *pgxpool.Pool, table *router.Table, rt *router.Router, logger *slog.Logger) error {
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: rt, ReadHeaderTimeout: 10 * time.Second}
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return table.Watch(ctx, pool, 10*time.Second, logger) })
	g.Go(func() error { rt.PauseIdle(ctx); return nil })
	g.Go(func() error { return serve(srv.ListenAndServe()) })
	g.Go(func() error {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	})
	logger.Warn("greyship-router listening on plain HTTP only: add a domain to get HTTPS", "http", cfg.HTTPAddr, "domain", cfg.AppsDomain, "platform_domain", cfg.PlatformDomain)
	return g.Wait()
}

func serve(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// certificates picks how TLS certificates are made: the local dev CA, or Let's Encrypt on demand for routed
// hostnames only (decisions.md 031). It returns the HTTP handler too, which answers ACME challenges in production.
func certificates(cfg config.Router, table *router.Table, rt *router.Router, redirect http.Handler) (*tls.Config, http.Handler, error) {
	routed := rt.Serves
	// The handshake names the service before its request arrives: start waking it now (F-81).
	warm := func(hello *tls.ClientHelloInfo) {
		if label, ok := rt.LabelOf(hello.ServerName); ok {
			rt.Warm(label)
		}
	}
	if cfg.DevCADir != "" {
		ca, err := router.LoadDevCA(cfg.DevCADir)
		if err != nil {
			return nil, nil, err
		}
		return &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				if !routed(hello.ServerName) {
					return nil, fmt.Errorf("no app at %q", hello.ServerName)
				}
				warm(hello)
				return ca.Certificate(hello.ServerName)
			},
		}, redirect, nil
	}

	magic := certmagic.NewDefault()
	magic.Storage = &certmagic.FileStorage{Path: cfg.CertDir}
	magic.OnDemand = &certmagic.OnDemandConfig{
		// Only hostnames we actually serve get certificates, so nobody can make us request them for anything else.
		DecisionFunc: func(_ context.Context, name string) error {
			if !routed(name) {
				return fmt.Errorf("not a routed hostname: %s", name)
			}
			return nil
		},
	}
	issuer := certmagic.NewACMEIssuer(magic, certmagic.ACMEIssuer{CA: certmagic.LetsEncryptProductionCA, Email: cfg.ACMEEmail, Agreed: true})
	magic.Issuers = []certmagic.Issuer{issuer}
	tlsConfig := magic.TLSConfig()
	getCert := tlsConfig.GetCertificate
	tlsConfig.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		warm(hello)
		return getCert(hello)
	}
	tlsConfig.NextProtos = append([]string{"h2", "http/1.1"}, tlsConfig.NextProtos...)
	return tlsConfig, issuer.HTTPChallengeHandler(redirect), nil
}
