package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopanel/internal/acme"
	"gopanel/internal/apps"
	"gopanel/internal/audit"
	"gopanel/internal/auth"
	"gopanel/internal/backup"
	"gopanel/internal/certs"
	"gopanel/internal/config"
	"gopanel/internal/cronjobs"
	"gopanel/internal/databases"
	"gopanel/internal/files"
	"gopanel/internal/jobs"
	"gopanel/internal/metrics"
	"gopanel/internal/model"
	"gopanel/internal/sites"
	"gopanel/internal/store"
	"gopanel/internal/sys"
	"gopanel/internal/terminal"
	"gopanel/web"
)

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"

	APIPrefix = "/api/v1"
)

type Deps struct {
	Config    *config.Config
	Store     *store.Store
	Auth      *auth.Service
	Metrics   *metrics.Service
	Files     *files.Service
	Sites     *sites.Service
	Backups   *backup.Service
	Cron      *cronjobs.Service
	Apps      *apps.Service
	Jobs      *jobs.Manager
	Certs     *certs.Service
	Databases *databases.Service
	ACME      *acme.Service
	Terminal  *terminal.Manager
	Audit     *audit.Recorder
	System    sys.System
	Logger    *slog.Logger
	Version   string
	Started   time.Time
}

type Server struct {
	Deps
	logger   *slog.Logger
	mux      *http.ServeMux
	trusted  []netip.Prefix
	limit    *ipLimiter
	handlers []routeDescription
}

type routeDescription struct {
	method  string
	pattern string
	roles   []string
}

type ctxKey int

const principalKey ctxKey = 1

type Principal struct {
	User    *model.User
	Session *model.Session
}

func New(deps Deps) (*Server, error) {
	if deps.Auth == nil || deps.Store == nil || deps.System == nil {
		return nil, fmt.Errorf("httpapi requires auth, store and system dependencies")
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Started.IsZero() {
		deps.Started = time.Now()
	}
	s := &Server{Deps: deps, logger: deps.Logger, mux: http.NewServeMux(), limit: newIPLimiter(1200, time.Minute)}
	if deps.Config != nil {
		for _, raw := range deps.Config.TrustedProxies {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
			if err != nil {
				s.logger.Warn("ignore invalid trusted proxy", "value", raw, "error", err)
				continue
			}
			s.trusted = append(s.trusted, prefix)
		}
	}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, APIPrefix+"/") || r.URL.Path == APIPrefix {
			s.mux.ServeHTTP(w, r)
			return
		}
		s.serveStatic(w, r)
	})
	return s.recoverer(s.accessLog(s.securityHeaders(handler)))
}

