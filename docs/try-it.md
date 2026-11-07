# Try Greyship

Two ways to ship a backend through Greyship: on your Mac in about five minutes, or on the real server once it's set up.

## On your Mac

You need Docker Desktop running and Lima installed (`brew install lima`). The first `make up` creates the sandbox VM, which takes a few minutes; later runs take seconds.

```sh
make up        # Postgres, Garage, the sandbox VM, then the five Greyship services
make login     # creates the local owner (you@example.com, password dev-password-123) and a CLI token
make hello     # deploys examples/hello: an API, a worker and a migration
```

`make hello` ends with something like:

```
✓ Live
  api     https://api-hello.localhost:8443
  worker  (worker, no URL)
```

Open it:

```sh
curl --cacert tmp/devca/ca.pem https://api-hello.localhost:8443/
```

To open it in a browser, trust the local certificate authority once with `make trust-devca`. That CA can only vouch for `*.localhost`, never a real site.

### Things to try

Load the CLI settings first: `. tmp/dev.env`. Then run these inside `examples/hello`:

| Try | What you'll see |
| --- | --- |
| `grey logs -f` | The API, the worker and the migration logging live |
| Leave it alone for 30 seconds, then `curl` again | It paused while idle and woke on your request in a few hundredths of a second; `visits` kept counting, because a paused app keeps its memory |
| `grey env set GREETING=hi` | A couple of seconds later the API says "hi", with no rebuild |
| Edit `.env.prod`, then `grey deploy` | A key-only diff of what would change, and a request to confirm |
| Break `cmd/migrate` (add `os.Exit(1)`), then `grey deploy` | The deploy stops with the migration's output, and the old version keeps serving |
| Break the API's code, then `grey deploy` | The compiler error, and the old version keeps serving |
| `grey env pull` | `.env.prod` rewritten from what's stored, readable only by you |

To ship your own backend, run `grey deploy` in any Go module once `. tmp/dev.env` is loaded. You don't need a Dockerfile or a config file.

When you're done:

```sh
make down              # stops the services and removes app instances
limactl stop greyship  # frees the VM's memory (make up starts it again)
```

The service logs are in `tmp/logs/`, if anything misbehaves.

## On the server

Install Greyship on a server with `deploy/install/install.sh` (see `docsweb/content/docs/self-hosting.mdx`), install the CLI from the server's `/install` address, run `grey login`, and `grey deploy` gives a real `https://<service>-<project>.<your apps domain>` URL.
