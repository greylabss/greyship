# How Greyship works: a guided tour of the backend

This is a reading guide to the whole system, for the founder. Part 1 is the **high level**: what the pieces are and what happens when someone signs in, deploys, or visits an app. Part 2 is the **low level**: the same journeys again, but through the actual code, explained in English, with the file to open if you want to look.

It describes the system as built on 2026-11-04. The formal documents are `spec.md` (what the product promises) and `architecture.md` (the technical design). `decisions.md` explains why things are the way they are. When this guide and those disagree, those win, and this guide needs fixing.

---

# Part 1 — High level

## 1.1 The pieces

Greyship is a handful of Go programs that talk to each other through one Postgres database. Here is where each one lives:

```
 Your Mac / a user's laptop          Vercel                       Fly.io
 ┌─────────────────────┐            ┌───────────────────┐        ┌─────────────────────────┐
 │ grey (the CLI)      │            │ greyship.tech     │        │ build machines          │
 └─────────┬───────────┘            │ site + dashboard  │        │ (greyship-buildd), only │
           │ HTTPS                  │ (Next.js)         │        │ running during a build  │
           │                        └────────┬──────────┘        └──────────┬──────────────┘
           │                                 │ /v1/* forwarded              │ report logs,
           ▼                                 ▼                              │ ask for upload URLs
 ┌───────────────────────────── the VPS (Contabo, Ubuntu) ──────────────────▼──────────────┐
 │  greyship-router  (the only public door: :80/:443, HTTPS certificates)                   │
 │     │ api.greyship.tech                       │ <app>.greyshipapp.tech                   │
 │     ▼                                         ▼                                          │
 │  greyship-api ──────┐                 users' apps (each in its own gVisor sandbox)       │
 │  greyship-scheduler ┤                         ▲                                          │
 │  greyship-builder ──┼── Postgres (all state)  │ starts / pauses / stops                  │
 │  greyship-agent ────┘                         └── greyship-agent                         │
 └─────────────────────────────────────────────────────────────────────────────────────────┘
                                   │ files (code, binaries, backups)
                                   ▼
                         Cloudflare R2 (object storage)
                         GitHub (repos, webhooks, commit statuses)
```

| Program | Job, in one sentence |
| --- | --- |
| `grey` | The command-line tool developers use: log in, deploy, see logs, set env vars. |
| Dashboard (`web/`) | The website at greyship.tech: the marketing pages, and the dashboard once you sign in. It never touches the database; it calls the API like the CLI does. |
| `greyship-api` | The front office. Every action (sign in, create a project, deploy, read logs) is an HTTP endpoint under `/v1`. It checks who you are and whether you're allowed. |
| `greyship-builder` | Turns code into binaries, or checks binaries someone built themselves. It runs the builds on Fly machines and records the results. |
| `greyship-scheduler` | Decides what runs where. It releases new versions, runs migrations first, wakes sleeping services, and cleans up. |
| `greyship-agent` | The hands on the server: it actually starts, pauses, resumes and stops app sandboxes, and records what apps print. |
| `greyship-router` | The receptionist: every visitor arrives here. It finds the right app, wakes it if it's asleep, and hands it the request. It also serves `api.greyship.tech` and gets HTTPS certificates. |
| `greyship-init` | A tiny program that runs first inside every app sandbox. It receives the app's settings and secrets and starts the app. |
| `greyship-buildd` | The program on a Fly build machine: download code, compile, upload, report, exit. |
| `greyship-backup` | Every night, copies the database to R2. |
| Postgres | The single source of truth: users, teams, projects, deployments, instances, encrypted env vars, logs, and the job queue. |
| R2 | Big files: uploaded source code, built binaries, nightly backups. |

**The most important idea: they talk through the database.** When the API wants a build, it doesn't call the builder. It writes a row in the `jobs` table saying "build deployment X". The builder is listening, picks the job up, and does it. When the build finishes, the builder writes "release deployment X" for the scheduler. This makes the system robust: if a program restarts, the work is still sitting in the database waiting.

## 1.2 Signing in

There are no passwords. You prove you own an email address, or a Google or GitHub account.

**On the website:**
1. You type your email. The API sends a 6-digit code (through Resend).
2. You type the code back. The API checks it. If you're new, it creates your account and a personal team, provided your email is on the beta invite list.
3. The API gives your browser a **session cookie**: a long random secret. Only a fingerprint (hash) of it is stored in the database, so even someone reading the database can't use it.

**Email:** the admin pastes a Resend key and domain in Admin > Email. The key is encrypted with the instance's master key before it is saved. Every email (login codes, notifications) asks `mail.Dynamic` which provider to use at that moment, so changes apply at once and, with email off, codes land in a folder on the server instead.

