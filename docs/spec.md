# Greyship — Product Spec

> Status: living spec of the self-hosted product (decisions.md 050, 053) · Owner: Haki · Last updated: 2026-11-07. Requirements that are not built yet say so.

---

## 1. What we're building

Greyship is an open-source (AGPL-3.0), self-hosted serverless runtime for Go. You install it on a server you own with one command. People on that instance push a Go repo (from the CLI, the dashboard, or GitHub) and every service in it goes live on a URL, scales to zero when idle, and wakes on the next request. The instance's admin adds the people, and can limit how much each may use.

**One line:** Your own place to deploy Go backends — APIs, workers, crons — with one command, that holds no CPU while idle.

### Why Go only
Being single-language is the strategy, not a limitation. Because we only run Go we can:
- build every app the same way (`CGO_ENABLED=0 go build`) with no Dockerfile and no buildpacks,
- start apps fast enough that scale-to-zero feels instant,
- show Go-specific insight (goroutines, GC, pprof) no general host bothers to build,
- keep the platform small enough for a small team to run.

### Who it's for
| Persona | Situation | What they want |
| --- | --- | --- |
| **Solo developer with a VPS** | Side projects, bots, hackathon APIs on a server they already pay for | One command to install, no ops, many apps on one cheap box |
| **Small team** | 1–10 people, Go API + worker | Their own internal platform: logins for the team, logs that work, deploys on every push |
| **Agency / studio** | Many small client APIs | One instance for every client app, with limits per person so one can't starve the rest |
| **Learner or beta cohort** | Learning Go, or a group trying a product | An admin who adds each person with a password, and a path from `go run` to a public URL |

### Non-goals
- Other languages.
- Hosted databases (people connect external ones).
- Frontend hosting of any kind (static sites, React/Next.js, SSR). Frontends that are part of the Go binary via `//go:embed` work automatically because they are just Go.
- GPUs, long-running batch jobs over 15 minutes, WebSockets over 1 hour.
- Billing, plans or a hosted cloud (decisions.md 050). A paid hosted version, if it ever comes, is a separate product.
- High availability across servers (one server per instance for now).

---

## 2. Product principles
1. **`main` is the contract.** If it runs with `go run`, it runs on Greyship. No SDK, no wrappers.
2. **Zero config by default, full config when needed.** Detection first; `greyship.toml` overrides.
3. **CLI and dashboard are equal.** Every action exists in both, backed by one public API. One documented exception: per-service env overrides are dashboard-only (F-47a).
4. **Easy to run.** One command installs it, no domain is needed to start, a forgotten password is fixed on the server, and an upgrade keeps everything.
5. **Respect Go's runtime.** Background goroutines get time to finish; graceful shutdown is honored.
6. **Fast is a feature.** Deploy in under a minute; cold start under 1s now, under 100ms later.

### Serverless rules (every feature must pass all four)
1. **People who deploy never manage servers.** They never see, size, patch or SSH into machines; only the instance's owner looks after the one server.
2. **Idle holds no CPU by default.** Anything that stays running while idle (`min_instances`, workers) is an explicit opt-in, labelled "always on · holds memory 24/7" in the CLI and dashboard.
3. **Scales automatically,** up with load and down to zero, without the user doing anything.
4. **Usage follows actual running time,** and what a person may use is limited by their admin (F-71), never by reserved capacity by default.

A feature that would break a rule is redesigned (e.g. made event-driven) or shipped only as a clearly labelled opt-in.

---

## 3. Core concepts

| Concept | Meaning |
| --- | --- |
| **Account** | A user: an email and a password. The first account on an instance is its owner and admin; the admin creates the rest. |
| **Team** | Owns projects. Every account has a personal team; sharing a team between accounts is not built yet. |
| **Project** | One repo. Holds services, env vars, domains. |
| **Service** | One Go `main` package inside the project. Type: `web`, `worker`, or `cron`. |
| **Deployment** | An immutable build of a project at a commit. Has status and logs. |
| **Instance** | A running copy of a service's deployment. 0..N. |
| **Environment** | `production` and `preview` (per PR) in v1. |

---

## 4. Functional requirements

### 4.1 Auth & accounts
- **F-1** Sign in with an email and a password (decisions.md 053). There are no emailed codes, no Google or GitHub sign-in and no self-service signup. The first visit to a new instance shows **Create the owner account** (email, name, password); it works once, and when `GREYSHIP_OWNER_EMAIL` is set (the installer sets it when given `--email`) only that email may create it. The owner becomes the instance's **admin** (F-56) and adds everyone else (F-56a). A forgotten password is reset on the server with `greyship-admin password <email>` (a new random password, printed once, the account signed out everywhere); there is no "forgot password" email.
- **F-2** CLI login via device flow (`grey login` opens browser, CLI polls).
- **F-3** Personal API tokens, scoped to a team, revocable.
- **F-4** Teams: invite by email, roles `owner`, `admin`, `member`, `viewer`. *(Not built: every user has their own personal team for now.)*
- **F-5** Notifications (decisions.md 046): **deploy succeeded**, **deploy failed** and **new project** reach the person who started the deploy (`grey deploy`, the dashboard, a redeploy or rollback; for a push to GitHub, whoever connected GitHub for the team) or created the project, while they're still on the team. Each arrives three ways:
  - the dashboard **bell**: newest first, unread dot, a click opens the deployment or project; reading the list marks it read;
  - **email**, with a link to the dashboard page; a failure says why and that the live version keeps serving;
  - **browser push**, once the person clicks Turn on in Settings > Notifications in that browser (desktop browsers and Android; on iPhone only once Greyship is added to the home screen). It arrives with the dashboard closed.
  Settings > Notifications, and `grey notifications email|push on|off [event]`, switch email and push per event; all are on by default, and the bell always keeps the entry. Messages never contain secrets, env values or app output, only the one-line reason a deploy failed.

