package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"gopanel/internal/model"
)

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(row scanner) (*model.User, error) {
	var u model.User
	var totpEnabled, disabled, mustChange int
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Locale, &u.TOTPSecretEnc,
		&totpEnabled, &u.TOTPLastStep, &disabled, &mustChange, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt)
	if err != nil {
		return nil, NormalizeErr(err)
	}
	u.TOTPEnabled = totpEnabled != 0
	u.Disabled = disabled != 0
	u.MustChangePassword = mustChange != 0
	return &u, nil
}

const userColumns = `id, username, password_hash, role, locale, totp_secret_enc, totp_enabled, totp_last_step, disabled, must_change_password, created_at, updated_at, last_login_at`

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, u *model.User) error {
	ts := now()
	if u.CreatedAt == 0 {
		u.CreatedAt = ts
	}
	u.UpdatedAt = ts
	res, err := s.db.ExecContext(ctx, `INSERT INTO users(username, password_hash, role, locale, totp_secret_enc, totp_enabled, totp_last_step, disabled, must_change_password, created_at, updated_at, last_login_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		u.Username, u.PasswordHash, u.Role, u.Locale, u.TOTPSecretEnc, boolInt(u.TOTPEnabled), u.TOTPLastStep, boolInt(u.Disabled), boolInt(u.MustChangePassword), u.CreatedAt, u.UpdatedAt, u.LastLoginAt)
	if err != nil {
		return err
	}
	u.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE username = ? COLLATE NOCASE`, username)
	return scanUser(row)
}

func (s *Store) GetUser(ctx context.Context, id int64) (*model.User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id)
	return scanUser(row)
}

func (s *Store) ListUsers(ctx context.Context) ([]model.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (s *Store) UpdateUser(ctx context.Context, u *model.User) error {
	u.UpdatedAt = now()
	_, err := s.db.ExecContext(ctx, `UPDATE users SET username=?, role=?, locale=?, disabled=?, must_change_password=?, updated_at=? WHERE id=?`,
		u.Username, u.Role, u.Locale, boolInt(u.Disabled), boolInt(u.MustChangePassword), u.UpdatedAt, u.ID)
	return err
}

func (s *Store) UpdateLocale(ctx context.Context, id int64, locale string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET locale=?, updated_at=? WHERE id=?`, locale, now(), id)
	return err
}

func (s *Store) UpdatePassword(ctx context.Context, id int64, hash string, mustChange bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash=?, must_change_password=?, updated_at=? WHERE id=?`, hash, boolInt(mustChange), now(), id)
	return err
}

func (s *Store) UpdateTOTP(ctx context.Context, id int64, enc []byte, enabled bool, lastStep int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET totp_secret_enc=?, totp_enabled=?, totp_last_step=?, updated_at=? WHERE id=?`, enc, boolInt(enabled), lastStep, now(), id)
	return err
}

func (s *Store) SetLastLogin(ctx context.Context, id int64, ts int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET last_login_at=? WHERE id=?`, ts, id)
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	return err
}

func (s *Store) ReplaceRecoveryCodes(ctx context.Context, userID int64, hashes []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id=?`, userID); err != nil {
		tx.Rollback()
		return err
	}
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_codes(user_id, code_hash) VALUES(?,?)`, userID, h); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

type RecoveryCode struct {
	ID     int64
	Hash   string
	UsedAt int64
}

func (s *Store) UnusedRecoveryCodes(ctx context.Context, userID int64) ([]RecoveryCode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, code_hash, used_at FROM recovery_codes WHERE user_id=? AND used_at=0`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecoveryCode
	for rows.Next() {
		var rc RecoveryCode
		if err := rows.Scan(&rc.ID, &rc.Hash, &rc.UsedAt); err != nil {
			return nil, err
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

func (s *Store) UseRecoveryCode(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE recovery_codes SET used_at=? WHERE id=? AND used_at=0`, now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CountRecoveryCodes(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM recovery_codes WHERE user_id=? AND used_at=0`, userID).Scan(&n)
	return n, err
}

func (s *Store) SetTOTPDisabled(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET totp_secret_enc=NULL, totp_enabled=0, totp_last_step=0, updated_at=? WHERE id=?`, now(), userID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id=?`, userID)
	return err
}

var ErrNotFound = errors.New("not found")

func NormalizeErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func Conflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
