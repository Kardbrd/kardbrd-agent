package executor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testCommandTimeout = 5 * time.Second

func TestClaudeExecutorCommandEnvAndResume(t *testing.T) {
	dir := fakeBinary(t, "claude", `#!/bin/sh
cat >/dev/null
printf '%s\n' "$@" > "$FAKE_ARGS"
printf '%s\n' "$KARDBRD_TOKEN|$KARDBRD_API_URL|$KARDBRD_CARD_ID|$KARDBRD_BOARD_ID" > "$FAKE_ENV"
printf '{"type":"result","result":"ok","session_id":"s1"}\n'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argsFile := filepath.Join(t.TempDir(), "args")
	envFile := filepath.Join(t.TempDir(), "env")
	t.Setenv("FAKE_ARGS", argsFile)
	t.Setenv("FAKE_ENV", envFile)
	t.Setenv("KARDBRD_CARD_ID", "stale-card")
	t.Setenv("KARDBRD_BOARD_ID", "stale-board")

	exec := NewClaude(Config{CWD: t.TempDir(), Timeout: testCommandTimeout, APIURL: "https://api.test", Token: "tok"})
	result := exec.Execute(context.Background(), Request{Prompt: "hello", ResumeSessionID: "resume1", CardID: "card1", BoardID: "board1"})
	assertEqual(t, true, result.Success)

	args := readFile(t, argsFile)
	assertContains(t, args, "-p\n-\n")
	assertContains(t, args, "--output-format=stream-json")
	assertContains(t, args, "--resume\nresume1")
	assertEqual(t, "tok|https://api.test|card1|board1\n", readFile(t, envFile))
}

func TestCodexExecutorCommand(t *testing.T) {
	dir := fakeBinary(t, "codex", `#!/bin/sh
cat >/dev/null
output_path=""
expect_output_path=0
for arg in "$@"; do
  if [ "$expect_output_path" = 1 ]; then
    output_path="$arg"
    expect_output_path=0
  elif [ "$arg" = "--output-last-message" ]; then
    expect_output_path=1
  fi
done
printf '%s\n' "$@" > "$FAKE_ARGS"
printf '%s\n' "$KARDBRD_CARD_ID|$KARDBRD_BOARD_ID" > "$FAKE_ENV"
printf 'ok' > "$output_path"
printf '{"type":"item.message","content":"ok"}\n'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argsFile := filepath.Join(t.TempDir(), "args")
	envFile := filepath.Join(t.TempDir(), "env")
	t.Setenv("FAKE_ARGS", argsFile)
	t.Setenv("FAKE_ENV", envFile)
	t.Setenv("KARDBRD_CARD_ID", "stale-card")
	t.Setenv("KARDBRD_BOARD_ID", "stale-board")

	exec := NewCodex(Config{CWD: t.TempDir(), Timeout: testCommandTimeout})
	result := exec.Execute(context.Background(), Request{Prompt: "hello", Model: "gpt-5.4", CardID: "card2", BoardID: "board2"})
	assertEqual(t, true, result.Success)
	args := readFile(t, argsFile)
	assertContains(t, args, "exec\n")
	assertContains(t, args, "--dangerously-bypass-approvals-and-sandbox")
	assertContains(t, args, "--json")
	assertContains(t, args, "--model\ngpt-5.4")
	assertEqual(t, "card2|board2\n", readFile(t, envFile))
}

func TestCodexExecutorUsesFinalMessageFileForTerminalResult(t *testing.T) {
	dir := fakeCodexBinary(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argsFile := filepath.Join(t.TempDir(), "args")
	promptFile := filepath.Join(t.TempDir(), "prompt")
	cwdFile := filepath.Join(t.TempDir(), "cwd")
	envFile := filepath.Join(t.TempDir(), "env")
	outputPathFile := filepath.Join(t.TempDir(), "output-path")
	t.Setenv("FAKE_ARGS", argsFile)
	t.Setenv("FAKE_PROMPT", promptFile)
	t.Setenv("FAKE_CWD", cwdFile)
	t.Setenv("FAKE_ENV", envFile)
	t.Setenv("FAKE_OUTPUT_PATH", outputPathFile)
	t.Setenv("FAKE_FINAL_MODE", "final")
	cwd := t.TempDir()

	var chunks []string
	exec := NewCodex(Config{CWD: cwd, Timeout: testCommandTimeout, APIURL: "https://api.test", Token: "tok"})
	result := exec.Execute(context.Background(), Request{
		Prompt:  "fresh prompt",
		Model:   "gpt-5.4",
		CardID:  "card2",
		BoardID: "board2",
		OnChunk: func(content string, chunkType string) { chunks = append(chunks, chunkType+":"+content) },
	})

	assertEqual(t, true, result.Success)
	assertEqual(t, "authoritative final response", result.ResultText)
	outputPath := strings.TrimSpace(readFile(t, outputPathFile))
	if outputPath == "" {
		t.Fatal("Codex command did not receive an output-last-message path")
	}
	assertEqual(t, "exec\n--dangerously-bypass-approvals-and-sandbox\n--json\n--output-last-message\n"+outputPath+"\n--model\ngpt-5.4\n", readFile(t, argsFile))
	assertEqual(t, "fresh prompt", readFile(t, promptFile))
	assertEqual(t, cwd+"\n", readFile(t, cwdFile))
	assertEqual(t, "tok|https://api.test|card2|board2\n", readFile(t, envFile))
	assertEqual(t, "assistant:working", chunks[0])
	assertEqual(t, "assistant:final JSONL message", chunks[1])
	assertEqual(t, 2, len(chunks))
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("final-message file remains after execution: %q, err=%v", outputPath, err)
	}
}

func TestCodexExecutorResumesExplicitSessionInsteadOfStartingFresh(t *testing.T) {
	dir := fakeCodexBinary(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argsFile := filepath.Join(t.TempDir(), "args")
	promptFile := filepath.Join(t.TempDir(), "prompt")
	cwdFile := filepath.Join(t.TempDir(), "cwd")
	outputPathFile := filepath.Join(t.TempDir(), "output-path")
	t.Setenv("FAKE_ARGS", argsFile)
	t.Setenv("FAKE_PROMPT", promptFile)
	t.Setenv("FAKE_CWD", cwdFile)
	t.Setenv("FAKE_ENV", filepath.Join(t.TempDir(), "env"))
	t.Setenv("FAKE_OUTPUT_PATH", outputPathFile)
	t.Setenv("FAKE_FINAL_MODE", "final")
	cwd := t.TempDir()

	exec := NewCodex(Config{CWD: cwd, Timeout: testCommandTimeout})
	result := exec.Execute(context.Background(), Request{
		Prompt:          "resume prompt",
		ResumeSessionID: "-thread-123",
		Model:           "gpt-5.4",
		CardID:          "card2",
		BoardID:         "board2",
	})

	assertEqual(t, true, result.Success)
	assertEqual(t, "authoritative final response", result.ResultText)
	outputPath := strings.TrimSpace(readFile(t, outputPathFile))
	assertEqual(t, "exec\nresume\n--dangerously-bypass-approvals-and-sandbox\n--json\n--output-last-message\n"+outputPath+"\n--model\ngpt-5.4\n--\n-thread-123\n", readFile(t, argsFile))
	assertEqual(t, "resume prompt", readFile(t, promptFile))
	assertEqual(t, cwd+"\n", readFile(t, cwdFile))
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("final-message file remains after resume: %q, err=%v", outputPath, err)
	}
}

func TestCodexExecutorRejectsMissingFinalMessageFile(t *testing.T) {
	dir := fakeCodexBinary(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_ARGS", filepath.Join(t.TempDir(), "args"))
	t.Setenv("FAKE_PROMPT", filepath.Join(t.TempDir(), "prompt"))
	t.Setenv("FAKE_CWD", filepath.Join(t.TempDir(), "cwd"))
	t.Setenv("FAKE_ENV", filepath.Join(t.TempDir(), "env"))
	outputPathFile := filepath.Join(t.TempDir(), "output-path")
	t.Setenv("FAKE_OUTPUT_PATH", outputPathFile)
	t.Setenv("FAKE_FINAL_MODE", "missing")

	result := NewCodex(Config{CWD: t.TempDir(), Timeout: testCommandTimeout}).Execute(context.Background(), Request{Prompt: "prompt"})

	assertEqual(t, false, result.Success)
	assertContains(t, result.Error, "did not write final output")
	outputPath := strings.TrimSpace(readFile(t, outputPathFile))
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("missing final-message file remains after execution: %q, err=%v", outputPath, err)
	}
}

func TestCodexExecutorRejectsReplacedFinalMessagePath(t *testing.T) {
	dir := fakeCodexBinary(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_ARGS", filepath.Join(t.TempDir(), "args"))
	t.Setenv("FAKE_PROMPT", filepath.Join(t.TempDir(), "prompt"))
	t.Setenv("FAKE_CWD", filepath.Join(t.TempDir(), "cwd"))
	t.Setenv("FAKE_ENV", filepath.Join(t.TempDir(), "env"))
	t.Setenv("FAKE_OUTPUT_PATH", filepath.Join(t.TempDir(), "output-path"))
	externalPath := filepath.Join(t.TempDir(), "external")
	if err := os.WriteFile(externalPath, []byte("must not be published"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_EXTERNAL_OUTPUT", externalPath)
	t.Setenv("FAKE_FINAL_MODE", "replace")

	result := NewCodex(Config{CWD: t.TempDir(), Timeout: testCommandTimeout}).Execute(context.Background(), Request{Prompt: "prompt"})

	assertEqual(t, false, result.Success)
	assertContains(t, result.Error, "did not write final output")
	assertNotContains(t, result.ResultText, "must not be published")
}

func TestCodexExecutorPreservesEmptyFinalMessageFileForBoundedRecovery(t *testing.T) {
	dir := fakeCodexBinary(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_ARGS", filepath.Join(t.TempDir(), "args"))
	t.Setenv("FAKE_PROMPT", filepath.Join(t.TempDir(), "prompt"))
	t.Setenv("FAKE_CWD", filepath.Join(t.TempDir(), "cwd"))
	t.Setenv("FAKE_ENV", filepath.Join(t.TempDir(), "env"))
	outputPathFile := filepath.Join(t.TempDir(), "output-path")
	t.Setenv("FAKE_OUTPUT_PATH", outputPathFile)
	t.Setenv("FAKE_FINAL_MODE", "empty")

	result := NewCodex(Config{CWD: t.TempDir(), Timeout: testCommandTimeout}).Execute(context.Background(), Request{Prompt: "prompt"})

	assertEqual(t, true, result.Success)
	assertEqual(t, "", result.ResultText)
	assertEqual(t, "thread-from-fake", result.SessionID)
	outputPath := strings.TrimSpace(readFile(t, outputPathFile))
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("empty final-message file remains after execution: %q, err=%v", outputPath, err)
	}
}

func TestCodexExecutorRejectsTruncatedModernJSONL(t *testing.T) {
	dir := fakeCodexBinary(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_ARGS", filepath.Join(t.TempDir(), "args"))
	t.Setenv("FAKE_PROMPT", filepath.Join(t.TempDir(), "prompt"))
	t.Setenv("FAKE_CWD", filepath.Join(t.TempDir(), "cwd"))
	t.Setenv("FAKE_ENV", filepath.Join(t.TempDir(), "env"))
	t.Setenv("FAKE_OUTPUT_PATH", filepath.Join(t.TempDir(), "output-path"))
	t.Setenv("FAKE_FINAL_MODE", "truncated")

	result := NewCodex(Config{CWD: t.TempDir(), Timeout: testCommandTimeout}).Execute(context.Background(), Request{Prompt: "prompt"})

	assertEqual(t, false, result.Success)
	assertContains(t, result.Error, "ended before turn.completed")
}

func TestCodexExecutorCleansFinalMessageFileOnFailureAndCancellation(t *testing.T) {
	t.Run("failed turn", func(t *testing.T) {
		dir := fakeCodexBinary(t)
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("FAKE_ARGS", filepath.Join(t.TempDir(), "args"))
		t.Setenv("FAKE_PROMPT", filepath.Join(t.TempDir(), "prompt"))
		t.Setenv("FAKE_CWD", filepath.Join(t.TempDir(), "cwd"))
		t.Setenv("FAKE_ENV", filepath.Join(t.TempDir(), "env"))
		outputPathFile := filepath.Join(t.TempDir(), "output-path")
		t.Setenv("FAKE_OUTPUT_PATH", outputPathFile)
		t.Setenv("FAKE_FINAL_MODE", "failed")

		result := NewCodex(Config{CWD: t.TempDir(), Timeout: testCommandTimeout}).Execute(context.Background(), Request{Prompt: "prompt"})
		assertEqual(t, false, result.Success)
		assertContains(t, result.Error, "synthetic failed turn")
		outputPath := strings.TrimSpace(readFile(t, outputPathFile))
		if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
			t.Fatalf("final-message file remains after failed turn: %q, err=%v", outputPath, err)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		dir := fakeCodexBinary(t)
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("FAKE_ARGS", filepath.Join(t.TempDir(), "args"))
		t.Setenv("FAKE_PROMPT", filepath.Join(t.TempDir(), "prompt"))
		t.Setenv("FAKE_CWD", filepath.Join(t.TempDir(), "cwd"))
		t.Setenv("FAKE_ENV", filepath.Join(t.TempDir(), "env"))
		outputPathFile := filepath.Join(t.TempDir(), "output-path")
		t.Setenv("FAKE_OUTPUT_PATH", outputPathFile)
		t.Setenv("FAKE_FINAL_MODE", "cancel")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan Result, 1)
		go func() {
			done <- NewCodex(Config{CWD: t.TempDir(), Timeout: testCommandTimeout}).Execute(ctx, Request{Prompt: "prompt"})
		}()
		waitForFile(t, outputPathFile)
		outputPath := strings.TrimSpace(readFile(t, outputPathFile))
		cancel()
		select {
		case result := <-done:
			assertEqual(t, false, result.Success)
		case <-time.After(time.Second):
			t.Fatal("cancelled Codex execution did not return")
		}
		if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
			t.Fatalf("final-message file remains after cancellation: %q, err=%v", outputPath, err)
		}
	})
}

func TestRunCommandCapturesTerminalRecordAtProcessExit(t *testing.T) {
	dir := fakeBinary(t, "terminal-record", `#!/bin/sh
cat >/dev/null
printf '{"type":"thread.started","thread_id":"thread-at-exit"}\n'
printf '{"type":"item.completed","item":{"id":"final","type":"agent_message","text":"terminal record"}}\n'
printf '{"type":"turn.completed"}\n'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	stdout, stderr, code, err := runCommand(context.Background(), Config{Timeout: testCommandTimeout}, t.TempDir(), []string{"terminal-record"}, "prompt", "card", "board", "timed out", nil)
	if err != nil {
		t.Fatalf("runCommand failed: %v", err)
	}
	assertEqual(t, "", stderr)
	if code == nil {
		t.Fatal("runCommand returned no exit code")
	}
	assertEqual(t, 0, *code)
	result := parseCodexOutput(stdout, stderr, *code, []string{"codex"})
	assertEqual(t, true, result.Success)
	assertEqual(t, "terminal record", result.ResultText)
	assertEqual(t, "thread-at-exit", result.SessionID)
}

func TestSupervisorCaptureDrainsAfterChildExit(t *testing.T) {
	barrier := filepath.Join(t.TempDir(), "first-line-seen")
	t.Setenv("SUPERVISOR_BARRIER", barrier)
	dir := fakeBinary(t, "capture-fixture", `#!/bin/sh
printf 'first\n'
while [ ! -e "$SUPERVISOR_BARRIER" ]; do sleep 0.01; done
printf 'terminal-record\n'
`)

	stdout, _, _, err := runCommand(context.Background(), Config{Timeout: time.Second}, t.TempDir(), []string{filepath.Join(dir, "capture-fixture")}, "", "testcard", "testboard", "timeout", func(line string) {
		if line == "first" {
			if err := os.WriteFile(barrier, []byte("ready"), 0o600); err != nil {
				t.Error(err)
			}
			time.Sleep(150 * time.Millisecond)
		}
	})
	if err != nil {
		t.Fatalf("capture error: %v", err)
	}
	if !strings.Contains(stdout, "terminal-record") {
		t.Fatalf("successful process lost terminal output: %q", stdout)
	}
}

func TestSupervisorInheritedStderrRespectsTimeout(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "child-pid")
	t.Setenv("SUPERVISOR_CHILD_PID", pidPath)
	dir := fakeBinary(t, "inherited-pipes", `#!/bin/sh
sleep 30 &
printf '%s' "$!" > "$SUPERVISOR_CHILD_PID"
printf 'parent finished\n'
exit 0
`)
	done := make(chan struct{})
	go func() {
		_, _, _, _ = runCommand(context.Background(), Config{Timeout: 100 * time.Millisecond}, t.TempDir(), []string{filepath.Join(dir, "inherited-pipes")}, "", "card", "board", "timeout", nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("runner exceeded its bound while a descendant held stderr open")
	}
	assertSupervisorChildStopped(t, pidPath)
}

func TestSupervisorBlockedCallbackHasBoundedDrain(t *testing.T) {
	dir := fakeBinary(t, "callback-fixture", "#!/bin/sh\nprintf 'first\\n'\n")
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_, _, _, _ = runCommand(context.Background(), Config{Timeout: 100 * time.Millisecond}, t.TempDir(), []string{filepath.Join(dir, "callback-fixture")}, "", "card", "board", "timeout", func(string) { <-release })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("stdout callback prevented bounded process completion")
	}
	close(release)
}

func TestSupervisorInheritedStdinCannotHoldParentCompletion(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "stdin-child-pid")
	promptPath := filepath.Join(t.TempDir(), "stdin-prompt")
	t.Setenv("SUPERVISOR_CHILD_PID", pidPath)
	t.Setenv("SUPERVISOR_PROMPT", promptPath)
	dir := fakeBinary(t, "inherited-input", `#!/bin/sh
exec 3<&0
dd bs=1 count=12 <&3 > "$SUPERVISOR_PROMPT" 2>/dev/null
sleep 30 <&3 >/dev/null 2>&1 &
printf '%s' "$!" > "$SUPERVISOR_CHILD_PID"
exit 0
`)
	done := make(chan struct{})
	go func() {
		_, _, _, _ = runCommand(context.Background(), Config{Timeout: testCommandTimeout}, t.TempDir(), []string{filepath.Join(dir, "inherited-input")}, strings.Repeat("long prompt ", 32768), "card", "board", "timeout", nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("direct child exited but inherited stdin held runner completion")
	}
	assertEqual(t, "long prompt ", readFile(t, promptPath))
	assertSupervisorChildStopped(t, pidPath)
}

func TestSupervisorManySlowCallbacksCannotExtendDrain(t *testing.T) {
	dir := fakeBinary(t, "slow-callbacks", `#!/bin/sh
i=0
while [ "$i" -lt 100 ]; do printf 'line\n'; i=$((i + 1)); done
`)
	done := make(chan struct{})
	go func() {
		_, _, _, _ = runCommand(context.Background(), Config{Timeout: 100 * time.Millisecond}, t.TempDir(), []string{filepath.Join(dir, "slow-callbacks")}, "", "card", "board", "timeout", func(string) { time.Sleep(40 * time.Millisecond) })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("many individually responsive progress callbacks extended runner completion")
	}
}

func TestRunCommandBoundsAndRedactsFailureDiagnostics(t *testing.T) {
	dir := fakeBinary(t, "noisy-failure", `#!/bin/sh
printf '%s' "$KARDBRD_TOKEN" >&2
i=0
while [ "$i" -lt 70000 ]; do
  printf x >&2
  i=$((i + 1))
done
exit 1
`)
	config := Config{Timeout: testCommandTimeout, APIURL: "https://api.test", Token: "tok_sensitive"}
	stdout, stderr, code, runErr := runCommand(context.Background(), config, t.TempDir(), []string{filepath.Join(dir, "noisy-failure")}, "", "card", "board", "timeout", nil)
	if code == nil {
		t.Fatal("runCommand returned no exit code")
	}
	if len(stderr) > maxSubprocessStderrBytes+len("\n... (output truncated)") {
		t.Fatalf("stderr was not bounded: %d bytes", len(stderr))
	}
	result := resultFromRun(parseCodexOutput, stdout, stderr, code, []string{"codex"}, runErr, config)
	assertEqual(t, false, result.Success)
	assertNotContains(t, result.Error, "tok_sensitive")
	assertNotContains(t, result.Stderr, "tok_sensitive")
	assertContains(t, result.Error, "[REDACTED]")
}

func TestRunCommandRetainsDiagnosticLimitFailureForOtherExecutors(t *testing.T) {
	dir := fakeBinary(t, "noisy-success", `#!/bin/sh
i=0
while [ "$i" -lt 70000 ]; do
  printf x >&2
  i=$((i + 1))
done
`)

	_, _, _, err := runCommand(context.Background(), Config{Timeout: testCommandTimeout}, t.TempDir(), []string{filepath.Join(dir, "noisy-success")}, "", "card", "board", "timeout", nil)
	if err == nil {
		t.Fatal("non-Codex runner accepted diagnostics beyond its configured limit")
	}
	assertContains(t, err.Error(), "subprocess output exceeds configured diagnostic limit")
}

func TestCodexExecutorAcceptsVerboseSuccessfulOutput(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			dir := fakeBinary(t, "codex", `#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
output = args[args.index('--output-last-message') + 1]
sys.stdin.read()
print(json.dumps({'type': 'thread.started', 'thread_id': 'synthetic-verbose'}))
if os.environ['SUPERVISOR_NOISY_STREAM'] == 'stdout':
    for i in range(5000):
        print(json.dumps({'type': 'item.completed', 'item': {'id': str(i), 'type': 'command_execution', 'aggregated_output': 'x' * 1000, 'exit_code': 0}}))
else:
    sys.stderr.write('ordinary diagnostics\\n' * 4000)
print(json.dumps({'type': 'item.completed', 'item': {'id': 'final', 'type': 'agent_message', 'text': 'completed'}}))
print(json.dumps({'type': 'turn.completed', 'usage': {}}))
with open(output, 'w') as f:
    f.write('completed')
`)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("SUPERVISOR_NOISY_STREAM", stream)

			result := NewCodex(Config{Timeout: 10 * time.Second}).Execute(context.Background(), Request{
				CWD:     filepath.Dir(dir),
				Prompt:  "synthetic",
				CardID:  "card",
				BoardID: "board",
			})
			if !result.Success || result.ResultText != "completed" {
				t.Fatalf("valid verbose run was discarded: success=%v final=%q error=%q", result.Success, result.ResultText, result.Error)
			}
		})
	}
}

func TestCodexExecutorReportsVerboseTurnFailure(t *testing.T) {
	dir := fakeBinary(t, "codex", `#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
output = args[args.index('--output-last-message') + 1]
sys.stdin.read()
print(json.dumps({'type': 'thread.started', 'thread_id': 'synthetic-verbose'}))
for i in range(5000):
    print(json.dumps({'type': 'item.completed', 'item': {'id': str(i), 'type': 'command_execution', 'aggregated_output': 'x' * 1000, 'exit_code': 0}}))
print(json.dumps({'type': 'turn.failed', 'error': {'message': 'synthetic verbose failure'}}))
with open(output, 'w') as f:
    f.write('completed')
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result := NewCodex(Config{Timeout: 10 * time.Second}).Execute(context.Background(), Request{
		CWD:     filepath.Dir(dir),
		Prompt:  "synthetic",
		CardID:  "card",
		BoardID: "board",
	})
	assertEqual(t, false, result.Success)
	assertContains(t, result.Error, "synthetic verbose failure")
}

func TestRunCommandDoesNotStartWithCancelledContext(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	dir := fakeBinary(t, "cancelled-fixture", `#!/bin/sh
printf started > "$STARTED_MARKER"
`)
	t.Setenv("STARTED_MARKER", marker)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, _, err := runCommand(ctx, Config{Timeout: testCommandTimeout}, t.TempDir(), []string{filepath.Join(dir, "cancelled-fixture")}, "", "card", "board", "timeout", nil)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runCommand error = %v, want context cancellation", err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("cancelled command started: %v", statErr)
	}
}

func assertSupervisorChildStopped(t *testing.T, pidPath string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	rawPID, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("fixture did not record descendant pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if err != nil {
		t.Fatalf("invalid descendant pid %q: %v", rawPID, err)
	}
	procPath := filepath.Join("/proc", strconv.Itoa(pid))
	deadline := time.Now().Add(time.Second)
	for {
		if _, statErr := os.Stat(procPath); errors.Is(statErr, os.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			if commandLine, readErr := os.ReadFile(filepath.Join(procPath, "cmdline")); readErr == nil && strings.Contains(string(commandLine), "sleep") {
				if process, findErr := os.FindProcess(pid); findErr == nil {
					_ = process.Kill()
				}
			}
			t.Fatalf("descendant process %d survived runner cleanup", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestScanStdoutReturnsReaderError(t *testing.T) {
	readErr := errors.New("synthetic stdout read failure")
	reader := &failingStdoutReader{err: readErr}
	var stdout bytes.Buffer

	err := scanStdout(reader, &stdout, nil)
	if !errors.Is(err, readErr) {
		t.Fatalf("scanStdout error = %v, want %v", err, readErr)
	}
}

func TestReadCodexFinalMessageBoundsOutput(t *testing.T) {
	output, err := createCodexFinalMessageFile()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = output.remove() }()
	if err := output.file.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := output.file.Write([]byte(strings.Repeat("x", maxCodexFinalMessageBytes+1))); err != nil {
		t.Fatal(err)
	}

	_, err = output.read()
	if err == nil {
		t.Fatal("oversized final output was accepted")
	}
	assertContains(t, err.Error(), "exceeds")
}

func TestCodexFinalMessageCleanupRejectsDirectoryReplacement(t *testing.T) {
	output, err := createCodexFinalMessageFile()
	if err != nil {
		t.Fatal(err)
	}
	movedDir := output.dir + "-moved"
	defer func() {
		_ = os.RemoveAll(movedDir)
		_ = os.RemoveAll(output.dir)
	}()
	if err := os.Rename(output.dir, movedDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(output.dir, "must-remain")
	if err := os.WriteFile(marker, []byte("replacement content"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = output.remove()

	if err == nil {
		t.Fatal("cleanup accepted a replacement directory")
	}
	assertContains(t, err.Error(), "identity changed")
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("cleanup removed replacement content: %v", statErr)
	}
}

func TestCodexAuthHintMentionsOpenAIAPIKey(t *testing.T) {
	dir := fakeBinary(t, "codex", `#!/bin/sh
if [ "$1" = "login" ] && [ "$2" = "status" ]; then
  echo "not logged in" >&2
  exit 1
fi
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := NewCodex(Config{Timeout: testCommandTimeout})
	status := exec.CheckAuth(context.Background())

	assertEqual(t, false, status.Authenticated)
	assertContains(t, status.AuthHint, "OPENAI_API_KEY")
}

func TestGooseExecutorCommand(t *testing.T) {
	dir := fakeBinary(t, "goose", `#!/bin/sh
cat >/dev/null
printf '%s\n' "$@" > "$FAKE_ARGS"
printf '%s\n' "$KARDBRD_CARD_ID|$KARDBRD_BOARD_ID" > "$FAKE_ENV"
printf '{"type":"AgentMessageChunk","content":"ok"}\n'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argsFile := filepath.Join(t.TempDir(), "args")
	envFile := filepath.Join(t.TempDir(), "env")
	t.Setenv("FAKE_ARGS", argsFile)
	t.Setenv("FAKE_ENV", envFile)
	t.Setenv("KARDBRD_CARD_ID", "stale-card")
	t.Setenv("KARDBRD_BOARD_ID", "stale-board")

	exec := NewGoose(Config{CWD: t.TempDir(), Timeout: testCommandTimeout})
	result := exec.Execute(context.Background(), Request{Prompt: "hello", ResumeSessionID: "session1", CardID: "card3", BoardID: "board3"})
	if !result.Success {
		t.Fatalf("goose execute failed: error=%q stderr=%q command=%v", result.Error, result.Stderr, result.Command)
	}
	args := readFile(t, argsFile)
	assertContains(t, args, "run\n")
	assertContains(t, args, "-t\n-")
	assertContains(t, args, "--output-format\nstream-json")
	assertContains(t, args, "-r\n-n\nsession1")
	assertEqual(t, "card3|board3\n", readFile(t, envFile))
}

func TestExecutorEmitsChunksBeforeProcessExit(t *testing.T) {
	dir := fakeBinary(t, "goose", `#!/bin/sh
cat >/dev/null
printf '{"type":"AgentMessageChunk","content":"live"}\n'
sleep 2
printf '{"type":"AgentMessageChunk","content":"done"}\n'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := NewGoose(Config{CWD: t.TempDir(), Timeout: 5 * time.Second})
	chunks := make(chan string, 2)
	done := make(chan Result, 1)
	go func() {
		done <- exec.Execute(context.Background(), Request{
			Prompt: "hello",
			OnChunk: func(content string, chunkType string) {
				chunks <- content
			},
		})
	}()

	select {
	case got := <-chunks:
		assertEqual(t, "live", got)
	case <-done:
		t.Fatal("executor returned before streaming first chunk")
	case <-time.After(time.Second):
		t.Fatal("first chunk was not streamed while process was running")
	}

	result := <-done
	assertEqual(t, true, result.Success)
}

func TestPiExecutorCommand(t *testing.T) {
	dir := fakeBinary(t, "pi", `#!/bin/sh
cat >/dev/null
printf '%s\n' "$@" > "$FAKE_ARGS"
printf '%s\n' "$KARDBRD_CARD_ID|$KARDBRD_BOARD_ID" > "$FAKE_ENV"
printf '{"type":"session","id":"s1"}\n{"type":"message_end","message":"ok"}\n'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argsFile := filepath.Join(t.TempDir(), "args")
	envFile := filepath.Join(t.TempDir(), "env")
	t.Setenv("FAKE_ARGS", argsFile)
	t.Setenv("FAKE_ENV", envFile)
	t.Setenv("KARDBRD_CARD_ID", "stale-card")
	t.Setenv("KARDBRD_BOARD_ID", "stale-board")

	exec := NewPi(Config{CWD: t.TempDir(), Timeout: testCommandTimeout})
	result := exec.Execute(context.Background(), Request{Prompt: "hello", ResumeSessionID: "session1", CardID: "card4", BoardID: "board4"})
	assertEqual(t, true, result.Success)
	args := readFile(t, argsFile)
	assertContains(t, args, "--mode\njson")
	assertContains(t, args, "-p\n-")
	assertContains(t, args, "-a")
	assertContains(t, args, "--session\nsession1")
	assertEqual(t, "card4|board4\n", readFile(t, envFile))
}

func fakeBinary(t *testing.T, name string, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

type failingStdoutReader struct {
	err   error
	first bool
}

func (r *failingStdoutReader) Read(data []byte) (int, error) {
	if !r.first {
		r.first = true
		return copy(data, "first\n"), nil
	}
	return 0, r.err
}

func fakeCodexBinary(t *testing.T) string {
	t.Helper()
	return fakeBinary(t, "codex", `#!/bin/sh
output_path=""
expect_output_path=0
for arg in "$@"; do
  if [ "$expect_output_path" = 1 ]; then
    output_path="$arg"
    expect_output_path=0
  elif [ "$arg" = "--output-last-message" ] || [ "$arg" = "-o" ]; then
    expect_output_path=1
  fi
done
printf '%s\n' "$@" > "$FAKE_ARGS"
printf '%s' "$output_path" > "$FAKE_OUTPUT_PATH"
pwd > "$FAKE_CWD"
printf '%s\n' "$KARDBRD_TOKEN|$KARDBRD_API_URL|$KARDBRD_CARD_ID|$KARDBRD_BOARD_ID" > "$FAKE_ENV"
cat > "$FAKE_PROMPT"
if [ "$FAKE_FINAL_MODE" = "cancel" ]; then
  while :; do :; done
fi
printf '{"type":"thread.started","thread_id":"thread-from-fake"}\n'
if [ "$FAKE_FINAL_MODE" = "failed" ]; then
  printf '{"type":"turn.failed","error":{"message":"synthetic failed turn"}}\n'
  exit 0
fi
printf '{"type":"item.updated","item":{"id":"progress","type":"agent_message","text":"working"}}\n'
printf '{"type":"item.updated","item":{"id":"progress","type":"agent_message","text":"working"}}\n'
printf '{"type":"item.completed","item":{"id":"final","type":"agent_message","text":"final JSONL message"}}\n'
case "$FAKE_FINAL_MODE" in
  final) printf 'authoritative final response' > "$output_path" ;;
  empty) : > "$output_path" ;;
  missing) : ;;
  replace) rm -f "$output_path"; ln -s "$FAKE_EXTERNAL_OUTPUT" "$output_path" ;;
  truncated) printf 'untrusted final output' > "$output_path" ;;
esac
if [ "$FAKE_FINAL_MODE" != "truncated" ]; then
  printf '{"type":"turn.completed"}\n'
fi
`)
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q to contain %q", got, want)
	}
}

func assertNotContains(t *testing.T, got string, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Fatalf("expected %q not to contain %q", got, want)
	}
}
