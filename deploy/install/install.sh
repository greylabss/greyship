#!/bin/sh
# Installs Greyship on this server, or upgrades it (decisions.md 050). Run as root on a fresh Ubuntu 22.04/24.04 or
# Debian 12 server (x86_64 or arm64, 4 GB RAM or more, ports 80 and 443 free):
#
#   curl -fsSL https://raw.githubusercontent.com/greylabss/greyship/main/deploy/install/install.sh | sudo sh
#
# It installs Docker, gVisor, Postgres, storage and Node, downloads the release, writes every secret into
# /etc/greyship (root-only), sets Greyship up as systemd services, and prints the DNS records to add. Running it again
# upgrades in place and keeps /etc/greyship.
#
# Questions can be answered up front instead (for scripts):
#   --domain greyship.example.com   where the dashboard lives (needs a DNS record)
#   --apps-domain example.net       where apps live (needs a wildcard record); better a different domain than the dashboard's
#   --email you@example.com         owner account, and the address Let's Encrypt writes to
#   --sslip                         no domain: use free sslip.io names built from this server's IP (for trying it out:
#                                   all of sslip.io is one site to browsers, and Let's Encrypt's limits are shared)
#   --version v0.1.0                a release other than the latest
#   --yes                           don't ask; fail if something is missing
set -eu

REPO=${GREYSHIP_REPO:-greylabss/greyship}
VERSION=${GREYSHIP_VERSION:-latest}
LIB=/usr/local/lib/greyship
ETC=/etc/greyship

say() { printf '%s\n' "$*"; }
step() { printf '\n== %s\n' "$*"; }
fail() {
	printf 'greyship install: %s\n' "$*" >&2
	exit 1
}

PLATFORM_DOMAIN=${GREYSHIP_DOMAIN:-}
APPS_DOMAIN=${GREYSHIP_APPS_DOMAIN:-}
OWNER_EMAIL=${GREYSHIP_OWNER_EMAIL:-}
SSLIP=0
YES=0

parse_args() {
	while [ $# -gt 0 ]; do
		case "$1" in
			--domain) PLATFORM_DOMAIN=${2:?--domain needs a value}; shift ;;
			--apps-domain) APPS_DOMAIN=${2:?--apps-domain needs a value}; shift ;;
			--email) OWNER_EMAIL=${2:?--email needs a value}; shift ;;
			--version) VERSION=${2:?--version needs a value}; shift ;;
			--sslip) SSLIP=1 ;;
			--yes | -y) YES=1 ;;
			*) fail "unknown option $1 (see the top of this script)" ;;
		esac
		shift
	done
}

# ask VAR "question" "default": keeps a value already set, else asks on the terminal (also when piped from curl).
ask() {
	var=$1
	question=$2
	default=${3:-}
	eval "current=\${$var:-}"
	[ -z "$current" ] || return 0
	if [ "$YES" = 0 ] && (: </dev/tty) 2>/dev/null; then
		if [ -n "$default" ]; then
			printf '%s [%s]: ' "$question" "$default" >/dev/tty
		else
			printf '%s: ' "$question" >/dev/tty
		fi
		read -r answer </dev/tty || answer=
		eval "$var=\${answer:-$default}"
	else
		eval "$var=\$default"
	fi
	eval "current=\${$var:-}"
	[ -n "$current" ] || fail "missing $var; pass it as an option (see the top of this script)"
}

check_system() {
	[ "$(id -u)" = 0 ] || fail "run as root (sudo)"
	[ -f /etc/os-release ] || fail "can't tell which OS this is"
	# Read in a subshell: os-release defines VERSION, which would overwrite ours.
	ID=$(. /etc/os-release && printf '%s' "${ID:-}")
	VERSION_CODENAME=$(. /etc/os-release && printf '%s' "${VERSION_CODENAME:-}")
	case "$ID" in
		ubuntu | debian) ;;
		*) fail "unsupported OS ${ID:-unknown}: use Ubuntu 22.04/24.04 or Debian 12" ;;
	esac
	case "$(uname -m)" in
		x86_64 | amd64) ARCH=amd64; GVISOR_ARCH=x86_64 ;;
		aarch64 | arm64) ARCH=arm64; GVISOR_ARCH=aarch64 ;;
		*) fail "unsupported CPU $(uname -m): Greyship runs on x86_64 and arm64" ;;
	esac
	command -v systemctl >/dev/null || fail "this server doesn't use systemd"
	mem_mb=$(awk '/MemTotal/ { print int($2 / 1024) }' /proc/meminfo)
	[ "$mem_mb" -ge 1800 ] || fail "only ${mem_mb} MB of memory: Greyship needs at least 2 GB, and 4 GB or more to build apps comfortably"
	[ "$mem_mb" -ge 3500 ] || say "warning: ${mem_mb} MB of memory is tight. Builds on the server may fail; 4 GB or more is better"
	if ! systemctl is-active --quiet greyship-router 2>/dev/null; then
		for port in 80 443; do
			if ss -ltn "( sport = :$port )" 2>/dev/null | grep -q LISTEN; then
				fail "something already listens on port $port; Greyship's router needs ports 80 and 443"
			fi
		done
	fi
}