**With a password:** an admin can create an account with a password for someone. They type email and password; the API checks a salted PBKDF2 hash (never the password itself), counts wrong tries (8 per email, 15 minutes) and then gives the same session cookie.

**In the terminal (`grey login`):**
1. The CLI asks the API for a "device code" and opens `greyship.tech/device` in your browser, showing a short code like `KRWT-PLMB`.
2. You check the code matches what your terminal shows and click Approve, signed in on the website.
3. The CLI has been asking "approved yet?" every 5 seconds. Now the answer is yes, with an **API token** (`gs_…`). The CLI saves it. Again, only its hash is stored on our side.

**Every later request** carries either the cookie (browser) or the token (CLI). The API looks up who that is and checks that the thing you're touching belongs to a team you're in. Someone outside the team gets "not found", so team and project names can't even be guessed.

**Protection against websites tricking your browser:**
- Users' apps live on a different domain (`greyshipapp.tech`) from Greyship (`greyship.tech`), so a malicious app is a stranger to the dashboard.
- Any change made with a browser cookie must come from the dashboard's own address.

## 1.3 Deploying with `grey deploy`

There are three ways code becomes a running app. They meet in the middle.

```
 A. grey deploy, Go installed     B. grey deploy, no Go / --remote     C. git push to a connected repo
    build on the laptop              upload the source                    GitHub tells Greyship
          │                                 │                                    │
          ▼                                 ▼                                    ▼
    upload binaries                  builder: build on a Fly machine      builder: download the commit,
    builder: check them              (dedicated CPUs, team cache)         build on a Fly machine
          │                                 │                                    │
          └──────────────────► binaries stored, deployment "built" ◄────────────┘
                                            │
                                            ▼
                     scheduler: run the migration command (if any),
                     start the new version, wait until it answers, switch traffic,
                     stop the old version
                                            │
                                            ▼
                                     ✓ Live at https://<service>-<project>.greyshipapp.tech
```

