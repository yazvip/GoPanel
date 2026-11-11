package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"

	"gopanel/internal/model"
	"gopanel/internal/store"
)

const (
	SessionCookie = "gopanel_session"
	CSRFCookie    = "gopanel_csrf"
	preauthTTL    = 5 * time.Minute
)

type preauthEntry struct {
	userID  int64
	expires time.Time
}

type Service struct {
	st       *store.Store
	key      []byte
	secure   bool
	idle     time.Duration
	absolute time.Duration
	limiter  *Limiter
	mu       sync.Mutex
	preauth  map[string]preauthEntry
}

func New(st *store.Store, key []byte, secure bool, idle, absolute time.Duration) *Service {
	return &Service{
		st:       st,
		key:      key,
		secure:   secure,
		idle:     idle,
		absolute: absolute,
		limiter:  NewLimiter(5, 15*time.Minute),
		preauth:  map[string]preauthEntry{},
	}
}

func (a *Service) Store() *store.Store { return a.st }

func (a *Service) Encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(a.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func (a *Service) Decrypt(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(a.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, payload := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, payload, nil)
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (a *Service) CreateSession(ctx context.Context, userID int64, ip, ua string) (string, *model.Session, error) {
	token, err := RandomToken(32)
	if err != nil {
		return "", nil, err
	}
	csrf, err := RandomToken(24)
	if err != nil {
		return "", nil, err
	}
	ts := time.Now().Unix()
	sess := &model.Session{
		ID:         HashToken(token),
		UserID:     userID,
		CSRF:       csrf,
		IP:         ip,
		UserAgent:  ua,
		CreatedAt:  ts,
		LastSeenAt: ts,
		ExpiresAt:  ts + int64(a.idle.Seconds()),
	}
	if err := a.st.CreateSession(ctx, sess); err != nil {
		return "", nil, err
	}
	return token, sess, nil
}

func (a *Service) SessionFromRequest(ctx context.Context, r *http.Request) (*model.User, *model.Session, error) {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil || cookie.Value == "" {
		return nil, nil, store.ErrNotFound
	}
	sess, user, err := a.st.GetSession(ctx, HashToken(cookie.Value))
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().Unix()
	if sess.ExpiresAt <= now || sess.CreatedAt+int64(a.absolute.Seconds()) <= now {
		_ = a.st.DeleteSession(ctx, sess.ID)
		return nil, nil, store.ErrNotFound
	}
	if user.Disabled {
		return nil, nil, store.ErrNotFound
	}
	newExpiry := now + int64(a.idle.Seconds())
	if maxExpiry := sess.CreatedAt + int64(a.absolute.Seconds()); newExpiry > maxExpiry {
		newExpiry = maxExpiry
	}
	if newExpiry != sess.ExpiresAt || now-sess.LastSeenAt > 60 {
		_ = a.st.TouchSession(ctx, sess.ID, now, newExpiry)
		sess.LastSeenAt = now
		sess.ExpiresAt = newExpiry
	}
	return user, sess, nil
}

func (a *Service) SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(a.absolute.Seconds()),
	})
}

func (a *Service) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode})
}

func (a *Service) EnsureCSRFCookie(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(CSRFCookie); err == nil && len(c.Value) >= 20 {
		return c.Value
	}
	token, err := RandomToken(24)
	if err != nil {
		return ""
	}
	a.SetCSRFCookie(w, token)
	return token
}

func (a *Service) SetCSRFCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		Secure:   a.secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   7 * 24 * 3600,
	})
}

func (a *Service) ClearCSRFCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CSRFCookie, Value: "", Path: "/", MaxAge: -1, Secure: a.secure, SameSite: http.SameSiteStrictMode})
}

func (a *Service) CSRFFromCookie(r *http.Request) string {
	if c, err := r.Cookie(CSRFCookie); err == nil {
		return c.Value
	}
	return ""
}

func (a *Service) ValidateCSRF(r *http.Request, sessionToken string) bool {
	header := r.Header.Get("X-CSRF-Token")
	if header == "" {
		return false
	}
	if sessionToken != "" {
		return subtle.ConstantTimeCompare([]byte(header), []byte(sessionToken)) == 1
	}
	cookie := a.CSRFFromCookie(r)
	return cookie != "" && subtle.ConstantTimeCompare([]byte(header), []byte(cookie)) == 1
}

func (a *Service) CreatePreauth(userID int64) (string, error) {
	token, err := RandomToken(24)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	for k, v := range a.preauth {
		if time.Now().After(v.expires) {
			delete(a.preauth, k)
		}
	}
	a.preauth[token] = preauthEntry{userID: userID, expires: time.Now().Add(preauthTTL)}
	a.mu.Unlock()
	return token, nil
}

func (a *Service) ConsumePreauth(token string) (int64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.preauth[token]
	if !ok || time.Now().After(entry.expires) {
		delete(a.preauth, token)
		return 0, false
	}
	delete(a.preauth, token)
	return entry.userID, true
}

func (a *Service) Limiter() *Limiter { return a.limiter }

func (a *Service) EnsureFirstAdmin(ctx context.Context, dev bool, devPassword string) (username, password string, created bool, err error) {
	n, err := a.st.CountUsers(ctx)
	if err != nil {
		return "", "", false, err
	}
	if n > 0 {
		return "", "", false, nil
	}
	password = devPassword
	if password == "" {
		password = GeneratePassword(16)
	}
	if err := ValidatePassword(password); err != nil {
		return "", "", false, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return "", "", false, err
	}
	u := &model.User{Username: "admin", PasswordHash: hash, Role: model.RoleAdmin, Locale: "id", MustChangePassword: !dev}
	if err := a.st.CreateUser(ctx, u); err != nil {
		return "", "", false, err
	}
	return "admin", password, true, nil
}

func (a *Service) VerifyTOTPForUser(user *model.User, code string, now time.Time) (int64, bool, error) {
	if len(user.TOTPSecretEnc) == 0 {
		return 0, false, errors.New("totp not configured")
	}
	secret, err := a.Decrypt(user.TOTPSecretEnc)
	if err != nil {
		return 0, false, err
	}
	step, ok := ValidateTOTP(string(secret), code, now)
	if !ok || step <= user.TOTPLastStep {
		return 0, false, nil
	}
	return step, true, nil
}

func (a *Service) VerifyRecoveryCode(ctx context.Context, userID int64, code string) (bool, error) {
	hash := HashRecoveryCode(code)
	codes, err := a.st.UnusedRecoveryCodes(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, c := range codes {
		if subtle.ConstantTimeCompare([]byte(c.Hash), []byte(hash)) == 1 {
			if err := a.st.UseRecoveryCode(ctx, c.ID); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

func (a *Service) QRDataURI(content string) (string, error) {
	return QRDataURI(content)
}

func (a *Service) OTPAuthURL(issuer, account, secret string) string {
	return OTPAuthURL(issuer, account, secret)
}

func (a *Service) GenerateTOTPSecret() (string, error) { return GenerateTOTPSecret() }

func (a *Service) GenerateRecoveryCodes(n int) ([]string, []string, error) {
	return GenerateRecoveryCodes(n)
}

func (a *Service) Key() []byte { return a.key }
