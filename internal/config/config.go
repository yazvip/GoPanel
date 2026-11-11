package config

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type AllowedRoot struct {
	Token string `json:"token"`
	Label string `json:"label"`
	Path  string `json:"path"`
}

type TLS struct {
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

type Config struct {
	Listen               string        `json:"listen"`
	DataDir              string        `json:"data_dir"`
	DBPath               string        `json:"db_path"`
	LogLevel             string        `json:"log_level"`
	PanelURL             string        `json:"panel_url"`
	AllowedRoots         []AllowedRoot `json:"allowed_roots"`
	UploadMaxBytes       int64         `json:"upload_max_bytes"`
	SessionIdleHours     int           `json:"session_idle_hours"`
	SessionAbsoluteHours int           `json:"session_absolute_hours"`
	ACMEEmail            string        `json:"acme_email"`
	ACMEStaging          bool          `json:"acme_staging"`
	ACMEWebroot          string        `json:"acme_webroot"`
	SSLDir               string        `json:"ssl_dir"`
	BackupDir            string        `json:"backup_dir"`
	LogDir               string        `json:"log_dir"`
	CronFile             string        `json:"cron_file"`
	SiteRootBase         string        `json:"site_root_base"`
	NginxSitesAvailable  string        `json:"nginx_sites_available"`
	NginxSitesEnabled    string        `json:"nginx_sites_enabled"`
	NginxConf            string        `json:"nginx_conf"`
	NginxLogDir          string        `json:"nginx_log_dir"`
	PHPConfDir           string        `json:"php_conf_dir"`
	MySQLAdminUser       string        `json:"mysql_admin_user"`
	MySQLAdminPassword   string        `json:"mysql_admin_password"`
	MySQLSocket          string        `json:"mysql_socket"`
	SSHDPort             int           `json:"sshd_port"`
	TerminalEnabled      bool          `json:"terminal_enabled"`
	BehindProxy          bool          `json:"behind_proxy"`
	TrustedProxies       []string      `json:"trusted_proxies"`
	TLS                  TLS           `json:"tls"`
	SecretKeyPath        string        `json:"secret_key_path"`

	Dev        bool   `json:"-"`
	ConfigPath string `json:"-"`
}

func Defaults(dev bool) *Config {
	if dev {
		root := filepath.Join("data-dev", "roots")
		return &Config{
			Listen:               "127.0.0.1:7800",
			DataDir:              "data-dev",
			DBPath:               filepath.Join("data-dev", "panel.db"),
			LogLevel:             "debug",
			AllowedRoots:         []AllowedRoot{{Token: "var-www", Label: "Web root", Path: filepath.Join(root, "var-www")}, {Token: "home", Label: "Home", Path: filepath.Join(root, "home")}},
			UploadMaxBytes:       2 << 30,
			SessionIdleHours:     12,
			SessionAbsoluteHours: 168,
			ACMEWebroot:          filepath.Join("data-dev", "acme"),
			SSLDir:               filepath.Join("data-dev", "ssl"),
			BackupDir:            filepath.Join("data-dev", "backups"),
			LogDir:               filepath.Join("data-dev", "logs"),
			CronFile:             filepath.Join("data-dev", "cron.d", "gopanel"),
			SiteRootBase:         filepath.Join("data-dev", "www"),
			NginxSitesAvailable:  filepath.Join("data-dev", "nginx", "sites-available"),
			NginxSitesEnabled:    filepath.Join("data-dev", "nginx", "sites-enabled"),
			NginxConf:            filepath.Join("data-dev", "nginx", "nginx.conf"),
			NginxLogDir:          filepath.Join("data-dev", "nginx", "logs"),
			PHPConfDir:           filepath.Join("data-dev", "php"),
			MySQLAdminUser:       "root",
			MySQLSocket:          "",
			SSHDPort:             22,
			TerminalEnabled:      true,
			SecretKeyPath:        filepath.Join("data-dev", "secret.key"),
		}
	}
	return &Config{
		Listen:               "0.0.0.0:7800",
		DataDir:              "/var/lib/gopanel",
		DBPath:               "/var/lib/gopanel/panel.db",
		LogLevel:             "info",
		AllowedRoots:         []AllowedRoot{{Token: "var-www", Label: "Web root", Path: "/var/www"}, {Token: "home", Label: "Home", Path: "/home"}, {Token: "srv", Label: "Services", Path: "/srv"}, {Token: "opt", Label: "Optional", Path: "/opt"}},
		UploadMaxBytes:       2 << 30,
		SessionIdleHours:     12,
		SessionAbsoluteHours: 168,
		ACMEWebroot:          "/var/lib/gopanel/acme",
		SSLDir:               "/etc/gopanel/ssl",
		BackupDir:            "/var/backups/gopanel",
		LogDir:               "/var/log/gopanel",
		CronFile:             "/etc/cron.d/gopanel",
		SiteRootBase:         "/var/www",
		NginxSitesAvailable:  "/etc/nginx/sites-available",
		NginxSitesEnabled:    "/etc/nginx/sites-enabled",
		NginxConf:            "/etc/nginx/nginx.conf",
		NginxLogDir:          "/var/log/nginx",
		PHPConfDir:           "/etc/php",
		MySQLAdminUser:       "root",
		MySQLSocket:          "/run/mysqld/mysqld.sock",
		SSHDPort:             22,
		TerminalEnabled:      true,
		SecretKeyPath:        "/var/lib/gopanel/secret.key",
	}
}

func Load(path string, dev bool) (*Config, error) {
	cfg := Defaults(dev)
	cfg.Dev = dev
	if path == "" {
		path = "config.json"
	}
	cfg.ConfigPath = path
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read config: %w", err)
	}
	applyEnv(cfg)
	cfg.Dev = dev
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(cfg.DataDir, "panel.db")
	}
	if cfg.SecretKeyPath == "" {
		cfg.SecretKeyPath = filepath.Join(cfg.DataDir, "secret.key")
	}
	for _, dir := range []string{cfg.DataDir, cfg.LogDir, cfg.BackupDir, cfg.ACMEWebroot, cfg.SSLDir, filepath.Dir(cfg.DBPath), filepath.Dir(cfg.CronFile), cfg.SiteRootBase, cfg.NginxSitesAvailable, cfg.NginxSitesEnabled, cfg.NginxLogDir, filepath.Dir(cfg.NginxConf)} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", dir, err)
		}
	}
	for _, root := range cfg.AllowedRoots {
		if root.Path == "" {
			continue
		}
		if err := os.MkdirAll(root.Path, 0o755); err != nil {
			return nil, fmt.Errorf("create root %s: %w", root.Path, err)
		}
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := cfg.Save(); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("GOPANEL_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := os.Getenv("GOPANEL_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("GOPANEL_DB"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("GOPANEL_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("GOPANEL_LOG_DIR"); v != "" {
		cfg.LogDir = v
	}
	if v := os.Getenv("GOPANEL_ACME_WEBROOT"); v != "" {
		cfg.ACMEWebroot = v
	}
	if v := os.Getenv("GOPANEL_SSL_DIR"); v != "" {
		cfg.SSLDir = v
	}
	if v := os.Getenv("GOPANEL_BACKUP_DIR"); v != "" {
		cfg.BackupDir = v
	}
	if v := os.Getenv("GOPANEL_CRON_FILE"); v != "" {
		cfg.CronFile = v
	}
	if v := os.Getenv("GOPANEL_SITE_ROOT"); v != "" {
		cfg.SiteRootBase = v
	}
	if v := os.Getenv("GOPANEL_TLS_CERT"); v != "" {
		cfg.TLS.CertFile = v
	}
	if v := os.Getenv("GOPANEL_TLS_KEY"); v != "" {
		cfg.TLS.KeyFile = v
	}
	if v := os.Getenv("GOPANEL_ACME_EMAIL"); v != "" {
		cfg.ACMEEmail = v
	}
	if v := os.Getenv("GOPANEL_TERMINAL"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.TerminalEnabled = b
		}
	}
}

func (c *Config) Save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(c.ConfigPath), 0o755); err != nil && filepath.Dir(c.ConfigPath) != "." {
		return err
	}
	return os.WriteFile(c.ConfigPath, data, 0o600)
}

func (c *Config) EnsureSecretKey() ([]byte, error) {
	data, err := os.ReadFile(c.SecretKeyPath)
	if err == nil {
		if len(data) != 32 {
			return nil, fmt.Errorf("secret key %s must be 32 bytes", c.SecretKeyPath)
		}
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(c.SecretKeyPath), 0o700); err != nil && filepath.Dir(c.SecretKeyPath) != "." {
		return nil, err
	}
	if err := os.WriteFile(c.SecretKeyPath, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func (c *Config) SecureCookies() bool {
	return c.TLS.CertFile != "" && c.TLS.KeyFile != ""
}

func (c *Config) SiteRoot(name string) string {
	return filepath.Join(c.SiteRootBase, name)
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.Listen) == "" {
		return errors.New("listen address is required")
	}
	if c.SessionIdleHours <= 0 {
		c.SessionIdleHours = 12
	}
	if c.SessionAbsoluteHours <= 0 || c.SessionAbsoluteHours < c.SessionIdleHours {
		c.SessionAbsoluteHours = 168
	}
	if c.UploadMaxBytes <= 0 {
		c.UploadMaxBytes = 2 << 30
	}
	return nil
}
