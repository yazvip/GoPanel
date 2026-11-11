package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopanel/internal/model"
	"gopanel/internal/store"
)

const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"
)

const (
	maxOutputBytes = 256 << 10
	maxLogBytes    = 8 << 20
	maxRingEvents  = 256
	subBuffer      = 256
)

var (
	ErrQueueFull   = errors.New("job queue is full")
	ErrUnknownType = errors.New("unknown job type")
	ErrInvalidJob  = errors.New("invalid job id")
	ErrDuplicate   = errors.New("job type already registered")
)

type Handler func(ctx context.Context, r *Runner) error

type registration struct {
	handler Handler
	sem     chan struct{}
}

type task struct {
	job     *model.Job
	handler Handler
}

type Event struct {
	Seq      int64  `json:"seq"`
	Type     string `json:"type"`
	JobID    string `json:"job_id"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Message  string `json:"message,omitempty"`
	Line     string `json:"line,omitempty"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
}

type ring struct {
	events   []Event
	subs     map[chan Event]struct{}
	finished bool
	state    Event
}

type Manager struct {
	st     *store.Store
	logger *slog.Logger
	logDir string
	queue  chan *task

	mu      sync.Mutex
	types   map[string]*registration
	rings   map[string]*ring
	runners map[string]*Runner

	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	workers int
}

func New(st *store.Store, logDir string, workers int, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	if workers <= 0 {
		workers = 3
	}
	if workers > 8 {
		workers = 8
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		st:      st,
		logger:  logger,
		logDir:  logDir,
		queue:   make(chan *task, 128),
		types:   map[string]*registration{},
		rings:   map[string]*ring{},
		runners: map[string]*Runner{},
		ctx:     ctx,
		cancel:  cancel,
	}
	m.workers = workers
	return m
}

func (m *Manager) Register(jobType string, concurrency int, handler Handler) error {
	if jobType == "" || handler == nil {
		return fmt.Errorf("invalid job registration")
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.types[jobType]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicate, jobType)
	}
	m.types[jobType] = &registration{handler: handler, sem: make(chan struct{}, concurrency)}
	return nil
}

func (m *Manager) Start() {
	m.wg.Add(m.workers)
	for i := 0; i < m.workers; i++ {
		go m.worker()
	}
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.cancel()
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	for {
		select {
		case t := <-m.queue:
			m.failQueued(t.job, "panel stopped before the job started")
		default:
			return nil
		}
	}
}

func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case t := <-m.queue:
			m.run(t)
		}
	}
}

func (m *Manager) Enqueue(ctx context.Context, jobType string, payload any, createdBy int64) (*model.Job, error) {
	m.mu.Lock()
	reg, ok := m.types[jobType]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownType, jobType)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode job payload: %w", err)
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	job := &model.Job{
		ID:        id,
		Type:      jobType,
		Status:    StatusQueued,
		Payload:   string(raw),
		CreatedBy: createdBy,
		CreatedAt: time.Now().Unix(),
	}
	if err := m.st.CreateJob(ctx, job); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.rings[id] = &ring{
		subs:  map[chan Event]struct{}{},
		state: Event{JobID: id, Status: StatusQueued},
	}
	m.mu.Unlock()
	select {
	case m.queue <- &task{job: job, handler: reg.handler}:
		return job, nil
	default:
		m.failQueued(job, "job queue is full")
		return nil, ErrQueueFull
	}
}

func (m *Manager) failQueued(job *model.Job, reason string) {
	job.Status = StatusFailed
	job.Error = reason
	job.FinishedAt = time.Now().Unix()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.st.UpdateJob(ctx, job); err != nil {
		m.logger.Warn("persist failed job", "job", job.ID, "error", err)
	}
	m.mu.Lock()
	if r, ok := m.rings[job.ID]; ok {
		r.finished = true
		ev := Event{Type: "done", JobID: job.ID, Status: StatusFailed, Error: reason}
		ev.Seq = r.state.Seq + 1
		r.events = appendEvent(r.events, ev)
		r.state = ev
		m.broadcastLocked(r, ev)
		m.closeSubsLocked(r)
	}
	m.mu.Unlock()
	time.AfterFunc(10*time.Minute, func() {
		m.mu.Lock()
		delete(m.rings, job.ID)
		m.mu.Unlock()
	})
}

func (m *Manager) run(t *task) {
	m.mu.Lock()
	reg := m.types[t.job.Type]
	m.mu.Unlock()
	if reg == nil {
		m.failQueued(t.job, "unknown job type")
		return
	}
	select {
	case reg.sem <- struct{}{}:
		defer func() { <-reg.sem }()
	case <-m.ctx.Done():
		m.failQueued(t.job, "panel stopped before the job started")
		return
	}
	r := newRunner(m, t.job)
	m.mu.Lock()
	m.runners[t.job.ID] = r
	m.mu.Unlock()
	if err := r.begin(); err != nil {
		m.logger.Warn("start job log", "job", t.job.ID, "error", err)
	}
	err := m.execute(t.handler, r)
	r.finish(err)
	m.mu.Lock()
	if ring, ok := m.rings[t.job.ID]; ok {
		ring.finished = true
	}
	m.mu.Unlock()
	time.AfterFunc(10*time.Minute, func() {
		m.mu.Lock()
		delete(m.rings, t.job.ID)
		delete(m.runners, t.job.ID)
		m.mu.Unlock()
	})
}

