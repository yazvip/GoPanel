package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"gopanel/internal/audit"
	"gopanel/internal/terminal"
)

const (
	terminalIdleTimeout = 30 * time.Minute
	terminalMaxMessage  = 1 << 20
)

func (s *Server) terminalAvailable() bool {
	return s.Terminal != nil && s.Config != nil && s.Config.TerminalEnabled
}

func (s *Server) handleTerminalTicket(w http.ResponseWriter, r *http.Request) {
	if !s.terminalAvailable() {
		s.writeError(w, errf(http.StatusServiceUnavailable, "terminal.disabled", "the web terminal is disabled"))
		return
	}
	p := s.principal(r)
	if p == nil {
		s.writeError(w, errf(http.StatusUnauthorized, "auth.required", "sign in first"))
		return
	}
	ip := s.clientIP(r)
	ticket, err := s.Terminal.Issue(terminal.Grant{
		UserID:    p.User.ID,
		Username:  p.User.Username,
		SessionID: p.Session.ID,
		IP:        ip,
	})
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.auditEvent(r, "terminal.ticket", p.User.Username, "", audit.ResultSuccess)
	s.created(w, ticket)
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	if strings.EqualFold(parsed.Host, r.Host) {
		return true
	}
	return false
}

func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	if !s.terminalAvailable() {
		http.Error(w, "terminal disabled", http.StatusServiceUnavailable)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "cross-origin terminal connections are not allowed", http.StatusForbidden)
		return
	}
	token := r.URL.Query().Get("ticket")
	if token == "" {
		http.Error(w, "missing ticket", http.StatusUnauthorized)
		return
	}
	user, _, err := s.Auth.SessionFromRequest(r.Context(), r)
	if err != nil || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != RoleAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	grant, err := s.Terminal.Consume(token, user.ID, s.clientIP(r))
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, terminal.ErrExpired) {
			status = http.StatusGone
		}
		http.Error(w, "invalid terminal ticket", status)
		return
	}
	cols, rows := uint16(120), uint16(32)
	if raw := r.URL.Query().Get("cols"); raw != "" {
		if v, err := parseUint16(raw); err == nil && v >= 20 {
			cols = v
		}
	}
	if raw := r.URL.Query().Get("rows"); raw != "" {
		if v, err := parseUint16(raw); err == nil && v >= 5 {
			rows = v
		}
	}

	session, err := s.System.StartTerminal(r.Context(), cols, rows)
	if err != nil {
		s.logger.Error("start terminal", "error", err)
		http.Error(w, "could not start a shell", http.StatusInternalServerError)
		return
	}
	defer session.Close()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(terminalMaxMessage)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	s.auditEvent(r, "terminal.open", grant.Username, "ip="+grant.IP, audit.ResultSuccess)
	defer s.auditEvent(r, "terminal.close", grant.Username, "", audit.ResultSuccess)

	idleTimer := time.AfterFunc(terminalIdleTimeout, func() {
		_ = conn.Close(websocket.StatusPolicyViolation, "terminal idle timeout")
		cancel()
	})
	defer idleTimer.Stop()
	touch := func() { idleTimer.Reset(terminalIdleTimeout) }

	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		buf := make([]byte, 32<<10)
		for {
			n, err := session.Read(buf)
			if n > 0 {
				writeCtx, writeCancel := context.WithTimeout(ctx, 15*time.Second)
				werr := conn.Write(writeCtx, websocket.MessageBinary, buf[:n])
				writeCancel()
				if werr != nil {
					cancel()
					return
				}
			}
			if err != nil {
				cancel()
				return
			}
		}
	}()

readLoop:
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		touch()
		switch kind {
		case websocket.MessageBinary:
			if _, err := session.Write(data); err != nil {
				cancel()
				break readLoop
			}
		case websocket.MessageText:
			var msg struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
				Data string `json:"data"`
			}
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			switch msg.Type {
			case "resize":
				if msg.Cols >= 20 && msg.Cols <= 1000 && msg.Rows >= 5 && msg.Rows <= 1000 {
					_ = session.Resize(uint16(msg.Cols), uint16(msg.Rows))
				}
			case "input":
				if msg.Data != "" {
					_, _ = session.Write([]byte(msg.Data))
				}
			}
		}
	}
	cancel()
	<-outputDone
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func parseUint16(raw string) (uint16, error) {
	var value int
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid number")
		}
		value = value*10 + int(c-'0')
		if value > 65535 {
			return 0, errors.New("out of range")
		}
	}
	if value == 0 {
		return 0, errors.New("invalid number")
	}
	return uint16(value), nil
}
