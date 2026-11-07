// Package config reads greyship-api settings from GREYSHIP_* environment variables.
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"
)

type OAuthClient struct {
	ClientID     string
	ClientSecret string
}

func (c OAuthClient) Enabled() bool { return c.ClientID != "" && c.ClientSecret != "" }

// Storage is the S3-compatible bucket for source tarballs and binaries. The CLI uploads straight to Endpoint
// with presigned URLs, so it must be reachable from developers' machines, not only from the API.
type Storage struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
}

type Config struct {
	Dev          bool
	Addr         string
	DatabaseURL  string
	DashboardURL string
	HTTPOnly     bool   // GREYSHIP_HTTP_ONLY: no TLS yet (a fresh install on a bare IP); the owner adds a domain later
	OwnerEmail   string // GREYSHIP_OWNER_EMAIL: this account is made an instance owner at startup
	ProxySecret  string // GREYSHIP_PROXY_SECRET: shared with the dashboard's /v1 proxy
	CookieSecret []byte
	ResendAPIKey string
	MailFrom     string
	NotifyFrom   string // sender for deploy and project notifications
	VAPIDKey     string // GREYSHIP_VAPID_PRIVATE_KEY: browser push signing key; empty turns push off
	VAPIDSubject string // contact the push services may use, mailto: or https:
	OutboxDir    string
	Storage      Storage
	AppsDomain   string
	AppsPort     string
	MasterKey    string
	BuildGOARCH  string
}

// devMasterKey encrypts env vars in development only; production must set GREYSHIP_MASTER_KEY.
const devMasterKey = "6772657973686970206465762d6f6e6c79206d6173746572206b657920212121"

// Load reads configuration through getenv so tests can pass a map instead of the process environment.
func Load(getenv func(string) string) (Config, error) {
	get := getter(getenv)

	cfg := Config{
		Dev:          get("ENV", "production") == "development",
		Addr:         get("API_ADDR", ":8080"),
		DatabaseURL:  get("DATABASE_URL", ""),
		DashboardURL: strings.TrimRight(get("DASHBOARD_URL", "http://localhost:3000"), "/"),
		HTTPOnly:     get("HTTP_ONLY", "") == "true",
		OwnerEmail:   strings.ToLower(strings.TrimSpace(get("OWNER_EMAIL", ""))),
		ProxySecret:  get("PROXY_SECRET", ""),
		ResendAPIKey: get("RESEND_API_KEY", ""),
		MailFrom:     get("MAIL_FROM", "Greyship <login@greyship.tech>"),
		NotifyFrom:   get("NOTIFY_FROM", "Greyship <notifications@greyship.tech>"),
		VAPIDKey:     get("VAPID_PRIVATE_KEY", ""),
		VAPIDSubject: get("VAPID_SUBJECT", "mailto:support@greyship.tech"),
		OutboxDir:    get("OUTBOX_DIR", "tmp/outbox"),
	}
	cfg.Storage = loadStorage(get, cfg.Dev)
	// Must match the router's, which serves these names.
	cfg.AppsDomain = get("APPS_DOMAIN", devOrValue(cfg.Dev, "localhost", "greyship.tech"))
	cfg.AppsPort = get("APPS_PORT", devOrValue(cfg.Dev, "8443", ""))
	cfg.MasterKey = get("MASTER_KEY", devOrValue(cfg.Dev, devMasterKey, ""))
	// The same setting the builder reads: what the runtime nodes run, which local builds must target.
	cfg.BuildGOARCH = get("BUILD_GOARCH", devOrValue(cfg.Dev, goruntime.GOARCH, "amd64"))

	var errs []error
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("GREYSHIP_DATABASE_URL is required"))
	}
	if u, err := url.Parse(cfg.DashboardURL); err != nil || u.Scheme == "" || u.Host == "" {
		errs = append(errs, fmt.Errorf("GREYSHIP_DASHBOARD_URL must be an absolute URL, got %q", cfg.DashboardURL))
	}

	errs = append(errs, cfg.Storage.check()...)
	if cfg.MasterKey == "" {
		errs = append(errs, errors.New("GREYSHIP_MASTER_KEY is required outside development (openssl rand -hex 32)"))
	}

	secret, err := hex.DecodeString(get("COOKIE_SECRET", ""))
	switch {
	case err != nil:
		errs = append(errs, errors.New("GREYSHIP_COOKIE_SECRET must be hex (openssl rand -hex 32)"))
	case len(secret) < 32:
		errs = append(errs, errors.New("GREYSHIP_COOKIE_SECRET must be at least 32 bytes (64 hex characters)"))
	default:
		cfg.CookieSecret = secret
	}

	// Outside development a real mail provider is required, or nobody could log in.

	return cfg, errors.Join(errs...)
}

