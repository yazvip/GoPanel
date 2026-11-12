package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"gopanel/internal/audit"
	"gopanel/internal/certs"
	"gopanel/internal/model"
	"gopanel/internal/sites"
	"gopanel/internal/store"
	"gopanel/internal/sys"
	"gopanel/internal/validate"
)

func (s *Server) handleListSites(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListSites(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	if list == nil {
		list = []model.Site{}
	}
	s.ok(w, list)
}

func (s *Server) handleCreateSite(w http.ResponseWriter, r *http.Request) {
	var in sites.Input
	if err := s.decode(w, r, &in, 128<<10); err != nil {
		s.writeError(w, err)
		return
	}
	site, err := s.Sites.Create(r.Context(), in)
	if err != nil {
		s.auditEvent(r, "sites.create", in.Name, err.Error(), audit.ResultFailed)
		s.writeError(w, mapSiteError(err))
		return
	}
	s.auditEvent(r, "sites.create", site.Name, site.RootPath, audit.ResultSuccess)
	s.created(w, site)
}

func mapSiteError(err error) error {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	switch {
	case errors.Is(err, sites.ErrExists):
		return errf(http.StatusConflict, "sites.exists", "%v", err)
	case errors.Is(err, sites.ErrDomainTaken):
		return errf(http.StatusConflict, "sites.domain_taken", "%v", err)
	case errors.Is(err, store.ErrNotFound):
		return errf(http.StatusNotFound, "not_found", "site not found")
	case errors.Is(err, sys.ErrNginxMissing):
		return errf(http.StatusServiceUnavailable, "sites.nginx_missing", "%v", err)
	case errors.Is(err, sites.ErrApplyFailed), errors.Is(err, sites.ErrInvalid), errors.Is(err, sites.ErrRootOutside), errors.Is(err, sites.ErrPanelUpstream):
		return errf(http.StatusUnprocessableEntity, "sites.invalid", "%v", err)
	}
	return errf(http.StatusBadRequest, "sites.invalid", "%v", err)
}

func (s *Server) siteFromPath(w http.ResponseWriter, r *http.Request) (*model.Site, bool) {
	id, err := s.pathID(r, "id")
	if err != nil {
		s.writeError(w, err)
		return nil, false
	}
	site, err := s.Sites.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, errf(http.StatusNotFound, "sites.not_found", "site not found"))
		} else {
			s.writeError(w, err)
		}
		return nil, false
	}
	return site, true
}

func (s *Server) handleGetSite(w http.ResponseWriter, r *http.Request) {
	site, ok := s.siteFromPath(w, r)
	if !ok {
		return
	}
	s.ok(w, site)
}

func (s *Server) handleUpdateSite(w http.ResponseWriter, r *http.Request) {
	site, ok := s.siteFromPath(w, r)
	if !ok {
		return
	}
	var in sites.Input
	if err := s.decode(w, r, &in, 128<<10); err != nil {
		s.writeError(w, err)
		return
	}
	updated, err := s.Sites.Update(r.Context(), site, in)
	if err != nil {
		s.auditEvent(r, "sites.update", site.Name, err.Error(), audit.ResultFailed)
		s.writeError(w, mapSiteError(err))
		return
	}
	s.auditEvent(r, "sites.update", updated.Name, "", audit.ResultSuccess)
	s.ok(w, updated)
}

type deleteSiteRequest struct {
	Confirm     string `json:"confirm"`
	DeleteFiles bool   `json:"delete_files"`
}

func (s *Server) handleDeleteSite(w http.ResponseWriter, r *http.Request) {
	site, ok := s.siteFromPath(w, r)
	if !ok {
		return
	}
	var req deleteSiteRequest
	if err := s.decode(w, r, &req, 16<<10); err != nil {
		s.writeError(w, err)
		return
	}
	if strings.TrimSpace(req.Confirm) != site.Name {
		s.writeError(w, errf(http.StatusBadRequest, "sites.confirm_required", "type the site name to confirm deletion"))
		return
	}
	if err := s.Sites.Delete(r.Context(), site, req.DeleteFiles); err != nil {
		s.auditEvent(r, "sites.delete", site.Name, err.Error(), audit.ResultFailed)
		s.writeError(w, mapSiteError(err))
		return
	}
	s.auditEvent(r, "sites.delete", site.Name, "files_deleted="+boolString(req.DeleteFiles), audit.ResultSuccess)
	s.noContent(w)
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func (s *Server) handleEnableSite(w http.ResponseWriter, r *http.Request) {
	s.setSiteEnabled(w, r, true)
}

func (s *Server) handleDisableSite(w http.ResponseWriter, r *http.Request) {
	s.setSiteEnabled(w, r, false)
}

func (s *Server) setSiteEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	site, ok := s.siteFromPath(w, r)
	if !ok {
		return
	}
	if site.Enabled == enabled {
		s.ok(w, site)
		return
	}
	if err := s.Sites.SetEnabled(r.Context(), site, enabled); err != nil {
		action := "sites.disable"
		if enabled {
			action = "sites.enable"
		}
		s.auditEvent(r, action, site.Name, err.Error(), audit.ResultFailed)
		s.writeError(w, mapSiteError(err))
		return
	}
	action := "sites.disable"
	if enabled {
		action = "sites.enable"
	}
	s.auditEvent(r, action, site.Name, "", audit.ResultSuccess)
	s.ok(w, site)
}

