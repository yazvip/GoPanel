package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gopanel/internal/model"
	"gopanel/internal/sites"
	"gopanel/internal/sys"
)

func (s *Server) registerMCPTools(srv *mcp.Server) {
	s.registerSystemMCPTools(srv)
	s.registerSiteMCPTools(srv)
	s.registerDataMCPTools(srv)
	s.registerFileMCPTools(srv)
	s.registerAdminMCPTools(srv)
	s.registerMetaMCPTools(srv)
}

func withQuery(path string, params map[string]string) string {
	values := url.Values{}
	for key, value := range params {
		if strings.TrimSpace(value) != "" {
			values.Set(key, value)
		}
	}
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}

type emptyInput struct{}

// ---------------------------------------------------------------- system ----

type systemMetricsInput struct {
	Range string `json:"range,omitempty" jsonschema:"history range: 1h (default), 6h, 24h or 7d"`
}

type processListInput struct {
	Sort  string `json:"sort,omitempty" jsonschema:"sort by cpu (default) or mem"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum entries, 1-300 (default 50)"`
}

type processKillInput struct {
	PID int `json:"pid" jsonschema:"process id to terminate"`
}

type serviceActionInput struct {
	Name   string `json:"name" jsonschema:"systemd unit name, for example nginx or mysql"`
	Action string `json:"action" jsonschema:"start, stop, restart or reload"`
}

type runCommandInput struct {
	Command        string `json:"command" jsonschema:"shell command executed as root on the server"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"timeout in seconds, 1-600 (default 60)"`
	Dir            string `json:"dir,omitempty" jsonschema:"working directory (default /root)"`
}

func (s *Server) registerSystemMCPTools(srv *mcp.Server) {
	addPanelTool(srv, "system_summary",
		"Get the dashboard overview: CPU, memory, disk, network, load, service states, recent jobs and backups.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", "/dashboard/summary", nil)
		})

	addPanelTool(srv, "system_metrics",
		"Get resource usage history samples.",
		func(ctx context.Context, sess *mcpSession, in systemMetricsInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", withQuery("/metrics", map[string]string{"range": in.Range}), nil)
		})

	addPanelTool(srv, "system_host",
		"Get host information: hostname, OS, kernel, uptime, CPU count, memory, disks, local and public IP.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", "/host", nil)
		})

	addPanelTool(srv, "list_services",
		"List systemd services with their active state.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", "/services", nil)
		})

	addPanelTool(srv, "service_action",
		"Start, stop, restart or reload a systemd service. This can interrupt running services.",
		func(ctx context.Context, sess *mcpSession, in serviceActionInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/services/"+pathSegment(in.Name)+"/"+pathSegment(in.Action), nil)
		})

	addPanelTool(srv, "list_processes",
		"List running processes sorted by CPU or memory.",
		func(ctx context.Context, sess *mcpSession, in processListInput) (any, error) {
			q := map[string]string{"sort": in.Sort}
			if in.Limit > 0 {
				q["limit"] = strconv.Itoa(in.Limit)
			}
			return s.panelCall(ctx, sess, "GET", withQuery("/processes", q), nil)
		})

	addPanelTool(srv, "kill_process",
		"Terminate a process by PID.",
		func(ctx context.Context, sess *mcpSession, in processKillInput) (any, error) {
			if in.PID <= 0 {
				return nil, errors.New("pid must be greater than zero")
			}
			return s.panelCall(ctx, sess, "POST", "/processes/"+strconv.Itoa(in.PID)+"/kill", nil)
		})

	addPanelTool(srv, "run_command",
		"Run an arbitrary shell command as root on the VPS and return stdout, stderr and the exit code. "+
			"Administrator tokens only. Requires explicit user confirmation.",
		func(ctx context.Context, sess *mcpSession, in runCommandInput) (any, error) {
			if sess.user.Role != RoleAdmin {
				return nil, errors.New("only an administrator token may run shell commands")
			}
			command := strings.TrimSpace(in.Command)
			if command == "" {
				return nil, errors.New("command is required")
			}
			timeout := 60 * time.Second
			if in.TimeoutSeconds > 0 {
				seconds := in.TimeoutSeconds
				if seconds > 600 {
					seconds = 600
				}
				timeout = time.Duration(seconds) * time.Second
			}
			dir := strings.TrimSpace(in.Dir)
			if dir == "" {
				dir = "/root"
			}
			result, err := s.System.Run(ctx, "/bin/sh", []string{"-lc", command}, sys.ExecOpts{
				Dir:       dir,
				Timeout:   timeout,
				MaxOutput: 64 << 10,
			})
			out := map[string]any{
				"stdout":    result.Stdout,
				"stderr":    result.Stderr,
				"exit_code": result.ExitCode,
			}
			if err != nil {
				out["error"] = err.Error()
			}
			return out, nil
		})
}

