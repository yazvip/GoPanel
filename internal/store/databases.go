package store

import (
	"context"

	"gopanel/internal/model"
)

func (s *Store) CreateDatabaseRecord(ctx context.Context, d *model.Database) error {
	d.CreatedAt = now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO databases(name, username, password_enc, created_at) VALUES(?,?,?,?)`, d.Name, d.Username, d.PasswordEnc, d.CreatedAt)
	if err != nil {
		return err
	}
	d.ID, err = res.LastInsertId()
	return err
}

func (s *Store) ListDatabases(ctx context.Context) ([]model.Database, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, username, password_enc, created_at FROM databases ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Database
	for rows.Next() {
		var d model.Database
		if err := rows.Scan(&d.ID, &d.Name, &d.Username, &d.PasswordEnc, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetDatabase(ctx context.Context, name string) (*model.Database, error) {
	var d model.Database
	err := s.db.QueryRowContext(ctx, `SELECT id, name, username, password_enc, created_at FROM databases WHERE name=?`, name).
		Scan(&d.ID, &d.Name, &d.Username, &d.PasswordEnc, &d.CreatedAt)
	if err != nil {
		return nil, NormalizeErr(err)
	}
	return &d, nil
}

func (s *Store) UpdateDatabasePassword(ctx context.Context, name string, enc []byte) error {
	_, err := s.db.ExecContext(ctx, `UPDATE databases SET password_enc=? WHERE name=?`, enc, name)
	return err
}

func (s *Store) DeleteDatabaseRecord(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM databases WHERE name=?`, name)
	return err
}
