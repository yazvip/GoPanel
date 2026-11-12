package sys

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ErrDatabaseClientMissing is returned when the host has no usable MySQL or
// MariaDB command-line client, so the panel cannot manage databases. Callers
// should surface it as a configuration problem, not as a server failure.
var ErrDatabaseClientMissing = errors.New("no MySQL/MariaDB client installed")

// ErrNginxMissing is returned when the host has no usable nginx binary, so the
// panel cannot validate or reload site configurations. Callers should surface
// it as a configuration problem, not as a server failure.
var ErrNginxMissing = errors.New("nginx is not installed")

type ExecOpts struct {
	Stdin       []byte
	StdinReader io.Reader
	Env         []string
	Dir         string
	Timeout     time.Duration
	MaxOutput   int
}

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type Executor interface {
	Run(ctx context.Context, name string, args []string, opts ExecOpts) (Result, error)
	RunIO(ctx context.Context, name string, args []string, opts ExecOpts, stdout, stderr io.Writer) (Result, error)
	Stream(ctx context.Context, name string, args []string, opts ExecOpts, sink func(string)) (Result, error)
}

type ExecError struct {
	Name   string
	Args   []string
	Result Result
	Err    error
}

func (e *ExecError) Error() string {
	msg := strings.TrimSpace(e.Result.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(e.Result.Stdout)
	}
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("%s: %s", e.Name, msg)
}

func (e *ExecError) Unwrap() error { return e.Err }

type HostInfo struct {
	Hostname  string      `json:"hostname"`
	OS        string      `json:"os"`
	Platform  string      `json:"platform"`
	Kernel    string      `json:"kernel"`
	Arch      string      `json:"arch"`
	Uptime    uint64      `json:"uptime"`
	CPUs      int         `json:"cpus"`
	MemTotal  uint64      `json:"mem_total"`
	DiskTotal uint64      `json:"disk_total"`
	LocalIP   string      `json:"local_ip"`
	PublicIP  string      `json:"public_ip"`
	Mounts    []MountInfo `json:"mounts,omitempty"`
}

type MountInfo struct {
	Device     string `json:"device"`
	Mountpoint string `json:"mountpoint"`
	FSType     string `json:"fstype"`
	Total      uint64 `json:"total"`
	Used       uint64 `json:"used"`
	Free       uint64 `json:"free"`
}

type Sample struct {
	TS        int64   `json:"ts"`
	CPU       float64 `json:"cpu"`
	MemUsed   uint64  `json:"mem_used"`
	MemTotal  uint64  `json:"mem_total"`
	SwapUsed  uint64  `json:"swap_used"`
	SwapTotal uint64  `json:"swap_total"`
	DiskUsed  uint64  `json:"disk_used"`
	DiskTotal uint64  `json:"disk_total"`
	NetRx     uint64  `json:"net_rx"`
	NetTx     uint64  `json:"net_tx"`
	Load1     float64 `json:"load1"`
}

type ProcessInfo struct {
	PID     int32   `json:"pid"`
	Name    string  `json:"name"`
	User    string  `json:"user"`
	CPU     float64 `json:"cpu"`
	Mem     float64 `json:"mem"`
	Command string  `json:"command"`
}

type ServiceInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
}

type DBInfo struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size"`
	Tables    int    `json:"tables"`
}

type DBUser struct {
	User string `json:"user"`
	Host string `json:"host"`
}

type FirewallRule struct {
	Number  int    `json:"number"`
	To      string `json:"to"`
	Action  string `json:"action"`
	From    string `json:"from"`
	Proto   string `json:"proto"`
	Comment string `json:"comment,omitempty"`
}

type FirewallRuleInput struct {
	Port    string `json:"port"`
	Proto   string `json:"proto"`
	Action  string `json:"action"`
	From    string `json:"from"`
	Comment string `json:"comment"`
}

type FirewallStatus struct {
	Active          bool           `json:"active"`
	Supported       bool           `json:"supported"`
	DefaultIncoming string         `json:"default_incoming"`
	DefaultOutgoing string         `json:"default_outgoing"`
	Rules           []FirewallRule `json:"rules"`
}

type TerminalSession interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows uint16) error
	Close() error
	Wait() error
}

type System interface {
	Executor
	HostInfo(ctx context.Context) (HostInfo, error)
	Snapshot(ctx context.Context) (Sample, error)
	Processes(ctx context.Context, sortBy string, limit int) ([]ProcessInfo, error)
	KillProcess(ctx context.Context, pid int32) error

	ListServices(ctx context.Context) ([]ServiceInfo, error)
	ServiceAction(ctx context.Context, name, action string) error
	ServiceLogs(ctx context.Context, name string, lines int) (string, error)

	NginxTest(ctx context.Context) error
	NginxReload(ctx context.Context) error
	ReadSiteConfig(ctx context.Context, name string) (string, bool, error)
	WriteSiteConfig(ctx context.Context, name, content string) error
	RemoveSiteConfig(ctx context.Context, name string) error
	SetSiteEnabled(ctx context.Context, name string, enabled bool) error
	SiteLogs(ctx context.Context, name, kind string, lines int) (string, error)
	PHPVersions(ctx context.Context) ([]string, error)

	MySQLListDatabases(ctx context.Context) ([]DBInfo, error)
	MySQLCreateDatabase(ctx context.Context, name string) error
	MySQLDropDatabase(ctx context.Context, name string) error
	MySQLListUsers(ctx context.Context) ([]DBUser, error)
	MySQLCreateDatabaseUser(ctx context.Context, user, password, database string) error
	MySQLDropUser(ctx context.Context, user string) error
	MySQLSetPassword(ctx context.Context, user, password string) error
	MySQLDump(ctx context.Context, database string, w io.Writer) error
	MySQLRestore(ctx context.Context, database string, r io.Reader) error

	AptUpdate(ctx context.Context, sink func(string)) error
	AptInstall(ctx context.Context, packages []string, sink func(string)) error
	AptInstalled(ctx context.Context) (map[string]string, error)

	UFWStatus(ctx context.Context) (FirewallStatus, error)
	UFWEnable(ctx context.Context) error
	UFWDisable(ctx context.Context) error
	UFWAddRule(ctx context.Context, rule FirewallRuleInput) error
	UFWDeleteRule(ctx context.Context, number int) error

	StartTerminal(ctx context.Context, cols, rows uint16) (TerminalSession, error)
}
