package validate

import (
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/robfig/cron/v3"
)

var (
	siteNameRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	dbNameRe    = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)
	unitRe      = regexp.MustCompile(`^[A-Za-z0-9@._-]{1,100}$`)
	usernameRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,31}$`)
	labelRe     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

func SiteName(name string) error {
	if !siteNameRe.MatchString(name) {
		return fmt.Errorf("site name must match %s", siteNameRe.String())
	}
	return nil
}

func Username(name string) error {
	if !usernameRe.MatchString(name) {
		return fmt.Errorf("username must be 3-32 characters of letters, digits, dot, dash or underscore")
	}
	return nil
}

func DatabaseName(name string) error {
	if !dbNameRe.MatchString(name) {
		return fmt.Errorf("database name must be 1-32 letters, digits or underscores")
	}
	return nil
}

func DatabaseUser(name string) error {
	return DatabaseName(name)
}

func Domain(domain string) error {
	if domain == "" || len(domain) > 253 {
		return fmt.Errorf("invalid domain length")
	}
	if strings.ContainsAny(domain, " /\\:*?\"<>|%") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return fmt.Errorf("invalid domain")
	}
	if net.ParseIP(domain) != nil {
		return fmt.Errorf("IP addresses are not accepted as domains")
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return fmt.Errorf("domain must contain a dot")
	}
	for _, label := range labels {
		if !labelRe.MatchString(label) {
			return fmt.Errorf("invalid domain label %q", label)
		}
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 || strings.Trim(tld, "0123456789") == "" {
		return fmt.Errorf("invalid top level domain")
	}
	return nil
}

func ServiceUnit(name string) error {
	if !unitRe.MatchString(name) {
		return fmt.Errorf("invalid service name")
	}
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, ".") {
		return fmt.Errorf("invalid service name")
	}
	if name == "gopanel" || strings.HasPrefix(name, "gopanel.") {
		return fmt.Errorf("the panel service cannot be managed from the panel")
	}
	return nil
}

func Upstream(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid upstream URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("upstream must use http or https")
	}
	if u.User != nil {
		return fmt.Errorf("upstream credentials are not allowed")
	}
	if u.Host == "" {
		return fmt.Errorf("upstream host is required")
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("upstream must be scheme://host:port")
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return fmt.Errorf("upstream must include a port")
	}
	if net.ParseIP(host) == nil {
		if err := Domain(strings.ToLower(host)); err != nil {
			return fmt.Errorf("invalid upstream host")
		}
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("invalid upstream port")
	}
	return nil
}

func Chmod(mode string) (uint32, error) {
	m, err := strconv.ParseUint(strings.TrimPrefix(mode, "0o"), 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid mode")
	}
	if m > 0o777 {
		return 0, fmt.Errorf("mode must be between 000 and 777")
	}
	return uint32(m), nil
}

func CronSchedule(expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return fmt.Errorf("schedule is required")
	}
	if _, err := cron.ParseStandard(expr); err != nil {
		return fmt.Errorf("invalid cron schedule: %w", err)
	}
	return nil
}

func CronCommand(command string) error {
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("command is required")
	}
	if len(command) > 4096 {
		return fmt.Errorf("command is too long")
	}
	if strings.ContainsAny(command, "\x00\n\r") {
		return fmt.Errorf("command must not contain newlines or NUL bytes")
	}
	return nil
}

func Email(value string) error {
	if _, err := mail.ParseAddress(value); err != nil {
		return fmt.Errorf("invalid email address")
	}
	return nil
}

func Port(value int) error {
	if value < 1 || value > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

func Role(role string) error {
	switch role {
	case "admin", "operator", "viewer":
		return nil
	}
	return fmt.Errorf("invalid role")
}

func SiteType(t string) error {
	switch t {
	case "static", "php", "proxy":
		return nil
	}
	return fmt.Errorf("invalid site type")
}

func Locale(l string) error {
	if l != "id" && l != "en" {
		return fmt.Errorf("unsupported locale")
	}
	return nil
}

func FirewallProto(p string) error {
	switch p {
	case "", "tcp", "udp", "any":
		return nil
	}
	return fmt.Errorf("proto must be tcp, udp or any")
}

func FirewallAction(a string) error {
	switch a {
	case "allow", "deny", "reject", "limit":
		return nil
	}
	return fmt.Errorf("invalid firewall action")
}

func CIDROrIP(value string) error {
	if value == "" || value == "any" {
		return nil
	}
	if net.ParseIP(value) != nil {
		return nil
	}
	if _, _, err := net.ParseCIDR(value); err == nil {
		return nil
	}
	return fmt.Errorf("from must be an IP or CIDR")
}

func PortSpec(value string) error {
	if value == "" {
		return fmt.Errorf("port is required")
	}
	parts := strings.Split(value, ":")
	if len(parts) > 2 {
		return fmt.Errorf("invalid port range")
	}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid port")
		}
	}
	if len(parts) == 2 {
		a, _ := strconv.Atoi(parts[0])
		b, _ := strconv.Atoi(parts[1])
		if a > b {
			return fmt.Errorf("invalid port range")
		}
	}
	return nil
}
