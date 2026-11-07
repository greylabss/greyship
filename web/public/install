#!/bin/sh
# Installs grey, the Greyship CLI, on macOS or Linux:
#
#   curl -fsSL https://greyship.tech/cli/install | sh
#
# It downloads the latest release from https://github.com/greylabss/greyship, checks its sha256 against the
# release's checksums.txt, and puts grey (plus the aliases gray and greyship) in /usr/local/bin, or in
# ~/.local/bin when /usr/local/bin isn't writable. It never uses sudo.
#
# GREY_VERSION=v0.1.0 picks a release; GREY_INSTALL_DIR picks the folder; GREY_REPO another repository.
# GREY_SERVER=https://greyship.example.com points grey at a self-hosted Greyship (every Greyship serves this same
# script at /install and prints the command with its own address).
set -eu

# Everything runs from main, so a download cut off halfway never runs half a script.
main() {
  repo="${GREY_REPO:-greylabss/greyship}"
  version="${GREY_VERSION:-latest}"

  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) fail "grey runs on macOS and Linux. On Windows, install it inside WSL." ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) fail "grey isn't built for $(uname -m) yet." ;;
  esac

  if [ "$version" = latest ]; then
    base="https://github.com/$repo/releases/latest/download"
  else
    base="https://github.com/$repo/releases/download/$version"
  fi
  file="grey_${os}_${arch}.tar.gz"

  need curl
  need tar
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT INT TERM

  say "Downloading grey ($os/$arch)"
  curl -fsSL "$base/$file" -o "$tmp/$file" || fail "couldn't download $base/$file"
  curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" || fail "couldn't download the checksums"

  want="$(awk -v f="$file" '$2 == f { print $1 }' "$tmp/checksums.txt")"
  [ -n "$want" ] || fail "the release has no checksum for $file"
  got="$(sha256 "$tmp/$file")"
  [ "$want" = "$got" ] || fail "checksum mismatch for $file: the download is damaged or was tampered with"

  tar -xzf "$tmp/$file" -C "$tmp" grey

  dir="${GREY_INSTALL_DIR:-}"
  if [ -z "$dir" ]; then
    if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
      dir=/usr/local/bin
    else
      dir="$HOME/.local/bin"
    fi
  fi
  mkdir -p "$dir"
  cp "$tmp/grey" "$dir/grey.new"
  chmod 0755 "$dir/grey.new"
  mv "$dir/grey.new" "$dir/grey"
  ln -sf grey "$dir/gray"
  ln -sf grey "$dir/greyship"

  say "Installed $("$dir/grey" --version 2>/dev/null || echo grey) in $dir"
  case ":$PATH:" in
    *":$dir:"*) ;;
    *)
      say ""
      say "$dir isn't on your PATH yet. Add this line to your shell profile (~/.zshrc or ~/.bashrc):"
      say "  export PATH=\"$dir:\$PATH\""
      ;;
  esac
  say ""
  if [ -n "${GREY_SERVER:-}" ]; then
    "$dir/grey" server "$GREY_SERVER"
    say "Then grey deploy in your Go project."
  else
    say "Next: grey login, then grey deploy in your Go project."
  fi
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{ print $1 }'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{ print $1 }'
  else
    fail "need sha256sum or shasum to check the download"
  fi
}

need() { command -v "$1" >/dev/null 2>&1 || fail "need $1 to install grey"; }
say() { printf '%s\n' "$1"; }
fail() {
  printf 'grey install: %s\n' "$1" >&2
  exit 1
}

main "$@"
