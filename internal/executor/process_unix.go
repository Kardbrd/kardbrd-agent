//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package executor

import (
	"os/exec"
	"syscall"
)

func configureExecutorProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killExecutorProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
