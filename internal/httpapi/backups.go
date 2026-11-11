package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"strings"

	"gopanel/internal/audit"
	"gopanel/internal/backup"
	"gopanel/internal/model"
	"gopanel/internal/store"
	"gopanel/internal/validate"
)

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListBackups(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	if list == nil {
		list = []model.Backup{}
	}
	kind := r.URL.Query().Get("kind")
	ref := r.URL.Query().Get("ref")
	if kind == "" && ref == "" {
		s.ok(w, list)
		return
	}
	filtered := make([]model.Backup, 0, len(list))
	for _, item := range list {
		if kind != "" && item.Kind != kind {
			continue
		}
		if ref != "" && item.Ref != ref {
			continue
		}
		filtered = append(filtered, item)
	}
	s.ok(w, filtered)
}

type createBackupRequest struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	Keep int    `json:"keep"`
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	if s.Backups == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "backups.unavailable", "the backup module is not enabled"))
		return
	}
	var req createBackupRequest
	if err := s.decode(w, r, &req, 16<<10); err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.validateBackupTarget(r, req.Kind, req.Ref); err != nil {
		s.writeError(w, err)
		return
	}
	if req.Keep < 0 || req.Keep > 100 {
		s.writeError(w, errf(http.StatusBadRequest, "backups.keep_invalid", "keep must be between 1 and 100"))
		return
	}
	payload := backup.JobPayload{Kind: req.Kind, Ref: req.Ref, Keep: req.Keep}
	job, err := s.Jobs.Enqueue(r.Context(), backup.JobType, payload, s.principal(r).User.ID)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "backups.create", req.Ref, req.Kind+" job="+job.ID, audit.ResultSuccess)
	s.accepted(w, job)
}

func (s *Server) validateBackupTarget(r *http.Request, kind, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errf(http.StatusBadRequest, "backups.target_required", "choose a site or database to back up")
	}
	switch kind {
	case backup.KindSite:
		if _, err := s.Store.GetSiteByName(r.Context(), ref); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return errf(http.StatusNotFound, "sites.not_found", "site not found")
			}
			return err
		}
	case backup.KindDatabase:
		if err := validate.DatabaseName(ref); err != nil {
			return errf(http.StatusBadRequest, "databases.name_invalid", "%v", err)
		}
	default:
		return errf(http.StatusBadRequest, "backups.kind_invalid", "kind must be site or database")
	}
	return nil
}

func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	if s.Backups == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "backups.unavailable", "the backup module is not enabled"))
		return
	}
	id := r.PathValue("id")
	if err := s.Backups.Delete(r.Context(), id); err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "backups.delete", id, "", audit.ResultSuccess)
	s.noContent(w)
}

type restoreBackupRequest struct {
	Confirm string `json:"confirm"`
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	if s.Backups == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "backups.unavailable", "the backup module is not enabled"))
		return
	}
	id := r.PathValue("id")
	rec, err := s.Store.GetBackup(r.Context(), id)
	if err != nil {
		s.writeError(w, err)
		return
	}
	var req restoreBackupRequest
	if err := s.decode(w, r, &req, 16<<10); err != nil {
		s.writeError(w, err)
		return
	}
	if strings.TrimSpace(req.Confirm) != rec.Ref {
		s.writeError(w, errf(http.StatusBadRequest, "backups.confirm_required", "type %q to confirm the restore", rec.Ref))
		return
	}
	if rec.Status != "success" {
		s.writeError(w, errf(http.StatusConflict, "backups.not_ready", "that backup did not finish successfully"))
		return
	}
	job, err := s.Jobs.Enqueue(r.Context(), backup.RestoreJobType, backup.RestoreJobPayload{BackupID: id}, s.principal(r).User.ID)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "backups.restore", rec.Ref, rec.Kind+" job="+job.ID, audit.ResultSuccess)
	s.accepted(w, job)
}

func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	if s.Backups == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "backups.unavailable", "the backup module is not enabled"))
		return
	}
	id := r.PathValue("id")
	file, name, err := s.Backups.Open(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, errf(http.StatusNotFound, "not_found", "backup not found"))
			return
		}
		s.writeError(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		s.writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, info.ModTime(), file)
}

type scheduleRequest struct {
	Kind     string `json:"kind"`
	Ref      string `json:"ref"`
	CronExpr string `json:"cron_expr"`
	Keep     int    `json:"keep"`
	Enabled  *bool  `json:"enabled"`
}

func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListSchedules(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	if list == nil {
		list = []model.BackupSchedule{}
	}
	s.ok(w, list)
}

func (s *Server) scheduleInput(w http.ResponseWriter, r *http.Request, req *scheduleRequest) (*model.BackupSchedule, bool) {
	if err := s.validateBackupTarget(r, req.Kind, req.Ref); err != nil {
		s.writeError(w, err)
		return nil, false
	}
	expr := strings.TrimSpace(req.CronExpr)
	if err := validate.CronSchedule(expr); err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "schedules.cron_invalid", "%v", err))
		return nil, false
	}
	if req.Keep <= 0 || req.Keep > 100 {
		s.writeError(w, errf(http.StatusBadRequest, "schedules.keep_invalid", "keep must be between 1 and 100"))
		return nil, false
	}
	return &model.BackupSchedule{Kind: req.Kind, Ref: req.Ref, CronExpr: expr, Keep: req.Keep, Enabled: true}, true
}

func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	var req scheduleRequest
	if err := s.decode(w, r, &req, 16<<10); err != nil {
		s.writeError(w, err)
		return
	}
	sch, ok := s.scheduleInput(w, r, &req)
	if !ok {
		return
	}
	if req.Enabled != nil {
		sch.Enabled = *req.Enabled
	}
	if err := s.Store.CreateSchedule(r.Context(), sch); err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "backups.schedule_create", sch.Ref, sch.CronExpr, audit.ResultSuccess)
	s.created(w, sch)
}

func (s *Server) handleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	id, err := s.pathID(r, "id")
	if err != nil {
		s.writeError(w, err)
		return
	}
	sch, err := s.Store.GetSchedule(r.Context(), id)
	if err != nil {
		s.writeError(w, err)
		return
	}
	var req scheduleRequest
	if err := s.decode(w, r, &req, 16<<10); err != nil {
		s.writeError(w, err)
		return
	}
	merged := scheduleRequest{Kind: sch.Kind, Ref: sch.Ref, CronExpr: sch.CronExpr, Keep: sch.Keep}
	if req.Kind != "" {
		merged.Kind = req.Kind
	}
	if req.Ref != "" {
		merged.Ref = req.Ref
	}
	if req.CronExpr != "" {
		merged.CronExpr = req.CronExpr
	}
	if req.Keep > 0 {
		merged.Keep = req.Keep
	}
	validated, ok := s.scheduleInput(w, r, &merged)
	if !ok {
		return
	}
	sch.Kind = validated.Kind
	sch.Ref = validated.Ref
	sch.CronExpr = validated.CronExpr
	sch.Keep = validated.Keep
	if req.Enabled != nil {
		sch.Enabled = *req.Enabled
	}
	if err := s.Store.UpdateSchedule(r.Context(), sch); err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "backups.schedule_update", sch.Ref, sch.CronExpr, audit.ResultSuccess)
	s.ok(w, sch)
}

func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	id, err := s.pathID(r, "id")
	if err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.Store.DeleteSchedule(r.Context(), id); err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "backups.schedule_delete", "", "id", audit.ResultSuccess)
	s.noContent(w)
}
