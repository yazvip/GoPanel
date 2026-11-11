//go:build linux

package linux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"

	"gopanel/internal/config"
	"gopanel/internal/execx"
	"gopanel/internal/sys"
)

type System struct {
	cfg    *config.Config
	runner *execx.Runner
	mu     sync.Mutex
	pubIP  string
	pubAt  time.Time
}

func New(cfg *config.Config) *System {
	return &System{cfg: cfg, runner: execx.New()}
}

func (s *System) Run(ctx context.Context, name string, args []string, opts sys.ExecOpts) (sys.Result, error) {
	return s.runner.Run(ctx, name, args, opts)
}

func (s *System) RunIO(ctx context.Context, name string, args []string, opts sys.ExecOpts, stdout, stderr io.Writer) (sys.Result, error) {
	return s.runner.RunIO(ctx, name, args, opts, stdout, stderr)
}

func (s *System) Stream(ctx context.Context, name string, args []string, opts sys.ExecOpts, sink func(string)) (sys.Result, error) {
	return s.runner.Stream(ctx, name, args, opts, sink)
}

func (s *System) HostInfo(ctx context.Context) (sys.HostInfo, error) {
	var info sys.HostInfo
	hi, err := host.InfoWithContext(ctx)
	if err != nil {
		return info, err
	}
	info.Hostname = hi.Hostname
	info.OS = hi.OS
	info.Platform = strings.TrimSpace(hi.Platform + " " + hi.PlatformVersion)
	info.Kernel = hi.KernelVersion
	info.Arch = hi.KernelArch
	info.Uptime = hi.Uptime
	if cpus, err := cpu.CountsWithContext(ctx, true); err == nil {
		info.CPUs = cpus
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		info.MemTotal = vm.Total
	}
	if du, err := disk.UsageWithContext(ctx, "/"); err == nil {
		info.DiskTotal = du.Total
	}
	info.LocalIP = localIP()
	info.PublicIP = s.publicIP(ctx)
	if parts, err := disk.PartitionsWithContext(ctx, false); err == nil {
		seen := map[string]bool{}
		for _, part := range parts {
			if len(info.Mounts) >= 20 || seen[part.Mountpoint] {
				continue
			}
			seen[part.Mountpoint] = true
			usage, err := disk.UsageWithContext(ctx, part.Mountpoint)
			if err != nil {
				continue
			}
			info.Mounts = append(info.Mounts, sys.MountInfo{
				Device:     part.Device,
				Mountpoint: part.Mountpoint,
				FSType:     part.Fstype,
				Total:      usage.Total,
				Used:       usage.Used,
				Free:       usage.Free,
			})
		}
	}
	return info, nil
}

func localIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if ip.To4() != nil {
				return ip.String()
			}
		}
	}
	return ""
}

func (s *System) publicIP(ctx context.Context) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.pubAt) < 10*time.Minute {
		return s.pubIP
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "udp", "1.1.1.1:53")
	if err != nil {
		return s.pubIP
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		s.pubIP = addr.IP.String()
		s.pubAt = time.Now()
	}
	return s.pubIP
}

func (s *System) Snapshot(ctx context.Context) (sys.Sample, error) {
	now := time.Now().Unix()
	sample := sys.Sample{TS: now}
	if pct, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(pct) > 0 {
		sample.CPU = pct[0]
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		sample.MemUsed = vm.Used
		sample.MemTotal = vm.Total
	}
	if swap, err := mem.SwapMemoryWithContext(ctx); err == nil {
		sample.SwapUsed = swap.Used
		sample.SwapTotal = swap.Total
	}
	if du, err := disk.UsageWithContext(ctx, "/"); err == nil {
		sample.DiskUsed = du.Used
		sample.DiskTotal = du.Total
	}
	if counters, err := gnet.IOCountersWithContext(ctx, false); err == nil && len(counters) > 0 {
		for _, c := range counters {
			sample.NetRx += c.BytesRecv
			sample.NetTx += c.BytesSent
		}
	}
	if avg, err := load.AvgWithContext(ctx); err == nil {
		sample.Load1 = avg.Load1
	}
	return sample, nil
}

