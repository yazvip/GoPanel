package store

import (
	"context"

	"gopanel/internal/model"
)

const apiTokenColumns = `id, user_id, name, prefix, created_at, last_used_at, expires_at`

func (s *Store) CreateAPIToken(ctx context.Context, userID int64, name, prefix, hash string, expiresAt int64) (*model.APIToken, error) {
	ts := now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO api_tokens(user_id, name, token_hash, prefix, created_at, last_used_at, expires_at)
		 VALUES(?,?,?,?,?,0,?)`,
		userID, name, hash, prefix, ts, expiresAt)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &model.APIToken{
		ID:        id,
		UserID:    userID,
		Name:      name,
		Prefix:    prefix,
		CreatedAt: ts,
		ExpiresAt: expiresAt,
	}, nil
}

func (s *Store) ListAPITokens(ctx context.Context, userID int64) ([]model.APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+apiTokenColumns+` FROM api_tokens WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.APIToken
	for rows.Next() {
		var t model.APIToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

func (s *Store) CountAPITokens(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

func (s *Store) FindAPITokenByHash(ctx context.Context, hash string) (*model.APIToken, error) {
	var t model.APIToken
	err := s.db.QueryRowContext(ctx,
		`SELECT `+apiTokenColumns+` FROM api_tokens WHERE token_hash = ?`, hash).
		Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt)
	if err != nil {
		return nil, NormalizeErr(err)
	}
	return &t, nil
}

func (s *Store) TouchAPIToken(ctx context.Context, id, ts int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, ts, id)
	return err
}

func (s *Store) DeleteAPIToken(ctx context.Context, id, userID int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