// Builder is greyship-builder's configuration.
type Builder struct {
	DatabaseURL string
	Storage     Storage
	Docker      string
	DockerHost  string // the sandbox VM's docker, which has runsc
	Network     string
	ProxyURL    string
	ProxyHost   string
	DefaultGo   string
	GOARCH      string
	Timeout     time.Duration
	CPUs        float64
	MemoryMB    int
	Concurrency int      // builds at once on this builder
	Seed        []string // Go versions whose standard-library build cache is made ahead of time
	Runner      string   // gvisor (this machine) or fly (decisions.md 039)
	Fly         Fly
	TokenSecret []byte // GREYSHIP_COOKIE_SECRET: signs build tokens the API checks
}

// Fly is where server builds run in production.
type Fly struct {
	Token       string
	App         string
	Region      string
	ImagePrefix string
	MaxMachines int64
	VolumeGB    int
	CPUs        int // CPUs of a build machine: 2, 4 or 8
	MemoryMB    int
	APIURL      string // the public API build machines report to
}

func LoadBuilder(getenv func(string) string) (Builder, error) {
	get := getter(getenv)
	dev := get("ENV", "production") == "development"
	b := Builder{
		DatabaseURL: get("DATABASE_URL", ""),
		Storage:     loadStorage(get, dev),
		Docker:      get("BUILD_DOCKER", "docker"),
		DockerHost:  get("BUILD_DOCKER_HOST", devOrValue(dev, "unix://"+getenv("HOME")+"/.lima/greyship/sock/docker.sock", "unix:///var/run/docker.sock")),
		Network:     get("BUILD_NETWORK", "gs-build"),
		ProxyURL:    get("BUILD_PROXY_URL", "http://modproxy:8080"),
		ProxyHost:   get("BUILD_PROXY_HOST", "modproxy:172.30.0.2"),
		DefaultGo:   get("BUILD_DEFAULT_GO", "1.27"),
		// A dev Mac's sandbox VM has the Mac's CPU; production nodes are amd64.
		GOARCH: get("BUILD_GOARCH", devOrValue(dev, goruntime.GOARCH, "amd64")),
	}
	var err error
	errs := b.Storage.check()
	// The dev Mac's sandbox VM has 4 CPUs and 4GB, shared with app instances; a production server gives builds more.
	// Two builds at a time on a server, 3 CPUs each (a 6-core machine), one team at a time each.
	if b.Concurrency, err = strconv.Atoi(get("BUILD_CONCURRENCY", devOrValue(dev, "1", "2"))); err != nil || b.Concurrency < 1 || b.Concurrency > 16 {
		errs = append(errs, errors.New("GREYSHIP_BUILD_CONCURRENCY must be 1 to 16"))
	}
	if b.CPUs, err = strconv.ParseFloat(get("BUILD_CPUS", devOrValue(dev, "2", "3")), 64); err != nil || b.CPUs <= 0 {
		errs = append(errs, errors.New("GREYSHIP_BUILD_CPUS must be a positive number"))
	}
	if b.MemoryMB, err = strconv.Atoi(get("BUILD_MEMORY_MB", devOrValue(dev, "1536", "2048"))); err != nil || b.MemoryMB < 512 {
		errs = append(errs, errors.New("GREYSHIP_BUILD_MEMORY_MB must be at least 512"))
	}
	for _, v := range strings.Split(get("BUILD_SEED_GO", ""), ",") {
		if v = strings.TrimSpace(v); v != "" {
			b.Seed = append(b.Seed, v)
		}
	}
	if b.GOARCH != "amd64" && b.GOARCH != "arm64" {
		errs = append(errs, errors.New("GREYSHIP_BUILD_GOARCH must be amd64 or arm64"))
	}
	if b.DatabaseURL == "" {
		errs = append(errs, errors.New("GREYSHIP_DATABASE_URL is required"))
	}
	if b.Timeout, err = time.ParseDuration(get("BUILD_TIMEOUT", "10m")); err != nil || b.Timeout <= 0 {
		errs = append(errs, errors.New("GREYSHIP_BUILD_TIMEOUT must be a duration such as 10m"))
	}
	b.Runner = get("BUILD_RUNNER", "gvisor")
	app := get("FLY_APP", "greyship-build")
	b.Fly = Fly{
		Token: get("FLY_API_TOKEN", ""), App: app, Region: get("FLY_REGION", "fra"),
		ImagePrefix: get("FLY_IMAGE_PREFIX", "registry.fly.io/"+app), APIURL: get("BUILD_REPORT_URL", "https://api.greyship.tech"),
	}
	if b.Fly.MaxMachines, err = strconv.ParseInt(get("FLY_MAX_MACHINES", "8"), 10, 64); err != nil || b.Fly.MaxMachines < 1 {
		errs = append(errs, errors.New("GREYSHIP_FLY_MAX_MACHINES must be a positive number"))
	}
	if b.Fly.CPUs, err = strconv.Atoi(get("FLY_MACHINE_CPUS", "4")); err != nil || (b.Fly.CPUs != 2 && b.Fly.CPUs != 4 && b.Fly.CPUs != 8) {
		errs = append(errs, errors.New("GREYSHIP_FLY_MACHINE_CPUS must be 2, 4 or 8"))
	}
	if b.Fly.MemoryMB, err = strconv.Atoi(get("FLY_MACHINE_MEMORY_MB", "8192")); err != nil || b.Fly.MemoryMB < 2048 {
		errs = append(errs, errors.New("GREYSHIP_FLY_MACHINE_MEMORY_MB must be at least 2048"))
	}
	if b.Fly.VolumeGB, err = strconv.Atoi(get("FLY_VOLUME_GB", "10")); err != nil || b.Fly.VolumeGB < 1 {
		errs = append(errs, errors.New("GREYSHIP_FLY_VOLUME_GB must be a positive number"))
	}
	switch b.Runner {
	case "gvisor":
	case "fly":
		if b.Fly.Token == "" {
			errs = append(errs, errors.New("GREYSHIP_FLY_API_TOKEN is required with GREYSHIP_BUILD_RUNNER=fly"))
		}
		secret, err := hex.DecodeString(get("COOKIE_SECRET", ""))
		if err != nil || len(secret) < 32 {
			errs = append(errs, errors.New("GREYSHIP_COOKIE_SECRET (64 hex characters) is required with GREYSHIP_BUILD_RUNNER=fly: it signs build tokens"))
		}
		b.TokenSecret = secret
	default:
		errs = append(errs, errors.New("GREYSHIP_BUILD_RUNNER must be gvisor or fly"))
	}
	return b, errors.Join(errs...)
}

