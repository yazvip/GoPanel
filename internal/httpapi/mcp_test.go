package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopanel/internal/auth"
	"gopanel/internal/db"
	"gopanel/internal/model"
	"gopanel/internal/store"
	"gopanel/internal/sys/sysfake"
)

type mcpTestEnv struct {
	server *httptest.Server
	store  *store.Store
	auth   *auth.Service
}

func newMCPTestEnv(t *testing.T) *mcpTestEnv {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	st := store.New(database)
	authService := auth.New(st, bytes.Repeat([]byte{7}, 32), false, time.Hour, 24*time.Hour)
	srv, err := New(Deps{
		Store:   st,
		Auth:    authService,
		System:  sysfake.New(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version: "test",
		Started: time.Now(),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)
	return &mcpTestEnv{server: httpServer, store: st, auth: authService}
}

func (e *mcpTestEnv) user(t *testing.T, username, role string) *model.User {
	t.Helper()
	user := &model.User{Username: username, PasswordHash: "x", Role: role, Locale: "id"}
	if err := e.store.CreateUser(t.Context(), user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func (e *mcpTestEnv) token(t *testing.T, user *model.User) string {
	t.Helper()
	raw, _, err := e.auth.CreateAPIToken(t.Context(), user.ID, "test", 0)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return raw
}

func (e *mcpTestEnv) post(t *testing.T, token string, payload any) (int, []byte, http.Header) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, e.server.URL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := e.server.Client().Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, out, resp.Header
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
		Tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError           bool            `json:"isError"`
		StructuredContent json.RawMessage `json:"structuredContent"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeRPC(t *testing.T, body []byte) rpcResponse {
	t.Helper()
	var resp rpcResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode rpc response %q: %v", string(body), err)
	}
	if resp.Error != nil {
		t.Fatalf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp
}

func TestMCPRequiresToken(t *testing.T) {
	env := newMCPTestEnv(t)
	status, _, header := env.post(t, "", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{},
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
	if header.Get("WWW-Authenticate") == "" {
		t.Fatal("missing WWW-Authenticate header")
	}
}

func TestMCPHandshakeAndTools(t *testing.T) {
	env := newMCPTestEnv(t)
	admin := env.user(t, "admin", model.RoleAdmin)
	token := env.token(t, admin)

	status, body, _ := env.post(t, token, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "1"},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("initialize status = %d: %s", status, body)
	}
	init := decodeRPC(t, body)
	if init.Result.ServerInfo.Name != "gopanel" {
		t.Fatalf("server name = %q", init.Result.ServerInfo.Name)
	}

	status, body, _ = env.post(t, token, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{},
	})
	if status != http.StatusOK {
		t.Fatalf("tools/list status = %d: %s", status, body)
	}
	list := decodeRPC(t, body)
	names := map[string]bool{}
	for _, tool := range list.Result.Tools {
		names[tool.Name] = true
		if len(tool.InputSchema) == 0 {
			t.Errorf("tool %q has no input schema", tool.Name)
		}
	}
	for _, want := range []string{"list_sites", "create_site", "run_command", "panel_api", "panel_routes", "write_file"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
	if len(names) < 50 {
		t.Errorf("only %d tools registered", len(names))
	}
}

func TestMCPToolCallListSites(t *testing.T) {
	env := newMCPTestEnv(t)
	admin := env.user(t, "admin", model.RoleAdmin)
	token := env.token(t, admin)

	status, body, _ := env.post(t, token, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "list_sites", "arguments": map[string]any{}},
	})
	if status != http.StatusOK {
		t.Fatalf("tools/call status = %d: %s", status, body)
	}
	resp := decodeRPC(t, body)
	if resp.Result.IsError {
		t.Fatalf("tool error: %s", string(body))
	}
	if strings.TrimSpace(string(resp.Result.StructuredContent)) != "[]" {
		t.Fatalf("structured content = %s, want []", resp.Result.StructuredContent)
	}
}

func TestMCPRunCommandRespectsRole(t *testing.T) {
	env := newMCPTestEnv(t)
	viewer := env.user(t, "viewer", model.RoleViewer)
	token := env.token(t, viewer)

	_, body, _ := env.post(t, token, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "run_command", "arguments": map[string]any{"command": "id"}},
	})
	resp := decodeRPC(t, body)
	if !resp.Result.IsError {
		t.Fatalf("viewer run_command should fail: %s", string(body))
	}
	if len(resp.Result.Content) == 0 || !strings.Contains(resp.Result.Content[0].Text, "administrator") {
		t.Fatalf("unexpected error: %s", body)
	}

	admin := env.user(t, "root", model.RoleAdmin)
	adminToken := env.token(t, admin)
	_, body, _ = env.post(t, adminToken, map[string]any{
		"jsonrpc": "2.0", "id": 5, "method": "tools/call",
		"params": map[string]any{"name": "run_command", "arguments": map[string]any{"command": "id"}},
	})
	resp = decodeRPC(t, body)
	if resp.Result.IsError {
		t.Fatalf("admin run_command failed: %s", string(body))
	}
	if !strings.Contains(string(resp.Result.StructuredContent), "exit_code") {
		t.Fatalf("unexpected output: %s (body %s)", resp.Result.StructuredContent, body)
	}
}

func TestTokenEndpointsAcceptBearer(t *testing.T) {
	env := newMCPTestEnv(t)
	admin := env.user(t, "admin", model.RoleAdmin)
	token := env.token(t, admin)

	body, _ := json.Marshal(map[string]any{"name": "ci", "expires_in_days": 0})
	req, _ := http.NewRequest(http.MethodPost, env.server.URL+"/api/v1/tokens", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No cookie and no CSRF token: a bearer credential must be enough.
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := env.server.Client().Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	var created struct {
		Token  string `json:"token"`
		Prefix string `json:"prefix"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(created.Token, auth.APITokenPrefix) {
		t.Fatalf("token = %q", created.Token)
	}

	req, _ = http.NewRequest(http.MethodGet, env.server.URL+"/api/v1/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+created.Token)
	resp, err = env.server.Client().Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	raw, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d: %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), `"name":"ci"`) {
		t.Fatalf("created token missing from list: %s", raw)
	}
}