// ----------------------------------------------------------------- sites ----

type siteIDInput struct {
	ID int64 `json:"id" jsonschema:"numeric site id (from list_sites)"`
}

type siteEnabledInput struct {
	ID      int64 `json:"id" jsonschema:"numeric site id"`
	Enabled bool  `json:"enabled" jsonschema:"true to enable, false to disable"`
}

type siteDomainInput struct {
	ID     int64  `json:"id" jsonschema:"numeric site id"`
	Domain string `json:"domain" jsonschema:"domain name to add, for example example.com"`
}

type siteDomainIDInput struct {
	ID       int64 `json:"id" jsonschema:"numeric site id"`
	DomainID int64 `json:"domain_id" jsonschema:"numeric domain id from get_site"`
}

type siteLogsInput struct {
	ID    int64  `json:"id" jsonschema:"numeric site id"`
	Type  string `json:"type,omitempty" jsonschema:"access or error (default error)"`
	Lines int    `json:"lines,omitempty" jsonschema:"number of lines, 1-2000 (default 200)"`
}

type siteSSLInput struct {
	ID      int64  `json:"id" jsonschema:"numeric site id"`
	Action  string `json:"action" jsonschema:"enable, renew or disable"`
	Email   string `json:"email,omitempty" jsonschema:"ACME account email; leave empty to use the panel default"`
	Staging bool   `json:"staging,omitempty" jsonschema:"use the Let's Encrypt staging environment"`
}

type createSiteInput struct {
	Name          string   `json:"name" jsonschema:"unique site name: lowercase letters, digits, dot, dash or underscore"`
	Type          string   `json:"type" jsonschema:"static, php or proxy"`
	Domains       []string `json:"domains" jsonschema:"domain names served by this site (at least one)"`
	RootPath      string   `json:"root_path,omitempty" jsonschema:"document root; defaults to the panel site root"`
	PHPVersion    string   `json:"php_version,omitempty" jsonschema:"PHP version for php sites, for example 8.3"`
	Upstream      string   `json:"upstream,omitempty" jsonschema:"proxy target such as http://127.0.0.1:3000 (proxy sites only)"`
	IndexFile     string   `json:"index_file,omitempty" jsonschema:"index directives, default index.html or index.php"`
	ClientMaxBody int64    `json:"client_max_body,omitempty" jsonschema:"upload limit in bytes; 0 uses the panel default"`
	AccessLog     *bool    `json:"access_log,omitempty" jsonschema:"enable access logging (default true)"`
}

type updateSiteInput struct {
	ID            int64    `json:"id" jsonschema:"numeric site id"`
	Name          string   `json:"name,omitempty" jsonschema:"new site name; empty keeps the current name"`
	Type          string   `json:"type,omitempty" jsonschema:"static, php or proxy; empty keeps the current type"`
	RootPath      string   `json:"root_path,omitempty" jsonschema:"new document root; empty keeps the current root"`
	PHPVersion    string   `json:"php_version,omitempty" jsonschema:"PHP version; empty keeps the current version"`
	Upstream      string   `json:"upstream,omitempty" jsonschema:"proxy target; empty keeps the current target"`
	IndexFile     string   `json:"index_file,omitempty" jsonschema:"index directives; empty keeps the current value"`
	ClientMaxBody int64    `json:"client_max_body,omitempty" jsonschema:"upload limit in bytes; 0 keeps the current value"`
	AccessLog     *bool    `json:"access_log,omitempty" jsonschema:"toggle access logging"`
	Domains       []string `json:"domains,omitempty" jsonschema:"full replacement domain list; omit to keep current domains"`
}

type deleteSiteInput struct {
	ID          int64 `json:"id" jsonschema:"numeric site id"`
	DeleteFiles bool  `json:"delete_files,omitempty" jsonschema:"also delete the site files from disk"`
}

