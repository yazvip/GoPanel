package backup

import (
	"context"
	"fmt"

	"gopanel/internal/jobs"
	"gopanel/internal/model"
)

const (
	JobType        = "backup"
	RestoreJobType = "backup-restore"
)

type JobPayload struct {
	Kind       string `json:"kind"`
	Ref        string `json:"ref"`
	Keep       int    `json:"keep,omitempty"`
	ScheduleID int64  `json:"schedule_id,omitempty"`
}

type RestoreJobPayload struct {
	BackupID string `json:"backup_id"`
}

func (s *Service) RegisterJobs(m *jobs.Manager) error {
	if err := m.Register(JobType, 2, func(ctx context.Context, r *jobs.Runner) error {
		var p JobPayload
		if err := r.DecodePayload(&p); err != nil {
			return err
		}
		return s.RunJob(ctx, p, r)
	}); err != nil {
		return err
	}
	return m.Register(RestoreJobType, 1, func(ctx context.Context, r *jobs.Runner) error {
		var p RestoreJobPayload
		if err := r.DecodePayload(&p); err != nil {
			return err
		}
		return s.Restore(ctx, p.BackupID, r)
	})
}

func (s *Service) RunJob(ctx context.Context, p JobPayload, report Reporter) error {
	if report == nil {
		report = nopReporter{}
	}
	var (
		rec *model.Backup
		err error
	)
	switch p.Kind {
	case KindSite:
		var site *model.Site
		site, err = s.st.GetSiteByName(ctx, p.Ref)
		if err != nil {
			return fmt.Errorf("site %s: %w", p.Ref, err)
		}
		rec, err = s.BackupSite(ctx, site, report)
	case KindDatabase:
		rec, err = s.BackupDatabase(ctx, p.Ref, report)
	default:
		return ErrInvalidKind
	}
	if err != nil {
		return err
	}
	report.Logf("Backup finished: %s (%d bytes)", rec.ID, rec.Size)
	if p.Keep > 0 {
		removed, rerr := s.Retention(ctx, p.Kind, p.Ref, p.Keep)
		if rerr != nil {
			report.Logf("Retention failed: %v", rerr)
		} else if removed > 0 {
			report.Logf("Retention removed %d old backup(s), keeping %d", removed, p.Keep)
		}
	}
	return nil
}