public_ip() {
	for url in https://api.ipify.org https://ifconfig.me https://icanhazip.com; do
		ip=$(curl -4fsS --max-time 8 "$url" 2>/dev/null | tr -d '[:space:]') || continue
		case "$ip" in
			*[!0-9.]* | "") continue ;;
			*) printf '%s' "$ip"; return 0 ;;
		esac
	done
	return 1
}

choose_domains() {
	# An upgrade keeps what the first install chose.
	if [ -f "$ETC/greyship.env" ]; then
		PLATFORM_DOMAIN=$(sed -n 's/^GREYSHIP_PLATFORM_DOMAIN=//p' "$ETC/greyship.env")
		APPS_DOMAIN=$(sed -n 's/^GREYSHIP_APPS_DOMAIN=//p' "$ETC/greyship.env")
		OWNER_EMAIL=$(sed -n 's/^GREYSHIP_OWNER_EMAIL=//p' "$ETC/greyship.env")
		return 0
	fi
	if [ "$SSLIP" = 1 ]; then
		ip=$(public_ip) || fail "couldn't find this server's public IP address; use --domain instead"
		dashed=$(printf '%s' "$ip" | tr . -)
		PLATFORM_DOMAIN=${PLATFORM_DOMAIN:-greyship-$dashed.sslip.io}
		APPS_DOMAIN=${APPS_DOMAIN:-apps-$dashed.sslip.io}
		say "Using sslip.io names for $ip: $PLATFORM_DOMAIN and *.$APPS_DOMAIN (fine for trying Greyship out; use your own domains for real use)"
	fi
	ask PLATFORM_DOMAIN "Domain for the dashboard (e.g. greyship.example.com)"
	ask APPS_DOMAIN "Domain for your apps (e.g. example.net; apps get <name>.<this domain>)"
	ask OWNER_EMAIL "Your email (the owner account, and Let's Encrypt's contact)"
	[ "$PLATFORM_DOMAIN" != "$APPS_DOMAIN" ] || fail "the dashboard and the apps need different domains (apps would otherwise share a name space with the dashboard)"
	case "$PLATFORM_DOMAIN$APPS_DOMAIN" in
		*[!A-Za-z0-9.-]*) fail "domains may only contain letters, digits, dots and dashes" ;;
	esac
	case "$OWNER_EMAIL" in
		*@*.*) ;;
		*) fail "that doesn't look like an email address: $OWNER_EMAIL" ;;
	esac
	OWNER_EMAIL=$(printf '%s' "$OWNER_EMAIL" | tr '[:upper:]' '[:lower:]')
}

install_docker() {
	step "Docker"
	export DEBIAN_FRONTEND=noninteractive
	apt_get() { apt-get -o DPkg::Lock::Timeout=900 "$@"; }
	apt_get update
	apt_get install -y ca-certificates curl gnupg openssl xz-utils iproute2
	if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1; then
		install -m 0755 -d /etc/apt/keyrings
		curl -fsSL "https://download.docker.com/linux/$ID/gpg" | gpg --dearmor --yes -o /etc/apt/keyrings/docker.gpg
		chmod a+r /etc/apt/keyrings/docker.gpg
		echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/$ID $VERSION_CODENAME stable" >/etc/apt/sources.list.d/docker.list
		apt_get update
		apt_get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
	fi
	systemctl enable --now docker
}

install_gvisor() {
	step "gVisor (the sandbox every app runs in)"
	if ! command -v runsc >/dev/null; then
		curl -fsSL https://gvisor.dev/archive.key | gpg --dearmor --yes -o /usr/share/keyrings/gvisor-archive-keyring.gpg
		echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" >/etc/apt/sources.list.d/gvisor.list
		apt-get -o DPkg::Lock::Timeout=900 update
		apt-get -o DPkg::Lock::Timeout=900 install -y runsc
	fi
	runsc install
	systemctl restart docker
	docker info --format '{{json .Runtimes}}' | grep -q runsc || fail "docker has no runsc runtime after installing gVisor"
}

