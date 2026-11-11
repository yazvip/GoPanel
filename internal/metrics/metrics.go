package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gopanel/internal/model"
	"gopanel/internal/store"
	"gopanel/internal/sys"
)

type Options struct {
	Interval  time.Duration
	Retention time.Duration
}

type Service struct {
	st     *store.Store
	sys    sys.System
	logger *slog.Logger
	opts   Options

	mu     sync.Mutex
	last   sys.Sample
	lastAt time.Time
	rateRx float64
	rateTx float64
}

type Snapshot struct {
	sys.Sample
	NetRxRate float64 `json:"net_rx_rate"`
	NetTxRate float64 `json:"net_tx_rate"`
}

type Overview struct {
	Live Snapshot     `json:"live"`
	Host sys.HostInfo `json:"host"`
}

var ErrRange = errors.New("invalid time range")

func New(st *store.Store, system sys.System, logger *slog.Logger, opts Options) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Second
	}
	if opts.Retention <= 0 {
		opts.Retention = 7 * 24 * time.Hour
	}
	return &Service{st: st, sys: system, logger: logger, opts: opts}
}

func (s *Service) Start(ctx context.Context) {
	go s.loop(ctx)
}

func (s *Service) loop(ctx context.Context) {
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()
	pruneEvery := time.NewTicker(20 * time.Minute)
	defer pruneEvery.Stop()
	s.collect(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.collect(ctx)
		case <-pruneEvery.C:
			s.prune(ctx)
		}
	}
}

func (s *Service) collect(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	sample, err := s.sys.Snapshot(ctx)
	if err != nil {
		s.logger.Warn("metrics snapshot failed", "error", err)
		return
	}
	if sample.TS == 0 {
		sample.TS = time.Now().Unix()
	}
	s.track(sample)
	m := model.MetricSample{
		TS:        sample.TS,
		CPU:       sample.CPU,
		MemUsed:   sample.MemUsed,
		MemTotal:  sample.MemTotal,
		SwapUsed:  sample.SwapUsed,
		DiskUsed:  sample.DiskUsed,
		DiskTotal: sample.DiskTotal,
		NetRx:     sample.NetRx,
		NetTx:     sample.NetTx,
		Load1:     sample.Load1,
	}
	if err := s.st.InsertSample(ctx, m); err != nil {
		s.logger.Warn("store metric sample", "error", err)
	}
}

func (s *Service) prune(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	before := time.Now().Add(-s.opts.Retention).Unix()
	if _, err := s.st.PruneMetrics(ctx, before); err != nil {
		s.logger.Warn("prune metrics", "error", err)
	}
}

func (s *Service) track(sample sys.Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := time.Unix(sample.TS, 0)
	if !s.lastAt.IsZero() && sample.TS > s.last.TS {
		elapsed := at.Sub(s.lastAt).Seconds()
		if elapsed > 0 && elapsed <= 300 {
			if sample.NetRx >= s.last.NetRx {
				s.rateRx = float64(sample.NetRx-s.last.NetRx) / elapsed
			}
			if sample.NetTx >= s.last.NetTx {
				s.rateTx = float64(sample.NetTx-s.last.NetTx) / elapsed
			}
		}
	}
	s.last = sample
	s.lastAt = at
}

func (s *Service) Snapshot(ctx context.Context) (*Snapshot, error) {
	sample, err := s.sys.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if sample.TS == 0 {
		sample.TS = time.Now().Unix()
	}
	s.track(sample)
	s.mu.Lock()
	rx, tx := s.rateRx, s.rateTx
	s.mu.Unlock()
	return &Snapshot{Sample: sample, NetRxRate: rx, NetTxRate: tx}, nil
}

func (s *Service) Overview(ctx context.Context) (*Overview, error) {
	live, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	host, err := s.sys.HostInfo(ctx)
	if err != nil {
		return nil, err
	}
	return &Overview{Live: *live, Host: host}, nil
}

type window struct {
	duration time.Duration
	bucket   int64
}

var windows = map[string]window{
	"1h":  {time.Hour, 60},
	"6h":  {6 * time.Hour, 300},
	"24h": {24 * time.Hour, 900},
	"7d":  {7 * 24 * time.Hour, 3600},
}

func (s *Service) History(ctx context.Context, rng string) ([]model.MetricSample, error) {
	w, ok := windows[rng]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrRange, rng)
	}
	now := time.Now().Unix()
	return s.st.QueryMetrics(ctx, now-int64(w.duration.Seconds()), now, w.bucket)
}

func (s *Service) Latest(ctx context.Context) (*model.MetricSample, error) {
	return s.st.LatestSample(ctx)
}

func (s *Service) Prune(ctx context.Context) error {
	s.prune(ctx)
	return nil
}
