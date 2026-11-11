package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"gopanel/internal/audit"
	"gopanel/internal/auth"
	"gopanel/internal/model"
	"gopanel/internal/store"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type totpRequest struct {
	PreauthToken string `json:"preauth_token"`
	Code         string `json:"code"`
	RecoveryCode string `json:"recovery_code"`
}

type authPayload struct {
	User      *model.User `json:"user"`
	CSRF      string      `json:"csrf_token"`
	TwoFactor bool        `json:"two_factor_required,omitempty"`
	Preauth   string      `json:"preauth_token,omitempty"`
}

var dummyHashOnce sync.Once
var dummyHash string

func (s *Server) verifyDummy(password string) {
	dummyHashOnce.Do(func() {
		dummyHash, _ = auth.HashPassword("gopanel-dummy-password-value")
	})
	_, _ = auth.VerifyPassword(dummyHash, password)
}

func (s *Server) loginKey(r *http.Request, username string) string {
	return s.clientIP(r) + "|" + strings.ToLower(strings.TrimSpace(username))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := s.decode(w, r, &req, 64<<10); err != nil {
		s.writeError(w, err)
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		s.writeError(w, errf(http.StatusBadRequest, "auth.invalid_credentials", "username and password are required"))
		return
	}
	limiter := s.Auth.Limiter()
	key := s.loginKey(r, username)
	if ok, retry := limiter.Allow(key); !ok {
		s.writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":       &apiError{Status: http.StatusTooManyRequests, Code: "auth.rate_limited", Message: "too many failed attempts, try again later"},
			"retry_after": int(retry.Seconds()) + 1,
		})
		return
	}
	user, err := s.Store.GetUserByUsername(r.Context(), username)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.writeError(w, err)
			return
		}
		s.verifyDummy(req.Password)
		limiter.Fail(key)
		s.auditEvent(r, "auth.login", username, "unknown user", audit.ResultFailed)
		s.writeError(w, errf(http.StatusUnauthorized, "auth.invalid_credentials", "invalid username or password"))
		return
	}
	ok, err := auth.VerifyPassword(user.PasswordHash, req.Password)
	if err != nil || !ok {
		limiter.Fail(key)
		s.auditEvent(r, "auth.login", username, "wrong password", audit.ResultFailed)
		s.writeError(w, errf(http.StatusUnauthorized, "auth.invalid_credentials", "invalid username or password"))
		return
	}
	if user.Disabled {
		limiter.Fail(key)
		s.auditEvent(r, "auth.login", username, "account disabled", audit.ResultDenied)
		s.writeError(w, errf(http.StatusUnauthorized, "auth.invalid_credentials", "invalid username or password"))
		return
	}
	if user.TOTPEnabled {
		token, err := s.Auth.CreatePreauth(user.ID)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.auditEvent(r, "auth.login", username, "password accepted, awaiting 2FA", audit.ResultSuccess)
		s.writeJSON(w, http.StatusOK, authPayload{TwoFactor: true, Preauth: token})
		return
	}
	s.startSession(w, r, user)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user *model.User) {
	limiter := s.Auth.Limiter()
	limiter.Reset(s.loginKey(r, user.Username))
	token, sess, err := s.Auth.CreateSession(r.Context(), user.ID, s.clientIP(r), r.UserAgent())
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.Auth.SetSessionCookie(w, token)
	s.Auth.SetCSRFCookie(w, sess.CSRF)
	now := time.Now().Unix()
	user.LastLoginAt = now
	if err := s.Store.SetLastLogin(r.Context(), user.ID, now); err != nil {
		s.logger.Warn("record last login", "user", user.Username, "error", err)
	}
	s.auditEvent(r, "auth.login", user.Username, "session created", audit.ResultSuccess)
	s.writeJSON(w, http.StatusOK, authPayload{User: user, CSRF: sess.CSRF})
}

func (s *Server) handleTOTP(w http.ResponseWriter, r *http.Request) {
	var req totpRequest
	if err := s.decode(w, r, &req, 64<<10); err != nil {
		s.writeError(w, err)
		return
	}
	if req.PreauthToken == "" {
		s.writeError(w, errf(http.StatusBadRequest, "auth.preauth_invalid", "preauth token is required"))
		return
	}
	if req.Code == "" && req.RecoveryCode == "" {
		s.writeError(w, errf(http.StatusBadRequest, "auth.code_required", "authenticator code is required"))
		return
	}
	limiter := s.Auth.Limiter()
	key := s.clientIP(r) + "|totp"
	if ok, retry := limiter.Allow(key); !ok {
		s.writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":       &apiError{Status: http.StatusTooManyRequests, Code: "auth.rate_limited", Message: "too many failed attempts, try again later"},
			"retry_after": int(retry.Seconds()) + 1,
		})
		return
	}
	userID, ok := s.Auth.ConsumePreauth(req.PreauthToken)
	if !ok {
		limiter.Fail(key)
		s.writeError(w, errf(http.StatusUnauthorized, "auth.preauth_invalid", "login expired, start again"))
		return
	}
	user, err := s.Store.GetUser(r.Context(), userID)
	if err != nil || user.Disabled || !user.TOTPEnabled {
		s.writeError(w, errf(http.StatusUnauthorized, "auth.invalid_credentials", "invalid account"))
		return
	}
	verified := false
	if req.RecoveryCode != "" {
		verified = s.consumeRecoveryCode(r, user, req.RecoveryCode)
	} else {
		secret, err := s.Auth.Decrypt(user.TOTPSecretEnc)
		if err == nil {
			step, valid := auth.ValidateTOTP(string(secret), req.Code, time.Now())
			if valid && step > user.TOTPLastStep {
				if err := s.Store.UpdateTOTP(r.Context(), user.ID, user.TOTPSecretEnc, true, step); err == nil {
					user.TOTPLastStep = step
					verified = true
				}
			}
		}
	}
	if !verified {
		limiter.Fail(key)
		s.auditEvent(r, "auth.totp", user.Username, "invalid second factor", audit.ResultFailed)
		s.writeError(w, errf(http.StatusUnauthorized, "auth.invalid_code", "invalid or already used code"))
		return
	}
	s.startSession(w, r, user)
}

