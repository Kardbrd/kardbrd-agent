package worktree

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWorktreeNaming(t *testing.T) {
	manager := NewManager("/repo", "", "", "claude")
	assertEqual(t, filepath.Join("/", "card-abcdef12"), filepath.Clean(manager.WorktreePath("abcdef123456")))
	assertEqual(t, "card/abcdef12", manager.BranchName("abcdef123456"))
}

func TestCreateWorktreeCommandOrder(t *testing.T) {
	base := t.TempDir()
	runner := &fakeRunner{stdout: map[string]string{"git rev-parse --abbrev-ref HEAD": "feature\n"}}
	manager := NewManager(base, t.TempDir(), "", "claude")
	manager.Runner = runner

	_, err := manager.Create("abcdef123456", "")
	if err != nil {
		t.Fatal(err)
	}

	got := runner.commandsOnly()
	want := []string{
		"git fetch origin main",
		"git rev-parse --abbrev-ref HEAD",
		"git checkout main",
		"git pull --ff-only",
		"git checkout feature",
		"git worktree add -b card/abcdef12 " + manager.WorktreePath("abcdef123456"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands mismatch:\nwant %#v\n got %#v", want, got)
	}
}

func TestCreateWorktreeFallsBackWhenBranchExists(t *testing.T) {
	base := t.TempDir()
	runner := &fakeRunner{
		stdout: map[string]string{"git rev-parse --abbrev-ref HEAD": "main\n"},
		errs: map[string]error{
			"git worktree add -b card/abcdef12 " + filepath.Join(base, "card-abcdef12"): RunError{Stderr: "fatal: branch already exists"},
		},
	}
	manager := NewManager(base, base, "", "claude")
	manager.Runner = runner

	_, err := manager.Create("abcdef123456", "")
	if err != nil {
		t.Fatal(err)
	}

	got := strings.Join(runner.commandsOnly(), "\n")
	assertContains(t, got, "git worktree add "+filepath.Join(base, "card-abcdef12")+" card/abcdef12")
}

func TestSetupSymlinks(t *testing.T) {
	base := t.TempDir()
	worktree := t.TempDir()
	writeFile(t, filepath.Join(base, ".env"), "TOKEN=1")
	if err := os.MkdirAll(filepath.Join(base, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(base, ".claude", "settings.local.json"), "{}")

	manager := NewManager(base, t.TempDir(), "", "claude")
	if err := manager.SetupSymlinks(worktree); err != nil {
		t.Fatal(err)
	}
	assertSymlinkTarget(t, filepath.Join(worktree, ".env"), filepath.Join(base, ".env"))
	assertSymlinkTarget(t, filepath.Join(worktree, ".claude", "settings.local.json"), filepath.Join(base, ".claude", "settings.local.json"))
}

func TestRemoveWorktreeCommands(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "card-abcdef12")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	manager := NewManager(base, base, "", "claude")
	manager.Runner = runner

	if err := manager.Remove("abcdef123456", true); err != nil {
		t.Fatal(err)
	}
	assertContains(t, strings.Join(runner.commandsOnly(), "\n"), "git worktree remove "+path+" --force")
}

type fakeRunner struct {
	commands []string
	stdout   map[string]string
	errs     map[string]error
}

func (r *fakeRunner) Run(dir string, args []string) (RunResult, error) {
	command := strings.Join(args, " ")
	r.commands = append(r.commands, command)
	if r.errs != nil {
		if err := r.errs[command]; err != nil {
			return RunResult{}, err
		}
	}
	if r.stdout != nil {
		if stdout, ok := r.stdout[command]; ok {
			return RunResult{Stdout: stdout}, nil
		}
	}
	return RunResult{}, nil
}

func (r *fakeRunner) commandsOnly() []string {
	return append([]string(nil), r.commands...)
}

type RunError struct {
	Stderr string
}

func (e RunError) Error() string {
	return e.Stderr
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertSymlinkTarget(t *testing.T, path string, want string) {
	t.Helper()
	got, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("want symlink target %q, got %q", want, got)
	}
}

func assertEqual[T comparable](t *testing.T, want T, got T) {
	t.Helper()
	if got != want {
		t.Fatalf("want %#v, got %#v", want, got)
	}
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q to contain %q", got, want)
	}
}
