package httpapi

import (
	"context"
	"errors"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type updateSettingsInput struct {
	Settings map[string]string `json:"settings" jsonschema:"settings to change: acme_email, acme_staging (true/false), panel_name, locale (en/id) or theme"`
}

type userIDInput struct {
	ID int64 `json:"id" jsonschema:"user id from list_users"`
}

type createUserInput struct {
	Username string `json:"username" jsonschema:"login name"`
	Password string `json:"password" jsonschema:"initial password"`
	Role     string `json:"role,omitempty" jsonschema:"admin, operator or viewer (default viewer)"`
	Locale   string `json:"locale,omitempty" jsonschema:"en or id (default id)"`
}

type updateUserInput struct {
	ID       int64   `json:"id" jsonschema:"user id"`
	Role     *string `json:"role,omitempty" jsonschema:"admin, operator or viewer"`
	Locale   *string `json:"locale,omitempty" jsonschema:"en or id"`
	Disabled *bool   `json:"disabled,omitempty" jsonschema:"disable or re-enable the account"`
}

type resetPasswordInput struct {
	ID                 int64  `json:"id" jsonschema:"user id"`
	Password           string `json:"password" jsonschema:"new password"`
	MustChangePassword bool   `json:"must_change_password,omitempty" jsonschema:"force a password change at the next login"`
}

type auditInput struct {
	Page    int `json:"page,omitempty" jsonschema:"page number starting at 1"`
	PerPage int `json:"per_page,omitempty" jsonschema:"entries per page, 1-200 (default 50)"`
}

func (s *Server) registerAdminMCPTools(srv *mcp.Server) {
	addPanelTool(srv, "list_php_versions",
		"List installed and installable PHP versions.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", "/php/versions", nil)
		})

	addPanelTool(srv, "get_settings",
		"Get the editable panel settings, TLS certificate state and panel version.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", "/settings", nil)
		})

	addPanelTool(srv, "update_settings",
		"Change editable panel settings. Administrator tokens only.",
		func(ctx context.Context, sess *mcpSession, in updateSettingsInput) (any, error) {
			if len(in.Settings) == 0 {
				return nil, errors.New("settings must not be empty")
			}
			return s.panelCall(ctx, sess, "PATCH", "/settings", map[string]any{"settings": in.Settings})
		})

	addPanelTool(srv, "update_panel",
		"Check for and install a GoPanel update. Administrator tokens only; restarts the panel service.",
		func(ctx context.Context, sess *mcpSession, in updatePanelInput) (any, error) {
			path := "/update"
			if in.Apply {
				if in.Force {
					path += "?force=1"
				}
				return s.panelCall(ctx, sess, "POST", "/update/apply", nil)
			}
			return s.panelCall(ctx, sess, "GET", path, nil)
		})

	addPanelTool(srv, "list_users",
		"List panel users with role, locale, disabled flag and last login.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", "/users", nil)
		})

	addPanelTool(srv, "create_user",
		"Create a panel user. Administrator tokens only.",
		func(ctx context.Context, sess *mcpSession, in createUserInput) (any, error) {
			if sess.user.Role != RoleAdmin {
				return nil, errors.New("only an administrator token may manage users")
			}
			return s.panelCall(ctx, sess, "POST", "/users", map[string]any{
				"username": in.Username,
				"password": in.Password,
				"role":     in.Role,
				"locale":   in.Locale,
			})
		})

	addPanelTool(srv, "update_user",
		"Change a panel user's role, locale or disabled flag. Administrator tokens only.",
		func(ctx context.Context, sess *mcpSession, in updateUserInput) (any, error) {
			body := map[string]any{}
			if in.Role != nil {
				body["role"] = *in.Role
			}
			if in.Locale != nil {
				body["locale"] = *in.Locale
			}
			if in.Disabled != nil {
				body["disabled"] = *in.Disabled
			}
			if len(body) == 0 {
				return nil, errors.New("provide at least one field to update")
			}
			return s.panelCall(ctx, sess, "PATCH", "/users/"+strconv.FormatInt(in.ID, 10), body)
		})

	addPanelTool(srv, "delete_user",
		"Delete a panel user. Administrator tokens only.",
		func(ctx context.Context, sess *mcpSession, in userIDInput) (any, error) {
			return s.panelCall(ctx, sess, "DELETE", "/users/"+strconv.FormatInt(in.ID, 10), nil)
		})

	addPanelTool(srv, "reset_user_password",
		"Set a new password for a panel user. Administrator tokens only.",
		func(ctx context.Context, sess *mcpSession, in resetPasswordInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/users/"+strconv.FormatInt(in.ID, 10)+"/password", map[string]any{
				"password":             in.Password,
				"must_change_password": in.MustChangePassword,
			})
		})

	addPanelTool(srv, "list_audit",
		"Read the audit log, newest first. Administrator tokens only.",
		func(ctx context.Context, sess *mcpSession, in auditInput) (any, error) {
			q := map[string]string{}
			if in.Page > 0 {
				q["page"] = strconv.Itoa(in.Page)
			}
			if in.PerPage > 0 {
				q["per_page"] = strconv.Itoa(in.PerPage)
			}
			return s.panelCall(ctx, sess, "GET", withQuery("/audit", q), nil)
		})
}

type updatePanelInput struct {
	Apply bool `json:"apply,omitempty" jsonschema:"true installs the update, false only reports the available version"`
	Force bool `json:"force,omitempty" jsonschema:"apply even when already up to date"`
}