func (s *System) Processes(ctx context.Context, sortBy string, limit int) ([]sys.ProcessInfo, error) {
	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]sys.ProcessInfo, 0, len(procs))
	for _, p := range procs {
		if ctx.Err() != nil {
			break
		}
		pi := sys.ProcessInfo{PID: p.Pid}
		if name, err := p.NameWithContext(ctx); err == nil {
			pi.Name = name
		}
		if user, err := p.UsernameWithContext(ctx); err == nil {
			pi.User = user
		}
		if pct, err := p.CPUPercentWithContext(ctx); err == nil {
			pi.CPU = pct
		}
		if pct, err := p.MemoryPercentWithContext(ctx); err == nil {
			pi.Mem = float64(pct)
		}
		if cmdline, err := p.CmdlineWithContext(ctx); err == nil {
			pi.Command = cmdline
		}
		out = append(out, pi)
	}
	switch sortBy {
	case "mem":
		sort.Slice(out, func(i, j int) bool { return out[i].Mem > out[j].Mem })
	case "pid":
		sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	case "name":
		sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	default:
		sort.Slice(out, func(i, j int) bool { return out[i].CPU > out[j].CPU })
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *System) KillProcess(ctx context.Context, pid int32) error {
	if pid <= 1 {
		return errors.New("refusing to kill pid 1")
	}
	if int(pid) == os.Getpid() {
		return errors.New("refusing to kill the panel process")
	}
	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return err
	}
	return p.SendSignalWithContext(ctx, syscall.SIGTERM)
}

func (s *System) ListServices(ctx context.Context) ([]sys.ServiceInfo, error) {
	res, err := s.Run(ctx, "systemctl", []string{"list-units", "--type=service", "--all", "--no-legend", "--plain", "--no-pager"}, sys.ExecOpts{Timeout: 15 * time.Second, MaxOutput: 4 << 20})
	if err != nil {
		return nil, err
	}
	var out []sys.ServiceInfo
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "●"))
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.HasSuffix(fields[0], ".service") {
			continue
		}
		desc := ""
		if len(fields) > 4 {
			desc = strings.Join(fields[4:], " ")
		}
		out = append(out, sys.ServiceInfo{
			Name:        strings.TrimSuffix(fields[0], ".service"),
			Load:        fields[1],
			Active:      fields[2],
			Sub:         fields[3],
			Description: desc,
		})
	}
	return out, nil
}

func (s *System) ServiceAction(ctx context.Context, name, action string) error {
	switch action {
	case "start", "stop", "restart", "reload":
	default:
		return fmt.Errorf("invalid action")
	}
	if name == "gopanel" {
		return errors.New("the panel service cannot be managed from the panel")
	}
	_, err := s.Run(ctx, "systemctl", []string{action, name}, sys.ExecOpts{Timeout: 60 * time.Second})
	return err
}

func (s *System) ServiceLogs(ctx context.Context, name string, lines int) (string, error) {
	if lines <= 0 {
		lines = 200
	}
	if lines > 2000 {
		lines = 2000
	}
	res, err := s.Run(ctx, "journalctl", []string{"-u", name, "-n", strconv.Itoa(lines), "--no-pager", "-o", "short-iso"}, sys.ExecOpts{Timeout: 15 * time.Second, MaxOutput: 2 << 20})
	if err != nil {
		return res.Stdout + res.Stderr, err
	}
	return res.Stdout, nil
}

var phpVersionRe = regexp.MustCompile(`^\d+\.\d+$`)

func (s *System) PHPVersions(ctx context.Context) ([]string, error) {
	dirs, err := os.ReadDir(s.cfg.PHPConfDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	var versions []string
	for _, d := range dirs {
		if !d.IsDir() || !phpVersionRe.MatchString(d.Name()) {
			continue
		}
		if _, err := os.Stat(s.cfg.PHPConfDir + "/" + d.Name() + "/fpm"); err == nil {
			versions = append(versions, d.Name())
		}
	}
	sort.Strings(versions)
	return versions, nil
}

func (s *System) StartTerminal(ctx context.Context, cols, rows uint16) (sys.TerminalSession, error) {
	return s.startPTY(cols, rows)
}