**What `grey deploy` does on your laptop, in order:**
1. **Find the project.** It finds `go.mod`, and reads `.greyship/project.json` to know which project this folder is. The first time, it creates the project and writes that file.
2. **Find the services.** It reads your code, without running it, and lists every `package main`: `cmd/api` becomes a service called `api`, `cmd/worker` becomes `worker`, and `cmd/migrate` is the release command. It works out web vs worker by looking for a server starting (`http.ListenAndServe`, gin's `Run`, and so on). This is "detection".
3. **Sync env vars.** If there's a `.env.prod`, it shows which keys would change and asks you before sending them. Values are never printed.
4. **Build:**
   - **With Go installed:** it compiles each service right there, for Linux on the server's CPU, then hashes and compresses the binaries. It asks the API "which of these do you not have yet?" and uploads only those, straight to R2, through one-time upload links.
   - **Without Go, or with `--remote`:** it packs your folder into a `.tar.gz`, leaving out `.git`, `.env` files and whatever `.gitignore` says, and uploads that instead.
5. **Start and watch.** It tells the API "uploaded, go". Then it streams the deployment's log live until it says ✓ Live (with your URLs) or ✗ with the reason.

## 1.4 How we use Fly.io

**Fly.io only builds code. It never runs users' apps.** Apps run on our own server.

Why: compiling Go needs a lot of CPU for a minute or two, and doing that on the same server as users' apps slowed both down. A fresh gin project took 118 seconds. Fly rents out machines with **dedicated CPUs by the second**, and a stopped machine costs nothing but its disk.

**What happens in one server build:**
1. The builder (on our server) reads the team's plan. Free gets a 2-CPU machine, Pro 4, Team 8.
2. **Paying teams** have their own Fly machine with its own disk (a "volume") holding their Go caches, so their second build is fast. That machine is only ever used for that team, so no team can tamper with another's cache. **Free teams** get a fresh machine that is destroyed after the build.
3. The builder starts the machine and hands it only what it needs: a link to download the code, a one-time pass (a token valid for this one build), and the API's address. It never sends secrets, env vars or storage keys.
4. On the machine, `greyship-buildd`:
   1. downloads the code;
   2. compiles each service (no cgo, for Linux, with the toolchain pinned);
   3. asks the API for an upload link for each binary, now that it knows its size, and uploads it to R2;
   4. sends its log lines and the result to the API;
   5. wipes its workspace and exits.

   Exiting stops the machine, so billing stops.
5. The builder has been relaying those log lines to you all along. When the result arrives, it checks the binaries really are in R2 with the right sizes, records them, and notes the build time for the monthly build-minute count (Free: 100 minutes).

The images Fly boots are built by `make build-images`: one per Go version (1.24–1.27), each holding the official Go toolchain plus `greyship-buildd`. They're pushed to Fly's registry under the app `greyship-build`.

## 1.5 Deploying from GitHub

1. **Connect once.** In the dashboard, "Import from GitHub" sends you to GitHub to install the **Greyship GitHub app** and tick which repositories it may read. GitHub sends you back. We double-check, with your own GitHub login, that you really have access to that installation, and remember it for your team.
2. **Import, review, deploy.** You pick a repo. Greyship creates a project named after it and connects its default branch, but doesn't deploy yet. The review page downloads the latest commit and shows what it found: every service, whether it's web, worker or cron and why, and the release command. You add env vars (names from `.env.example` are suggested) and click **Deploy**.
3. **Every push** to that branch makes GitHub call our webhook (`api.greyship.tech/v1/github/webhook`). We check GitHub's signature, so nobody can fake a push. We create a deployment for that commit; pushing the same commit twice does nothing.
4. **The builder downloads that exact commit** as an archive, removes the same things `grey deploy` would (`.env` files, `.git`, ignored files) and builds it on Fly, as in 1.4.
5. **GitHub shows the result:** a "Greyship" status on the commit, with a link to the deploy. It's yellow while deploying, then ✓ or ✗.
6. **Or paste a public link** ("Deploy a public repository" on New project, or `grey github import <url>`), with no app needed. It deploys at once; afterwards, **Deploy latest** (or `grey github deploy`) deploys the newest commit. There are no automatic deploys or ✓ for these, because only the app hears about pushes. Deploy latest also works for app-connected projects, for example to retry after adding env vars.

## 1.6 Running apps: sandboxes, sleeping and waking

- **Every app instance is a gVisor sandbox:** a container with an extra layer that intercepts every system call, so code that tries to escape hits Greyship's wall, not the server's real kernel. Inside there is only `greyship-init`, the app's binary, a list of DNS servers and a list of trusted HTTPS certificate authorities. Nothing else: no shell, no OS. It runs as a nobody user, on a read-only disk, on its own private network, unable to reach the server or other apps, with capped memory and CPU.
- **Env vars and secrets** are stored encrypted. Each project has its own key, and that key is itself locked by the master key in `/etc/greyship/greyship.env`. They're only decrypted by the agent at the moment an instance starts, and handed to the app through `greyship-init` over a pipe. They're never written to disk.
- **Sleeping:** when a web service has had no requests for a while (its "grace" time), the router asks the agent to **pause** it. It's frozen, costs nothing, and keeps its memory. If it stays paused long enough, the scheduler stops it entirely.
- **Waking:** a request arrives for a sleeping service. The router **holds** the request (up to 30 seconds) and asks for a wake-up. A paused app is resumed in milliseconds; a stopped one is started fresh, in about a second. Then the request goes through. The visitor just sees a slightly slower first response.
- **Busy:** if every instance is at its concurrency limit, the router asks for one more, up to the service's maximum.

## 1.7 Visiting an app

1. A visitor opens `https://api-temp.greyshipapp.tech`. DNS points every `*.greyshipapp.tech` name at the VPS.
2. **The router** answers. The first time it sees a hostname, it gets an HTTPS certificate from Let's Encrypt, but only for names that belong to a real app, so nobody can make us request certificates for anything else.
3. It looks the name up in its **routing table**, kept in memory and refreshed the moment anything changes, and forwards the request to one of that service's instances on the server's private loopback address.
4. The app's response goes back to the visitor. The app's own output is collected by the agent into the logs you see with `grey logs` or in the dashboard.

## 1.8 Storage, backups and the server

- **The VPS holds only what can be lost or rebuilt:** Postgres's data (backed up nightly to R2), caches, and the configuration in `/etc/greyship`, which you back up yourself.
- **R2 holds:** sources (`sources/<project>/<deployment>.tar.gz`), binaries (`artifacts/<deployment>/<service>`, and `binaries/<project>/<sha>.gz` for laptop builds), and `backups/postgres/<date>.dump`.
- **Nobody gets permanent storage keys except our own services.** The CLI, build machines and agents get links that expire.
- Services are managed by systemd and restart on their own. `deploy/vps/release.sh grey` ships new code. `docs/server.md` is the server's manual.

---

# Part 2 — Low level: the code, explained

How to read this part: each section follows one journey through the code. File names are links you can open in the editor. The repository has one rule that helps a lot: **`cmd/<program>/main.go` only wires things together; the real logic is in `internal/<area>/`.**

## 2.1 How the programs start (`cmd/`)

Every `main.go` does the same four things:
1. Read settings from `GREYSHIP_*` environment variables, through [`internal/config`](../internal/config/config.go). In production these come from `/etc/greyship/greyship.env`, loaded by systemd. Missing required settings stop the program at start with a clear message.
2. Connect to Postgres (a connection pool, `pgxpool`).
3. Build the objects that do the work, handing each one what it needs: "dependency injection by hand", no global variables.
4. Run until told to stop, then shut down cleanly.

## 2.2 The database layer (`migrations/`, `internal/store`)

- **`migrations/00001…00017_*.sql`** describe the database tables, in order. `greyship-migrate` applies the new ones at each release. A migration that has run on the server is never edited; a change is always a new file.
- **`internal/store/queries/*.sql`** hold every SQL query, written by hand. The `sqlc` tool turns them into typed Go functions in `internal/store/*.sql.go` (never edited by hand). So `store.New(pool).GetDeployment(ctx, id)` runs exactly the SQL named `GetDeployment` in [`deployments.sql`](../internal/store/queries/deployments.sql).
- The big tables:
  - `users`, `teams`, `memberships`: who's who;
  - `sessions`, `tokens`, `device_codes`, `login_codes`: signing in;
  - `projects`, `services`: what you deploy;
  - `deployments`, `artifacts`, `build_logs`: each version and its binaries and build log;
  - `releases`: which deployment is live;
  - `instances`, `nodes`: what's running where;
  - `env_vars`, `project_keys`: encrypted settings;
  - `app_logs`;
  - `jobs`: the work queue;
  - `build_machines`, `build_reports`: Fly;
  - `github_installations`, `project_repos`: GitHub.

## 2.3 The job queue (`internal/bus`)

This is how the programs hand work to each other.

- **Adding a job** ([`queue.go`](../internal/bus/queue.go)): `bus.Enqueue` inserts a row into `jobs` with a kind (`build.requested`, `deployment.release`, …) and a JSON payload, inside the same database transaction as the change that caused it. Then Postgres's `NOTIFY` wakes the listening workers. If the transaction fails, the job never exists, so there's no "deployment marked queued but no build job".
- **Groups and plans:** `EnqueueGrouped` puts a job in a group, the team, with a limit and a priority from the plan. A team on Free runs 1 build at a time, Pro 2, and higher priority is picked first.
- **Taking a job** (`Claim`): a worker runs one SQL statement that picks the oldest eligible job, marks it `running` and gives it a **lease** (a deadline). Workers renew the lease while they work. If a worker dies, the lease runs out and another worker takes the job. So every handler is written to be safe if it runs twice.
- **Finishing:** `Complete`, or on error `Fail`, which schedules a retry with a growing delay, and after 5 attempts gives up and marks the job failed.
- **The worker loop** ([`worker.go`](../internal/bus/worker.go)): hold one database connection to *listen* for new jobs; when woken, or every few seconds anyway, claim and handle. *Lesson learned:* each worker needs two connections (one to listen, one to work), so the builder sizes its pool to its number of workers.

## 2.4 The API server (`internal/api`)

[`server.go`](../internal/api/server.go) builds the router with Go's standard `net/http` patterns: `"POST /v1/projects/{id}/deployments"` maps to a handler function. Every request passes through `ServeHTTP`, which:
1. catches panics and logs the request (method, path, status, time; never bodies or secrets);
2. answers CORS for the dashboard's address;
3. runs **`sameOrigin`**: a change (POST, PATCH, DELETE) that comes with a browser session cookie must carry `Origin: https://greyship.tech`, or it's refused. CLI requests use a bearer token, which a malicious website can't attach, so they're exempt.

**`requireUser`** ([`auth.go`](../internal/api/auth.go)) wraps every endpoint that needs a signed-in person:
- An `Authorization: Bearer gs_…` header goes to `Auth.Authenticate`: hash the token, look up the hash.
- Otherwise it reads the `__Host-gs_session` cookie: `Auth.SessionUser`.

It puts the user, and the token's team, into the request context for the handler.

**Handlers are thin.** They decode JSON, call a service in another package (`project`, `deploy`, `env`, `github`, …), and turn its errors into HTTP answers. For example, `project.ErrNotFound` becomes 404, `ErrReadOnly` becomes 403 for viewers, and `deploy.ErrBadBinary` becomes 400.

## 2.5 Signing in, in code (`internal/auth`)

- **Email code** ([`service.go`](../internal/auth/service.go)):
  - `StartEmail` limits how often codes can be requested per email and per IP, stores a *hash* of a random 6-digit code, and queues the email.
  - `VerifyEmail`, in one transaction: finds the latest unexpired code, counts the attempt (5 tries max), compares in constant time, marks it used, then `findOrCreateUser`, which also creates a personal team for new users if the **signup allowlist** permits, then `createSession`.
- **OAuth** ([`oauth.go`](../internal/auth/oauth.go)): the standard "code + PKCE" dance with Google or GitHub. An existing linked account signs in. A new one is linked by email only if the provider says the email is verified.
- **Device flow** ([`device.go`](../internal/api/device.go) and the auth service):
  - `StartDevice` makes a `device_code` (secret, for the CLI) and a `user_code` (short, for you).
  - Approving in the dashboard ties them to your team.
  - `pollDevice` hands the CLI a fresh `gs_` token exactly once.
- **Client IP for rate limits** (`clientIP` in `server.go`): the API only ever sees the router connecting from the server itself, so it trusts the address the router wrote in `X-Forwarded-For`. When the dashboard forwards a browser's request, the dashboard adds the real visitor address with a shared secret, `GREYSHIP_PROXY_SECRET`.

- **Who may sign up** ([`users.go`](../internal/auth/users.go) `findOrCreateUser`, `Signup.check` in [`service.go`](../internal/auth/service.go)): during the beta, the API asks the beta app whether the email applied ([`internal/beta`](../internal/beta/beta.go) → `beta/app/api/testers/check`). Testers get in and get `beta_tester`, which the dashboard shows as a checkmark; allowlisted emails get in too.

## 2.6 `grey deploy`, in code (`internal/cli`, `internal/deploy`)

The CLI is in [`internal/cli`](../internal/cli); `cmd/greyship/main.go` just runs it. People install it with `curl -fsSL https://greyship.tech/install | sh`: the script ([`web/public/install`](../web/public/install)) downloads the right build from the public repo `greylabss/greyship`, checks its sha256 and installs it. [`deploy/open-source/release.sh`](../deploy/open-source/release.sh) `vX.Y.Z` builds and publishes a release there: server, dashboard and CLI together (decisions.md 050).
1. **[`deploy.go`](../internal/cli/deploy.go) `deploy()`**:
   - finds the module root and `ensureProject` (reads or creates the link file);
   - runs **`detect.Detect`** ([`internal/detect`](../internal/detect)) on the folder. Detection parses Go files with the standard `go/parser`, walks from each `main()` through the module's own packages, and recognises known server starts by import path. It never runs code. [`reads.go`](../internal/detect/reads.go) also looks for repo paths the code opens at runtime (`os.ReadFile("scripts/install.sh")`) that `greyship.toml`'s `files` doesn't ship, and turns them into warnings.
2. **If `go` is on PATH** → [`localbuild.go`](../internal/cli/localbuild.go) `deployLocal()`:
   1. Asks `GET /v1/build-target` for the server's CPU. If `greyship.toml` lists `files`, packs them first with `pack.BundleDir` ([`files.go`](../internal/pack/files.go)), so a typo fails before anything compiles.
   2. Runs `go build` per service with `CGO_ENABLED=0 GOOS=linux GOARCH=… GOFLAGS=-trimpath`, as a separate command with separate arguments, never a shell string. `go vet` runs alongside; its warnings are printed, never fatal.
   3. Hashes (sha256) and gzips each binary.
   4. Sends `POST /v1/projects/{id}/deployments` with the list of binaries (and the files bundle's sha256 and size).
   5. On the server, [`deploy/local.go`](../internal/deploy/local.go) `CreateLocal`:
      - validates names, paths, types and sizes;
      - records each binary under a key made from the project and the binary's own hash (`binaries/<project>/<sha>.gz`);
      - returns an upload link only for keys not already in R2, so unchanged services aren't uploaded again. The files bundle works the same way, at `files/<project>/<sha>.tar.gz`.
   6. Uploads those with HTTP PUT, then calls `POST /v1/deployments/{id}/start`. `Start` checks every upload is really there with the right size, then queues a **`build.verify`** job.
3. **Otherwise**, `pack.Write` ([`internal/pack`](../internal/pack/pack.go)) makes the tarball, honouring ignore files and always leaving out `.git`, `.env*` and symlinks, with the same bytes for the same files. `deploy.Create` records it and returns one upload link. `Start` queues **`build.requested`**.
4. Both end in **`followLog`**: `GET /v1/deployments/{id}/logs?follow=1` streams newline-separated JSON lines, plus a final `end` record with the status, URLs and build minutes, which `printBuildEnd` shows.

## 2.7 The builder (`internal/build`, `internal/build/fly`, `internal/buildd`)

`cmd/greyship-builder/main.go` starts workers for three job kinds: `build.requested`, `build.verify` and `github.fetch`. For a build on Greyship, `bundleFiles` in [`builder.go`](../internal/build/builder.go) packs `greyship.toml`'s `files` from the uploaded source right after detection and stores it next to the binaries. With Fly it also starts the cache-volume cleanup and the GitHub status sweep.

**Checking a laptop build** ([`verify.go`](../internal/build/verify.go) `Verify`): a binary the project already has (same sha256 and CPU) was checked before, so the new deployment just points at it ("unchanged api … already checked"). For each new binary, it downloads, decompresses (with a size cap), checks the sha256, and reads the build information Go embeds in every binary (`debug/buildinfo`). It must say Linux, the right CPU, cgo off and `-trimpath`; a binary built for a Mac is refused with a clear message. It stores the binary as an artifact, then `finish` marks the deployment `built` and enqueues `deployment.release` for the scheduler. This check catches mistakes, not attackers; the sandbox is the real protection.

**Building on the server** ([`builder.go`](../internal/build/builder.go) `Handle` → `build`):
1. `StartBuild` moves the deployment from `queued` to `building` (or does nothing if it's already done: safe to repeat).
2. Downloads the tarball and checks its sha256 against what the CLI declared.
3. Runs detection again on the server's copy. This one counts, because the laptop's output can't be trusted.
4. `checkMinutes`: refuses if a Free team has used its 100 build minutes this month.
5. Calls **`b.Runner.Run(job)`**. `Runner` is an interface ([`runner.go`](../internal/build/runner.go)) with two implementations; the builder doesn't care which:
   - **`LocalRunner`** wraps the gVisor `Sandbox` ([`sandbox.go`](../internal/build/sandbox.go)), used on a laptop and in tests. It runs `docker run --runtime=runsc` with no network except our module proxy, a read-only filesystem, and capped CPU and memory.
   - **`fly.Runner`** ([`fly/runner.go`](../internal/build/fly/runner.go)), in production:
     - `lease` picks the team's stopped machine with its cache volume (`PickBuildMachine`, a `SELECT … FOR UPDATE SKIP LOCKED`, so two builders never take the same one), or creates one through the Fly Machines API ([`fly/client.go`](../internal/build/fly/client.go), plain HTTPS calls to `api.machines.dev`).
     - The job goes in one environment variable, `GREYSHIP_BUILD_JOB`: the source download link, the targets, the API address and the one-build token (signed with `SignToken`).
     - `follow` polls every half second: new rows in `build_reports` become your log lines, the outcome row ends the wait, and a machine that stops without reporting is an error that gets retried. Past the plan's timeout, it stops the machine.
     - `release` stops the machine and returns it to the pool, or destroys it for Free.
   - **`greyship-buildd`** ([`internal/buildd/buildd.go`](../internal/buildd/buildd.go)), on the Fly machine: download, `unpack` (refusing any file path that escapes the folder), `go mod download`, `go build` per target, `upload` (asks `POST /v1/internal/builds/{id}/upload` for a link signed for that size), and the reporter, which batches log lines to `…/report` twice a second. It always reports an outcome, even on failure.
   - **The API side of that** ([`fly/reports.go`](../internal/build/fly/reports.go)): every call must carry the build token for *that* deployment, and the deployment must still be `building`. Lines are capped in number and length.
6. Back in `build`: records runner, machine size and seconds (`RecordBuildRun`), creates an `artifacts` row per binary, then `finish`.
7. **Errors** are sorted into the user's fault (doesn't compile, too slow, bad config), which fails the deployment and doesn't retry, and ours (storage down, Fly hiccup), which retries the job.

## 2.8 GitHub deploys (`internal/github`)

- [`github.go`](../internal/github/github.go) is the GitHub client:
  - `jwt()` signs a 9-minute token with the app's private key (RS256);
  - `InstallationToken` trades it for a 1-hour token for one installation, cached in memory;
  - the rest are single API calls (`Repos`, `BranchHead`, `Tarball`, `SetStatus`);
  - `ValidSignature` checks webhook deliveries with HMAC-SHA256 in constant time.
- [`service.go`](../internal/github/service.go):
  - **`InstallURL`** signs `{team, you}` into the `state` GitHub hands back.
  - **`Callback`**: the state must match you, then it exchanges GitHub's `code` for your user token and asks GitHub which installations *you* can access. Only one of those is saved (`AddInstallation`).
  - **`Import` / `Connect`** check the repo is in one of *your team's* installations (`find`), then `ConnectRepo`.
  - **`Push`** reads the webhook body. For each project connected to that repo and branch, through the same installation, it calls `deploy`: `CreateGithubDeployment`, which does nothing if that commit already has one, plus a `github.fetch` job.
  - **`Fetcher.Handle`** (builder):
    1. `Tarball` downloads the commit.
    2. [`repack.go`](../internal/github/repack.go) unpacks it safely and re-packs it with `internal/pack`, so the same files are left out as for `grey deploy`.
    3. Stores it, `SetFetchedSource`, then `deploy.QueueBuild`. From here it's an ordinary server build.
  - **`Statuses.Run`** (builder) every 5 seconds: `GithubStatusesDue` finds deployments whose GitHub status is behind; `SetStatus` posts it; `MarkGithubReported` remembers it.
- **Public links** ([`public.go`](../internal/github/public.go)):
  - `ParseRepoURL` accepts only github.com links;
  - `PublicRefs` reads git's ref list (`…/info/refs?service=git-upload-pack`, a simple length-prefixed format) to find branches and the default branch;
  - `PublicTarball` downloads from codeload.

  `ImportPublic` connects the project with no installation; `DeployLatest` deploys the branch head for either kind; `Fetcher.Handle` picks the public download when there's no installation.
- The API handlers are in [`internal/api/github.go`](../internal/api/github.go). The webhook handler checks the signature before reading anything else.

## 2.9 The scheduler (`internal/sched`)

- It runs **one leader** at a time (a Postgres advisory lock), so two schedulers never make conflicting decisions.
- **Release** (`deployment.release` jobs): for a built deployment,
  1. takes a per-project lock;
  2. runs the **release command** first, if there is one (`release_cmd.go`): one instance of `cmd/migrate`, wait for its exit code. Failure stops the deploy and the old version keeps serving.
  3. creates or updates the `services` rows and picks a node with enough free memory;
  4. sends `instance.start` commands to that node's agent, as jobs of kind `node.<id>`;
  5. waits until the new instances are healthy;
  6. switches `releases` to the new deployment;
  7. `NotifyRoutes` (so routers update instantly);
  8. stops the old instances.

  If a new instance never becomes healthy, the new ones are stopped and nothing switches.
- **Redeploy and rollback** (`grey redeploy`, `grey rollback`, the dashboard buttons) don't build anything. `deploy.Service.Redeploy` copies an earlier deployment's row and its `artifacts` (pointing at the same binaries in R2) into a new deployment, marks it `built`, and queues the same release job with `skip_release_command`, so step 2 is skipped and the rest runs as usual: health check first, then switch.
- **Scale-up** (`service.scale_up`, from the router), in [`scale.go`](../internal/sched/scale.go): re-checks the request (they repeat and race), then starts one more instance if the service is below its maximum.
- **Sweeps:** `SweepIdle` stops instances paused longer than their idle limit; `SweepLogs` deletes app logs older than the retention.
- **Env changed** (`roll.go`): restarts running services with the new values, one fresh instance at a time, health-checked.

## 2.10 The agent and the app sandbox (`internal/agent`, `internal/runtime/gvisor`, `internal/guestinit`)

- The agent registers its node, heartbeats, and claims `node.<its id>` jobs (start, stop, pause, resume) with 4 workers at once. The scheduler and router send each instance's commands as a group of one (`sched.SendCommand`), so one instance's commands still happen in order while different instances start side by side.
- **Start** ([`agent.go`](../internal/agent/agent.go) `Start`):
  1. `image`: download the binary from R2, check its sha256, and make a minimal container image from two files, `greyship-init` and `app`, plus the deployment's runtime files under `/srv` if it has any ([`files.go`](../internal/agent/files.go), every name checked again, all read-only). The image is named after the hashes of init, the binary and the files, so it's made once per server. With files, the app starts in `/srv`, so `os.Open("scripts/install.sh")` finds them.
  2. `Env`: compute the app's environment: your variables decrypted (through `internal/env` and `internal/secrets`, envelope encryption with AES-GCM, each value bound to its project and key name so it can't be copied to another row), plus `PORT` and the `GREYSHIP_*` variables, which you can't override.
  3. `Driver.Start` ([`gvisor.go`](../internal/runtime/gvisor/gvisor.go)): create a private Docker network for this instance and `docker run --runtime=runsc` with every lock on: read-only, no capabilities, nobody user, memory and CPU and process limits, the port published only on loopback, plus the two read-only files for DNS and certificates. The config, secrets included, goes in through **stdin**, not as container environment, because Docker stores container environment on disk.
  4. `healthy`: a web app must accept connections within 30 seconds (on any port, see below); a worker must still be running after a few seconds. If not, the instance is stopped and its last output lines explain why.
- **Inside the sandbox** ([`guestinit`](../internal/guestinit)): `greyship-init` is process 1. It reads one JSON line (environment and arguments) from stdin, starts `/app` with exactly that environment, passes stop signals through, and reaps orphaned processes. For a web service it also runs the **doorman** ([`doorman.go`](../internal/guestinit/doorman.go)): it watches the sandbox's list of listening sockets until the app opens a port. Port 8080 is the only way in from the router. If the app is already there, the doorman steps aside; if it chose another port (say `:3000`, or `localhost:3000`), the doorman listens on 8080 and passes every connection through to it, byte for byte, until one side hangs up.
- **Logs:** everything the app prints goes from the sandbox's output to the agent, which rate-limits it and writes it to `app_logs`.
- **On restart** the agent removes leftovers and marks its old instances stopped. Web services wake again on their next request. (Plain workers don't yet; they get woken properly when the queue arrives in step 20.)

## 2.11 The router (`internal/router`)

- **Table** ([`table.go`](../internal/router/table.go)): an in-memory map from label (`api-temp`) to that service's backends (instances with their loopback addresses and in-flight counters). It reloads from Postgres on every `NOTIFY routes` and on a timer.
- **`ServeHTTP`** ([`router.go`](../internal/router/router.go)):
  1. Is this host one of ours (`api.greyship.tech`, under the *platform* domain)? Then proxy it to the API.
  2. Otherwise take the label under the *apps* domain and look it up.
  3. If there's a running backend, `pick` takes turns between that service's instances, forward the request (`httputil.ReverseProxy`), and track in-flight requests and last activity.
  4. If none: `hold`, i.e. ask to wake it (`Resume` a paused one, or a `service.scale_up` job), and wait on table changes for up to 30 seconds.
  5. If every backend is at its concurrency limit, ask for one more.
- **`PauseIdle`**: a web instance with nothing in flight for its grace period gets paused through the agent.
- **TLS** (`cmd/greyship-router`): CertMagic gets certificates on demand from Let's Encrypt. The `Serves` check allows only hostnames we route. Plain HTTP redirects to HTTPS.

## 2.11a Notifications (`internal/notify`, `internal/webpush`)

- **When:** the code that marks a deployment `ready` or `failed` (scheduler, builder, verifier, GitHub fetcher) and the code that creates a project also queue a `notify` job, in the same database transaction. So there's a notification exactly when the thing really happened.
- **The worker** ([`notify.go`](../internal/notify/notify.go)) runs inside `greyship-api`. For each job it:
  1. works out who to tell: whoever started the deploy, or for a GitHub push whoever connected GitHub; skips them if they left the team;
  2. writes the bell entry (`notifications`); a retried job finds the same row;
  3. reads their settings and sends the email (if not sent yet) and the browser pushes (if not sent yet).
- **Browser push** ([`webpush.go`](../internal/webpush/webpush.go)): when you click Turn on, the browser gives us an address at its push service (Google's, Mozilla's, Apple's) and two keys. To push, we encrypt the message so only that browser can read it, sign the request with our VAPID key, and post it to that address. Only those known push services are ever contacted.
- **The dashboard:** the bell in [`menus.tsx`](../web/components/shell/menus.tsx) polls `/v1/notifications` once a minute; Settings > Notifications ([`notification-settings.tsx`](../web/components/team/notification-settings.tsx)) holds the switches and registers [`sw.js`](../web/public/sw.js), the small background script that shows a push and opens the page you click.

## 2.11b The admin view (`internal/admin`, `web/app/(admin)`)

- **Who:** only an instance owner (the first account on an instance, or `GREYSHIP_OWNER_EMAIL`), signed in to the dashboard. The API checks `users.is_owner` in `requireAdmin` ([`admin.go`](../internal/api/admin.go)); a CLI token never works there, and anyone else gets "not found".
- **What:** `greyship.tech/admin` shows totals, the latest failed deployments, nodes, the job queue and failed jobs. Paste a deployment id (or its first characters, as the CLI shows it) into the search box to get its page: the error, who started it, how it was built, its binaries, instances and their errors, every job that worked on it, the full build log and what its instances printed. It can't change anything.

## 2.12 The dashboard (`web/`)

- Next.js on Vercel. **`web/proxy.ts`** runs in front of every request. It forwards `/v1/*` to `GREYSHIP_API_URL` with the visitor's real IP and the proxy secret, so the browser only ever talks to `greyship.tech`. It also hides the dashboard during "coming soon" unless `NEXT_PUBLIC_DASHBOARD=open`.
- **Pages** read data through one client, [`web/lib/api/index.ts`](../web/lib/api/index.ts):
  - with `GREYSHIP_API_URL` set, it uses [`live.ts`](../web/lib/api/live.ts), which calls the API from the server with your session cookie and converts the JSON into what the screens expect;
  - without it, sample data (fixtures), for designing screens without an API.

  `available` marks the screens the API can't answer yet; those say when they arrive instead of showing sample numbers.
- **Sign-in, device approval and imports** call `/v1` from the browser through [`web/lib/greyship-api.ts`](../web/lib/greyship-api.ts).

## 2.13 Where to look when something breaks

| Symptom | First look |
| --- | --- |
| Can't sign in | `journalctl -u greyship-api`; check the Resend dashboard for the email |
| Deploy stuck at "queued" | `journalctl -u greyship-builder` (workers, Fly errors) |
| Build fails on Fly | The deploy log itself; then the builder journal; `fly machines list -a greyship-build` |
| Built but never live | `journalctl -u greyship-scheduler` and `-u greyship-agent`; the deploy log shows the app's last lines |
| App URL doesn't load | `journalctl -u greyship-router`; is DNS for `*.greyshipapp.tech` right? |
| GitHub push didn't deploy | GitHub app settings → Advanced → Recent deliveries (status code); `journalctl -u greyship-api` |
| GitHub status not updating | `journalctl -u greyship-builder` (status sweep warnings) |