### 4.2 Projects & detection
- **F-10** Create a project from CLI (`grey init`), dashboard, or by importing a GitHub repo.
- **F-10c** Delete a project: Project → Settings → Delete project (type the project's slug), `grey projects delete <slug>` (also asks for the slug, or `--yes`), or `DELETE /v1/projects/{id}` with `{"confirm": "<slug>"}`; viewers can't. The project disappears at once; its apps are stopped and its deployments, env vars, logs, domains and stored files are removed in the background (decisions.md 056). It can't be undone. Renaming is in the same Settings page.
- **F-10b** Project link: the first `grey init` or `grey deploy` in a folder writes `.greyship/project.json` (`team`, `project`, `id`), so later commands know which project the folder belongs to (they look in the current folder and its parents). The link sits next to `go.mod` (the module root). `grey init` is optional: it only exists to pick the name up front. `grey deploy` in an unlinked folder creates the project from the inferred name without asking anything. If that name is already taken in the team, it asks in a terminal "Deploy to it? [y/N]" (no creates `<name>-2`), and in CI it stops with a hint (`grey init --yes` to link, `--name` to choose another). It holds no secrets and is safe to commit, which lets teammates and CI deploy to the same project. `greyship.toml` is never created automatically; it only exists when someone needs an override. If the name is already taken in the team, `grey init` offers to link the folder to that project instead (`--yes` in scripts).
- **F-10a** Project name: inferred from the last element of the `go.mod` module path (`github.com/acme/orders-api` → `orders-api`, a `/vN` suffix is dropped), else the folder name (CLI), or the repo name (GitHub import). `grey init` lets you change it before the first deploy; any project can be renamed later. Stored as a slug: lowercase letters, digits and dashes (`Acme API` → `acme-api`). Unique within a team.
- **F-11** Detection runs on every deploy and must get common projects right **with no `greyship.toml` at all**. `greyship.toml` is for overrides and the few things that can't be inferred (today: cron schedules). Nobody should need to write config to deploy a normal Go backend.

Finding services — every `package main` directory in the module is a candidate:

| Where the main lives | Result |
| --- | --- |
| repo root (`main.go`) or `cmd/main.go` | 1 service named after the project |
| `cmd/<name>/` | named after the folder |
| anywhere else (`services/orders/`, `apps/billing/cmd/server/`) | named after its folder; if two would share a name, the nearest meaningful parent folder is prefixed (`orders-server`, `payments-server`). Generic folders (`cmd`, `app(s)`, `service(s)`, `svc`, `server(s)`, `internal`, `src`) are passed over when prefixing |
| `cmd/migrate/` (or `[release]` in config) | the release command (F-29), not a service |
| `vendor/`, `testdata/`, `examples/`, `_`/`.`-prefixed folders, files excluded from a linux/amd64 build (`//go:build ignore`, `tools`, `_windows.go`, …), mains only run by `//go:generate go run` | skipped |
| a folder with its own `go.mod` (a separate module) | skipped until multi-repo projects (F-95) |

Service type:
- Greyship reads the code during the build. A main that starts a server (`http.ListenAndServe`, `net.Listen`, or a framework's equivalent such as gin `Run`, echo `Start`, fiber `Listen`) is `web`; a main that doesn't is `worker`.
- `cron` is never guessed: set `type = "cron"` and `schedule` in `greyship.toml`.
- `type` in `greyship.toml` always wins over the guess. Users never have to change their code.
- Every deploy prints what was detected and why (`api  web  starts a server in cmd/api/main.go:22`), so a wrong guess is obvious and fixable with one line of config.
- Detection accuracy is a product feature: it is backed by a growing test corpus of real-world layouts and code styles (architecture.md §2.2), and every reported misdetection becomes a new test case.

- **F-12** Go version from `go.mod` `go`/`toolchain` directive; default latest stable.
- **F-13** `greyship.toml` (optional, next to `go.mod`) overrides per service: path, type, memory, cpu, min/max instances, concurrency, grace window, schedule, health path, region.
- **F-13a** Runtime files: `files = ["templates/", "scripts/install.sh"]` (at the top of `greyship.toml`, before any `[section]`) ships those repo files and folders with every service of the project, at the same relative paths, read-only, in the app's working folder, so `os.Open("scripts/install.sh")` works as it does locally (decisions.md 045). Entries are paths inside the repo (no `..`, no wildcards, at most 100). The source rules apply: ignored files, `.git`, `.env` files and symlinks are never shipped. An entry that matches nothing fails the deploy with its name. At most 50 MB and 5,000 files. With Go installed, `grey deploy` uploads them only when they change. Redeploys and rollbacks bring back that deployment's files. Without `files`, nothing but the binary is shipped (F-83); `//go:embed` works either way. Detection warns (never fails) when code reads a repo path at runtime that `files` doesn't ship, e.g. `cmd/install/main.go:42 reads scripts/install.sh while running, but only the binary is shipped; add it to greyship.toml (files = ["scripts/install.sh"]) or use //go:embed`. It sees string-literal paths passed to `os.Open`, `os.ReadFile`, `os.ReadDir`, `os.Stat`, `os.DirFS`, `template.ParseFiles`/`ParseGlob` and `http.Dir`.
- **F-14** `path` in config is an override: it renames a detected service, includes a main that detection skipped, or (with `exclude = true`) leaves one out. A declared name that matches a detected one overrides its settings.
- **F-15** Service names must be unique in a project and are slugified like project names. If detection can't produce unique names even with parent prefixes (e.g. root `main.go` and `cmd/main.go`), the deploy fails with a clear error and the one-line config fix.

```toml
files = ["templates/", "scripts/install.sh"]  # repo files the app reads at runtime (F-13a)

[services.orders]
path = "./services/orders"  # a main package outside cmd/

[services.api]
memory = "256MB"
min_instances = 0
max_instances = 10
concurrency = 100
grace = "30s"

[release]                # runs once per deploy, before traffic switches
command = "./cmd/migrate" # any main package in the repo
timeout = "5m"

[services.cleanup]
type = "cron"
schedule = "0 3 * * *"

[services.orders]
visibility = "private"   # reachable only as orders.internal
protocol = "h2c"         # gRPC / Connect

[[routes]]               # one domain, several services
domain = "acme.com"
path = "/payments"
service = "payments"
```

### 4.3 Deploys
- **F-20** `grey deploy` builds the module, uploads, streams the deploy log, prints URLs. Before uploading it prints the services it found and why. Two ways to build (decisions.md 034):
  - **On your machine (default when Go is installed):** each service and the release command is compiled locally for Greyship's Linux CPU (`CGO_ENABLED=0 GOOS=linux GOARCH=<platform> go build -trimpath -ldflags="-s -w"`). Only binaries the project doesn't already have are uploaded (gzip, at most 20 binaries, each at most 200 MB, 100 MB compressed). A compile error stops the deploy before anything is sent. `go vet` runs alongside and its warnings are printed; they never block or delay the deploy. Greyship checks each binary before deploying it (F-23).
  - **On Greyship (no Go installed, or `--remote`):** the CLI packages the working dir (honoring `.gitignore` + `.greyshipignore`) and uploads the source. `.git`, `node_modules`, symlinks and every `.env` / `.env.*` file are never uploaded, whatever the ignore files say. The compressed source can be at most 100 MB; above that the CLI lists the biggest files.
- **F-21** Import from GitHub in the dashboard: install the Greyship GitHub app and pick a repository, or paste a **public** repository's link (`https://github.com/owner/repo`, optionally `/tree/<branch>`) without installing anything, like Render's public Git repositories. **Import** creates and connects the project but doesn't deploy. A review page then shows what the deploy will run (every detected service with its type and why, the release command, skipped folders, or the problem that stops a deploy), lets the person set env vars (names from the repo's `.env.example` are suggested), and has the **Deploy** button. `grey github import` imports and deploys in one step. Every GitHub-connected project has **Deploy latest**, which deploys the branch's newest commit. A commit already live or deploying isn't deployed twice; one whose deploy failed can be deployed again, for example after adding its env vars. Choosing any branch to deploy comes after the beta.
- **F-22** GitHub integration (decisions.md 042, 043): only repositories connected through the app deploy on push. Public repositories added by link update only with Deploy latest (or `grey github deploy`) and get no commit status. A push to the connected branch (the repo's default branch unless chosen otherwise) deploys to production, built on Greyship's build machines. The commit gets a "Greyship" status on GitHub (pending, then ✓ or ✗) linking to the deployment. GitHub deploys use the project's stored env vars; `.env` files in the repo are never used. *After the beta:* PR → preview deploy with its own URL, commented on the PR, torn down when the PR closes. Each team creates its own GitHub App in the dashboard (New project or Settings → GitHub: one click, then confirm on GitHub; decisions.md 055), with no env vars; until it has, the team can still import a public repository by link, which needs no app. The app only reads the repositories the person picks and writes commit statuses, and a team can remove it.
- **F-23** Build on Greyship (F-20's remote mode, and later GitHub and dashboard deploys): on a dedicated build machine sized by the instance's configuration (`GREYSHIP_FLY_MACHINE_CPUS`, `GREYSHIP_FLY_MACHINE_MEMORY_MB`; decisions.md 039, 053), `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"` per service, then `go vet` (warn only). The build log streams live to `grey deploy` (and the dashboard), which ends with the build time and the month's build minutes (§7). A build may take at most 10 minutes on Free and 15 on paid plans. Modules come from the public Go module proxy and are checked against the Go checksum database; private modules aren't supported yet. Each team's build cache is its own, never shared. A binary built on the developer's machine is checked instead of built: its checksum and size must match what was declared, and the build information Go embeds must say Linux, the platform's CPU, `CGO_ENABLED=0` and `-trimpath`. Anything else fails the deployment with the reason (for example "api was built for darwin/arm64; Greyship runs linux/amd64"). The check catches mistakes, not attackers; every binary runs in the sandbox either way (architecture.md §5).
- **F-24** Build fails, or the new version fails its health check → deployment `failed`, previous deployment keeps serving. The log ends by saying so: `nothing changed: the live version (deployment 1a2b3c4d) keeps serving`, or `nothing changed: this project has no live version yet`.
- **F-25** Health check: `web` services must accept TCP connections on their port (F-30) within 30s (or 200 on `health_path` if set); `worker` services must still be running after a few seconds. Fail → deployment `failed`, no traffic switched, and the app's last output lines are shown. `health_path` arrives with the per-service settings.
- **F-26** Zero-downtime switch: new instances healthy before old ones drain. `grey deploy` follows the deploy to the end: it shows each instance starting and ends with "Live" once traffic has switched and the previous version is stopping.
- **F-27** `grey rollback [deployment]` puts a previous deployment's binaries live again (no rebuild); without an id it goes back to the deployment that was live before the current one, and an id prefix is enough. `grey redeploy` starts the live deployment again on fresh instances (to pick up env var changes, for example). The dashboard offers both: **Redeploy** on the live deployment and **Roll back to this** on earlier ones, on the deployment page and in the list. Each makes a new deployment that records which one it came from, starts its binaries with the project's current env vars, and switches traffic only once they're healthy, like any release; only deployments that went live (`ready` or `superseded`) can be used (409 `not_deployable` otherwise). Rollback does **not** run the release command and does not undo schema changes; the docs explain this and recommend backward-compatible migrations (expand, deploy, then contract).
- **F-29** Release command (DB migrations, one-off setup): a main package set in `[release]` or detected at `cmd/migrate`. It is built with the rest of the deploy and runs **once per deploy**, in a sandbox, with the production (or preview) env vars, after the build and **before** any traffic switch.
  - Exit 0 → deploy continues. Non-zero exit or timeout (default 5 min, max 15 min) → deployment `failed`, logs shown, previous deployment keeps serving.
  - Its output appears as a "Release" step in the build log and dashboard.
  - Only one release runs per project at a time; a newer deploy waits for it.
  - Release time is billed as normal running time.
- **F-28** Keep the last 10 deployments per project for rollback. *Not enforced yet: every deployment that went live is kept and can be rolled back to. Cleaning up older ones (their binaries are shared with newer deployments, so only unreferenced objects may be deleted) comes after the beta.*

### 4.4 Runtime behaviour
- **F-30** `web` services may listen on any port, any address (`:3000`, `localhost:3000`, `:8080`), with any framework or protocol over TCP; Greyship serves it on the service's URL. `$PORT` is still set, and an app listening on it on every address is reached directly; otherwise the app's lowest port is served (decisions.md 044). If the app opens several ports, its logs say which one is served. Opening no port at all within 30s fails the health check.
- **F-31** Scale to zero: after the last request, instance stays running for the **grace window** (default 30s), then is **paused**. Paused instances stay in memory (pause-first, see F-80) and are **stopped** only when the node needs the memory (least recently used first) or after the **idle timeout** (default 24h).
- **F-32** Requests to a stopped/paused service are held at the router (max 30s) while an instance starts.
- **F-33** Concurrency: one instance handles up to `concurrency` in-flight requests (default 100). Above that, the router requests another instance up to `max_instances`.
- **F-34** `min_instances ≥ 1` keeps a service always warm.
- **F-35** `worker` services are event-driven and scale to zero by default. A sleeping worker is woken by: a message on its built-in queue (F-35a), an internal call to `<worker>.internal`, or a schedule. It goes back to sleep after its queue is empty and the grace window passes.
- **F-35a** Built-in queue: every project gets durable queues. Apps enqueue with `POST /v1/queues/<name>/messages` (or the `greyship.dev/jobs` package) and workers receive messages from `queue.internal`, with acknowledgements, retries with backoff and a dead-letter queue.
- **F-35b** Always-on workers (e.g. consuming an external Kafka or a websocket feed) are an explicit opt-in: `min_instances = 1`, shown everywhere as "always on · billed 24/7". Never the default.
- **F-36** `cron` services run on schedule, are killed after `timeout` (default 5 min, max 15 min); runs and exit codes recorded.
- **F-37** Shutdown: `SIGTERM`, then `SIGKILL` after 10s.
- **F-38** Request limits: 30s default response timeout (max 300s on paid), 10MB request body (configurable), HTTP/1.1 + HTTP/2, WebSockets up to 1h. An open WebSocket keeps its instance awake and billed; the docs and pricing page say so.
- **F-39** Injected env: `PORT`, `GREYSHIP_SERVICE`, `GREYSHIP_DEPLOYMENT_ID`, `GREYSHIP_REGION`, `GOMAXPROCS` and `GOMEMLIMIT` (see F-82), plus user env vars.

### 4.5 Env vars & secrets
- **F-39a** Env files in the project root:

| File | Used by | Meaning |
| --- | --- | --- |
| `.env` | `grey dev` | local development values |
| `.env.local` | `grey dev` | personal local values; overrides `.env` |
| `.env.prod` | `grey deploy` (and `grey dev --prod`) | production values |
| `.env.preview` | preview deploys (optional) | preview values |

`.env` and `.env.local` are local only and never leave the machine.

- **F-40** CLI works on the project's **main (shared) variables**, like wrangler:
  - `grey env set KEY=VALUE [--env preview]`, `grey env rm KEY`, `grey env ls`
  - `grey env pull` writes the stored production variables to `.env.prod` (asks before overwriting an existing file). Downloading secrets is always explicit.
  - `grey env push [file]` loads a whole file (default `.env.prod`). It shows a diff (added / changed keys, values hidden) and asks to confirm. `--yes` skips the prompt for CI.
  - `--env preview` targets preview deployments; default is production.
- **F-40a** Deploy and `.env.prod`: the stored variables (dashboard) are the source of truth. `grey deploy` **compares** `.env.prod` with them — never overwrites the local file — and shows a key-only diff before the release step:

  ```
  .env.prod vs production:
    + NEW_FLAG        only in file → will be added
    ~ STRIPE_KEY      different    → will be updated
    = 12 keys match
    · SENTRY_DSN      only stored  → kept
  Continue? [y/N]
  ```

  Keys only in the file are added, changed keys are updated, keys only stored are kept (removed only with `--prune`). It asks only when something would change; `--yes` skips the prompt. Values are sent only to the encrypted env store, never with the source, and never read during builds.
- **F-40b** If `.env`, `.env.local` or `.env.prod` is tracked by git, the CLI warns loudly and suggests adding it to `.gitignore`. GitHub-triggered deploys never read env files; they use the variables already stored for the project.
- **F-40c** If the project already exists and there is no `.env.prod`, deploy offers once: *"Download production variables to .env.prod? [y/N]"*. It never writes secrets to disk without that yes or `grey env pull`.
- **F-41** Values encrypted at rest; shown masked in dashboard; reveal requires re-auth.
- **F-42** Changing env vars restarts only the services that receive the changed variable (no rebuild).
- **F-44** Two layers per environment: **main (shared)** variables go to every service in the project; **service overrides** go only to one service.
- **F-45** Overrides: when a key exists in both layers, the service value wins for that service. Other services keep the main value.
- **F-46** Least privilege: a service receives only main variables plus its own overrides. It never receives another service's overrides, so `payments` can hold a key that `api` can't read.
- **F-47** Opt-out: a service can exclude a main key entirely instead of overriding it with a value.
- **F-47a** Service overrides and exclusions are managed **in the dashboard only**, on the service's Env page. The CLI shows them read-only and links to the page. (The public API supports them, so automation is possible later.)
- **F-48** Effective view: the dashboard and `grey env ls --service x` (read-only) show the final variables a service will get, each marked `main`, `override` or `service-only`.

Resolution order for one service in one environment (later wins):

| Layer | Example |
| --- | --- |
| Platform-injected (`PORT`, `GREYSHIP_*`, `GOMAXPROCS`, `GOMEMLIMIT`) | always present; `PORT` and `GREYSHIP_*` can't be overridden |
| Main (shared) — CLI, `.env` push or dashboard | `DATABASE_URL` for all services |
| Service override — dashboard only | `DATABASE_URL` for `payments` only, `STRIPE_KEY` only in `payments` |
- **F-43** *(v2)* Envi integration: link a project to an Envi workspace.

### 4.6 Domains & TLS
- **F-50** Every public web service gets `<service>-<project>.<apps domain>`: the instance's apps domain (`GREYSHIP_APPS_DOMAIN`), or an `apps-<ip>.sslip.io` name until the owner adds a domain (decisions 037, 050). Apps should never share a registrable domain with the dashboard or API. `grey deploy` prints it when the deploy is live. Private services get no public URL (see F-90). Workers and crons never get a URL. Scaled copies of a service share its one URL.
- **F-50a** Default subdomains are unique across all users of the instance. If `<service>-<project>` is already taken, a short random suffix is added (`api-acme-api-k7x2.<apps domain>`) and shown at deploy time. Once assigned, a service keeps its subdomain until it's deleted or renamed.
- **F-50b** Subdomain labels are at most 63 characters. Longer `<service>-<project>` names are shortened and a short hash added (`stripe-webhook-a3f9.<apps domain>`). Custom domains have no such limits.
- **F-51** Custom domains (decisions.md 057): Project → Domains, `grey domains add <hostname> [--service name]`, `grey domains remove <hostname>` and `grey domains` (list), or `POST|GET|DELETE /v1/projects/{id}/domains`. The page shows the exact DNS record to create (a CNAME to the service's default address, or an A record to the server for a name with no subdomain) with copy buttons and a status (verification required, valid configuration, or points somewhere else); it checks again by itself every few seconds while a domain is waiting, and there is a Refresh button (`POST /v1/projects/{id}/domains/{hostname}/verify`, `grey domains verify <hostname> [--wait]`). For a domain on Cloudflare a **Configure Cloudflare** button opens Cloudflare to add the record after the person approves it (Domain Connect, decisions.md 059; `GET .../connect`, `grey domains connect <hostname>`); it only appears when Cloudflare has Greyship's template. A hostname belongs to one web service, can't be under the platform or apps domain, and goes away with its service or project. Viewers can look but not change.
- **F-52** Automatic TLS via ACME for all domains; renewal automatic. Certificates are issued the first time a hostname is visited, only for hostnames Greyship serves.
- **F-53** HTTP → HTTPS redirect on by default.

### 4.7 Logs, metrics & Go insight
- **F-60** Live logs: `grey logs -f [--service x]` and dashboard stream; stdout/stderr per instance, JSON logs pretty-printed.
- **F-61** Log search by text, time, level, service. Retention is the instance's own setting.
- **F-62** Request metrics: count, status codes, p50/p95/p99 latency, cold starts and their duration.
- **F-63** Instance metrics: CPU, memory, instance count over time.
- **F-64** Go runtime metrics (no code change): goroutines, heap in use, GC pause, GC count.
- **F-65** *(not built)* On-demand profiles: `grey pprof cpu|heap|goroutine <service>`; dashboard flame graph. Requires `import _ "greyship.dev/agent"`.
- **F-66** *(not built)* Alerts: goroutine leak (monotonic growth), memory near limit, error-rate spike, crash loop. Delivery via email + webhook.

### 4.8 Limits per user
- **F-70** There are no plans, prices or billing in Greyship (decisions.md 050, 053). Everyone on an instance can build and run as much as the server allows, unless the instance's admin limits them.
- **F-71** The admin can limit, per user, the **memory** and the **CPU** their running apps may use together, and how many **builds** they run at once (F-56a). A new user has no limits. A service that would pass a limit isn't started: a release fails with `memory limit reached: your apps use 700 of 1024 MB and api needs 512 MB more. Ask the instance admin to raise your limit, or stop something`, and the live version keeps serving; an app that wants to scale up beyond it stays at the size it has. Paused instances count (they keep their memory); the release command (migrations) doesn't. A deploy briefly runs the old and the new version side by side, so a limit needs some room above the apps' normal use.
- **F-72** A user sees their use and their limits on the Usage page and, when a memory limit is set, in the sidebar (`GET /v1/me/usage`).

### 4.9 CLI
Commands (v1): `login`, `logout`, `whoami`, `init`, `dev`, `deploy`, `ps`, `logs`, `env`, `domains`, `rollback`, `redeploy`, `open`, `pprof`, `projects`, `teams`, `tokens`.
- **`grey dev`** builds and runs the project **on the developer's machine**, the same way it would run on Greyship:
  - detects services exactly like a deploy, builds each one and runs web services and workers, each on its own local port (printed at start);
  - loads `.env`, then `.env.local` on top (`--prod` loads `.env.prod` instead);
  - injects `PORT` and the same `GREYSHIP_*` variables a deploy would;
  - makes `<service>.internal` resolve to the matching local service, so code that calls `http://payments.internal` works unchanged;
  - runs crons on demand (`grey dev run cleanup`) and the release command with `grey dev release`;
  - rebuilds and restarts a service when its files change.
- **`grey deploy`** ships it: uploads source, syncs `.env.prod` (F-40a), builds, runs the release step and switches traffic.
- **`grey github`**: `install` prints (and opens) the page that installs the Greyship GitHub app for the team, `repos` lists the repositories it can read, `connect OWNER/REPO [--branch]` makes pushes to that branch deploy this folder's project, `import URL [--branch]` deploys a public repository from its link (and links this folder to the new project), and `deploy` deploys the latest commit of this project's connected branch. Both follow the deploy log like `grey deploy`.
- Binary name `grey`. The installer also creates `gray` (same binary) and `greyship` (full name, for scripts), so every spelling works.
- Single static Go binary; install via `curl | sh`, Homebrew, `go install`, Scoop. *As built (decisions.md 047, 050): `curl -fsSL https://greyship.tech/cli/install | sh` (or the same `install.sh` from `raw.githubusercontent.com/greylabss/greyship/main/deploy/cli`) for macOS and Linux, amd64 and arm64; Windows through WSL. The installer checks the sha256 against the release's `checksums.txt`, installs into `/usr/local/bin` or `~/.local/bin` without sudo, and running it again updates. Homebrew, Scoop and `go install` come later.*
- `--json` output on every command for scripting.
- Works in CI with `GREYSHIP_TOKEN`.

### 4.9a Operator admin
- **F-56** The instance owner (the first account, or the one `GREYSHIP_OWNER_EMAIL` names; decisions.md 048, 050) has a read-only admin view at `/admin`, signed in to the dashboard (API tokens never count). It finds any deployment by its id or the start of it, or by project or team slug, and shows everything needed to debug it: status and error, team, project and who started it, source and build details (runner, machine, time, commit, repo, source and files objects), binaries, instances (node, state, error), the related jobs (attempts, last error), the full build and release log, and the instances' recent output. It also shows totals, the latest failed deployments, nodes and their heartbeats, the job queue and failed jobs, and every user with their teams. Everyone else gets 404. It changes nothing.
- **F-56a** An admin manages the instance's users in the dashboard (`/admin/users`, CLI parity exception like F-56: the API is the same). **Add user**: email, optional name, a password (10 to 128 characters, with a generate button) and an Admin checkbox; the new account gets a personal team and signs in with the password. Per user: **Limits** (memory in MB, CPU in cores, builds at once; empty means no limit; F-71), **Reset password** (a new random password, shown once) and **Make admin / Remove admin**; the last admin can't be removed. The list shows each user's role and what their apps use now next to their limits. Anyone signed in can change their own password in Settings (the current one is needed). Wrong passwords are limited to 8 per email and 30 per IP in 15 minutes.
- **F-56b** Email is optional and only sends deploy notifications (success, failure, new project). It is turned on in the dashboard (`/admin/email`, admin only): a Resend API key, the sending domain (verified in Resend), a sender name and an on/off switch, plus **Send test email** (to the admin, showing the provider's error if it fails). The key is stored encrypted and never shown again; leaving it empty when saving keeps it. Mail is sent from `notifications@<domain>`. Settings saved here replace `GREYSHIP_RESEND_API_KEY`, which still works when nothing is saved. With email off, nothing is mailed; the bell and browser push still work. Signing in never needs email.

### 4.9b Install counts and newsletter (decisions.md 060)
- **F-75** The installer counts installs anonymously: started, succeeded, failed (with the step) and upgraded, with a random install id, version, OS and CPU type. No email, hostname or IP address is kept. It says so at the start of every run; `GREYSHIP_NO_TELEMETRY=1` turns it off. A failed or slow ping never affects the install.
- **F-76** After the owner email, the installer asks whether the person wants Greyship news by email (`--newsletter`/`--no-newsletter`). Only a yes sends the address, and nothing is sent to it until the person confirms from the email (double opt-in). Every newsletter has a one-click unsubscribe. greyship.tech/privacy describes both.

### 4.10 Dashboard
Pages: Overview (projects), Project (services, latest deploy, URLs), Deployments (list, logs, rollback), Logs, Metrics, Go Runtime, Env Vars, Domains, Settings (rename, delete), Usage (what your apps use next to your limits), account Settings (password, notifications, your GitHub App, tokens), and for the admin Admin (overview, users and limits, email).
- First-run flow: sign in → create your GitHub App (once) and install it on a repo → detected services shown → Deploy. Target: live in under 2 minutes without touching a terminal.
- As of step 17 the dashboard shows real data for projects, services (with live state and URLs), deployments and build logs, app logs, env var keys and API tokens, and it approves `grey login`. Env vars can be added, changed, removed, or pasted as a whole `.env` file, parsed with exactly the rules `grey deploy` uses for `.env.prod`; values are never sent back to the browser. Screens the API can't answer yet (metrics, Go runtime) say that they are not built instead of showing sample numbers. Redeploy and rollback work from the dashboard (F-27), and tokens can be revoked there (after a confirm). The dashboard never shows a button that doesn't work.

### 4.11 Public API
- REST, JSON, versioned `/v1`. Everything the CLI and dashboard do goes through it.
- Auth: the dashboard uses a session cookie; the CLI and other clients use bearer tokens. Rate limit 100 req/min per token.
- A token only works within the team it was created for (F-3); using it on another team's resources is refused with `wrong_team`.
- Projects: `GET|POST /v1/teams/{team}/projects`, `GET|PATCH /v1/projects/{id}` (rename). Creating a name that already exists in the team returns `409 slug_taken` with that project.
- Documented with OpenAPI; CLI generated client.

### 4.12 Go-native performance
Speed that only a Go-only platform can offer. Everything here is automatic; users change nothing.
- **F-80** Pause-first: idle Go apps are small (roughly 10–30MB), so idle instances are kept paused in memory rather than stopped. Most "cold" requests become a resume that takes milliseconds.
- **F-81** TLS head start: the router starts resuming or starting an instance as soon as the TLS handshake names the domain (SNI), before the HTTP request arrives.
- **F-82** Automatic runtime tuning: `GOMAXPROCS` is set to the instance's CPU quota and `GOMEMLIMIT` to ~90% of its memory limit, unless the user sets them. Prevents CPU throttling and surprise out-of-memory kills.
- **F-83** Binary-only images: an instance's filesystem is just the static binary plus guest init — no OS image, no layers — plus the project's runtime files when `greyship.toml` lists them (F-13a). Two read-only files are mounted in so apps can reach the internet: `/etc/resolv.conf` (public resolvers) and `/etc/ssl/certs/ca-certificates.crt` (the node's CA bundle, so TLS to databases and APIs verifies). Active binaries are kept in each node's page cache.
- **F-84** Fast rebuilds: shared Go module cache and per-project build cache; only changed packages recompile.
- **F-85** *(v1.0)* Automatic PGO: production CPU profiles are merged into a `default.pgo` and used for the next build, so each redeploy gets faster with no user action. On for every plan; opt out with `pgo = false` in `greyship.toml`.
- **F-86** *(v1.0)* Startup insights: the dashboard shows how long an app takes to become ready and what it spends that time on (e.g. slow `init()`, connecting to external services).

### 4.13 Microservices & internal networking
For teams running an API gateway in front of private services.
- **F-90** Visibility: each web service is `public` (default) or `private`. Private services have no public URL and can't be reached from the internet.
- **F-91** Internal names: services in the same project reach each other at `http://<service>.internal` (any port they listen on via `$PORT`). Services in other projects or teams can never reach them; cross-project calls use public URLs. The name is the service name, which is the folder name by default (`cmd/payments` → `payments.internal`). Hard-coding `http://payments.internal` in code is safe — it only resolves inside the project, and `grey dev` resolves it locally too. Putting it in an env var instead is equally supported; it's the developer's choice.
- **F-92** Private services scale to zero too: an internal call wakes a sleeping service exactly like a public request, with the same holding and concurrency rules (F-31–F-33).
- **F-93** gRPC and Connect: services can set `protocol = "h2c"`; HTTP/2 works end to end, internally and at the edge.
- **F-94** Path routing: one domain can route path prefixes to different services (e.g. `/payments` → `payments`), optionally stripping the prefix. Longest prefix wins.
- **F-95** *(v1.0)* Multi-repo projects: a project can link several repos; their services share one private network, env scopes and domains.
- **F-96** Service map: the dashboard shows public and private services and which services call which, based on internal traffic.
- **F-97** Internal requests are metered like public ones; traffic between services in the same project is never charged as egress.

---

## 5. Non-functional requirements

| Area | Target (v1) | Target (after Firecracker) |
| --- | --- | --- |
| Cold start (stopped → first byte) | p50 < 800ms | p50 < 100ms |
| Wake from pause | p50 < 50ms | p50 < 20ms |
| Warm request overhead (router) | < 5ms | < 2ms |
| Deploy time (small API, cached) | < 60s | < 30s |
| Availability | One server: as good as the server (no high availability) | Two or more nodes |
| Data durability (Postgres) | `greyship-admin backup` before every upgrade and on demand; copy it off the server | Scheduled off-site backups, PITR |

- **Security:** every app in a sandbox (gVisor now, Firecracker later); no tenant can reach another tenant or internal services; secrets encrypted with envelope encryption.
- **Abuse:** per-user limits set by the admin (F-71); outbound port 25 blocked *(not built)*; the admin decides who gets an account, so open signup abuse doesn't exist.
- **Privacy:** we never read app memory or request bodies; logs belong to the people who deploy and are deleted with the project (F-10c).

---

## 6. Limits

Greyship has no plans. What every deploy is subject to, on any instance:

| | |
| --- | --- |
| Source upload per deploy (compressed) | 100MB |
| Memory per instance | set by the service (`memory` in `greyship.toml`), up to the node's memory |
| Max instances per service | set by the service (`max_instances`) |
| Build time (server builds) | `GREYSHIP_BUILD_TIMEOUT`, 10 minutes by default |
| Concurrent builds | one at a time per instance, the rest queue; each build may use every CPU core (decisions.md 058); per user also whatever the admin allows (F-71) |

On top of that the instance's admin may limit each user's memory, CPU and concurrent builds (F-71).

---

## 7. Pricing

None. Greyship is open source (AGPL-3.0) and runs on your own server (decisions.md 050). Builds on your own machine with `grey deploy` and on the server are never metered. A paid hosted version may come later and would be a separate product.

---

## 8. Success metrics

| Stage | Metric | Target |
| --- | --- | --- |
| Install | Time from `curl … | sh` on a fresh server to the owner's first live app | < 15 min median |
| Install | Installs that finish without the owner asking for help | > 90% |
| Use | Deploys that succeed on the first try | > 80% |
| Use | Time from first sign-in to a live URL | < 5 min median |
| Adoption | Public instances running, GitHub stars, contributors | tracked, no target yet |
| Always | Cold start p50 | within NFR target |

---

## 9. Release scope

| Release | Contents |
| --- | --- |
| **v0.1 (shipped)** | One-command installer (no domain needed; `greyship-admin domain` adds HTTPS), email + password sign-in with the owner creating users, per-user limits, admin view, dashboard and CLI, GitHub deploys, `cmd/*` detection, release command (migrations), rollback and redeploy, env vars, logs, notifications, backup, restore and upgrades that keep everything, public repo and releases |
| **v0.1.x (shipped)** | Every team creates its own GitHub App from the dashboard, project settings and deleting projects, custom domains, `make release-patch|minor|major`, install URLs |
| **Next** | Workers that wake on events and crons that run on schedule, built-in queue, metrics and Go runtime metrics, private services and `.internal` names, SMTP email, preview deploys, a release workflow in GitHub Actions |
| **Later** | Teams that share projects (and limits per team), second node, Firecracker runtime, pprof and alerts, automatic PGO, startup insights, multi-repo projects, OpenTelemetry |

---

## 9a. Roadmap: long-term edge

Greyship stays the best place for Go by going deeper into Go than any general platform will. Each item below is a direction, not yet a requirement; when one is scheduled it gets F-numbers in §4.

| When | Item | What it means |
| --- | --- | --- |
| Next | **Vulnerability checks on deploy** | Run `govulncheck` on every build; warn (or block, per project setting) when the code actually calls a vulnerable function, with the fixed version. |
| Next | **Go-aware crash reports** | On panic or out-of-memory kill, capture the panic trace / goroutine dump and show it grouped and readable in the dashboard. |
| Done | **Open source** | The whole platform, CLI and installer are AGPL-3.0 at github.com/greylabss/greyship. |
| Later | **MCP server for coding agents** | Agents can deploy, read logs, inspect metrics and roll back through MCP; CLI `--json` stays complete. |
| Later | **Framework starters + Deploy button** | Official starters for net/http, Chi, Gin, Echo, Fiber; a "Deploy to Greyship" button for READMEs. |
| Later | **Background jobs package** | `greyship.dev/jobs`: a typed Go API over the built-in queue (F-35a) with retries and scheduling. Jobs wake workers; they never keep them running. |
| Later | **Go version upgrade checks** | When a new Go release ships, test-build each app on it and report whether it's safe to upgrade and any speed change. |
| Later (v1.x) | **API docs** | Deploy detects an OpenAPI file (2.0 / `swagger.json` or 3.x; from the repo, or fetched once from the app's `/openapi.json` during the health check) and renders it **privately** in a dashboard "API" tab. If the app already serves its own Swagger UI, the tab links to it instead. Optional public docs URL, opt-in only. `grey docs` scans code locally (net/http, chi, gin, echo routes and the structs handlers decode/encode) and writes a standard `openapi.yaml` for the dev to review and commit — never a custom format. |

Guardrail: Greyship stays Go-only. Requests to support other languages are declined.

---

## 10. Open questions
1. ~~Final name.~~ Decided: **Greyship** (see decisions.md 007).
2. ~~Open-source the CLI and guest agent?~~ Decided and done: the whole platform is open source (decisions.md 050).
3. Should paused instances count against a user's memory limit? Today they do, because they keep their memory (F-71).
4. Do teams that share projects need limits per team rather than per user? (Today every user has one team.)
5. Should the release be built and published by a workflow in the public repository instead of a laptop?
