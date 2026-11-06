# Greyship

**Serverless for Go, on your own server.** Deploy any Go backend (APIs, workers, crons) with one command. Greyship
reads your code to find your services, builds them, runs each one in its own [gVisor](https://gvisor.dev) sandbox,
gives web services an HTTPS URL, and puts them to sleep when nobody is using them. The next request wakes them in
milliseconds. No Dockerfile, no YAML.

> **Status: early.** Greyship is young. It runs real apps today and is in a private beta, but expect rough edges and
> breaking changes. Single server only for now.

```sh
curl -fsSL https://greyship.tech/install | sh     # the CLI, on your laptop
grey login https://greyship.example.com           # your own Greyship
cd my-go-project && grey deploy
```

## What it does

- **`grey deploy`**: builds on your machine (only changed binaries are uploaded) or on the server, health-checks the
  new version, then switches traffic. A failed deploy changes nothing: the live version keeps serving.
- **Deploy from GitHub**: import a repository (public link, or the GitHub App for push-to-deploy).
- **Scale to zero**: idle web services pause after 30 seconds and wake on the next request.
- **Any port**: listen on `:3000`, `localhost`, or `$PORT`; Greyship serves it.
- **Env vars** encrypted at rest, **runtime files** (`files = [...]` in `greyship.toml`), **release command** for
  migrations, **rollbacks** in seconds, **logs**, and notifications by email, push and the dashboard bell.
- **Sandboxed**: every app runs in gVisor, with its own network. Tenants can't reach each other, the host, or
  Greyship's internals.

Documentation: <https://docs.greyship.tech>. How it works inside: [`docs/how-it-works.md`](docs/how-it-works.md) and
[`docs/architecture.md`](docs/architecture.md).

## Self-host it

You need a fresh **Ubuntu 22.04/24.04 or Debian 12** server (4 GB RAM or more, x86_64 or arm64, ports 80 and 443
open), a domain for the dashboard, and a second domain for your apps, each with DNS records pointing at the server.
Then, as root on the server:

```sh
curl -fsSL https://raw.githubusercontent.com/greylabss/greyship/main/deploy/install/install.sh | sudo sh
```

The installer asks for your domains and an email for Let's Encrypt, installs Docker, gVisor and Postgres, sets up
Greyship as systemd services with generated secrets, and tells you which DNS records to add. Open the dashboard and
create your account: **the first account is the owner.** More in the
[self-hosting guide](https://docs.greyship.tech/self-hosting).

## Build from source

Go 1.27, Docker (for Postgres and storage), and [Lima](https://lima-vm.io) on macOS for the gVisor sandboxes:

```sh
make up        # infrastructure, the sandbox VM, and every service
make login
make hello     # deploys examples/hello
make test      # unit tests; make test-int for integration tests
```

See [`docs/try-it.md`](docs/try-it.md) and [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Licence

[AGPL-3.0](LICENSE). You can run, study and change Greyship freely. If you offer a modified Greyship to others over a
network, you must publish your changes under the same licence.
