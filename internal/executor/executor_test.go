package executor

import (
	"context"
	"os"
	"path/filepath"
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
		ResumeSessionID: "thread-123",
		Model:           "gpt-5.4",
		CardID:          "card2",
		BoardID:         "board2",
	})

	assertEqual(t, true, result.Success)
	assertEqual(t, "authoritative final response", result.ResultText)
	outputPath := strings.TrimSpace(readFile(t, outputPathFile))
	assertEqual(t, "exec\nresume\n--dangerously-bypass-approvals-and-sandbox\n--json\n--output-last-message\n"+outputPath+"\n--model\ngpt-5.4\nthread-123\n", readFile(t, argsFile))
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

func TestReadCodexFinalMessageBoundsOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "final.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxCodexFinalMessageBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := readCodexFinalMessage(path)
	if err == nil {
		t.Fatal("oversized final output was accepted")
	}
	assertContains(t, err.Error(), "exceeds")
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
esac
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
