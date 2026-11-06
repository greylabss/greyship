# AGENTS.md

Rules for anyone changing this repository, human or AI.

## What this is

**Greyship** is an open-source (AGPL-3.0) serverless runtime for Go that you install on your own server: developers
deploy Go backends (APIs, workers, crons) with `grey deploy` or from the dashboard; apps scale to zero when idle and
wake on the next request. Read `docs/spec.md` (what it does) and `docs/architecture.md` (how) before changing anything.

## Serverless rules

Every change keeps these true: users never manage servers for their apps; an idle service uses no CPU or memory;
everything scales up with load and down to zero; background features wake instances on events, never keep them
running.

## Docs and code stay consistent

If a change alters behaviour, the public API, the CLI, the data model, a component's responsibility or the
deployment, update `docs/spec.md` and `docs/architecture.md` in the same change, and add an entry to
`docs/decisions.md` for an architectural shift. If a task contradicts the docs, say so before writing code.

## Code style

Clean, boring, readable Go. Clarity over cleverness; short functions, early returns, obvious names. Minimal
comments: say *why*, not what. `gofmt`, `goimports`, `golangci-lint` clean. `context.Context` first for anything that
does I/O. Return and wrap errors (`fmt.Errorf("start instance %s: %w", id, err)`). `log/slog` with key-value pairs,
never logging secrets, env var values, tokens or request bodies. No package-level mutable state. Prefer the standard
library; adding a dependency needs a reason. Define interfaces where they're used.

## Database

Schema changes are new files in `migrations/`; never edit an applied one. Queries live in `.sql` files and are
generated with `sqlc` (`make gen`). Data model changes are reflected in `docs/architecture.md` §3.

## Security (non-negotiable)

Treat every user's code and build as hostile: builds and apps always run inside a sandbox (gVisor or Firecracker),
never directly on the host. Never build shell commands by concatenating strings. Secrets are decrypted only at
instance start and never written to logs, snapshots, errors or API responses. Tenants must never reach each other,
the host or internal services. Validate all input; enforce authorization on every endpoint. Never disable AppArmor,
SELinux, sandboxing or TLS verification to make something work.

## CLI and dashboard parity

Every action is a public API endpoint first; the CLI and the dashboard are both clients of the API.

## Testing

Table-driven tests with the standard `testing` package, run with `-race`. Integration tests that need Postgres or a
sandbox use the build tag `integration`. New behaviour comes with tests; a bug fix comes with a test that would have
caught it.

## Commands

`make up` start everything locally, `make test`, `make test-int`, `make lint`, `make gen`, `make build`.