install_node() {
	step "Node.js (runs the dashboard)"
	if [ -x "$LIB/node/bin/node" ]; then
		say "already installed: $("$LIB/node/bin/node" --version)"
		return 0
	fi
	case "$ARCH" in amd64) nodearch=x64 ;; arm64) nodearch=arm64 ;; esac
	base=https://nodejs.org/dist/latest-v22.x
	tmp=$(mktemp -d)
	curl -fsSL "$base/SHASUMS256.txt" -o "$tmp/SHASUMS256.txt"
	file=$(awk -v a="linux-$nodearch.tar.xz" '$2 ~ a { print $2; exit }' "$tmp/SHASUMS256.txt")
	[ -n "$file" ] || fail "couldn't find a Node.js build for $nodearch"
	curl -fsSL "$base/$file" -o "$tmp/$file"
	(cd "$tmp" && grep " $file\$" SHASUMS256.txt | sha256sum -c - >/dev/null) || fail "the Node.js download doesn't match its checksum"
	install -d -m 0755 "$LIB/node"
	tar -xJf "$tmp/$file" -C "$LIB/node" --strip-components=1
	rm -rf "$tmp"
	say "installed $("$LIB/node/bin/node" --version)"
}

resolve_version() {
	# GREYSHIP_BASE_URL points at a folder holding the release files, for mirrors and for testing a build.
	if [ -n "${GREYSHIP_BASE_URL:-}" ]; then
		BASE=${GREYSHIP_BASE_URL%/}
		[ "$VERSION" != latest ] || VERSION=dev
		return 0
	fi
	if [ "$VERSION" = latest ]; then
		url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") || fail "couldn't reach GitHub"
		VERSION=${url##*/}
		case "$VERSION" in v[0-9]*) ;; *) fail "$REPO has no release yet" ;; esac
	fi
	BASE="https://github.com/$REPO/releases/download/$VERSION"
}

download_release() {
	step "Greyship $VERSION"
	DL=$(mktemp -d)
	trap 'rm -rf "$DL"' EXIT
	for f in "greyship-server_$ARCH.tar.gz" greyship-dashboard.tar.gz checksums.txt; do
		curl -fsSL "$BASE/$f" -o "$DL/$f" || fail "couldn't download $BASE/$f"
	done
	for f in "greyship-server_$ARCH.tar.gz" greyship-dashboard.tar.gz; do
		want=$(awk -v f="$f" '$2 == f { print $1 }' "$DL/checksums.txt")
		[ -n "$want" ] || fail "checksums.txt has no entry for $f"
		got=$(sha256sum "$DL/$f" | awk '{ print $1 }')
		[ "$want" = "$got" ] || fail "checksum mismatch for $f: the download is damaged or was tampered with"
	done
	mkdir "$DL/server" "$DL/dashboard"
	tar -xzf "$DL/greyship-server_$ARCH.tar.gz" -C "$DL/server"
	tar -xzf "$DL/greyship-dashboard.tar.gz" -C "$DL/dashboard"
}

make_user_and_folders() {
	step "user and folders"
	id greyship >/dev/null 2>&1 || useradd --system --home-dir /var/lib/greyship --shell /usr/sbin/nologin greyship
	usermod -aG docker greyship
	install -d -o greyship -g greyship -m 0750 /var/lib/greyship /var/lib/greyship/certs
	install -d -m 0755 "$LIB"
	install -d -m 0700 "$ETC"
}

hex() { openssl rand -hex "$1"; }

