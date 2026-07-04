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
printf '%s\n' "$@" > "$FAKE_ARGS"
printf '%s\n' "$KARDBRD_TOKEN|$KARDBRD_API_URL" > "$FAKE_ENV"
printf '{"type":"result","result":"ok","session_id":"s1"}\n'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argsFile := filepath.Join(t.TempDir(), "args")
	envFile := filepath.Join(t.TempDir(), "env")
	t.Setenv("FAKE_ARGS", argsFile)
	t.Setenv("FAKE_ENV", envFile)

	exec := NewClaude(Config{CWD: t.TempDir(), Timeout: testCommandTimeout, APIURL: "https://api.test", Token: "tok"})
	result := exec.Execute(context.Background(), Request{Prompt: "hello", ResumeSessionID: "resume1"})
	assertEqual(t, true, result.Success)

	args := readFile(t, argsFile)
	assertContains(t, args, "-p\n-\n")
	assertContains(t, args, "--output-format=stream-json")
	assertContains(t, args, "--resume\nresume1")
	assertEqual(t, "tok|https://api.test\n", readFile(t, envFile))
}

func TestCodexExecutorCommand(t *testing.T) {
	dir := fakeBinary(t, "codex", `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_ARGS"
printf '{"type":"item.message","content":"ok"}\n'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_ARGS", argsFile)

	exec := NewCodex(Config{CWD: t.TempDir(), Timeout: testCommandTimeout})
	result := exec.Execute(context.Background(), Request{Prompt: "hello", Model: "gpt-5.4"})
	assertEqual(t, true, result.Success)
	args := readFile(t, argsFile)
	assertContains(t, args, "exec\n")
	assertContains(t, args, "--dangerously-bypass-approvals-and-sandbox")
	assertContains(t, args, "--json")
	assertContains(t, args, "--model\ngpt-5.4")
}

func TestGooseExecutorCommand(t *testing.T) {
	dir := fakeBinary(t, "goose", `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_ARGS"
printf '{"type":"AgentMessageChunk","content":"ok"}\n'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_ARGS", argsFile)

	exec := NewGoose(Config{CWD: t.TempDir(), Timeout: testCommandTimeout})
	result := exec.Execute(context.Background(), Request{Prompt: "hello", ResumeSessionID: "session1"})
	assertEqual(t, true, result.Success)
	args := readFile(t, argsFile)
	assertContains(t, args, "run\n")
	assertContains(t, args, "-t\n-")
	assertContains(t, args, "--output-format\nstream-json")
	assertContains(t, args, "-r\n-n\nsession1")
}

func TestExecutorEmitsChunksBeforeProcessExit(t *testing.T) {
	dir := fakeBinary(t, "goose", `#!/bin/sh
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
printf '%s\n' "$@" > "$FAKE_ARGS"
printf '{"type":"session","id":"s1"}\n{"type":"message_end","message":"ok"}\n'
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_ARGS", argsFile)

	exec := NewPi(Config{CWD: t.TempDir(), Timeout: testCommandTimeout})
	result := exec.Execute(context.Background(), Request{Prompt: "hello", ResumeSessionID: "session1"})
	assertEqual(t, true, result.Success)
	args := readFile(t, argsFile)
	assertContains(t, args, "--mode\njson")
	assertContains(t, args, "-p\n-")
	assertContains(t, args, "-a")
	assertContains(t, args, "--session\nsession1")
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
