package httpapi

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"gopanel/internal/audit"
	"gopanel/internal/sys"
	"gopanel/internal/validate"
)

func (s *Server) handleFirewall(w http.ResponseWriter, r *http.Request) {
	status, err := s.System.UFWStatus(r.Context())
	if err != nil {
		s.writeError(w, errf(http.StatusBadGateway, "firewall.unavailable", "%v", err))
		return
	}
	if status.Rules == nil {
		status.Rules = []sys.FirewallRule{}
	}
	s.ok(w, status)
}

func (s *Server) handleFirewallEnable(w http.ResponseWriter, r *http.Request) {
	if err := s.ensureAccessPorts(r); err != nil {
		s.writeError(w, err)
		return
	}
	if err := s.System.UFWEnable(r.Context()); err != nil {
		s.auditEvent(r, "firewall.enable", "", err.Error(), audit.ResultFailed)
		s.writeError(w, errf(http.StatusBadGateway, "firewall.enable_failed", "%v", err))
		return
	}
	s.auditEvent(r, "firewall.enable", "", "", audit.ResultSuccess)
	s.respondFirewall(w, r)
}

func (s *Server) handleFirewallDisable(w http.ResponseWriter, r *http.Request) {
	if err := s.System.UFWDisable(r.Context()); err != nil {
		s.auditEvent(r, "firewall.disable", "", err.Error(), audit.ResultFailed)
		s.writeError(w, errf(http.StatusBadGateway, "firewall.disable_failed", "%v", err))
		return
	}
	s.auditEvent(r, "firewall.disable", "", "", audit.ResultSuccess)
	s.respondFirewall(w, r)
}

func (s *Server) respondFirewall(w http.ResponseWriter, r *http.Request) {
	status, err := s.System.UFWStatus(r.Context())
	if err != nil {
		s.writeError(w, errf(http.StatusBadGateway, "firewall.unavailable", "%v", err))
		return
	}
	if status.Rules == nil {
		status.Rules = []sys.FirewallRule{}
	}
	s.ok(w, status)
}

// ensureAccessPorts adds allow rules for SSH and the panel listener before the
// firewall comes up, so enabling it cannot lock the operator out.
func (s *Server) ensureAccessPorts(r *http.Request) error {
	ports := []string{}
	if s.Config != nil {
		if s.Config.SSHDPort > 0 {
			ports = append(ports, strconv.Itoa(s.Config.SSHDPort))
		}
		if _, port, err := net.SplitHostPort(s.Config.Listen); err == nil && port != "" {
			ports = append(ports, port)
		}
	}
	if len(ports) == 0 {
		return nil
	}
	status, err := s.System.UFWStatus(r.Context())
	if err != nil {
		return errf(http.StatusBadGateway, "firewall.unavailable", "%v", err)
	}
	for _, port := range ports {
		if firewallAllows(status.Rules, port) {
			continue
		}
		rule := sys.FirewallRuleInput{Port: port, Proto: "tcp", Action: "allow", From: "any", Comment: "gopanel access"}
		if err := s.System.UFWAddRule(r.Context(), rule); err != nil {
			return errf(http.StatusBadGateway, "firewall.rule_failed", "could not allow port %s: %v", port, err)
		}
		s.logger.Info("firewall allow access port", "port", port)
	}
	return nil
}

func firewallAllows(rules []sys.FirewallRule, port string) bool {
	for _, rule := range rules {
		if rule.Action != "ALLOW" && !strings.EqualFold(rule.Action, "allow") {
			continue
		}
		target := strings.TrimSpace(rule.To)
		if i := strings.Index(target, "/"); i >= 0 {
			target = target[:i]
		}
		if i := strings.LastIndexAny(target, " :"); i >= 0 {
			target = target[i+1:]
		}
		if target == port {
			return true
		}
	}
	return false
}

type firewallRuleRequest struct {
	Port    string `json:"port"`
	Proto   string `json:"proto"`
	Action  string `json:"action"`
	From    string `json:"from"`
	Comment string `json:"comment"`
}

func (s *Server) handleFirewallAddRule(w http.ResponseWriter, r *http.Request) {
	var req firewallRuleRequest
	if err := s.decode(w, r, &req, 16<<10); err != nil {
		s.writeError(w, err)
		return
	}
	req.Port = strings.TrimSpace(req.Port)
	if err := validate.PortSpec(req.Port); err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "firewall.port_invalid", "%v", err))
		return
	}
	if req.Proto == "" {
		req.Proto = "tcp"
	}
	if err := validate.FirewallProto(req.Proto); err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "firewall.proto_invalid", "%v", err))
		return
	}
	if req.Action == "" {
		req.Action = "allow"
	}
	if err := validate.FirewallAction(req.Action); err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "firewall.action_invalid", "%v", err))
		return
	}
	if req.From == "" {
		req.From = "any"
	}
	if err := validate.CIDROrIP(req.From); err != nil {
		s.writeError(w, errf(http.StatusBadRequest, "firewall.from_invalid", "%v", err))
		return
	}
	if len(req.Comment) > 120 {
		s.writeError(w, errf(http.StatusBadRequest, "firewall.comment_too_long", "comment is limited to 120 characters"))
		return
	}
	rule := sys.FirewallRuleInput{Port: req.Port, Proto: req.Proto, Action: req.Action, From: req.From, Comment: strings.TrimSpace(req.Comment)}
	if err := s.System.UFWAddRule(r.Context(), rule); err != nil {
		s.auditEvent(r, "firewall.rule_add", req.Port, err.Error(), audit.ResultFailed)
		s.writeError(w, errf(http.StatusBadGateway, "firewall.rule_failed", "%v", err))
		return
	}
	s.auditEvent(r, "firewall.rule_add", fmt.Sprintf("%s/%s %s", req.Port, req.Proto, req.Action), req.From, audit.ResultSuccess)
	s.respondFirewall(w, r)
}

func (s *Server) handleFirewallDeleteRule(w http.ResponseWriter, r *http.Request) {
	num, err := s.pathID(r, "num")
	if err != nil {
		s.writeError(w, err)
		return
	}
	status, serr := s.System.UFWStatus(r.Context())
	if serr == nil {
		for _, rule := range status.Rules {
			if rule.Number != int(num) {
				continue
			}
			if s.Config != nil && rule.Proto != "udp" && firewallAllows([]sys.FirewallRule{rule}, strconv.Itoa(s.Config.SSHDPort)) {
				s.writeError(w, errf(http.StatusConflict, "firewall.ssh_protected", "this rule allows SSH access and cannot be removed from the panel"))
				return
			}
			break
		}
	}
	if err := s.System.UFWDeleteRule(r.Context(), int(num)); err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) {
			s.writeError(w, err)
			return
		}
		s.writeError(w, errf(http.StatusBadGateway, "firewall.rule_failed", "%v", err))
		return
	}
	s.auditEvent(r, "firewall.rule_delete", strconv.FormatInt(num, 10), "", audit.ResultSuccess)
	s.respondFirewall(w, r)
}