func (s *Server) consumeRecoveryCode(r *http.Request, user *model.User, code string) bool {
	codes, err := s.Store.UnusedRecoveryCodes(r.Context(), user.ID)
	if err != nil {
		return false
	}
	hash := auth.HashRecoveryCode(code)
	for _, rc := range codes {
		if subtle.ConstantTimeCompare([]byte(rc.Hash), []byte(hash)) == 1 {
			if err := s.Store.UseRecoveryCode(r.Context(), rc.ID); err == nil {
				s.logger.Warn("recovery code used", "user", user.Username)
				return true
			}
			return false
		}
	}
	return false
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	p := s.principal(r)
	if p != nil && p.Session != nil {
		if err := s.Store.DeleteSession(r.Context(), p.Session.ID); err != nil {
			s.logger.Warn("delete session", "error", err)
		}
		s.auditEvent(r, "auth.logout", p.User.Username, "", audit.ResultSuccess)
	}
	s.Auth.ClearSessionCookie(w)
	s.Auth.ClearCSRFCookie(w)
	s.noContent(w)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p := s.principal(r)
	caps := map[string]any{
		"terminal":   p.User.Role == RoleAdmin && s.Terminal != nil,
		"backups":    s.Backups != nil,
		"databases":  s.Databases != nil,
		"acme":       s.ACME != nil && s.ACME.Configured(),
		"apps":       s.Apps != nil,
		"version":    s.Version,
		"panel_port": "",
		"read_only":  p.User.Role == RoleViewer,
	}
	if s.Config != nil {
		caps["panel_port"] = s.Config.Listen
	}
	s.ok(w, map[string]any{
		"user":         p.User,
		"csrf_token":   p.Session.CSRF,
		"capabilities": caps,
	})
}

type passwordChangeRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req passwordChangeRequest
	if err := s.decode(w, r, &req, 64<<10); err != nil {
		s.writeError(w, err)
		return
	}
	p := s.principal(r)
	ok, err := auth.VerifyPassword(p.User.PasswordHash, req.CurrentPassword)
	if err != nil || !ok {
		s.auditEvent(r, "auth.password", p.User.Username, "wrong current password", audit.ResultFailed)
		s.writeError(w, errf(http.StatusBadRequest, "auth.invalid_password", "current password is incorrect"))
		return
	}
	if err := auth.ValidatePassword(req.NewPassword); err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "auth.weak_password", "%v", err))
		return
	}
	if req.NewPassword == req.CurrentPassword {
		s.writeError(w, errf(http.StatusBadRequest, "auth.same_password", "the new password must be different"))
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.Store.UpdatePassword(r.Context(), p.User.ID, hash, false); err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.Store.DeleteUserSessions(r.Context(), p.User.ID, p.Session.ID); err != nil {
		s.logger.Warn("revoke sessions after password change", "error", err)
	}
	s.auditEvent(r, "auth.password", p.User.Username, "password changed", audit.ResultSuccess)
	s.noContent(w)
}

func (s *Server) handle2FASetup(w http.ResponseWriter, r *http.Request) {
	p := s.principal(r)
	if p.User.TOTPEnabled {
		s.writeError(w, errf(http.StatusConflict, "auth.totp_enabled", "two-factor authentication is already enabled"))
		return
	}
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		s.writeError(w, err)
		return
	}
	uri := auth.OTPAuthURL("GoPanel", p.User.Username, secret)
	qr, err := auth.QRDataURI(uri)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.ok(w, map[string]any{"secret": secret, "otpauth_url": uri, "qr": qr})
}

type totpEnableRequest struct {
	Secret string `json:"secret"`
	Code   string `json:"code"`
}

