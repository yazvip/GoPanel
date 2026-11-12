package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"gopanel/internal/audit"
	"gopanel/internal/backup"
	"gopanel/internal/certs"
	"gopanel/internal/jobs"
	"gopanel/internal/store"
)

type Options struct {
	Store            *store.Store
	Backup           *backup.Service
	Certs            *certs.Service
	Jobs             *jobs.Manager
	Audit            *audit.Recorder
	Logger           *slog.Logger
	AuditRetention   time.Duration
	JobRetention     time.Duration
	JobLogsRetention time.Duration
	RenewBefore      time.Duration
	SSLHour          int
	SchedulePoll     time.Duration
	MaintenancePoll  time.Duration
}

type Service struct {
	opts   Options
	cron   *cron.Cron
	logger *slog.Logger
}

func New(opts Options) (*Service, error) {
	if opts.Store == nil || opts.Jobs == nil {
		return nil, fmt.Errorf("scheduler requires store and job manager")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.AuditRetention <= 0 {
		opts.AuditRetention = 90 * 24 * time.Hour
	}
	if opts.JobRetention <= 0 {
		opts.JobRetention = 30 * 24 * time.Hour
	}
	if opts.JobLogsRetention <= 0 {
		opts.JobLogsRetention = 30 * 24 * time.Hour
	}
	if opts.RenewBefore <= 0 {
		opts.RenewBefore = 30 * 24 * time.Hour
	}
	if opts.SSLHour <= 0 || opts.SSLHour > 23 {
		opts.SSLHour = 4
	}
	if opts.SchedulePoll <= 0 {
		opts.SchedulePoll = 30 * time.Second
	}
	if opts.MaintenancePoll <= 0 {
		opts.MaintenancePoll = 6 * time.Hour
	}

	s := &Service{opts: opts, logger: opts.Logger}
	logger := cronLogger{logger: opts.Logger}
	s.cron = cron.New(cron.WithLogger(logger), cron.WithChain(cron.Recover(logger)))

	scheduleSpec := fmt.Sprintf("@every %s", opts.SchedulePoll)
	if _, err := s.cron.AddFunc(scheduleSpec, s.checkSchedules); err != nil {
		return nil, err
	}
	maintenanceSpec := fmt.Sprintf("@every %s", opts.MaintenancePoll)
	if _, err := s.cron.AddFunc(maintenanceSpec, s.prune); err != nil {
		return nil, err
	}
	minute := 5 + rand.IntN(50)
	sslSpec := fmt.Sprintf("%d %d * * *", minute, opts.SSLHour)
	if _, err := s.cron.AddFunc(sslSpec, s.renewCertificates); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) Start() {
	s.logger.Info("scheduler started")
	s.cron.Start()
}

func (s *Service) Stop(ctx context.Context) error {
	stopCtx := s.cron.Stop()
	select {
	case <-stopCtx.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) checkSchedules() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schedules, err := s.opts.Store.ListSchedules(ctx)
	if err != nil {
		s.logger.Warn("list backup schedules", "error", err)
		return
	}
	now := time.Now()
	for _, sch := range schedules {
		if !sch.Enabled {
			continue
		}
		parser, err := cron.ParseStandard(sch.CronExpr)
		if err != nil {
			s.logger.Warn("invalid backup schedule", "id", sch.ID, "cron", sch.CronExpr, "error", err)
			continue
		}
		base := sch.LastRunAt
		if base == 0 {
			base = sch.CreatedAt
		}
		if base == 0 {
			base = now.Unix()
		}
		next := parser.Next(time.Unix(base, 0))
		if next.After(now) {
			continue
		}
		if s.scheduleBusy(ctx, sch.ID) {
			continue
		}
		payload := backup.JobPayload{Kind: sch.Kind, Ref: sch.Ref, Keep: sch.Keep, ScheduleID: sch.ID}
		job, err := s.opts.Jobs.Enqueue(ctx, backup.JobType, payload, 0)
		if err != nil {
			s.logger.Warn("enqueue scheduled backup", "schedule", sch.ID, "error", err)
			continue
		}
		if err := s.opts.Store.SetScheduleLastRun(ctx, sch.ID, now.Unix()); err != nil {
			s.logger.Warn("record schedule run", "schedule", sch.ID, "error", err)
		}
		s.logger.Info("scheduled backup queued", "schedule", sch.ID, "job", job.ID, "kind", sch.Kind, "ref", sch.Ref)
	}
}

func (s *Service) scheduleBusy(ctx context.Context, id int64) bool {
	list, err := s.opts.Store.ListJobs(ctx, 200)
	if err != nil {
		return false
	}
	needle := fmt.Sprintf(`"schedule_id":%d`, id)
	for _, j := range list {
		if j.Status == jobs.StatusQueued || j.Status == jobs.StatusRunning {
			if strings.Contains(j.Payload, needle) {
				return true
			}
		}
	}
	return false
}

func (s *Service) prune() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	now := time.Now()
	if n, err := s.opts.Store.PruneSessions(ctx); err != nil {
		s.logger.Warn("prune sessions", "error", err)
	} else if n > 0 {
		s.logger.Info("pruned sessions", "count", n)
	}
	if n, err := s.opts.Store.PruneMetrics(ctx, now.Add(-7*24*time.Hour).Unix()); err != nil {
		s.logger.Warn("prune metrics", "error", err)
	} else if n > 0 {
		s.logger.Info("pruned metric samples", "count", n)
	}
	if n, err := s.opts.Store.PruneAudit(ctx, now.Add(-s.opts.AuditRetention).Unix()); err != nil {
		s.logger.Warn("prune audit log", "error", err)
	} else if n > 0 {
		s.logger.Info("pruned audit rows", "count", n)
	}
	if n, err := s.opts.Store.PruneJobs(ctx, now.Add(-s.opts.JobRetention).Unix()); err != nil {
		s.logger.Warn("prune jobs", "error", err)
	} else if n > 0 {
		s.logger.Info("pruned jobs", "count", n)
	}
	if s.opts.Jobs != nil {
		if n, err := s.opts.Jobs.PruneJobLogs(ctx, now.Add(-s.opts.JobLogsRetention)); err != nil {
			s.logger.Warn("prune job logs", "error", err)
		} else if n > 0 {
			s.logger.Info("pruned job logs", "count", n)
		}
	}
}

func (s *Service) renewCertificates() {
	if s.opts.Certs == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cutoff := time.Now().Add(s.opts.RenewBefore)
	renewed, failed := s.opts.Certs.RenewDue(ctx, cutoff)
	if renewed > 0 || failed > 0 {
		s.logger.Info("certificate renewal pass finished", "renewed", renewed, "failed", failed)
	}
}

func (s *Service) audit(ctx context.Context, e audit.Event) {
	if s.opts.Audit == nil {
		return
	}
	s.opts.Audit.Record(ctx, e)
}

type cronLogger struct {
	logger *slog.Logger
}

func (c cronLogger) Info(msg string, keysAndValues ...any) {
	c.logger.Debug("cron: "+msg, keysAndValues...)
}

func (c cronLogger) Error(err error, msg string, keysAndValues ...any) {
	c.logger.Warn("cron: "+msg, append(keysAndValues, "error", err)...)
}
