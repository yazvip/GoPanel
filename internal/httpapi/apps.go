package httpapi

import (
	"net/http"
	"strings"

	"gopanel/internal/apps"
	"gopanel/internal/audit"
)

func (s *Server) handleAppsCatalog(w http.ResponseWriter, r *http.Request) {
	items, err := s.Apps.Catalog(r.Context())
	if err != nil {
		s.writeError(w, errf(http.StatusBadGateway, "apps.catalog_failed", "%v", err))
		return
	}
	if items == nil {
		items = []apps.Item{}
	}
	s.ok(w, items)
}

type appsInstallRequest struct {
	Apps        []string `json:"apps"`
	UpdateCache bool     `json:"update_cache"`
}

func (s *Server) handleAppsInstall(w http.ResponseWriter, r *http.Request) {
	var req appsInstallRequest
	if err := s.decode(w, r, &req, 32<<10); err != nil {
		s.writeError(w, err)
		return
	}
	names := make([]string, 0, len(req.Apps))
	for _, name := range req.Apps {
		name = strings.TrimSpace(name)
		if name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		s.writeError(w, errf(http.StatusBadRequest, "apps.empty", "select at least one application"))
		return
	}
	if len(names) > 12 {
		s.writeError(w, errf(http.StatusBadRequest, "apps.too_many", "install at most 12 applications at once"))
		return
	}
	payload := apps.Request{Apps: names, UpdateCache: req.UpdateCache}
	job, err := s.Jobs.Enqueue(r.Context(), apps.JobType, payload, s.principal(r).User.ID)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "apps.install", strings.Join(names, ","), "job="+job.ID, audit.ResultSuccess)
	s.accepted(w, job)
}

func (s *Server) handleAppsUpdateCache(w http.ResponseWriter, r *http.Request) {
	job, err := s.Jobs.Enqueue(r.Context(), apps.UpdateJobType, struct{}{}, s.principal(r).User.ID)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "apps.update_cache", "", "job="+job.ID, audit.ResultSuccess)
	s.accepted(w, job)
}
