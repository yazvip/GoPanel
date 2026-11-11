package store

import (
	"context"

	"gopanel/internal/model"
)

const scheduleColumns = `id, kind, ref, cron_expr, keep, enabled, last_run_at, created_at`

func scanSchedule(row scanner) (*model.BackupSchedule, error) {
	var b model.BackupSchedule
	var enabled int
	if err := row.Scan(&b.ID, &b.Kind, &b.Ref, &b.CronExpr, &b.Keep, &enabled, &b.LastRunAt, &b.CreatedAt); err != nil {
		return nil, err
	}
	b.Enabled = enabled != 0
	return &b, nil
}

func (s *Store) ListSchedules(ctx context.Context) ([]model.BackupSchedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+scheduleColumns+` FROM backup_schedules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.BackupSchedule
	for rows.Next() {
		b, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func (s *Store) GetSchedule(ctx context.Context, id int64) (*model.BackupSchedule, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM backup_schedules WHERE id=?`, id)
	b, err := scanSchedule(row)
	if err != nil {
		return nil, NormalizeErr(err)
	}
	return b, nil
}

func (s *Store) CreateSchedule(ctx context.Context, b *model.BackupSchedule) error {
	b.CreatedAt = now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO backup_schedules(kind, ref, cron_expr, keep, enabled, last_run_at, created_at) VALUES(?,?,?,?,?,?,?)`,
		b.Kind, b.Ref, b.CronExpr, b.Keep, boolInt(b.Enabled), b.LastRunAt, b.CreatedAt)
	if err != nil {
		return err
	}
	b.ID, err = res.LastInsertId()
	return err
}

func (s *Store) UpdateSchedule(ctx context.Context, b *model.BackupSchedule) error {
	_, err := s.db.ExecContext(ctx, `UPDATE backup_schedules SET kind=?, ref=?, cron_expr=?, keep=?, enabled=? WHERE id=?`, b.Kind, b.Ref, b.CronExpr, b.Keep, boolInt(b.Enabled), b.ID)
	return err
}

func (s *Store) SetScheduleLastRun(ctx context.Context, id int64, ts int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE backup_schedules SET last_run_at=? WHERE id=?`, ts, id)
	return err
}

func (s *Store) DeleteSchedule(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM backup_schedules WHERE id=?`, id)
	return err
}
