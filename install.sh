#!/usr/bin/env bash
# DevStationOne installer.
#
#   curl -fsSL https://raw.githubusercontent.com/Dev-Station-One/DevStationOne/main/install.sh | sudo bash -s -- --server
#   curl -fsSL https://raw.githubusercontent.com/Dev-Station-One/DevStationOne/main/install.sh | bash -s -- --runtime
#
# Downloads release binaries from GitHub Releases and installs:
#   --server   /usr/local/bin binary, /etc/devstationone/server.env (first
#              run only), systemd system unit. Must run as root.
#   --runtime  ~/.local/bin binary, ~/.config/devstationone/runtime.env
#              (first run only), systemd user unit. Runs as your user.
#
# Env overrides (pinning, airgap, tests):
#   DEVSTATIONONE_VERSION      release version without "v" (default: latest)
#   DEVSTATIONONE_TARBALL_URL  fetch the tarball from this URL instead
#   DEVSTATIONONE_INSTALL_ROOT prepend this path to every install location
set -euo pipefail

REPO="Dev-Station-One/DevStationOne"
VERSION="${DEVSTATIONONE_VERSION:-}"
# ponytail: INSTALL_ROOT/TARBALL_URL exist so CI can smoke-test this script
# and airgapped hosts can mirror; delete if they ever stop earning their keep.
INSTALL_ROOT="${DEVSTATIONONE_INSTALL_ROOT:-}"
TARBALL_URL="${DEVSTATIONONE_TARBALL_URL:-}"

die() { echo "install.sh: $*" >&2; exit 1; }
info() { echo "install.sh: $*"; }

MODE="${1:-}"
case "$MODE" in
  --server) ;;
  --runtime) ;;
  *) die "usage: install.sh --server | --runtime" ;;
esac

for tool in curl tar sha256sum; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done

case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
  *) die "unsupported architecture: $(uname -m) (amd64 and arm64 are built)" ;;
esac

if [ -z "$VERSION" ] && [ -z "$TARBALL_URL" ]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
    grep -m1 '"tag_name"' | sed 's/.*"v\([^"]*\)".*/\1/')" ||
    die "could not determine the latest release; set DEVSTATIONONE_VERSION (e.g. 0.1.0)"
  [ -n "$VERSION" ] || die "could not determine the latest release; set DEVSTATIONONE_VERSION (e.g. 0.1.0)"
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

if [ -n "$TARBALL_URL" ]; then
  info "fetching tarball from $TARBALL_URL"
  curl -fsSL "$TARBALL_URL" -o "$WORK/bundle.tar.gz"
else
  BASE="https://github.com/$REPO/releases/download/v$VERSION"
  TARBALL="devstationone_${VERSION}_linux_${ARCH}.tar.gz"
  info "downloading $TARBALL"
  curl -fsSL "$BASE/$TARBALL" -o "$WORK/bundle.tar.gz"
  curl -fsSL "$BASE/checksums.txt" -o "$WORK/checksums.txt"
  ( cd "$WORK" && grep " $TARBALL\$" checksums.txt | sha256sum -c - >/dev/null ) ||
    die "checksum verification failed for $TARBALL"
  info "checksum verified"
fi

mkdir -p "$WORK/bin"
tar -xzf "$WORK/bundle.tar.gz" -C "$WORK/bin"
[ -x "$WORK/bin/devstationone-server" ] && [ -x "$WORK/bin/devstationone-runtime" ] && [ -x "$WORK/bin/devstationone-admin" ] ||
  die "downloaded tarball does not contain all binaries"

install_server() {
  if [ -z "$INSTALL_ROOT" ] && [ "$(id -u)" -ne 0 ]; then
    die "--server installs system-wide and must run as root (sudo bash -s -- --server)"
  fi

  install -Dm755 "$WORK/bin/devstationone-server" "$INSTALL_ROOT/usr/local/bin/devstationone-server"
  install -Dm755 "$WORK/bin/devstationone-admin" "$INSTALL_ROOT/usr/local/bin/devstationone-admin"

  local env_file="$INSTALL_ROOT/etc/devstationone/server.env"
  mkdir -p "$(dirname "$env_file")"
  if [ ! -f "$env_file" ]; then
    cat > "$env_file" <<'EOF'
# DevStationOne Control Server configuration.
# Edit, then: sudo systemctl enable --now devstationone-server
DEVSTATIONONE_ENV=production
DEVSTATIONONE_LOG_LEVEL=info
DEVSTATIONONE_SERVER_ADDR=:8080
DEVSTATIONONE_DATABASE_URL=postgres://devstation:change-me@localhost:5432/devstation?sslmode=disable
EOF
    chmod 600 "$env_file"
    info "wrote $env_file — edit it before starting the service"
  else
    info "keeping existing $env_file"
  fi

  local unit="$INSTALL_ROOT/etc/systemd/system/devstationone-server.service"
  mkdir -p "$(dirname "$unit")"
  cat > "$unit" <<'EOF'
[Unit]
Description=DevStationOne Control Server
Wants=network-online.target
After=network-online.target

[Service]
EnvironmentFile=/etc/devstationone/server.env
ExecStart=/usr/local/bin/devstationone-server
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

  if [ -n "$INSTALL_ROOT" ]; then
    info "(install root: $INSTALL_ROOT)"
  else
    systemctl daemon-reload
    info "next steps: sudoedit $env_file && sudo systemctl enable --now devstationone-server"
    info "then seed an administrator: echo 'your-password' | devstationone-admin --email you@example.com"
  fi
}

install_runtime() {
  local bin="$INSTALL_ROOT$HOME/.local/bin/devstationone-runtime"
  install -Dm755 "$WORK/bin/devstationone-runtime" "$bin"
  case ":$PATH:" in
    *":${INSTALL_ROOT}${HOME}/.local/bin:"*) ;;
    *) info "note: ${INSTALL_ROOT}${HOME}/.local/bin is not on your PATH" ;;
  esac

  local env_file="$INSTALL_ROOT$HOME/.config/devstationone/runtime.env"
  mkdir -p "$(dirname "$env_file")"
  if [ ! -f "$env_file" ]; then
    cat > "$env_file" <<'EOF'
# DevStationOne Development Runtime configuration.
# Server URL and enrollment token come from the Control Server web UI:
# Runtimes -> Add runtime. The token is shown once.
DEVSTATIONONE_SERVER_URL=wss://your-server.example.com
DEVSTATIONONE_RUNTIME_TOKEN=
DEVSTATIONONE_ENV=production
EOF
    chmod 600 "$env_file"
    info "wrote $env_file — add your server URL and enrollment token"
  else
    info "keeping existing $env_file"
  fi

  local unit="$INSTALL_ROOT$HOME/.config/systemd/user/devstationone-runtime.service"
  mkdir -p "$(dirname "$unit")"
  cat > "$unit" <<'EOF'
[Unit]
Description=DevStationOne Development Runtime
After=network-online.target

[Service]
EnvironmentFile=%h/.config/devstationone/runtime.env
ExecStart=%h/.local/bin/devstationone-runtime
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF

  if [ -n "$INSTALL_ROOT" ]; then
    info "(install root: $INSTALL_ROOT)"
  else
    systemctl --user daemon-reload 2>/dev/null ||
      info "systemd --user not reachable right now; the unit is installed and loads on next login"
    info "next steps: edit $env_file, then systemctl --user enable --now devstationone-runtime && loginctl enable-linger"
  fi
}

if [ "$MODE" = "--server" ]; then
  install_server
else
  install_runtime
fi

info "done"
