#!/usr/bin/env bash
#
# GoPanel VPS Installer — aaPanel style: git clone + run, done.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/yazvip/GoPanel/main/scripts/install.sh | sudo bash -
#   GOPANEL_ADMIN_PASSWORD="xxxxxxxxxxxx" sudo bash scripts/install.sh
#
# Defaults:
#   - Install dir: /opt/gopanel
#   - Config: /etc/gopanel/config.json
#   - Listen: 127.0.0.1:7800 (HTTP). TLS/SSL must be added later via Pengaturan.
#   - Bootstrap admin: user "admin", password from GOPANEL_ADMIN_PASSWORD env
#     or auto-generated 16-char random password.

set -euo pipefail

###############################################################################
# Helpers
###############################################################################
log()   { printf '\n[%s] %s\n' "$(date '+%H:%M:%S')" "$*" ; }
ok()    { printf '\n\033[32m✅ %s\033[0m\n' "$*" ; }
warn()  { printf '\n\033[33m⚠️  %s\033[0m\n' "$*" ; }
err()   { printf '\033[31m❌ %s\033[0m\n' "$*" >&2 ; }

confirm_root() {
  if [ "$(id -u)" -ne 0 ]; then
    err "Installer harus dijalankan sebagai root (sudo)."
    exit 1
  fi
}

detect_os() {
  if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS_NAME="${PRETTY_NAME:-${NAME:-Unknown}}"
    OS_ID="${ID:-unknown}"
    OS_VERSION="${VERSION_ID:-}"
  else
    OS_NAME="Unknown"; OS_ID="unknown"; OS_VERSION=""
  fi
}

ensure_go() {
  if command -v go >/dev/null 2>&1; then
    log "Go sudah terpasang: $(go version)"
    return 0
  fi

  log "Go belum terdeteksi, mengunduh Go 1.26.3..."
  GO_VERSION="1.26.3"
  GO_TAR="go${GO_VERSION}.linux-amd64.tar.gz"
  tmp_tar="$(mktemp)"
  wget -qO "$tmp_tar" "https://go.dev/dl/${GO_TAR}" || {
    err "Gagal mengunduh Go dari https://go.dev/dl/${GO_TAR}"
    rm -f "$tmp_tar"
    exit 1
  }
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "$tmp_tar"
  rm -f "$tmp_tar"
  export PATH=/usr/local/go/bin:$PATH
  log "Go dipasang: $(go version)"
}

ensure_git() {
  if command -v git >/dev/null 2>&1; then return 0; fi
  log "git belum terpasang, memasang git..."
  case "$OS_ID" in
    ubuntu|debian)
      apt-get update -qq && apt-get install -y -qq git
      ;;
    *)
      err "git wajib terpasang terlebih dahulu pada $OS_NAME."
      exit 1
      ;;
  esac
}

generate_password() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 8
  elif command -v tr >/dev/null 2>&1 && command -v head >/dev/null 2>&1; then
    tr -dc 'a-f0-9' < /dev/urandom | head -c 16
  else
    err "Tidak dapat menghasilkan password acak."
    exit 1
  fi
}

