package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"gopanel/internal/audit"
	"gopanel/internal/backup"
	"gopanel/internal/databases"
	"gopanel/internal/sys"
)

func (s *Server) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	if s.Databases == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "databases.unavailable", "the database module is not enabled"))
		return
	}
	list, err := s.Databases.List(r.Context())
	if err != nil {
		s.writeError(w, errf(http.StatusBadGateway, "databases.unreachable", "%v", err))
		return
	}
	if list == nil {
		list = []databases.Item{}
	}
	s.ok(w, list)
}

func (s *Server) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	if s.Databases == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "databases.unavailable", "the database module is not enabled"))
		return
	}
	var in databases.CreateInput
	if err := s.decode(w, r, &in, 32<<10); err != nil {
		s.writeError(w, err)
		return
	}
	created, err := s.Databases.Create(r.Context(), in)
	if err != nil {
		s.auditEvent(r, "databases.create", in.Name, err.Error(), audit.ResultFailed)
		s.writeError(w, mapDatabaseError(err))
		return
	}
	s.auditEvent(r, "databases.create", created.Database.Name, created.Username, audit.ResultSuccess)
	s.created(w, created)
}

func mapDatabaseError(err error) error {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	switch {
	case errors.Is(err, databases.ErrNotFound):
		return errf(http.StatusNotFound, "databases.not_found", "database not found")
	case errors.Is(err, databases.ErrNameTaken):
		return errf(http.StatusConflict, "databases.exists", "that database already exists")
	case errors.Is(err, databases.ErrUserTaken):
		return errf(http.StatusConflict, "databases.user_exists", "that database user already exists")
	case errors.Is(err, databases.ErrProtected):
		return errf(http.StatusForbidden, "databases.protected", "that database user is protected")
	case errors.Is(err, databases.ErrNoCredential):
		return errf(http.StatusConflict, "databases.no_credential", "this database has no panel-managed user")
	}
	return errf(http.StatusBadRequest, "databases.invalid", "%v", err)
}

type deleteDatabaseRequest struct {
	Confirm string `json:"confirm"`
}

func (s *Server) handleDeleteDatabase(w http.ResponseWriter, r *http.Request) {
	if s.Databases == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "databases.unavailable", "the database module is not enabled"))
		return
	}
	name := r.PathValue("name")
	var req deleteDatabaseRequest
	if err := s.decode(w, r, &req, 16<<10); err != nil {
		s.writeError(w, err)
		return
	}
	if strings.TrimSpace(req.Confirm) != name {
		s.writeError(w, errf(http.StatusBadRequest, "databases.confirm_required", "type the database name to confirm deletion"))
		return
	}
	if err := s.Databases.Delete(r.Context(), name); err != nil {
		s.auditEvent(r, "databases.delete", name, err.Error(), audit.ResultFailed)
		s.writeError(w, mapDatabaseError(err))
		return
	}
	s.auditEvent(r, "databases.delete", name, "", audit.ResultSuccess)
	s.noContent(w)
}

func (s *Server) handleListDatabaseUsers(w http.ResponseWriter, r *http.Request) {
	if s.Databases == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "databases.unavailable", "the database module is not enabled"))
		return
	}
	users, err := s.Databases.Users(r.Context())
	if err != nil {
		s.writeError(w, errf(http.StatusBadGateway, "databases.unreachable", "%v", err))
		return
	}
	if users == nil {
		users = []sys.DBUser{}
	}
	s.ok(w, users)
}

func (s *Server) handleCreateDatabaseUser(w http.ResponseWriter, r *http.Request) {
	if s.Databases == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "databases.unavailable", "the database module is not enabled"))
		return
	}
	var in databases.UserInput
	if err := s.decode(w, r, &in, 32<<10); err != nil {
		s.writeError(w, err)
		return
	}
	created, err := s.Databases.CreateUser(r.Context(), in)
	if err != nil {
		s.writeError(w, mapDatabaseError(err))
		return
	}
	s.auditEvent(r, "databases.user_create", created.Username, created.Database, audit.ResultSuccess)
	s.created(w, created)
}

func (s *Server) handleDeleteDatabaseUser(w http.ResponseWriter, r *http.Request) {
	if s.Databases == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "databases.unavailable", "the database module is not enabled"))
		return
	}
	user := r.PathValue("user")
	if err := s.Databases.DeleteUser(r.Context(), user); err != nil {
		s.writeError(w, mapDatabaseError(err))
		return
	}
	s.auditEvent(r, "databases.user_delete", user, "", audit.ResultSuccess)
	s.noContent(w)
}

type databasePasswordRequest struct {
	Password string `json:"password"`
}

func (s *Server) handleDatabasePassword(w http.ResponseWriter, r *http.Request) {
	if s.Databases == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "databases.unavailable", "the database module is not enabled"))
		return
	}
	name := r.PathValue("name")
	var req databasePasswordRequest
	if err := s.decode(w, r, &req, 16<<10); err != nil {
		s.writeError(w, err)
		return
	}
	password, err := s.Databases.SetPassword(r.Context(), name, req.Password)
	if err != nil {
		s.writeError(w, mapDatabaseError(err))
		return
	}
	s.auditEvent(r, "databases.password", name, "", audit.ResultSuccess)
	s.ok(w, map[string]any{"database": name, "password": password})
}

type databaseBackupRequest struct {
	Keep int `json:"keep"`
}

func (s *Server) handleDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	if s.Backups == nil {
		s.writeError(w, errf(http.StatusServiceUnavailable, "backups.unavailable", "the backup module is not enabled"))
		return
	}
	name := r.PathValue("name")
	var req databaseBackupRequest
	if r.ContentLength > 0 {
		if err := s.decode(w, r, &req, 16<<10); err != nil {
			s.writeError(w, err)
			return
		}
	}
	payload := backup.JobPayload{Kind: backup.KindDatabase, Ref: name, Keep: req.Keep}
	job, err := s.Jobs.Enqueue(r.Context(), backup.JobType, payload, s.principal(r).User.ID)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "databases.backup", name, "job="+job.ID, audit.ResultSuccess)
	s.accepted(w, job)
}
