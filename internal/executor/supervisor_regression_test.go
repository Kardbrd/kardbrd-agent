package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExternalSupervisorOtherExecutorsKeepVerboseTerminalOutput(t *testing.T) {
	for _, name := range []string{"claude", "goose", "pi"} {
		t.Run(name, func(t *testing.T) {
			dir := fakeBinary(t, name, `#!/usr/bin/env python3
import json, os, sys
sys.stdin.read()
for i in range(5000): print(json.dumps({'type':'tool_use','detail':'x'*1000}))
name=os.environ['SUPERVISOR_EXECUTOR_NAME']
if name=='claude': print(json.dumps({'type':'result','result':'c'*2000,'session_id':'synthetic'}))
elif name=='goose': print(json.dumps({'type':'AgentMessageChunk','content':'c'*2000}))
else: print(json.dumps({'type':'message_end','message':'c'*2000}))
`)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("SUPERVISOR_EXECUTOR_NAME", name)
			cfg := Config{CWD: t.TempDir(), Timeout: 10 * time.Second}

			var adapter interface {
				Execute(context.Context, Request) Result
			}
			switch name {
			case "claude":
				adapter = NewClaude(cfg)
			case "goose":
				adapter = NewGoose(cfg)
			case "pi":
				adapter = NewPi(cfg)
			}

			result := adapter.Execute(context.Background(), Request{Prompt: "synthetic", CardID: "card", BoardID: "board"})
			if !result.Success || result.ResultText != strings.Repeat("c", 2000) {
				t.Fatalf("verbose %s terminal result lost: success=%v final=%q error=%q", name, result.Success, result.ResultText, result.Error)
			}
		})
	}
}

func TestExternalSupervisorOtherExecutorsRetainTrailingProtocolFailure(t *testing.T) {
	for _, name := range []string{"claude", "goose", "pi"} {
		t.Run(name, func(t *testing.T) {
			dir := fakeBinary(t, name, `#!/usr/bin/env python3
import json, os, sys
sys.stdin.read()
for i in range(5000): print(json.dumps({'type':'tool_use','detail':'x'*1000}))
name=os.environ['SUPERVISOR_EXECUTOR_NAME']
if name=='claude': print(json.dumps({'type':'error','error':{'message':'trailing failure'}}))
else: print(json.dumps({'type':'error','message':'trailing failure'}))
`)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("SUPERVISOR_EXECUTOR_NAME", name)
			cfg := Config{CWD: t.TempDir(), Timeout: 10 * time.Second}

			var adapter interface {
				Execute(context.Context, Request) Result
			}
			switch name {
			case "claude":
				adapter = NewClaude(cfg)
			case "goose":
				adapter = NewGoose(cfg)
			case "pi":
				adapter = NewPi(cfg)
			}

			result := adapter.Execute(context.Background(), Request{Prompt: "synthetic", CardID: "card", BoardID: "board"})
			if result.Success || result.Error != "trailing failure" {
				t.Fatalf("verbose %s trailing failure was lost: success=%v error=%q", name, result.Success, result.Error)
			}
		})
	}
}

func TestExternalSupervisorCancellationAtParentExit(t *testing.T) {
	cwd := t.TempDir()
	for i := 0; i < 300; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		timer := time.AfterFunc(time.Duration(200+(i%20)*75)*time.Microsecond, cancel)
		_, _, _, _ = runCommand(ctx, Config{Timeout: time.Second}, cwd, []string{"/bin/sh", "-c", "sleep 0.001"}, "", "card", "board", "timeout", nil)
		timer.Stop()
		cancel()
	}
}

func TestExternalSupervisorCancellationCleansDescendant(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires /proc descendant verification")
	}
	dir := fakeBinary(t, "cancel-parent-exit", `#!/bin/sh
sleep 30 &
printf '%s' "$!" > "$SUPERVISOR_CHILD_PID"
printf ready > "$SUPERVISOR_PARENT_READY"
while [ ! -f "$SUPERVISOR_PARENT_RELEASE" ]; do sleep 0.001; done
printf armed > "$SUPERVISOR_PARENT_ARMED"
sleep 30
`)
	pidPath := filepath.Join(t.TempDir(), "child-pid")
	readyPath := filepath.Join(t.TempDir(), "parent-ready")
	releasePath := filepath.Join(t.TempDir(), "parent-release")
	armedPath := filepath.Join(t.TempDir(), "parent-armed")
	t.Setenv("SUPERVISOR_CHILD_PID", pidPath)
	t.Setenv("SUPERVISOR_PARENT_READY", readyPath)
	t.Setenv("SUPERVISOR_PARENT_RELEASE", releasePath)
	t.Setenv("SUPERVISOR_PARENT_ARMED", armedPath)
	defer cleanupSupervisorChild(pidPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, _, err := runCommand(ctx, Config{Timeout: time.Second}, t.TempDir(), []string{filepath.Join(dir, "cancel-parent-exit")}, "prompt", "card", "board", "timeout", nil)
		done <- err
	}()
	waitForSupervisorChildPID(t, pidPath)
	waitForSupervisorFile(t, readyPath, "parent ready marker")
	if err := os.WriteFile(releasePath, []byte("release"), 0o600); err != nil {
		t.Fatalf("release synthetic parent: %v", err)
	}
	waitForSupervisorFile(t, armedPath, "parent armed marker")
	// The fixture waits after acknowledging release, so cancellation is
	// guaranteed to exercise cleanup while its descendant is still in the
	// process group. The companion race test drives the tighter parent-exit
	// Wait/Cancel interleaving.
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled command error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled parent did not return promptly")
	}
	assertSupervisorChildStopped(t, pidPath)
}

func waitForSupervisorChildPID(t *testing.T, pidPath string) {
	t.Helper()
	waitForSupervisorFile(t, pidPath, "descendant PID")
}

func waitForSupervisorFile(t *testing.T, path, description string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture did not create %s at %q", description, path)
		}
		time.Sleep(time.Millisecond)
	}
}
