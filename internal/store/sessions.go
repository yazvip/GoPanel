package store

import (
	"context"

	"gopanel/internal/model"
)

func (s *Store) CreateSession(ctx context.Context, sess *model.Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(id, token_hash, user_id, csrf_token, ip, user_agent, created_at, last_seen_at, expires_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		sess.ID, sess.ID, sess.UserID, sess.CSRF, sess.IP, sess.UserAgent, sess.CreatedAt, sess.LastSeenAt, sess.ExpiresAt)
	return err
}

func (s *Store) GetSession(ctx context.Context, tokenHash string) (*model.Session, *model.User, error) {
	var sess model.Session
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, csrf_token, ip, user_agent, created_at, last_seen_at, expires_at FROM sessions WHERE token_hash=?`, tokenHash).
		Scan(&sess.ID, &sess.UserID, &sess.CSRF, &sess.IP, &sess.UserAgent, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt)
	if err != nil {
		return nil, nil, NormalizeErr(err)
	}
	user, err := s.GetUser(ctx, sess.UserID)
	if err != nil {
		return nil, nil, NormalizeErr(err)
	}
	return &sess, user, nil
}

func (s *Store) TouchSession(ctx context.Context, id string, lastSeen, expires int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at=?, expires_at=? WHERE id=?`, lastSeen, expires, id)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id)
	return err
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID int64, exceptID string) error {
	if exceptID == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND id<>?`, userID, exceptID)
	return err
}

func (s *Store) ListSessions(ctx context.Context, userID int64) ([]model.Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, csrf_token, ip, user_agent, created_at, last_seen_at, expires_at FROM sessions WHERE user_id=? AND expires_at>? ORDER BY last_seen_at DESC`, userID, now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Session
	for rows.Next() {
		var sess model.Session
		if err := rows.Scan(&sess.ID, &sess.UserID, &sess.CSRF, &sess.IP, &sess.UserAgent, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

func (s *Store) GetSessionByID(ctx context.Context, id string) (*model.Session, error) {
	var sess model.Session
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, csrf_token, ip, user_agent, created_at, last_seen_at, expires_at FROM sessions WHERE id=?`, id).
		Scan(&sess.ID, &sess.UserID, &sess.CSRF, &sess.IP, &sess.UserAgent, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt)
	if err != nil {
		return nil, NormalizeErr(err)
	}
	return &sess, nil
}

func (s *Store) PruneSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at<=?`, now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
