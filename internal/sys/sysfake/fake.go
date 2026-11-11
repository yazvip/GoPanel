package sysfake

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"gopanel/internal/sys"
)

type System struct {
	mu       sync.Mutex
	start    time.Time
	host     sys.HostInfo
	services map[string]*sys.ServiceInfo
	configs  map[string]string
	enabled  map[string]bool
	dbs      map[string]int64
	dbUsers  []sys.DBUser
	packages map[string]string
	firewall sys.FirewallStatus
}

func New() *System {
	hostname, _ := os.Hostname()
	now := time.Now()
	fw := sys.FirewallStatus{
		Supported:       true,
		Active:          false,
		DefaultIncoming: "deny",
		DefaultOutgoing: "allow",
		Rules: []sys.FirewallRule{
			{Number: 1, To: "22", Proto: "tcp", Action: "allow", From: "Anywhere"},
			{Number: 2, To: "80", Proto: "tcp", Action: "allow", From: "Anywhere"},
			{Number: 3, To: "443", Proto: "tcp", Action: "allow", From: "Anywhere"},
			{Number: 4, To: "7800", Proto: "tcp", Action: "allow", From: "Anywhere"},
		},
	}
	return &System{
		start: now,
		host: sys.HostInfo{
			Hostname:  hostname,
			OS:        "Ubuntu 24.04 LTS (simulasi dev)",
			Platform:  "Ubuntu 24.04 (simulated)",
			Kernel:    "6.8.0-fake",
			Arch:      runtime.GOARCH,
			CPUs:      runtime.NumCPU(),
			MemTotal:  8 << 30,
			DiskTotal: 80 << 30,
			LocalIP:   "127.0.0.1",
			PublicIP:  "203.0.113.10",
			Mounts: []sys.MountInfo{
				{Device: "/dev/vda1", Mountpoint: "/", FSType: "ext4", Total: 80 << 30, Used: 25 << 30, Free: 55 << 30},
				{Device: "/dev/vdb1", Mountpoint: "/var", FSType: "xfs", Total: 200 << 30, Used: 61 << 30, Free: 139 << 30},
			},
		},
		services: map[string]*sys.ServiceInfo{
			"nginx":        {Name: "nginx", Description: "A high performance web server", Active: "active", Sub: "running", Load: "loaded"},
			"mysql":        {Name: "mysql", Description: "MySQL Community Server", Active: "active", Sub: "running", Load: "loaded"},
			"php8.3-fpm":   {Name: "php8.3-fpm", Description: "The PHP 8.3 FastCGI Process Manager", Active: "active", Sub: "running", Load: "loaded"},
			"cron":         {Name: "cron", Description: "Regular background program processing daemon", Active: "active", Sub: "running", Load: "loaded"},
			"redis-server": {Name: "redis-server", Description: "Advanced key-value store", Active: "inactive", Sub: "dead", Load: "loaded"},
			"fail2ban":     {Name: "fail2ban", Description: "Ban hosts that cause multiple authentication errors", Active: "inactive", Sub: "dead", Load: "loaded"},
			"ufw":          {Name: "ufw", Description: "Uncomplicated firewall", Active: "active", Sub: "exited", Load: "loaded"},
		},
		configs:  map[string]string{},
		enabled:  map[string]bool{},
		dbs:      map[string]int64{},
		dbUsers:  []sys.DBUser{},
		packages: map[string]string{"nginx": "1.24.0-2ubuntu7"},
		firewall: fw,
	}
}

func (s *System) Run(ctx context.Context, name string, args []string, opts sys.ExecOpts) (sys.Result, error) {
	var res sys.Result
	base := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(name, "/usr/bin/"), ".exe"))
	switch {
	case strings.HasSuffix(base, "bash"), strings.HasSuffix(base, "sh"):
		cmdline := ""
		for i, a := range args {
			if a == "-c" && i+1 < len(args) {
				cmdline = args[i+1]
			}
		}
		res.Stdout = fmt.Sprintf("[dev mode] command accepted: %s\nOK\n", cmdline)
	case strings.HasSuffix(base, "systemctl"):
		res.Stdout = "active\n"
	case strings.HasSuffix(base, "dpkg-query"):
		res.Stdout = "nginx\t1.24.0-2ubuntu7\n"
	default:
		res.Stdout = ""
	}
	if opts.StdinReader != nil {
		_, _ = io.Copy(io.Discard, opts.StdinReader)
	}
	return res, nil
}

