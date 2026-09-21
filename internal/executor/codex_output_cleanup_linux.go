//go:build linux

package executor

import (
	"errors"
	"os"
	"syscall"
)

func removeCodexFinalOutputEntry(dir *os.File, _ string) error {
	err := syscall.Unlinkat(int(dir.Fd()), "final.txt")
	if errors.Is(err, syscall.ENOENT) {
		return nil
	}
	return err
}
