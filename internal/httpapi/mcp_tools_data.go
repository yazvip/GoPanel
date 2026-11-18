package httpapi

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Server) registerDataMCPTools(srv *mcp.Server) {
	s.registerDatabaseMCPTools(srv)
	s.registerCronMCPTools(srv)
	s.registerBackupMCPTools(srv)
}

// ------------------------------------------------------------- databases ----

type createDatabaseInput struct {
	Name     string `json:"name" jsonschema:"database name"`
	Username string `json:"username,omitempty" jsonschema:"user to create; empty lets the panel choose"`
	Password string `json:"password,omitempty" jsonschema:"password; empty generates a strong one"`
}

type databaseNameInput struct {
	Name string `json:"name" jsonschema:"database name"`
}

type deleteDatabaseInput struct {
	Name    string `json:"name" jsonschema:"database name"`
	Confirm string `json:"confirm" jsonschema:"repeat the database name to confirm deletion"`
}

type createDatabaseUserInput struct {
	Username string `json:"username" jsonschema:"database user name"`
	Database string `json:"database" jsonschema:"database to grant access to"`
	Password string `json:"password,omitempty" jsonschema:"password; empty generates a strong one"`
}

type databaseUserInput struct {
	User string `json:"user" jsonschema:"database user name"`
}

type databasePasswordInput struct {
	Name     string `json:"name" jsonschema:"database name"`
	Password string `json:"password,omitempty" jsonschema:"new password; empty generates a strong one"`
}

type databaseBackupInput struct {
	Name string `json:"name" jsonschema:"database name"`
	Keep int    `json:"keep,omitempty" jsonschema:"how many recent backups to keep, 0 keeps all"`
}

func (s *Server) registerDatabaseMCPTools(srv *mcp.Server) {
	addPanelTool(srv, "list_databases",
		"List MySQL/MariaDB databases with size, table count, presence and their users.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", "/databases", nil)
		})

	addPanelTool(srv, "create_database",
		"Create a database and optionally a user with access to it. The password is returned once.",
		func(ctx context.Context, sess *mcpSession, in createDatabaseInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/databases", map[string]any{
				"name":     in.Name,
				"username": in.Username,
				"password": in.Password,
			})
		})

	addPanelTool(srv, "delete_database",
		"Drop a database. Requires user confirmation; pass the database name as confirm.",
		func(ctx context.Context, sess *mcpSession, in deleteDatabaseInput) (any, error) {
			return s.panelCall(ctx, sess, "DELETE", "/databases/"+pathSegment(in.Name),
				map[string]any{"confirm": in.Confirm})
		})

	addPanelTool(srv, "list_database_users",
		"List database users and their host grants.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", "/databases/users", nil)
		})

	addPanelTool(srv, "create_database_user",
		"Create a database user and grant it access to a database.",
		func(ctx context.Context, sess *mcpSession, in createDatabaseUserInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/databases/users", map[string]any{
				"username": in.Username,
				"database": in.Database,
				"password": in.Password,
			})
		})

	addPanelTool(srv, "delete_database_user",
		"Drop a database user.",
		func(ctx context.Context, sess *mcpSession, in databaseUserInput) (any, error) {
			return s.panelCall(ctx, sess, "DELETE", "/databases/users/"+pathSegment(in.User), nil)
		})

	addPanelTool(srv, "set_database_password",
		"Set or rotate a database user password. The new password is returned once.",
		func(ctx context.Context, sess *mcpSession, in databasePasswordInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/databases/"+pathSegment(in.Name)+"/password",
				map[string]any{"password": in.Password})
		})

	addPanelTool(srv, "database_backup",
		"Dump a database to the panel backup directory as a background job.",
		func(ctx context.Context, sess *mcpSession, in databaseBackupInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/databases/"+pathSegment(in.Name)+"/backup",
				map[string]any{"keep": in.Keep})
		})
}

// ------------------------------------------------------------------ cron ----

type cronListInput struct{}

type createCronInput struct {
	Name     string `json:"name" jsonschema:"cron job name"`
	Schedule string `json:"schedule" jsonschema:"five-field cron expression, for example 0 3 * * *"`
	Command  string `json:"command" jsonschema:"shell command to run"`
	Enabled  *bool  `json:"enabled,omitempty" jsonschema:"enable the job (default true)"`
}

type updateCronInput struct {
	ID       int64  `json:"id" jsonschema:"cron job id"`
	Name     string `json:"name,omitempty" jsonschema:"new name; empty keeps the current name"`
	Schedule string `json:"schedule,omitempty" jsonschema:"new cron expression; empty keeps the current schedule"`
	Command  string `json:"command,omitempty" jsonschema:"new command; empty keeps the current command"`
	Enabled  *bool  `json:"enabled,omitempty" jsonschema:"toggle the job"`
}

