# Decisions

A short log of architectural decisions. Newest at the bottom. Add an entry whenever the architecture shifts, and update `spec.md` / `architecture.md` in the same change.

Format:

```
## NNN — Title
Date · Status: proposed | accepted | superseded by NNN
Context: why this came up
Decision: what we chose
Consequences: what it changes or costs
```

---

## 001 — Go only
2026-10-23 · Status: accepted
Context: General platforms treat Go like any other language.
Decision: Only run Go apps. Builds are plain `go build`, no Dockerfiles or buildpacks.
Consequences: Simpler builder, faster cold starts, Go-specific features possible. No other languages.

## 002 — `main` listening on `$PORT` is the only contract
2026-10-23 · Status: accepted
Context: Lambda-style platforms require handler wrappers.
Decision: Any Go `main` that listens on `$PORT` works. No SDK required; an optional `greyship.dev/agent` package adds pprof.
Consequences: Any router/framework works. Platform must handle normal long-lived processes.

## 003 — CLI and dashboard are both clients of one public API
2026-10-23 · Status: accepted
Context: Not every user wants a terminal.
Decision: Every action is a `/v1` API endpoint first; CLI and dashboard call it.
Consequences: Feature parity by design. API design comes before UI work.

## 004 — Stage 0 runtime is containers + gVisor
2026-10-23 · Status: accepted
Context: First production target is a 12GB VPS without KVM; Firecracker needs KVM.
Decision: Run builds and apps in gVisor (`runsc`, systrap) sandboxes. Firecracker comes later on bare metal, behind a `runtime.Driver` interface.
Consequences: Cold starts slower than with snapshots at first (target p50 < 800ms). Swapping runtimes must not change agent logic.

## 005 — Stage 0 messaging without NATS
2026-10-23 · Status: accepted 2026-10-25
Context: Everything runs on one machine at first; NATS is an extra moving part.
Decision: Use a Postgres `jobs` table (`FOR UPDATE SKIP LOCKED`) for work queues and `LISTEN/NOTIFY` for events in Stage 0, behind an internal `bus` interface. Introduce NATS (JetStream) when there are multiple nodes, as a second implementation of the same interface.
Consequences: One fewer service on the 12GB VPS. `architecture.md` §2.7, §4 and §8 describe both stages.

