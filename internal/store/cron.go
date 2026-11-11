package store

import (
	"context"

	"gopanel/internal/model"
)

const cronColumns = `id, name, schedule, command, enabled, last_run_at, last_status, created_by, created_at, updated_at`

func scanCron(row scanner) (*model.CronJob, error) {
	var c model.CronJob
	var enabled int
	if err := row.Scan(&c.ID, &c.Name, &c.Schedule, &c.Command, &enabled, &c.LastRunAt, &c.LastStatus, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.Enabled = enabled != 0
	return &c, nil
}

func (s *Store) ListCronJobs(ctx context.Context) ([]model.CronJob, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cronColumns+` FROM cron_jobs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CronJob
	for rows.Next() {
		c, err := scanCron(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) GetCronJob(ctx context.Context, id int64) (*model.CronJob, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+cronColumns+` FROM cron_jobs WHERE id=?`, id)
	c, err := scanCron(row)
	if err != nil {
		return nil, NormalizeErr(err)
	}
	return c, nil
}

func (s *Store) CreateCronJob(ctx context.Context, c *model.CronJob) error {
	ts := now()
	c.CreatedAt = ts
	c.UpdatedAt = ts
	res, err := s.db.ExecContext(ctx, `INSERT INTO cron_jobs(name, schedule, command, enabled, last_run_at, last_status, created_by, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		c.Name, c.Schedule, c.Command, boolInt(c.Enabled), c.LastRunAt, c.LastStatus, c.CreatedBy, c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return err
	}
	c.ID, err = res.LastInsertId()
	return err
}

func (s *Store) UpdateCronJob(ctx context.Context, c *model.CronJob) error {
	c.UpdatedAt = now()
	_, err := s.db.ExecContext(ctx, `UPDATE cron_jobs SET name=?, schedule=?, command=?, enabled=?, updated_at=? WHERE id=?`,
		c.Name, c.Schedule, c.Command, boolInt(c.Enabled), c.UpdatedAt, c.ID)
	return err
}

func (s *Store) SetCronRun(ctx context.Context, id int64, ts int64, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE cron_jobs SET last_run_at=?, last_status=? WHERE id=?`, ts, status, id)
	return err
}

func (s *Store) DeleteCronJob(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cron_jobs WHERE id=?`, id)
	return err
}