type cronIDInput struct {
	ID int64 `json:"id" jsonschema:"cron job id"`
}

type cronLogInput struct {
	ID    int64 `json:"id" jsonschema:"cron job id"`
	Lines int   `json:"lines,omitempty" jsonschema:"number of lines, 1-2000 (default 200)"`
}

func (s *Server) registerCronMCPTools(srv *mcp.Server) {
	addPanelTool(srv, "list_cron_jobs",
		"List cron jobs with schedule, command, enabled flag and last run status.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", "/cron", nil)
		})

	addPanelTool(srv, "create_cron_job",
		"Create a cron job that runs a shell command on the server as root.",
		func(ctx context.Context, sess *mcpSession, in createCronInput) (any, error) {
			body := map[string]any{
				"name":     in.Name,
				"schedule": in.Schedule,
				"command":  in.Command,
			}
			if in.Enabled != nil {
				body["enabled"] = *in.Enabled
			}
			return s.panelCall(ctx, sess, "POST", "/cron", body)
		})

	addPanelTool(srv, "update_cron_job",
		"Update a cron job. Omitted fields keep their current value.",
		func(ctx context.Context, sess *mcpSession, in updateCronInput) (any, error) {
			body := map[string]any{}
			if in.Name != "" {
				body["name"] = in.Name
			}
			if in.Schedule != "" {
				body["schedule"] = in.Schedule
			}
			if in.Command != "" {
				body["command"] = in.Command
			}
			if in.Enabled != nil {
				body["enabled"] = *in.Enabled
			}
			if len(body) == 0 {
				return nil, errors.New("provide at least one field to update")
			}
			return s.panelCall(ctx, sess, "PATCH", "/cron/"+strconv.FormatInt(in.ID, 10), body)
		})

	addPanelTool(srv, "delete_cron_job",
		"Delete a cron job.",
		func(ctx context.Context, sess *mcpSession, in cronIDInput) (any, error) {
			return s.panelCall(ctx, sess, "DELETE", "/cron/"+strconv.FormatInt(in.ID, 10), nil)
		})

	addPanelTool(srv, "run_cron_job",
		"Run a cron job immediately as a background job.",
		func(ctx context.Context, sess *mcpSession, in cronIDInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/cron/"+strconv.FormatInt(in.ID, 10)+"/run", nil)
		})

	addPanelTool(srv, "cron_job_log",
		"Read the output log of a cron job.",
		func(ctx context.Context, sess *mcpSession, in cronLogInput) (any, error) {
			lines := in.Lines
			if lines <= 0 {
				lines = 200
			}
			return s.panelCall(ctx, sess, "GET",
				withQuery("/cron/"+strconv.FormatInt(in.ID, 10)+"/log", map[string]string{"lines": strconv.Itoa(lines)}), nil)
		})
}

// ---------------------------------------------------------------- backups ----

type createBackupInput struct {
	Kind string `json:"kind" jsonschema:"site or database"`
	Ref  string `json:"ref" jsonschema:"site name or database name"`
	Keep int    `json:"keep,omitempty" jsonschema:"how many recent backups to keep, 0 keeps all"`
}

type backupIDInput struct {
	ID string `json:"id" jsonschema:"backup id from list_backups"`
}

type restoreBackupInput struct {
	ID      string `json:"id" jsonschema:"backup id from list_backups"`
	Confirm string `json:"confirm" jsonschema:"the backup ref (site or database name) to confirm the restore"`
}

func (s *Server) registerBackupMCPTools(srv *mcp.Server) {
	addPanelTool(srv, "list_backups",
		"List site and database backups with status, size and creation time.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", "/backups", nil)
		})

	addPanelTool(srv, "create_backup",
		"Back up a site or database as a background job.",
		func(ctx context.Context, sess *mcpSession, in createBackupInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/backups", map[string]any{
				"kind": in.Kind,
				"ref":  in.Ref,
				"keep": in.Keep,
			})
		})

	addPanelTool(srv, "delete_backup",
		"Delete a backup file. Requires user confirmation.",
		func(ctx context.Context, sess *mcpSession, in backupIDInput) (any, error) {
			if strings.TrimSpace(in.ID) == "" {
				return nil, errors.New("id is required")
			}
			return s.panelCall(ctx, sess, "DELETE", "/backups/"+pathSegment(in.ID), nil)
		})

	addPanelTool(srv, "restore_backup",
		"Restore a site or database from a backup. Overwrites current data; requires user confirmation.",
		func(ctx context.Context, sess *mcpSession, in restoreBackupInput) (any, error) {
			if strings.TrimSpace(in.ID) == "" {
				return nil, errors.New("id is required")
			}
			return s.panelCall(ctx, sess, "POST", "/backups/"+pathSegment(in.ID)+"/restore",
				map[string]any{"confirm": in.Confirm})
		})
}

