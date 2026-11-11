package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopanel/internal/sys"
)

var allowed = map[string]bool{
	"systemctl":  true,
	"nginx":      true,
	"mysql":      true,
	"mysqldump":  true,
	"apt-get":    true,
	"dpkg-query": true,
	"ufw":        true,
	"openssl":    true,
	"crontab":    true,
	"bash":       true,
	"journalctl": true,
}

type Runner struct {
	DefaultTimeout time.Duration
	MaxOutput      int
}

func New() *Runner {
	return &Runner{DefaultTimeout: 30 * time.Second, MaxOutput: 1 << 20}
}

func validate(name string, args []string) error {
	base := filepath.Base(name)
	if !allowed[base] {
		return fmt.Errorf("command %q is not allowed", base)
	}
	if strings.ContainsRune(name, 0) {
		return errors.New("invalid command")
	}
	for _, a := range args {
		if strings.ContainsRune(a, 0) {
			return errors.New("argument contains NUL byte")
		}
	}
	return nil
}

func (r *Runner) outputLimit(opts sys.ExecOpts) int {
	if opts.MaxOutput < 0 {
		return -1
	}
	if opts.MaxOutput > 0 {
		return opts.MaxOutput
	}
	return r.MaxOutput
}

func (r *Runner) timeout(opts sys.ExecOpts) time.Duration {
	if opts.Timeout != 0 {
		return opts.Timeout
	}
	return r.DefaultTimeout
}

func (r *Runner) command(ctx context.Context, name string, args []string, opts sys.ExecOpts) (*exec.Cmd, context.CancelFunc, error) {
	if err := validate(name, args); err != nil {
		return nil, nil, err
	}
	timeout := r.timeout(opts)
	runCtx := ctx
	cancel := context.CancelFunc(func() {})
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	cmd := exec.CommandContext(runCtx, name, args...)
	configureProcess(cmd)
	cmd.Cancel = func() error {
		killProcessTree(cmd)
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = buildEnv(opts.Env)
	cmd.Dir = opts.Dir
	if opts.StdinReader != nil {
		cmd.Stdin = opts.StdinReader
	} else if len(opts.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(opts.Stdin)
	}
	return cmd, cancel, nil
}

func (r *Runner) Run(ctx context.Context, name string, args []string, opts sys.ExecOpts) (sys.Result, error) {
	return r.RunIO(ctx, name, args, opts, nil, nil)
}

func (r *Runner) RunIO(ctx context.Context, name string, args []string, opts sys.ExecOpts, stdout, stderr io.Writer) (sys.Result, error) {
	var res sys.Result
	cmd, cancel, err := r.command(ctx, name, args, opts)
	if err != nil {
		return res, err
	}
	defer cancel()
	limit := r.outputLimit(opts)
	var outBuf, errBuf *limitedBuffer
	if stdout == nil {
		outBuf = newLimitedBuffer(limit)
		cmd.Stdout = outBuf
	} else {
		cmd.Stdout = stdout
	}
	errBuf = newLimitedBuffer(limit)
	if stderr != nil {
		cmd.Stderr = io.MultiWriter(errBuf, stderr)
	} else {
		cmd.Stderr = errBuf
	}
	waitErr := cmd.Wait()
	if outBuf != nil {
		res.Stdout = outBuf.String()
	}
	res.Stderr = errBuf.String()
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = -1
		}
		execErr := &sys.ExecError{Name: name, Args: args, Result: res, Err: waitErr}
		if ctx.Err() != nil {
			execErr.Err = ctx.Err()
		} else if errors.Is(waitErr, context.DeadlineExceeded) {
			execErr.Err = context.DeadlineExceeded
		}
		return res, execErr
	}
	return res, nil
}

func (r *Runner) Stream(ctx context.Context, name string, args []string, opts sys.ExecOpts, sink func(string)) (sys.Result, error) {
	var mu sync.Mutex
	outLW := &lineWriter{mu: &mu, sink: sink}
	errLW := &lineWriter{mu: &mu, sink: sink}
	outBuf := newLimitedBuffer(r.outputLimit(opts))
	errBuf := newLimitedBuffer(r.outputLimit(opts))
	res, err := r.RunIO(ctx, name, args, opts, io.MultiWriter(outBuf, outLW), io.MultiWriter(errBuf, errLW))
	outLW.Flush()
	errLW.Flush()
	outLW.finish()
	errLW.finish()
	res.Stdout = outBuf.String()
	res.Stderr = errBuf.String()
	return res, err
}

type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func newLimitedBuffer(max int) *limitedBuffer {
	return &limitedBuffer{max: max}
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	if w.max >= 0 {
		remaining := w.max - w.buf.Len()
		if remaining <= 0 {
			w.truncated = true
			return n, nil
		}
		if len(p) > remaining {
			p = p[:remaining]
			w.truncated = true
		}
	}
	w.buf.Write(p)
	return n, nil
}

func (w *limitedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.buf.String()
	if w.truncated {
		s += "\n[output truncated]\n"
	}
	return s
}

type lineWriter struct {
	mu   *sync.Mutex
	sink func(string)
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		if w.sink != nil {
			w.sink(strings.TrimRight(line, "\r"))
		}
	}
	if len(w.buf) > 32*1024 {
		if w.sink != nil {
			w.sink(string(w.buf))
		}
		w.buf = nil
	}
	return len(p), nil
}

func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 && w.sink != nil {
		w.sink(strings.TrimRight(string(w.buf), "\r\n"))
	}
	w.buf = nil
}

func (w *lineWriter) finish() {}

func buildEnv(extra []string) []string {
	values := map[string]string{
		"PATH":              "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"LANG":              "C.UTF-8",
		"LC_ALL":            "C.UTF-8",
		"HOME":              "/root",
		"DEBIAN_FRONTEND":   "noninteractive",
		"APT_LISTCHANGES_FRONTEND": "none",
	}
	for _, kv := range extra {
		if k, v, ok := strings.Cut(kv, "="); ok {
			values[k] = v
		}
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+values[k])
	}
	return out
}
