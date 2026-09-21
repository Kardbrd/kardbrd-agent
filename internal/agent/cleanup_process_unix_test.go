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
	registerCleanupChildKill(t, childOutput)
	pid := waitForCleanupChildPID(t, childOutput, 5*time.Second)
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

func TestDoneCleanupOwnershipLossKillsForkedDescendants(t *testing.T) {
	manager := newTestManager(t)
	manager.Timeout = 5 * time.Second
	outputFile := filepath.Join(t.TempDir(), "cleanup-output")
	manager.Client.(*fakeBoardClient).card = rawJSON(t, map[string]any{"list": map[string]any{"name": "Done"}})
	manager.Rules = &rules.Engine{Rules: []rules.Rule{doneCleanupRule(outputFile, "fork")}}
	started := make(chan struct{})
	releaseStart := make(chan struct{})
	manager.cleanupCommandStarted = func() {
		close(started)
		<-releaseStart
	}

	done := make(chan error, 1)
	go func() { done <- manager.HandleBoardEvent(context.Background(), doneCardMovedEvent("card1")) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("cleanup command did not start")
	}
	childOutput := outputFile + ".child"
	registerCleanupChildKill(t, childOutput)
	pid := waitForCleanupChildPID(t, childOutput, 5*time.Second)

	manager.mu.Lock()
	manager.Active["card1"] = &ActiveSession{CardID: "card1"}
	manager.mu.Unlock()
	close(releaseStart)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not finish after losing ownership")
	}
	assertCleanupProcessGone(t, pid)
}

func registerCleanupChildKill(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(func() {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
}

func waitForCleanupChildPID(t *testing.T, path string, timeout time.Duration) int {
	t.Helper()
	deadline := time.After(timeout)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			if pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data))); parseErr == nil && pid > 0 {
				return pid
			}
		}
		select {
		case <-deadline:
			t.Fatalf("cleanup child PID file %q was not populated", path)
		case <-ticker.C:
		}
	}
}

func assertCleanupProcessGone(t *testing.T, pid int) {
	t.Helper()
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
			t.Fatal("forked cleanup descendant survived cancellation")
		case <-ticker.C:
		}
	}
}