func (s *Server) registerSiteMCPTools(srv *mcp.Server) {
	addPanelTool(srv, "list_sites",
		"List all sites with their domains, PHP version, SSL state and enabled flag.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", "/sites", nil)
		})

	addPanelTool(srv, "get_site",
		"Get one site by id, including its domains.",
		func(ctx context.Context, sess *mcpSession, in siteIDInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", "/sites/"+strconv.FormatInt(in.ID, 10), nil)
		})

	addPanelTool(srv, "create_site",
		"Create a site and its nginx configuration, then reload nginx. Destructive to nothing, but changes server config.",
		func(ctx context.Context, sess *mcpSession, in createSiteInput) (any, error) {
			accessLog := true
			if in.AccessLog != nil {
				accessLog = *in.AccessLog
			}
			return s.panelCall(ctx, sess, "POST", "/sites", map[string]any{
				"name":            in.Name,
				"type":            in.Type,
				"domains":         in.Domains,
				"root_path":       in.RootPath,
				"php_version":     in.PHPVersion,
				"upstream":        in.Upstream,
				"index_file":      in.IndexFile,
				"client_max_body": in.ClientMaxBody,
				"access_log":      accessLog,
			})
		})

	addPanelTool(srv, "update_site",
		"Update a site. Fields left empty keep their current value. Passing domains replaces the whole domain list.",
		func(ctx context.Context, sess *mcpSession, in updateSiteInput) (any, error) {
			current, err := s.siteByID(ctx, sess, in.ID)
			if err != nil {
				return nil, err
			}
			body := sites.Input{
				Name:          current.Name,
				Type:          current.Type,
				RootPath:      current.RootPath,
				PHPVersion:    current.PHPVersion,
				Upstream:      current.Upstream,
				IndexFile:     current.IndexFile,
				ClientMaxBody: current.ClientMaxBody,
				AccessLog:     current.AccessLog,
			}
			if in.Name != "" {
				body.Name = in.Name
			}
			if in.Type != "" {
				body.Type = in.Type
			}
			if in.RootPath != "" {
				body.RootPath = in.RootPath
			}
			if in.PHPVersion != "" {
				body.PHPVersion = in.PHPVersion
			}
			if in.Upstream != "" {
				body.Upstream = in.Upstream
			}
			if in.IndexFile != "" {
				body.IndexFile = in.IndexFile
			}
			if in.ClientMaxBody > 0 {
				body.ClientMaxBody = in.ClientMaxBody
			}
			if in.AccessLog != nil {
				body.AccessLog = *in.AccessLog
			}
			if len(in.Domains) > 0 {
				body.Domains = in.Domains
			}
			return s.panelCall(ctx, sess, "PATCH", "/sites/"+strconv.FormatInt(in.ID, 10), body)
		})

	addPanelTool(srv, "delete_site",
		"Delete a site and its nginx configuration. Optionally deletes the site files. Requires user confirmation.",
		func(ctx context.Context, sess *mcpSession, in deleteSiteInput) (any, error) {
			current, err := s.siteByID(ctx, sess, in.ID)
			if err != nil {
				return nil, err
			}
			return s.panelCall(ctx, sess, "DELETE", "/sites/"+strconv.FormatInt(in.ID, 10), map[string]any{
				"confirm":      current.Name,
				"delete_files": in.DeleteFiles,
			})
		})

	addPanelTool(srv, "set_site_enabled",
		"Enable or disable a site (toggles its nginx configuration and reloads nginx).",
		func(ctx context.Context, sess *mcpSession, in siteEnabledInput) (any, error) {
			action := "disable"
			if in.Enabled {
				action = "enable"
			}
			return s.panelCall(ctx, sess, "POST", "/sites/"+strconv.FormatInt(in.ID, 10)+"/"+action, nil)
		})

	addPanelTool(srv, "add_site_domain",
		"Attach an extra domain to a site.",
		func(ctx context.Context, sess *mcpSession, in siteDomainInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/sites/"+strconv.FormatInt(in.ID, 10)+"/domains",
				map[string]any{"domain": in.Domain})
		})

	addPanelTool(srv, "remove_site_domain",
		"Detach a domain from a site. The primary domain cannot be removed.",
		func(ctx context.Context, sess *mcpSession, in siteDomainIDInput) (any, error) {
			return s.panelCall(ctx, sess, "DELETE",
				"/sites/"+strconv.FormatInt(in.ID, 10)+"/domains/"+strconv.FormatInt(in.DomainID, 10), nil)
		})

	addPanelTool(srv, "set_primary_domain",
		"Make one of a site's domains the primary domain.",
		func(ctx context.Context, sess *mcpSession, in siteDomainIDInput) (any, error) {
			return s.panelCall(ctx, sess, "POST",
				"/sites/"+strconv.FormatInt(in.ID, 10)+"/domains/"+strconv.FormatInt(in.DomainID, 10)+"/primary", nil)
		})

	addPanelTool(srv, "site_logs",
		"Read the access or error log of a site.",
		func(ctx context.Context, sess *mcpSession, in siteLogsInput) (any, error) {
			lines := in.Lines
			if lines <= 0 {
				lines = 200
			}
			q := map[string]string{"type": in.Type, "lines": strconv.Itoa(lines)}
			return s.panelCall(ctx, sess, "GET",
				withQuery("/sites/"+strconv.FormatInt(in.ID, 10)+"/logs", q), nil)
		})

	addPanelTool(srv, "site_ssl",
		"Enable, renew or disable a Let's Encrypt certificate for a site. Issuing a certificate takes a while and runs in a job.",
		func(ctx context.Context, sess *mcpSession, in siteSSLInput) (any, error) {
			base := "/sites/" + strconv.FormatInt(in.ID, 10) + "/ssl"
			switch strings.ToLower(strings.TrimSpace(in.Action)) {
			case "disable":
				return s.panelCall(ctx, sess, "DELETE", base, nil)
			case "renew":
				return s.panelCall(ctx, sess, "POST", base+"/renew", map[string]any{"email": in.Email, "staging": in.Staging})
			case "enable":
				return s.panelCall(ctx, sess, "POST", base, map[string]any{"email": in.Email, "staging": in.Staging})
			default:
				return nil, errors.New("action must be enable, renew or disable")
			}
		})
}

