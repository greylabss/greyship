#!/bin/sh
# Prepares the sandbox VM's Docker (architecture §2.2, §5): the internal gs-build network, which has no route out,
# the module proxy, the only thing on that network that can reach the internet, and the firewall rule that keeps
# containers away from the node. Safe to re-run; run it again after the VM restarts.
set -eu

export DOCKER_HOST="${GREYSHIP_BUILD_DOCKER_HOST:-unix://$HOME/.lima/greyship/sock/docker.sock}"
arch=$(docker info --format '{{.Architecture}}')
case "$arch" in
	x86_64) goarch=amd64 ;;
	aarch64) goarch=arm64 ;;
	*) echo "unsupported VM architecture $arch" >&2; exit 1 ;;
esac

ctx=$(mktemp -d)
trap 'rm -rf "$ctx"' EXIT
# On a server there's no Go toolchain: the release ships the proxy binary (MODPROXY_BINARY) and its Dockerfile.
if [ -n "${MODPROXY_BINARY:-}" ]; then
	cp "$MODPROXY_BINARY" "$ctx/greyship-modproxy"
	cp "$(dirname "$MODPROXY_BINARY")/modproxy.Dockerfile" "$ctx/Dockerfile"
else
	CGO_ENABLED=0 GOOS=linux GOARCH=$goarch go build -trimpath -ldflags="-s -w" -o "$ctx/greyship-modproxy" ./cmd/greyship-modproxy
	cp deploy/modproxy/Dockerfile "$ctx/"
fi
docker build -q -t greyship/modproxy "$ctx" >/dev/null

# Fixed addresses: Docker's embedded DNS doesn't work under gVisor, so builds get the proxy as a host entry.
docker network inspect gs-build >/dev/null 2>&1 || docker network create --internal --subnet 172.30.0.0/24 gs-build >/dev/null
docker volume create gs-modproxy-cache >/dev/null
docker rm -f modproxy >/dev/null 2>&1 || true
# root inside the container only so it can write the root-owned cache volume; every capability is dropped.
docker run -d --name modproxy --restart unless-stopped --user 0 \
	--read-only --cap-drop=ALL --security-opt=no-new-privileges \
	-v gs-modproxy-cache:/cache greyship/modproxy >/dev/null
docker network connect --ip 172.30.0.2 gs-build modproxy
# Containers (builds and instances) may never open connections to the node itself (architecture §5). Replies
# to connections the node starts (health checks, the router) still pass, because only NEW ones are dropped.
as_root() {
	if [ "$(id -u)" = 0 ]; then
		"$@"
	elif command -v limactl >/dev/null && limactl list -q 2>/dev/null | grep -qx greyship; then
		limactl shell greyship sudo "$@"
	else
		sudo "$@"
	fi
}
for iface in 'br-+' docker0; do
	as_root iptables -C INPUT -i "$iface" -m conntrack --ctstate NEW -j DROP 2>/dev/null ||
		as_root iptables -I INPUT -i "$iface" -m conntrack --ctstate NEW -j DROP
done

# Instances get these read-only (architecture §5): Docker's embedded DNS doesn't answer under gVisor, and app
# images hold nothing but guest init and the app, so there are no CA certificates to verify TLS with.
shared=/var/lib/greyship-sandbox
dns="${GREYSHIP_RUNTIME_DNS:-1.1.1.1 8.8.8.8}"
as_root sh -c '
	set -eu
	mkdir -p "$1"
	for ns in $2; do echo "nameserver $ns"; done > "$1/resolv.conf"
	for ca in /etc/ssl/certs/ca-certificates.crt /etc/pki/tls/certs/ca-bundle.crt; do
		if [ -f "$ca" ]; then cp "$ca" "$1/ca-certificates.crt"; break; fi
	done
	test -s "$1/ca-certificates.crt" || { echo "no CA bundle found on this host" >&2; exit 1; }
	chmod 755 "$1"
	chmod 444 "$1/resolv.conf" "$1/ca-certificates.crt"
' sh "$shared" "$dns"

echo "sandbox ready: network gs-build, module proxy at http://modproxy:8080 (172.30.0.2)"
