package httpapi

import (
	"errors"
	"net/http"

	"gopanel/internal/audit"
	"gopanel/internal/update"
)

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if s.Update == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "update.unavailable", "the update service is not available"))
		return
	}
	force := r.URL.Query().Get("force") == "1" || r.URL.Query().Get("force") == "true"
	s.ok(w, s.Update.Check(r.Context(), force))
}

func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if s.Update == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "update.unavailable", "the update service is not available"))
		return
	}
	status := s.Update.Check(r.Context(), true)
	if !status.Supported {
		s.writeError(w, errf(http.StatusNotImplemented, "update.unsupported", "automatic updates are not available on this installation"))
		return
	}
	if !status.Available {
		s.writeError(w, errf(http.StatusConflict, "update.none", "no update is available"))
		return
	}
	unit, err := s.Update.Apply()
	if err != nil {
		switch {
		case errors.Is(err, update.ErrBusy):
			s.writeError(w, errf(http.StatusConflict, "update.busy", "an update is already in progress"))
		case errors.Is(err, update.ErrUnsupported):
			s.writeError(w, errf(http.StatusNotImplemented, "update.unsupported", "automatic updates are not available on this installation"))
		default:
			s.auditEvent(r, "update.apply", "gopanel", err.Error(), audit.ResultFailed)
			s.writeError(w, errf(http.StatusInternalServerError, "update.apply_failed", "%v", err))
		}
		return
	}
	s.auditEvent(r, "update.apply", "gopanel", "unit="+unit+" latest="+status.Latest, audit.ResultSuccess)
	s.accepted(w, map[string]any{
		"started": true,
		"unit":    unit,
		"latest":  status.Latest,
	})
}