func (s *Server) handle2FAEnable(w http.ResponseWriter, r *http.Request) {
	var req totpEnableRequest
	if err := s.decode(w, r, &req, 32<<10); err != nil {
		s.writeError(w, err)
		return
	}
	p := s.principal(r)
	if p.User.TOTPEnabled {
		s.writeError(w, errf(http.StatusConflict, "auth.totp_enabled", "two-factor authentication is already enabled"))
		return
	}
	secret := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(req.Secret), " ", ""))
	if secret == "" {
		s.writeError(w, errf(http.StatusBadRequest, "auth.secret_required", "secret is required"))
		return
	}
	step, ok := auth.ValidateTOTP(secret, req.Code, time.Now())
	if !ok {
		s.writeError(w, errf(http.StatusBadRequest, "auth.invalid_code", "the authenticator code is not valid"))
		return
	}
	enc, err := s.Auth.Encrypt([]byte(secret))
	if err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.Store.UpdateTOTP(r.Context(), p.User.ID, enc, true, step); err != nil {
		s.writeError(w, err)
		return
	}
	codes, hashes, err := auth.GenerateRecoveryCodes(10)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.Store.ReplaceRecoveryCodes(r.Context(), p.User.ID, hashes); err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "auth.2fa", p.User.Username, "two-factor enabled", audit.ResultSuccess)
	s.ok(w, map[string]any{"enabled": true, "recovery_codes": codes})
}

type twoFADisableRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

func (s *Server) handle2FADisable(w http.ResponseWriter, r *http.Request) {
	var req twoFADisableRequest
	if err := s.decode(w, r, &req, 32<<10); err != nil {
		s.writeError(w, err)
		return
	}
	p := s.principal(r)
	if !p.User.TOTPEnabled {
		s.writeError(w, errf(http.StatusConflict, "auth.totp_disabled", "two-factor authentication is not enabled"))
		return
	}
	ok, err := auth.VerifyPassword(p.User.PasswordHash, req.Password)
	if err != nil || !ok {
		s.writeError(w, errf(http.StatusForbidden, "auth.invalid_password", "password is incorrect"))
		return
	}
	secret, err := s.Auth.Decrypt(p.User.TOTPSecretEnc)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if _, valid := auth.ValidateTOTP(string(secret), req.Code, time.Now()); !valid {
		s.writeError(w, errf(http.StatusBadRequest, "auth.invalid_code", "the authenticator code is not valid"))
		return
	}
	if err := s.Store.SetTOTPDisabled(r.Context(), p.User.ID); err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "auth.2fa", p.User.Username, "two-factor disabled", audit.ResultSuccess)
	s.noContent(w)
}

func (s *Server) handle2FARecovery(w http.ResponseWriter, r *http.Request) {
	var req twoFADisableRequest
	if err := s.decode(w, r, &req, 32<<10); err != nil {
		s.writeError(w, err)
		return
	}
	p := s.principal(r)
	if !p.User.TOTPEnabled {
		s.writeError(w, errf(http.StatusConflict, "auth.totp_disabled", "two-factor authentication is not enabled"))
		return
	}
	ok, err := auth.VerifyPassword(p.User.PasswordHash, req.Password)
	if err != nil || !ok {
		s.writeError(w, errf(http.StatusForbidden, "auth.invalid_password", "password is incorrect"))
		return
	}
	secret, err := s.Auth.Decrypt(p.User.TOTPSecretEnc)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if _, valid := auth.ValidateTOTP(string(secret), req.Code, time.Now()); !valid {
		s.writeError(w, errf(http.StatusBadRequest, "auth.invalid_code", "the authenticator code is not valid"))
		return
	}
	codes, hashes, err := auth.GenerateRecoveryCodes(10)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.Store.ReplaceRecoveryCodes(r.Context(), p.User.ID, hashes); err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "auth.2fa", p.User.Username, "recovery codes regenerated", audit.ResultSuccess)
	s.ok(w, map[string]any{"recovery_codes": codes})
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	p := s.principal(r)
	list, err := s.Store.ListSessions(r.Context(), p.User.ID)
	if err != nil {
		s.writeError(w, err)
		return
	}
	type sessionItem struct {
		model.Session
		Current bool `json:"current"`
	}
	out := make([]sessionItem, 0, len(list))
	for _, sess := range list {
		out = append(out, sessionItem{Session: sess, Current: sess.ID == p.Session.ID})
	}
	s.ok(w, out)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	p := s.principal(r)
	id := r.PathValue("id")
	if len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
		s.writeError(w, errf(http.StatusBadRequest, "request.invalid_id", "invalid session id"))
		return
	}
	sess, err := s.Store.GetSessionByID(r.Context(), id)
	if err != nil || sess.UserID != p.User.ID {
		s.writeError(w, errf(http.StatusNotFound, "not_found", "session not found"))
		return
	}
	if err := s.Store.DeleteSession(r.Context(), sess.ID); err != nil {
		s.writeError(w, err)
		return
	}
	if sess.ID == p.Session.ID {
		s.Auth.ClearSessionCookie(w)
		s.Auth.ClearCSRFCookie(w)
	}
	s.auditEvent(r, "auth.session_revoke", p.User.Username, sess.IP, audit.ResultSuccess)
	s.noContent(w)
}
