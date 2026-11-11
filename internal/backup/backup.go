package backup

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopanel/internal/archive"
	"gopanel/internal/model"
	"gopanel/internal/store"
	"gopanel/internal/sys"
	"gopanel/internal/validate"
)

const (
	KindSite     = "site"
	KindDatabase = "database"

	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"
)

var (
	ErrInvalidKind = errors.New("invalid backup kind")
	ErrOutside     = errors.New("backup path is outside the backup directory")
	ErrNotFile     = errors.New("backup file is missing")
)

type Reporter interface {
	Progress(pct int, message string)
	Logf(format string, args ...any)
}

type nopReporter struct{}

func (nopReporter) Progress(int, string) {}
func (nopReporter) Logf(string, ...any)  {}

type Options struct {
	Dir          string
	SiteRootBase string
}

type Service struct {
	st     *store.Store
	sys    sys.System
	logger *slog.Logger
	opts   Options
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func New(st *store.Store, system sys.System, logger *slog.Logger, opts Options) (*Service, error) {
	if logger == nil {
		logger = slog.Default()
	}
	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, err
	}
	if dir == "" || dir == string(filepath.Separator) {
		return nil, fmt.Errorf("invalid backup directory")
	}
	opts.Dir = dir
	for _, sub := range []string{"sites", "databases"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return nil, err
		}
	}
	return &Service{st: st, sys: system, logger: logger, opts: opts}, nil
}

func (s *Service) Dir() string { return s.opts.Dir }

func (s *Service) MarkInterrupted(ctx context.Context) {
	if err := s.st.MarkBackupsInterrupted(ctx); err != nil {
		s.logger.Warn("mark interrupted backups", "error", err)
	}
}

