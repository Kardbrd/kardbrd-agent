//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

func TestStoppedDoneCleanupKillsForkedDescendants(t *testing.T) {
	manager := newTestManager(t)
	manager.Timeout = 5 * time.Second
	outputFile := filepath.Join(t.TempDir(), "cleanup-output")
	manager.Client.(*fakeBoardClient).card = rawJSON(t, map[string]any{"list": map[string]any{"name": "Done"}})
	manager.Rules = &rules.Engine{Rules: []rules.Rule{doneCleanupRule(outputFile, "fork")}}

	done := make(chan error, 1)
	go func() { done <- manager.HandleBoardEvent(context.Background(), doneCardMovedEvent("card1")) }()
	childOutput := outputFile + ".child"
	waitForFileWithin(t, childOutput, 5*time.Second)
	pid, err := strconv.Atoi(strings.TrimSpace(readFile(t, childOutput)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()
	if err := manager.HandleStopReaction(context.Background(), "card1", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not stop")
	}
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			if os.IsNotExist(err) || err == syscall.ESRCH {
				return
			}
			t.Fatal(err)
		}
		select {
		case <-deadline:
			t.Fatal("forked cleanup descendant survived timeout")
		case <-ticker.C:
		}
	}
}
