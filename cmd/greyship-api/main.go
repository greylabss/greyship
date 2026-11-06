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
	"greyship.tech/internal/beta"
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
	"greyship.tech/internal/store"
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
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("greyship-api stopped", "err", err)
		os.Exit(1)
	}
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
		logger.Info("no email provider set: turn email on in the dashboard (Admin > Email); until then login codes are written to files in the outbox folder, readable by the server's owner", "dir", cfg.OutboxDir)
	}

	// Mail is sent in the background so login requests never wait on the provider.
	// Stopped on return, after the HTTP server has shut down, so it can drain what is queued.
	mailer := mail.NewQueue(sender, 1024, logger)
	queueCtx, stopQueue := context.WithCancel(context.Background())
	queueDone := make(chan struct{})
	go func() {
		defer close(queueDone)
		mailer.Run(queueCtx, 10*time.Second)
	}()
	defer func() {
		stopQueue()
		<-queueDone
	}()

	providers := map[string]auth.Provider{}
	if cfg.Google.Enabled() {
		providers["google"] = auth.Google(cfg.Google.ClientID, cfg.Google.ClientSecret, cfg.DashboardURL+"/v1/auth/oauth/google/callback", auth.GoogleEndpoints)
	}
	if cfg.GitHub.Enabled() {
		providers["github"] = auth.GitHub(cfg.GitHub.ClientID, cfg.GitHub.ClientSecret, cfg.DashboardURL+"/v1/auth/oauth/github/callback", auth.GitHubEndpoints)
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

	signup := auth.Signup{Open: cfg.SignupOpen || cfg.Dev, Allow: auth.ParseAllowlist(cfg.SignupAllow), PlansOn: cfg.PlansOn, OwnerEmail: cfg.OwnerEmail}
	if cfg.OwnerEmail != "" {
		if err := store.New(pool).MakeOwnerByEmail(ctx, cfg.OwnerEmail); err != nil {
			return fmt.Errorf("make %s an owner: %w", cfg.OwnerEmail, err)
		}
	}
	if cfg.BetaURL != "" {
		signup.Testers = beta.New(cfg.BetaURL, cfg.BetaSecret)
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

	authService := auth.NewService(pool, mailer, time.Now, signup)
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
		Providers:     providers,
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
		logger.Info("greyship-api listening", "addr", cfg.Addr, "oauth_providers", len(providers))
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
