package store

import (
	"context"
	"database/sql"

	"gopanel/internal/model"
)

const siteColumns = `id, name, root_path, type, php_version, upstream, ssl_enabled, ssl_cert_path, ssl_key_path, ssl_expires_at, enabled, index_file, client_max_body, access_log, created_at, updated_at`

func scanSite(row scanner) (*model.Site, error) {
	var s model.Site
	var ssl, enabled, accessLog int
	err := row.Scan(&s.ID, &s.Name, &s.RootPath, &s.Type, &s.PHPVersion, &s.Upstream, &ssl, &s.SSLCertPath, &s.SSLKeyPath, &s.SSLExpiresAt, &enabled, &s.IndexFile, &s.ClientMaxBody, &accessLog, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	s.SSLEnabled = ssl != 0
	s.Enabled = enabled != 0
	s.AccessLog = accessLog != 0
	return &s, nil
}

func (s *Store) CreateSite(ctx context.Context, site *model.Site) error {
	ts := now()
	site.CreatedAt = ts
	site.UpdatedAt = ts
	res, err := s.db.ExecContext(ctx, `INSERT INTO sites(name, root_path, type, php_version, upstream, ssl_enabled, ssl_cert_path, ssl_key_path, ssl_expires_at, enabled, index_file, client_max_body, access_log, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		site.Name, site.RootPath, site.Type, site.PHPVersion, site.Upstream, boolInt(site.SSLEnabled), site.SSLCertPath, site.SSLKeyPath, site.SSLExpiresAt, boolInt(site.Enabled), site.IndexFile, site.ClientMaxBody, boolInt(site.AccessLog), site.CreatedAt, site.UpdatedAt)
	if err != nil {
		return err
	}
	site.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetSite(ctx context.Context, id int64) (*model.Site, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+siteColumns+` FROM sites WHERE id=?`, id)
	site, err := scanSite(row)
	if err != nil {
		return nil, NormalizeErr(err)
	}
	return site, nil
}

func (s *Store) GetSiteByName(ctx context.Context, name string) (*model.Site, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+siteColumns+` FROM sites WHERE name=?`, name)
	site, err := scanSite(row)
	if err != nil {
		return nil, NormalizeErr(err)
	}
	return site, nil
}

func (s *Store) GetSiteByDomain(ctx context.Context, domain string) (*model.Site, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+prefixedColumns("s", siteColumns)+` FROM sites s JOIN site_domains d ON d.site_id=s.id WHERE d.domain=?`, domain)
	site, err := scanSite(row)
	if err != nil {
		return nil, NormalizeErr(err)
	}
	return site, nil
}

func (s *Store) ListSites(ctx context.Context) ([]model.Site, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+siteColumns+` FROM sites ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var out []model.Site
	for rows.Next() {
		site, err := scanSite(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *site)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range out {
		domains, err := s.ListDomains(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Domains = domains
	}
	return out, nil
}

func (s *Store) UpdateSite(ctx context.Context, site *model.Site) error {
	site.UpdatedAt = now()
	_, err := s.db.ExecContext(ctx, `UPDATE sites SET name=?, root_path=?, type=?, php_version=?, upstream=?, enabled=?, index_file=?, client_max_body=?, access_log=?, updated_at=? WHERE id=?`,
		site.Name, site.RootPath, site.Type, site.PHPVersion, site.Upstream, boolInt(site.Enabled), site.IndexFile, site.ClientMaxBody, boolInt(site.AccessLog), site.UpdatedAt, site.ID)
	return err
}

func (s *Store) SetSiteEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sites SET enabled=?, updated_at=? WHERE id=?`, boolInt(enabled), now(), id)
	return err
}

func (s *Store) SetSiteSSL(ctx context.Context, id int64, enabled bool, certPath, keyPath string, expires int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sites SET ssl_enabled=?, ssl_cert_path=?, ssl_key_path=?, ssl_expires_at=?, updated_at=? WHERE id=?`,
		boolInt(enabled), certPath, keyPath, expires, now(), id)
	return err
}

func (s *Store) DeleteSite(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sites WHERE id=?`, id)
	return err
}

func (s *Store) CountSites(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sites`).Scan(&n)
	return n, err
}

const domainColumns = `id, site_id, domain, is_primary`

func (s *Store) ListDomains(ctx context.Context, siteID int64) ([]model.Domain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+domainColumns+` FROM site_domains WHERE site_id=? ORDER BY is_primary DESC, id`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Domain
	for rows.Next() {
		var d model.Domain
		var primary int
		if err := rows.Scan(&d.ID, &d.SiteID, &d.Domain, &primary); err != nil {
			return nil, err
		}
		d.IsPrimary = primary != 0
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) AddDomain(ctx context.Context, siteID int64, domain string, primary bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if primary {
		if _, err := tx.ExecContext(ctx, `UPDATE site_domains SET is_primary=0 WHERE site_id=?`, siteID); err != nil {
			tx.Rollback()
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO site_domains(site_id, domain, is_primary) VALUES(?,?,?)`, siteID, domain, boolInt(primary)); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) SetPrimaryDomain(ctx context.Context, siteID, domainID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE site_domains SET is_primary=0 WHERE site_id=?`, siteID); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE site_domains SET is_primary=1 WHERE id=? AND site_id=?`, domainID, siteID); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteDomain(ctx context.Context, siteID, domainID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	var primary int
	if err := tx.QueryRowContext(ctx, `SELECT is_primary FROM site_domains WHERE id=? AND site_id=?`, domainID, siteID).Scan(&primary); err != nil {
		tx.Rollback()
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_domains WHERE id=?`, domainID); err != nil {
		tx.Rollback()
		return err
	}
	if primary != 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE site_domains SET is_primary=1 WHERE id=(SELECT id FROM site_domains WHERE site_id=? ORDER BY id LIMIT 1)`, siteID); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DomainExists(ctx context.Context, domain string, excludeSite int64) (bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT site_id FROM site_domains WHERE domain=?`, domain).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return id != excludeSite, nil
}

func (s *Store) CountDomains(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM site_domains`).Scan(&n)
	return n, err
}

func (s *Store) ListSSLExpiring(ctx context.Context, before int64) ([]model.Site, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+siteColumns+` FROM sites WHERE ssl_enabled=1 AND ssl_expires_at>0 AND ssl_expires_at<=? ORDER BY ssl_expires_at`, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Site
	for rows.Next() {
		site, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *site)
	}
	return out, rows.Err()
}

func prefixedColumns(prefix, columns string) string {
	out := ""
	start := 0
	for i := 0; i <= len(columns); i++ {
		if i == len(columns) || columns[i] == ',' {
			if out != "" {
				out += ", "
			}
			out += prefix + "." + trimSpace(columns[start:i])
			start = i + 1
		}
	}
	return out
}

func trimSpace(v string) string {
	for len(v) > 0 && (v[0] == ' ' || v[0] == '\t') {
		v = v[1:]
	}
	for len(v) > 0 && (v[len(v)-1] == ' ' || v[len(v)-1] == '\t') {
		v = v[:len(v)-1]
	}
	return v
}