## 006 — Name: Gorune
2026-10-23 · Status: superseded by 007
Context: Needed a short, ownable name that nods to Go. Many candidates (Rune, Tusk, Relay, Goship) were taken in the dev-tools space.
Decision: The platform is called **Gorune** ("go-rune", after Go's `rune` type). CLI binary `gorune`, config file `gorune.toml`, env prefix `GORUNE_`, agent package `gorune.dev/agent`.
Consequences: Always say and write the pronunciation "go-rune" early, since it can be misread as `go run`. Domains and handles must be secured before public posts.

## 007 — Name: Greyship
2026-10-24 · Status: accepted
Context: Gorune can be misread as `go run`. The product was announced publicly as Greyship.
Decision: The platform is **Greyship**. CLI binary `grey`, with `gray` and `greyship` also installed so every spelling works. Config `greyship.toml`, env prefix `GREYSHIP_`, agent package `greyship.dev/agent`, platform domain `greyship.app`.
Consequences: Secure greyship domains (and the grayship spelling as a redirect) and handles. All docs use Greyship.

## 008 — Go-native performance
2026-10-24 · Status: accepted
Context: The edge over general platforms should come from being Go-only.
Decision: Adopt pause-first scheduling, binary-only images, page-cache binaries, automatic `GOMAXPROCS`/`GOMEMLIMIT`, shared build caches and a TLS-handshake head start in Stage 0–1; automatic PGO and startup insights in v1.0.
Consequences: Idle timeout before stopping moves from 15 min to 24h with LRU eviction under memory pressure. See architecture.md §11 and spec.md §4.12.

## 009 — Microservices: private services and an internal router
2026-10-24 · Status: accepted
Context: Teams running an API gateway in front of private Go services (often gRPC) need service-to-service calls, and those services should still scale to zero.
Decision: Web services get `visibility` (public | private) and `protocol` (http1 | h2c). Same-project services call each other at `<service>.internal`, always through an internal router that reuses the edge router's wake-on-request, holding and metering logic and only accepts traffic from the same project. Domains can fan out to services by path prefix (`routes`). Projects can link several repos (v1.0).
Consequences: Data model gains `project_repos`, `routes`, `services.visibility/protocol/repo_id`; `domains` belong to projects. Instances still never talk to each other directly, so tenant isolation is unchanged. See spec.md §4.13 and architecture.md §12.

## 010 — Shared and per-service environment variables
2026-10-24 · Status: accepted
Context: Different services need different secrets; most projects also share a few values (e.g. a database URL) across services.
Decision: Two layers per environment. Shared variables go to every service; service variables go only to their service and override a shared key of the same name. A service can also exclude a shared key. A service never receives another service's own variables. `PORT` and `GREYSHIP_*` are reserved.
Consequences: New `env_excludes` table; env changes restart only affected services; the dashboard and `grey env ls --service` show the effective set with each key's source. See spec.md §4.5 (F-44–F-48) and architecture.md §4.4.

## 011 — Serverless rules and event-driven workers
2026-10-24 · Status: accepted
Context: Workers ran always-on by default (old F-35), which broke the serverless promise for every repo with a worker.
Decision: Adopt four serverless rules (spec.md §2) as a gate for every feature. Workers are event-driven by default: asleep until a message arrives on the built-in queue, an internal call, or a schedule. The built-in queue lives in Postgres in Stage 0. Always-on is opt-in only and labelled "always on · billed 24/7". Event-driven workers move from v1.x to public beta.
Consequences: New `queue_messages` and `queue_bindings` tables, a queue API (`/v1/queues`, `queue.internal`), scheduler wakes workers on queue activity. See spec.md F-35–F-35b and architecture.md §13.

## 012 — Release command for migrations
2026-10-24 · Status: accepted
Context: Production apps need DB migrations and one-off setup to run safely on deploy. The spec had no place for them.
Decision: An optional release command — a main package set in `[release]` or detected at `cmd/migrate` — runs once per deploy as a one-off sandboxed instance with the project's env, after build and before traffic switches. Failure or timeout fails the deploy and the previous version keeps serving. One release per project at a time. Rollback doesn't run releases or undo schema; docs recommend expand/contract migrations.
Consequences: Deployment status gains `releasing`; deploy flow gains a release step. See spec.md F-29 and architecture.md §4.1.

## 013 — Static frontends
2026-10-25 · Status: deferred (removed from scope 2026-10-25; may return later)
Context: Monorepos often ship a frontend (React, Vite, plain HTML) in `web/` next to the Go API, and teams deploy it separately and fight CORS.
Decision: Detect `web/index.html` or `web/package.json` (or `[static] dir`). Build with Node at build time only; serve static files only — no SSR, no Node runtime. With a frontend, it is served at `/` and the main API at `/api` (configurable; prefix kept by default). Without a frontend, the API stays at `/`. Static files are served by the router and never wake an instance.
Consequences: Not built. Greyship serves Go backends only; frontend hosting stays a non-goal. `//go:embed` frontends work because they are part of the Go binary.

## 014 — API docs (later)
2026-10-25 · Status: proposed
Context: Many Go APIs already have an OpenAPI spec (hand-written, swaggo, huma, ogen); some serve their own Swagger UI.
Decision (proposed): v1.x feature. Detect OpenAPI (2.0 or 3.x), render privately in the dashboard; link to the app's own Swagger UI if present; public docs URL only by opt-in. `grey docs` generates a standard `openapi.yaml` from code locally — never a custom format.
Consequences: Tracked in spec.md §9a until scheduled.

## 015 — Service detection and naming
2026-10-25 · Status: accepted
Context: Questions about multiple APIs, non-standard layouts and URL collisions showed gaps in detection and naming.
Decision: Detect root `main.go` and `cmd/main.go` (one service named after the project) and `cmd/<name>/main.go` (named after the folder); mains elsewhere are declared with `path` in `greyship.toml`. Project names come from the folder or repo, are slugified, editable, unique per team. Free subdomains are globally unique — a short suffix is added if taken — and names over 63 characters are shortened with a hash. Same-project calls use `<service>.internal`; cross-project calls use public URLs.
Consequences: `projects.slug`, `services.path`, `services.subdomain`. See spec.md F-10a, F-11, F-13–F-15, F-50–F-50b, F-91.

## 016 — Service type: read the code, config wins
2026-10-25 · Status: accepted
Context: Folder-name rules (`worker`, `cron-`) and magic comments guessed poorly and pushed users to rename things.
Decision: The builder reads the code: a main that starts a server is `web`, otherwise `worker`. `cron` is only set in `greyship.toml` (`type` + `schedule`). `type` in config always overrides. Folder-name rules and the `//greyship:cron` comment are dropped.
Consequences: Detection stays simple and users never change code. See spec.md F-11.

## 017 — Zero-config detection with a test corpus
2026-10-25 · Status: accepted
Context: `greyship.toml` must stay for overrides and mandatory specs (cron schedules), not something users write to deploy.
Decision: Detection scans the whole module for `package main` directories (with skip rules), names services by folder (parent prefix on collisions), reads code to choose web vs worker, and prints what it detected and why. `path` becomes an override, not a requirement. `internal/detect` is a pure, corpus-tested package that the coding agent grows with realistic layouts; accuracy must never regress.
Consequences: Supersedes the `cmd/`-only rule in 015. See spec.md F-11, F-14, F-15 and architecture.md §2.2.

## 018 — Env workflow: CLI for main vars, dashboard for overrides
2026-10-25 · Status: accepted
Context: Most Go projects keep one shared `.env` in the repo root. A wrangler-style CLI flow fits that; per-service overrides are rarer and easier to get right in a UI.
Decision: The CLI manages the project's main (shared) variables: `grey env set/rm/ls/pull` and `grey env push [.env]` with a diff and confirmation. `grey deploy` offers to load missing keys from a root `.env` but never uploads it silently; the file is never sent with source. Service overrides and exclusions are set in the dashboard only (the API supports them; the CLI shows them read-only) — a documented exception to CLI/dashboard parity.
Consequences: Updates decision 010's interface (not its model). See spec.md F-40–F-48 and AGENTS.md.

## 019 — Env files, `grey dev` and hard-coded internal names
2026-10-25 · Status: accepted
Context: Developers want one obvious local-vs-production split and a local runner that behaves like Greyship.
Decision: `.env` and `.env.local` are local only; `.env.prod` is production. `grey deploy` syncs `.env.prod` into production main variables (add/update, confirm on change, no deletes without `--prune`); GitHub deploys never read env files; tracked env files trigger a warning. `grey dev` builds and runs all services locally with `.env` + `.env.local` (or `.env.prod` with `--prod`) and resolves `<service>.internal` to local ports, so hard-coding internal names in code is safe everywhere. `grey dev` moves from the roadmap into private beta.
Consequences: Replaces the `.env` prompt in decision 018. See spec.md F-39a–F-40b, §4.9, F-91 and architecture.md §7a.

## 020 — Deploy compares env, project link file
2026-10-25 · Status: accepted
Context: Pulling stored secrets into `.env.prod` on every deploy would overwrite local edits before they ship and leave production secrets on every machine that deploys. Projects also need a way for a folder to remember its project without forcing a `greyship.toml`.
Decision: The stored variables are the source of truth. `grey deploy` compares `.env.prod` against them using value hashes and sends only added or changed keys after a key-only diff; it never rewrites the local file. Downloading is explicit (`grey env pull`, or a one-time offer when `.env.prod` is missing). The first init/deploy writes `.greyship/project.json` (no secrets, safe to commit); `greyship.toml` is never auto-created.
Consequences: Env API exposes key hashes for comparison. See spec.md F-10b, F-40, F-40a, F-40c and architecture.md §7b.
## 021 — Dashboard typeface and radius scale
2026-10-24 · Status: accepted
Context: design.md asked for a deliberate typeface choice before building `web/`, and gave two radius scales (§3: 8px controls, 10–14px panels; §6.1: 6/8/12px).
Decision: Keep Geist + Geist Mono, matching the design canvas. Use the §6.1 radius scale: 6px controls, 8px inputs and buttons, 12px panels. Tailwind's default colour, radius, type-size and weight scales are removed so only design.md tokens exist in code.
Consequences: design.md §3 updated to the single radius scale. Any new token needs approval (design.md §8).

## 022 — Dashboard primitives: Radix + shadcn, lucide icons
2026-10-24 · Status: accepted
Context: Hand-rolled controls made the dashboard look basic, and menus, dialogs and a command palette need solid keyboard and screen-reader behaviour.
Decision: Build interactive controls on Radix (`radix-ui`) using shadcn components, copied into `web/components/primitives/` and restyled to design.md tokens (no default shadcn theme). `cmdk` powers the ⌘K command palette. Icons come from `lucide-react` (one set, 16px). Brand assets are SVGs traced (potrace) from the generated PNGs in `web/public/brand/source/`: mark, lockup, dark variants and app icon in `web/public/brand/`; favicon `web/app/icon.svg`, touch icon `web/app/apple-icon.png`.
Consequences: New web dependencies: radix-ui, cmdk, lucide-react, class-variance-authority, clsx, tailwind-merge, tw-animate-css. After `shadcn add`, remap the component's colour, radius and size classes to our tokens before use.

## 023 — Passwordless login: email code, Google, GitHub
2026-10-25 · Status: superseded by 053: sign-in is email and password only
Context: Spec F-1 offered GitHub OAuth or an email magic link. Magic links break when the email is opened on another device, and many developers sign in with Google.
Decision: No passwords. Sign in with a 6-digit code sent by email (10-minute expiry, 5 attempts, single use, stored hashed), or with Google or GitHub OAuth (authorization code + PKCE via `golang.org/x/oauth2`). An OAuth login links to an existing account only when the provider reports the email as verified. The dashboard gets an opaque session cookie (hashed in Postgres); the CLI keeps the device flow (F-2) and uses bearer tokens. Login emails are sent through Resend's HTTP API; in development they are written to a local outbox folder, never to logs.
Consequences: New tables `oauth_identities`, `login_codes`, `sessions`; new dependency `golang.org/x/oauth2`; `spec.md` F-1 and `architecture.md` §2.1 and §3 updated. The `web/` sign-in mock needs a code-entry step and a Google button when it is wired to the API.

## 024 — Development on macOS
2026-10-25 · Status: accepted
Context: The founder develops on an Apple Silicon Mac, not the Fedora laptop the docs assumed. gVisor, namespaces, cgroups and iptables only exist on Linux.
Decision: Go services, the CLI and the dashboard run natively on macOS; Postgres and MinIO run in Docker. The node agent and its gVisor sandboxes run inside a Linux VM (Docker's VM or Lima). User code still never runs directly on the host. Production is a Linux amd64 VPS, so builds cross-compile and the first VPS deploy is the real test of amd64, gVisor without KVM, and SELinux.
Consequences: `AGENTS.md` (Environment) and `architecture.md` §7 updated. SELinux labelling issues surface only on the server.

## 025 — Temporary domain: greyship.tech
2026-10-25 · Status: accepted
Context: greyship.app and greyship.dev (decision 007) are not bought yet; the founder owns greyship.tech.
Decision: Use greyship.tech for the running services for now: Go module path `greyship.tech`, API and dashboard URLs, and the login-email sender. Specs keep the intended public names (`greyship.dev/agent`, `greyship.dev/jobs`, `*.greyship.app`); product copy in `web/` and `beta/` keeps its example URLs until one sweep when the final domains are bought.
Consequences: Moving to the final domain is a module-path rename plus config, cheap while nothing is published. The public package paths must be live before the first public release.

## 026 — `grey deploy` infers the project; `grey init` is optional
2026-10-26 · Status: accepted
Context: Nobody should need a setup command before their first deploy (spec §2, F-11). Decision 020 introduced the project link file but left open what deploy does without one, and architecture §7b said the CLI would ask.
Decision: In an unlinked folder, `grey deploy` takes the project name from the last element of the `go.mod` module path (dropping `/vN`), else the folder name, creates the project in the token's team and writes `.greyship/project.json` next to `go.mod`. It asks nothing in the normal case. If the name is taken in the team, it never deploys there silently: in a terminal it asks with default no (no creates `<name>-2`); without a terminal it fails and points to `grey init --yes` or `--name`. `grey init` uses the same name rule and exists only to choose the name up front.
Consequences: Two unrelated repos with the same module name can't overwrite each other's production by accident. CI deploys of a new repo need either a committed link file or a unique name. Spec F-10a/F-10b and architecture §7b updated.

## 027 — Detection reads code syntactically; TOML via BurntSushi
2026-10-26 · Status: accepted
Context: Decision 016 says detection reads the code to tell web from worker. Full type-checked call graphs need `golang.org/x/tools` and every module downloaded, which makes detection slow, networked and unusable offline in `grey dev`. `greyship.toml` needs a TOML parser, which the standard library lacks.
Decision: `internal/detect` parses with `go/parser` only. It follows calls from `main()`/`init()` through the module's own packages (method calls resolve to every same-named method in reached packages), and recognises third-party server starts from a table keyed by import path. Generic method names (`Run`, `Start`, `Serve`, `Listen`) need the framework imported in the calling file. Build constraints are evaluated for linux/amd64 with CGO off. `greyship.toml` is parsed with `github.com/BurntSushi/toml` (mature, no dependencies), added to the approved list.
Consequences: Detection is pure, fast (this repo in ~40ms) and works offline. Known blind spots: a server started only inside a third-party wrapper that isn't in the table, or a same-named method on a non-server type in a reached package. Both are fixed by adding a corpus case and a table entry, or by `type` in `greyship.toml`.

## 028 — Public site phases and blog comments in web/
2026-10-28 · Status: accepted
Context: The landing page goes public before the platform exists, with a `/blog` that explains the architecture to build trust, plus moderated questions and comments. The Go API isn't hosted yet, and the site deploys on Vercel.
Decision: `web/` serves the public site as well as the dashboard. One switch, `NEXT_PUBLIC_SITE_MODE`, sets the phase: `coming-soon` (the default when unset; only `/` and `/blog` are public, everything else redirects home, CTAs point to the blog and the beta survey), later `waitlist`, then `live` (the full dashboard). Blog comments are Next.js server actions writing to their own small Postgres database (Drizzle), separate from the platform database. Posts need a name and email (the email is never shown or returned by public queries), pass validation, a honeypot and a per-IP-hash rate limit, and appear only after the founder approves them in `/blog/admin`. This is a second, narrow exception to Go-only: marketing-site features only. Articles describe the designed system, not the current build stage. SEO is built in: metadata, canonical URLs, Open Graph images, JSON-LD, sitemap, robots, and `llms.txt` / `llms-full.txt` generated from the same article data.
Consequences: The site can ship and change independently of the platform. If comments ever need platform accounts, they move behind the Go API. New dependencies in `web/` (drizzle-orm, postgres, zod, server-only, drizzle-kit) match `beta/`.

## 029 — Garage for object storage; S3 presigning in the standard library
2026-10-28 · Status: accepted
Context: `grey deploy` (step 6) uploads source tarballs straight to object storage with presigned URLs (architecture §4.1). The docs named MinIO for development and the VPS, but MinIO no longer publishes pullable images (Docker Hub and quay.io both refuse). Talking to S3 needs Signature V4 signing, and no S3 client is on the approved dependency list.
Decision: Use Garage (`dxflrs/garage`, a small S3-compatible server built for self-hosting) in `deploy/compose.dev.yml` and on the VPS. `deploy/garage-init.sh` gives the dev node a role, imports a dev-only access key and creates the `greyship` and `greyship-test` buckets; `make infra` runs it. `internal/objstore` implements SigV4 query signing (presigned PUT with `Content-Length` signed, presigned GET, and HEAD via a presigned URL) in about 150 lines of standard library, verified against AWS's published test vector and against Garage in integration tests. No SDK is added. Path-style URLs; endpoint, region, bucket and keys come from `GREYSHIP_S3_*`.
Consequences: The code is not tied to Garage: AWS S3, Cloudflare R2 or MinIO built from source work by changing config. Garage needs a public S3 hostname in production because the CLI uploads to it directly. Decision 024's mention of MinIO now reads as Garage. If we ever need multipart uploads or other S3 features, revisit the SDK question then.

## 030 — Our own module proxy; a Lima VM for build sandboxes
2026-10-29 · Status: accepted
Context: Builds must run in gVisor with no network except a Go module proxy (architecture §2.2), which the docs left as "Athens or a small custom proxy". On the Mac, Docker Desktop's VM only has runc and can't keep gVisor installed. Decision 024 allowed "Docker's VM or Lima".
Decision: The proxy is ours: `greyship-modproxy` (`internal/modproxy`, about 200 lines), which serves only the GOPROXY protocol and sum.golang.org paths, forwards to proxy.golang.org and sum.golang.org, and caches immutable files on disk. Athens would be another large service with its own storage backends to run and secure on a 12GB box for a job this small. Sandboxes join an `--internal` Docker network whose only other member is the proxy, at a fixed address passed with `--add-host` (Docker's DNS doesn't work under gVisor). On the Mac, a Lima VM (`deploy/lima/greyship.yaml`, Ubuntu + Docker + runsc, nothing shared from the Mac) runs the sandboxes; Postgres and Garage stay in Docker Desktop. The builder drives the VM's Docker over the forwarded socket with the docker CLI (separate arguments, never a shell string) and refuses to start without runsc.
Consequences: The production VPS runs the same Docker + runsc + proxy setup, so dev and prod match. Private modules need proxy support later (credentials per team). The proxy is a shared component every build reaches, so it stays small and allow-list based.

## 031 — Certificates on demand per hostname; a local CA in development
2026-10-29 · Status: accepted
Context: Every public service needs HTTPS at `<service>-<project>.greyship.tech` (F-52). Architecture §7 planned one wildcard certificate through ACME DNS-01, which needs an API client for whoever hosts the domain's DNS (a new dependency) and DNS credentials on the server. On a laptop there is no public DNS or ACME at all.
Decision: In production the router uses CertMagic's on-demand issuing: the first TLS handshake for a hostname obtains its certificate (TLS-ALPN-01 or HTTP-01) from Let's Encrypt. A decision function allows only hostnames in the routing table, so nobody can make us request certificates for arbitrary names. Certificates live on the node's disk; shared Postgres storage comes with several routers. DNS is one wildcard record pointing at the VPS. In development the router serves `*.localhost` (browsers resolve it to 127.0.0.1) and signs per-host certificates with a local CA it creates in `tmp/devca`. That CA is name-constrained to `localhost`, so even once trusted it can't vouch for real sites.
Consequences: No new dependency or DNS credentials. Let's Encrypt allows 50 certificates per registered domain per week, enough for the beta; past that, or for wildcard needs, add DNS-01 later without changing the router's shape. The first visit to a brand-new service waits a few seconds for its certificate.

## 032 — One public entry on the server: platform hostnames through the router
2026-10-29 · Status: accepted
Context: On a single server the edge router owns ports 80 and 443. The API and the storage endpoint need public HTTPS names too: the CLI calls the API, and uploads source straight to storage with presigned URLs.
Decision: The router also serves Greyship's own hostnames, configured as `GREYSHIP_ROUTER_PLATFORM` (`api=127.0.0.1:8080,s3=127.0.0.1:3900`). App labels always contain a hyphen (`<service>-<project>`), platform labels never do, so they can't collide. Platform routes keep the client's Host header (presigned signatures depend on it) and skip the app limits (10MB bodies, wake-and-hold). They get certificates on demand like apps. Postgres and Garage listen on loopback only. Servers have no Go toolchain, so migrations ship as a binary, `greyship-migrate`, with the migrations embedded.
Consequences: Only SSH, 80 and 443 are open. One more reason the router must stay small and robust: the API and uploads now depend on it. With several routers (Stage 2) the platform hostnames move behind their own load balancer entry, unchanged for clients.

## 033 — The server runs Ubuntu LTS with AppArmor
2026-10-30 · Status: accepted
Context: The docs assumed a RHEL-family server with SELinux enforcing. The rented VPS runs Ubuntu, which the founder knows well, and the development sandbox VM is already Ubuntu 24.04, where builds and instances were tested under gVisor.
Decision: Production runs Ubuntu LTS (24.04 or 22.04), amd64. Mandatory access control stays on: AppArmor, which Docker applies to every container by default, alongside gVisor, which is the main isolation boundary. `bootstrap.sh` detects the OS: apt and `ufw` on Ubuntu (SSH, 80 and 443 open), dnf and firewalld on Rocky/Alma, so either still works. Disabling AppArmor or SELinux is never a fix.
Consequences: The server matches the tested dev VM. Supersedes the SELinux assumption in decision 024's consequences and AGENTS.md (updated).

## 034 — `grey deploy` builds on the developer's machine when Go is installed
2026-10-31 · Status: accepted
Context: A brand-new gin project took 118s to deploy on the production server: 69s compiling third-party libraries from scratch inside gVisor, 32s of `go vet`, 7s copying the prepared standard library cache. The shared build cache plan (`docs/plans/shared-build-cache.md`) attacks this with a platform-wide cache, which is a lot of security-sensitive machinery. But Greyship is Go only, Go cross-compiles with one environment variable, and nearly every Go developer already has Go and a warm build cache for their own project.
Decision: When `go` is on PATH, `grey deploy` compiles each service and the release command locally for `linux/<platform CPU>` with `CGO_ENABLED=0`, `-trimpath` and `-ldflags="-s -w"`, and uploads only the gzip'd binaries the project doesn't already store (content-addressed per project). The deployment is recorded with `build_mode = 'local'` and no source. The builder runs a `build.verify` job instead of a build: checksum and size, then Go's embedded build info (Linux, the right CPU, no cgo, `-trimpath`), then the binary becomes an artifact exactly as if it had been built here. `go vet` runs on the developer's machine beside the deploy, warnings only. Without Go, or with `--remote`, the old path (upload source, build in gVisor) is unchanged; GitHub and dashboard deploys will use it too.
Trust: nothing about isolation changes. Every binary already runs in gVisor as hostile code, whoever compiled it; a server build never made the code trustworthy, it only kept the *build* from touching the host. The verify step is there to catch honest mistakes with a clear message, not to stop attackers (build info can be faked). We lose the guarantee that the deployed binary matches a source tarball we hold; we never promised that, and the commit SHA is still recorded.
Consequences: A deploy with a warm local cache is bound by upload speed, not compile time; an unchanged service uploads nothing. Upload size grows from a small source tarball to compressed binaries (typically 3–10MB per service), worth it against minutes of server CPU. Server builds become the fallback, so the shared global cache plan waits until GitHub deploys make server builds common again; its vet-after-go-live and team-cache steps still apply to the remote path. Different local Go versions are fine: the artifact records the version the binary was built with. New job kind `build.verify`, table `uploaded_binaries`, columns `deployments.build_mode` and nullable `source_*` (migration 00015), endpoint `GET /v1/build-target`.

## 035 — The Go gopher on the landing page
2026-10-31 · Status: accepted
Context: The design brief bans illustrations (design.md §1, §6.1) so the product's own output carries the pages. The founder wants the Go gopher on the site: Greyship exists only for Go, and the gopher says that faster than any sentence.
Decision: One exception. A waving gopher sits beside the "About Greyship" section of the landing page (above the heading on phones), at 112px on phones and 240px from md up, captioned "Built only for Go." It is not in the hero, so the project canvas stays the main visual, and it never appears in the dashboard. The gopher was designed by Renée French under CC BY 4.0, so the site footer credits her with a link to the license.
Consequences: Any other illustration still needs its own decision. If the picture is replaced, the credit stays as long as it is a gopher.

## 036 — A grey wireframe sea behind the landing page
2026-11-03 · Status: accepted
Context: The founder found the all-black landing page flat. The brief (design.md §1, §6.1) bans decorative imagery. Greyship is a ship; a sea under it gives the page a theme without competing with the product shots.
Decision: The landing page only (not Features, Pricing, But…, the blog or the dashboard) has one fixed background: a grey wireframe ocean (`web/public/ocean.jpg`), greyscale, at 60% opacity and 140% brightness, faded to black at the top of the viewport like a night sky, swelling slowly over 16s (still under reduced motion). Because the mesh is bright, that page raises its text tokens (text white, muted `#D4D4D8`, dim `#B4B4B9`) so copy stays readable. Product shots keep their solid panels on top.
Consequences: One 309KB image on the landing page, loaded with priority. Any other page that wants a background needs its own decision. If text contrast slips when the image changes, adjust opacity before the text tokens.

## 037 — Apps on their own domain, greyshipapp.tech
2026-11-03 · Status: accepted
Context: Apps ran at `*.greyship.tech`, the same registrable domain as the API, storage and the coming dashboard. Browsers treat subdomains of one registrable domain as the same site: once the dashboard signs people in with cookies, any user's app could send same-site requests that carry those cookies (CSRF; CORS doesn't stop requests from being sent), and anyone could deploy `login-greyship.greyship.tech` to phish. Vercel (`vercel.app`), Fly (`fly.dev`) and Render (`onrender.com`) keep apps on separate domains for this reason. `greyship.app` (decision 007) isn't affordable yet.
Decision: Apps move to `*.greyshipapp.tech` (`GREYSHIP_APPS_DOMAIN`) until `greyship.app` is bought at production; the site, dashboard, API and storage stay on `greyship.tech` (`GREYSHIP_PLATFORM_DOMAIN`). The router serves platform labels (`api`, `s3`) only under the platform domain and app labels only under the apps domain. In development both are `localhost`. Session cookies are hardened as well (decision 038).
Consequences: Existing app URLs change once (`api-temp.greyship.tech` → `api-temp.greyshipapp.tech`): labels are stored without a domain, so every URL moves with the setting. A second wildcard DNS record is needed. Moving to `greyship.app` later is the same settings change. Later, both apps domains should be submitted to the Public Suffix List so browsers isolate one app from another as well.

## 038 — Hardening browser sessions: __Host- cookie, Origin check, signup allowlist
2026-11-03 · Status: accepted
Context: Step 17 opens the dashboard, the first time browsers hold Greyship sessions. CORS limits who can read responses, not who can send requests, and SameSite=Lax still sends cookies on same-site requests. Opening sign-up would also let anyone deploy on the single beta server.
Decision: The production session cookie is `__Host-gs_session`, which browsers only accept when Secure, `Path=/` and without a `Domain`, so no subdomain can plant or overwrite it. Every state-changing request that could ride on that cookie must carry the dashboard's `Origin`; bearer-token requests are exempt because a cross-site page can't attach the token. `GREYSHIP_SIGNUP_ALLOWLIST` limits who may create an account during the private beta, checked only after the person proves the email, so it can't be used to probe who's invited; existing accounts always sign in. OAuth callbacks go through the dashboard's origin. `GREYSHIP_PUBLIC_URL`, which only held the callback base, is removed.
Consequences: Scripts that sign in with a cookie (`login.sh`, `dev-login.sh`) send the dashboard's Origin. Changing the cookie name signs everyone out once. The Google and GitHub OAuth apps need their callback URLs changed to `https://greyship.tech/v1/auth/oauth/<provider>/callback`.

## 039 — Server builds run on Fly Machines; build tiers by plan
2026-11-03 · Status: accepted (being built)
Context: Server builds (no Go installed, `--remote`, and soon GitHub and dashboard deploys) ran in gVisor on the 6-CPU VPS that also runs user apps. A brand-new gin project took 118s, and two builds took 4GB that apps could use. A good Go build wants 4 or more CPUs to itself.
Decision:
- Server builds run on **Fly.io Machines** with dedicated (performance) CPUs. The builder on the VPS stays the control plane, and the build goes through one `Runner` interface with two implementations: the existing gVisor sandbox (development, tests) and Fly (production).
- Build machines are a small pool, stopped when idle. A machine with a cache volume only ever builds for one team; paid plans get a per-team volume, and Free builds start cold on a volume-less machine.
- The machine gets presigned URLs and a one-time job token, never secrets or credentials, and reports logs and results to the API.
- Machine size, included build minutes, concurrency, queue priority and timeout come from the plan:
  - Free: performance-2x, 100 min, 1 concurrent, lowest priority, no overage, 10 min timeout.
  - Pro: performance-4x, 500 min, 2 concurrent, $0.01 per minute.
  - Team: performance-8x, 1,500 min per team, 4 concurrent, $0.015 per minute.
  - Enterprise (new): custom.

  Minutes count server builds only, from machine start to the last upload. Beyond the included minutes, overage is drawn from the plan's included usage credit, then billed under the spend cap. Free is refused until the month resets.
- User apps keep running on our own nodes (VPS and gVisor). Fly as overflow capacity for apps is deferred.

Rejected:
- Builds on the VPS: too slow, and they compete with apps.
- AWS Fargate: cold caches every build, a NAT gateway (about $30+ a month) to restrict the network, and a second vendor.
- Fly shared CPUs: they guarantee 6.25% of a core, so a 2-minute build gets throttled to about half a core.
- All apps on Fly: about 10× our own nodes' cost at around 500 apps.

Consequences:
- Our cost is about $0.006 per 2-minute build on performance-4x, plus about $0.15 per GB-month for volumes. Overage is about 3× cost, inside the spec's 60–70% margin target.
- Counting overage against the included credit keeps a 1-seat Team plan from going negative ($20 credit plus about $8.55 of full build minutes against $25).
- New: `cmd/greyship-buildd`, `internal/build/fly` (Machines REST API over `net/http`, no SDK), `build_machines`, deployment build metering, job priority and group limits. The VPS frees about 4GB for apps.
- Local builds (decision 034) stay for now. The founder intends to remove them later with a decision that supersedes 034; until then they're free and unmetered.
- Supersedes the global layer in `docs/plans/shared-build-cache.md`: per-team volumes replace it.

## 040 — Object storage on Cloudflare R2
2026-11-03 · Status: accepted (being built)
Context: Build machines on Fly (decision 039) must download sources and upload binaries over the internet. Garage on the VPS would mean serving them through the VPS, it keeps irreplaceable data on one disk, and it costs memory there.
Decision: Production object storage moves to **Cloudflare R2**: sources, binaries, profiles and nightly Postgres backups. Every client reaches it only with presigned URLs: the CLI, build machines and node agents. `internal/objstore` is plain S3 SigV4 and works unchanged; R2 uses region `auto`. Development keeps Garage in Docker.
Migration steps:
1. Create the bucket and a scoped API token.
2. Copy existing objects once with `rclone sync` from Garage to R2.
3. Switch `GREYSHIP_S3_*` in `/etc/greyship/greyship.env`.
4. Release, and check a deploy end to end.
5. Remove Garage from `deploy/vps/compose.yml` and `s3=` from `GREYSHIP_ROUTER_PLATFORM`.
6. Drop the `s3.greyship.tech` DNS record.

Consequences: The VPS keeps no objects; Postgres is the only state left on it, until the separate Neon change. R2 has no egress fees, which matters because the CLI and agents download binaries. Supersedes decision 029 for production (Garage stays for development) and the `s3.` platform route of decision 032.

## 041 — Build machines fetch modules from proxy.golang.org over open internet
2026-11-03 · Status: accepted (being built)
Context: VPS builds reach the internet only through our module proxy on a private Docker network (decision 030). Fly Machines can't join that network and Fly has no egress firewall.
Decision: Build machines have open outbound internet and use `GOPROXY=https://proxy.golang.org`, with `GOSUMDB` on, `GOFLAGS=-mod=readonly`, `GOTOOLCHAIN=local` and no private modules.
Reasoning:
- `go build` with cgo off executes no user code: no build scripts, and `go generate` isn't run. The code that runs on the machine is ours and the Go toolchain's.
- Module integrity is enforced by the checksum database, whichever network path is taken.
- The machine holds nothing worth stealing: no secrets, no long-lived credentials, only URLs that expire.
- `-mod=readonly` stops a build from rewriting go.mod or go.sum, and `GOTOOLCHAIN=local` stops a `toolchain` line from downloading a different toolchain.

Consequences: Our module proxy remains only for the gVisor runner in development. Private modules need credentials per team later, and a decision of their own.

## 042 — Deploys from GitHub through a GitHub App, tarballs and a status sweep
2026-11-04 · Status: accepted
Context: Beta testers should deploy by pushing to GitHub, without Go or the CLI. GitHub offers OAuth apps (act as the user, with broad scopes) and GitHub Apps (installed per account, with per-repository consent and short-lived installation tokens).
Decision:
- Greyship is a **GitHub App** with read access to contents and metadata and write access to commit statuses, subscribed to `push`. An installation is recorded for a team only after the installing person's own GitHub login proves they can access it. No GitHub token is ever stored: installation tokens are minted from the app's private key when needed and cached in memory for under an hour.
- The source is the **tarball** of the pushed commit, not `git clone`: no git binary, no credentials on disk, no history. It is repacked with the same rules as `grey deploy`, so both paths build the same files.
- Commit statuses are posted by a **sweep** over deployments whose reported state is behind, rather than hooks in the builder and scheduler: it retries by itself and needs no changes where deployments change state.

Consequences: Private repositories work with no extra setup. Submodules aren't fetched (tarballs don't include them). PR previews, comments and a deploy button for any branch build on the same pieces after the beta. The app's private key lives only in `/etc/greyship/github-app.pem`.