write_config() {
	if [ -f "$ETC/greyship.env" ]; then
		step "settings: keeping the ones in $ETC"
		return 0
	fi
	step "secrets and settings"
	# Only for the files written here; everything installed later must stay readable by the greyship user.
	old_umask=$(umask)
	umask 077
	pg=$(hex 24)
	rpc=$(hex 32)
	admin=$(hex 24)
	key_id="GK$(hex 12)"
	key_secret=$(hex 32)
	proxy=$(hex 32)
	node_mb=$((mem_mb * 55 / 100))
	cat >"$ETC/infra.env" <<ENV
POSTGRES_PASSWORD=$pg
GARAGE_KEY_ID=$key_id
GARAGE_KEY_SECRET=$key_secret
ENV
	cat >"$ETC/garage.toml" <<TOML
metadata_dir = "/var/lib/garage/meta"
data_dir = "/var/lib/garage/data"
db_engine = "sqlite"
replication_factor = 1
rpc_bind_addr = "[::]:3901"
rpc_public_addr = "127.0.0.1:3901"
rpc_secret = "$rpc"

[s3_api]
s3_region = "garage"
api_bind_addr = "[::]:3900"
root_domain = ".s3.$PLATFORM_DOMAIN"

[admin]
api_bind_addr = "[::]:3903"
admin_token = "$admin"
TOML
	cat >"$ETC/greyship.env" <<ENV
GREYSHIP_ENV=production
GREYSHIP_DATABASE_URL=postgres://greyship:$pg@127.0.0.1:5432/greyship?sslmode=disable
GREYSHIP_API_ADDR=127.0.0.1:8080
GREYSHIP_DASHBOARD_URL=https://$PLATFORM_DOMAIN
GREYSHIP_PLATFORM_DOMAIN=$PLATFORM_DOMAIN
GREYSHIP_APPS_DOMAIN=$APPS_DOMAIN
GREYSHIP_COOKIE_SECRET=$(hex 32)
GREYSHIP_MASTER_KEY=$(hex 32)
GREYSHIP_PROXY_SECRET=$proxy
GREYSHIP_VAPID_PRIVATE_KEY=$("$DL/server/greyship-api" vapid-key)
# The owner account. While no account exists, only this email may sign up; after that signup is closed.
# To let anyone sign up add GREYSHIP_SIGNUP_OPEN=true, or list emails in GREYSHIP_SIGNUP_ALLOWLIST.
GREYSHIP_OWNER_EMAIL=$OWNER_EMAIL
# Email: optional. Without a provider, login codes are saved as files (run: greyship-admin codes).
# GREYSHIP_RESEND_API_KEY=
# GREYSHIP_MAIL_FROM=Greyship <login@$PLATFORM_DOMAIN>
GREYSHIP_S3_ENDPOINT=https://s3.$PLATFORM_DOMAIN
GREYSHIP_S3_REGION=garage
GREYSHIP_S3_BUCKET=greyship
GREYSHIP_S3_ACCESS_KEY=$key_id
GREYSHIP_S3_SECRET_KEY=$key_secret
GREYSHIP_ACME_EMAIL=$OWNER_EMAIL
GREYSHIP_ROUTER_PLATFORM=@=127.0.0.1:3000,api=127.0.0.1:8080,s3=127.0.0.1:3900
GREYSHIP_ROUTER_CERT_DIR=/var/lib/greyship/certs
GREYSHIP_BUILD_DOCKER_HOST=unix:///var/run/docker.sock
GREYSHIP_BUILD_GOARCH=$ARCH
GREYSHIP_BUILD_RUNNER=gvisor
GREYSHIP_BUILD_SEED_GO=1.27
GREYSHIP_RUNTIME_DOCKER_HOST=unix:///var/run/docker.sock
GREYSHIP_INIT_BINARY=$LIB/greyship-init
GREYSHIP_NODE_NAME=$(hostname -s)
GREYSHIP_REGION=local
GREYSHIP_NODE_MEMORY_MB=$node_mb
ENV
	cat >"$ETC/dashboard.env" <<ENV
NODE_ENV=production
PORT=3000
HOSTNAME=127.0.0.1
GREYSHIP_API_URL=http://127.0.0.1:8080
GREYSHIP_PROXY_SECRET=$proxy
ENV
	chmod 0600 "$ETC/garage.toml" "$ETC/infra.env"
	chown root:greyship "$ETC" "$ETC/greyship.env" "$ETC/dashboard.env"
	chmod 0750 "$ETC"
	chmod 0640 "$ETC/greyship.env" "$ETC/dashboard.env"
	umask "$old_umask"
}

