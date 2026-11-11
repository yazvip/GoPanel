package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"gopanel/internal/audit"
	"gopanel/internal/auth"
	"gopanel/internal/model"
	"gopanel/internal/store"
	"gopanel/internal/validate"
)

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Locale   string `json:"locale"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Store.ListUsers(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	if users == nil {
		users = []model.User{}
	}
	s.ok(w, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := s.decode(w, r, &req, 64<<10); err != nil {
		s.writeError(w, err)
		return
	}
	username := strings.TrimSpace(req.Username)
	if err := validate.Username(username); err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "users.username_invalid", "%v", err))
		return
	}
	role := req.Role
	if role == "" {
		role = model.RoleViewer
	}
	if err := validate.Role(role); err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "users.role_invalid", "%v", err))
		return
	}
	locale := req.Locale
	if locale == "" {
		locale = "id"
	}
	if err := validate.Locale(locale); err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "users.locale_invalid", "%v", err))
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "auth.weak_password", "%v", err))
		return
	}
	if _, err := s.Store.GetUserByUsername(r.Context(), username); err == nil {
		s.writeError(w, errf(http.StatusConflict, "users.exists", "that username is already taken"))
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.writeError(w, err)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.writeError(w, err)
		return
	}
	user := &model.User{
		Username:     username,
		PasswordHash: hash,
		Role:         role,
		Locale:       locale,
	}
	if err := s.Store.CreateUser(r.Context(), user); err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "users.create", username, "role="+role, audit.ResultSuccess)
	s.created(w, user)
}

type updateUserRequest struct {
	Role     *string `json:"role"`
	Locale   *string `json:"locale"`
	Disabled *bool   `json:"disabled"`
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := s.pathID(r, "id")
	if err != nil {
		s.writeError(w, err)
		return
	}
	var req updateUserRequest
	if err := s.decode(w, r, &req, 64<<10); err != nil {
		s.writeError(w, err)
		return
	}
	target, err := s.Store.GetUser(r.Context(), id)
	if err != nil {
		s.writeError(w, err)
		return
	}
	p := s.principal(r)
	changed := []string{}
	if req.Role != nil && *req.Role != target.Role {
		if err := validate.Role(*req.Role); err != nil {
			s.writeError(w, errf(http.StatusBadRequest, "users.role_invalid", "%v", err))
			return
		}
		if target.Role == model.RoleAdmin && *req.Role != model.RoleAdmin {
			if err := s.ensureOtherAdmin(r, target.ID); err != nil {
				s.writeError(w, err)
				return
			}
		}
		target.Role = *req.Role
		changed = append(changed, "role")
	}
	if req.Locale != nil && *req.Locale != target.Locale {
		if err := validate.Locale(*req.Locale); err != nil {
			s.writeError(w, errf(http.StatusBadRequest, "users.locale_invalid", "%v", err))
			return
		}
		target.Locale = *req.Locale
		changed = append(changed, "locale")
	}
	if req.Disabled != nil && *req.Disabled != target.Disabled {
		if *req.Disabled {
			if target.ID == p.User.ID {
				s.writeError(w, errf(http.StatusBadRequest, "users.self_disable", "you cannot disable your own account"))
				return
			}
			if target.Role == model.RoleAdmin {
				if err := s.ensureOtherAdmin(r, target.ID); err != nil {
					s.writeError(w, err)
					return
				}
			}
		}
		target.Disabled = *req.Disabled
		changed = append(changed, "disabled")
	}
	if len(changed) == 0 {
		s.ok(w, target)
		return
	}
	if err := s.Store.UpdateUser(r.Context(), target); err != nil {
		s.writeError(w, err)
		return
	}
	if target.Disabled {
		if err := s.Store.DeleteUserSessions(r.Context(), target.ID, ""); err != nil {
			s.logger.Warn("revoke disabled user sessions", "user", target.Username, "error", err)
		}
	}
	s.auditEvent(r, "users.update", target.Username, strings.Join(changed, ","), audit.ResultSuccess)
	s.ok(w, target)
}

func (s *Server) ensureOtherAdmin(r *http.Request, excludeID int64) error {
	users, err := s.Store.ListUsers(r.Context())
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.ID != excludeID && u.Role == model.RoleAdmin && !u.Disabled {
			return nil
		}
	}
	return errf(http.StatusBadRequest, "users.last_admin", "at least one active administrator is required")
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := s.pathID(r, "id")
	if err != nil {
		s.writeError(w, err)
		return
	}
	p := s.principal(r)
	if id == p.User.ID {
		s.writeError(w, errf(http.StatusBadRequest, "users.self_delete", "you cannot delete your own account"))
		return
	}
	target, err := s.Store.GetUser(r.Context(), id)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if target.Role == model.RoleAdmin {
		if err := s.ensureOtherAdmin(r, target.ID); err != nil {
			s.writeError(w, err)
			return
		}
	}
	if err := s.Store.DeleteUser(r.Context(), id); err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "users.delete", target.Username, "", audit.ResultSuccess)
	s.noContent(w)
}

type resetPasswordRequest struct {
	Password           string `json:"password"`
	MustChangePassword bool   `json:"must_change_password"`
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := s.pathID(r, "id")
	if err != nil {
		s.writeError(w, err)
		return
	}
	var req resetPasswordRequest
	if err := s.decode(w, r, &req, 64<<10); err != nil {
		s.writeError(w, err)
		return
	}
	target, err := s.Store.GetUser(r.Context(), id)
	if err != nil {
		s.writeError(w, err)
		return
	}
	password := req.Password
	generated := false
	if password == "" {
		password = auth.GeneratePassword(18)
		generated = true
	}
	if err := auth.ValidatePassword(password); err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "auth.weak_password", "%v", err))
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.Store.UpdatePassword(r.Context(), target.ID, hash, req.MustChangePassword); err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.Store.DeleteUserSessions(r.Context(), target.ID, ""); err != nil {
		s.logger.Warn("revoke sessions after admin reset", "user", target.Username, "error", err)
	}
	s.auditEvent(r, "users.password_reset", target.Username, "", audit.ResultSuccess)
	out := map[string]any{"user": target, "generated": generated}
	if generated {
		out["password"] = password
	}
	s.ok(w, out)
}
