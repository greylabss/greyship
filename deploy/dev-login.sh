#!/bin/sh
# Signs in to the local API with an emailed code (read from tmp/outbox, since nothing is really sent in
# development), creates a CLI token for your personal team and writes tmp/dev.env. Then: . tmp/dev.env
set -eu
cd "$(dirname "$0")/.."
email=${1:-you@example.com}
api=http://localhost:8080
jar=$(mktemp)
trap 'rm -f "$jar"' EXIT

before=$(ls tmp/outbox 2>/dev/null | wc -l)
curl -fs -X POST "$api/v1/auth/email/start" -H 'Content-Type: application/json' -d "{\"email\":\"$email\"}" -o /dev/null
for i in $(seq 1 20); do
	[ "$(ls tmp/outbox | wc -l)" -gt "$before" ] && break
	sleep 0.25
done
code=$(cat "tmp/outbox/$(ls -t tmp/outbox | head -1)" | grep -oE '\b[0-9]{6}\b' | head -1)
curl -fs -c "$jar" -X POST "$api/v1/auth/email/verify" -H 'Content-Type: application/json' -d "{\"email\":\"$email\",\"code\":\"$code\"}" -o /dev/null
team=$(curl -fs -b "$jar" "$api/v1/me" | sed -n 's/.*"teams":\[{"id":"[^"]*","slug":"\([^"]*\)".*/\1/p')
token=$(curl -fs -b "$jar" -X POST "$api/v1/tokens" -H "Origin: http://localhost:3000" -H 'Content-Type: application/json' -d "{\"name\":\"local\",\"team\":\"$team\"}" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
[ -n "$token" ] || { echo "couldn't create a token; see tmp/logs/greyship-api.log"; exit 1; }
umask 077
printf 'export GREYSHIP_API_URL=%s\nexport GREYSHIP_TOKEN=%s\nexport PATH="%s/bin:$PATH"\n' "$api" "$token" "$PWD" > tmp/dev.env
echo "✓ Signed in as $email (team $team). Run:  . tmp/dev.env   then use grey as usual."
