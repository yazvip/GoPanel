package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gopanel/internal/model"
)

type Runner struct {
	m   *Manager
	job *model.Job

	mu          sync.Mutex
	seq         int64
	output      []byte
	truncated   bool
	log         *os.File
	logBytes    int64
	logNoted    bool
	lastPersist time.Time
	persistMu   sync.Mutex
}

func newRunner(m *Manager, job *model.Job) *Runner {
	return &Runner{m: m, job: job, output: make([]byte, 0, 4096)}
}

func (r *Runner) JobID() string { return r.job.ID }

func (r *Runner) Payload() string { return r.job.Payload }

func (r *Runner) DecodePayload(dst any) error {
	if err := json.Unmarshal([]byte(r.job.Payload), dst); err != nil {
		return fmt.Errorf("decode job payload: %w", err)
	}
	return nil
}

func (r *Runner) begin() error {
	r.mu.Lock()
	r.job.Status = StatusRunning
	r.job.StartedAt = time.Now().Unix()
	r.seq++
	ev := r.eventLocked("update")
	file, err := r.m.openLog(r.job.ID)
	if err == nil {
		r.log = file
		r.writeLogLocked(fmt.Sprintf("GoPanel job %s (%s) started at %s\n", r.job.ID, r.job.Type, time.Now().Format(time.RFC3339)))
	}
	snapshot := r.snapshotLocked()
	r.mu.Unlock()
	r.m.publish(r.job.ID, ev)
	r.persistSnapshot(snapshot)
	return err
}

func (r *Runner) Progress(pct int, message string) {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	r.mu.Lock()
	if pct < r.job.Progress {
		pct = r.job.Progress
	}
	r.job.Progress = pct
	if message != "" {
		r.job.Message = message
	}
	r.seq++
	ev := r.eventLocked("update")
	snapshot := r.snapshotLocked()
	r.mu.Unlock()
	r.m.publish(r.job.ID, ev)
	r.persistSnapshot(snapshot)
}

func (r *Runner) Message(message string) {
	r.mu.Lock()
	if r.job.Message == message {
		r.mu.Unlock()
		return
	}
	r.job.Message = message
	r.seq++
	ev := r.eventLocked("update")
	snapshot := r.snapshotLocked()
	r.mu.Unlock()
	r.m.publish(r.job.ID, ev)
	r.persistSnapshot(snapshot)
}

func (r *Runner) Print(text string) {
	if text == "" {
		return
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	r.mu.Lock()
	r.appendOutputLocked(text)
	r.writeLogLocked(text)
	r.seq++
	ev := r.eventLocked("update")
	ev.Line = text
	snapshot := r.snapshotLocked()
	r.mu.Unlock()
	r.m.publish(r.job.ID, ev)
	r.persistSnapshot(snapshot)
}

func (r *Runner) Logf(format string, args ...any) {
	r.Print(fmt.Sprintf(format, args...))
}

func (r *Runner) Sink() func(string) {
	return func(s string) { r.Print(s) }
}

func (r *Runner) finish(err error) {
	r.mu.Lock()
	r.job.FinishedAt = time.Now().Unix()
	if err != nil {
		r.job.Status = StatusFailed
		r.job.Error = err.Error()
	} else {
		r.job.Status = StatusSuccess
		r.job.Progress = 100
		if r.job.Message == "" {
			r.job.Message = "Completed"
		}
	}
	r.seq++
	done := r.eventLocked("done")
	r.writeLogLocked(fmt.Sprintf("\n[job %s at %s]\n", r.job.Status, time.Now().Format(time.RFC3339)))
	if r.log != nil {
		r.log.Close()
		r.log = nil
	}
	snapshot := r.snapshotLocked()
	r.mu.Unlock()

	r.persistMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	uerr := r.m.st.UpdateJob(ctx, &snapshot)
	cancel()
	r.persistMu.Unlock()
	if uerr != nil {
		r.m.logger.Warn("persist finished job", "job", r.job.ID, "error", uerr)
	}
	r.m.publish(r.job.ID, done)
}

func (r *Runner) snapshotEvent() Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := string(r.output)
	if r.truncated {
		out = "[earlier output truncated]\n" + out
	}
	return Event{
		Seq:      r.seq,
		Type:     "snapshot",
		JobID:    r.job.ID,
		Status:   r.job.Status,
		Progress: r.job.Progress,
		Message:  r.job.Message,
		Output:   out,
		Error:    r.job.Error,
	}
}

func (r *Runner) snapshotLocked() model.Job {
	out := string(r.output)
	if r.truncated {
		out = "[earlier output truncated]\n" + out
	}
	snapshot := *r.job
	snapshot.Output = out
	return snapshot
}

func (r *Runner) eventLocked(kind string) Event {
	return Event{
		Seq:      r.seq,
		Type:     kind,
		JobID:    r.job.ID,
		Status:   r.job.Status,
		Progress: r.job.Progress,
		Message:  r.job.Message,
		Error:    r.job.Error,
	}
}

func (r *Runner) appendOutputLocked(text string) {
	r.output = append(r.output, text...)
	if len(r.output) <= maxOutputBytes {
		return
	}
	drop := len(r.output) - maxOutputBytes
	for drop < len(r.output) && !utf8.RuneStart(r.output[drop]) {
		drop++
	}
	r.output = append(r.output[:0], r.output[drop:]...)
	r.truncated = true
}

func (r *Runner) writeLogLocked(text string) {
	if r.log == nil {
		return
	}
	if r.logBytes >= maxLogBytes {
		if !r.logNoted {
			r.log.WriteString("\n[log truncated]\n")
			r.logNoted = true
		}
		return
	}
	if remaining := maxLogBytes - r.logBytes; int64(len(text)) > remaining {
		text = text[:remaining]
	}
	n, err := r.log.WriteString(text)
	r.logBytes += int64(n)
	if err != nil {
		r.m.logger.Warn("write job log", "job", r.job.ID, "error", err)
		r.log.Close()
		r.log = nil
	}
}

func (r *Runner) persistSnapshot(snapshot model.Job) {
	r.persistMu.Lock()
	defer r.persistMu.Unlock()
	r.mu.Lock()
	if terminal(r.job.Status) || time.Since(r.lastPersist) < time.Second {
		r.mu.Unlock()
		return
	}
	r.lastPersist = time.Now()
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.m.st.UpdateJob(ctx, &snapshot); err != nil {
		r.m.logger.Warn("persist job", "job", r.job.ID, "error", err)
	}
}
