#!/usr/bin/env bash
#
# GoPanel VPS Installer — aaPanel style: one command, clone, build, run.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/yazvip/GoPanel/main/scripts/install.sh | sudo bash -
#   GOPANEL_ADMIN_PASSWORD="password-panjang-anda" sudo bash scripts/install.sh
#
# Environment overrides:
#   GOPANEL_ADMIN_PASSWORD   Password admin pertama (default: acak 16 karakter)
#   GOPANEL_INSTALL_DIR      Direktori instalasi (default: /opt/gopanel)
#   GOPANEL_CONFIG           Path config (default: /etc/gopanel/config.json)
#   GOPANEL_PORT             Port panel (default: 7800)
#   GOPANEL_REPO_URL         URL repo git (default: https://github.com/yazvip/GoPanel.git)
#
# Defaults:
#   - Install dir: /opt/gopanel
#   - Config: /etc/gopanel/config.json
#   - Listen: 0.0.0.0:7800 (HTTP). Pasang TLS lewat Pengaturan / reverse proxy.
#   - Admin pertama: user "admin"; password dari GOPANEL_ADMIN_PASSWORD atau acak.

set -euo pipefail

GO_VERSION="${GOPANEL_GO_VERSION:-1.26.3}"
INSTALL_DIR="${GOPANEL_INSTALL_DIR:-/opt/gopanel}"
CONFIG_PATH="${GOPANEL_CONFIG:-/etc/gopanel/config.json}"
PANEL_PORT="${GOPANEL_PORT:-7800}"
REPO_URL="${GOPANEL_REPO_URL:-${1:-https://github.com/yazvip/GoPanel.git}}"

###############################################################################
# Output helpers
###############################################################################
log()  { printf '\n[%s] %s\n' "$(date '+%H:%M:%S')" "$*"; }
ok()   { printf '\n\033[32m✅ %s\033[0m\n' "$*"; }
warn() { printf '\n\033[33m⚠️  %s\033[0m\n' "$*"; }
err()  { printf '\033[31m❌ %s\033[0m\n' "$*" >&2; }
die()  { err "$*"; exit 1; }

confirm_root() {
  [ "$(id -u)" -eq 0 ] || die "Installer harus dijalankan sebagai root (sudo)."
}

detect_os() {
  if [ -f /etc/os-release ]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    OS_NAME="${PRETTY_NAME:-${NAME:-Unknown}}"
    OS_ID="${ID:-unknown}"
  else
    OS_NAME="Unknown"; OS_ID="unknown"
  fi
}

have_systemd() {
  command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]
}

download() { # url dest
  local url="$1" dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 --connect-timeout 20 -o "$dest" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$dest" "$url"
  else
    die "curl atau wget diperlukan untuk mengunduh $url"
  fi
}

http_get() { # url (silent, no output on failure)
  if command -v curl >/dev/null 2>&1; then
    curl -fsS --max-time 5 "$1" >/dev/null 2>&1
  elif command -v wget >/dev/null 2>&1; then
    wget -q --timeout=5 -O /dev/null "$1" >/dev/null 2>&1
  else
    return 1
  fi
}

ensure_apt_packages() {
  local missing=()
  local pkg
  for pkg in "$@"; do
    command -v "$pkg" >/dev/null 2>&1 || missing+=("$pkg")
  done
  [ ${#missing[@]} -eq 0 ] && return 0
  case "$OS_ID" in
    ubuntu|debian)
      log "Memasang paket: ${missing[*]}"
      export DEBIAN_FRONTEND=noninteractive
      apt-get update -qq || die "apt-get update gagal"
      apt-get install -y -qq "${missing[@]}" || die "Gagal memasang paket: ${missing[*]}"
      ;;
    *)
      die "Paket berikut wajib terpasang: ${missing[*]} (OS: $OS_NAME)"
      ;;
  esac
}

go_version_ok() {
  local v major minor
  v="$(go env GOVERSION 2>/dev/null || true)"
  v="${v#go}"
  [ -n "$v" ] || return 1
  # shellcheck disable=SC2086
  set -- $(printf '%s' "${v%%-*}" | tr '.' ' ')
  major="${1:-0}"; minor="${2:-0}"
  [ "$major" -gt 1 ] && return 0
  [ "$major" -eq 1 ] && [ "$minor" -ge 26 ] && return 0
  return 1
}