## 050 — Greyship is open source and self-hosted
2026-11-06 · Status: accepted; plans removed by 053
Context: The founder decided Greyship should be software people run on their own server, not a platform we host for customers. Our own server becomes one instance of it, where the private beta runs.
Decision: The core is open source under AGPL-3.0 and installs on a single Ubuntu or Debian server with one command. The first account on an instance is its **owner** (`users.is_owner`; `GREYSHIP_OWNER_EMAIL` names one too); owners get the admin view. Signup is closed after the first account unless the owner opens it (`GREYSHIP_SIGNUP_OPEN`), allowlists emails, or a beta list names them. Plans are an optional setting: without `GREYSHIP_PLANS=on` every team is on **Unlimited** (no build-minute limit, no plan UI); our instance turns plans on for now. Builds run in gVisor on the same server by default (Fly and S3 or R2 stay optional); email is optional (login codes go to files in the outbox folder when no provider is set). The CLI logs in to any instance (`grey login <server>`). Billing is out of the open-source core; a paid hosted cloud may come later. The private repository stays the source of truth: docs, newsletter, beta app and landing site live only there and keep deploying on Vercel, and the public repository (github.com/greylabss/greyship) is produced from it by an export script.
Consequences: The hardcoded admin email (048) is replaced by the owner flag. Signup defaults changed: an empty allowlist no longer means everyone. The Free, Pro and Team plans, build minutes and pricing only exist on instances that set `GREYSHIP_PLANS=on`. Contributions to the public repository have to be merged back into the private one until that changes.

