package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/rules"
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

func TestLifecyclePrepareFetchesConfiguredRefWithoutMutatingDirtyBase(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	git(t, "", "init", "--bare", remote)
	seed := filepath.Join(t.TempDir(), "seed")
	git(t, "", "clone", remote, seed)
	configureTestGit(t, seed)
	writeFile(t, filepath.Join(seed, "source.txt"), "main\n")
	git(t, seed, "add", "source.txt")
	git(t, seed, "commit", "-m", "main")
	git(t, seed, "branch", "-M", "main")
	git(t, seed, "push", "-u", "origin", "main")

	base := filepath.Join(t.TempDir(), "base")
	git(t, "", "clone", remote, base)
	configureTestGit(t, base)
	git(t, base, "checkout", "-b", "feature", "origin/main")
	writeFile(t, filepath.Join(base, "source.txt"), "dirty feature\n")

	manager, err := NewLifecycleManager(base, filepath.Join(t.TempDir(), "worktrees"), "codex", rules.WorktreeConfig{
		Base:     rules.WorktreeBase{Remote: "origin", Ref: "refs/heads/main"},
		Checkout: rules.WorktreeCheckout{Mode: rules.CheckoutFull},
		Sharing:  rules.WorktreeSharing{Env: rules.SharingEnvDisabled, Skills: rules.SharingSkillsDisabled},
	})
	if err != nil {
		t.Fatal(err)
	}

	path, err := manager.Prepare(context.Background(), LifecycleRequest{CardID: "AbCdEf123", BoardID: "board1"})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, filepath.Join(manager.WorktreesBase, "card-AbCdEf123"), path)
	assertEqual(t, "main\n", readFile(t, filepath.Join(path, "source.txt")))
	assertEqual(t, "feature\n", gitOutput(t, base, "branch", "--show-current"))
	assertEqual(t, "dirty feature\n", readFile(t, filepath.Join(base, "source.txt")))
	assertEqual(t, "card/AbCdEf123\n", gitOutput(t, path, "branch", "--show-current"))
}

func TestLifecycleExistingOrBaseUsesBaseForAbsentSource(t *testing.T) {
	base := t.TempDir()
	git(t, base, "init")
	configureTestGit(t, base)
	manager, err := NewLifecycleManager(base, filepath.Join(t.TempDir(), "worktrees"), "codex", rules.WorktreeConfig{
		Checkout: rules.WorktreeCheckout{Mode: rules.CheckoutFull},
		Sharing:  rules.WorktreeSharing{Env: rules.SharingEnvDisabled, Skills: rules.SharingSkillsDisabled},
	})
	if err != nil {
		t.Fatal(err)
	}

	path, err := manager.ExistingOrBase(context.Background(), LifecycleRequest{CardID: "CardOne", BoardID: "board1"})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, manager.BaseRepo, path)
}

func TestLifecycleExistingOrBaseFailsClosedForPresentForeignOrSymlinkPath(t *testing.T) {
	for _, mode := range []string{"foreign", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			git(t, base, "init")
			configureTestGit(t, base)
			root := filepath.Join(t.TempDir(), "worktrees")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			manager, err := NewLifecycleManager(base, root, "codex", rules.WorktreeConfig{Checkout: rules.WorktreeCheckout{Mode: rules.CheckoutFull}, Sharing: rules.WorktreeSharing{Env: rules.SharingEnvDisabled, Skills: rules.SharingSkillsDisabled}})
			if err != nil {
				t.Fatal(err)
			}
			path := manager.WorktreePath("Foreign")
			if mode == "foreign" {
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(t.TempDir(), path); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.ExistingOrBase(context.Background(), LifecycleRequest{CardID: "Foreign", BoardID: "board1"}); err == nil {
				t.Fatal("expected present unowned path to fail closed")
			}
		})
	}
}

func TestLifecycleExistingOrBaseUsesBaseForStaleAbsentRegistration(t *testing.T) {
	base := t.TempDir()
	git(t, base, "init")
	configureTestGit(t, base)
	writeFile(t, filepath.Join(base, "source.txt"), "base\n")
	git(t, base, "add", "source.txt")
	git(t, base, "commit", "-m", "base")
	root := filepath.Join(t.TempDir(), "worktrees")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manager, err := NewLifecycleManager(base, root, "codex", rules.WorktreeConfig{Checkout: rules.WorktreeCheckout{Mode: rules.CheckoutFull}, Sharing: rules.WorktreeSharing{Env: rules.SharingEnvDisabled, Skills: rules.SharingSkillsDisabled}})
	if err != nil {
		t.Fatal(err)
	}
	path := manager.WorktreePath("Stale")
	git(t, base, "worktree", "add", "-b", manager.BranchName("Stale"), path)
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	selected, err := manager.ExistingOrBase(context.Background(), LifecycleRequest{CardID: "Stale", BoardID: "board1"})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, manager.BaseRepo, selected)
	registered := gitOutput(t, base, "worktree", "list", "--porcelain")
	assertContains(t, registered, "worktree "+path)
}