ensure_go() {
  if command -v go >/dev/null 2>&1; then
    if go_version_ok; then
      log "Go sudah terpasang: $(go version)"
      return 0
    fi
    warn "Versi Go terpasang terlalu lama ($(go version)); memasang Go ${GO_VERSION}..."
  else
    log "Go belum terdeteksi, mengunduh Go ${GO_VERSION}..."
  fi

  local arch tar_tmp
  case "$(uname -m)" in
    x86_64|amd64)   arch="amd64" ;;
    aarch64|arm64)  arch="arm64" ;;
    armv7l|armv6l)  arch="armv6l" ;;
    *)              die "Arsitektur $(uname -m) tidak didukung untuk unduhan Go otomatis." ;;
  esac

  tar_tmp="$(mktemp)"
  download "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" "$tar_tmp" \
    || die "Gagal mengunduh Go dari https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "$tar_tmp"
  rm -f "$tar_tmp"
  export PATH="/usr/local/go/bin:$PATH"
  command -v go >/dev/null 2>&1 || die "Instalasi Go gagal: binary go tidak ditemukan di /usr/local/go/bin"
  log "Go dipasang: $(go version)"
}

generate_password() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 8
  elif command -v tr >/dev/null 2>&1; then
    tr -dc 'a-f0-9' < /dev/urandom | head -c 16
  else
    die "Tidak dapat menghasilkan password acak (openssl/tr tidak tersedia)."
  fi
}

write_config() {
  [ -f "$CONFIG_PATH" ] && { log "Config sudah ada, mempertahankan $CONFIG_PATH"; return 0; }
  mkdir -p "$(dirname "$CONFIG_PATH")"
  umask 077
  cat > "$CONFIG_PATH" <<EOF
{
  "listen": "0.0.0.0:${PANEL_PORT}",
  "data_dir": "/var/lib/gopanel",
  "db_path": "/var/lib/gopanel/panel.db",
  "log_level": "info",
  "panel_url": "",
  "allowed_roots": [
    { "token": "var-www", "label": "Web root", "path": "/var/www" },
    { "token": "home", "label": "Home", "path": "/home" }
  ],
  "upload_max_bytes": 2147483648,
  "session_idle_hours": 12,
  "session_absolute_hours": 168,
  "acme_email": "",
  "acme_staging": false,
  "acme_webroot": "/var/lib/gopanel/acme",
  "ssl_dir": "/etc/gopanel/ssl",
  "backup_dir": "/var/backups/gopanel",
  "log_dir": "/var/log/gopanel",
  "cron_file": "/etc/cron.d/gopanel",
  "site_root_base": "/var/www",
  "nginx_sites_available": "/etc/nginx/sites-available",
  "nginx_sites_enabled": "/etc/nginx/sites-enabled",
  "nginx_conf": "/etc/nginx/nginx.conf",
  "nginx_log_dir": "/var/log/nginx",
  "php_conf_dir": "/etc/php",
  "mysql_admin_user": "root",
  "mysql_admin_password": "",
  "mysql_socket": "",
  "sshd_port": 22,
  "terminal_enabled": true,
  "behind_proxy": false,
  "trusted_proxies": null,
  "tls": {
    "cert_file": "",
    "key_file": ""
  },
  "secret_key_path": "/var/lib/gopanel/secret.key"
}
EOF
  umask 022
  log "Config ditulis ke $CONFIG_PATH (listen 0.0.0.0:${PANEL_PORT})"
}

write_systemd_unit() {
  local unit_path="/etc/systemd/system/gopanel.service"
  cat > "$unit_path" <<EOF
[Unit]
Description=GoPanel VPS Control Panel
After=network.target

[Service]
Type=simple
EnvironmentFile=-/etc/default/gopanel
ExecStart=${INSTALL_DIR}/gopanel -config ${CONFIG_PATH}
Restart=on-failure
RestartSec=5
WorkingDirectory=${INSTALL_DIR}
User=root
Group=root
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
  log "Unit systemd ditulis ke $unit_path"
}

