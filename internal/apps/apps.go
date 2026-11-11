package apps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"gopanel/internal/sys"
)

var (
	ErrUnknownApp = errors.New("unknown application")
	ErrEmpty      = errors.New("select at least one application")
)

type App struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Packages    []string `json:"packages"`
	Service     string   `json:"service,omitempty"`
	Conflicts   []string `json:"conflicts,omitempty"`
	Warning     string   `json:"warning,omitempty"`
	Popular     bool     `json:"popular,omitempty"`
}

type Item struct {
	App
	Installed         bool   `json:"installed"`
	Version           string `json:"version,omitempty"`
	MissingPackages   int    `json:"missing_packages"`
	InstalledPackages int    `json:"installed_packages"`
}

type Reporter interface {
	Progress(pct int, message string)
	Print(text string)
	Logf(format string, args ...any)
}

type Request struct {
	Apps        []string `json:"apps"`
	UpdateCache bool     `json:"update_cache"`
}

type Service struct {
	sys    sys.System
	logger *slog.Logger
	mu     sync.Mutex
}

func New(system sys.System, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{sys: system, logger: logger}
}

var catalog = []App{
	{
		ID:          "nginx",
		Name:        "Nginx",
		Category:    "web",
		Description: "Web server and reverse proxy used by every site managed by GoPanel.",
		Packages:    []string{"nginx"},
		Service:     "nginx",
		Conflicts:   []string{"apache"},
		Popular:     true,
	},
	{
		ID:          "apache",
		Name:        "Apache",
		Category:    "web",
		Description: "Apache HTTP Server. Serves on ports 80/443, so it cannot share the host with Nginx.",
		Packages:    []string{"apache2"},
		Service:     "apache2",
		Conflicts:   []string{"nginx"},
	},
	{
		ID:          "mariadb",
		Name:        "MariaDB",
		Category:    "database",
		Description: "MariaDB database server with client tools for the databases page.",
		Packages:    []string{"mariadb-server", "mariadb-client"},
		Service:     "mariadb",
		Conflicts:   []string{"mysql"},
		Popular:     true,
	},
	{
		ID:          "mysql",
		Name:        "MySQL",
		Category:    "database",
		Description: "Oracle MySQL server. Choose this or MariaDB, not both.",
		Packages:    []string{"mysql-server", "mysql-client"},
		Service:     "mysql",
		Conflicts:   []string{"mariadb"},
	},
	{
		ID:          "php",
		Name:        "PHP-FPM",
		Category:    "runtime",
		Description: "PHP-FPM with the common extensions (MySQL, curl, gd, mbstring, xml, zip, bcmath, intl). Installs the distribution default PHP version.",
		Packages: []string{
			"php-fpm", "php-cli", "php-mysql", "php-curl", "php-gd",
			"php-mbstring", "php-xml", "php-zip", "php-bcmath", "php-intl",
		},
		Popular: true,
	},
	{
		ID:          "nodejs",
		Name:        "Node.js",
		Category:    "runtime",
		Description: "Node.js JavaScript runtime with npm for web apps and build tooling.",
		Packages:    []string{"nodejs", "npm"},
		Popular:     true,
	},
	{
		ID:          "python",
		Name:        "Python",
		Category:    "runtime",
		Description: "Python 3 interpreter with pip and venv for scripts and applications.",
		Packages:    []string{"python3", "python3-pip", "python3-venv"},
		Popular:     true,
	},
	{
		ID:          "redis",
		Name:        "Redis",
		Category:    "database",
		Description: "In-memory cache and queue server.",
		Packages:    []string{"redis-server"},
		Service:     "redis-server",
		Popular:     true,
	},
	{
		ID:          "ufw",
		Name:        "UFW",
		Category:    "security",
		Description: "Uncomplicated Firewall front-end used by the firewall page.",
		Packages:    []string{"ufw"},
		Popular:     true,
	},
	{
		ID:          "fail2ban",
		Name:        "Fail2ban",
		Category:    "security",
		Description: "Bans hosts that fail authentication repeatedly (SSH, nginx).",
		Packages:    []string{"fail2ban"},
		Service:     "fail2ban",
	},
	{
		ID:          "phpmyadmin",
		Name:        "phpMyAdmin",
		Category:    "tool",
		Description: "Web database administration interface.",
		Packages:    []string{"phpmyadmin"},
		Warning:     "phpMyAdmin is installed without a web server block. Create a site pointing at /usr/share/phpmyadmin (PHP site type) after installation, and restrict access with HTTP auth or an IP allowlist.",
	},
}

