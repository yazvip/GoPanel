package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gopanel/internal/model"
)

// mcpSession carries the authenticated token through the MCP tool handler
// context. Every tool call re-enters the panel's own REST API with the same
// bearer token, so role checks, audit logging and validation are shared with
// the web UI.
type mcpSession struct {
	user  *model.User
	token string
}

type mcpCtxKey int

const mcpSessionKey mcpCtxKey = 1

var errMCPUnauthorized = errors.New("missing or invalid API token")

func mcpSessionFrom(ctx context.Context) (*mcpSession, error) {
	sess, _ := ctx.Value(mcpSessionKey).(*mcpSession)
	if sess == nil || sess.user == nil {
		return nil, errMCPUnauthorized
	}
	return sess, nil
}

func (s *Server) newMCPHandler() http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "gopanel",
		Title:   "GoPanel VPS",
		Version: s.Version,
	}, &mcp.ServerOptions{
		Instructions: "GoPanel VPS control panel. Tools operate on the server that hosts this panel and follow the " +
			"role of the API token owner. Ask the user for confirmation before destructive calls: deleting sites, " +
			"databases, files or backups, restoring a backup, changing firewall rules, restarting services, and " +
			"running shell commands.",
	})
	s.registerMCPTools(srv)

	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		Stateless: true,
		// JSON responses avoid long-lived SSE streams on small VPS instances.
		JSONResponse: true,
		// The panel is usually reached through a reverse proxy on localhost, so
		// the Host header legitimately differs from the socket address.
		DisableLocalhostProtection: true,
		MaxRequestBodyBytes:        8 << 20,
		Logger:                     s.logger,
	})

	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(u.Host, r.Host) {
				s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin request rejected"})
				return
			}
		}
		raw := s.Auth.TokenFromRequest(r)
		user, _, err := s.Auth.TokenUser(r.Context(), raw)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gopanel"`)
			s.writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "a valid API token is required"})
			return
		}
		if user.MustChangePassword {
			s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "the token owner must change their password before using the API"})
			return
		}
		ctx := context.WithValue(r.Context(), mcpSessionKey, &mcpSession{user: user, token: raw})
		stream.ServeHTTP(w, r.WithContext(ctx))
	})
	return s.rateLimit(protected)
}

// addPanelTool registers a typed MCP tool whose result is JSON-serializable.
func addPanelTool[In any](srv *mcp.Server, name, description string, fn func(context.Context, *mcpSession, In) (any, error)) {
	mcp.AddTool(srv, &mcp.Tool{Name: name, Description: description},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			sess, err := mcpSessionFrom(ctx)
			if err != nil {
				return nil, nil, err
			}
			out, err := fn(ctx, sess, in)
			if err != nil {
				return nil, nil, err
			}
			if out == nil {
				out = map[string]any{"ok": true}
			}
			return nil, out, nil
		})
}

// panelCall executes an API request through the panel's own mux using the
// caller's bearer token. It returns the decoded JSON body (or an empty object
// for 204 responses).
func (s *Server) panelCall(ctx context.Context, sess *mcpSession, method, apiPath string, body any) (any, error) {
	apiPath = strings.TrimSpace(apiPath)
	if !strings.HasPrefix(apiPath, "/") || strings.Contains(apiPath, "..") {
		return nil, fmt.Errorf("invalid API path %q", apiPath)
	}
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, APIPrefix+apiPath, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+sess.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.RemoteAddr = "127.0.0.1:1"

	rec := newCapture()
	s.mux.ServeHTTP(rec, req)

	if rec.status >= http.StatusBadRequest {
		return nil, fmt.Errorf("%s", rec.errorMessage())
	}
	if rec.body.Len() == 0 {
		return map[string]any{"ok": true}, nil
	}
	var out any
	if err := json.Unmarshal(rec.body.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("decode API response: %w", err)
	}
	return out, nil
}

type capture struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newCapture() *capture {
	return &capture{header: http.Header{}, status: http.StatusOK}
}

func (c *capture) Header() http.Header { return c.header }

func (c *capture) WriteHeader(status int) {
	if c.status == http.StatusOK {
		c.status = status
	}
}

func (c *capture) Write(b []byte) (int, error) { return c.body.Write(b) }

// errorMessage extracts the panel's {"error":{"code","message"}} envelope.
func (c *capture) errorMessage() string {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(c.body.Bytes(), &envelope); err == nil && envelope.Error.Message != "" {
		if envelope.Error.Code != "" {
			return fmt.Sprintf("%s: %s", envelope.Error.Code, envelope.Error.Message)
		}
		return envelope.Error.Message
	}
	msg := strings.TrimSpace(c.body.String())
	if msg == "" {
		msg = http.StatusText(c.status)
	}
	if len(msg) > 500 {
		msg = msg[:500] + "..."
	}
	return fmt.Sprintf("HTTP %d: %s", c.status, msg)
}

func pathSegment(value string) string {
	return url.PathEscape(value)
}