func (s *Server) routes() {
	s.handle(http.MethodGet, "/healthz", s.handleHealth)

	s.handle(http.MethodPost, "/auth/login", s.handleLogin)
	s.handle(http.MethodPost, "/auth/totp", s.handleTOTP)
	s.handle(http.MethodPost, "/auth/logout", s.handleLogout)
	s.handle(http.MethodGet, "/auth/me", s.handleMe)
	s.handle(http.MethodPut, "/auth/password", s.handleChangePassword)
	s.handle(http.MethodPost, "/auth/2fa/setup", s.handle2FASetup)
	s.handle(http.MethodPost, "/auth/2fa/enable", s.handle2FAEnable)
	s.handle(http.MethodPost, "/auth/2fa/disable", s.handle2FADisable)
	s.handle(http.MethodPost, "/auth/2fa/recovery", s.handle2FARecovery)
	s.handle(http.MethodGet, "/auth/sessions", s.handleListSessions)
	s.handle(http.MethodDelete, "/auth/sessions/{id}", s.handleDeleteSession)

	s.handleRoles(http.MethodGet, "/users", only(RoleAdmin), s.handleListUsers)
	s.handleRoles(http.MethodPost, "/users", only(RoleAdmin), s.handleCreateUser)
	s.handleRoles(http.MethodPatch, "/users/{id}", only(RoleAdmin), s.handleUpdateUser)
	s.handleRoles(http.MethodDelete, "/users/{id}", only(RoleAdmin), s.handleDeleteUser)
	s.handleRoles(http.MethodPost, "/users/{id}/password", only(RoleAdmin), s.handleResetPassword)

	s.handle(http.MethodGet, "/dashboard/summary", s.handleDashboard)
	s.handle(http.MethodGet, "/metrics", s.handleMetrics)
	s.handle(http.MethodGet, "/processes", s.handleProcesses)
	s.handleRoles(http.MethodPost, "/processes/{pid}/kill", operator(), s.handleKillProcess)
	s.handle(http.MethodGet, "/services", s.handleServices)
	s.handleRoles(http.MethodPost, "/services/{name}/{action}", operator(), s.handleServiceAction)
	s.handle(http.MethodGet, "/host", s.handleHost)

	s.handle(http.MethodGet, "/files/roots", s.handleFileRoots)
	s.handle(http.MethodGet, "/files", s.handleFileList)
	s.handle(http.MethodGet, "/files/stat", s.handleFileStat)
	s.handle(http.MethodGet, "/files/download", s.handleFileDownload)
	s.handle(http.MethodGet, "/files/preview", s.handleFilePreview)
	s.handle(http.MethodGet, "/files/content", s.handleFileRead)
	s.handleRoles(http.MethodPut, "/files/content", operator(), s.handleFileWrite)
	s.handleRoles(http.MethodPost, "/files/upload", operator(), s.handleFileUpload)
	s.handleRoles(http.MethodPost, "/files/mkdir", operator(), s.handleFileMkdir)
	s.handleRoles(http.MethodPost, "/files/create", operator(), s.handleFileCreate)
	s.handleRoles(http.MethodPost, "/files/rename", operator(), s.handleFileRename)
	s.handleRoles(http.MethodPost, "/files/copy", operator(), s.handleFileCopy)
	s.handleRoles(http.MethodPost, "/files/move", operator(), s.handleFileMove)
	s.handleRoles(http.MethodPost, "/files/delete", operator(), s.handleFileDelete)
	s.handleRoles(http.MethodPost, "/files/chmod", operator(), s.handleFileChmod)
	s.handleRoles(http.MethodPost, "/files/archive", operator(), s.handleFileArchive)
	s.handleRoles(http.MethodPost, "/files/extract", operator(), s.handleFileExtract)
	s.handle(http.MethodGet, "/files/search", s.handleFileSearch)

	s.handle(http.MethodGet, "/sites", s.handleListSites)
	s.handleRoles(http.MethodPost, "/sites", operator(), s.handleCreateSite)
	s.handle(http.MethodGet, "/sites/{id}", s.handleGetSite)
	s.handleRoles(http.MethodPatch, "/sites/{id}", operator(), s.handleUpdateSite)
	s.handleRoles(http.MethodDelete, "/sites/{id}", operator(), s.handleDeleteSite)
	s.handleRoles(http.MethodPost, "/sites/{id}/enable", operator(), s.handleEnableSite)
	s.handleRoles(http.MethodPost, "/sites/{id}/disable", operator(), s.handleDisableSite)
	s.handleRoles(http.MethodPost, "/sites/{id}/domains", operator(), s.handleAddDomain)
	s.handleRoles(http.MethodDelete, "/sites/{id}/domains/{domainId}", operator(), s.handleRemoveDomain)
	s.handleRoles(http.MethodPost, "/sites/{id}/domains/{domainId}/primary", operator(), s.handlePrimaryDomain)
	s.handle(http.MethodGet, "/sites/{id}/logs", s.handleSiteLogs)
	s.handleRoles(http.MethodPost, "/sites/{id}/ssl", operator(), s.handleEnableSSL)
	s.handleRoles(http.MethodPost, "/sites/{id}/ssl/renew", operator(), s.handleRenewSSL)
	s.handleRoles(http.MethodDelete, "/sites/{id}/ssl", operator(), s.handleDisableSSL)
	s.handle(http.MethodGet, "/php/versions", s.handlePHPVersions)

	s.handle(http.MethodGet, "/databases", s.handleListDatabases)
	s.handleRoles(http.MethodPost, "/databases", operator(), s.handleCreateDatabase)
	s.handleRoles(http.MethodDelete, "/databases/{name}", operator(), s.handleDeleteDatabase)
	s.handle(http.MethodGet, "/databases/users", s.handleListDatabaseUsers)
	s.handleRoles(http.MethodPost, "/databases/users", operator(), s.handleCreateDatabaseUser)
	s.handleRoles(http.MethodDelete, "/databases/users/{user}", operator(), s.handleDeleteDatabaseUser)
	s.handleRoles(http.MethodPost, "/databases/{name}/password", operator(), s.handleDatabasePassword)
	s.handleRoles(http.MethodPost, "/databases/{name}/backup", operator(), s.handleDatabaseBackup)

	s.handle(http.MethodGet, "/cron", s.handleListCron)
	s.handleRoles(http.MethodPost, "/cron", operator(), s.handleCreateCron)
	s.handleRoles(http.MethodPatch, "/cron/{id}", operator(), s.handleUpdateCron)
	s.handleRoles(http.MethodDelete, "/cron/{id}", operator(), s.handleDeleteCron)
	s.handleRoles(http.MethodPost, "/cron/{id}/run", operator(), s.handleRunCron)
	s.handle(http.MethodGet, "/cron/{id}/log", s.handleCronLog)

	s.handle(http.MethodGet, "/firewall", s.handleFirewall)
	s.handleRoles(http.MethodPost, "/firewall/enable", operator(), s.handleFirewallEnable)
	s.handleRoles(http.MethodPost, "/firewall/disable", operator(), s.handleFirewallDisable)
	s.handleRoles(http.MethodPost, "/firewall/rules", operator(), s.handleFirewallAddRule)
	s.handleRoles(http.MethodDelete, "/firewall/rules/{num}", operator(), s.handleFirewallDeleteRule)

	s.handle(http.MethodGet, "/backups", s.handleListBackups)
	s.handleRoles(http.MethodPost, "/backups", operator(), s.handleCreateBackup)
	s.handleRoles(http.MethodDelete, "/backups/{id}", operator(), s.handleDeleteBackup)
	s.handleRoles(http.MethodPost, "/backups/{id}/restore", operator(), s.handleRestoreBackup)
	s.handle(http.MethodGet, "/backups/{id}/download", s.handleDownloadBackup)
	s.handle(http.MethodGet, "/backup-schedules", s.handleListSchedules)
	s.handleRoles(http.MethodPost, "/backup-schedules", operator(), s.handleCreateSchedule)
	s.handleRoles(http.MethodPatch, "/backup-schedules/{id}", operator(), s.handleUpdateSchedule)
	s.handleRoles(http.MethodDelete, "/backup-schedules/{id}", operator(), s.handleDeleteSchedule)

	s.handle(http.MethodGet, "/apps/catalog", s.handleAppsCatalog)
	s.handleRoles(http.MethodPost, "/apps/install", operator(), s.handleAppsInstall)
	s.handleRoles(http.MethodPost, "/apps/update-cache", operator(), s.handleAppsUpdateCache)

	s.handle(http.MethodGet, "/jobs", s.handleListJobs)
	s.handle(http.MethodGet, "/jobs/{id}", s.handleGetJob)
	s.handle(http.MethodGet, "/jobs/{id}/events", s.handleJobEvents)

	s.handleRoles(http.MethodPost, "/terminal/ticket", only(RoleAdmin), s.handleTerminalTicket)
	s.handle(http.MethodGet, "/terminal/ws", s.handleTerminalWS)

	s.handle(http.MethodGet, "/settings", s.handleGetSettings)
	s.handleRoles(http.MethodPatch, "/settings", only(RoleAdmin), s.handleUpdateSettings)
	s.handleRoles(http.MethodPost, "/settings/tls", only(RoleAdmin), s.handleUploadTLS)
	s.handleRoles(http.MethodGet, "/audit", only(RoleAdmin), s.handleAudit)
}

