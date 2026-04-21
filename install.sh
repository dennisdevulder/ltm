#!/bin/sh
# Install script for ltm — portable understanding for AI work sessions.
#
# Usage:
#   curl -fsSL https://ltm-cli.dev/install | sh
#
# Or pin a version:
#   curl -fsSL https://ltm-cli.dev/install | LTM_VERSION=v0.1.0 sh
#
# Install system-wide (requires sudo) — default is user-local (~/.local/bin):
#   curl -fsSL https://ltm-cli.dev/install | LTM_INSTALL_DIR=/usr/local/bin sh

set -eu

REPO="dennisdevulder/ltm"
# Default to a user-local path so `curl | sh` doesn't need sudo. Users who want
# a system-wide install can opt in explicitly via LTM_INSTALL_DIR.
INSTALL_DIR="${LTM_INSTALL_DIR:-$HOME/.local/bin}"

log()  { printf '==> %s\n' "$*"; }
fail() { printf 'error: %s\n' "$*" >&2; exit 1; }

require() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required tool: $1"
}

require curl
require tar
require uname
require mktemp

# ---- detect platform ----

os_raw=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os_raw" in
  linux)  os="linux"  ;;
  darwin) os="darwin" ;;
  *) fail "unsupported OS: $os_raw" ;;
esac

arch_raw=$(uname -m)
case "$arch_raw" in
  x86_64|amd64)  arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) fail "unsupported arch: $arch_raw" ;;
esac

# ---- resolve version ----

if [ -n "${LTM_VERSION:-}" ]; then
  version="$LTM_VERSION"
else
  log "resolving latest release"
  version=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' \
    | head -n1 \
    | sed -E 's/.*"([^"]+)"$/\1/')
fi

[ -n "$version" ] || fail "could not resolve release version"
[ "${version#v}" != "$version" ] || version="v$version"

# ---- download and install ----

stripped="${version#v}"
archive="ltm_${stripped}_${os}_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/download/${version}/${archive}"

tmpdir=$(mktemp -d 2>/dev/null || mktemp -d -t ltm)
trap 'rm -rf "$tmpdir"' EXIT

log "downloading $archive"
curl -fsSL -o "$tmpdir/$archive" "$url" \
  || fail "download failed: $url"

log "extracting"
tar -xzf "$tmpdir/$archive" -C "$tmpdir"

[ -f "$tmpdir/ltm" ] || fail "archive did not contain an 'ltm' binary"
chmod +x "$tmpdir/ltm"

if [ ! -d "$INSTALL_DIR" ]; then
  mkdir -p "$INSTALL_DIR" 2>/dev/null \
    || sudo mkdir -p "$INSTALL_DIR"
fi

if [ -w "$INSTALL_DIR" ]; then
  mv "$tmpdir/ltm" "$INSTALL_DIR/ltm"
else
  log "$INSTALL_DIR is not writable — using sudo"
  sudo mv "$tmpdir/ltm" "$INSTALL_DIR/ltm"
fi

# ---- verify & path check ----

# Always report the newly installed binary, not whatever `ltm` in PATH resolves to —
# users commonly have an older ltm elsewhere (e.g. /usr/local/bin from a prior
# install, or $GOPATH/bin from `go build`) that would otherwise shadow the new one.
log "installed: $("$INSTALL_DIR/ltm" --version)"

# Warn if the shell will resolve `ltm` to a different binary than the one we just installed.
active=$(command -v ltm 2>/dev/null || true)
if [ -n "$active" ] && [ "$active" != "$INSTALL_DIR/ltm" ]; then
  printf '\n'
  printf 'note: "ltm" in your PATH still resolves to %s.\n' "$active"
  printf 'remove that binary or reorder PATH so %s comes first.\n' "$INSTALL_DIR"
fi

# Warn if the install dir isn't in PATH at all.
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    printf '\n'
    printf 'note: %s is not on your PATH.\n' "$INSTALL_DIR"
    printf 'add this to your shell rc:\n\n'
    # $PATH is intentionally literal — the user's shell expands it when they source the rc.
    # shellcheck disable=SC2016
    printf '  export PATH="%s:$PATH"\n\n' "$INSTALL_DIR"
    ;;
esac

cat <<'NEXT'

Next steps:
  ltm --help                  see all commands
  ltm server init             initialize a self-hosted server
  ltm auth <url> <token>      authenticate a client

Spec:  https://github.com/dennisdevulder/ltm/blob/main/SPEC.md
Home:  https://ltm-cli.dev
NEXT
