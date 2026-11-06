# Contributing to Greyship

Thanks for helping. Greyship is early, so the best contributions right now are bug reports with a way to reproduce
them, and small focused fixes.

## Before you start

- Open an issue for anything bigger than a small fix, so we can agree on the approach first.
- Read [`docs/architecture.md`](docs/architecture.md) for what you're touching, and [`AGENTS.md`](AGENTS.md) for the
  code rules (they apply to people too).
- Docs and code change together. If a change alters behaviour, the CLI, the API, the data model or the deployment,
  update `docs/spec.md` and `docs/architecture.md` in the same pull request.

## Working on it

```sh
make up          # Postgres, storage, the sandbox VM and all services (macOS: needs Docker and Lima)
make lint        # golangci-lint
make test        # go test -race ./...
make test-int    # integration tests (need Postgres and the sandbox)
```

- Go: clean, boring, readable. Table-driven tests, `-race`, no new dependencies without a reason.
- Database changes are new migrations in `migrations/` plus `sqlc` queries (`make gen`); never edit an applied
  migration.
- The dashboard lives in `web/` (Next.js, `bun`). `bun run lint` and `bun run build` must pass.
- Commits: conventional style, e.g. `feat(router): hold requests during cold start`.

## Security

Never report a vulnerability in a public issue. See [`SECURITY.md`](SECURITY.md).

## Licence

By contributing you agree your work is licensed under the AGPL-3.0, like the rest of the project.