func getter(getenv func(string) string) func(key, fallback string) string {
	return func(key, fallback string) string {
		if v := strings.TrimSpace(getenv("GREYSHIP_" + key)); v != "" {
			return v
		}
		return fallback
	}
}

// loadStorage reads GREYSHIP_S3_*. Development defaults match the Garage service in deploy/compose.dev.yml and
// the key deploy/garage-init.sh imports.
func loadStorage(get func(key, fallback string) string, dev bool) Storage {
	return Storage{
		Endpoint:  strings.TrimRight(get("S3_ENDPOINT", devOrValue(dev, "http://localhost:3900", "")), "/"),
		Region:    get("S3_REGION", devOrValue(dev, "garage", "us-east-1")),
		Bucket:    get("S3_BUCKET", "greyship"),
		AccessKey: get("S3_ACCESS_KEY", devOrValue(dev, "GK677265797368697064657631", "")),
		SecretKey: get("S3_SECRET_KEY", devOrValue(dev, "6772657973686970206465762073336b6579206e6f7420666f722070726f6421", "")),
	}
}

func (s Storage) check() []error {
	var errs []error
	if u, err := url.Parse(s.Endpoint); err != nil || u.Scheme == "" || u.Host == "" {
		errs = append(errs, fmt.Errorf("GREYSHIP_S3_ENDPOINT must be an absolute URL, got %q", s.Endpoint))
	}
	if s.AccessKey == "" || s.SecretKey == "" {
		errs = append(errs, errors.New("GREYSHIP_S3_ACCESS_KEY and GREYSHIP_S3_SECRET_KEY are required outside development"))
	}
	return errs
}

