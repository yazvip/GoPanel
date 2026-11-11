//go:build linux

package linux

import (
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"

	"gopanel/internal/sys"
)

type ptySession struct {
	file *os.File
	cmd  *exec.Cmd
	once sync.Once
}

func (s *System) startPTY(cols, rows uint16) (sys.TerminalSession, error) {
	cmd := exec.Command("/bin/bash", "-l")
	cmd.Env = []string{
		"TERM=xterm-256color",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/root",
		"USER=root",
		"LOGNAME=root",
		"LANG=C.UTF-8",
	}
	if cols == 0 {
		cols = 120
	}
	if rows == 0 {
		rows = 32
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return &ptySession{file: f, cmd: cmd}, nil
}

func (p *ptySession) Read(b []byte) (int, error)  { return p.file.Read(b) }
func (p *ptySession) Write(b []byte) (int, error) { return p.file.Write(b) }

func (p *ptySession) Resize(cols, rows uint16) error {
	return pty.Setsize(p.file, &pty.Winsize{Cols: cols, Rows: rows})
}

func (p *ptySession) Close() error {
	var err error
	p.once.Do(func() {
		if p.cmd.Process != nil {
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGHUP)
			_ = p.cmd.Process.Signal(syscall.SIGHUP)
		}
		err = p.file.Close()
	})
	return err
}

func (p *ptySession) Wait() error {
	return p.cmd.Wait()
}