## 056 — Projects can be deleted, in the background
2026-11-07 · Status: accepted
Context: Projects could be created and renamed but not deleted, which a beta user hit at once. Deleting rows is easy; the hard part is not leaving a running container or stored files behind.
Decision: A member who can write (not a viewer) deletes a project with `DELETE /v1/projects/{id}` and the project's slug as confirmation (`{"confirm": "<slug>"}`; a mismatch is 400). The request, in one transaction, sets `projects.deleting_at` and enqueues a `project.delete` job; from then on the project is invisible: lists, lookups, routing, `ServiceURLs` and push deliveries skip it. The builder, which already holds the storage credentials, runs the job (`internal/projdel`): it asks every live instance to stop (the same `instance.stop` command as scaling uses), waits up to a minute for them to stop and, if they haven't, fails the attempt so the job is retried; then it deletes the project's stored objects (sources, files bundles, built and uploaded binaries) through the new `objstore.Delete`, best effort and logged; then it deletes the project row, and every table that points at it cascades (deployments, services, instances, env vars, app logs, the GitHub connection, notifications). Dashboard: Project → Settings has the name and a Delete project dialog that asks for the slug; CLI: `grey projects delete <slug>` (asks for the slug, or `--yes`). Rejected: deleting the rows at once (an agent can't find a container whose row is gone, so it would leak), and a soft delete that keeps everything (disk and names would stay taken).
Consequences: Migration 00029 adds `projects.deleting_at`. A deleted project's slug is free again once the job finishes, normally within seconds. Deleting is not undoable; a backup is the only way back.

