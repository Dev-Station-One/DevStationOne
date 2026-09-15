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

mkdir -p "$WORK/pkg"
tar -xzf "$WORK/bundle.tar.gz" -C "$WORK/pkg"
[ -x "$WORK/pkg/devstationone-server" ] && [ -x "$WORK/pkg/devstationone-runtime" ] && [ -x "$WORK/pkg/devstationone-admin" ] ||
  die "downloaded tarball does not contain all binaries"
[ -d "$WORK/pkg/web/static" ] && [ -d "$WORK/pkg/assets" ] ||
  die "downloaded tarball does not contain web/static or assets (static assets are not embedded in the binary)"

install_server() {
  if [ -z "$INSTALL_ROOT" ] && [ "$(id -u)" -ne 0 ]; then
    die "--server installs system-wide and must run as root (sudo bash -s -- --server)"
  fi
  command -v openssl >/dev/null 2>&1 || die "openssl is required (to generate DEVSTATIONONE_DB_ENCRYPTION_KEY)"

  install -Dm755 "$WORK/pkg/devstationone-server" "$INSTALL_ROOT/usr/local/bin/devstationone-server"
  install -Dm755 "$WORK/pkg/devstationone-admin" "$INSTALL_ROOT/usr/local/bin/devstationone-admin"

  # Static assets are served from disk at request time (http.Dir), not
  # embedded in the binary — they must live where the unit's
  # WorkingDirectory below points. Replaced wholesale on every
  # install/upgrade so a file removed upstream doesn't linger.
  local share_dir="$INSTALL_ROOT/usr/local/share/devstationone"
  rm -rf "$share_dir/web" "$share_dir/assets"
  mkdir -p "$share_dir"
  cp -r "$WORK/pkg/web" "$share_dir/web"
  cp -r "$WORK/pkg/assets" "$share_dir/assets"
  chmod -R a+rX "$share_dir"

  # Run as a dedicated, unprivileged system account rather than root
  # (docs/SECURITY.md "least privilege"/"defense in depth" — the same
  # posture already applied to the Development Runtime, which never needs
  # root either). Skipped under INSTALL_ROOT: that's a test/staging
  # sandbox, not a real host to add a system user to.
  if [ -z "$INSTALL_ROOT" ] && ! id -u devstationone >/dev/null 2>&1; then
    useradd --system --no-create-home --shell /usr/sbin/nologin devstationone
  fi

  local env_file="$INSTALL_ROOT/etc/devstationone/server.env"
  mkdir -p "$(dirname "$env_file")"
  if [ ! -f "$env_file" ]; then
    # Generated fresh per install, not a placeholder: DEVSTATIONONE_DB_ENCRYPTION_KEY
    # is required (encrypts stored database connection credentials, ADR-021) and a
    # shared/example value would be actively dangerous rather than merely wrong.
    # Back it up alongside the database (docs/BACKUPS.md) — never regenerated on
    # an existing install (this whole block is skipped once the file exists).
    local db_key
    db_key="$(openssl rand -base64 32)"
    cat > "$env_file" <<EOF
# DevStationOne Control Server configuration.
# Edit, then: sudo systemctl enable --now devstationone-server
DEVSTATIONONE_ENV=production
DEVSTATIONONE_LOG_LEVEL=info
DEVSTATIONONE_SERVER_ADDR=:8080
DEVSTATIONONE_DATABASE_URL=postgres://devstation:change-me@localhost:5432/devstation?sslmode=disable
DEVSTATIONONE_DB_ENCRYPTION_KEY=$db_key
EOF
    chmod 600 "$env_file"
    [ -z "$INSTALL_ROOT" ] && chown devstationone:devstationone "$env_file"
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
User=devstationone
Group=devstationone
WorkingDirectory=/usr/local/share/devstationone
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
    info "then seed an administrator (as root, so it can read $env_file):"
    info "  set -a; source $env_file; set +a; echo 'your-password' | devstationone-admin --email you@example.com"
  fi
}

install_runtime() {
  local bin="$INSTALL_ROOT$HOME/.local/bin/devstationone-runtime"
  install -Dm755 "$WORK/pkg/devstationone-runtime" "$bin"
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
