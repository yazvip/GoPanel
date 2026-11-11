package httpapi

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopanel/internal/audit"
	"gopanel/internal/model"
	"gopanel/internal/validate"
)

var editableSettings = map[string]bool{
	"acme_email":   true,
	"acme_staging": true,
	"panel_name":   true,
	"locale":       true,
	"theme":        true,
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	values, err := s.Store.AllSettings(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	visible := make(map[string]string, len(values))
	for key, value := range values {
		if editableSettings[key] {
			visible[key] = value
		}
	}
	certFile, keyFile := s.tlsPaths()
	_, certErr := os.Stat(certFile)
	_, keyErr := os.Stat(keyFile)
	response := map[string]any{
		"settings":  visible,
		"tls":       map[string]any{"cert_file": certFile, "key_file": keyFile, "installed": certErr == nil && keyErr == nil},
		"version":   s.Version,
		"started":   s.Started.UTC().Format(time.RFC3339),
		"panel_url": s.panelURL(),
	}
	if s.Config != nil {
		response["listen"] = s.Config.Listen
		response["data_dir"] = s.Config.DataDir
		response["backup_dir"] = s.Config.BackupDir
		response["log_dir"] = s.Config.LogDir
		response["session_idle_hours"] = s.Config.SessionIdleHours
		response["session_absolute_hours"] = s.Config.SessionAbsoluteHours
	}
	s.ok(w, response)
}

func (s *Server) panelURL() string {
	if s.Config == nil {
		return ""
	}
	return s.Config.PanelURL
}

func (s *Server) tlsPaths() (certFile, keyFile string) {
	if s.Config != nil && s.Config.TLS.CertFile != "" && s.Config.TLS.KeyFile != "" {
		return s.Config.TLS.CertFile, s.Config.TLS.KeyFile
	}
	dir := ""
	if s.Config != nil {
		dir = s.Config.DataDir
	}
	if dir == "" {
		dir = "."
	}
	dir = filepath.Join(dir, "tls")
	return filepath.Join(dir, "panel.crt"), filepath.Join(dir, "panel.key")
}

type settingsPatch struct {
	Values map[string]string `json:"settings"`
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsPatch
	if err := s.decode(w, r, &req, 32<<10); err != nil {
		s.writeError(w, err)
		return
	}
	if len(req.Values) == 0 {
		s.writeError(w, errf(http.StatusBadRequest, "settings.empty", "no settings were provided"))
		return
	}
	for key, value := range req.Values {
		if !editableSettings[key] {
			s.writeError(w, errf(http.StatusBadRequest, "settings.unknown_key", "unknown setting %q", key))
			return
		}
		value = strings.TrimSpace(value)
		if err := validateSetting(key, value); err != nil {
			s.writeError(w, err)
			return
		}
	}
	for key, value := range req.Values {
		if err := s.Store.SetSetting(r.Context(), key, strings.TrimSpace(value)); err != nil {
			s.writeError(w, err)
			return
		}
	}
	s.auditEvent(r, "settings.update", strings.Join(sortedKeys(req.Values), ","), "", audit.ResultSuccess)
	s.handleGetSettings(w, r)
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func validateSetting(key, value string) error {
	switch key {
	case "acme_email":
		if value == "" {
			return nil
		}
		if err := validate.Email(value); err != nil {
			return errf(http.StatusBadRequest, "settings.email_invalid", "%v", err)
		}
	case "acme_staging":
		if value != "" && value != "true" && value != "false" {
			return errf(http.StatusBadRequest, "settings.value_invalid", "acme_staging must be true or false")
		}
	case "panel_name":
		if len(value) > 64 {
			return errf(http.StatusBadRequest, "settings.value_invalid", "panel_name is limited to 64 characters")
		}
	case "locale":
		if err := validate.Locale(value); err != nil {
			return errf(http.StatusBadRequest, "settings.value_invalid", "%v", err)
		}
	case "theme":
		if value != "system" && value != "light" && value != "dark" {
			return errf(http.StatusBadRequest, "settings.value_invalid", "theme must be system, light or dark")
		}
	}
	return nil
}

func (s *Server) handleUploadTLS(w http.ResponseWriter, r *http.Request) {
	if s.Config == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "settings.unavailable", "configuration is not available"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	reader, err := r.MultipartReader()
	if err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "request.invalid_multipart", "%v", err))
		return
	}
	var certPEM, keyPEM []byte
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.writeError(w, errf(http.StatusBadRequest, "request.invalid_multipart", "%v", err))
			return
		}
		switch part.FormName() {
		case "cert":
			certPEM, err = io.ReadAll(io.LimitReader(part, 1<<20))
		case "key":
			keyPEM, err = io.ReadAll(io.LimitReader(part, 1<<20))
		}
		part.Close()
		if err != nil {
			s.writeError(w, errf(http.StatusBadRequest, "request.invalid_multipart", "%v", err))
			return
		}
	}
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		s.writeError(w, errf(http.StatusBadRequest, "settings.tls_missing", "upload both the certificate and the private key"))
		return
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "settings.tls_invalid", "certificate and key do not match: %v", err))
		return
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "settings.tls_invalid", "%v", err))
		return
	}
	if time.Now().After(leaf.NotAfter) {
		s.writeError(w, errf(http.StatusBadRequest, "settings.tls_expired", "that certificate expired on %s", leaf.NotAfter.Format(time.RFC3339)))
		return
	}
	if block, _ := pem.Decode(certPEM); block == nil || block.Type != "CERTIFICATE" {
		s.writeError(w, errf(http.StatusBadRequest, "settings.tls_invalid", "the certificate must be PEM encoded"))
		return
	}
	certFile, keyFile := s.tlsPaths()
	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		s.writeError(w, err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		s.writeError(w, err)
		return
	}
	if err := writeAtomicFile(certFile, certPEM, 0o644); err != nil {
		s.writeError(w, err)
		return
	}
	if err := writeAtomicFile(keyFile, keyPEM, 0o600); err != nil {
		s.writeError(w, err)
		return
	}
	_ = s.Store.SetSetting(r.Context(), "tls_cert_file", certFile)
	_ = s.Store.SetSetting(r.Context(), "tls_key_file", keyFile)
	s.auditEvent(r, "settings.tls_upload", leaf.Subject.CommonName, "expires="+leaf.NotAfter.Format(time.RFC3339), audit.ResultSuccess)
	s.ok(w, map[string]any{
		"cert_file": certFile,
		"key_file":  keyFile,
		"subject":   leaf.Subject.CommonName,
		"expires":   leaf.NotAfter.Unix(),
		"restart":   true,
	})
}

func writeAtomicFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tls-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	page := s.queryInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	perPage := s.queryInt(r, "per_page", 50)
	if perPage < 1 || perPage > 200 {
		perPage = 50
	}
	events, total, err := s.Store.ListAudit(r.Context(), perPage, (page-1)*perPage)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if events == nil {
		events = []model.AuditLog{}
	}
	s.ok(w, map[string]any{"events": events, "total": total, "page": page, "per_page": perPage})
}