func (s *Service) BackupSite(ctx context.Context, site *model.Site, report Reporter) (*model.Backup, error) {
	report = ensure(report)
	if site == nil || site.Name == "" {
		return nil, fmt.Errorf("site is required")
	}
	if err := validate.SiteName(site.Name); err != nil {
		return nil, err
	}
	rootPath, err := s.siteRoot(site)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(rootPath)
	if err != nil {
		return nil, fmt.Errorf("site root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("site root is not a directory")
	}
	name := fmt.Sprintf("%s-%s-%s.tar.gz", safe(site.Name), stamp(), randomSuffix())
	path := filepath.Join(s.opts.Dir, "sites", name)
	rec, err := s.begin(ctx, KindSite, site.Name, path)
	if err != nil {
		return nil, err
	}
	report.Logf("Backing up site %s from %s", site.Name, rootPath)
	report.Progress(5, "Opening site root")
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, s.fail(ctx, rec, err)
	}
	defer root.Close()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".backup-*")
	if err != nil {
		return nil, s.fail(ctx, rec, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, s.fail(ctx, rec, err)
	}
	report.Progress(20, "Archiving files")
	if err := archive.WriteTarGz(&ctxWriter{ctx: ctx, w: tmp}, root, "."); err != nil {
		tmp.Close()
		return nil, s.fail(ctx, rec, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, s.fail(ctx, rec, err)
	}
	if err := tmp.Close(); err != nil {
		return nil, s.fail(ctx, rec, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return nil, s.fail(ctx, rec, err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		return nil, s.fail(ctx, rec, err)
	}
	rec.Size = stat.Size()
	rec.Status = StatusSuccess
	if err := s.st.UpdateBackup(context.WithoutCancel(ctx), rec); err != nil {
		return nil, err
	}
	report.Logf("Backup %s written (%d bytes)", rec.ID, rec.Size)
	report.Progress(100, "Backup complete")
	return rec, nil
}

func (s *Service) BackupDatabase(ctx context.Context, name string, report Reporter) (*model.Backup, error) {
	report = ensure(report)
	if err := validate.DatabaseName(name); err != nil {
		return nil, err
	}
	if _, err := s.st.GetDatabase(ctx, name); err != nil {
		return nil, fmt.Errorf("database %s: %w", name, err)
	}
	file := fmt.Sprintf("%s-%s-%s.sql.gz", safe(name), stamp(), randomSuffix())
	path := filepath.Join(s.opts.Dir, "databases", file)
	rec, err := s.begin(ctx, KindDatabase, name, path)
	if err != nil {
		return nil, err
	}
	report.Logf("Dumping database %s", name)
	report.Progress(10, "Running mysqldump")
	tmp, err := os.CreateTemp(filepath.Dir(path), ".backup-*")
	if err != nil {
		return nil, s.fail(ctx, rec, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, s.fail(ctx, rec, err)
	}
	gz := gzip.NewWriter(tmp)
	dumpErr := s.sys.MySQLDump(ctx, name, gz)
	closeErr := gz.Close()
	if dumpErr != nil {
		tmp.Close()
		return nil, s.fail(ctx, rec, dumpErr)
	}
	if closeErr != nil {
		tmp.Close()
		return nil, s.fail(ctx, rec, closeErr)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, s.fail(ctx, rec, err)
	}
	if err := tmp.Close(); err != nil {
		return nil, s.fail(ctx, rec, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return nil, s.fail(ctx, rec, err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		return nil, s.fail(ctx, rec, err)
	}
	rec.Size = stat.Size()
	rec.Status = StatusSuccess
	if err := s.st.UpdateBackup(context.WithoutCancel(ctx), rec); err != nil {
		return nil, err
	}
	report.Logf("Database backup %s written (%d bytes)", rec.ID, rec.Size)
	report.Progress(100, "Backup complete")
	return rec, nil
}

func (s *Service) Restore(ctx context.Context, id string, report Reporter) error {
	report = ensure(report)
	rec, err := s.st.GetBackup(ctx, id)
	if err != nil {
		return err
	}
	if rec.Status != StatusSuccess {
		return fmt.Errorf("backup is not complete")
	}
	file, _, err := s.Open(ctx, id)
	if err != nil {
		return err
	}
	defer file.Close()
	switch rec.Kind {
	case KindSite:
		return s.restoreSite(ctx, rec, file, report)
	case KindDatabase:
		return s.restoreDatabase(ctx, rec, file, report)
	default:
		return ErrInvalidKind
	}
}

func (s *Service) restoreSite(ctx context.Context, rec *model.Backup, file *os.File, report Reporter) error {
	site, err := s.st.GetSiteByName(ctx, rec.Ref)
	if err != nil {
		return fmt.Errorf("site %s: %w", rec.Ref, err)
	}
	report.Progress(5, "Creating safety backup")
	safety, err := s.BackupSite(ctx, site, safetyReporter{parent: report})
	if err != nil {
		return fmt.Errorf("safety backup failed: %w", err)
	}
	report.Logf("Safety backup %s created", safety.ID)
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	rootPath, err := s.siteRoot(site)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	report.Progress(40, "Extracting backup")
	if err := archive.ExtractTarGz(file, root, ".", archive.DefaultLimits()); err != nil {
		return fmt.Errorf("extract backup: %w", err)
	}
	report.Progress(100, "Restore complete")
	return nil
}

func (s *Service) restoreDatabase(ctx context.Context, rec *model.Backup, file *os.File, report Reporter) error {
	if err := validate.DatabaseName(rec.Ref); err != nil {
		return err
	}
	if _, err := s.st.GetDatabase(ctx, rec.Ref); err != nil {
		return fmt.Errorf("database %s: %w", rec.Ref, err)
	}
	report.Progress(5, "Creating safety backup")
	safety, err := s.BackupDatabase(ctx, rec.Ref, safetyReporter{parent: report})
	if err != nil {
		return fmt.Errorf("safety backup failed: %w", err)
	}
	report.Logf("Safety backup %s created", safety.ID)
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	gz, err := gzip.NewReader(&ctxReader{ctx: ctx, r: file})
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer gz.Close()
	report.Progress(40, "Restoring database")
	if err := s.sys.MySQLRestore(ctx, rec.Ref, gz); err != nil {
		return fmt.Errorf("restore database: %w", err)
	}
	report.Progress(100, "Restore complete")
	return nil
}

func (s *Service) Open(ctx context.Context, id string) (*os.File, string, error) {
	rec, err := s.st.GetBackup(ctx, id)
	if err != nil {
		return nil, "", err
	}
	rel, err := s.relative(rec.Path)
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(s.opts.Dir)
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", ErrNotFile
		}
		return nil, "", err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, "", err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, "", ErrNotFile
	}
	return f, filepath.Base(rel), nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	rec, err := s.st.GetBackup(ctx, id)
	if err != nil {
		return err
	}
	if rel, err := s.relative(rec.Path); err == nil {
		root, rerr := os.OpenRoot(s.opts.Dir)
		if rerr == nil {
			if rmErr := root.Remove(rel); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
				root.Close()
				return rmErr
			}
			root.Close()
		}
	}
	return s.st.DeleteBackup(ctx, id)
}

func (s *Service) Retention(ctx context.Context, kind, ref string, keep int) (int, error) {
	if keep < 1 {
		keep = 1
	}
	list, err := s.st.ListBackupsFor(ctx, kind, ref)
	if err != nil {
		return 0, err
	}
	removed := 0
	for i, rec := range list {
		if i < keep {
			continue
		}
		if err := s.Delete(ctx, rec.ID); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func (s *Service) begin(ctx context.Context, kind, ref, path string) (*model.Backup, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	rec := &model.Backup{
		ID:        id,
		Kind:      kind,
		Ref:       ref,
		Path:      path,
		Status:    StatusRunning,
		CreatedAt: time.Now().Unix(),
	}
	if err := s.st.CreateBackup(ctx, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

func (s *Service) fail(ctx context.Context, rec *model.Backup, cause error) error {
	rec.Status = StatusFailed
	rec.Error = cause.Error()
	uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.st.UpdateBackup(uctx, rec); err != nil {
		s.logger.Warn("update failed backup", "backup", rec.ID, "error", err)
	}
	return cause
}

func (s *Service) siteRoot(site *model.Site) (string, error) {
	root, err := filepath.Abs(site.RootPath)
	if err != nil {
		return "", err
	}
	if s.opts.SiteRootBase == "" {
		return root, nil
	}
	base, err := filepath.Abs(s.opts.SiteRootBase)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, root)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("site root is outside the configured web root")
	}
	return root, nil
}

type safetyReporter struct {
	parent Reporter
}

func (r safetyReporter) Progress(pct int, message string) {
	r.parent.Progress(5+pct*20/100, message)
}

func (r safetyReporter) Logf(format string, args ...any) {
	r.parent.Logf(format, args...)
}

func (s *Service) relative(path string) (string, error) {
	rel, err := filepath.Rel(s.opts.Dir, filepath.Clean(path))
	if err != nil {
		return "", ErrOutside
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", ErrOutside
	}
	return rel, nil
}

func ensure(r Reporter) Reporter {
	if r == nil {
		return nopReporter{}
	}
	return r
}

func safe(name string) string {
	out := unsafeName.ReplaceAllString(name, "_")
	if out == "" || out == "." || out == ".." {
		out = "backup"
	}
	return out
}

func stamp() string {
	return time.Now().UTC().Format("20060102-150405")
}

func randomSuffix() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "000000"
	}
	return hex.EncodeToString(b[:])
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

type ctxWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w *ctxWriter) Write(p []byte) (int, error) {
	select {
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	default:
	}
	return w.w.Write(p)
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *ctxReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
	}
	return r.r.Read(p)
}
