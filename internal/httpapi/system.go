package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gopanel/internal/audit"
	"gopanel/internal/jobs"
	"gopanel/internal/metrics"
	"gopanel/internal/model"
	"gopanel/internal/validate"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.ok(w, map[string]any{
		"status":  "ok",
		"version": s.Version,
		"uptime":  int64(time.Since(s.Started).Seconds()),
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if s.Metrics == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "metrics.unavailable", "metrics are not available"))
		return
	}
	overview, err := s.Metrics.Overview(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	sites, err := s.Store.CountSites(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	domains, err := s.Store.CountDomains(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	services, err := s.System.ListServices(r.Context())
	if err != nil {
		s.logger.Debug("list services for dashboard", "error", err)
	}
	recentJobs, err := s.Store.ListJobs(r.Context(), 8)
	if err != nil {
		s.writeError(w, err)
		return
	}
	backups, err := s.Store.ListBackups(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	if len(backups) > 5 {
		backups = backups[:5]
	}
	s.ok(w, map[string]any{
		"overview": overview,
		"counts": map[string]int{
			"sites":   sites,
			"domains": domains,
		},
		"services": services,
		"jobs":     recentJobs,
		"backups":  backups,
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.Metrics == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "metrics.unavailable", "metrics are not available"))
		return
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "1h"
	}
	list, err := s.Metrics.History(r.Context(), rng)
	if err != nil {
		if errors.Is(err, metrics.ErrRange) {
			s.writeError(w, errf(http.StatusBadRequest, "metrics.range_invalid", "range must be one of 1h, 6h, 24h, 7d"))
			return
		}
		s.writeError(w, err)
		return
	}
	s.ok(w, map[string]any{
		"range":  rng,
		"points": list,
		"live":   s.metricsLive(r),
	})
}

func (s *Server) metricsLive(r *http.Request) any {
	if s.Metrics == nil {
		return nil
	}
	live, err := s.Metrics.Snapshot(r.Context())
	if err != nil {
		return nil
	}
	return live
}

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	sortBy := r.URL.Query().Get("sort")
	switch sortBy {
	case "", "cpu", "mem":
	default:
		s.writeError(w, errf(http.StatusBadRequest, "processes.sort_invalid", "sort must be cpu or mem"))
		return
	}
	if sortBy == "" {
		sortBy = "cpu"
	}
	limit := s.queryInt(r, "limit", 50)
	if limit <= 0 || limit > 300 {
		limit = 50
	}
	list, err := s.System.Processes(r.Context(), sortBy, limit)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.ok(w, list)
}

func (s *Server) handleKillProcess(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("pid")
	pid, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || pid <= 1 {
		s.writeError(w, errf(http.StatusBadRequest, "processes.pid_invalid", "invalid process id"))
		return
	}
	if err := s.System.KillProcess(r.Context(), int32(pid)); err != nil {
		s.auditEvent(r, "processes.kill", raw, err.Error(), audit.ResultFailed)
		s.writeError(w, errf(http.StatusBadRequest, "processes.kill_failed", "%v", err))
		return
	}
	s.auditEvent(r, "processes.kill", raw, "", audit.ResultSuccess)
	s.noContent(w)
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	list, err := s.System.ListServices(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.ok(w, list)
}

func (s *Server) handleServiceAction(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	action := r.PathValue("action")
	if err := validate.ServiceUnit(name); err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "services.name_invalid", "%v", err))
		return
	}
	switch action {
	case "start", "stop", "restart", "reload":
	default:
		s.writeError(w, errf(http.StatusBadRequest, "services.action_invalid", "action must be start, stop, restart or reload"))
		return
	}
	if err := s.System.ServiceAction(r.Context(), name, action); err != nil {
		s.auditEvent(r, "services."+action, name, err.Error(), audit.ResultFailed)
		s.writeError(w, errf(http.StatusBadRequest, "services.action_failed", "%v", err))
		return
	}
	s.auditEvent(r, "services."+action, name, "", audit.ResultSuccess)
	list, err := s.System.ListServices(r.Context())
	if err != nil {
		s.noContent(w)
		return
	}
	s.ok(w, list)
}

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	info, err := s.System.HostInfo(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.ok(w, info)
}

func (s *Server) handlePHPVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := s.System.PHPVersions(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.ok(w, versions)
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	limit := s.queryInt(r, "limit", 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := s.Store.ListJobs(r.Context(), limit)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if list == nil {
		list = []model.Job{}
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	type_ := strings.TrimSpace(r.URL.Query().Get("type"))
	if status == "" && type_ == "" {
		s.ok(w, list)
		return
	}
	filtered := make([]model.Job, 0, len(list))
	for _, job := range list {
		if status != "" && job.Status != status {
			continue
		}
		if type_ != "" && job.Type != type_ {
			continue
		}
		filtered = append(filtered, job)
	}
	s.ok(w, filtered)
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, err := s.Store.GetJob(r.Context(), id)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.ok(w, job)
}

func (s *Server) handleJobEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, errf(http.StatusInternalServerError, "sse.unavailable", "streaming is not supported"))
		return
	}
	job, err := s.Store.GetJob(r.Context(), id)
	if err != nil {
		s.writeError(w, err)
		return
	}
	lastID := int64(0)
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			lastID = v
		}
	}
	if raw := r.URL.Query().Get("last_id"); raw != "" && lastID == 0 {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			lastID = v
		}
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if job.Status != jobs.StatusQueued && job.Status != jobs.StatusRunning {
		writeSSE(w, lastID+1, "snapshot", job)
		writeSSE(w, lastID+2, "done", job)
		flusher.Flush()
		return
	}
	ctx := r.Context()
	ch, cancel, err := s.Jobs.Subscribe(ctx, id, lastID)
	if err != nil {
		writeSSE(w, lastID+1, "error", map[string]string{"message": err.Error()})
		flusher.Flush()
		return
	}
	defer cancel()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case event, ok := <-ch:
			if !ok {
				return
			}
			writeSSE(w, event.Seq, event.Type, event)
			flusher.Flush()
			if event.Type == "done" {
				return
			}
		}
	}
}

func writeSSE(w http.ResponseWriter, id int64, event string, data any) {
	payload, err := marshalJSON(data)
	if err != nil {
		payload = []byte(`{"message":"encode error"}`)
	}
	if id > 0 {
		w.Write([]byte("id: " + strconv.FormatInt(id, 10) + "\n"))
	}
	w.Write([]byte("event: " + event + "\n"))
	w.Write([]byte("data: " + string(payload) + "\n\n"))
}
