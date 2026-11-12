package sites

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"

	"gopanel/internal/model"
	"gopanel/internal/store"
	"gopanel/internal/sys"
	"gopanel/internal/validate"
)

var (
	ErrExists        = errors.New("site already exists")
	ErrInvalid       = errors.New("invalid site configuration")
	ErrApplyFailed   = errors.New("nginx rejected the configuration")
	ErrDomainTaken   = errors.New("domain is already assigned to another site")
	ErrRootOutside   = errors.New("site root must be inside the configured web root")
	ErrPanelUpstream = errors.New("upstream must not point at the panel itself")
)

var phpVersionRe = regexp.MustCompile(`^[0-9]\.[0-9]{1,2}$`)
var safePathRe = regexp.MustCompile(`^[A-Za-z0-9/._\\:-]+$`)

type Options struct {
	SiteRootBase  string
	NginxLogDir   string
	ACMEWebroot   string
	PanelPort     int
	DefaultPHP    string
	ClientMaxBody int64
}

type Service struct {
	st   *store.Store
	sys  sys.System
	opts Options
	mu   sync.Mutex
}

type Input struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	RootPath      string   `json:"root_path"`
	PHPVersion    string   `json:"php_version"`
	Upstream      string   `json:"upstream"`
	IndexFile     string   `json:"index_file"`
	ClientMaxBody int64    `json:"client_max_body"`
	AccessLog     bool     `json:"access_log"`
	Domains       []string `json:"domains"`
}

func New(st *store.Store, system sys.System, opts Options) *Service {
	if opts.SiteRootBase == "" {
		opts.SiteRootBase = "/var/www"
	}
	if opts.ClientMaxBody <= 0 {
		opts.ClientMaxBody = 64 << 20
	}
	return &Service{st: st, sys: system, opts: opts}
}

func (s *Service) RootBase() string { return s.opts.SiteRootBase }

func (s *Service) Get(ctx context.Context, id int64) (*model.Site, error) {
	site, err := s.st.GetSite(ctx, id)
	if err != nil {
		return nil, err
	}
	site.Domains, err = s.st.ListDomains(ctx, id)
	if err != nil {
		return nil, err
	}
	return site, nil
}

func (s *Service) Create(ctx context.Context, in Input) (*model.Site, error) {
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	if err := validate.SiteName(in.Name); err != nil {
		return nil, err
	}
	if err := validate.SiteType(in.Type); err != nil {
		return nil, err
	}
	if _, err := s.st.GetSiteByName(ctx, in.Name); err == nil {
		return nil, ErrExists
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	domains, err := normalizeDomains(in.Domains)
	if err != nil {
		return nil, err
	}
	if len(domains) == 0 {
		return nil, fmt.Errorf("at least one domain is required")
	}
	for _, d := range domains {
		taken, err := s.st.DomainExists(ctx, d, 0)
		if err != nil {
			return nil, err
		}
		if taken {
			return nil, fmt.Errorf("%w: %s", ErrDomainTaken, d)
		}
	}
	root, err := s.resolveRoot(in.Name, in.RootPath)
	if err != nil {
		return nil, err
	}
	index, err := normalizeIndex(in.IndexFile, in.Type)
	if err != nil {
		return nil, err
	}
	phpVersion, err := s.validatePHP(ctx, in.Type, in.PHPVersion)
	if err != nil {
		return nil, err
	}
	upstream, err := s.validateUpstream(in.Type, in.Upstream)
	if err != nil {
		return nil, err
	}
	maxBody := in.ClientMaxBody
	if maxBody < 0 {
		return nil, fmt.Errorf("client_max_body must not be negative")
	}
	if maxBody == 0 {
		maxBody = s.opts.ClientMaxBody
	}
	if maxBody > 1<<32 {
		return nil, fmt.Errorf("client_max_body is too large")
	}
	accessLog := in.AccessLog
	createdRoot := false
	if _, statErr := os.Stat(root); errors.Is(statErr, os.ErrNotExist) {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, err
		}
		createdRoot = true
	}
	site := &model.Site{
		Name:          in.Name,
		RootPath:      root,
		Type:          in.Type,
		PHPVersion:    phpVersion,
		Upstream:      upstream,
		Enabled:       true,
		IndexFile:     index,
		ClientMaxBody: maxBody,
		AccessLog:     accessLog,
	}
	if err := s.st.CreateSite(ctx, site); err != nil {
		if createdRoot {
			os.RemoveAll(root)
		}
		return nil, err
	}
	for i, d := range domains {
		if err := s.st.AddDomain(ctx, site.ID, d, i == 0); err != nil {
			s.st.DeleteSite(ctx, site.ID)
			if createdRoot {
				os.RemoveAll(root)
			}
			return nil, err
		}
	}
	site.Domains, _ = s.st.ListDomains(ctx, site.ID)
	if in.Type == model.SiteTypeStatic {
		writeWelcome(root, domains[0])
	}
	if err := s.Apply(ctx, site, false); err != nil {
		s.st.DeleteSite(ctx, site.ID)
		s.sys.RemoveSiteConfig(ctx, site.Name)
		if createdRoot {
			os.RemoveAll(root)
		}
		return nil, err
	}
	return site, nil
}

