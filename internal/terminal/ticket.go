package terminal

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

var (
	ErrInvalid = errors.New("invalid terminal ticket")
	ErrExpired = errors.New("terminal ticket expired")
	ErrUsed    = errors.New("terminal ticket already used")
	ErrBinding = errors.New("terminal ticket does not belong to this user")
)

const (
	// TicketTTL is deliberately short: the browser trades the ticket for a
	// WebSocket immediately after requesting it.
	TicketTTL = 30 * time.Second
	// MaxPending bounds memory if a client requests tickets in a loop.
	MaxPending = 512
)

type Grant struct {
	UserID    int64
	Username  string
	SessionID string
	IP        string
}

type Ticket struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	ExpiresIn int    `json:"expires_in"`
}

type entry struct {
	grant     Grant
	expiresAt time.Time
}

type Manager struct {
	mu      sync.Mutex
	entries map[string]entry
	now     func() time.Time
}

func New() *Manager {
	return &Manager{entries: make(map[string]entry), now: time.Now}
}

func (m *Manager) Issue(g Grant) (*Ticket, error) {
	if g.UserID <= 0 || g.Username == "" {
		return nil, ErrBinding
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	exp := m.now().Add(TicketTTL)

	m.mu.Lock()
	m.sweepLocked()
	if len(m.entries) >= MaxPending {
		m.mu.Unlock()
		return nil, errors.New("too many pending terminal tickets")
	}
	m.entries[digest(token)] = entry{grant: g, expiresAt: exp}
	m.mu.Unlock()

	return &Ticket{
		Token:     token,
		ExpiresAt: exp.Unix(),
		ExpiresIn: int(TicketTTL / time.Second),
	}, nil
}

// Consume validates and burns the ticket in one step. A ticket is bound to
// the user that requested it and to the requesting IP address.
func (m *Manager) Consume(token string, userID int64, ip string) (Grant, error) {
	if token == "" || len(token) > 128 {
		return Grant{}, ErrInvalid
	}
	key := digest(token)
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return Grant{}, ErrInvalid
	}
	if m.now().After(e.expiresAt) {
		delete(m.entries, key)
		return Grant{}, ErrExpired
	}
	if e.grant.UserID != userID || e.grant.IP != ip {
		return Grant{}, ErrBinding
	}
	delete(m.entries, key)
	return e.grant, nil
}

func (m *Manager) sweepLocked() {
	now := m.now()
	for k, e := range m.entries {
		if now.After(e.expiresAt) {
			delete(m.entries, k)
		}
	}
}

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