func (m *Manager) execute(handler Handler, r *Runner) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("job panicked: %v", rec)
		}
	}()
	runCtx, cancel := context.WithCancel(m.ctx)
	defer cancel()
	return handler(runCtx, r)
}

func (m *Manager) publish(jobID string, ev Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rings[jobID]
	if !ok {
		return
	}
	if ev.Seq == 0 {
		ev.Seq = r.state.Seq + 1
	}
	if ev.Seq <= r.state.Seq && ev.Type != "done" {
		ev.Seq = r.state.Seq + 1
	}
	r.state = ev
	r.events = appendEvent(r.events, ev)
	if ev.Type == "done" {
		r.finished = true
	}
	m.broadcastLocked(r, ev)
	if ev.Type == "done" {
		m.closeSubsLocked(r)
	}
}

func (m *Manager) closeSubsLocked(r *ring) {
	for ch := range r.subs {
		close(ch)
		delete(r.subs, ch)
	}
}

func appendEvent(list []Event, ev Event) []Event {
	if len(list) >= maxRingEvents {
		copy(list, list[len(list)-maxRingEvents+1:])
		list = list[:maxRingEvents-1]
	}
	return append(list, ev)
}

func (m *Manager) broadcastLocked(r *ring, ev Event) {
	for ch := range r.subs {
		select {
		case ch <- ev:
		default:
			// A stalled reader: drop it. EventSource reconnects with
			// Last-Event-ID and receives a full snapshot instead.
			close(ch)
			delete(r.subs, ch)
		}
	}
}

func (m *Manager) Subscribe(ctx context.Context, jobID string, lastID int64) (<-chan Event, func(), error) {
	if jobID == "" || len(jobID) > 64 || strings.ContainsAny(jobID, "/\\") {
		return nil, nil, ErrInvalidJob
	}
	m.mu.Lock()
	if r, ok := m.rings[jobID]; ok {
		ch, cancel := m.subscribeLocked(jobID, r, lastID)
		m.mu.Unlock()
		return ch, cancel, nil
	}
	m.mu.Unlock()

	job, err := m.st.GetJob(ctx, jobID)
	if err != nil {
		return nil, nil, err
	}
	snapshot := Event{
		Type:     "snapshot",
		JobID:    job.ID,
		Status:   job.Status,
		Progress: job.Progress,
		Message:  job.Message,
		Output:   job.Output,
		Error:    job.Error,
	}
	ch := make(chan Event, 16)
	ch <- snapshot
	if terminal(job.Status) {
		ch <- Event{Type: "done", JobID: job.ID, Status: job.Status, Progress: job.Progress, Error: job.Error}
		close(ch)
		return ch, func() {}, nil
	}
	m.mu.Lock()
	r, ok := m.rings[jobID]
	if !ok {
		r = &ring{subs: map[chan Event]struct{}{}, state: snapshot}
		m.rings[jobID] = r
	}
	if r.finished {
		close(ch)
		m.mu.Unlock()
		return ch, func() {}, nil
	}
	r.subs[ch] = struct{}{}
	m.mu.Unlock()
	return ch, func() { m.removeSub(r, ch) }, nil
}

func (m *Manager) subscribeLocked(jobID string, r *ring, lastID int64) (<-chan Event, func()) {
	replay := lastID > 0 && len(r.events) > 0 && r.events[0].Seq <= lastID+1
	if r.finished {
		ch := make(chan Event, maxRingEvents+2)
		if replay {
			for _, ev := range r.events {
				if ev.Seq > lastID {
					ch <- ev
				}
			}
		} else {
			snap := m.snapshotLocked(jobID, r)
			ch <- snap
			ch <- Event{Seq: snap.Seq, Type: "done", JobID: jobID, Status: snap.Status, Progress: snap.Progress, Message: snap.Message, Error: snap.Error}
		}
		close(ch)
		return ch, func() {}
	}
	if replay {
		ch := make(chan Event, len(r.events)+subBuffer)
		for _, ev := range r.events {
			if ev.Seq > lastID {
				ch <- ev
			}
		}
		r.subs[ch] = struct{}{}
		return ch, func() { m.removeSub(r, ch) }
	}
	ch := make(chan Event, subBuffer)
	ch <- m.snapshotLocked(jobID, r)
	r.subs[ch] = struct{}{}
	return ch, func() { m.removeSub(r, ch) }
}

func (m *Manager) snapshotLocked(jobID string, r *ring) Event {
	if run, ok := m.runners[jobID]; ok {
		return run.snapshotEvent()
	}
	ev := r.state
	ev.Type = "snapshot"
	ev.Line = ""
	if ev.Status == "" {
		ev.Status = StatusQueued
	}
	return ev
}

func (m *Manager) removeSub(r *ring, ch chan Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := r.subs[ch]; ok {
		delete(r.subs, ch)
		close(ch)
	}
}

func terminal(status string) bool {
	return status == StatusSuccess || status == StatusFailed
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (m *Manager) logPath(id string) string {
	return filepath.Join(m.logDir, "jobs", id+".log")
}

func (m *Manager) openLog(id string) (*os.File, error) {
	dir := filepath.Join(m.logDir, "jobs")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, id+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
}
