#!/bin/sh
# Starts the whole platform on this Mac for trying it out: API, builder, scheduler, agent and router, each in the
# background with its log in tmp/logs. Infrastructure (Postgres, Garage, the sandbox VM) must already be up;
# `make up` does that first. Stop everything with `make down`.
set -eu
cd "$(dirname "$0")/.."

mkdir -p tmp/logs tmp/run tmp/outbox bin
db="postgres://greyship:greyship@localhost:5434/greyship?sslmode=disable"
[ -f tmp/dev-cookie-secret ] || openssl rand -hex 32 > tmp/dev-cookie-secret

echo "building…"
for b in greyship-api greyship-builder greyship-scheduler greyship-agent greyship-router; do
	go build -o "bin/$b" "./cmd/$b"
done
go build -o bin/grey ./cmd/greyship
[ -f tmp/dev-vapid-key ] || bin/greyship-api vapid-key > tmp/dev-vapid-key
CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o bin/greyship-init ./cmd/greyship-init
go tool goose -dir migrations postgres "$db" up >/dev/null

# Development settings only. The login email goes to tmp/outbox, never a real mail provider.
start() {
	name=$1; shift
	if [ -f "tmp/run/$name.pid" ] && kill -0 "$(cat "tmp/run/$name.pid")" 2>/dev/null; then
		kill "$(cat "tmp/run/$name.pid")" 2>/dev/null || true
		sleep 1
	fi
	env -i PATH="$PATH" HOME="$HOME" GREYSHIP_ENV=development GREYSHIP_DATABASE_URL="$db" "$@" "bin/$name" > "tmp/logs/$name.log" 2>&1 &
	echo $! > "tmp/run/$name.pid"
}
start greyship-api GREYSHIP_COOKIE_SECRET="$(cat tmp/dev-cookie-secret)" GREYSHIP_VAPID_PRIVATE_KEY="$(cat tmp/dev-vapid-key)" GREYSHIP_BETA_URL="${GREYSHIP_BETA_URL:-}" GREYSHIP_BETA_SECRET="${GREYSHIP_BETA_SECRET:-}" GREYSHIP_SIGNUP_ALLOWLIST="${GREYSHIP_SIGNUP_ALLOWLIST:-}" GREYSHIP_OUTBOX_DIR="$PWD/tmp/outbox" GREYSHIP_RESEND_API_KEY=
start greyship-builder
start greyship-scheduler
start greyship-agent GREYSHIP_INIT_BINARY="$PWD/bin/greyship-init" GREYSHIP_NODE_NAME=dev-1
start greyship-router GREYSHIP_ROUTER_DEV_CA="$PWD/tmp/devca"

for i in $(seq 1 30); do
	curl -fs -o /dev/null http://localhost:8080/healthz && break
	sleep 0.5
done
curl -fs -o /dev/null http://localhost:8080/healthz || { echo "the API didn't start; see tmp/logs/greyship-api.log"; exit 1; }
sleep 2
for name in greyship-builder greyship-scheduler greyship-agent greyship-router; do
	kill -0 "$(cat "tmp/run/$name.pid")" 2>/dev/null || { echo "$name stopped; see tmp/logs/$name.log"; tail -3 "tmp/logs/$name.log"; exit 1; }
done
echo "✓ Greyship is running locally (logs in tmp/logs). Next: make login, then make hello."