func (s *System) RunIO(ctx context.Context, name string, args []string, opts sys.ExecOpts, stdout, stderr io.Writer) (sys.Result, error) {
	res, err := s.Run(ctx, name, args, opts)
	if stdout != nil {
		_, _ = io.WriteString(stdout, res.Stdout)
		res.Stdout = ""
	}
	if stderr != nil && res.Stderr != "" {
		_, _ = io.WriteString(stderr, res.Stderr)
	}
	return res, err
}

func (s *System) Stream(ctx context.Context, name string, args []string, opts sys.ExecOpts, sink func(string)) (sys.Result, error) {
	res, err := s.Run(ctx, name, args, opts)
	if sink != nil {
		for _, line := range strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n") {
			if line != "" {
				sink(line)
			}
		}
	}
	return res, err
}

func (s *System) HostInfo(ctx context.Context) (sys.HostInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.host
	h.Uptime = uint64(time.Since(s.start).Seconds())
	return h, nil
}

func (s *System) Snapshot(ctx context.Context) (sys.Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := time.Since(s.start).Seconds()
	cpuPct := 14 + 9*math.Sin(t/25) + 3*math.Sin(t/5)
	memUsed := uint64(float64(s.host.MemTotal) * (0.38 + 0.06*math.Sin(t/40)))
	diskUsed := uint64(float64(s.host.DiskTotal) * 0.31)
	return sys.Sample{
		TS:        time.Now().Unix(),
		CPU:       math.Max(1, math.Min(99, cpuPct)),
		MemUsed:   memUsed,
		MemTotal:  s.host.MemTotal,
		SwapUsed:  0,
		DiskUsed:  diskUsed,
		DiskTotal: s.host.DiskTotal,
		NetRx:     uint64(1.2e9 + t*180000),
		NetTx:     uint64(3.4e8 + t*70000),
		Load1:     0.42 + 0.2*math.Sin(t/30),
	}, nil
}

func (s *System) Processes(ctx context.Context, sortBy string, limit int) ([]sys.ProcessInfo, error) {
	procs := []sys.ProcessInfo{
		{PID: 1, Name: "systemd", User: "root", CPU: 0.1, Mem: 0.4, Command: "/sbin/init"},
		{PID: 412, Name: "nginx", User: "www-data", CPU: 1.8, Mem: 0.9, Command: "nginx: worker process"},
		{PID: 618, Name: "mysqld", User: "mysql", CPU: 4.2, Mem: 12.4, Command: "/usr/sbin/mysqld"},
		{PID: 733, Name: "php-fpm8.3", User: "www-data", CPU: 0.7, Mem: 2.1, Command: "php-fpm: pool www"},
		{PID: int32(os.Getpid()), Name: "gopanel", User: "root", CPU: 0.9, Mem: 1.3, Command: "gopanel -dev"},
	}
	switch sortBy {
	case "mem":
		sort.Slice(procs, func(i, j int) bool { return procs[i].Mem > procs[j].Mem })
	case "pid":
		sort.Slice(procs, func(i, j int) bool { return procs[i].PID < procs[j].PID })
	case "name":
		sort.Slice(procs, func(i, j int) bool { return procs[i].Name < procs[j].Name })
	default:
		sort.Slice(procs, func(i, j int) bool { return procs[i].CPU > procs[j].CPU })
	}
	if limit > 0 && len(procs) > limit {
		procs = procs[:limit]
	}
	return procs, nil
}

func (s *System) KillProcess(ctx context.Context, pid int32) error {
	if pid <= 1 {
		return errors.New("refusing to kill pid 1")
	}
	if int(pid) == os.Getpid() {
		return errors.New("refusing to kill the panel process")
	}
	return nil
}