## 055 — Every team creates its own GitHub App from the dashboard
2026-11-07 · Status: accepted. Replaces the hand-made app of 042
Context: Push-to-deploy and private repositories need a GitHub App. Making one by hand on github.com and putting six values and a key file into the server's env was the hardest step of self-hosting, and one shared app would make the instance admin the owner of everyone's repository access. Coolify shows it can be a button, per person.
Decision: Any team member who can write (not a viewer) opens New project or Settings and presses **Create GitHub App** (optionally for an organisation, with a name). Greyship uses GitHub's app manifest flow: the API signs a short-lived `state` (team, user, 10 minutes) and returns the manifest and the GitHub page to post it to; the person's browser posts it, they confirm on GitHub, and GitHub sends the browser back to `/v1/github/manifest/callback` with a one-time code. The API checks the state against the signed-in user and that they can still write to the team, exchanges the code for the app's credentials (`POST /app-manifests/{code}/conversions`) and stores them in the `github_apps` table, one row per team, with the client secret, private key and webhook secret **sealed with the master key**. The manifest asks only for repository contents (read), metadata (read), commit statuses (write) and the `push` event; the app is private; its webhook is `<dashboard>/v1/github/webhook/<team id>` and its install callback the existing `/v1/github/callback`. Each team's app has its own webhook address and secret, and `Service.Push` acts only on installations that belong to the team the delivery was signed for, so one team's app can't make another team's project deploy. A call that carries an installation id finds the app through the installation's team (`github.Dynamic`); public repositories need no app at all. The API and the builder read apps from the database on use (cached 30 seconds per team, cleared after a save), so a new app works at once with no restart. A team can remove its app, which also removes its installations and the repositories connected through them. The `GREYSHIP_GITHUB_APP_*` and `GREYSHIP_GITHUB_WEBHOOK_SECRET` env vars and the key file are removed: one way to configure it. Rejected: one instance-wide app made by the admin (the admin would own everyone's access, and GitHub app names are global), keeping the env vars as a second way, and several apps per team (nothing needs it yet).
Consequences: New table `github_apps` (migration 00028); backups and restore carry the apps with the database. GitHub can only deliver pushes to a public address, so on a private or `localhost` dashboard address the page warns (import and Deploy latest still work). Replacing an app means removing it and creating another; its installations must be redone.

## 054 — Upgrades keep everything; backups move an instance
2026-11-07 · Status: accepted
Context: People who run Greyship will update it and sometimes reinstall or move the server. Neither should lose accounts, projects or env vars.
Decision: Re-running the installer (`greyship-admin upgrade`) is an in-place upgrade: it keeps `/etc/greyship` and the database and storage volumes, and now first writes a backup to `/var/backups/greyship` (the newest 5 are kept), taken by the new release's own `greyship-admin`. `greyship-admin backup [folder]` makes one root-only file with a Postgres dump and `/etc/greyship`; `greyship-admin restore <file>` puts it on a freshly installed Greyship of the same or a newer version: it replaces the database, carries over the secrets the data depends on (master key, cookie and proxy secrets, push key, GitHub app, mail settings, owner email) and keeps the new server's own database and storage settings, then runs migrations. Built binaries and uploaded sources in storage are not in a backup (they are only needed to roll back; a project can be deployed again from its source); the domain settings are not either (run `greyship-admin domain` again). `grey upgrade [version]` updates the CLI by running the same installer as a first install into the folder the CLI lives in.
Consequences: A backup file holds the master key, so it must be kept private and copied off the server. A restore is a replace, not a merge, and asks for confirmation. Rollbacks to deployments from before a restore don't work.

## 053 — A plain self-hosted instance: email and password only, no plans, per-user limits
2026-11-07 · Status: accepted. Supersedes 023 and 049, amends 050, 051 and 052
Context: Greyship became software you run yourself (050). Several things from the hosted-product design no longer fit: emailed login codes, Google and GitHub sign-in, signup rules, a beta list with a badge, and plans with build minutes. The beta is itself just an instance: the founder adds each tester as a user and sends them their password.
Decision: Sign-in is an email and a password. No emailed codes, no OAuth sign-in, no verification email, no self-service signup. The first visitor of a new instance creates the owner account once (`POST /v1/auth/setup`, only while there are no users, and only for `GREYSHIP_OWNER_EMAIL` if it is set); the owner (an admin) creates every other account in the dashboard with a password. A forgotten password is reset by whoever has shell access to the server: `greyship-admin reset-password <email>` runs `greyship-api reset-password`, sets a random password, prints it once and ends the account's sessions, like Coolify's `root:reset-password`. The device flow behind `grey login` stays and is approved after password sign-in. The plan system is removed (`internal/plan`, `teams.plan`, `GREYSHIP_PLANS`, build minutes, billing pages). In its place the admin can limit each user's memory, CPU and concurrent builds (`teams.max_memory_mb`, `max_cpu_millis`, `max_concurrent_builds` on the user's personal team; NULL is no limit, which is what every new user has). The scheduler checks memory and CPU, under a per-team lock, whenever it records an instance (a release, or a scale-up); a release over the limit fails with a message that says which limit and the live version keeps serving; a scale-up over it is skipped. Concurrent builds become the build queue's group limit for that team. The Fly build runner takes its machine size from configuration instead of a plan. Email stays only for deploy notifications. Rejected: keeping emailed codes as an option (needs working email, which self-hosters often lack), a "forgot password" email (same reason, and it is an account-takeover path), limits per team instead of per user (every user has exactly one team today; teams can get limits when they can be shared), and an instance-wide default limit (not asked for).
Consequences: Migration 00027 drops `login_codes`, `oauth_identities`, `users.beta_tester` and `teams.plan` and adds the three limit columns. `golang.org/x/oauth2` is no longer a dependency. The private beta app's list is no longer read by the platform. A limit needs headroom over an app's normal use, because a deploy briefly runs the old and new version together. Spec §6–7 and F-1, F-56a, F-56b, F-70–F-72 are rewritten; the rest of the spec still describes some hosted-product features (teams, billing-era wording) and is to be rewritten.

