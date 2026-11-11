package certs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gopanel/internal/acme"
	"gopanel/internal/jobs"
	"gopanel/internal/model"
	"gopanel/internal/sites"
	"gopanel/internal/store"
)

const JobType = "ssl-issue"

var ErrACMEDisabled = errors.New("ACME is not configured")

type Reporter interface {
	Progress(pct int, message string)
	Logf(format string, args ...any)
}

type Options struct {
	Email   string
	Staging bool
	Logger  *slog.Logger
}

type Service struct {
	st    *store.Store
	sites *sites.Service
	acme  *acme.Service
	opts  Options
}

type JobPayload struct {
	SiteID  int64  `json:"site_id"`
	Email   string `json:"email,omitempty"`
	Staging *bool  `json:"staging,omitempty"`
}

func New(st *store.Store, siteService *sites.Service, acmeService *acme.Service, opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Service{st: st, sites: siteService, acme: acmeService, opts: opts}
}

func (s *Service) Configured() bool {
	return s.acme != nil && s.acme.Configured()
}

func (s *Service) settings(ctx context.Context, email string, staging *bool) (string, bool) {
	effEmail := s.opts.Email
	effStaging := s.opts.Staging
	if v, ok, _ := s.st.GetSetting(ctx, "acme_email"); ok && strings.TrimSpace(v) != "" {
		effEmail = strings.TrimSpace(v)
	}
	if v, ok, _ := s.st.GetSetting(ctx, "acme_staging"); ok {
		effStaging = v == "true" || v == "1"
	}
	if email != "" {
		effEmail = email
	}
	if staging != nil {
		effStaging = *staging
	}
	return effEmail, effStaging
}

func (s *Service) Issue(ctx context.Context, siteID int64, email string, staging *bool, report Reporter) (*model.Site, error) {
	if !s.Configured() {
		return nil, ErrACMEDisabled
	}
	if report == nil {
		report = nopReporter{}
	}
	site, err := s.sites.Get(ctx, siteID)
	if err != nil {
		return nil, err
	}
	domains := make([]string, 0, len(site.Domains))
	for _, d := range site.Domains {
		domains = append(domains, d.Domain)
	}
	if len(domains) == 0 {
		return nil, fmt.Errorf("site %s has no domains", site.Name)
	}
	effEmail, effStaging := s.settings(ctx, email, staging)
	report.Logf("Requesting certificate for %s (staging=%t)", strings.Join(domains, ", "), effStaging)
	report.Progress(10, "Requesting certificate")
	res, err := s.acme.Issue(ctx, site, domains, effStaging, effEmail)
	if err != nil {
		return nil, err
	}
	report.Progress(80, "Installing certificate")
	if err := s.sites.SetSSL(ctx, site, true, res.CertPath, res.KeyPath, res.ExpiresAt.Unix()); err != nil {
		return nil, err
	}
	report.Logf("Certificate installed, expires %s", res.ExpiresAt.UTC().Format(time.RFC3339))
	report.Progress(100, "HTTPS enabled")
	return site, nil
}

// RenewDue refreshes or renews every certificate expiring before the cutoff.
// A renewed certificate file newer than the database row only has its expiry
// synced; only genuinely expiring certificates trigger a new ACME order.
func (s *Service) RenewDue(ctx context.Context, before time.Time) (renewed, failed int) {
	if !s.Configured() {
		return 0, 0
	}
	list, err := s.st.ListSSLExpiring(ctx, before.Unix())
	if err != nil {
		s.opts.Logger.Warn("list expiring certificates", "error", err)
		return 0, 1
	}
	for _, candidate := range list {
		site, err := s.sites.Get(ctx, candidate.ID)
		if err != nil {
			s.opts.Logger.Warn("load site for renewal", "site", candidate.Name, "error", err)
			failed++
			continue
		}
		if exp, err := acme.ExpiryFile(site.SSLCertPath); err == nil {
			if exp.After(before) {
				if err := s.st.SetSiteSSL(ctx, site.ID, true, site.SSLCertPath, site.SSLKeyPath, exp.Unix()); err != nil {
					s.opts.Logger.Warn("sync certificate expiry", "site", site.Name, "error", err)
				}
				continue
			}
		}
		if _, err := s.Issue(ctx, site.ID, "", nil, nil); err != nil {
			s.opts.Logger.Warn("renew certificate", "site", site.Name, "error", err)
			failed++
			continue
		}
		s.opts.Logger.Info("certificate renewed", "site", site.Name)
		renewed++
	}
	return renewed, failed
}

func (s *Service) RegisterJobs(m *jobs.Manager) error {
	return m.Register(JobType, 2, func(ctx context.Context, r *jobs.Runner) error {
		var p JobPayload
		if err := r.DecodePayload(&p); err != nil {
			return err
		}
		_, err := s.Issue(ctx, p.SiteID, p.Email, p.Staging, r)
		return err
	})
}

type nopReporter struct{}

func (nopReporter) Progress(int, string) {}
func (nopReporter) Logf(string, ...any)  {}