// ------------------------------------------------------------------ files ----

type filePathInput struct {
	Root string `json:"root" jsonschema:"root name from list_file_roots, for example sites"`
	Path string `json:"path" jsonschema:"path relative to the root"`
}

type fileReadInput struct {
	Root string `json:"root" jsonschema:"root name from list_file_roots"`
	Path string `json:"path" jsonschema:"path relative to the root"`
}

type fileWriteInput struct {
	Root    string `json:"root" jsonschema:"root name from list_file_roots"`
	Path    string `json:"path" jsonschema:"path relative to the root"`
	Content string `json:"content" jsonschema:"full new file content"`
	Mtime   int64  `json:"mtime,omitempty" jsonschema:"mtime returned by read_file; nonzero rejects the write if the file changed"`
}

type fileRenameInput struct {
	Root string `json:"root" jsonschema:"root name from list_file_roots"`
	From string `json:"from" jsonschema:"current path relative to the root"`
	To   string `json:"to" jsonschema:"new path relative to the root"`
}

type fileChmodInput struct {
	Root string `json:"root" jsonschema:"root name from list_file_roots"`
	Path string `json:"path" jsonschema:"path relative to the root"`
	Mode string `json:"mode" jsonschema:"octal mode, for example 644 or 755"`
}

type fileSearchInput struct {
	Root  string `json:"root" jsonschema:"root name from list_file_roots"`
	Path  string `json:"path,omitempty" jsonschema:"subdirectory to search; empty searches the root"`
	Query string `json:"query" jsonschema:"name fragment to search for"`
}

func (s *Server) registerFileTools(srv *mcp.Server) {
	addPanelTool(srv, "list_file_roots",
		"List the file manager roots (sites, config, logs) usable by the file tools.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", "/files/roots", nil)
		})

	addPanelTool(srv, "list_files",
		"List a directory inside a file root.",
		func(ctx context.Context, sess *mcpSession, in filePathInput) (any, error) {
			return s.panelCall(ctx, sess, "GET",
				withQuery("/files", map[string]string{"root": in.Root, "path": in.Path}), nil)
		})

	addPanelTool(srv, "read_file",
		"Read a UTF-8 text file. Binary and oversized files are rejected.",
		func(ctx context.Context, sess *mcpSession, in fileReadInput) (any, error) {
			return s.panelCall(ctx, sess, "GET",
				withQuery("/files/content", map[string]string{"root": in.Root, "path": in.Path}), nil)
		})

	addPanelTool(srv, "write_file",
		"Overwrite a text file. Pass the mtime from read_file to avoid clobbering concurrent edits.",
		func(ctx context.Context, sess *mcpSession, in fileWriteInput) (any, error) {
			return s.panelCall(ctx, sess, "PUT", "/files/content", map[string]any{
				"root":    in.Root,
				"path":    in.Path,
				"content": in.Content,
				"mtime":   in.Mtime,
			})
		})

	addPanelTool(srv, "create_file",
		"Create an empty file.",
		func(ctx context.Context, sess *mcpSession, in filePathInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/files/create", map[string]any{"root": in.Root, "path": in.Path})
		})

	addPanelTool(srv, "make_directory",
		"Create a directory.",
		func(ctx context.Context, sess *mcpSession, in filePathInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/files/mkdir", map[string]any{"root": in.Root, "path": in.Path})
		})

	addPanelTool(srv, "rename_file",
		"Rename or move a file or directory inside the same root.",
		func(ctx context.Context, sess *mcpSession, in fileRenameInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/files/rename", map[string]any{
				"root": in.Root,
				"from": in.From,
				"to":   in.To,
			})
		})

	addPanelTool(srv, "delete_file",
		"Delete a file or directory recursively. Requires user confirmation.",
		func(ctx context.Context, sess *mcpSession, in filePathInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/files/delete", map[string]any{"root": in.Root, "path": in.Path})
		})

	addPanelTool(srv, "chmod_file",
		"Change permissions of a file or directory.",
		func(ctx context.Context, sess *mcpSession, in fileChmodInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/files/chmod", map[string]any{
				"root": in.Root,
				"path": in.Path,
				"mode": in.Mode,
			})
		})

	addPanelTool(srv, "search_files",
		"Search files by name fragment inside a root.",
		func(ctx context.Context, sess *mcpSession, in fileSearchInput) (any, error) {
			return s.panelCall(ctx, sess, "GET",
				withQuery("/files/search", map[string]string{"root": in.Root, "path": in.Path, "q": in.Query}), nil)
		})
}
