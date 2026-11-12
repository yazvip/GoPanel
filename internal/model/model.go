package model

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

const (
	SiteTypeStatic = "static"
	SiteTypePHP    = "php"
	SiteTypeProxy  = "proxy"
)

type User struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	PasswordHash       string `json:"-"`
	Role               string `json:"role"`
	Locale             string `json:"locale"`
	TOTPSecretEnc      []byte `json:"-"`
	TOTPEnabled        bool   `json:"totp_enabled"`
	TOTPLastStep       int64  `json:"-"`
	Disabled           bool   `json:"disabled"`
	MustChangePassword bool   `json:"must_change_password"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
	LastLoginAt        int64  `json:"last_login_at"`
}

type Session struct {
	ID         string `json:"id"`
	UserID     int64  `json:"user_id"`
	CSRF       string `json:"-"`
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	ExpiresAt  int64  `json:"expires_at"`
}

type Site struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	RootPath      string   `json:"root_path"`
	Type          string   `json:"type"`
	PHPVersion    string   `json:"php_version,omitempty"`
	Upstream      string   `json:"upstream,omitempty"`
	SSLEnabled    bool     `json:"ssl_enabled"`
	SSLCertPath   string   `json:"ssl_cert_path,omitempty"`
	SSLKeyPath    string   `json:"ssl_key_path,omitempty"`
	SSLExpiresAt  int64    `json:"ssl_expires_at,omitempty"`
	Enabled       bool     `json:"enabled"`
	IndexFile     string   `json:"index_file"`
	ClientMaxBody int64    `json:"client_max_body"`
	AccessLog     bool     `json:"access_log"`
	Domains       []Domain `json:"domains,omitempty"`
	CreatedAt     int64    `json:"created_at"`
	UpdatedAt     int64    `json:"updated_at"`
}

type Domain struct {
	ID        int64  `json:"id"`
	SiteID    int64  `json:"site_id"`
	Domain    string `json:"domain"`
	IsPrimary bool   `json:"is_primary"`
}

type Database struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Username    string `json:"username"`
	PasswordEnc []byte `json:"-"`
	CreatedAt   int64  `json:"created_at"`
}

type Backup struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Ref       string `json:"ref"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

type BackupSchedule struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Ref       string `json:"ref"`
	CronExpr  string `json:"cron_expr"`
	Keep      int    `json:"keep"`
	Enabled   bool   `json:"enabled"`
	LastRunAt int64  `json:"last_run_at"`
	CreatedAt int64  `json:"created_at"`
}

type CronJob struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Schedule   string `json:"schedule"`
	Command    string `json:"command"`
	Enabled    bool   `json:"enabled"`
	LastRunAt  int64  `json:"last_run_at"`
	LastStatus string `json:"last_status"`
	CreatedBy  int64  `json:"created_by"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

type MetricSample struct {
	TS        int64   `json:"ts"`
	CPU       float64 `json:"cpu"`
	MemUsed   uint64  `json:"mem_used"`
	MemTotal  uint64  `json:"mem_total"`
	SwapUsed  uint64  `json:"swap_used"`
	DiskUsed  uint64  `json:"disk_used"`
	DiskTotal uint64  `json:"disk_total"`
	NetRx     uint64  `json:"net_rx"`
	NetTx     uint64  `json:"net_tx"`
	Load1     float64 `json:"load1"`
}

type AuditLog struct {
	ID       int64  `json:"id"`
	TS       int64  `json:"ts"`
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	IP       string `json:"ip"`
	Action   string `json:"action"`
	Target   string `json:"target,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Result   string `json:"result"`
}

type Job struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	Payload    string `json:"payload,omitempty"`
	Progress   int    `json:"progress"`
	Message    string `json:"message,omitempty"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	CreatedBy  int64  `json:"created_by"`
	CreatedAt  int64  `json:"created_at"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
}
