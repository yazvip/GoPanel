package store

import (
	"context"

	"gopanel/internal/model"
)

func (s *Store) InsertAudit(ctx context.Context, a *model.AuditLog) error {
	if a.TS == 0 {
		a.TS = now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_logs(ts, user_id, username, ip, action, target, detail, result) VALUES(?,?,?,?,?,?,?,?)`,
		a.TS, a.UserID, a.Username, a.IP, a.Action, a.Target, a.Detail, a.Result)
	return err
}

func (s *Store) ListAudit(ctx context.Context, limit, offset int) ([]model.AuditLog, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_logs`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, ts, user_id, username, ip, action, target, detail, result FROM audit_logs ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]model.AuditLog, 0, limit)
	for rows.Next() {
		var a model.AuditLog
		if err := rows.Scan(&a.ID, &a.TS, &a.UserID, &a.Username, &a.IP, &a.Action, &a.Target, &a.Detail, &a.Result); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

func (s *Store) PruneAudit(ctx context.Context, before int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM audit_logs WHERE ts<?`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