func devOrValue(dev bool, devValue, value string) string {
	if dev {
		return devValue
	}
	return value
}

// Agent is greyship-agent's configuration.
type Agent struct {
	DatabaseURL string
	Storage     Storage
	Docker      string
	DockerHost  string // the node's docker, which has runsc
	InitBinary  string // greyship-init built for this node's CPU
	NodeName    string
	Region      string
	TotalMB     int
	SharedDir   string // resolv.conf and CA certificates for instances, on the Docker host
	MasterKey   string
}

func LoadAgent(getenv func(string) string, hostname string) (Agent, error) {
	get := getter(getenv)
	dev := get("ENV", "production") == "development"
	a := Agent{
		DatabaseURL: get("DATABASE_URL", ""),
		Storage:     loadStorage(get, dev),
		Docker:      get("RUNTIME_DOCKER", "docker"),
		DockerHost:  get("RUNTIME_DOCKER_HOST", devOrValue(dev, "unix://"+getenv("HOME")+"/.lima/greyship/sock/docker.sock", "unix:///var/run/docker.sock")),
		InitBinary:  get("INIT_BINARY", "bin/greyship-init"),
		NodeName:    get("NODE_NAME", hostname),
		Region:      get("REGION", devOrValue(dev, "dev", "")),
		SharedDir:   get("RUNTIME_SHARED_DIR", "/var/lib/greyship-sandbox"),
		MasterKey:   get("MASTER_KEY", devOrValue(dev, devMasterKey, "")),
	}
	errs := a.Storage.check()
	if a.DatabaseURL == "" {
		errs = append(errs, errors.New("GREYSHIP_DATABASE_URL is required"))
	}
	if a.Region == "" {
		errs = append(errs, errors.New("GREYSHIP_REGION is required outside development"))
	}
	if a.MasterKey == "" {
		errs = append(errs, errors.New("GREYSHIP_MASTER_KEY is required outside development"))
	}
	var err error
	// Memory the node offers to instances; the rest is for the platform itself (architecture §7).
	if a.TotalMB, err = strconv.Atoi(get("NODE_MEMORY_MB", devOrValue(dev, "2048", "7168"))); err != nil || a.TotalMB <= 0 {
		errs = append(errs, errors.New("GREYSHIP_NODE_MEMORY_MB must be a positive number"))
	}
	return a, errors.Join(errs...)
}

// Router is greyship-router's configuration.
type Router struct {
	DatabaseURL string
	AppsDomain  string // services are <label>.<AppsDomain>
	// PlatformDomain holds api. and s3.; a separate registrable domain from AppsDomain in production.
	PlatformDomain string
	AppsPort       string // the port in public URLs; empty for 443 (80 when HTTPOnly)
	HTTPOnly       bool   // serve plain HTTP only: no TLS, no certificates (decisions.md 050)
	HTTPSAddr      string
	HTTPAddr       string
	DevCADir       string // development: sign certificates with a local CA kept here
	CertDir        string // production: where Let's Encrypt certificates are stored
	ACMEEmail      string
	Platform       map[string]string // label → node address for Greyship's own hostnames (api, s3)
}