func (s *System) ListServices(ctx context.Context) ([]sys.ServiceInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []sys.ServiceInfo
	for _, svc := range s.services {
		out = append(out, *svc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *System) ServiceAction(ctx context.Context, name, action string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[name]
	if !ok {
		return fmt.Errorf("unit %s not found", name)
	}
	switch action {
	case "start", "restart":
		svc.Active, svc.Sub = "active", "running"
	case "stop":
		svc.Active, svc.Sub = "inactive", "dead"
	case "reload":
		if svc.Active != "active" {
			return fmt.Errorf("unit %s is not active", name)
		}
	default:
		return errors.New("invalid action")
	}
	return nil
}

func (s *System) ServiceLogs(ctx context.Context, name string, lines int) (string, error) {
	var b strings.Builder
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, "2026-11-11T10:%02d:00+0000 %s[%d]: simulated log line %d\n", i, name, 100+i, i+1)
	}
	return b.String(), nil
}

func (s *System) NginxTest(ctx context.Context) error   { return nil }
func (s *System) NginxReload(ctx context.Context) error { return nil }

func (s *System) ReadSiteConfig(ctx context.Context, name string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.configs[name]
	return c, ok, nil
}

func (s *System) WriteSiteConfig(ctx context.Context, name, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configs[name] = content
	return nil
}

func (s *System) RemoveSiteConfig(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.configs, name)
	delete(s.enabled, name)
	return nil
}

func (s *System) SetSiteEnabled(ctx context.Context, name string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled[name] = enabled
	return nil
}

func (s *System) SiteLogs(ctx context.Context, name, kind string, lines int) (string, error) {
	var b strings.Builder
	for i := 0; i < 12; i++ {
		if kind == "error" {
			fmt.Fprintf(&b, "2026/11/11 10:%02d:00 [error] 100#0: simulated %s error line %d\n", i, name, i+1)
		} else {
			fmt.Fprintf(&b, "127.0.0.1 - - [11/Nov/2026:10:%02d:00 +0000] \"GET / HTTP/1.1\" 200 615 \"-\" \"Mozilla/5.0\"\n", i)
		}
	}
	return b.String(), nil
}

func (s *System) PHPVersions(ctx context.Context) ([]string, error) {
	return []string{"8.3", "8.2"}, nil
}

func (s *System) MySQLListDatabases(ctx context.Context) ([]sys.DBInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []sys.DBInfo
	for name, size := range s.dbs {
		out = append(out, sys.DBInfo{Name: name, SizeBytes: size, Tables: 1})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *System) MySQLCreateDatabase(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dbs[name]; ok {
		return fmt.Errorf("database %s already exists", name)
	}
	s.dbs[name] = 0
	return nil
}

func (s *System) MySQLDropDatabase(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dbs[name]; !ok {
		return fmt.Errorf("database %s does not exist", name)
	}
	delete(s.dbs, name)
	return nil
}

func (s *System) MySQLListUsers(ctx context.Context) ([]sys.DBUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]sys.DBUser{}, s.dbUsers...)
	return out, nil
}

func (s *System) MySQLCreateDatabaseUser(ctx context.Context, user, password, database string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dbs[database]; !ok {
		return fmt.Errorf("database %s does not exist", database)
	}
	for _, u := range s.dbUsers {
		if u.User == user {
			return nil
		}
	}
	s.dbUsers = append(s.dbUsers, sys.DBUser{User: user, Host: "localhost"})
	return nil
}

func (s *System) MySQLDropUser(ctx context.Context, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.dbUsers[:0]
	for _, u := range s.dbUsers {
		if u.User != user {
			filtered = append(filtered, u)
		}
	}
	s.dbUsers = filtered
	return nil
}

func (s *System) MySQLSetPassword(ctx context.Context, user, password string) error { return nil }

func (s *System) MySQLDump(ctx context.Context, database string, w io.Writer) error {
	s.mu.Lock()
	_, ok := s.dbs[database]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("database %s does not exist", database)
	}
	_, err := fmt.Fprintf(w, "-- gopanel dev dump of %s\nCREATE TABLE demo (id INT PRIMARY KEY);\n", database)
	return err
}

