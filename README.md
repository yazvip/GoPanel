# GoPanel

GoPanel adalah panel kontrol server untuk Linux: backend Go, frontend SPA JavaScript tanpa framework yang di-embed ke dalam binary, dan penyimpanan data panel di SQLite. Panel ini mengelola layanan host secara langsung (Nginx, PHP-FPM, MySQL/MariaDB, cron, systemd, UFW) dan menyediakan API JSON di `/api/v1`.

Karakteristik utama:

- Satu binary tanpa runtime Node.js atau proses frontend terpisah.
- Database panel SQLite dengan migrasi yang di-embed; tidak perlu server database untuk panel itu sendiri.
- Frontend memakai ES module, hash router, dan locale `en`/`id`.
- Target praktis: Ubuntu/Debian dengan systemd. Build non-Linux memakai backend sistem simulasi (`sysfake`).

## Quick Install (seperti aaPanel)

Di VPS Linux (Ubuntu/Debian) yang baru, install GoPanel dalam satu baris:

```bash
curl -fsSL https://raw.githubusercontent.com/yazvip/GoPanel/main/scripts/install.sh | sudo bash -
```

Atau manual:

```bash
sudo apt-get update && sudo apt-get install -y git
sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/yazvip/GoPanel/main/scripts/install.sh)"
```

Langkah yang dilakukan skrip secara otomatis:

1. **Instal Go** 1.26.3 bila belum ada.
2. **Clone** repositori ke `/opt/gopanel`.
3. **Build** binary (`go build -o gopanel ./cmd/gopanel`).
4. **Buat** `config.json` di `/etc/gopanel/` dengan default produksi (listen `127.0.0.1:7800`, data di `/var/lib/gopanel`).
5. **Bootstrap** admin pertama — username `admin`, password acak 16 karakter (atau `GOPANEL_ADMIN_PASSWORD` bila di-set).
6. **Aktifkan** service systemd `gopanel.service` (auto-start).
7. **Tampilkan** username + password di akhir.

Setelah install selesai, buka `http://127.0.0.1:7800` dan login dengan kredensial yang ditampilkan.