func writeWelcome(root, domain string) {
	file := filepath.Join(root, "index.html")
	if _, err := os.Stat(file); err == nil {
		return
	}
	body := fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>%s</title></head>
<body><h1>%s</h1><p>This site is served by GoPanel.</p></body></html>
`, domain, domain)
	os.WriteFile(file, []byte(body), 0o644)
}

func (s *Service) resolveRoot(name, raw string) (string, error) {
	base, err := filepath.Abs(s.opts.SiteRootBase)
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(raw)
	if root == "" {
		root = filepath.Join(base, name)
	}
	root, err = filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, root)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrRootOutside
	}
	if !safePathRe.MatchString(root) {
		return "", fmt.Errorf("site root contains unsupported characters")
	}
	return root, nil
}

func normalizeIndex(index, siteType string) (string, error) {
	index = strings.TrimSpace(index)
	if index == "" {
		if siteType == model.SiteTypePHP {
			index = "index.php index.html"
		} else {
			index = "index.html index.htm"
		}
	}
	parts := strings.Fields(index)
	if len(parts) == 0 || len(parts) > 5 {
		return "", fmt.Errorf("invalid index file list")
	}
	for _, p := range parts {
		if strings.ContainsAny(p, "/\\;\"'{}") || strings.Contains(p, "..") {
			return "", fmt.Errorf("invalid index file")
		}
	}
	return strings.Join(parts, " "), nil
}

func (s *Service) validatePHP(ctx context.Context, siteType, version string) (string, error) {
	if siteType != model.SiteTypePHP {
		return "", nil
	}
	versions, err := s.sys.PHPVersions(ctx)
	if err != nil {
		return "", fmt.Errorf("detect PHP versions: %w", err)
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no PHP-FPM version detected; install PHP first")
	}
	version = strings.TrimSpace(version)
	if version == "" {
		version = versions[0]
		if s.opts.DefaultPHP != "" {
			version = s.opts.DefaultPHP
		}
	}
	if !phpVersionRe.MatchString(version) {
		return "", fmt.Errorf("invalid PHP version")
	}
	for _, v := range versions {
		if v == version {
			return version, nil
		}
	}
	return "", fmt.Errorf("PHP %s is not installed", version)
}

func (s *Service) validateUpstream(siteType, upstream string) (string, error) {
	if siteType != model.SiteTypeProxy {
		return "", nil
	}
	upstream = strings.TrimSpace(upstream)
	if err := validate.Upstream(upstream); err != nil {
		return "", err
	}
	u, err := parsePort(upstream)
	if err != nil {
		return "", err
	}
	if s.opts.PanelPort > 0 && u == s.opts.PanelPort {
		return "", ErrPanelUpstream
	}
	return upstream, nil
}

func parsePort(upstream string) (int, error) {
	idx := strings.LastIndex(upstream, ":")
	if idx < 0 {
		return 0, fmt.Errorf("upstream port missing")
	}
	port, err := strconv.Atoi(strings.TrimSuffix(upstream[idx+1:], "/"))
	if err != nil {
		return 0, fmt.Errorf("invalid upstream port")
	}
	return port, nil
}

func normalizeDomains(list []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, raw := range list {
		d := strings.ToLower(strings.TrimSpace(raw))
		if d == "" {
			continue
		}
		if err := validate.Domain(d); err != nil {
			return nil, fmt.Errorf("%s: %w", d, err)
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out, nil
}

func (s *Service) Update(ctx context.Context, site *model.Site, in Input) (*model.Site, error) {
	if err := validate.SiteName(in.Name); err != nil {
		return nil, err
	}
	if err := validate.SiteType(in.Type); err != nil {
		return nil, err
	}
	if in.Name != site.Name {
		if _, err := s.st.GetSiteByName(ctx, in.Name); err == nil {
			return nil, ErrExists
		}
	}
	root, err := s.resolveRoot(in.Name, in.RootPath)
	if err != nil {
		return nil, err
	}
	index, err := normalizeIndex(in.IndexFile, in.Type)
	if err != nil {
		return nil, err
	}
	phpVersion, err := s.validatePHP(ctx, in.Type, in.PHPVersion)
	if err != nil {
		return nil, err
	}
	upstream, err := s.validateUpstream(in.Type, in.Upstream)
	if err != nil {
		return nil, err
	}
	if in.ClientMaxBody < 0 {
		return nil, fmt.Errorf("client_max_body must not be negative")
	}
	prev := *site
	prevEnabled := site.Enabled
	site.Name = in.Name
	site.RootPath = root
	site.Type = in.Type
	site.PHPVersion = phpVersion
	site.Upstream = upstream
	site.IndexFile = index
	site.ClientMaxBody = in.ClientMaxBody
	if site.ClientMaxBody == 0 {
		site.ClientMaxBody = s.opts.ClientMaxBody
	}
	site.AccessLog = in.AccessLog
	if err := s.st.UpdateSite(ctx, site); err != nil {
		*site = prev
		return nil, err
	}
	domains, err := normalizeDomains(in.Domains)
	if err != nil {
		*site = prev
		s.st.UpdateSite(ctx, &prev)
		return nil, err
	}
	if len(domains) > 0 {
		if err := s.replaceDomains(ctx, site.ID, domains); err != nil {
			*site = prev
			s.st.UpdateSite(ctx, &prev)
			return nil, err
		}
	}
	site.Domains, _ = s.st.ListDomains(ctx, site.ID)
	if err := s.Apply(ctx, site, prevEnabled); err != nil {
		if prev.Name != site.Name {
			s.sys.RemoveSiteConfig(ctx, site.Name)
		}
		*site = prev
		s.st.UpdateSite(ctx, &prev)
		if len(domains) > 0 {
			s.replaceDomains(ctx, prev.ID, domainsOf(prev.Domains))
		}
		s.Apply(ctx, &prev, prevEnabled)
		return nil, err
	}
	if prev.Name != site.Name {
		s.sys.RemoveSiteConfig(ctx, prev.Name)
	}
	return site, nil
}

func domainsOf(list []model.Domain) []string {
	out := make([]string, 0, len(list))
	for _, d := range list {
		out = append(out, d.Domain)
	}
	return out
}

func (s *Service) replaceDomains(ctx context.Context, siteID int64, domains []string) error {
	existing, err := s.st.ListDomains(ctx, siteID)
	if err != nil {
		return err
	}
	existingSet := map[string]bool{}
	for _, d := range existing {
		existingSet[d.Domain] = true
	}
	wanted := map[string]bool{}
	for i, d := range domains {
		wanted[d] = true
		if existingSet[d] {
			continue
		}
		taken, err := s.st.DomainExists(ctx, d, siteID)
		if err != nil {
			return err
		}
		if taken {
			return fmt.Errorf("%w: %s", ErrDomainTaken, d)
		}
		if err := s.st.AddDomain(ctx, siteID, d, i == 0); err != nil {
			return err
		}
	}
	for _, d := range existing {
		if !wanted[d.Domain] {
			if err := s.st.DeleteDomain(ctx, siteID, d.ID); err != nil {
				return err
			}
		}
	}
	after, err := s.st.ListDomains(ctx, siteID)
	if err != nil {
		return err
	}
	if len(after) > 0 && !after[0].IsPrimary {
		_ = s.st.SetPrimaryDomain(ctx, siteID, after[0].ID)
	}
	return nil
}

func (s *Service) SetEnabled(ctx context.Context, site *model.Site, enabled bool) error {
	prev := site.Enabled
	site.Enabled = enabled
	if err := s.st.SetSiteEnabled(ctx, site.ID, enabled); err != nil {
		site.Enabled = prev
		return err
	}
	if err := s.Apply(ctx, site, prev); err != nil {
		site.Enabled = prev
		s.st.SetSiteEnabled(ctx, site.ID, prev)
		s.Apply(ctx, site, prev)
		return err
	}
	return nil
}

func (s *Service) AddDomain(ctx context.Context, site *model.Site, domain string) error {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if err := validate.Domain(domain); err != nil {
		return err
	}
	taken, err := s.st.DomainExists(ctx, domain, site.ID)
	if err != nil {
		return err
	}
	if taken {
		return ErrDomainTaken
	}
	if err := s.st.AddDomain(ctx, site.ID, domain, false); err != nil {
		return err
	}
	prevEnabled := site.Enabled
	site.Domains, _ = s.st.ListDomains(ctx, site.ID)
	if err := s.Apply(ctx, site, prevEnabled); err != nil {
		domains := site.Domains
		if len(domains) > 0 {
			_ = s.st.DeleteDomain(ctx, site.ID, domains[len(domains)-1].ID)
		}
		site.Domains, _ = s.st.ListDomains(ctx, site.ID)
		s.Apply(ctx, site, prevEnabled)
		return err
	}
	return nil
}

func (s *Service) RemoveDomain(ctx context.Context, site *model.Site, domainID int64) error {
	domains, err := s.st.ListDomains(ctx, site.ID)
	if err != nil {
		return err
	}
	if len(domains) <= 1 {
		return fmt.Errorf("a site must keep at least one domain")
	}
	var removed model.Domain
	for _, d := range domains {
		if d.ID == domainID {
			removed = d
			break
		}
	}
	if removed.ID == 0 {
		return store.ErrNotFound
	}
	if err := s.st.DeleteDomain(ctx, site.ID, domainID); err != nil {
		return err
	}
	prevEnabled := site.Enabled
	site.Domains, _ = s.st.ListDomains(ctx, site.ID)
	if err := s.Apply(ctx, site, prevEnabled); err != nil {
		_ = s.st.AddDomain(ctx, site.ID, removed.Domain, removed.IsPrimary)
		site.Domains, _ = s.st.ListDomains(ctx, site.ID)
		s.Apply(ctx, site, prevEnabled)
		return err
	}
	return nil
}

func (s *Service) SetPrimary(ctx context.Context, site *model.Site, domainID int64) error {
	if err := s.st.SetPrimaryDomain(ctx, site.ID, domainID); err != nil {
		return err
	}
	prevEnabled := site.Enabled
	site.Domains, _ = s.st.ListDomains(ctx, site.ID)
	if err := s.Apply(ctx, site, prevEnabled); err != nil {
		return err
	}
	return nil
}

func (s *Service) SetSSL(ctx context.Context, site *model.Site, enabled bool, certPath, keyPath string, expires int64) error {
	prev := *site
	if err := s.st.SetSiteSSL(ctx, site.ID, enabled, certPath, keyPath, expires); err != nil {
		return err
	}
	site.SSLEnabled = enabled
	site.SSLCertPath = certPath
	site.SSLKeyPath = keyPath
	site.SSLExpiresAt = expires
	if err := s.Apply(ctx, site, site.Enabled); err != nil {
		*site = prev
		s.st.SetSiteSSL(ctx, site.ID, prev.SSLEnabled, prev.SSLCertPath, prev.SSLKeyPath, prev.SSLExpiresAt)
		s.Apply(ctx, site, site.Enabled)
		return err
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, site *model.Site, deleteFiles bool) error {
	if site.Enabled {
		if err := s.sys.SetSiteEnabled(ctx, site.Name, false); err != nil {
			return err
		}
	}
	if err := s.sys.RemoveSiteConfig(ctx, site.Name); err != nil {
		return err
	}
	if err := s.sys.NginxTest(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrApplyFailed, err)
	}
	if err := s.sys.NginxReload(ctx); err != nil {
		return fmt.Errorf("reload nginx: %w", err)
	}
	if deleteFiles {
		base, err := filepath.Abs(s.opts.SiteRootBase)
		if err != nil {
			return err
		}
		root, err := filepath.Abs(site.RootPath)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, root)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ErrRootOutside
		}
		if err := os.RemoveAll(root); err != nil {
			return err
		}
	}
	return s.st.DeleteSite(ctx, site.ID)
}

func (s *Service) Logs(ctx context.Context, site *model.Site, kind string, lines int) (string, error) {
	return s.sys.SiteLogs(ctx, site.Name, kind, lines)
}

func (s *Service) RawConfig(ctx context.Context, site *model.Site) (string, error) {
	content, ok, err := s.sys.ReadSiteConfig(ctx, site.Name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", store.ErrNotFound
	}
	return content, nil
}

func (s *Service) Apply(ctx context.Context, site *model.Site, prevEnabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	domains := domainsOf(site.Domains)
	if len(domains) == 0 {
		return fmt.Errorf("site has no domains")
	}
	content, err := Render(site, domains, s.opts)
	if err != nil {
		return err
	}
	oldContent, oldExists, err := s.sys.ReadSiteConfig(ctx, site.Name)
	if err != nil {
		return err
	}
	if err := s.sys.WriteSiteConfig(ctx, site.Name, content); err != nil {
		return err
	}
	if err := s.sys.SetSiteEnabled(ctx, site.Name, site.Enabled); err != nil {
		s.restore(ctx, site.Name, oldContent, oldExists, prevEnabled)
		return err
	}
	if err := s.sys.NginxTest(ctx); err != nil {
		s.restore(ctx, site.Name, oldContent, oldExists, prevEnabled)
		_ = s.sys.NginxReload(ctx)
		return fmt.Errorf("%w: %w", ErrApplyFailed, err)
	}
	if err := s.sys.NginxReload(ctx); err != nil {
		s.restore(ctx, site.Name, oldContent, oldExists, prevEnabled)
		if rerr := s.sys.NginxReload(ctx); rerr != nil {
			return fmt.Errorf("reload nginx: %w (rollback reload: %v)", err, rerr)
		}
		return fmt.Errorf("reload nginx: %w", err)
	}
	return nil
}

func (s *Service) restore(ctx context.Context, name, oldContent string, oldExists, oldEnabled bool) {
	if oldExists {
		_ = s.sys.WriteSiteConfig(ctx, name, oldContent)
		_ = s.sys.SetSiteEnabled(ctx, name, oldEnabled)
		return
	}
	_ = s.sys.RemoveSiteConfig(ctx, name)
}

type tplData struct {
	Slug          string
	Domains       string
	Root          string
	Index         string
	AccessLog     bool
	ErrorLogPath  string
	AccessLogPath string
	ACMEWebroot   string
	Type          string
	PHPVersion    string
	Upstream      string
	ClientMaxBody int64
	SSL           bool
	CertPath      string
	KeyPath       string
}

var vhostTemplate = template.Must(template.New("vhost").Parse(`# Managed by GoPanel. Do not edit manually.
{{- if .SSL}}
server {
    listen 80;
    listen [::]:80;
    server_name {{.Domains}};

    location ^~ /.well-known/acme-challenge/ {
        root {{.ACMEWebroot}};
        default_type text/plain;
    }

    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name {{.Domains}};

    ssl_certificate     {{.CertPath}};
    ssl_certificate_key {{.KeyPath}};
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 1d;

    root {{.Root}};
    index {{.Index}};
{{- if .AccessLog}}
    access_log {{.AccessLogPath}};
{{- else}}
    access_log off;
{{- end}}
    error_log {{.ErrorLogPath}};
{{- if gt .ClientMaxBody 0}}
    client_max_body_size {{.ClientMaxBody}};
{{- end}}

    location ^~ /.well-known/acme-challenge/ {
        root {{.ACMEWebroot}};
        default_type text/plain;
    }

    location ~ /\.(?!well-known) {
        deny all;
    }

{{.LocationBlock}}
}
{{- else}}
server {
    listen 80;
    listen [::]:80;
    server_name {{.Domains}};

    root {{.Root}};
    index {{.Index}};
{{- if .AccessLog}}
    access_log {{.AccessLogPath}};
{{- else}}
    access_log off;
{{- end}}
    error_log {{.ErrorLogPath}};
{{- if gt .ClientMaxBody 0}}
    client_max_body_size {{.ClientMaxBody}};
{{- end}}

    location ^~ /.well-known/acme-challenge/ {
        root {{.ACMEWebroot}};
        default_type text/plain;
    }

    location ~ /\.(?!well-known) {
        deny all;
    }

{{.LocationBlock}}
}
{{- end}}
`))

func (d tplData) LocationBlock() string {
	switch d.Type {
	case model.SiteTypePHP:
		return fmt.Sprintf(`    location ~ \.php$ {
        include fastcgi_params;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        fastcgi_param SCRIPT_NAME $fastcgi_script_name;
        fastcgi_pass unix:/run/php/php%s-fpm.sock;
        fastcgi_read_timeout 300s;
    }

    location / {
        try_files $uri $uri/ /index.php?$query_string;
    }`, d.PHPVersion)
	case model.SiteTypeProxy:
		return fmt.Sprintf(`    location / {
        proxy_pass %s;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }`, d.Upstream)
	default:
		return `    location / {
        try_files $uri $uri/ =404;
    }`
	}
}

func Render(site *model.Site, domains []string, opts Options) (string, error) {
	if len(domains) == 0 {
		return "", fmt.Errorf("site has no domains")
	}
	for _, d := range domains {
		if err := validate.Domain(d); err != nil {
			return "", err
		}
	}
	if err := validate.SiteName(site.Name); err != nil {
		return "", err
	}
	if !safePathRe.MatchString(site.RootPath) || strings.Contains(site.RootPath, "..") {
		return "", fmt.Errorf("unsafe site root")
	}
	if site.Type == model.SiteTypePHP && !phpVersionRe.MatchString(site.PHPVersion) {
		return "", fmt.Errorf("invalid PHP version")
	}
	if site.Type == model.SiteTypeProxy {
		if err := validate.Upstream(site.Upstream); err != nil {
			return "", err
		}
	}
	data := tplData{
		Slug:          site.Name,
		Domains:       strings.Join(domains, " "),
		Root:          filepath.ToSlash(site.RootPath),
		Index:         site.IndexFile,
		AccessLog:     site.AccessLog,
		ErrorLogPath:  path.Join(filepath.ToSlash(opts.NginxLogDir), "gopanel-"+site.Name+".error.log"),
		AccessLogPath: path.Join(filepath.ToSlash(opts.NginxLogDir), "gopanel-"+site.Name+".access.log"),
		ACMEWebroot:   filepath.ToSlash(opts.ACMEWebroot),
		Type:          site.Type,
		PHPVersion:    site.PHPVersion,
		Upstream:      site.Upstream,
		ClientMaxBody: site.ClientMaxBody,
		SSL:           site.SSLEnabled,
		CertPath:      filepath.ToSlash(site.SSLCertPath),
		KeyPath:       filepath.ToSlash(site.SSLKeyPath),
	}
	if data.Index == "" {
		data.Index = "index.html"
	}
	if data.SSL {
		if data.CertPath == "" || data.KeyPath == "" {
			return "", fmt.Errorf("SSL is enabled but certificate paths are missing")
		}
		if !safePathRe.MatchString(data.CertPath) || !safePathRe.MatchString(data.KeyPath) {
			return "", fmt.Errorf("unsafe certificate path")
		}
	}
	var buf bytes.Buffer
	if err := vhostTemplate.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
