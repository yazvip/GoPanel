//go:build !unix && !windows

package execx

import "os/exec"

func configureProcess(cmd *exec.Cmd) {}

func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
