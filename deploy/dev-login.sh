#!/bin/sh
# Signs in to the local API (the first run creates the owner with this email and password; development only),
# creates a CLI token for your personal team and writes tmp/dev.env. Then: . tmp/dev.env
set -eu
cd "$(dirname "$0")/.."
email=${1:-you@example.com}
password=${2:-dev-password-123}
api=http://localhost:8080
jar=$(mktemp)
trap 'rm -f "$jar"' EXIT

if curl -fs "$api/v1/auth/setup" | grep -q '"setup_needed":true'; then
	path=/v1/auth/setup
else
	path=/v1/auth/password/login
fi
curl -fs -c "$jar" -X POST "$api$path" -H 'Content-Type: application/json' -d "{\"email\":\"$email\",\"password\":\"$password\"}" -o /dev/null ||
	{ echo "couldn't sign in as $email: the local instance may already have another owner or password"; exit 1; }
team=$(curl -fs -b "$jar" "$api/v1/me" | sed -n 's/.*"teams":\[{"id":"[^"]*","slug":"\([^"]*\)".*/\1/p')
token=$(curl -fs -b "$jar" -X POST "$api/v1/tokens" -H "Origin: http://localhost:3000" -H 'Content-Type: application/json' -d "{\"name\":\"local\",\"team\":\"$team\"}" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
[ -n "$token" ] || { echo "couldn't create a token; see tmp/logs/greyship-api.log"; exit 1; }
umask 077
printf 'export GREYSHIP_API_URL=%s\nexport GREYSHIP_TOKEN=%s\nexport PATH="%s/bin:$PATH"\n' "$api" "$token" "$PWD" > tmp/dev.env
echo "✓ Signed in as $email (team $team). Run:  . tmp/dev.env   then use grey as usual."