func (s *Server) handle(method, pattern string, handler http.HandlerFunc) {
	s.handleRoles(method, pattern, nil, handler)
}

func only(roles ...string) []string { return roles }

func operator() []string { return []string{RoleAdmin, RoleOperator} }

func (s *Server) handleRoles(method, pattern string, roles []string, handler http.HandlerFunc) {
	s.handlers = append(s.handlers, routeDescription{method: method, pattern: pattern, roles: roles})
	public := pattern == "/auth/login" || pattern == "/auth/totp" || pattern == "/healthz" || pattern == "/terminal/ws"
	wrapped := s.publicCSRF(handler)
	if !public {
		wrapped = s.authorized(roles, handler)
	}
	s.mux.HandleFunc(method+" "+APIPrefix+pattern, s.rateLimit(wrapped))
}

func (s *Server) authorized(roles []string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.principalFrom(r)
		if err != nil {
			s.writeJSON(w, http.StatusUnauthorized, map[string]any{"error": &apiError{Status: http.StatusUnauthorized, Code: "auth.required", Message: "login required"}})
			return
		}
		if !safeMethod(r.Method) {
			if err := s.checkCSRF(r, p); err != nil {
				s.writeError(w, err)
				return
			}
		}
		if len(roles) > 0 && !hasRole(p.User.Role, roles) {
			s.auditDenied(r, p, "permission denied")
			s.writeError(w, errf(http.StatusForbidden, "auth.forbidden", "your role does not allow this action"))
			return
		}
		ctx := context.WithValue(r.Context(), principalKey, p)
		handler(w, r.WithContext(ctx))
	}
}

