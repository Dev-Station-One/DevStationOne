#!/usr/bin/env bash
# Smoke-tests install.sh's --server and --runtime paths against a locally
# built release tarball, sandboxed under DEVSTATIONONE_INSTALL_ROOT — the
# seam install.sh's own header comment says exists "so CI can smoke-test
# this script" (Slice 13). Not covered: the real-GitHub download+checksum
# branch (DEVSTATIONONE_TARBALL_URL unset) — that needs a real release to
# fetch from, so it's exercised by actual installs instead.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
assert_file() { [ -f "$1" ] || fail "expected file missing: $1"; }
assert_contains() { grep -qF "$2" "$1" || fail "$1 does not contain: $2"; }

echo "building binaries..."
CGO_ENABLED=0 go build -o "$WORK/build/" \
  "$ROOT_DIR/cmd/devstationone-server" "$ROOT_DIR/cmd/devstationone-runtime" "$ROOT_DIR/cmd/devstationone-admin"

mkdir -p "$WORK/dist"
TARBALL="$WORK/dist/devstationone_0.0.0-test_linux_amd64.tar.gz"
tar -czf "$TARBALL" -C "$WORK/build" \
  devstationone-server devstationone-runtime devstationone-admin \
  -C "$ROOT_DIR" web/static assets

echo "--- install.sh --server ---"
SERVER_ROOT="$WORK/server-root"
DEVSTATIONONE_INSTALL_ROOT="$SERVER_ROOT" DEVSTATIONONE_TARBALL_URL="file://$TARBALL" \
  bash "$ROOT_DIR/install.sh" --server

assert_file "$SERVER_ROOT/usr/local/bin/devstationone-server"
assert_file "$SERVER_ROOT/usr/local/bin/devstationone-admin"
assert_file "$SERVER_ROOT/etc/devstationone/server.env"
assert_file "$SERVER_ROOT/etc/systemd/system/devstationone-server.service"
assert_contains "$SERVER_ROOT/etc/systemd/system/devstationone-server.service" "ExecStart=/usr/local/bin/devstationone-server"
assert_contains "$SERVER_ROOT/etc/systemd/system/devstationone-server.service" "User=devstationone"
assert_contains "$SERVER_ROOT/etc/systemd/system/devstationone-server.service" "WorkingDirectory=/usr/local/share/devstationone"
grep -q "^DEVSTATIONONE_DB_ENCRYPTION_KEY=.\+" "$SERVER_ROOT/etc/devstationone/server.env" ||
  fail "server.env missing a generated DEVSTATIONONE_DB_ENCRYPTION_KEY"

echo "static assets land where WorkingDirectory expects them..."
assert_file "$SERVER_ROOT/usr/local/share/devstationone/web/static/login.html"
assert_file "$SERVER_ROOT/usr/local/share/devstationone/assets/logo/devstationone-top-logo.png"

echo "reinstall keeps an edited env file, and replaces stale static assets..."
echo "CUSTOM_MARKER=1" >>"$SERVER_ROOT/etc/devstationone/server.env"
STALE="$SERVER_ROOT/usr/local/share/devstationone/web/static/stale-from-old-release.html"
touch "$STALE"
DEVSTATIONONE_INSTALL_ROOT="$SERVER_ROOT" DEVSTATIONONE_TARBALL_URL="file://$TARBALL" \
  bash "$ROOT_DIR/install.sh" --server
assert_contains "$SERVER_ROOT/etc/devstationone/server.env" "CUSTOM_MARKER=1"
[ ! -f "$STALE" ] || fail "reinstall must not leave stale static assets from a prior release: $STALE"

echo "--- install.sh --runtime ---"
RUNTIME_ROOT="$WORK/runtime-root"
DEVSTATIONONE_INSTALL_ROOT="$RUNTIME_ROOT" DEVSTATIONONE_TARBALL_URL="file://$TARBALL" \
  bash "$ROOT_DIR/install.sh" --runtime

assert_file "$RUNTIME_ROOT$HOME/.local/bin/devstationone-runtime"
assert_file "$RUNTIME_ROOT$HOME/.config/devstationone/runtime.env"
assert_file "$RUNTIME_ROOT$HOME/.config/systemd/user/devstationone-runtime.service"
[ ! -f "$RUNTIME_ROOT$HOME/.local/bin/devstationone-server" ] || fail "runtime install must not ship the server binary"

echo "PASS: install.sh --server / --runtime"
