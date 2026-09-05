package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentStartReportsMissingNewEnvNames(t *testing.T) {
	isolateAgentStartEnvironment(t)
	t.Setenv("KARDBRD_TOKEN", "")
	t.Setenv("KARDBRD_AGENT_BOARD_ID", "")
	t.Setenv("KARDBRD_AGENT_NAME", "")

	stdout, stderr, err := executeRoot("agent", "start", "--cwd", t.TempDir())
	if err == nil {
		t.Fatal("expected missing config error")
	}
	output := stdout + stderr
	assertCLIContains(t, output, "KARDBRD_AGENT_BOARD_ID (--board-id)")
	assertCLIContains(t, output, "KARDBRD_TOKEN (--token)")
	assertCLIContains(t, output, "KARDBRD_AGENT_NAME (--name)")
}

func TestAgentStartReportsLegacyEnvRenames(t *testing.T) {
	isolateAgentStartEnvironment(t)
	t.Setenv("KARDBRD_TOKEN", "tok")
	t.Setenv("KARDBRD_ID", "board")
	t.Setenv("KARDBRD_AGENT", "bot")
	t.Setenv("AGENT_CWD", "/repo")

	stdout, stderr, err := executeRoot("agent", "start", "--cwd", t.TempDir())
	if err == nil {
		t.Fatal("expected legacy env error")
	}
	output := stdout + stderr
	assertCLIContains(t, output, "KARDBRD_ID was renamed to KARDBRD_AGENT_BOARD_ID")
	assertCLIContains(t, output, "KARDBRD_AGENT was renamed to KARDBRD_AGENT_NAME")
	assertCLIContains(t, output, "AGENT_CWD was renamed to KARDBRD_AGENT_CWD")
}

func isolateAgentStartEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"KARDBRD_API_URL",
		"KARDBRD_TOKEN",
		"KARDBRD_AGENT_BOARD_ID",
		"KARDBRD_AGENT_CWD",
		"KARDBRD_AGENT_NAME",
		"KARDBRD_AGENT_TIMEOUT",
		"KARDBRD_AGENT_MAX_CONCURRENT",
		"KARDBRD_AGENT_WORKTREES_DIR",
		"KARDBRD_AGENT_SETUP_CMD",
		"KARDBRD_AGENT_RULES_FILE",
		"KARDBRD_AGENT_EXECUTOR",
		"KARDBRD_ID",
		"KARDBRD_AGENT",
		"KARDBRD_URL",
		"AGENT_CWD",
		"AGENT_TIMEOUT",
		"AGENT_MAX_CONCURRENT",
		"AGENT_WORKTREES_DIR",
		"AGENT_SETUP_CMD",
		"AGENT_RULES_FILE",
		"AGENT_EXECUTOR",
	} {
		t.Setenv(name, "")
	}
}

func TestAgentValidateReportsValidRulesFile(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "rules", "valid.yml")

	stdout, stderr, err := executeRoot("agent", "validate", path)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	assertCLIContains(t, stdout, "Valid")
}

func TestAgentCommandsRejectExplicitFormat(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "rules", "valid.yml")
	for _, tt := range []struct {
		name string
		args []string
	}{
		{name: "group", args: []string{"agent", "--format", "json"}},
		{name: "validate before", args: []string{"--format", "json", "agent", "validate", path}},
		{name: "validate after", args: []string{"agent", "validate", path, "--format", "md"}},
		{name: "start", args: []string{"agent", "start", "--format", "tsv"}},
		{name: "unknown", args: []string{"--format", "yaml", "agent", "validate", path}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, err := executeRoot(tt.args...)
			if err == nil {
				t.Fatal("expected agent command format error")
			}
			assertCLIContains(t, stderr, "--format")
			if tt.name == "unknown" {
				assertCLIContains(t, stderr, "tsv, json, md")
			} else {
				assertCLIContains(t, stderr, "agent")
			}
		})
	}
}

