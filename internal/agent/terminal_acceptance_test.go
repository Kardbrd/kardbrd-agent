package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/executor"
)

// This exercises the same Codex subprocess, progress stream, and card
// publication path used by a mention. It is also runnable at the v0.11.0
// runtime source revision to demonstrate the original missing-report failure.
func TestCodexTerminalAcceptanceModernFinalAfterProgress(t *testing.T) {
	dir := t.TempDir()
	countPath := filepath.Join(dir, "executions")
	artifactPath := filepath.Join(dir, "artifact")
	script := `#!/bin/sh
if [ "$1" = login ]; then exit 0; fi
printf 'run\n' >> "$ACCEPT_COUNT"
printf 'useful work\n' >> "$ACCEPT_ARTIFACT"
output_path=""
previous=""
for arg in "$@"; do
  if [ "$previous" = "--output-last-message" ]; then output_path="$arg"; fi
  previous="$arg"
done
cat >/dev/null
printf '{"type":"thread.started","thread_id":"thread-acceptance"}\n'
printf '{"type":"item.updated","item":{"id":"progress","type":"agent_message","text":"working"}}\n'
printf '{"type":"item.completed","item":{"id":"final","type":"agent_message","text":"protocol fallback only"}}\n'
if [ -n "$output_path" ]; then printf 'tested final report' > "$output_path"; fi
printf '{"type":"turn.completed"}\n'
`
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ACCEPT_COUNT", countPath)
	t.Setenv("ACCEPT_ARTIFACT", artifactPath)

	manager := newTestManager(t)
	manager.Executor = executor.NewCodex(executor.Config{CWD: dir, Timeout: 3 * time.Second})
	manager.Worktree = &fakeWorktree{path: dir}
	client := manager.Client.(*fakeBoardClient)
	stream := &fakeStream{}
	client.onReaction = func(_, _, emoji string) {
		if emoji == "👀" {
			manager.mu.Lock()
			manager.Active["card1"].Stream = stream
			manager.mu.Unlock()
		}
	}
	progressBeforeComment := false
	client.onAddComment = func(_, _ string) {
		for _, payload := range stream.payloads {
			if chunk, ok := payload.(map[string]any); ok && chunk["text"] == "working" {
				progressBeforeComment = true
			}
		}
	}

	if err := manager.ProcessMention(context.Background(), "card1", "comment1", "@coder do work", "Paul"); err != nil {
		t.Fatal(err)
	}
	countBytes, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(countBytes), "run\n"); got != 1 {
		t.Fatalf("Codex invocations = %d, want 1", got)
	}
	artifactBytes, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(artifactBytes); got != "useful work\n" {
		t.Fatalf("artifact = %q, want one completed side effect", got)
	}
	comments := client.commentsSnapshot()
	if len(comments) != 1 || !strings.Contains(comments[0].content, "tested final report") {
		t.Fatalf("card comments = %+v, want one actual terminal report", comments)
	}
	if strings.Contains(comments[0].content, "No terminal response received") {
		t.Fatalf("spurious missing-output warning: %q", comments[0].content)
	}
	if strings.Contains(comments[0].content, "protocol fallback only") {
		t.Fatalf("published JSONL fallback instead of authoritative final file: %q", comments[0].content)
	}
	if !progressBeforeComment {
		t.Fatalf("progress was not delivered before terminal publication: %+v", stream.payloads)
	}
	assertReaction(t, client.reactionsSnapshot(), "comment1", "✅")
	assertNoReaction(t, client.reactionsSnapshot(), "comment1", "⚠️")
}

func TestCodexTerminalAcceptanceFailuresStayVisible(t *testing.T) {
	for _, test := range []struct {
		name    string
		want    string
		timeout time.Duration
	}{
		{name: "missing", want: "Codex did not write final output"},
		{name: "protocol", want: "synthetic turn failure"},
		{name: "nonzero", want: "exited with code 7"},
		{name: "timeout", want: "Codex execution timed out", timeout: 500 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			countPath := filepath.Join(dir, "executions")
			script := `#!/bin/sh
if [ "$1" = login ]; then exit 0; fi
printf 'run\n' >> "$ACCEPT_COUNT"
output_path=""
previous=""
for arg in "$@"; do
  if [ "$previous" = "--output-last-message" ]; then output_path="$arg"; fi
  previous="$arg"
done
cat >/dev/null
printf '{"type":"thread.started","thread_id":"thread-failure"}\n'
case "$ACCEPT_SCENARIO" in
  missing)
    printf '{"type":"turn.completed"}\n' ;;
  protocol)
    printf 'untrusted final' > "$output_path"
    printf '{"type":"turn.failed","error":{"message":"synthetic turn failure"}}\n' ;;
  nonzero)
    printf 'untrusted final' > "$output_path"
    printf '{"type":"turn.completed"}\n'
    exit 7 ;;
  timeout)
    sleep 5 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("ACCEPT_COUNT", countPath)
			t.Setenv("ACCEPT_SCENARIO", test.name)
			manager := newTestManager(t)
			manager.Worktree = &fakeWorktree{path: dir}
			timeout := test.timeout
			if timeout == 0 {
				timeout = 3 * time.Second
			}
			manager.Executor = executor.NewCodex(executor.Config{CWD: dir, Timeout: timeout})
			started := time.Now()
			if err := manager.ProcessMention(context.Background(), "card1", "comment1", "@coder do work", "Paul"); err != nil {
				t.Fatal(err)
			}
			if elapsed := time.Since(started); elapsed > 2*time.Second {
				t.Fatalf("completion took %s, want bounded terminal result", elapsed)
			}
			countBytes, err := os.ReadFile(countPath)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(string(countBytes), "run\n"); got != 1 {
				t.Fatalf("Codex invocations = %d, want 1", got)
			}
			client := manager.Client.(*fakeBoardClient)
			comments := client.commentsSnapshot()
			if len(comments) != 1 || !strings.Contains(comments[0].content, test.want) {
				t.Fatalf("card comments = %+v, want one failure containing %q", comments, test.want)
			}
			assertNoReaction(t, client.reactionsSnapshot(), "comment1", "✅")
			assertReaction(t, client.reactionsSnapshot(), "comment1", "🛑")
		})
	}
}

func TestCodexTerminalAcceptanceStopDoesNotPublishSuccess(t *testing.T) {
	dir := t.TempDir()
	startedPath := filepath.Join(dir, "started")
	script := `#!/bin/sh
if [ "$1" = login ]; then exit 0; fi
cat >/dev/null
touch "$ACCEPT_STARTED"
sleep 5
`
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ACCEPT_STARTED", startedPath)
	manager := newTestManager(t)
	manager.Worktree = &fakeWorktree{path: dir}
	manager.Executor = executor.NewCodex(executor.Config{CWD: dir, Timeout: 3 * time.Second})
	done := make(chan error, 1)
	go func() {
		done <- manager.ProcessMention(context.Background(), "card1", "comment1", "@coder do work", "Paul")
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(startedPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Codex did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := manager.HandleStopReaction(context.Background(), "card1", "comment1"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("interrupted subprocess did not finish promptly")
	}
	client := manager.Client.(*fakeBoardClient)
	comments := client.commentsSnapshot()
	if len(comments) != 1 || !strings.Contains(comments[0].content, "Agent stopped") {
		t.Fatalf("card comments = %+v, want one interruption report", comments)
	}
	assertNoReaction(t, client.reactionsSnapshot(), "comment1", "✅")
}
