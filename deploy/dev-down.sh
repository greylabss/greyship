#!/bin/sh
# Stops what `make up` started and removes any app instances it left in the sandbox VM.
# Postgres, Garage and the VM keep running (limactl stop greyship stops the VM).
set -u
cd "$(dirname "$0")/.."
for f in tmp/run/*.pid; do
	[ -f "$f" ] || continue
	kill "$(cat "$f")" 2>/dev/null && echo "stopped $(basename "$f" .pid)"
	rm -f "$f"
done
sock="unix://$HOME/.lima/greyship/sock/docker.sock"
ids=$(docker -H "$sock" ps -aq --filter label=greyship=instance 2>/dev/null)
if [ -n "$ids" ]; then
	# Paused (sleeping) instances are woken first: removing a paused gVisor container can hang.
	docker -H "$sock" unpause $ids >/dev/null 2>&1
	docker -H "$sock" rm -f $ids >/dev/null 2>&1
	echo "removed app instances"
fi
docker -H "$sock" network prune -f --filter label=greyship=instance >/dev/null 2>&1
exit 0
