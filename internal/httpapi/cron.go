package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"gopanel/internal/audit"
	"gopanel/internal/cronjobs"
	"gopanel/internal/model"
	"gopanel/internal/store"
)

func (s *Server) handleListCron(w http.ResponseWriter, r *http.Request) {
	list, err := s.Cron.List(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	if list == nil {
		list = []model.CronJob{}
	}
	s.ok(w, list)
}

type cronRequest struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Enabled  *bool  `json:"enabled"`
}

func (s *Server) cronFromPath(w http.ResponseWriter, r *http.Request) (*model.CronJob, bool) {
	id, err := s.pathID(r, "id")
	if err != nil {
		s.writeError(w, err)
		return nil, false
	}
	job, err := s.Cron.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, errf(http.StatusNotFound, "cron.not_found", "cron job not found"))
		} else {
			s.writeError(w, err)
		}
		return nil, false
	}
	return job, true
}

func (s *Server) handleCreateCron(w http.ResponseWriter, r *http.Request) {
	var req cronRequest
	if err := s.decode(w, r, &req, 64<<10); err != nil {
		s.writeError(w, err)
		return
	}
	in := cronjobs.Input{Name: req.Name, Schedule: req.Schedule, Command: req.Command, Enabled: true}
	if req.Enabled != nil {
		in.Enabled = *req.Enabled
	}
	job, err := s.Cron.Create(r.Context(), in, s.principal(r).User.ID)
	if err != nil {
		s.auditEvent(r, "cron.create", req.Name, err.Error(), audit.ResultFailed)
		s.writeError(w, mapCronError(err))
		return
	}
	s.auditEvent(r, "cron.create", job.Name, job.Schedule, audit.ResultSuccess)
	s.created(w, job)
}

func mapCronError(err error) error {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	if errors.Is(err, store.ErrNotFound) {
		return errf(http.StatusNotFound, "cron.not_found", "cron job not found")
	}
	return errf(http.StatusBadRequest, "cron.invalid", "%v", err)
}

func (s *Server) handleUpdateCron(w http.ResponseWriter, r *http.Request) {
	job, ok := s.cronFromPath(w, r)
	if !ok {
		return
	}
	var req cronRequest
	if err := s.decode(w, r, &req, 64<<10); err != nil {
		s.writeError(w, err)
		return
	}
	in := cronjobs.Input{Name: job.Name, Schedule: job.Schedule, Command: job.Command, Enabled: job.Enabled}
	if strings.TrimSpace(req.Name) != "" {
		in.Name = req.Name
	}
	if strings.TrimSpace(req.Schedule) != "" {
		in.Schedule = req.Schedule
	}
	if strings.TrimSpace(req.Command) != "" {
		in.Command = req.Command
	}
	if req.Enabled != nil {
		in.Enabled = *req.Enabled
	}
	updated, err := s.Cron.Update(r.Context(), job, in)
	if err != nil {
		s.auditEvent(r, "cron.update", job.Name, err.Error(), audit.ResultFailed)
		s.writeError(w, mapCronError(err))
		return
	}
	s.auditEvent(r, "cron.update", updated.Name, updated.Schedule, audit.ResultSuccess)
	s.ok(w, updated)
}

func (s *Server) handleDeleteCron(w http.ResponseWriter, r *http.Request) {
	job, ok := s.cronFromPath(w, r)
	if !ok {
		return
	}
	if err := s.Cron.Delete(r.Context(), job.ID); err != nil {
		s.writeError(w, mapCronError(err))
		return
	}
	s.auditEvent(r, "cron.delete", job.Name, "", audit.ResultSuccess)
	s.noContent(w)
}

func (s *Server) handleRunCron(w http.ResponseWriter, r *http.Request) {
	job, ok := s.cronFromPath(w, r)
	if !ok {
		return
	}
	payload := cronjobs.JobPayload{ID: job.ID}
	j, err := s.Jobs.Enqueue(r.Context(), cronjobs.JobType, payload, s.principal(r).User.ID)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "cron.run", job.Name, "job="+j.ID, audit.ResultSuccess)
	s.accepted(w, j)
}

func (s *Server) handleCronLog(w http.ResponseWriter, r *http.Request) {
	job, ok := s.cronFromPath(w, r)
	if !ok {
		return
	}
	lines := s.queryInt(r, "lines", 200)
	if lines <= 0 || lines > 2000 {
		lines = 200
	}
	content, err := s.Cron.Log(r.Context(), job.ID, lines)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.ok(w, map[string]any{"id": job.ID, "name": job.Name, "lines": lines, "content": content})
}
