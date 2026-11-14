package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	"greyship.tech/internal/domains"
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
	// greyship-api create-users reads a password on the first line of stdin and one email per line after it, and makes
	// an account for each (greyship-admin add-users).
	if len(os.Args) == 2 && os.Args[1] == "create-users" {
		if err := createUsers(os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, "create-users:", err)
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

func createUsers(in io.Reader) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	var lines []string
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if len(lines) < 2 {
		return errors.New("give a password on the first line and at least one email after it")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	created, exists, refused := 0, 0, 0
	for _, r := range auth.NewService(pool, time.Now, cfg.OwnerEmail).CreateMany(ctx, lines[0], lines[1:]) {
		switch {
		case r.Created:
			created++
			fmt.Println("created  ", r.Email)
		case errors.Is(r.Err, auth.ErrUserExists):
			exists++
			fmt.Println("exists   ", r.Email)
		default:
			refused++
			fmt.Printf("refused   %s: %v\n", r.Email, r.Err)
		}
	}
	fmt.Printf("%d created, %d already had an account, %d refused\n", created, exists, refused)
	return nil
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
	githubApps := &github.Apps{Pool: pool, Keys: keyring, Now: time.Now, Logger: logger}
	ghDeploys := &github.Service{Pool: pool, GitHub: github.Dynamic{Apps: githubApps}, Apps: githubApps, DashboardURL: cfg.DashboardURL, Projects: projects, Signer: signer, Logger: logger}

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
		Domains:       &domains.Service{Pool: pool, Projects: projects, AppsDomain: cfg.AppsDomain, PlatformDomain: cfg.PlatformDomain, Resolver: net.DefaultResolver, Warm: certificateWarmer(cfg)},
		Mail:          sender,
		Projects:      projects,
		Deployments:   deploy.NewService(pool, projects, storage, logger, time.Now),
		Env:           env.NewService(pool, keyring, projects),
		Builds:        &fly.Reports{Pool: pool, Storage: storage, Signer: auth.Signer{Key: cfg.CookieSecret, Now: time.Now}},
		GitHub:        ghDeploys,
		Notifications: inbox,
		Admin:         &admin.Service{Pool: pool},
		PushKey:       inbox.PushKey,
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

// certificateWarmer is how the API asks the router on this machine for a new domain's certificate. Development and
// plain-HTTP installs have no certificates to get.
func certificateWarmer(cfg config.Config) func(ctx context.Context, hostname string) error {
	if cfg.Dev || cfg.HTTPOnly {
		return nil
	}
	return domains.TLSWarmer("127.0.0.1:443")
}
