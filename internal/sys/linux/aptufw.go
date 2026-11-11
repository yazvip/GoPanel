//go:build linux

package linux

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopanel/internal/sys"
)

func (s *System) AptUpdate(ctx context.Context, sink func(string)) error {
	_, err := s.Stream(ctx, "apt-get", []string{"update", "-y"}, sys.ExecOpts{Timeout: 10 * time.Minute}, sink)
	return err
}

func (s *System) AptInstall(ctx context.Context, packages []string, sink func(string)) error {
	if len(packages) == 0 {
		return errors.New("no packages requested")
	}
	args := []string{"install", "-y", "--no-install-recommends",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
		"-o", "DPkg::Lock::Timeout=60"}
	args = append(args, packages...)
	_, err := s.Stream(ctx, "apt-get", args, sys.ExecOpts{Timeout: 30 * time.Minute, MaxOutput: 2 << 20}, sink)
	return err
}

func (s *System) AptInstalled(ctx context.Context) (map[string]string, error) {
	res, err := s.Run(ctx, "dpkg-query", []string{"-W", "-f=${Package}\\t${Version}\\n"}, sys.ExecOpts{Timeout: 30 * time.Second, MaxOutput: 8 << 20})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		out[fields[0]] = fields[1]
	}
	return out, nil
}

var ufwRuleRe = regexp.MustCompile(`^\[\s*(\d+)\]\s+(.+?)\s{2,}(ALLOW|DENY|REJECT|LIMIT)\s+(IN|OUT)\s+(.+)$`)

func (s *System) UFWStatus(ctx context.Context) (sys.FirewallStatus, error) {
	status := sys.FirewallStatus{Rules: []sys.FirewallRule{}}
	res, err := s.Run(ctx, "ufw", []string{"status", "numbered"}, sys.ExecOpts{Timeout: 15 * time.Second})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") || strings.Contains(strings.ToLower(err.Error()), "no such file") {
			return status, nil
		}
		return status, err
	}
	status.Supported = true
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "Status:") {
			status.Active = strings.Contains(line, "active")
			continue
		}
		m := ufwRuleRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		num, _ := strconv.Atoi(m[1])
		to := strings.TrimSpace(m[2])
		proto := ""
		if idx := strings.LastIndex(to, "/"); idx >= 0 {
			proto = to[idx+1:]
			to = to[:idx]
		}
		from := strings.TrimSpace(m[5])
		if idx := strings.Index(from, "#"); idx >= 0 {
			from = strings.TrimSpace(from[:idx])
		}
		status.Rules = append(status.Rules, sys.FirewallRule{Number: num, To: to, Action: strings.ToLower(m[3]), Proto: proto, From: from})
	}
	verbose, verr := s.Run(ctx, "ufw", []string{"status", "verbose"}, sys.ExecOpts{Timeout: 15 * time.Second})
	if verr == nil {
		for _, line := range strings.Split(verbose.Stdout, "\n") {
			if strings.HasPrefix(line, "Default:") {
				parts := strings.Split(strings.TrimPrefix(line, "Default:"), ",")
				if len(parts) >= 2 {
					status.DefaultIncoming = strings.TrimSpace(strings.TrimSuffix(parts[0], "(incoming)"))
					status.DefaultOutgoing = strings.TrimSpace(strings.TrimSuffix(parts[1], "(outgoing)"))
				}
			}
		}
	}
	return status, nil
}

func (s *System) UFWEnable(ctx context.Context) error {
	if _, err := s.Run(ctx, "ufw", []string{"default", "deny", "incoming"}, sys.ExecOpts{Timeout: 15 * time.Second}); err != nil {
		return err
	}
	if _, err := s.Run(ctx, "ufw", []string{"default", "allow", "outgoing"}, sys.ExecOpts{Timeout: 15 * time.Second}); err != nil {
		return err
	}
	_, err := s.Run(ctx, "ufw", []string{"--force", "enable"}, sys.ExecOpts{Timeout: 30 * time.Second})
	return err
}

func (s *System) UFWDisable(ctx context.Context) error {
	_, err := s.Run(ctx, "ufw", []string{"--force", "disable"}, sys.ExecOpts{Timeout: 30 * time.Second})
	return err
}

func (s *System) UFWAddRule(ctx context.Context, rule sys.FirewallRuleInput) error {
	action := strings.ToLower(rule.Action)
	if action == "" {
		action = "allow"
	}
	args := []string{action}
	from := strings.TrimSpace(rule.From)
	if from != "" && from != "any" {
		args = append(args, "from", from, "to", "any", "port", rule.Port)
		if rule.Proto == "tcp" || rule.Proto == "udp" {
			args = append(args, "proto", rule.Proto)
		}
	} else {
		port := rule.Port
		if rule.Proto == "tcp" || rule.Proto == "udp" {
			port += "/" + rule.Proto
		}
		args = append(args, port)
	}
	if rule.Comment != "" {
		args = append(args, "comment", rule.Comment)
	}
	_, err := s.Run(ctx, "ufw", args, sys.ExecOpts{Timeout: 30 * time.Second})
	return err
}

func (s *System) UFWDeleteRule(ctx context.Context, number int) error {
	if number <= 0 {
		return errors.New("invalid rule number")
	}
	_, err := s.Run(ctx, "ufw", []string{"--force", "delete", strconv.Itoa(number)}, sys.ExecOpts{Timeout: 30 * time.Second})
	return err
}