// siteByID fetches a site through the panel API and decodes it into the model.
func (s *Server) siteByID(ctx context.Context, sess *mcpSession, id int64) (*model.Site, error) {
	if id <= 0 {
		return nil, errors.New("id must be greater than zero")
	}
	raw, err := s.panelCall(ctx, sess, "GET", "/sites/"+strconv.FormatInt(id, 10), nil)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var site model.Site
	if err := json.Unmarshal(payload, &site); err != nil {
		return nil, fmt.Errorf("decode site: %w", err)
	}
	return &site, nil
}

// -------------------------------------------------------------- firewall ----

type firewallRuleInput struct {
	Port    string `json:"port" jsonschema:"port or range, for example 22, 80, 443 or 8000:8100"`
	Proto   string `json:"proto,omitempty" jsonschema:"tcp (default), udp or any"`
	Action  string `json:"action,omitempty" jsonschema:"allow (default), deny or limit"`
	From    string `json:"from,omitempty" jsonschema:"source address or any (default any)"`
	Comment string `json:"comment,omitempty" jsonschema:"optional rule comment"`
}

type firewallToggleInput struct {
	Enabled bool `json:"enabled" jsonschema:"true to enable the firewall, false to disable it"`
}

type firewallRuleNumberInput struct {
	Number int `json:"number" jsonschema:"rule number from firewall_status"`
}

func (s *Server) registerFirewallMCPTools(srv *mcp.Server) {
	addPanelTool(srv, "firewall_status",
		"Get the ufw status, default policies and rule list.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", "/firewall", nil)
		})

	addPanelTool(srv, "firewall_set",
		"Enable or disable ufw. Enabling ufw can lock out SSH unless the SSH port is allowed; confirm with the user.",
		func(ctx context.Context, sess *mcpSession, in firewallToggleInput) (any, error) {
			action := "disable"
			if in.Enabled {
				action = "enable"
			}
			return s.panelCall(ctx, sess, "POST", "/firewall/"+action, nil)
		})

	addPanelTool(srv, "firewall_add_rule",
		"Add a ufw rule. Requires user confirmation.",
		func(ctx context.Context, sess *mcpSession, in firewallRuleInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/firewall/rules", map[string]any{
				"port":    in.Port,
				"proto":   in.Proto,
				"action":  in.Action,
				"from":    in.From,
				"comment": in.Comment,
			})
		})

	addPanelTool(srv, "firewall_delete_rule",
		"Delete a ufw rule by its number. Requires user confirmation.",
		func(ctx context.Context, sess *mcpSession, in firewallRuleNumberInput) (any, error) {
			if in.Number <= 0 {
				return nil, errors.New("number must be greater than zero")
			}
			return s.panelCall(ctx, sess, "DELETE", "/firewall/rules/"+strconv.Itoa(in.Number), nil)
		})
}