type domainRequest struct {
	Domain string `json:"domain"`
}

func (s *Server) handleAddDomain(w http.ResponseWriter, r *http.Request) {
	site, ok := s.siteFromPath(w, r)
	if !ok {
		return
	}
	var req domainRequest
	if err := s.decode(w, r, &req, 16<<10); err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.Sites.AddDomain(r.Context(), site, req.Domain); err != nil {
		s.writeError(w, mapSiteError(err))
		return
	}
	s.auditEvent(r, "sites.domain_add", site.Name, req.Domain, audit.ResultSuccess)
	site, _ = s.Sites.Get(r.Context(), site.ID)
	s.created(w, site)
}

func (s *Server) handleRemoveDomain(w http.ResponseWriter, r *http.Request) {
	site, ok := s.siteFromPath(w, r)
	if !ok {
		return
	}
	domainID, err := s.pathID(r, "domainId")
	if err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.Sites.RemoveDomain(r.Context(), site, domainID); err != nil {
		s.writeError(w, mapSiteError(err))
		return
	}
	s.auditEvent(r, "sites.domain_remove", site.Name, domainIDString(domainID), audit.ResultSuccess)
	site, _ = s.Sites.Get(r.Context(), site.ID)
	s.ok(w, site)
}

func domainIDString(id int64) string {
	return strconv.FormatInt(id, 10)
}

func (s *Server) handlePrimaryDomain(w http.ResponseWriter, r *http.Request) {
	site, ok := s.siteFromPath(w, r)
	if !ok {
		return
	}
	domainID, err := s.pathID(r, "domainId")
	if err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.Sites.SetPrimary(r.Context(), site, domainID); err != nil {
		s.writeError(w, mapSiteError(err))
		return
	}
	s.auditEvent(r, "sites.domain_primary", site.Name, domainIDString(domainID), audit.ResultSuccess)
	site, _ = s.Sites.Get(r.Context(), site.ID)
	s.ok(w, site)
}

func (s *Server) handleSiteLogs(w http.ResponseWriter, r *http.Request) {
	site, ok := s.siteFromPath(w, r)
	if !ok {
		return
	}
	kind := r.URL.Query().Get("type")
	if kind != "access" && kind != "error" {
		kind = "error"
	}
	lines := s.queryInt(r, "lines", 200)
	if lines <= 0 || lines > 2000 {
		lines = 200
	}
	content, err := s.Sites.Logs(r.Context(), site, kind, lines)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.ok(w, map[string]any{"type": kind, "lines": lines, "content": content})
}

type sslRequest struct {
	Email   string `json:"email"`
	Staging *bool  `json:"staging"`
}

func (s *Server) handleEnableSSL(w http.ResponseWriter, r *http.Request) {
	site, ok := s.siteFromPath(w, r)
	if !ok {
		return
	}
	var req sslRequest
	if err := s.decode(w, r, &req, 16<<10); err != nil {
		s.writeError(w, err)
		return
	}
	if req.Email != "" {
		if err := validate.Email(req.Email); err != nil {
			s.writeError(w, errf(http.StatusBadRequest, "settings.email_invalid", "%v", err))
			return
		}
	}
	if s.Certs == nil || !s.Certs.Configured() {
		s.writeError(w, errf(http.StatusConflict, "ssl.not_configured", "configure an ACME email in Settings before requesting certificates"))
		return
	}
	payload := certs.JobPayload{SiteID: site.ID, Email: req.Email, Staging: req.Staging}
	job, err := s.Jobs.Enqueue(r.Context(), certs.JobType, payload, s.principal(r).User.ID)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "sites.ssl", site.Name, "job="+job.ID, audit.ResultSuccess)
	s.accepted(w, job)
}

func (s *Server) handleRenewSSL(w http.ResponseWriter, r *http.Request) {
	s.handleEnableSSL(w, r)
}

func (s *Server) handleDisableSSL(w http.ResponseWriter, r *http.Request) {
	site, ok := s.siteFromPath(w, r)
	if !ok {
		return
	}
	if !site.SSLEnabled {
		s.ok(w, site)
		return
	}
	if err := s.Sites.SetSSL(r.Context(), site, false, "", "", 0); err != nil {
		s.auditEvent(r, "sites.ssl_disable", site.Name, err.Error(), audit.ResultFailed)
		s.writeError(w, mapSiteError(err))
		return
	}
	s.auditEvent(r, "sites.ssl_disable", site.Name, "", audit.ResultSuccess)
	s.ok(w, site)
}
