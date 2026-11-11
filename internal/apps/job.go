package apps

import (
	"context"
	"fmt"
	"strings"

	"gopanel/internal/jobs"
)

const (
	JobType       = "app-install"
	UpdateJobType = "apt-update"
)

func (s *Service) RegisterJobs(m *jobs.Manager) error {
	if err := m.Register(JobType, 1, func(ctx context.Context, r *jobs.Runner) error {
		var req Request
		if err := r.DecodePayload(&req); err != nil {
			return err
		}
		r.Logf("Installing: %s", strings.Join(req.Apps, ", "))
		if err := s.Install(ctx, req, r); err != nil {
			return err
		}
		r.Logf("Done")
		return nil
	}); err != nil {
		return fmt.Errorf("register %s: %w", JobType, err)
	}
	if err := m.Register(UpdateJobType, 1, func(ctx context.Context, r *jobs.Runner) error {
		return s.Refresh(ctx, r)
	}); err != nil {
		return fmt.Errorf("register %s: %w", UpdateJobType, err)
	}
	return nil
}
