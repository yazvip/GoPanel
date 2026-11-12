//go:build linux

package linux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopanel/internal/sys"
)

// nginxFallbacks covers hosts where PATH is minimal (systemd units often run
// with a short PATH that omits /usr/sbin) or nginx comes from a source install
// that is not linked into a standard bin directory.
var nginxFallbacks = []string{
	"/usr/sbin/nginx",
	"/usr/local/sbin/nginx",
	"/usr/local/nginx/sbin/nginx",
	"/opt/nginx/sbin/nginx",
	"/usr/local/openresty/nginx/sbin/nginx",
}

// nginxBinary resolves the nginx executable used by "nginx -t". A PATH-only
// lookup reports nginx as missing on hosts where it is installed but the panel
// service does not inherit /usr/sbin, so well-known locations are checked too.
func nginxBinary() (string, error) {
	if path, err := exec.LookPath("nginx"); err == nil {
		return path, nil
	}
	for _, path := range nginxFallbacks {
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("%w: install nginx (App Store) or make it reachable from the gopanel service PATH", sys.ErrNginxMissing)
}

func (s *System) siteConf(name string) string {
	return filepath.Join(s.cfg.NginxSitesAvailable, "gopanel-"+name+".conf")
}

func (s *System) siteLink(name string) string {
	return filepath.Join(s.cfg.NginxSitesEnabled, "gopanel-"+name+".conf")
}

func (s *System) NginxTest(ctx context.Context) error {
	if s.cfg.NginxConf == "" {
		return errors.New("nginx config path is not configured")
	}
	binary, err := nginxBinary()
	if err != nil {
		return err
	}
	_, err = s.Run(ctx, binary, []string{"-t", "-c", s.cfg.NginxConf}, sys.ExecOpts{Timeout: 20 * time.Second})
	return err
}

func (s *System) NginxReload(ctx context.Context) error {
	_, err := s.Run(ctx, "systemctl", []string{"reload", "nginx"}, sys.ExecOpts{Timeout: 30 * time.Second})
	return err
}

func (s *System) ReadSiteConfig(ctx context.Context, name string) (string, bool, error) {
	data, err := os.ReadFile(s.siteConf(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(data), true, nil
}

func (s *System) WriteSiteConfig(ctx context.Context, name, content string) error {
	path := s.siteConf(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicWrite(path, []byte(content), 0o644)
}

func (s *System) RemoveSiteConfig(ctx context.Context, name string) error {
	link := s.siteLink(name)
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(s.siteConf(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *System) SetSiteEnabled(ctx context.Context, name string, enabled bool) error {
	if err := os.MkdirAll(s.cfg.NginxSitesEnabled, 0o755); err != nil {
		return err
	}
	link := s.siteLink(name)
	if !enabled {
		if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if _, err := os.Stat(s.siteConf(name)); err != nil {
		return fmt.Errorf("site config missing: %w", err)
	}
	tmp := link + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(s.siteConf(name), tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *System) SiteLogs(ctx context.Context, name, kind string, lines int) (string, error) {
	if kind != "access" && kind != "error" {
		return "", fmt.Errorf("invalid log type")
	}
	if lines <= 0 {
		lines = 200
	}
	if lines > 2000 {
		lines = 2000
	}
	path := filepath.Join(s.cfg.NginxLogDir, "gopanel-"+name+"."+kind+".log")
	text, err := tailFile(path, lines)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("log file not found yet")
		}
		return "", err
	}
	return text, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".gopanel-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func tailFile(path string, lines int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	const maxRead = 512 * 1024
	start := int64(0)
	if st.Size() > maxRead {
		start = st.Size() - maxRead
	}
	if _, err := f.Seek(start, 0); err != nil {
		return "", err
	}
	data := make([]byte, st.Size()-start)
	if _, err := f.Read(data); err != nil {
		return "", err
	}
	text := string(data)
	if start > 0 {
		if idx := strings.IndexByte(text, '\n'); idx >= 0 {
			text = text[idx+1:]
		}
	}
	parts := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n"), nil
}