write_config() {
  local config_path="$1"
  mkdir -p "$(dirname "$config_path")"
  cat > "$config_path" <<'EOF'
{
  "listen": "0.0.0.0:7800",
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
  log "Config ditulis ke $config_path (listen 0.0.0.0:7800 — akses dari luar VPS)"
}

install_systemd_unit() {
  local unit_path="/etc/systemd/system/gopanel.service"
  cat > "$unit_path" <<'EOF'
[Unit]
Description=GoPanel VPS Control Panel
After=network.target

[Service]
Type=simple
EnvironmentFile=-/etc/default/gopanel
ExecStart=/opt/gopanel/gopanel -config /etc/gopanel/config.json
Restart=on-failure
RestartSec=5
WorkingDirectory=/opt/gopanel
User=root
Group=root

[Install]
WantedBy=multi-user.target
EOF
  log "Unit systemd ditulis ke $unit_path"
  systemctl daemon-reload
  systemctl enable gopanel.service
  log "systemd: gopanel.service diaktifkan"
}

bootstrap_panel() {
  local install_dir="$1"
  local config_path="$2"
  local admin_pass="$3"

  # Start panel once to bootstrap the first admin; capture output for password.
  local bootstrap_log
  bootstrap_log="$(mktemp)"
  GOPANEL_ADMIN_PASSWORD="$admin_pass" \
    "$install_dir/gopanel" -config "$config_path" > "$bootstrap_log" 2>&1 &
  local pid=$!

  # Wait for /auth/me to accept unauthenticated? No — admin is created, login works.
  # The panel may exit after starting (it keeps running). Wait briefly.
  log "Menunggu panel siap (maksimal 30 detik)..."
  local ready=0
  for i in $(seq 1 30); do
    if kill -0 "$pid" 2>/dev/null; then
      # Panel still running — check if we can see bootstrap output yet
      if grep -q "GoPanel first administrator" "$bootstrap_log" 2>/dev/null || \
         grep -q "listening" "$bootstrap_log" 2>/dev/null; then
        ready=1
        break
      fi
      sleep 1
    else
      wait "$pid" || true
      break
    fi
  done

  # Kill the foreground bootstrap process; we'll start via systemd.
  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi

  # Try to extract bootstrap credentials from log.
  local found_user="admin"
  local found_pass=""
  if [ -f "$bootstrap_log" ]; then
    found_pass="$(grep -oP '(?<=password: )\S+' "$bootstrap_log" | head -1 || true)"
    found_user="$(grep -oP '(?<=username: )\S+' "$bootstrap_log" | head -1 || echo "admin")"
  fi
  rm -f "$bootstrap_log"

  if [ -n "$found_pass" ]; then
    admin_pass="$found_pass"
  fi

  # Start via systemd for persistent operation.
  systemctl start gopanel.service || {
    warn "systemctl start gagal, mencoba nohup fallback..."
    nohup "$install_dir/gopanel" -config "$config_path" > /var/log/gopanel.log 2>&1 &
    echo $! > /var/run/gopanel.pid
  }

  # Detect the VPS IP so the user can reach the panel from outside.
  local panel_ip=""
  if command -v hostname >/dev/null 2>&1; then
    panel_ip="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
  fi
  if [ -z "$panel_ip" ] && command -v ip >/dev/null 2>&1; then
    panel_ip="$(ip route get 1 2>/dev/null | awk '{print $7; exit}' || true)"
  fi
  [ -z "$panel_ip" ] && panel_ip="127.0.0.1"

  # Display credentials.
  echo ""
  echo "========================================="
  echo "   GoPanel terpasang! 🎉"
  echo "========================================="
  echo ""
  echo "🌐 Panel URL:      http://${panel_ip}:7800"
  echo "🔑 Admin username: $found_user"
  echo "🔐 Admin password: $admin_pass"
  echo ""
  echo "⚠️  Catatan keamanan:"
  echo "   - Panel diikat ke 0.0.0.0:7800 (HTTP tanpa TLS)."
  echo "   - Akses hanya dari jaringan tepercaya; jangan buka ke internet."
  echo "   - Gunakan reverse proxy + TLS (lihat Pengaturan) untuk produksi."
  echo ""
  echo "📝 Segera login dan ganti password melalui halaman Akun Saya."
  echo "   Atau setel GOPANEL_ADMIN_PASSWORD sebelum install untuk menentukan sendiri."
  echo ""
  echo "🛠️  Perintah berguna:"
  echo "   - Log panel:     journalctl -u gopanel -f"
  echo "   - Stop panel:    systemctl stop gopanel"
  echo "   - Restart panel: systemctl restart gopanel"
  echo ""
  echo "========================================="
}

main() {
  confirm_root
  detect_os
  log "GoPanel VPS Installer dimulai pada $OS_NAME ($OS_VERSION)"

  # Repo URL: first arg overrides; else GOPANEL_REPO_URL env; else default.
  local repo_url="${1:-${GO_PANEL_REPO_URL:-https://github.com/yazvip/GoPanel.git}}"
  local install_dir="/opt/gopanel"
  local config_path="/etc/gopanel/config.json"

  log "Repo: $repo_url"
  log "Install ke: $install_dir"

  ensure_go
  ensure_git

  if [ -d "$install_dir/.git" ]; then
    log "Direktori $install_dir sudah ada, mencoba git pull..."
    git -C "$install_dir" pull --ff-only || true
  else
    log "Mengkloning repositori..."
    git clone "$repo_url" "$install_dir"
  fi

  cd "$install_dir"

  log "Membangun binary GoPanel..."
  go build -o gopanel ./cmd/gopanel

  write_config "$config_path"

  # Determine bootstrap admin password.
  local admin_pass="${GOPANEL_ADMIN_PASSWORD:-}"
  if [ -z "$admin_pass" ]; then
    admin_pass="$(generate_password)"
  fi

  install_systemd_unit
  bootstrap_panel "$install_dir" "$config_path" "$admin_pass"
}

main "$@"
