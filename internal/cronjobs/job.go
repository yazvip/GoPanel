package cronjobs

import (
	"context"
	"fmt"

	"gopanel/internal/jobs"
)

const JobType = "cron-run"

type JobPayload struct {
	ID int64 `json:"id"`
}

func (s *Service) RegisterJobs(m *jobs.Manager) error {
	return m.Register(JobType, 2, func(ctx context.Context, r *jobs.Runner) error {
		var p JobPayload
		if err := r.DecodePayload(&p); err != nil {
			return err
		}
		job, err := s.st.GetCronJob(ctx, p.ID)
		if err != nil {
			return fmt.Errorf("cron job %d: %w", p.ID, err)
		}
		return s.RunNow(ctx, job, r)
	})
}