> ⚠️ **Keamanan:** password default dicetak ke stdout dan log. Ganti segera setelah login pertama, atau setel `GOPANEL_ADMIN_PASSWORD` sebelum install. Untuk produksi, gunakan TLS (lihat bagian [TLS panel](#tls-panel)) dan bind panel ke `127.0.0.1:7800` di belakang reverse proxy.

## Fitur utama

Kecuali ditandai khusus, halaman baca tersedia untuk semua pengguna yang sudah login, sedangkan aksi tulis pada modul operasional memerlukan role `admin` atau `operator`. Role `viewer` bersifat baca saja.

- **Dasbor dan pemantauan** — ringkasan host, pemakaian CPU/memori/disk/load/jaringan, status layanan, daftar proses, dan grafik metrik. Sampel metrik diambil tiap 30 detik dengan rentang riwayat 1 jam, 6 jam, 24 jam, dan 7 hari. Proses dapat dihentikan dengan `SIGTERM`; PID 1 dan proses panel sendiri dilindungi.
- **Situs web** — membuat situs bertipe `static`, `php`, atau `proxy`, menghasilkan vhost Nginx, mengelola domain tambahan, mengaktifkan/menonaktifkan situs, melihat log akses/error, serta menerbitkan dan memperbarui sertifikat Let's Encrypt.
- **Basis data** — daftar, buat, dan hapus database MySQL/MariaDB; kelola pengguna database; ganti password; dan jalankan backup database. Password yang dikosongkan akan dibuat otomatis (24 karakter) dan disimpan terenkripsi di database panel.
- **Manajer berkas** — akses hanya di dalam root yang terdaftar pada `allowed_roots`; lihat, baca/edit (maksimum 2 MiB), unggah, buat folder/berkas, ganti nama, salin, pindah, hapus, ubah mode, arsipkan ke `tar.gz`/`zip`, ekstrak arsip, dan cari berkas. Symlink tidak diikuti.
- **Cadangan** — backup situs dan database sebagai job latar, unduh/hapus hasil backup, restore dengan konfirmasi nama target, serta jadwal backup berbasis cron dengan retensi `keep` (default 7 untuk jadwal).
- **Tugas cron** — kelola job cron di panel; isinya disinkronkan ke `cron_file` (default `/etc/cron.d/gopanel`), dapat dijalankan sekarang sebagai job, dan menyimpan log keluaran.
- **Toko aplikasi** — katalog paket apt: Nginx, Apache, MariaDB, MySQL, PHP-FPM, Node.js, Python, Redis, UFW, Fail2ban, dan phpMyAdmin. Status/versi terpasang dibaca dari `dpkg-query`; instalasi dan pembaruan cache berjalan sebagai job dengan progres live per kartu. Konflik diperingatkan (Nginx ↔ Apache, MariaDB ↔ MySQL).
- **Firewall** — status dan aturan UFW, tambah/hapus aturan, aktifkan/nonaktifkan. Sebelum UFW diaktifkan, panel menambahkan aturan izin untuk port SSH (`sshd_port`) dan port panel. Aturan yang membuka port SSH tidak dapat dihapus dari panel.
- **Layanan** — daftar service systemd, aksi start/stop/restart/reload, dan log via `journalctl`. Service panel sendiri (`gopanel`) tidak dapat dikelola dari panel.
- **Terminal (admin)** — terminal web berbasis PTY dan WebSocket. Tiket terminal berumur 30 detik, sekali pakai, terikat pada user dan IP; sesi idle ditutup setelah 30 menit. Shell berjalan sebagai root (`/bin/bash -l`) pada implementasi Linux.
- **Antrean tugas** — job latar dengan worker terbatas, event SSE untuk progres/log, dan berkas log per job. Job yang terputus saat panel berhenti ditandai saat start berikutnya.
- **Pengguna (admin)** — buat/ubah/hapus pengguna, atur role (`admin`, `operator`, `viewer`), locale, reset password, dan nonaktifkan akun. Panel mencegah penghapusan/penurunan role admin terakhir.
- **Log audit (admin)** — mencatat login, perubahan konfigurasi/data, penolakan izin, dan aksi sensitif lain. Pembersihan otomatis dijadwalkan; default retensi audit 90 hari dan job 30 hari.
- **Pengaturan (admin)** — nama panel, locale, tema (`system`/`light`/`dark`), email dan mode staging ACME, serta unggah sertifikat TLS panel. Perubahan TLS memerlukan restart.
- **Akun saya** — ganti password (sesi lain dicabut), aktifkan/nonaktifkan TOTP 2FA, membuat 10 kode pemulihan sekali pakai, dan mencabut sesi aktif.

## Tumpukan teknologi

- **Go 1.26.3** (lihat `go.mod`), modul `gopanel`.
- **`net/http`** dengan `ServeMux` bermetode dan API JSON berprefix `/api/v1`; error berbentuk `{"error":{"code":"...","message":"..."}}`.
- **SQLite** `modernc.org/sqlite` (pure Go), mode WAL, koneksi tunggal, migrasi SQL di-embed dari `internal/db/migrations`.
- **gopsutil v4** untuk metrik host dan proses.
- **go-acme/lego v4** untuk penerbitan sertifikat Let's Encrypt (HTTP-01 webroot).
- **robfig/cron v3** untuk jadwal backup dan pemeliharaan.
- **creack/pty** dan **coder/websocket** untuk terminal.
- **skip2/go-qrcode** untuk QR TOTP, **x/crypto/argon2** untuk hash password.
- **Frontend**: HTML/CSS/JavaScript ES module tanpa bundler, hash router, emulator terminal sendiri, dan berkas locale JSON. Tidak ada `package.json`, Node.js, atau langkah build frontend.

## Modul backend

| Paket | Tanggung jawab |
| --- | --- |
| `internal/config` | Struct konfigurasi, default produksi/dev, override environment, pembuatan `secret.key`, dan penyimpanan config. |
| `internal/db` | Membuka SQLite, mengatur pragma, dan menjalankan migrasi yang di-embed. |
| `internal/store` | Seluruh query dan transaksi ke database panel. |
| `internal/model` | Struct data bersama (user, sesi, situs, database, backup, job, audit, metrik). |
| `internal/auth` | Hash password Argon2id, sesi server-side, cookie, TOTP, kode pemulihan, enkripsi AES-GCM, bootstrap admin, dan rate limit login. |
| `internal/audit` | Perekam log audit. |
| `internal/httpapi` | Routing, middleware (auth, CSRF, rate limit, header keamanan), handler API, dan penyajian aset statis. |
| `internal/validate` | Validator nama situs, database, user, domain, unit systemd, cron, dan locale. |
| `internal/execx` | Menjalankan perintah eksternal dengan timeout, batas keluaran, dan streaming. |
| `internal/sys` | Antarmuka dan tipe inti untuk operasi sistem (host, service, firewall, MySQL, Nginx, PTY). |
| `internal/sys/factory` | Memilih implementasi sistem berdasarkan build tag: Linux asli atau simulasi. |
| `internal/sys/linux` | Implementasi nyata: Nginx, MySQL, apt/dpkg, UFW, systemd, proses, PHP, dan PTY. |
| `internal/sys/sysfake` | Implementasi palsu untuk build non-Linux; tidak menyentuh layanan host. |
| `internal/files` | Operasi manajer berkas di dalam root yang diizinkan. |
| `internal/archive` | Ekstraksi/pembuatan `tar.gz` dan `zip` dengan pembatasan path dan ukuran. |
| `internal/sites` | CRUD situs, domain, vhost Nginx, dan validasi root situs. |
| `internal/acme` | Klien ACME/Let's Encrypt dan challenge HTTP-01 berbasis webroot. |
| `internal/certs` | Orkestrasi penerbitan dan pembaruan sertifikat situs sebagai job. |
| `internal/backup` | Backup/restore situs dan database, retensi, serta handler job backup. |
| `internal/databases` | Operasi database dan pengguna MySQL/MariaDB. |
| `internal/cronjobs` | CRUD cron, sinkronisasi ke berkas cron, eksekusi manual, dan log. |
| `internal/apps` | Katalog aplikasi apt, deteksi paket, dan job instalasi. |
| `internal/jobs` | Manajer antrean, worker, registry tipe job, runner, dan event SSE. |
| `internal/metrics` | Kolektor metrik periodik, riwayat, dan pemangkasan data lama. |
| `internal/scheduler` | Penjadwal backup, retensi/pemangkasan, dan pembaruan sertifikat. |
| `internal/terminal` | Penerbitan dan konsumsi tiket terminal. |
| `web` | Aset frontend yang di-embed dan handler HTTP-nya. |

## Persyaratan

### Sistem operasi dan hak akses

- Linux dengan systemd. Implementasi Linux memanggil `systemctl`, `nginx`, `mysql`, `mysqldump`, `ufw`, `apt-get`, `dpkg-query`, dan `/bin/bash`.
- Jalankan panel sebagai **root** atau dengan izin setara. Tanpa itu, operasi ke `/etc/nginx`, `/etc/cron.d`, `systemctl`, `ufw`, `apt-get`, dan terminal akan gagal.
- Build non-Linux tetap dapat dijalankan, tetapi seluruh operasi sistem memakai `sysfake` dan tidak mengubah host.

### Perangkat lunak host

Tidak semuanya wajib untuk start; pasang sesuai modul yang dipakai:

- **Nginx** untuk situs web dan challenge ACME.
- **MySQL atau MariaDB** beserta klien `mysql` dan `mysqldump` untuk modul basis data.
- **PHP-FPM** di `/etc/php/<versi>/fpm` jika memakai situs PHP.
- **UFW** untuk modul firewall.
- **systemd** untuk modul layanan dan reload Nginx.
- **apt/dpkg** untuk Toko Aplikasi.
- **bash** untuk terminal web.

### Port dan direktori

- Port panel: `7800` (dapat diubah), plus `80`/`443` untuk situs dan ACME.
- Direktori default yang perlu dapat ditulis: `/var/lib/gopanel`, `/var/log/gopanel`, `/var/backups/gopanel`, `/etc/gopanel/ssl`, `/etc/cron.d`, `/etc/nginx/sites-available`, `/etc/nginx/sites-enabled`, `/var/log/nginx`, dan `/var/www`.
- Untuk membangun: Go 1.26.3 atau lebih baru. Tidak diperlukan Node.js.

## Build dan test

```bash
# kompilasi semua paket
go build ./...

# binary utama
go build -o gopanel ./cmd/gopanel

# menyisipkan versi ke binary (default: "dev")
go build -ldflags "-X main.version=1.0.0" -o gopanel ./cmd/gopanel
```

Pemeriksaan statis dan test:

```bash
go vet ./...
go test ./...
```

Repo ini belum memiliki berkas `*_test.go`, sehingga `go test ./...` melaporkan `[no test files]` untuk semua paket. `go build ./...` dan `go vet ./...` lolos pada kondisi repo saat ini.

Repo tidak menyertakan `Makefile`, skrip build/deploy, unit systemd, atau Dockerfile. Semua perintah di atas dijalankan langsung dengan toolchain Go.

## Menjalankan

### Produksi

```bash
sudo ./gopanel -config /etc/gopanel/config.json
```

Perilaku start:

- Jika berkas config belum ada, GoPanel membuatnya dari default produksi dengan mode `0600`.
- Jika belum ada pengguna, panel membuat user `admin` dengan password acak 16 karakter (atau nilai `GOPANEL_ADMIN_PASSWORD` bila diisi). Password dicetak ke stdout dan log start; ganti segera setelah login pertama.
- Pada mode non-dev, admin pertama wajib mengganti password (`must_change_password`).
- Jika `tls.cert_file` dan `tls.key_file` tersedia, server langsung memakai HTTPS (`ListenAndServeTLS`). Tanpa itu panel berjalan sebagai HTTP polos.

Perintah lain:

```bash
./gopanel -version
./gopanel -config /etc/gopanel/config.json -listen 127.0.0.1:7800
```

`-listen` hanya mengganti alamat saat proses berjalan; nilainya tidak ditulis kembali ke berkas config.

### Mode pengembangan

```bash
go run ./cmd/gopanel -dev
# buka http://127.0.0.1:7800
```

Mode `-dev` memakai default: listen `127.0.0.1:7800`, data di `data-dev/`, log level `debug`, dan admin pertama tidak wajib mengganti password.

Catatan penting: pada build **Linux**, `-dev` tetap memakai implementasi sistem asli; flag ini tidak mengaktifkan data palsu. Backend `sysfake` dipilih oleh build tag `!linux` pada `internal/sys/factory/factory_other.go`. Di Linux, operasi seperti systemctl, apt, UFW, dan terminal tetap nyata. Berkas `config.json` di root repo saat ini berisi path gaya dev (`data-dev`), sehingga otomatis terbaca bila dijalankan dari direktori repo.

### Flag CLI

| Flag | Default | Keterangan |
| --- | --- | --- |
| `-config` | `config.json` | Path berkas konfigurasi JSON. |
| `-dev` | `false` | Memakai default pengembangan dan melewati kewajiban ganti password admin pertama. |
| `-listen` | kosong | Override alamat listen; tidak disimpan ke config. |
| `-version` | `false` | Cetak versi lalu keluar. |

## Konfigurasi

### Urutan pemuatan

1. Default bawaan: produksi, atau default dev bila `-dev` dipakai.
2. Berkas JSON pada `-config`. Field yang tidak ada tetap memakai default.
3. Environment variable (daftar di bawah).
4. Override `-listen` dari CLI.

Saat start, GoPanel juga membuat direktori yang diperlukan (data, log, backup, ACME, SSL, root situs, path Nginx, dan `allowed_roots`) dengan mode `0755`, serta membuat `secret.key` acak 32 byte dengan mode `0600` bila belum ada. Perubahan config hanya berlaku setelah restart.

### Contoh config.json

```json
{
  "listen": "0.0.0.0:7800",
  "data_dir": "/var/lib/gopanel",
  "db_path": "/var/lib/gopanel/panel.db",
  "log_level": "info",
  "panel_url": "https://panel.example.com",
  "allowed_roots": [
    { "token": "var-www", "label": "Web root", "path": "/var/www" },
    { "token": "home", "label": "Home", "path": "/home" },
    { "token": "srv", "label": "Services", "path": "/srv" },
    { "token": "opt", "label": "Optional", "path": "/opt" }
  ],
  "upload_max_bytes": 2147483648,
  "session_idle_hours": 12,
  "session_absolute_hours": 168,
  "acme_email": "admin@example.com",
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
  "mysql_socket": "/run/mysqld/mysqld.sock",
  "sshd_port": 22,
  "terminal_enabled": true,
  "behind_proxy": false,
  "trusted_proxies": [],
  "tls": {
    "cert_file": "",
    "key_file": ""
  },
  "secret_key_path": "/var/lib/gopanel/secret.key"
}
```

### Field penting

**Umum dan keamanan**

| Field | Default produksi | Keterangan |
| --- | --- | --- |
| `listen` | `0.0.0.0:7800` | Alamat HTTP/HTTPS panel. |
| `data_dir` | `/var/lib/gopanel` | Direktori data panel. |
| `db_path` | `<data_dir>/panel.db` | Berkas SQLite panel. |
| `log_level` | `info` | `debug`, `info`, `warn`, atau `error`. |
| `panel_url` | kosong | URL publik panel. Jika diawali `https://`, cookie sesi memakai atribut `Secure`. |
| `allowed_roots` | `/var/www`, `/home`, `/srv`, `/opt` | Root yang boleh diakses Manajer Berkas, masing-masing dengan `token`, `label`, dan `path`. |
| `upload_max_bytes` | `2147483648` (2 GiB) | Batas unggah Manajer Berkas. |
| `session_idle_hours` | `12` | Batas idle sesi. |
| `session_absolute_hours` | `168` (7 hari) | Batas umur absolut sesi. |
| `secret_key_path` | `<data_dir>/secret.key` | Kunci 32 byte untuk enkripsi AES-GCM. |
| `behind_proxy` | `false` | Aktifkan bila panel berada di belakang reverse proxy. |
| `trusted_proxies` | `[]` | Daftar CIDR proxy yang boleh memasok `X-Forwarded-For`. |
| `tls.cert_file`, `tls.key_file` | kosong | Sertifikat dan kunci TLS panel; bila diisi, panel melayani HTTPS. |

**Path hosting**

| Field | Default produksi | Keterangan |
| --- | --- | --- |
| `acme_email` | kosong | Email akun ACME/Let's Encrypt; diperlukan untuk menerbitkan sertifikat situs. |
| `acme_staging` | `false` | Gunakan direktori staging Let's Encrypt untuk pengujian. |
| `acme_webroot` | `/var/lib/gopanel/acme` | Webroot challenge HTTP-01. |
| `ssl_dir` | `/etc/gopanel/ssl` | Penyimpanan sertifikat situs. |
| `backup_dir` | `/var/backups/gopanel` | Penyimpanan hasil backup. |
| `log_dir` | `/var/log/gopanel` | Log panel dan job. |
| `cron_file` | `/etc/cron.d/gopanel` | Berkas cron yang ditulis dari job panel. |
| `site_root_base` | `/var/www` | Root situs harus berada di dalam direktori ini. |
| `nginx_sites_available` | `/etc/nginx/sites-available` | Direktori vhost Nginx. |
| `nginx_sites_enabled` | `/etc/nginx/sites-enabled` | Direktori symlink vhost aktif. |
| `nginx_conf` | `/etc/nginx/nginx.conf` | Berkas utama Nginx untuk `nginx -t`. |
| `nginx_log_dir` | `/var/log/nginx` | Log akses/error situs. |
| `php_conf_dir` | `/etc/php` | Sumber daftar versi PHP-FPM (`<dir>/<versi>/fpm`). |

**MySQL, SSH, dan terminal**

| Field | Default produksi | Keterangan |
| --- | --- | --- |
| `mysql_admin_user` | `root` | User admin MySQL untuk operasi panel. |
| `mysql_admin_password` | kosong | Password user tersebut; ditulis ke berkas sementara `0600` saat dipakai. Simpan config dengan izin ketat. |
| `mysql_socket` | `/run/mysqld/mysqld.sock` | Socket MySQL; kosongkan untuk memakai default klien. |
| `sshd_port` | `22` | Port SSH yang diizinkan saat UFW diaktifkan dan dilindungi dari penghapusan aturan. |
| `terminal_enabled` | `true` | Mengaktifkan terminal web (tetap khusus admin). |

Catatan dev: `-dev` mengubah `listen` menjadi `127.0.0.1:7800`, `log_level` menjadi `debug`, seluruh path data ke `data-dev/...`, dan `allowed_roots` menjadi `data-dev/roots/var-www` serta `data-dev/roots/home`.

### Variabel lingkungan

Environment diterapkan setelah berkas config:

```bash
GOPANEL_LISTEN=127.0.0.1:7800
GOPANEL_DATA_DIR=/var/lib/gopanel
GOPANEL_DB=/var/lib/gopanel/panel.db
GOPANEL_LOG_LEVEL=info
GOPANEL_LOG_DIR=/var/log/gopanel
GOPANEL_ACME_WEBROOT=/var/lib/gopanel/acme
GOPANEL_SSL_DIR=/etc/gopanel/ssl
GOPANEL_BACKUP_DIR=/var/backups/gopanel
GOPANEL_CRON_FILE=/etc/cron.d/gopanel
GOPANEL_SITE_ROOT=/var/www
GOPANEL_TLS_CERT=/etc/gopanel/ssl/panel.crt
GOPANEL_TLS_KEY=/etc/gopanel/ssl/panel.key
GOPANEL_ACME_EMAIL=admin@example.com
GOPANEL_TERMINAL=true
GOPANEL_ADMIN_PASSWORD=password-awal-yang-panjang
```

`GOPANEL_ADMIN_PASSWORD` hanya dipakai saat belum ada pengguna. Password tersebut tetap dicetak ke stdout/log pada pembuatan admin pertama.

## Frontend, embed, dan i18n

Aset frontend berada di `web/` dan di-embed lewat `web/embed.go`:

```go
//go:embed index.html assets locales
```

Konsekuensinya: panel **selalu** menyajikan aset dari binary. Mengubah `web/index.html`, `web/assets/**`, atau `web/locales/*.json` tidak akan terlihat sampai binary dibangun ulang. Mode `-dev` tidak menyajikan berkas dari disk dan tidak menyediakan hot reload.

```bash
# setelah mengubah apa pun di web/
go build -o gopanel ./cmd/gopanel
```

Struktur singkat:

- `web/index.html` — shell aplikasi.
- `web/assets/js/main.js` — bootstrap, navigasi, pembatasan menu berdasarkan role, dan pemuatan view.
- `web/assets/js/router.js` — hash router (`#/dashboard`, `#/sites`, dan seterusnya).
- `web/assets/js/views/` — satu modul per halaman.
- `web/assets/js/i18n.js` — pemuatan locale dan fallback.
- `web/locales/en.json`, `web/locales/id.json` — kamus teks.

Internasionalisasi:

- Bahasa yang didukung: Inggris (`en`) dan Indonesia (`id`).
- `i18n.js` memuat `/locales/<bahasa>.json`; jika kunci tidak ada, teks jatuh kembali ke `en.json`.
- Preferensi disimpan di `localStorage` dengan kunci `gopanel.lang`; bila kosong, panel memakai bahasa browser (Indonesia bila diawali `id`, selain itu Inggris).
- Pilihan bahasa tersedia di halaman Pengaturan (admin). Setelah menambah kunci baru, tambahkan ke **kedua** berkas locale.
- Tema disimpan di `localStorage` (`gopanel.theme`) dengan nilai `system`, `light`, atau `dark`.

## Catatan produksi

### TLS panel

Ada dua cara:

1. **Langsung di config** — isi `tls.cert_file` dan `tls.key_file`, lalu restart. Panel memakai `ListenAndServeTLS`.
2. **Unggah lewat Pengaturan** — secara default sertifikat disimpan sebagai `<data_dir>/tls/panel.crt` dan `<data_dir>/tls/panel.key` (atau ke path `tls.*` bila sudah diisi di config), path-nya dicatat di setting `tls_cert_file`/`tls_key_file`, dan panel perlu di-restart.

Selalu set `panel_url` ke URL `https://...` yang dipakai pengguna. Tanpa itu, cookie sesi tidak memakai atribut `Secure` meskipun TLS dipasang lewat unggahan Pengaturan. Panel tidak melakukan redirect otomatis dari HTTP ke HTTPS; pengalihan harus dilakukan di reverse proxy atau firewall.

Jangan biarkan `0.0.0.0:7800` terbuka tanpa TLS. Jika memakai reverse proxy, bind panel ke `127.0.0.1:7800`.

### Sertifikat situs (ACME)

- Isi `acme_email` (atau setting `acme_email`) sebelum menerbitkan sertifikat.
- Domain harus mengarah ke server dan port 80 harus dapat dijangkau internet untuk challenge HTTP-01.
- `acme_webroot` harus sama dengan lokasi yang dilayani blok `/.well-known/acme-challenge/` pada vhost Nginx buatan panel.
- Gunakan `acme_staging: true` untuk pengujian agar tidak terkena batas Let's Encrypt.
- Scheduler memeriksa sertifikat setiap hari (sekitar pukul 04.00 waktu server, menit acak) dan memperbarui sertifikat yang kedaluwarsa dalam 30 hari.

### Reverse proxy

Jika TLS diakhiri di proxy:

- Set `behind_proxy: true` dan isi `trusted_proxies` dengan CIDR proxy agar IP klien diambil dari `X-Forwarded-For` hanya dari sumber tepercaya.
- Set `panel_url` ke URL HTTPS publik.
- Teruskan header `Upgrade` dan `Connection` untuk WebSocket terminal di `/api/v1/terminal/ws`.
- Batas unggah proxy harus minimal sebesar `upload_max_bytes` bila fitur unggah besar dipakai.

### Service manager

Repo menyertakan unit systemd siap pakai di `systemd/gopanel.service`. Install skrip `scripts/install.sh` otomatis meng-copy dan mengaktifkannya:

```bash
sudo systemctl enable --now gopanel.service
sudo systemctl status gopanel
```

Jika ingin manual, salin ke `/etc/systemd/system/gopanel.service` dan jalankan `systemctl daemon-reload`. Karena panel menjalankan `systemctl`, `apt`, `ufw`, dan terminal root, proses harus berjalan sebagai root.

### Data dan backup

- Data utama berada di `data_dir`: `panel.db` (SQLite, mode WAL), `secret.key`, dan `tls/`.
- `backup_dir` menyimpan arsip backup situs/database.
- **Jangan kehilangan `secret.key`.** Kunci ini dipakai untuk mendekripsi password database yang disimpan panel dan secret TOTP; tanpa kunci tersebut data terenkripsi tidak dapat dipulihkan.
- Salin `data_dir` saat panel berhenti agar `panel.db` dan berkas WAL-nya konsisten.
- Sertifikat situs berada di `ssl_dir`; akun ACME dan data pendukungnya berada di bawah `data_dir`.

### Firewall

Buka port panel (`7800` atau nilai `listen`) dan port situs (`80`/`443`). Saat UFW diaktifkan dari panel, aturan izin untuk `sshd_port` dan port panel ditambahkan lebih dulu. Pastikan port SSH sudah diizinkan sebelum mengaktifkan firewall dari luar panel.

## Keamanan

- **Password**: hash Argon2id (`m=64 MiB`, `t=3`, `p=4`); panjang minimum 10 karakter dan maksimum 200 karakter.
- **Sesi**: token disimpan sebagai hash di server, cookie `gopanel_session` bersifat `HttpOnly` dan `SameSite=Strict`, dengan batas idle dan absolut. Mengganti password mencabut sesi lain; pengguna dapat mencabut sesi sendiri dari halaman Akun Saya.
- **CSRF**: permintaan non-GET memerlukan header `X-CSRF-Token` yang cocok dengan cookie `gopanel_csrf`, ditambah pemeriksaan `Origin`/`Sec-Fetch-Site`.
- **Rate limit**: login dibatasi 5 kegagalan per 15 menit untuk kombinasi IP+username; endpoint API dibatasi 1200 permintaan per menit per IP.
- **2FA**: TOTP dengan 10 kode pemulihan sekali pakai; token pra-autentikasi berlaku 5 menit.
- **RBAC**: role `admin`, `operator`, `viewer`. Pengguna, pengaturan, audit, tiket terminal, dan WebSocket terminal khusus admin.
- **Terminal**: tiket 30 detik, sekali pakai, terikat user dan IP; koneksi WebSocket harus same-origin; idle 30 menit; shell berjalan sebagai root. Nonaktifkan dengan `terminal_enabled: false` bila tidak diperlukan.
- **Enkripsi**: password database dan secret TOTP dienkripsi AES-GCM dengan `secret.key` 32 byte; berkas kunci dibuat dengan mode `0600`.
- **Audit**: aksi sensitif dan penolakan izin dicatat; retensi default 90 hari.
- **Header**: CSP `default-src 'self'`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, dan `Permissions-Policy` membatasi kamera/mikrofon/lokasi.
- **Rate limit bukan pengganti TLS**: password admin pertama dicetak ke stdout dan log; batasi akses ke log dan jalankan panel hanya di jaringan tepercaya atau di belakang TLS.

## Batasan saat ini

- Belum ada test otomatis (`*_test.go`); `go test ./...` hanya melaporkan `no test files`.
- Belum ada unit systemd, Makefile, atau skrip deploy di repo.
- `-dev` bukan simulator penuh di Linux: backend sistem tetap nyata, hanya default config dan kewajiban ganti password yang berubah.
- Frontend tidak memiliki dev server atau hot reload; setiap perubahan `web/` memerlukan rebuild.

## Peta file dan alur

Untuk peta lengkap berkas, tanggung jawab paket, alur request API, alur job/SSE, serta hubungan frontend–backend, lihat [`map_flow.md`](map_flow.md).
