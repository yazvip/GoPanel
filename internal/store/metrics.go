package store

import (
	"context"

	"gopanel/internal/model"
)

func (s *Store) InsertSample(ctx context.Context, m model.MetricSample) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO metric_samples (ts, cpu, mem_used, mem_total, swap_used, disk_used, disk_total, net_rx, net_tx, load1) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		m.TS, m.CPU, m.MemUsed, m.MemTotal, m.SwapUsed, m.DiskUsed, m.DiskTotal, m.NetRx, m.NetTx, m.Load1)
	return err
}

func (s *Store) QueryMetrics(ctx context.Context, from, to int64, bucket int64) ([]model.MetricSample, error) {
	if bucket < 1 {
		bucket = 60
	}
	rows, err := s.db.QueryContext(ctx, `SELECT (ts/?)*? AS b,
		AVG(cpu),
		CAST(AVG(mem_used) AS INTEGER), MAX(mem_total),
		CAST(AVG(swap_used) AS INTEGER),
		CAST(AVG(disk_used) AS INTEGER), MAX(disk_total),
		MAX(net_rx), MAX(net_tx),
		AVG(load1)
		FROM metric_samples WHERE ts>=? AND ts<=? GROUP BY b ORDER BY b`, bucket, bucket, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.MetricSample
	for rows.Next() {
		var m model.MetricSample
		if err := rows.Scan(&m.TS, &m.CPU, &m.MemUsed, &m.MemTotal, &m.SwapUsed, &m.DiskUsed, &m.DiskTotal, &m.NetRx, &m.NetTx, &m.Load1); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) LatestSample(ctx context.Context) (*model.MetricSample, error) {
	var m model.MetricSample
	err := s.db.QueryRowContext(ctx, `SELECT ts, cpu, mem_used, mem_total, swap_used, disk_used, disk_total, net_rx, net_tx, load1 FROM metric_samples ORDER BY ts DESC LIMIT 1`).
		Scan(&m.TS, &m.CPU, &m.MemUsed, &m.MemTotal, &m.SwapUsed, &m.DiskUsed, &m.DiskTotal, &m.NetRx, &m.NetTx, &m.Load1)
	if err != nil {
		return nil, NormalizeErr(err)
	}
	return &m, nil
}

func (s *Store) PruneMetrics(ctx context.Context, before int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM metric_samples WHERE ts<?`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