install_files() {
	step "installing"
	for f in "$DL"/server/greyship-*; do
		case "$f" in *.service | *.timer) continue ;; esac
		install -m 0755 "$f" "$LIB/$(basename "$f")"
	done
	install -m 0644 "$DL/server/compose.yml" "$LIB/compose.yml"
	install -m 0644 "$DL/server/modproxy.Dockerfile" "$LIB/modproxy.Dockerfile"
	install -m 0755 "$DL/server/sandbox-init.sh" "$LIB/sandbox-init.sh"
	install -m 0755 "$DL/server/greyship-admin" /usr/local/bin/greyship-admin
	for f in "$DL"/server/systemd/*.service; do
		install -m 0644 "$f" "/etc/systemd/system/$(basename "$f")"
	done
	rm -rf "$LIB/dashboard.new"
	cp -R "$DL/dashboard" "$LIB/dashboard.new"
	rm -rf "$LIB/dashboard"
	mv "$LIB/dashboard.new" "$LIB/dashboard"
	chown -R root:root "$LIB/dashboard"
	chmod -R a+rX "$LIB/dashboard" "$LIB/node"
	printf '%s\n' "$VERSION" >"$LIB/VERSION"
	systemctl daemon-reload
}

firewall() {
	step "firewall: only 22, 80 and 443 from outside"
	if command -v ufw >/dev/null; then
		ufw allow OpenSSH >/dev/null
		ufw allow 80/tcp >/dev/null
		ufw allow 443/tcp >/dev/null
		ufw --force enable >/dev/null
	else
		say "ufw isn't installed; make sure only ports 22, 80 and 443 are open to the internet"
	fi
}

start_everything() {
	step "starting"
	systemctl enable --now greyship-infra.service greyship-firewall.service >/dev/null
	systemctl restart greyship-firewall.service
	# Garage: a one-node layout, the access key, the bucket. Safe to repeat.
	. "$ETC/infra.env"
	g() { docker compose -p greyship -f "$LIB/compose.yml" exec -T -e RUST_LOG=warn garage /garage "$@"; }
	if g layout show | grep -q "Current cluster layout version: 0"; then
		node=$(g node id -q | cut -d@ -f1)
		g layout assign -z local -c 50G "$node" >/dev/null
		g layout apply --version 1 >/dev/null
	fi
	g key info "$GARAGE_KEY_ID" >/dev/null 2>&1 || g key import --yes -n greyship "$GARAGE_KEY_ID" "$GARAGE_KEY_SECRET" >/dev/null
	g bucket info greyship >/dev/null 2>&1 || g bucket create greyship >/dev/null
	g bucket allow --read --write --owner greyship --key "$GARAGE_KEY_ID" >/dev/null

	# greyship.env is read by systemd, not the shell, so migrations run through systemd with the services' settings.
	systemd-run --quiet --wait --pipe --collect -p User=greyship -p EnvironmentFile="$ETC/greyship.env" "$LIB/greyship-migrate"
	for svc in api builder scheduler agent router dashboard; do
		systemctl enable "greyship-$svc.service" >/dev/null 2>&1
		systemctl restart "greyship-$svc.service"
	done
	i=0
	while [ $i -lt 40 ]; do
		if curl -fs -o /dev/null http://127.0.0.1:8080/healthz && curl -fs -o /dev/null http://127.0.0.1:3000/signin; then
			return 0
		fi
		i=$((i + 1))
		sleep 1
	done
	for svc in api builder scheduler agent router dashboard; do
		systemctl is-active --quiet "greyship-$svc.service" || { say "greyship-$svc failed:"; journalctl -u "greyship-$svc" -n 15 --no-pager; }
	done
	fail "Greyship didn't come up; see the messages above (greyship-admin logs <service>)"
}

finish() {
	ip=$(public_ip 2>/dev/null || echo "this server's IP")
	resolved=$(getent hosts "$PLATFORM_DOMAIN" 2>/dev/null | awk '{ print $1; exit }' || true)
	cat <<MSG

✓ Greyship $VERSION is running.

DNS: point these records at $ip (skip any that already exist):
    A   $PLATFORM_DOMAIN       →  $ip
    A   *.$PLATFORM_DOMAIN     →  $ip    (s3.… for uploads, api.… for the API)
    A   *.$APPS_DOMAIN         →  $ip    (your apps live here)
MSG
	if [ -n "$resolved" ] && [ "$resolved" != "$ip" ]; then
		say "    (note: $PLATFORM_DOMAIN points at $resolved right now, not at this server)"
	elif [ -z "$resolved" ]; then
		say "    ($PLATFORM_DOMAIN doesn't resolve yet; HTTPS works as soon as it does)"
	fi
	cat <<MSG

Then open https://$PLATFORM_DOMAIN and sign up with $OWNER_EMAIL: that account is the owner.
No email provider is set, so the login code is saved on this server: run   greyship-admin codes
From your computer:   curl -fsSL https://greyship.tech/install | sh   then   grey login https://$PLATFORM_DOMAIN

Looking after it: greyship-admin status | logs | codes | upgrade     Settings: $ETC/greyship.env
Docs: https://docs.greyship.tech/self-hosting
MSG
}

main() {
	parse_args "$@"
	check_system
	choose_domains
	install_docker
	install_gvisor
	install_node
	resolve_version
	download_release
	make_user_and_folders
	write_config
	install_files
	firewall
	start_everything
	finish
}

main "$@"