stop_running_panel() {
  if have_systemd && systemctl list-unit-files gopanel.service >/dev/null 2>&1; then
    systemctl stop gopanel.service >/dev/null 2>&1 || true
  fi
  if [ -f /var/run/gopanel.pid ]; then
    local old_pid
    old_pid="$(cat /var/run/gopanel.pid 2>/dev/null || true)"
    if [ -n "$old_pid" ] && kill -0 "$old_pid" 2>/dev/null; then
      kill "$old_pid" 2>/dev/null || true
      sleep 1
    fi
    rm -f /var/run/gopanel.pid
  fi
  # Tunggu port benar-benar bebas (maks 10 detik).
  local i
  for i in $(seq 1 10); do
    if ! (exec 3<>"/dev/tcp/127.0.0.1/${PANEL_PORT}") 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

bootstrap_admin() {
  local admin_pass="$1"
  local bootstrap_log pid ready found_user found_pass

  bootstrap_log="$(mktemp)"
  GOPANEL_ADMIN_PASSWORD="$admin_pass" \
    "$INSTALL_DIR/gopanel" -config "$CONFIG_PATH" >"$bootstrap_log" 2>&1 &
  pid=$!

  log "Menunggu panel menyala (maksimal 30 detik)..."
  ready=0
  local i
  for i in $(seq 1 30); do
    if kill -0 "$pid" 2>/dev/null; then
      if grep -q "GoPanel first administrator" "$bootstrap_log" 2>/dev/null \
         || grep -q "gopanel listening" "$bootstrap_log" 2>/dev/null; then
        ready=1
        break
      fi
      sleep 1
    else
      wait "$pid" 2>/dev/null || true
      break
    fi
  done

  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi

  if [ "$ready" -ne 1 ]; then
    err "Panel gagal start saat bootstrap. Cuplikan log:"
    sed -n '1,40p' "$bootstrap_log" >&2 || true
    rm -f "$bootstrap_log"
    return 1
  fi

  # Ambil kredensial bila admin baru dibuat. Baris dari main.go:
  #   GoPanel first administrator
  #   username: admin
  #   password: xxxx
  found_user="admin"
  found_pass=""
  found_pass="$(sed -n 's/^[[:space:]]*password: \([^[:space:]]*\).*/\1/p' "$bootstrap_log" | head -1)"
  local user_line
  user_line="$(sed -n 's/^[[:space:]]*username: \([^[:space:]]*\).*/\1/p' "$bootstrap_log" | head -1)"
  [ -n "$user_line" ] && found_user="$user_line"
  rm -f "$bootstrap_log"

  BOOTSTRAP_USER="$found_user"
  BOOTSTRAP_PASS="$found_pass"
  return 0
}

start_panel_service() {
  if have_systemd; then
    systemctl daemon-reload
    systemctl enable gopanel.service >/dev/null 2>&1 || warn "systemctl enable gagal"
    systemctl restart gopanel.service || die "systemctl start gopanel gagal (lihat: journalctl -u gopanel -n 50)"
  else
    warn "systemd tidak tersedia; menjalankan panel dengan nohup."
    mkdir -p /var/log
    nohup "$INSTALL_DIR/gopanel" -config "$CONFIG_PATH" >>/var/log/gopanel.log 2>&1 &
    echo $! >/var/run/gopanel.pid
  fi
}

wait_health() {
  local i
  for i in $(seq 1 20); do
    if http_get "http://127.0.0.1:${PANEL_PORT}/api/v1/healthz"; then
      return 0
    fi
    sleep 1
  done
  return 1
}

detect_public_ip() {
  local ip=""
  if command -v hostname >/dev/null 2>&1; then
    ip="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
  fi
  if [ -z "$ip" ] && command -v ip >/dev/null 2>&1; then
    ip="$(ip route get 1 2>/dev/null | awk '{print $7; exit}' || true)"
  fi
  printf '%s' "${ip:-127.0.0.1}"
}

show_summary() {
  local panel_ip="$1" user="$2" pass="$3" existing="$4"
  echo ""
  echo "========================================="
  echo "   GoPanel terpasang! 🎉"
  echo "========================================="
  echo ""
  echo "🌐 Panel URL:      http://${panel_ip}:${PANEL_PORT}"
  echo "🔑 Admin username: ${user}"
  if [ "$existing" = "1" ]; then
    echo "🔐 Admin password: (sudah ada sebelumnya — gunakan password lama)"
    echo ""
    echo "ℹ️  Instalasi yang sudah ada terdeteksi. Password tidak diubah."
    echo "   Lupa password? Reset dari server:"
    echo "   sudo /opt/gopanel/gopanel -config /etc/gopanel/config.json -reset-admin"
  else
    echo "🔐 Admin password: ${pass}"
    echo ""
    echo "📝 Segera login dan ganti password melalui halaman Akun Saya."
    echo "   Atau setel GOPANEL_ADMIN_PASSWORD sebelum install untuk menentukan sendiri."
  fi
  echo ""
  echo "⚠️  Catatan keamanan:"
  echo "   - Panel listen di 0.0.0.0:${PANEL_PORT} (HTTP tanpa TLS)."
  echo "   - Batasi akses lewat firewall / reverse proxy + TLS."
  echo ""
  echo "🛠️  Perintah berguna:"
  echo "   - Status:        systemctl status gopanel"
  echo "   - Log:           journalctl -u gopanel -f"
  echo "   - Restart:       systemctl restart gopanel"
  echo "   - Upgrade:       sudo bash ${INSTALL_DIR}/scripts/install.sh"
  echo ""
  echo "========================================="
}

main() {
  confirm_root
  detect_os
  log "GoPanel VPS Installer — ${OS_NAME}"
  log "Repo: ${REPO_URL}"
  log "Install ke: ${INSTALL_DIR}"

  ensure_apt_packages git curl
  ensure_go

  if [ -d "$INSTALL_DIR/.git" ]; then
    log "Direktori ${INSTALL_DIR} sudah ada, memperbarui..."
    git -C "$INSTALL_DIR" fetch --all --prune \
      && git -C "$INSTALL_DIR" reset --hard origin/main >/dev/null \
      || warn "Pembaruan repo gagal; memakai checkout yang ada."
  elif [ -d "$INSTALL_DIR" ] && [ -n "$(ls -A "$INSTALL_DIR" 2>/dev/null)" ]; then
    die "${INSTALL_DIR} sudah ada dan bukan repo git. Pindahkan/hapus dulu, lalu jalankan ulang."
  else
    log "Mengkloning repositori..."
    git clone --depth 1 "$REPO_URL" "$INSTALL_DIR" || die "git clone gagal"
  fi

  [ -f "$INSTALL_DIR/go.mod" ] || die "go.mod tidak ditemukan di ${INSTALL_DIR}; clone tidak lengkap."

  cd "$INSTALL_DIR"
  local version
  version="$(git -C "$INSTALL_DIR" describe --tags --always 2>/dev/null || echo dev)"
  log "Membangun binary GoPanel (versi ${version})..."
  go build -trimpath -ldflags "-s -w -X main.version=${version}" -o gopanel ./cmd/gopanel \
    || die "Build gagal. Pastikan Go >= ${GO_VERSION} dan koneksi internet tersedia."

  write_config
  write_systemd_unit

  local admin_pass="${GOPANEL_ADMIN_PASSWORD:-}"
  if [ -z "$admin_pass" ]; then
    admin_pass="$(generate_password)"
  fi

  local existing=0
  stop_running_panel \
    || die "Port ${PANEL_PORT} masih dipakai proses lain. Hentikan proses tersebut lalu jalankan ulang installer."
  bootstrap_admin "$admin_pass" \
    || die "Bootstrap admin gagal — lihat cuplikan log di atas."
  [ -n "${BOOTSTRAP_PASS:-}" ] || existing=1

  start_panel_service

  if ! wait_health; then
    err "Panel tidak merespons di http://127.0.0.1:${PANEL_PORT}/api/v1/healthz"
    if have_systemd; then
      journalctl -u gopanel -n 40 --no-pager >&2 || true
    else
      tail -n 40 /var/log/gopanel.log >&2 || true
    fi
    exit 1
  fi
  ok "Panel merespons dan siap digunakan."

  show_summary "$(detect_public_ip)" "${BOOTSTRAP_USER:-admin}" "${BOOTSTRAP_PASS:-$admin_pass}" "$existing"
}

main "$@"
