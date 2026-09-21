//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package worktree

import "os/exec"

func configureLifecycleProcessGroup(_ *exec.Cmd) {}

func terminateLifecycleProcessGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