func TestAgentGroupWithoutFormatPrintsHelp(t *testing.T) {
	stdout, stderr, err := executeRoot("agent")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	assertCLIContains(t, stdout, "Agent daemon commands")
}

func TestAgentValidateReportsInvalidRulesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yml")
	if err := os.WriteFile(path, []byte("agent: Bot\nrules: not-a-list\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := executeRoot("agent", "validate", path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	output := stdout + stderr
	assertCLIContains(t, output, "Missing required field 'board_id'")
	assertCLIContains(t, output, "'rules' must be a list")
}

func TestAgentStartPrintsSummaryAndRunsRuntime(t *testing.T) {
	dir := t.TempDir()
	var captured agentRuntime
	restore := stubAgentRuntime(t, func(ctx context.Context, runtime agentRuntime) error {
		captured = runtime
		return nil
	})
	defer restore()

	stdout, stderr, err := executeRoot(
		"--token", "tok",
		"agent", "start",
		"--board-id", "board1",
		"--name", "coder",
		"--cwd", dir,
		"--executor", "codex",
		"--timeout", "45",
		"--max-concurrent", "2",
		"--setup-cmd", "npm install",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}

	assertCLIContains(t, stdout, "Starting kardbrd agent")
	assertCLIContains(t, stdout, "Board: board1")
	assertCLIContains(t, stdout, "Agent: @coder")
	assertCLIContains(t, stdout, "Executor: codex")
	assertCLIContains(t, stdout, "Timeout: 45s")
	assertCLIContains(t, stdout, "Max concurrent: 2")
	assertCLIContains(t, stderr, "not a git repository")
	assertEqual(t, "board1", captured.Config.BoardID)
	assertEqual(t, "coder", captured.Config.AgentName)
	assertEqual(t, "codex", captured.Config.Executor)
	assertEqual(t, false, captured.WorktreesEnabled)
}

func TestAgentStartPropagatesNoRetryToRuntimeClient(t *testing.T) {
	dir := t.TempDir()
	var captured agentRuntime
	restore := stubAgentRuntime(t, func(ctx context.Context, runtime agentRuntime) error {
		captured = runtime
		return nil
	})
	defer restore()

	_, stderr, err := executeRoot(
		"--no-retry",
		"--token", "tok",
		"agent", "start",
		"--board-id", "board1",
		"--name", "coder",
		"--cwd", dir,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	assertEqual(t, true, captured.NoRetry)
}

func TestAgentStartRulesFileOverridesExecutor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kardbrd.yml")
	if err := os.WriteFile(path, []byte("board_id: board-from-rules\nagent: coder\nexecutor: codex\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var captured agentRuntime
	restore := stubAgentRuntime(t, func(ctx context.Context, runtime agentRuntime) error {
		captured = runtime
		return nil
	})
	defer restore()

	_, stderr, err := executeRoot(
		"--token", "tok",
		"agent", "start",
		"--board-id", "board-flag",
		"--name", "coder",
		"--cwd", dir,
		"--executor", "claude",
		"--rules", path,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}

	assertEqual(t, "board-from-rules", captured.Config.BoardID)
	assertEqual(t, "codex", captured.Config.Executor)
}

func stubAgentRuntime(t *testing.T, fn func(context.Context, agentRuntime) error) func() {
	t.Helper()
	previous := runAgentRuntime
	runAgentRuntime = fn
	return func() {
		runAgentRuntime = previous
	}
}

func executeRoot(args ...string) (string, string, error) {
	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func assertCLIContains(t *testing.T, output string, want string) {
	t.Helper()
	if !strings.Contains(output, want) {
		t.Fatalf("expected output to contain %q:\n%s", want, output)
	}
}

func assertEqual[T comparable](t *testing.T, want T, got T) {
	t.Helper()
	if got != want {
		t.Fatalf("want %#v, got %#v", want, got)
	}
}