func List() []App {
	out := make([]App, len(catalog))
	for i, a := range catalog {
		out[i] = a
		out[i].Packages = append([]string(nil), a.Packages...)
	}
	return out
}

func lookup(id string) (App, bool) {
	for _, a := range catalog {
		if a.ID == id {
			return a, true
		}
	}
	return App{}, false
}

func (s *Service) Catalog(ctx context.Context) ([]Item, error) {
	installed, err := s.sys.AptInstalled(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(catalog))
	for _, app := range catalog {
		item := Item{App: app}
		for _, pkg := range app.Packages {
			if version, ok := installed[pkg]; ok {
				item.InstalledPackages++
				if item.Version == "" {
					item.Version = version
				}
			} else {
				item.MissingPackages++
			}
		}
		item.Installed = item.MissingPackages == 0
		items = append(items, item)
	}
	return items, nil
}

func (s *Service) Refresh(ctx context.Context, report Reporter) error {
	report = ensure(report)
	s.mu.Lock()
	defer s.mu.Unlock()
	report.Progress(5, "Updating package index")
	report.Print("Running apt-get update")
	if err := s.sys.AptUpdate(ctx, func(line string) { report.Print(line) }); err != nil {
		return fmt.Errorf("apt-get update: %w", err)
	}
	report.Progress(100, "Package index updated")
	return nil
}

func (s *Service) Install(ctx context.Context, req Request, report Reporter) error {
	report = ensure(report)
	ids, err := normalize(req.Apps)
	if err != nil {
		return err
	}
	installed, err := s.sys.AptInstalled(ctx)
	if err != nil {
		return fmt.Errorf("read installed packages: %w", err)
	}

	var selected []App
	packages := make([]string, 0, 16)
	seen := map[string]bool{}
	for _, id := range ids {
		app, _ := lookup(id)
		for _, other := range app.Conflicts {
			if contains(ids, other) {
				conf, _ := lookup(other)
				return fmt.Errorf("%s and %s cannot be installed together", app.Name, conf.Name)
			}
			if conf, ok := lookup(other); ok && packagesInstalled(installed, conf.Packages) {
				return fmt.Errorf("%s conflicts with the installed %s", app.Name, conf.Name)
			}
		}
		selected = append(selected, app)
		for _, pkg := range app.Packages {
			if !seen[pkg] {
				seen[pkg] = true
				packages = append(packages, pkg)
			}
		}
	}

	missing := make([]string, 0, len(packages))
	for _, pkg := range packages {
		if _, ok := installed[pkg]; !ok {
			missing = append(missing, pkg)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if req.UpdateCache {
		report.Progress(2, "Updating package index")
		report.Print("Running apt-get update")
		if err := s.sys.AptUpdate(ctx, func(line string) { report.Print(line) }); err != nil {
			return fmt.Errorf("apt-get update: %w", err)
		}
	}
	if len(missing) == 0 {
		for _, app := range selected {
			report.Logf("%s is already installed", app.Name)
		}
		report.Progress(100, "Nothing to install")
		return nil
	}

	report.Progress(10, "Installing packages")
	report.Print("Packages: " + strings.Join(missing, " "))
	start := time.Now()
	if err := s.sys.AptInstall(ctx, missing, func(line string) { report.Print(line) }); err != nil {
		return fmt.Errorf("apt-get install: %w", err)
	}
	report.Logf("Installed %d package(s) in %s", len(missing), time.Since(start).Round(time.Second))
	report.Progress(95, "Verifying installation")

	after, err := s.sys.AptInstalled(ctx)
	if err != nil {
		return fmt.Errorf("verify installation: %w", err)
	}
	var failed []string
	for _, pkg := range missing {
		if _, ok := after[pkg]; !ok {
			failed = append(failed, pkg)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("packages are still missing after install: %s", strings.Join(failed, ", "))
	}
	report.Progress(100, "Installation complete")
	return nil
}

func normalize(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, ErrEmpty
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" || seen[id] {
			continue
		}
		if _, ok := lookup(id); !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownApp, id)
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, ErrEmpty
	}
	sort.Strings(out)
	return out, nil
}

func packagesInstalled(installed map[string]string, packages []string) bool {
	for _, pkg := range packages {
		if _, ok := installed[pkg]; !ok {
			return false
		}
	}
	return true
}

func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

type nopReporter struct{}

func (nopReporter) Progress(int, string) {}
func (nopReporter) Print(string)         {}
func (nopReporter) Logf(string, ...any)  {}

func ensure(r Reporter) Reporter {
	if r == nil {
		return nopReporter{}
	}
	return r
}
