package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"gopanel/internal/files"
	"gopanel/internal/store"
)

type apiError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *apiError) Error() string { return e.Message }

func errf(status int, code, format string, args ...any) *apiError {
	return &apiError{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if value == nil || status == http.StatusNoContent {
		return
	}
	if err := json.NewEncoder(w).Encode(value); err != nil {
		s.logger.Debug("write response", "error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, err error) {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		s.writeJSON(w, apiErr.Status, map[string]any{"error": apiErr})
		return
	}
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, store.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, files.ErrNotFound):
		status, code = http.StatusNotFound, "files.not_found"
	case errors.Is(err, files.ErrExists):
		status, code = http.StatusConflict, "files.exists"
	case errors.Is(err, files.ErrConflict):
		status, code = http.StatusConflict, "files.conflict"
	case errors.Is(err, files.ErrTooLarge):
		status, code = http.StatusRequestEntityTooLarge, "files.too_large"
	case errors.Is(err, files.ErrBinary):
		status, code = http.StatusUnprocessableEntity, "files.binary"
	case errors.Is(err, files.ErrForbidden), errors.Is(err, files.ErrInvalidRoot):
		status, code = http.StatusForbidden, codeFor(err)
	case errors.Is(err, files.ErrInvalidName), errors.Is(err, files.ErrIsDir), errors.Is(err, files.ErrNotDir):
		status, code = http.StatusBadRequest, codeFor(err)
	}
	if status >= 500 {
		s.logger.Error("request failed", "error", err)
	}
	apiErr = &apiError{Status: status, Code: code, Message: err.Error()}
	s.writeJSON(w, status, map[string]any{"error": apiErr})
}

func codeFor(err error) string {
	switch {
	case errors.Is(err, files.ErrInvalidRoot):
		return "files.root_invalid"
	case errors.Is(err, files.ErrForbidden):
		return "files.path_invalid"
	case errors.Is(err, files.ErrInvalidName):
		return "files.name_invalid"
	case errors.Is(err, files.ErrIsDir):
		return "files.is_dir"
	case errors.Is(err, files.ErrNotDir):
		return "files.not_dir"
	}
	return "invalid_request"
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request, dst any, max int64) error {
	if max <= 0 {
		max = 1 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return errf(http.StatusRequestEntityTooLarge, "request.too_large", "request body is too large")
		}
		if errors.Is(err, io.EOF) {
			return errf(http.StatusBadRequest, "request.invalid_json", "request body is required")
		}
		return errf(http.StatusBadRequest, "request.invalid_json", "invalid JSON body: %v", err)
	}
	return nil
}

func (s *Server) ok(w http.ResponseWriter, value any) {
	s.writeJSON(w, http.StatusOK, value)
}

func (s *Server) created(w http.ResponseWriter, value any) {
	s.writeJSON(w, http.StatusCreated, value)
}

func (s *Server) accepted(w http.ResponseWriter, value any) {
	s.writeJSON(w, http.StatusAccepted, value)
}

func (s *Server) noContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

func marshalJSON(value any) ([]byte, error) {
	return json.Marshal(value)
}
