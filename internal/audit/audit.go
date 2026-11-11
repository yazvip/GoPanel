package audit

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"gopanel/internal/model"
	"gopanel/internal/store"
)

const (
	ResultSuccess = "success"
	ResultDenied  = "denied"
	ResultFailed  = "failed"
)

type Event struct {
	UserID   int64
	Username string
	IP       string
	Action   string
	Target   string
	Detail   string
	Result   string
}

type Recorder struct {
	st     *store.Store
	logger *slog.Logger
}

func New(st *store.Store, logger *slog.Logger) *Recorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &Recorder{st: st, logger: logger}
}

func (r *Recorder) Record(ctx context.Context, e Event) {
	if r == nil || r.st == nil {
		return
	}
	if e.Result == "" {
		e.Result = ResultSuccess
	}
	entry := &model.AuditLog{
		UserID:   e.UserID,
		Username: clip(e.Username, 64),
		IP:       clip(e.IP, 64),
		Action:   clip(e.Action, 64),
		Target:   clip(e.Target, 255),
		Detail:   clip(e.Detail, 512),
		Result:   clip(e.Result, 16),
	}
	// Audit writes must survive the request context being cancelled.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := r.st.InsertAudit(writeCtx, entry); err != nil {
		r.logger.Warn("write audit log", "action", entry.Action, "error", err)
	}
}

func (r *Recorder) Success(ctx context.Context, e Event) {
	e.Result = ResultSuccess
	r.Record(ctx, e)
}

func (r *Recorder) Denied(ctx context.Context, e Event) {
	e.Result = ResultDenied
	r.Record(ctx, e)
}

func (r *Recorder) Failed(ctx context.Context, e Event) {
	e.Result = ResultFailed
	r.Record(ctx, e)
}

func clip(value string, max int) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
	if len(value) <= max {
		return value
	}
	for max > 0 && value[max]&0xC0 == 0x80 {
		max--
	}
	return value[:max]
}