## 052 — Email is set up in the dashboard, not in the environment file
2026-11-06 · Status: accepted; the Email page serves deploy notifications only since 053
Context: Email is optional on a self-hosted instance (050). Turning it on meant editing `/etc/greyship/greyship.env` and restarting, which the people installing Greyship shouldn't need to do.
Decision: The instance admin opens Admin > Email in the dashboard, pastes a Resend API key and the sending domain, picks a sender name, switches email on and can send a test message to themselves. The settings are one row in `mail_settings` (migration 00026). The key is sealed with the master key (AES-GCM, bound to the label `mail:resend`), never logged and never returned by the API; saving without a key keeps the stored one. Mail goes out from `login@<domain>` (login codes) and `notifications@<domain>`. `mail.Dynamic` picks the sender at the moment of sending: saved settings replace `GREYSHIP_RESEND_API_KEY`; with nothing saved the environment key applies as before; with neither, login codes are written to the outbox folder and notification emails are skipped (bell and browser push still work). Changes apply at once. Only an admin session may read or change them. Rejected: showing the key again after saving (a leaked session would leak the key), verifying the domain through Resend's domains API (the test message shows the same error and needs no extra permission).
Consequences: New table `mail_settings`. A wrong master key makes the saved key unreadable, so email stays off until the key is saved again. The admin must verify the domain in Resend themselves.