func LoadRouter(getenv func(string) string) (Router, error) {
	get := getter(getenv)
	dev := get("ENV", "production") == "development"
	r := Router{
		DatabaseURL: get("DATABASE_URL", ""),
		AppsDomain:  get("APPS_DOMAIN", devOrValue(dev, "localhost", "")),
		AppsPort:    get("APPS_PORT", devOrValue(dev, "8443", "")),
		HTTPSAddr:   get("ROUTER_HTTPS_ADDR", devOrValue(dev, ":8443", ":443")),
		HTTPAddr:    get("ROUTER_HTTP_ADDR", devOrValue(dev, ":8081", ":80")),
		DevCADir:    get("ROUTER_DEV_CA", devOrValue(dev, "tmp/devca", "")),
		CertDir:     get("ROUTER_CERT_DIR", "/var/lib/greyship/certs"),
		ACMEEmail:   get("ACME_EMAIL", ""),
		Platform:    map[string]string{},
		HTTPOnly:    get("HTTP_ONLY", "") == "true",
	}
	var errs []error
	// GREYSHIP_ROUTER_PLATFORM="@=127.0.0.1:3000,api=127.0.0.1:8080,s3=127.0.0.1:3900" on a single server; "@" is
	// the platform domain itself (the dashboard).
	for _, pair := range strings.Split(get("ROUTER_PLATFORM", ""), ",") {
		if strings.TrimSpace(pair) == "" {
			continue
		}
		label, addr, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || label == "" || strings.Contains(label, "-") || addr == "" {
			errs = append(errs, fmt.Errorf("GREYSHIP_ROUTER_PLATFORM: %q must be label=host:port, with a label without hyphens", pair))
			continue
		}
		r.Platform[label] = addr
	}
	if r.DatabaseURL == "" {
		errs = append(errs, errors.New("GREYSHIP_DATABASE_URL is required"))
	}
	if r.AppsDomain == "" {
		errs = append(errs, errors.New("GREYSHIP_APPS_DOMAIN is required outside development"))
	}
	r.PlatformDomain = get("PLATFORM_DOMAIN", r.AppsDomain)
	if r.DevCADir == "" && r.ACMEEmail == "" && !r.HTTPOnly {
		errs = append(errs, errors.New("GREYSHIP_ACME_EMAIL is required for Let's Encrypt certificates"))
	}
	return r, errors.Join(errs...)
}

// LoadBackup reads what greyship-backup needs: only where to store the dump.
func LoadBackup(getenv func(string) string) (Storage, error) {
	get := getter(getenv)
	s := loadStorage(get, get("ENV", "production") == "development")
	return s, errors.Join(s.check()...)
}

// GitHubApp is the Greyship GitHub App (decisions.md 042). Empty ID means GitHub deploys are off.
type GitHubApp struct {
	ID            int64
	Slug          string
	ClientID      string
	ClientSecret  string
	KeyFile       string
	WebhookSecret string
}

// LoadGitHubApp reads GREYSHIP_GITHUB_APP_* and GREYSHIP_GITHUB_WEBHOOK_SECRET; the API and builder share them.
func LoadGitHubApp(getenv func(string) string) (GitHubApp, error) {
	get := getter(getenv)
	raw := get("GITHUB_APP_ID", "")
	if raw == "" {
		return GitHubApp{}, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return GitHubApp{}, errors.New("GREYSHIP_GITHUB_APP_ID must be the app's number")
	}
	a := GitHubApp{
		ID: id, Slug: get("GITHUB_APP_SLUG", ""), ClientID: get("GITHUB_APP_CLIENT_ID", ""), ClientSecret: get("GITHUB_APP_CLIENT_SECRET", ""),
		KeyFile: get("GITHUB_APP_PRIVATE_KEY_FILE", ""), WebhookSecret: get("GITHUB_WEBHOOK_SECRET", ""),
	}
	var errs []error
	for name, v := range map[string]string{
		"GREYSHIP_GITHUB_APP_SLUG": a.Slug, "GREYSHIP_GITHUB_APP_CLIENT_ID": a.ClientID, "GREYSHIP_GITHUB_APP_CLIENT_SECRET": a.ClientSecret,
		"GREYSHIP_GITHUB_APP_PRIVATE_KEY_FILE": a.KeyFile, "GREYSHIP_GITHUB_WEBHOOK_SECRET": a.WebhookSecret,
	} {
		if v == "" {
			errs = append(errs, fmt.Errorf("%s is required with GREYSHIP_GITHUB_APP_ID", name))
		}
	}
	return a, errors.Join(errs...)
}
