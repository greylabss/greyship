#!/bin/sh
# One-time setup of the dev Garage node: give it a storage role, import the dev access key and create the
# buckets (greyship for make dev, greyship-test for make test-int). Safe to run again; make infra runs it every time.
set -eu

compose="docker compose -f deploy/compose.dev.yml"
g() { $compose exec -T -e RUST_LOG=warn garage /garage "$@"; }

key_id=GK677265797368697064657631
secret=6772657973686970206465762073336b6579206e6f7420666f722070726f6421

if g layout show | grep -q "Current cluster layout version: 0"; then
	node=$(g node id -q | cut -d@ -f1)
	g layout assign -z dev -c 2G "$node" >/dev/null
	g layout apply --version 1 >/dev/null
fi

g key info "$key_id" >/dev/null 2>&1 || g key import --yes -n greyship-dev "$key_id" "$secret" >/dev/null

for bucket in greyship greyship-test; do
	g bucket info "$bucket" >/dev/null 2>&1 || g bucket create "$bucket" >/dev/null
	g bucket allow --read --write --owner "$bucket" --key "$key_id" >/dev/null
done
echo "garage ready: buckets greyship, greyship-test"
