package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"gopanel/internal/model"
	"gopanel/internal/store"
)

// APITokenPrefix marks tokens minted by this panel so stray credentials are
// rejected before a database lookup.
const APITokenPrefix = "gpm_"

var (
	ErrInvalidToken    = errors.New("invalid api token")
	ErrTokenName       = errors.New("token name must be 1-60 characters")
	ErrTokenLimit      = errors.New("too many api tokens for this user")
	ErrTokenExpiration = errors.New("token expiration must be between 1 and 3650 days")
)

const maxTokensPerUser = 25

// NewAPIToken returns the raw token (shown once), its storage hash and a short
// prefix used to identify it in the UI.
func NewAPIToken() (raw, hash, prefix string, err error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", "", "", err
	}
	raw = APITokenPrefix + base64.RawURLEncoding.EncodeToString(buf[:])
	return raw, HashToken(raw), raw[:12], nil
}

// CreateAPIToken mints a token for a user. ttl of zero means the token does
// not expire.
func (s *Service) CreateAPIToken(ctx context.Context, userID int64, name string, ttl time.Duration) (string, *model.APIToken, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 60 {
		return "", nil, ErrTokenName
	}
	if ttl < 0 || ttl > 3650*24*time.Hour {
		return "", nil, ErrTokenExpiration
	}
	n, err := s.st.CountAPITokens(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	if n >= maxTokensPerUser {
		return "", nil, ErrTokenLimit
	}
	raw, hash, prefix, err := NewAPIToken()
	if err != nil {
		return "", nil, err
	}
	var expiresAt int64
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl).Unix()
	}
	tok, err := s.st.CreateAPIToken(ctx, userID, name, prefix, hash, expiresAt)
	if err != nil {
		return "", nil, err
	}
	return raw, tok, nil
}

// TokenFromRequest extracts a machine credential from the Authorization
// header, X-API-Key, or the token query parameter (kept for MCP clients that
// can only be configured with a URL).
func (s *Service) TokenFromRequest(r *http.Request) string {
	if h := strings.TrimSpace(r.Header.Get("Authorization")); h != "" {
		if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
			return strings.TrimSpace(h[7:])
		}
	}
	if h := strings.TrimSpace(r.Header.Get("X-API-Key")); h != "" {
		return h
	}
	return strings.TrimSpace(r.URL.Query().Get("token"))
}

// TokenUser resolves a raw token to its user and token record.
func (s *Service) TokenUser(ctx context.Context, raw string) (*model.User, *model.APIToken, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, APITokenPrefix) || len(raw) < len(APITokenPrefix)+20 {
		return nil, nil, ErrInvalidToken
	}
	tok, err := s.st.FindAPITokenByHash(ctx, HashToken(raw))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, ErrInvalidToken
		}
		return nil, nil, err
	}
	now := time.Now().Unix()
	if tok.ExpiresAt > 0 && tok.ExpiresAt <= now {
		return nil, nil, ErrInvalidToken
	}
	user, err := s.st.GetUser(ctx, tok.UserID)
	if err != nil {
		return nil, nil, ErrInvalidToken
	}
	if user.Disabled {
		return nil, nil, ErrInvalidToken
	}
	if now-tok.LastUsedAt > 60 {
		_ = s.st.TouchAPIToken(ctx, tok.ID, now)
		tok.LastUsedAt = now
	}
	return user, tok, nil
}