func (s *Server) publicCSRF(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.Auth.EnsureCSRFCookie(w, r)
		handler(w, r)
	}
}

func (s *Server) principalFrom(r *http.Request) (*Principal, error) {
	user, sess, err := s.Auth.SessionFromRequest(r.Context(), r)
	if err != nil {
		return nil, err
	}
	return &Principal{User: user, Session: sess}, nil
}

func (s *Server) checkCSRF(r *http.Request, p *Principal) error {
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return errf(http.StatusForbidden, "auth.csrf", "cross-origin request rejected")
		}
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		return errf(http.StatusForbidden, "auth.csrf", "cross-site request rejected")
	}
	if !s.Auth.ValidateCSRF(r, p.Session.CSRF) {
		return errf(http.StatusForbidden, "auth.csrf", "invalid CSRF token")
	}
	return nil
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func hasRole(role string, allowed []string) bool {
	for _, candidate := range allowed {
		if role == candidate {
			return true
		}
	}
	return false
}

func (s *Server) principal(r *http.Request) *Principal {
	p, _ := r.Context().Value(principalKey).(*Principal)
	return p
}

func (s *Server) clientIP(r *http.Request) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	if s.Config != nil && s.Config.BehindProxy && s.isTrusted(remote) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if net.ParseIP(first) != nil {
				return first
			}
		}
		if rip := strings.TrimSpace(r.Header.Get("X-Real-IP")); rip != "" && net.ParseIP(rip) != nil {
			return rip
		}
	}
	return remote
}

func (s *Server) isTrusted(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	for _, prefix := range s.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func (s *Server) auditEvent(r *http.Request, action, target, detail, result string) {
	if s.Audit == nil {
		return
	}
	p := s.principal(r)
	event := audit.Event{IP: s.clientIP(r), Action: action, Target: target, Detail: detail, Result: result}
	if p != nil && p.User != nil {
		event.UserID = p.User.ID
		event.Username = p.User.Username
	}
	s.Audit.Record(r.Context(), event)
}

func (s *Server) auditDenied(r *http.Request, p *Principal, detail string) {
	if s.Audit == nil {
		return
	}
	s.Audit.Denied(r.Context(), audit.Event{
		UserID:   p.User.ID,
		Username: p.User.Username,
		IP:       s.clientIP(r),
		Action:   strings.TrimPrefix(r.URL.Path, APIPrefix+"/"),
		Detail:   detail,
	})
}

func (s *Server) pathID(r *http.Request, name string) (int64, error) {
	raw := r.PathValue(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errf(http.StatusBadRequest, "request.invalid_id", "invalid %s", name)
	}
	return id, nil
}

func (s *Server) queryInt(r *http.Request, name string, def int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "..") {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if r.URL.Path == "/" || !strings.Contains(path.Base(r.URL.Path), ".") {
		clone := r.Clone(r.Context())
		clone.URL.Path = "/"
		web.Handler().ServeHTTP(w, clone)
		return
	}
	web.Handler().ServeHTTP(w, r)
}

type ipBucket struct {
	count   int
	resetAt time.Time
}

type ipLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]*ipBucket
}

func newIPLimiter(limit int, window time.Duration) *ipLimiter {
	return &ipLimiter{limit: limit, window: window, buckets: map[string]*ipBucket{}}
}

func (l *ipLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.buckets) > 10000 {
		for k, b := range l.buckets {
			if now.After(b.resetAt) {
				delete(l.buckets, k)
			}
		}
	}
	b, ok := l.buckets[key]
	if !ok || now.After(b.resetAt) {
		l.buckets[key] = &ipBucket{count: 1, resetAt: now.Add(l.window)}
		return true, 0
	}
	if b.count >= l.limit {
		return false, b.resetAt.Sub(now)
	}
	b.count++
	return true, 0
}

func (s *Server) rateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ok, retry := s.limit.allow(s.clientIP(r)); !ok {
			seconds := int(retry.Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			s.writeError(w, errf(http.StatusTooManyRequests, "rate_limited", "too many requests, retry in %d seconds", seconds))
			return
		}
		next(w, r)
	}
}