## 051 — Accounts the instance owner creates, with passwords
2026-11-06 · Status: accepted; passwords are the only way in since 053
Context: A self-hosted instance (050) has closed signup, and its owner wants to give someone an account directly (for example a user named "beta" with a password) and choose whether that person is an admin. Emailed codes need working email, which self-hosters may not have set up.
Decision: Admins (`users.is_owner`) create accounts in the dashboard (`POST /v1/admin/users`: email, name, password, admin or not), reset passwords and grant or remove admin (`PATCH /v1/admin/users/{id}`). The password is optional; an account without one signs in with a code as before. Anyone with a password signs in at `POST /v1/auth/password/login`, and changes it at `POST /v1/me/password`. Passwords are hashed with PBKDF2-HMAC-SHA256, 600,000 iterations, a random salt per password, from the standard library (`crypto/pbkdf2`), so there is no new dependency. A wrong password and an unknown email give the same answer and take the same time. Failures are counted in the database: 8 per email and 30 per IP in 15 minutes, then the login is refused with 429, even for the right password. The last admin can't be removed. Created users get the instance's default plan and a personal team, like any signup. Only a session of an admin can use the admin endpoints; API tokens never can. Rejected: a one-time invite link (needs working email), argon2/bcrypt (an extra dependency for the same result), and the admin choosing a password the user can't change (users can change their own).
Consequences: New columns `users.password_hash` and table `password_failures` (migration 00025). The sign-in page offers "Use a password instead". An admin sees every user's role and sign-in method in `/admin/users`. Passwords are shown to the admin once, in their browser; the server stores only the hash and never logs it.

