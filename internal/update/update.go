// Package update checks whether a newer GoPanel build is available on GitHub
// and can start the bundled installer as a detached systemd unit.
//
// The panel itself cannot query GitHub from the browser (the CSP only allows
// same-origin requests), so the check runs on the server with a short timeout
// and its result is cached to stay friendly towards the unauthenticated
// GitHub API rate limit.
package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DefaultRepo is the public repository the release check points at.
const DefaultRepo = "yazvip/GoPanel"

const (
	defaultTTL     = 30 * time.Minute
	defaultTimeout = 15 * time.Second
	maxOutput      = 1 << 20
)

var (
	// ErrUnsupported means this installation cannot update itself (not a git
	// checkout, not running as root, or systemd is unavailable).
	ErrUnsupported = errors.New("automatic updates are not available on this installation")
	// ErrBusy means an update was already started recently.
	ErrBusy = errors.New("an update is already in progress")
	// ErrNoUpdate means the running checkout already matches origin/main.
	ErrNoUpdate = errors.New("no update is available")
)

// Status describes the result of a GitHub update check.
type Status struct {
	Supported     bool      `json:"supported"`
	Available     bool      `json:"available"`
	AutoUpdate    bool      `json:"auto_update"`
	Current       string    `json:"current"`
	Latest        string    `json:"latest,omitempty"`
	Behind        int       `json:"behind,omitempty"`
	URL           string    `json:"url,omitempty"`
	ManualCommand string    `json:"manual_command,omitempty"`
	CheckedAt     time.Time `json:"checked_at"`
	Error         string    `json:"error,omitempty"`
}

// Options configures a Service.
type Options struct {
	Repo       string
	Version    string
	InstallDir string
	ConfigPath string
	APIBase    string
	TTL        time.Duration
	Timeout    time.Duration
	Logger     *slog.Logger
}

// Service performs cached update checks and can spawn the installer.
type Service struct {
	repo       string
	version    string
	installDir string
	configPath string
	apiBase    string
	ttl        time.Duration
	timeout    time.Duration
	logger     *slog.Logger
	client     *http.Client

	checkMu sync.Mutex

	mu            sync.Mutex
	cached        *Status
	fetched       time.Time
	applyingUntil time.Time
}

// New builds an update service. An empty InstallDir is resolved from the
// running executable, which is the git checkout on installer deployments.
func New(opts Options) *Service {
	repo := strings.TrimSpace(opts.Repo)
	if repo == "" {
		repo = DefaultRepo
	}
	dir := strings.TrimSpace(opts.InstallDir)
	if dir == "" {
		if exe, err := os.Executable(); err == nil {
			if resolved, err := filepath.EvalSymlinks(exe); err == nil {
				exe = resolved
			}
			dir = filepath.Dir(exe)
		}
	}
	if dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	apiBase := strings.TrimRight(strings.TrimSpace(opts.APIBase), "/")
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	return &Service{
		repo:       repo,
		version:    strings.TrimSpace(opts.Version),
		installDir: dir,
		configPath: strings.TrimSpace(opts.ConfigPath),
		apiBase:    apiBase,
		ttl:        ttl,
		timeout:    timeout,
		logger:     logger,
		client:     &http.Client{Timeout: timeout},
	}
}

