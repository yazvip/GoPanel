package store

import (
	"context"

	"gopanel/internal/model"
)

const backupColumns = `id, kind, ref, path, size, status, error, created_at`

func scanBackup(row scanner) (*model.Backup, error) {
	var b model.Backup
	if err := row.Scan(&b.ID, &b.Kind, &b.Ref, &b.Path, &b.Size, &b.Status, &b.Error, &b.CreatedAt); err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *Store) CreateBackup(ctx context.Context, b *model.Backup) error {
	if b.CreatedAt == 0 {
		b.CreatedAt = now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO backups(id, kind, ref, path, size, status, error, created_at) VALUES(?,?,?,?,?,?,?,?)`,
		b.ID, b.Kind, b.Ref, b.Path, b.Size, b.Status, b.Error, b.CreatedAt)
	return err
}

func (s *Store) UpdateBackup(ctx context.Context, b *model.Backup) error {
	_, err := s.db.ExecContext(ctx, `UPDATE backups SET size=?, status=?, error=? WHERE id=?`, b.Size, b.Status, b.Error, b.ID)
	return err
}

func (s *Store) ListBackups(ctx context.Context) ([]model.Backup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+backupColumns+` FROM backups ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func (s *Store) GetBackup(ctx context.Context, id string) (*model.Backup, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+backupColumns+` FROM backups WHERE id=?`, id)
	b, err := scanBackup(row)
	if err != nil {
		return nil, NormalizeErr(err)
	}
	return b, nil
}

func (s *Store) ListBackupsFor(ctx context.Context, kind, ref string) ([]model.Backup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+backupColumns+` FROM backups WHERE kind=? AND ref=? ORDER BY created_at DESC, id`, kind, ref)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func (s *Store) DeleteBackup(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM backups WHERE id=?`, id)
	return err
}

func (s *Store) MarkBackupsInterrupted(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE backups SET status='failed', error='panel stopped before completion' WHERE status='running'`)
	return err
}