func TestLifecycleAdoptionPreservesCustomBranchAndEdits(t *testing.T) {
	base := t.TempDir()
	git(t, base, "init")
	configureTestGit(t, base)
	writeFile(t, filepath.Join(base, "source.txt"), "base\n")
	git(t, base, "add", "source.txt")
	git(t, base, "commit", "-m", "base")
	adopted := filepath.Join(t.TempDir(), "worktrees", "card-Existing")
	if err := os.MkdirAll(filepath.Dir(adopted), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, base, "worktree", "add", "-b", "fix/Existing-preserved", adopted)
	writeFile(t, filepath.Join(adopted, "source.txt"), "preserved dirty edit\n")
	common := gitOutput(t, base, "rev-parse", "--path-format=absolute", "--git-common-dir")

	manager, err := NewLifecycleManager(base, filepath.Dir(adopted), "codex", rules.WorktreeConfig{
		Checkout: rules.WorktreeCheckout{Mode: rules.CheckoutFull},
		Sharing:  rules.WorktreeSharing{Env: rules.SharingEnvDisabled, Skills: rules.SharingSkillsDisabled},
		Adoptions: []rules.WorktreeAdoption{{
			CardID: "Existing", Path: adopted, CommonGitDir: strings.TrimSpace(common), Branch: "fix/Existing-preserved",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.Config.Adoptions[0].Branch = "wrong/initial-branch"
	if err := manager.Adopt(context.Background(), "Existing"); err == nil {
		t.Fatal("initial adoption must verify the declared branch")
	}
	manager.Config.Adoptions[0].Branch = "fix/Existing-preserved"
	if err := manager.Adopt(context.Background(), "Existing"); err != nil {
		t.Fatal(err)
	}
	path, err := manager.ExistingOrBase(context.Background(), LifecycleRequest{CardID: "Existing", BoardID: "board1"})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, manager.BaseRepo, path)
	path, err = manager.Prepare(context.Background(), LifecycleRequest{CardID: "Existing", BoardID: "board1"})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, adopted, path)
	assertEqual(t, "fix/Existing-preserved\n", gitOutput(t, adopted, "branch", "--show-current"))
	assertEqual(t, "preserved dirty edit\n", readFile(t, filepath.Join(adopted, "source.txt")))
	assertEqual(t, "", manager.BranchContext(context.Background(), "Existing", adopted))
	assertEqual(t, "", manager.BranchContext(context.Background(), "Existing", manager.BaseRepo))

	for _, args := range [][]string{{"checkout", "-b", "fix/later-work"}, {"checkout", "--detach"}} {
		git(t, adopted, args...)
		recordPath, err := manager.recordPath(context.Background(), adopted)
		if err != nil {
			t.Fatal(err)
		}
		before := readFile(t, recordPath)
		selected, err := manager.ExistingOrBase(context.Background(), LifecycleRequest{CardID: "Existing", BoardID: "board1"})
		if err != nil {
			t.Fatal(err)
		}
		assertEqual(t, adopted, selected)
		assertContains(t, manager.BranchContext(context.Background(), "Existing", selected), "expected branch \"fix/Existing-preserved\"")
		assertEqual(t, before, readFile(t, recordPath))
		if _, err := manager.Prepare(context.Background(), LifecycleRequest{CardID: "Existing", BoardID: "board1"}); err != nil {
			t.Fatal(err)
		}
		assertEqual(t, before, readFile(t, recordPath))
		assertEqual(t, "preserved dirty edit\n", readFile(t, filepath.Join(adopted, "source.txt")))
	}
}

func TestLifecycleRejectsAliasedAdoptionPaths(t *testing.T) {
	base := t.TempDir()
	git(t, base, "init")
	configureTestGit(t, base)
	root := t.TempDir()
	path := filepath.Join(root, "card-existing")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "card-alias")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	common := gitOutput(t, base, "rev-parse", "--git-common-dir")
	if !filepath.IsAbs(common) {
		common = filepath.Join(base, common)
	}
	_, err := NewLifecycleManager(base, root, "codex", rules.WorktreeConfig{
		Checkout: rules.WorktreeCheckout{Mode: rules.CheckoutFull},
		Sharing:  rules.WorktreeSharing{Env: rules.SharingEnvDisabled, Skills: rules.SharingSkillsDisabled},
		Adoptions: []rules.WorktreeAdoption{
			{CardID: "First", Path: path, CommonGitDir: common, Branch: "feature/first"},
			{CardID: "Second", Path: alias, CommonGitDir: common, Branch: "feature/second"},
		},
	})
	if err == nil {
		t.Fatal("expected aliased adoption manifest to be rejected")
	}
	assertContains(t, err.Error(), "non-canonical alias")
}

func TestLifecyclePrepareRunsMinimalHookEnvironment(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	git(t, "", "init", "--bare", remote)
	seed := filepath.Join(t.TempDir(), "seed")
	git(t, "", "clone", remote, seed)
	configureTestGit(t, seed)
	writeFile(t, filepath.Join(seed, "source.txt"), "main\n")
	git(t, seed, "add", "source.txt")
	git(t, seed, "commit", "-m", "main")
	git(t, seed, "branch", "-M", "main")
	git(t, seed, "push", "-u", "origin", "main")
	base := filepath.Join(t.TempDir(), "base")
	git(t, "", "clone", remote, base)
	configureTestGit(t, base)

	stable := filepath.Join(t.TempDir(), "stable")
	if err := os.MkdirAll(stable, 0o755); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(stable, "prepare")
	writeFile(t, helper, "#!/bin/sh\nenv | sort > \"$KARDBRD_WORKTREE_PATH/hook.env\"\n")
	if err := os.Chmod(helper, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KARDBRD_TOKEN", "must-not-reach-hook")
	t.Setenv("ALLOWED_FOR_HOOK", "allowed")

	manager, err := NewLifecycleManager(base, filepath.Join(t.TempDir(), "worktrees"), "codex", rules.WorktreeConfig{
		Base:        rules.WorktreeBase{Remote: "origin", Ref: "refs/heads/main"},
		Helpers:     rules.WorktreeHelpers{StableRoot: stable},
		Checkout:    rules.WorktreeCheckout{Mode: rules.CheckoutFull},
		Prepare:     &rules.PrepareHook{Hook: rules.Hook{Argv: []string{helper}, TimeoutSeconds: 10}, OnCreate: true},
		Sharing:     rules.WorktreeSharing{Env: rules.SharingEnvDisabled, Skills: rules.SharingSkillsDisabled},
		Environment: rules.WorktreeEnvironment{Passthrough: []string{"ALLOWED_FOR_HOOK"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path, err := manager.Prepare(context.Background(), LifecycleRequest{CardID: "SafeEnv", BoardID: "board1"})
	if err != nil {
		t.Fatal(err)
	}
	environment := readFile(t, filepath.Join(path, "hook.env"))
	assertContains(t, environment, "ALLOWED_FOR_HOOK=allowed")
	assertContains(t, environment, "KARDBRD_CARD_ID=SafeEnv")
	assertContains(t, environment, "KARDBRD_WORKTREE_PHASE=prepare")
	if strings.Contains(environment, "KARDBRD_TOKEN=") {
		t.Fatalf("hook unexpectedly received daemon credential: %s", environment)
	}
}

func TestLifecycleDelegatedBootstrapMaterializesBeforeSkillFallback(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	git(t, "", "init", "--bare", remote)
	seed := filepath.Join(t.TempDir(), "seed")
	git(t, "", "clone", remote, seed)
	configureTestGit(t, seed)
	if err := os.MkdirAll(filepath.Join(seed, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(seed, ".agents", "skills", "repo-skill.md"), "repository skill\n")
	git(t, seed, "add", ".agents/skills/repo-skill.md")
	git(t, seed, "commit", "-m", "tracked skills")
	git(t, seed, "branch", "-M", "main")
	git(t, seed, "push", "-u", "origin", "main")
	base := filepath.Join(t.TempDir(), "base")
	git(t, "", "clone", remote, base)
	configureTestGit(t, base)

	stable := filepath.Join(t.TempDir(), "stable")
	if err := os.MkdirAll(stable, 0o755); err != nil {
		t.Fatal(err)
	}
	bootstrap := filepath.Join(stable, "bootstrap")
	writeFile(t, bootstrap, "#!/bin/sh\n[ ! -e .agents/skills ] || exit 33\ngit reset --hard HEAD\n")
	if err := os.Chmod(bootstrap, 0o700); err != nil {
		t.Fatal(err)
	}

	manager, err := NewLifecycleManager(base, filepath.Join(t.TempDir(), "worktrees"), "codex", rules.WorktreeConfig{
		Base:    rules.WorktreeBase{Remote: "origin", Ref: "refs/heads/main"},
		Helpers: rules.WorktreeHelpers{StableRoot: stable},
		Checkout: rules.WorktreeCheckout{Mode: rules.CheckoutDelegated, Bootstrap: &rules.Hook{
			Argv: []string{bootstrap}, TimeoutSeconds: 10,
		}},
		Sharing: rules.WorktreeSharing{Env: rules.SharingEnvDisabled, Skills: rules.SharingSkillsFallback},
	})
	if err != nil {
		t.Fatal(err)
	}
	path, err := manager.Prepare(context.Background(), LifecycleRequest{CardID: "Delegated", BoardID: "board1"})
	if err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(path, ".agents", "skills")
	info, err := os.Lstat(skill)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("fallback link hid tracked repository skills")
	}
	assertEqual(t, "repository skill\n", readFile(t, filepath.Join(skill, "repo-skill.md")))
}

func TestLifecycleCreatedRecoveryCompletesPrepareEvenWhenReuseDisabled(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	git(t, "", "init", "--bare", remote)
	seed := filepath.Join(t.TempDir(), "seed")
	git(t, "", "clone", remote, seed)
	configureTestGit(t, seed)
	writeFile(t, filepath.Join(seed, "source.txt"), "main\n")
	git(t, seed, "add", "source.txt")
	git(t, seed, "commit", "-m", "main")
	git(t, seed, "branch", "-M", "main")
	git(t, seed, "push", "-u", "origin", "main")
	base := filepath.Join(t.TempDir(), "base")
	git(t, "", "clone", remote, base)
	configureTestGit(t, base)
	stable := filepath.Join(t.TempDir(), "stable")
	if err := os.MkdirAll(stable, 0o755); err != nil {
		t.Fatal(err)
	}
	bootstrap := filepath.Join(stable, "bootstrap")
	writeFile(t, bootstrap, "#!/bin/sh\nif [ ! -e '"+filepath.Join(stable, "attempted")+"' ]; then touch '"+filepath.Join(stable, "attempted")+"'; exit 1; fi\ngit reset --hard HEAD\n")
	prepare := filepath.Join(stable, "prepare")
	writeFile(t, prepare, "#!/bin/sh\ntouch prepared\n")
	for _, helper := range []string{bootstrap, prepare} {
		if err := os.Chmod(helper, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := NewLifecycleManager(base, filepath.Join(t.TempDir(), "worktrees"), "codex", rules.WorktreeConfig{
		Base:     rules.WorktreeBase{Remote: "origin", Ref: "refs/heads/main"},
		Helpers:  rules.WorktreeHelpers{StableRoot: stable},
		Checkout: rules.WorktreeCheckout{Mode: rules.CheckoutDelegated, Bootstrap: &rules.Hook{Argv: []string{bootstrap}, TimeoutSeconds: 10}},
		Prepare:  &rules.PrepareHook{Hook: rules.Hook{Argv: []string{prepare}, TimeoutSeconds: 10}, OnCreate: true, OnReuse: false},
		Sharing:  rules.WorktreeSharing{Env: rules.SharingEnvDisabled, Skills: rules.SharingSkillsDisabled},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Prepare(context.Background(), LifecycleRequest{CardID: "Recover", BoardID: "board1"}); err == nil {
		t.Fatal("expected first bootstrap failure")
	}
	path, err := manager.Prepare(context.Background(), LifecycleRequest{CardID: "Recover", BoardID: "board1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, "prepared")); err != nil {
		t.Fatalf("create prepare was skipped during recovery: %v", err)
	}
}

func TestLifecycleRunnerReapsDescendantsOnCancellation(t *testing.T) {
	base := t.TempDir()
	git(t, base, "init")
	configureTestGit(t, base)
	manager, err := NewLifecycleManager(base, filepath.Join(t.TempDir(), "worktrees"), "codex", rules.WorktreeConfig{
		Checkout: rules.WorktreeCheckout{Mode: rules.CheckoutFull},
		Sharing:  rules.WorktreeSharing{Env: rules.SharingEnvDisabled, Skills: rules.SharingSkillsDisabled},
	})
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "descendant-ran")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = manager.run(ctx, base, []string{"/bin/sh", "-c", "(sleep 0.2; touch '" + output + "') & sleep 5"})
	if err == nil {
		t.Fatal("expected canceled process group")
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("descendant survived cancellation: %v", err)
	}
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

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func configureTestGit(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "config", "user.email", "worktree-test@example.test")
	git(t, dir, "config", "user.name", "Worktree Test")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
