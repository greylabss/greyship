package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"greyship.tech/internal/admin"
	"greyship.tech/internal/api"
	"greyship.tech/internal/auth"
	"greyship.tech/internal/build/fly"
	"greyship.tech/internal/bus"
	"greyship.tech/internal/config"
	"greyship.tech/internal/deploy"
	"greyship.tech/internal/env"
	"greyship.tech/internal/github"
	"greyship.tech/internal/mail"
	"greyship.tech/internal/notify"
	"greyship.tech/internal/objstore"
	"greyship.tech/internal/project"
	"greyship.tech/internal/secrets"
	"greyship.tech/internal/webpush"
)

func main() {
	// greyship-api vapid-key prints a new browser push signing key for GREYSHIP_VAPID_PRIVATE_KEY.
	if len(os.Args) == 2 && os.Args[1] == "vapid-key" {
		key, err := webpush.GenerateKey()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(key)
		return
	}
	// greyship-api reset-password <email> gives an account a new random password and prints it. Whoever can run it
	// on the server is the operator: this is how a forgotten password is recovered.
	if len(os.Args) == 3 && os.Args[1] == "reset-password" {
		if err := resetPassword(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "reset-password:", err)
			os.Exit(1)
		}
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("greyship-api stopped", "err", err)
		os.Exit(1)
	}
}

func resetPassword(email string) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	password, err := auth.NewService(pool, time.Now, cfg.OwnerEmail).ResetPassword(ctx, email)
	if errors.Is(err, auth.ErrNoUser) {
		return fmt.Errorf("no account with the email %s", email)
	}
	if err != nil {
		return err
	}
	fmt.Printf("New password for %s: %s\nThey are signed out everywhere. Ask them to change it in Settings.\n", email, password)
	return nil
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pgcfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	// The notify worker holds a connection for LISTEN; leave the rest for requests.
	pgcfg.MaxConns = max(pgcfg.MaxConns, 10)
	pool, err := pgxpool.NewWithConfig(ctx, pgcfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}

	keyring, err := secrets.NewKeyring(cfg.MasterKey)
	if err != nil {
		return err
	}
	// Email settings saved in the dashboard replace GREYSHIP_RESEND_API_KEY (decisions.md 052).
	sender := &mail.Dynamic{
		Pool: pool, Keys: keyring, Outbox: &mail.Outbox{Dir: cfg.OutboxDir, Now: time.Now},
		Dev: cfg.Dev, Now: time.Now, Logger: logger,
	}
	if cfg.ResendAPIKey != "" {
		sender.Env = mail.NewResend(cfg.ResendAPIKey, cfg.MailFrom)
	} else {
		logger.Info("email is off: turn it on in the dashboard (Admin > Email) to get deploy notifications by mail")
	}

	projects := project.NewService(pool)
	storage := &objstore.Store{
		Endpoint: cfg.Storage.Endpoint, Region: cfg.Storage.Region, Bucket: cfg.Storage.Bucket,
		AccessKey: cfg.Storage.AccessKey, SecretKey: cfg.Storage.SecretKey,
		HTTP: &http.Client{Timeout: 10 * time.Second}, Now: time.Now,
	}

	signer := auth.Signer{Key: cfg.CookieSecret, Now: time.Now}
	ghCfg, err := config.LoadGitHubApp(os.Getenv)
	if err != nil {
		return err
	}
	var ghDeploys api.GitHubDeploys
	if ghCfg.ID != 0 {
		key, err := os.ReadFile(ghCfg.KeyFile)
		if err != nil {
			return fmt.Errorf("read GitHub App key: %w", err)
		}
		app, err := github.NewApp(ghCfg.ID, ghCfg.Slug, ghCfg.ClientID, ghCfg.ClientSecret, key)
		if err != nil {
			return err
		}
		ghDeploys = &github.Service{Pool: pool, GitHub: app, Projects: projects, Signer: signer, Logger: logger}
	}

	var pusher notify.Pusher
	inbox := &notify.Inbox{Pool: pool}
	if cfg.VAPIDKey != "" {
		wp, err := webpush.NewSender(cfg.VAPIDKey, cfg.VAPIDSubject, &http.Client{Timeout: 10 * time.Second}, time.Now)
		if err != nil {
			return err
		}
		pusher, inbox.PushKey = wp, wp.PublicKey()
	}
	notifier := &notify.Notifier{Pool: pool, Mail: sender, From: cfg.NotifyFrom, Push: pusher, DashboardURL: cfg.DashboardURL, Logger: logger}
	notifyWorker := &bus.Worker{
		Queue: bus.NewQueue(pool), Pool: pool, ID: bus.WorkerID(), Kind: bus.Notify,
		Lease: time.Minute, Poll: 5 * time.Second, Logger: logger,
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		_ = notifyWorker.Run(workerCtx, notifier.Handle)
	}()
	defer func() {
		stopWorker()
		<-workerDone
	}()

	authService := auth.NewService(pool, time.Now, cfg.OwnerEmail)
	handler := api.New(api.Options{
		Auth:          authService,
		Accounts:      authService,
		Mail:          sender,
		Projects:      projects,
		Deployments:   deploy.NewService(pool, projects, storage, logger, time.Now),
		Env:           env.NewService(pool, keyring, projects),
		Builds:        &fly.Reports{Pool: pool, Storage: storage, Signer: auth.Signer{Key: cfg.CookieSecret, Now: time.Now}},
		GitHub:        ghDeploys,
		Notifications: inbox,
		Admin:         &admin.Service{Pool: pool},
		PushKey:       inbox.PushKey,
		WebhookSecret: ghCfg.WebhookSecret,
		Signer:        auth.Signer{Key: cfg.CookieSecret, Now: time.Now},
		DashboardURL:  cfg.DashboardURL,
		AppsDomain:    cfg.AppsDomain,
		AppsPort:      cfg.AppsPort,
		BuildGOARCH:   cfg.BuildGOARCH,
		ProxySecret:   cfg.ProxySecret,
		SecureCookie:  !cfg.Dev && !cfg.HTTPOnly,
		HTTPOnly:      cfg.HTTPOnly,
		Logger:        logger,
	})

	srv := &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute}
	errc := make(chan error, 1)
	go func() {
		logger.Info("greyship-api listening", "addr", cfg.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	return nil
}
