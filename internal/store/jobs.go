package store

import (
	"context"

	"gopanel/internal/model"
)

const jobColumns = `id, type, status, payload, progress, message, output, error, created_by, created_at, started_at, finished_at`

func scanJob(row scanner) (*model.Job, error) {
	var j model.Job
	if err := row.Scan(&j.ID, &j.Type, &j.Status, &j.Payload, &j.Progress, &j.Message, &j.Output, &j.Error, &j.CreatedBy, &j.CreatedAt, &j.StartedAt, &j.FinishedAt); err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *Store) CreateJob(ctx context.Context, j *model.Job) error {
	if j.CreatedAt == 0 {
		j.CreatedAt = now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO jobs(id, type, status, payload, progress, message, output, error, created_by, created_at, started_at, finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.Type, j.Status, j.Payload, j.Progress, j.Message, j.Output, j.Error, j.CreatedBy, j.CreatedAt, j.StartedAt, j.FinishedAt)
	return err
}

func (s *Store) UpdateJob(ctx context.Context, j *model.Job) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?, progress=?, message=?, output=?, error=?, started_at=?, finished_at=? WHERE id=?`,
		j.Status, j.Progress, j.Message, j.Output, j.Error, j.StartedAt, j.FinishedAt, j.ID)
	return err
}

func (s *Store) GetJob(ctx context.Context, id string) (*model.Job, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, id)
	j, err := scanJob(row)
	if err != nil {
		return nil, NormalizeErr(err)
	}
	return j, nil
}

func (s *Store) ListJobs(ctx context.Context, limit int) ([]model.Job, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (s *Store) MarkJobsInterrupted(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='failed', error='panel stopped before completion', finished_at=? WHERE status IN ('queued','running')`, now())
	return err
}

func (s *Store) PruneJobs(ctx context.Context, before int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE created_at<? AND status IN ('success','failed')`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