// ------------------------------------------------------------- apps/jobs ----

type installAppsInput struct {
	Apps        []string `json:"apps" jsonschema:"catalog slugs to install"`
	UpdateCache bool     `json:"update_cache,omitempty" jsonschema:"refresh the catalog before installing"`
}

type jobListInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum jobs, 1-200 (default 50)"`
	Status string `json:"status,omitempty" jsonschema:"filter by status, for example queued, running, success or failed"`
	Type   string `json:"type,omitempty" jsonschema:"filter by job type, for example backup or cert"`
}

type jobIDInput struct {
	ID string `json:"id" jsonschema:"job id"`
}

func (s *Server) registerAppMCPTools(srv *mcp.Server) {
	addPanelTool(srv, "list_apps",
		"List the application catalog (nginx, PHP, MySQL, etc.) with installed versions and available actions.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			return s.panelCall(ctx, sess, "GET", "/apps/catalog", nil)
		})

	addPanelTool(srv, "install_apps",
		"Install or update applications from the catalog. This runs apt and can take several minutes.",
		func(ctx context.Context, sess *mcpSession, in installAppsInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/apps/install", map[string]any{
				"apps":         in.Apps,
				"update_cache": in.UpdateCache,
			})
		})

	addPanelTool(srv, "update_apps_cache",
		"Refresh the application catalog cache.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			return s.panelCall(ctx, sess, "POST", "/apps/update-cache", nil)
		})

	addPanelTool(srv, "list_jobs",
		"List background jobs (backups, certificates, app installs) with progress and status.",
		func(ctx context.Context, sess *mcpSession, in jobListInput) (any, error) {
			q := map[string]string{"status": in.Status, "type": in.Type}
			if in.Limit > 0 {
				q["limit"] = strconv.Itoa(in.Limit)
			}
			return s.panelCall(ctx, sess, "GET", withQuery("/jobs", q), nil)
		})

	addPanelTool(srv, "get_job",
		"Get one background job, including its output and error text.",
		func(ctx context.Context, sess *mcpSession, in jobIDInput) (any, error) {
			if strings.TrimSpace(in.ID) == "" {
				return nil, errors.New("id is required")
			}
			return s.panelCall(ctx, sess, "GET", "/jobs/"+pathSegment(in.ID), nil)
		})
}

// ------------------------------------------------------------------ meta ----

type routeInfo struct {
	Method string   `json:"method"`
	Path   string   `json:"path"`
	Roles  []string `json:"roles,omitempty"`
}

type panelAPIInput struct {
	Method string `json:"method" jsonschema:"HTTP method: GET, POST, PUT, PATCH or DELETE"`
	Path   string `json:"path" jsonschema:"path below /api/v1, for example /sites or /sites/1/enable"`
	Body   any    `json:"body,omitempty" jsonschema:"optional JSON request body"`
}

func (s *Server) registerMetaMCPTools(srv *mcp.Server) {
	addPanelTool(srv, "panel_routes",
		"List every REST endpoint the panel exposes, with its HTTP method and allowed roles. "+
			"Use this together with panel_api to reach operations that have no dedicated tool.",
		func(ctx context.Context, sess *mcpSession, _ emptyInput) (any, error) {
			list := make([]routeInfo, 0, len(s.handlers))
			for _, route := range s.handlers {
				list = append(list, routeInfo{Method: route.method, Path: APIPrefix + route.pattern, Roles: route.roles})
			}
			return list, nil
		})

	addPanelTool(srv, "panel_api",
		"Call any panel REST endpoint as the token owner's user. Paths start with /. "+
			"Prefer the dedicated tools; use this for operations they do not cover.",
		func(ctx context.Context, sess *mcpSession, in panelAPIInput) (any, error) {
			method := strings.ToUpper(strings.TrimSpace(in.Method))
			switch method {
			case "GET", "POST", "PUT", "PATCH", "DELETE":
			default:
				return nil, fmt.Errorf("unsupported method %q", in.Method)
			}
			path := strings.TrimSpace(in.Path)
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			return s.panelCall(ctx, sess, method, path, in.Body)
		})
}

// ------------------------------------------------------------------ files ----

func (s *Server) registerFileMCPTools(srv *mcp.Server) {
	s.registerFileTools(srv)
}