// Check returns the cached status when it is fresh, otherwise queries GitHub.
// Force bypasses the cache.
func (s *Service) Check(ctx context.Context, force bool) Status {
	s.mu.Lock()
	if !force && s.cached != nil && time.Since(s.fetched) < s.ttl {
		st := *s.cached
		s.mu.Unlock()
		return st
	}
	s.mu.Unlock()

	s.checkMu.Lock()
	defer s.checkMu.Unlock()

	// Another request may have refreshed the cache while we waited.
	s.mu.Lock()
	if !force && s.cached != nil && time.Since(s.fetched) < s.ttl {
		st := *s.cached
		s.mu.Unlock()
		return st
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	st := s.check(ctx)

	s.mu.Lock()
	s.cached = &st
	s.fetched = time.Now()
	s.mu.Unlock()
	return st
}

func (s *Service) check(ctx context.Context) Status {
	st := Status{
		Current:       s.version,
		CheckedAt:     time.Now().UTC(),
		ManualCommand: s.manualCommand(),
	}
	if s.installDir == "" {
		st.Error = "the installation directory could not be determined"
		return st
	}
	if _, err := exec.LookPath("git"); err != nil {
		st.Error = "git is not available on this server"
		return st
	}
	if !isGitCheckout(s.installDir) {
		st.Error = "this installation is not a git checkout, so it cannot be updated from the panel"
		return st
	}
	st.Supported = true
	st.AutoUpdate = canAutoUpdate(s.installDir)

	local, err := s.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		st.Error = "read local revision: " + err.Error()
		return st
	}
	local = strings.TrimSpace(local)
	if local == "" {
		st.Error = "the local git checkout has no revision"
		return st
	}

	out, err := s.git(ctx, "ls-remote", "origin", "refs/heads/main")
	if err != nil {
		st.Error = "cannot reach GitHub: " + err.Error()
		return st
	}
	remote := parseRemoteSHA(out)
	if remote == "" {
		st.Error = "branch main was not found on origin"
		return st
	}

	st.Latest = shortSHA(remote)
	st.URL = fmt.Sprintf("https://github.com/%s", s.repo)
	if !strings.EqualFold(local, remote) {
		st.Available = true
		st.URL = fmt.Sprintf("https://github.com/%s/compare/%s...%s", s.repo, local, remote)
		st.Behind = s.commitsBehind(ctx, local, remote)
	}
	return st
}

// Apply starts the installer as a transient systemd unit so it survives the
// panel restart it performs. It returns the unit name for diagnostics.
func (s *Service) Apply() (string, error) {
	if s.installDir == "" || !canAutoUpdate(s.installDir) {
		return "", ErrUnsupported
	}

	s.mu.Lock()
	if time.Now().Before(s.applyingUntil) {
		s.mu.Unlock()
		return "", ErrBusy
	}
	s.applyingUntil = time.Now().Add(15 * time.Minute)
	s.mu.Unlock()

	script := filepath.Join(s.installDir, "scripts", "install.sh")
	unit := fmt.Sprintf("gopanel-update-%d", time.Now().Unix())

	args := []string{
		"--collect",
		"--no-block",
		"--quiet",
		"--unit=" + unit,
		"--description=GoPanel update",
	}
	if s.configPath != "" {
		args = append(args, "--setenv=GOPANEL_CONFIG="+s.configPath)
	}
	args = append(args, "--setenv=GOPANEL_INSTALL_DIR="+s.installDir)
	args = append(args, "bash", script)

	// The request context is cancelled as soon as the handler returns, so use
	// a detached context for the short systemd-run invocation.
	runCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "systemd-run", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		s.mu.Lock()
		s.applyingUntil = time.Time{}
		s.mu.Unlock()
		msg := strings.TrimSpace(out.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("start updater: %s", msg)
	}

	s.logger.Info("update started", "unit", unit, "script", script)
	return unit, nil
}

func (s *Service) manualCommand() string {
	if s.installDir == "" {
		return ""
	}
	script := filepath.Join(s.installDir, "scripts", "install.sh")
	if _, err := os.Stat(script); err != nil {
		return ""
	}
	return fmt.Sprintf("sudo bash %q", script)
}

func (s *Service) git(ctx context.Context, args ...string) (string, error) {
	full := append([]string{"-C", s.installDir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", errors.New(msg)
		}
		return "", err
	}
	if len(out) > maxOutput {
		out = out[:maxOutput]
	}
	return string(out), nil
}

func (s *Service) commitsBehind(ctx context.Context, local, remote string) int {
	url := fmt.Sprintf("%s/repos/%s/compare/%s...%s", s.apiBase, s.repo, local, remote)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "GoPanel/"+valueOr(s.version, "dev"))
	resp, err := s.client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return 0
	}
	var payload struct {
		AheadBy int `json:"ahead_by"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOutput)).Decode(&payload); err != nil {
		return 0
	}
	if payload.AheadBy < 0 {
		return 0
	}
	return payload.AheadBy
}

func isGitCheckout(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

// canAutoUpdate reports whether the bundled installer can run detached as a
// systemd unit. Every condition is required: the installer stops the panel
// service while rebuilding, and a child of that service would be killed.
func canAutoUpdate(dir string) bool {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return false
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return false
	}
	for _, binary := range []string{"systemctl", "systemd-run", "bash"} {
		if _, err := exec.LookPath(binary); err != nil {
			return false
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "scripts", "install.sh")); err != nil {
		return false
	}
	return true
}

func parseRemoteSHA(out string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "refs/heads/main" {
			return fields[0]
		}
	}
	return ""
}

func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