## 049 — Signups come from the beta app's list, through a small API
2026-11-06 · Status: superseded by 053: no beta list; the admin adds testers as users
Context: The private-beta applicants live in the beta survey app's own database (`beta/`). Copying them into `GREYSHIP_SIGNUP_ALLOWLIST` by hand doesn't scale, and every tester was promised a badge.
Decision: The beta app exposes `POST /api/testers/check` (shared secret, answers only yes or no: applied and not declined). Greyship's API asks it when someone new signs up, and at later sign-ins for users not yet marked; testers get `users.beta_tester` and a checkmark in the dashboard. The allowlist still adds people (the founder, partners). If the beta app is down, new signups fail closed and existing users are unaffected. Rejected: giving the platform the beta database's URL (it would depend on another app's schema and hold a second database credential); a one-time copy (stale the moment someone new applies).
Consequences: Approving someone means leaving them undeclined in the beta admin; no restart. At the public beta, `GREYSHIP_BETA_URL` is removed and signup opens; the badges stay.

## 048 — A read-only operator view, hardcoded to one account
2026-11-06 · Status: accepted, the hardcoded account replaced by the owner flag in 050
Context: During the beta the founder must debug any tester's failed deployment quickly, from its id alone, without SSH or SQL.
Decision: `/admin` in the dashboard and `/v1/admin/*` in the API, for one hardcoded account (the founder's, matched against the verified email of the signed-in session; replaced by the owner flag in 050). API tokens are never accepted there, so a leaked CLI token can't read other teams' data; everyone else gets 404, so the endpoints don't reveal themselves. It is read-only: no retry, stop or delete. Rejected: an admin role in the database (more to build and to get wrong for a single operator; can come with a support team), and actions (more damage if the account is ever taken over).
Consequences: Anyone who controls that email account (its Google or GitHub login, or its inbox for login codes) can read every team's deployments and logs, but never secrets: env values aren't part of the view. Changing the operator means changing `admin.Email` and releasing.

## 047 — The CLI is released from a separate public repo
2026-11-05 · Status: superseded by 050: the CLI is released from github.com/greylabss/greyship together with the server and the dashboard (`deploy/open-source/release.sh`); the installer's source is still `web/public/install`
Context: Beta testers need to install `grey`, but the platform's repository is private, so its releases and raw files can't be downloaded by anyone else, and `go install` can't fetch the `greyship.tech` module path.
Decision: `shellhaki/grey` is a public repository holding only the installer (`install.sh`), a README and GitHub Releases with `grey` for macOS and Linux (amd64, arm64) plus `checksums.txt`. `deploy/cli/release.sh vX.Y.Z` builds them from the private repo (version stamped in with `-X main.version`) and publishes them with `gh`. The installer's source of truth is `web/public/install`, which the dashboard also serves at `greyship.tech/install`; the release script copies it to the public repo. The installer downloads through GitHub's `releases/latest/download/...` links (no API rate limit), verifies the sha256 and never uses sudo.
Consequences: The platform code stays private. Each CLI release is one command. Homebrew and Scoop can point at the same release files later.

## 046 — Notifications through the job queue: bell, email and Web Push without a provider
2026-11-05 · Status: accepted
Context: Beta testers should hear when a deploy succeeds or fails and when a project is made, by email and on their phone or computer, without watching the dashboard.
Decision: Every place that ends a deployment or creates a project enqueues a `notify` job in the same transaction, so a notification is sent exactly for what happened, even across restarts. One worker in `greyship-api` writes a bell entry per recipient (unique per person, event and subject, with per-channel sent marks so retries never repeat), then sends email through Resend and browser pushes with the Web Push standard, implemented with the standard library (no new dependency, no push provider account: browsers' own push services carry it, keyed by our VAPID key). The recipient is the person who started it; the team isn't copied, to keep it quiet. Subscription endpoints are limited to the known browser push services. Rejected: a third-party push provider (another account and dependency for the same result); notifying the whole team (noisy; can come with team settings later); the in-memory mail queue used for login codes (it's in memory on purpose, and notifications must survive restarts).
Consequences: Push works on desktop browsers and Android, and on iPhone only from a home-screen install. Losing the VAPID key means every browser has to turn push on again. Each notify job is a few queries and one email.

## 045 — Runtime files listed in greyship.toml, shipped in the instance image
2026-11-05 · Status: accepted
Context: Instances hold only the binary (F-83), so an app that opens a repo file at runtime (`scripts/install.sh`, `templates/`) fails on Greyship although it works locally and on platforms that ship the whole repo. `//go:embed` fixes it but needs code changes and can't reach files above the package.
Decision: `files = [...]` in `greyship.toml` lists repo files and folders to ship, once per project. They are packed into a deterministic bundle with the source's rules (never `.git`, `.env` files or symlinks), stored content-addressed per project like binaries, recorded on the deployment, and put into the instance image under `/srv`, read-only, with `/srv` as the working folder. Rejected: shipping the whole repo by default (slower deploys from the developer's machine, bigger images on every node, more of the user's code on servers), and mounting the files from the node at runtime (another moving part per instance; the image is already built once per server).
Consequences: Repo-relative paths work with no code changes. A changed bundle makes a new image on each node, once. Rollbacks restore their own files. Limits: 50 MB and 5,000 files.

## 044 — Web services may listen on any port; guest init passes connections through
2026-11-05 · Status: accepted
Context: Every web service had to read `$PORT` and listen on `0.0.0.0:$PORT`. Apps that hard-code `:3000`, or listen on `localhost`, failed their health check on the first deploy, one of the most common first-deploy failures. The router reaches an instance only on its published port, which Docker fixes when the sandbox is created, before anyone knows which port the app will choose.
Decision: Guest init, already PID 1 in the sandbox, watches the sandbox's listening sockets (`/proc/net/tcp`, which gVisor provides) until the app opens a port. If it's 8080 on every address, nothing changes. Otherwise init listens on 8080 on the sandbox's own address and copies each connection to the app's port. It works on raw TCP, so every framework and protocol passes through. There's no port setting: the lowest port wins and the logs say which. Rejected: rewriting `net.Listen` at build time with `go build -overlay` (patches the standard library on every Go release, needs the same for local builds, misses sockets opened another way); connecting the router straight to the app's port on the sandbox address (no extra hop, but needs the agent and router to learn the port at runtime, and still can't reach a `localhost`-only app; worth revisiting with Firecracker).
Consequences: Apps on other ports pay one in-sandbox copy, about 0.14ms per request at 1 CPU; apps on `$PORT` pay nothing. The copying uses the instance's own CPU budget. An app whose main port isn't its lowest one (say pprof on 6060, the app on 9000) is served on the wrong port until it reads `$PORT`; its logs say so.

## 043 — Public repositories by link, updated on demand
2026-11-04 · Status: accepted
Context: Some people want to paste a GitHub link and deploy, as on Render, without installing an app. Without the app there are no webhooks, and GitHub's REST API allows only 60 unauthenticated calls an hour per IP, shared by every user of the server.
Decision: A public repository can be added by link. It deploys at once; later commits deploy only when someone asks (Deploy latest in the dashboard, `grey github deploy`), the same as Render's public Git repositories, with no polling and no commit status. Branches are read from git's own smart-HTTP ref advertisement and the source from codeload, the endpoints `git` itself uses, which aren't the rate-limited REST API. Private repositories, push-to-deploy and statuses still need the app. Deploy latest also serves app-connected projects, and a failed commit may be deployed again.
Consequences: No token ever touches a public repo's fetch. If GitHub changes the smart-HTTP or codeload endpoints, public imports break while app imports keep working; both are long-stable parts of how git works.

