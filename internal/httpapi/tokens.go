package httpapi

import (
	"net/http"
	"strings"
	"time"

	"gopanel/internal/audit"
	"gopanel/internal/auth"
	"gopanel/internal/model"
)

type createTokenRequest struct {
	Name          string `json:"name"`
	ExpiresInDays *int   `json:"expires_in_days"`
}

type createTokenResponse struct {
	Token     string `json:"token"`
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	p := s.principal(r)
	list, err := s.Store.ListAPITokens(r.Context(), p.User.ID)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if list == nil {
		list = []model.APIToken{}
	}
	s.ok(w, list)
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	p := s.principal(r)
	var req createTokenRequest
	if err := s.decode(w, r, &req, 16<<10); err != nil {
		s.writeError(w, err)
		return
	}
	days := 90
	if req.ExpiresInDays != nil {
		days = *req.ExpiresInDays
	}
	var ttl time.Duration
	switch {
	case days == 0:
		ttl = 0
	case days < 1 || days > 3650:
		s.writeError(w, errf(http.StatusBadRequest, "tokens.expiration_invalid", "expires_in_days must be 0 (never) or between 1 and 3650"))
		return
	default:
		ttl = time.Duration(days) * 24 * time.Hour
	}
	raw, tok, err := s.Auth.CreateAPIToken(r.Context(), p.User.ID, req.Name, ttl)
	if err != nil {
		switch err {
		case auth.ErrTokenName, auth.ErrTokenLimit, auth.ErrTokenExpiration:
			s.writeError(w, errf(http.StatusBadRequest, "tokens.invalid", "%v", err))
		default:
			s.writeError(w, err)
		}
		return
	}
	s.auditEvent(r, "tokens.create", tok.Name, tok.Prefix, audit.ResultSuccess)
	s.created(w, createTokenResponse{
		Token:     raw,
		ID:        tok.ID,
		Name:      tok.Name,
		Prefix:    tok.Prefix,
		CreatedAt: tok.CreatedAt,
		ExpiresAt: tok.ExpiresAt,
	})
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	p := s.principal(r)
	id, err := s.pathID(r, "id")
	if err != nil {
		s.writeError(w, err)
		return
	}
	deleted, err := s.Store.DeleteAPIToken(r.Context(), id, p.User.ID)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if !deleted {
		s.writeError(w, errf(http.StatusNotFound, "tokens.not_found", "token not found"))
		return
	}
	s.auditEvent(r, "tokens.delete", strings.TrimSpace(r.PathValue("id")), "", audit.ResultSuccess)
	s.noContent(w)
}