func (s *System) MySQLRestore(ctx context.Context, database string, r io.Reader) error {
	if _, err := io.Copy(io.Discard, r); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dbs[database]; !ok {
		return fmt.Errorf("database %s does not exist", database)
	}
	return nil
}

func (s *System) AptUpdate(ctx context.Context, sink func(string)) error {
	if sink != nil {
		sink("Hit:1 http://archive.ubuntu.com/ubuntu noble InRelease")
		sink("Reading package lists... Done")
	}
	return nil
}

func (s *System) AptInstall(ctx context.Context, packages []string, sink func(string)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range packages {
		if sink != nil {
			sink("Setting up " + p + " (simulated)...")
		}
		s.packages[p] = "simulated-1.0"
	}
	return nil
}

func (s *System) AptInstalled(ctx context.Context) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.packages {
		out[k] = v
	}
	return out, nil
}

func (s *System) UFWStatus(ctx context.Context) (sys.FirewallStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firewall, nil
}

func (s *System) UFWEnable(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.firewall.Active = true
	return nil
}

func (s *System) UFWDisable(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.firewall.Active = false
	return nil
}

func (s *System) UFWAddRule(ctx context.Context, rule sys.FirewallRuleInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	number := 1
	for _, r := range s.firewall.Rules {
		if r.Number >= number {
			number = r.Number + 1
		}
	}
	from := rule.From
	if from == "" {
		from = "Anywhere"
	}
	s.firewall.Rules = append(s.firewall.Rules, sys.FirewallRule{Number: number, To: rule.Port, Proto: rule.Proto, Action: rule.Action, From: from, Comment: rule.Comment})
	return nil
}

func (s *System) UFWDeleteRule(ctx context.Context, number int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []sys.FirewallRule
	for _, r := range s.firewall.Rules {
		if r.Number == number {
			continue
		}
		out = append(out, r)
	}
	if len(out) == len(s.firewall.Rules) {
		return fmt.Errorf("rule %d not found", number)
	}
	s.firewall.Rules = out
	return nil
}

type echoSession struct {
	mu      sync.Mutex
	pending []byte
	ch      chan []byte
	done    chan struct{}
	closed  bool
}

func (s *System) StartTerminal(ctx context.Context, cols, rows uint16) (sys.TerminalSession, error) {
	sess := &echoSession{ch: make(chan []byte, 64), done: make(chan struct{})}
	banner := "GoPanel dev terminal (simulated shell)\r\nType any command; inputs are echoed back.\r\nroot@gopanel-dev:~# "
	sess.ch <- []byte(banner)
	return sess, nil
}

func (e *echoSession) Read(p []byte) (int, error) {
	e.mu.Lock()
	if len(e.pending) > 0 {
		n := copy(p, e.pending)
		e.pending = e.pending[n:]
		e.mu.Unlock()
		return n, nil
	}
	e.mu.Unlock()
	select {
	case <-e.done:
		return 0, io.EOF
	case chunk, ok := <-e.ch:
		if !ok {
			return 0, io.EOF
		}
		n := copy(p, chunk)
		if n < len(chunk) {
			e.mu.Lock()
			e.pending = append(e.pending, chunk[n:]...)
			e.mu.Unlock()
		}
		return n, nil
	}
}

func (e *echoSession) Write(p []byte) (int, error) {
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return 0, io.ErrClosedPipe
	}
	text := strings.ReplaceAll(string(p), "\r", "\r\n")
	if strings.Contains(text, "\x03") {
		e.send("\r\n^C\r\nroot@gopanel-dev:~# ")
	} else if strings.HasSuffix(text, "\r\n") || strings.HasSuffix(text, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r"))
		e.send(text + fmt.Sprintf("[dev] executed %q (simulated)\r\nroot@gopanel-dev:~# ", line))
	} else {
		e.send(text)
	}
	return len(p), nil
}

func (e *echoSession) send(text string) {
	select {
	case e.ch <- []byte(text):
	default:
	}
}

func (e *echoSession) Resize(cols, rows uint16) error { return nil }

func (e *echoSession) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		e.closed = true
		close(e.done)
	}
	return nil
}

func (e *echoSession) Wait() error { return nil }
