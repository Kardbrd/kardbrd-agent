package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kardbrd/kardbrd-agent/internal/rules"
	"github.com/Kardbrd/kardbrd-agent/internal/worktree"
)

// Use real Git and lifecycle selection so this catches rejection before the
// executor as well as losing the advisory on any of the dispatch paths.
func TestBranchDriftReachesExecutorWithContextAndPreservesWork(t *testing.T) {
	for _, detached := range []bool{false, true} {
		for _, trigger := range []string{"mention", "prepare command", "existing command", "automation"} {
			name := trigger
			if detached {
				name += " detached"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				base := t.TempDir()
				runGit(t, base, "init", "-b", "main")
				runGit(t, base, "config", "user.email", "test@example.test")
				runGit(t, base, "config", "user.name", "Test")
				writeBranchTestFile(t, base, "source.txt", "base\n")
				runGit(t, base, "add", ".")
				runGit(t, base, "commit", "-m", "base")
				runGit(t, base, "remote", "add", "origin", base)
				lifecycle, err := worktree.NewLifecycleManager(base, t.TempDir(), "codex", rules.WorktreeConfig{
					Base:     rules.WorktreeBase{Remote: "origin", Ref: "refs/heads/main"},
					Checkout: rules.WorktreeCheckout{Mode: rules.CheckoutFull},
					Sharing:  rules.WorktreeSharing{Env: rules.SharingEnvDisabled, Skills: rules.SharingSkillsDisabled},
				})
				if err != nil {
					t.Fatal(err)
				}
				path, err := lifecycle.Prepare(ctx, worktree.LifecycleRequest{CardID: "card1", BoardID: "board1"})
				if err != nil {
					t.Fatal(err)
				}
				assertEqual(t, "", lifecycle.BranchContext(ctx, "card1", path))
				runGit(t, path, "checkout", "-b", "fix/recovery")
				writeBranchTestFile(t, path, "source.txt", "feature commit\n")
				runGit(t, path, "commit", "-am", "feature")
				if detached {
					runGit(t, path, "checkout", "--detach")
				}
				writeBranchTestFile(t, path, "source.txt", "staged edit\n")
				runGit(t, path, "add", "source.txt")
				writeBranchTestFile(t, path, "source.txt", "unstaged edit\n")
				writeBranchTestFile(t, path, "untracked.txt", "untracked work\n")
				checkout := exec.Command("git", "checkout", "card/card1")
				checkout.Dir = path
				if output, err := checkout.CombinedOutput(); err == nil {
					t.Fatalf("fixture must prevent switching to expected branch: %s", output)
				}
				beforeStatus := gitStatus(t, path)
				beforeTree := snapshotTree(t, path)
				beforeDiff := branchTestGitOutput(t, path, "diff", "--cached")
				beforeHead := branchTestGitOutput(t, path, "rev-parse", "HEAD")
				beforeBranch := branchTestGitOutput(t, path, "rev-parse", "--abbrev-ref", "HEAD")

				manager := newTestManager(t)
				manager.Worktree = realLifecycleWorktree{lifecycle}
				manager.Client.(*fakeBoardClient).card = rawJSON(t, map[string]any{"list": map[string]any{"name": "In Progress"}})
				event := map[string]any{"event_type": "comment_created", "card_id": "card1", "comment_id": "comment1", "content": "@coder inspect this checkout", "author_name": "Paul"}
				switch trigger {
				case "prepare command", "existing command":
					policy := rules.ExecutionPrepare
					if trigger == "existing command" {
						policy = rules.ExecutionExistingOrBase
					}
					manager.Rules = &rules.Engine{Rules: []rules.Rule{{Name: "Inspect", Events: []string{"comment_created"}, CommentCommand: "/inspect", Execution: policy, Action: "/inspect"}}}
					event["content"] = "@coder /inspect"
				case "automation":
					manager.Rules = &rules.Engine{Rules: []rules.Rule{{Name: "Inspect", Events: []string{"card_moved"}, Action: "inspect this checkout"}}}
					event = map[string]any{"event_type": "card_moved", "card_id": "card1", "list_name": "In Progress"}
				}
				if err := manager.HandleBoardEvent(ctx, event); err != nil {
					t.Fatal(err)
				}
				executor := manager.Executor.(*fakeExecutor)
				assertEqual(t, 1, executor.executeCount)
				assertEqual(t, path, executor.lastExecuteRequest.CWD)
				assertContains(t, executor.lastExecuteRequest.Prompt, "expected branch \"card/card1\"")
				actual := "branch \"fix/recovery\""
				if detached {
					actual = "detached HEAD"
				}
				assertContains(t, executor.lastExecuteRequest.Prompt, actual)
				assertContains(t, executor.lastExecuteRequest.Prompt, path)
				assertEqual(t, beforeStatus, gitStatus(t, path))
				assertStringMapEqual(t, beforeTree, snapshotTree(t, path))
				assertEqual(t, beforeDiff, branchTestGitOutput(t, path, "diff", "--cached"))
				assertEqual(t, beforeHead, branchTestGitOutput(t, path, "rev-parse", "HEAD"))
				assertEqual(t, beforeBranch, branchTestGitOutput(t, path, "rev-parse", "--abbrev-ref", "HEAD"))
				assertContains(t, lifecycle.BranchContext(ctx, "card1", path), "expected branch \"card/card1\"")
			})
		}
	}
}

type realLifecycleWorktree struct{ *worktree.LifecycleManager }

func (w realLifecycleWorktree) Prepare(ctx context.Context, cardID, boardID string) (string, error) {
	return w.LifecycleManager.Prepare(ctx, worktree.LifecycleRequest{CardID: cardID, BoardID: boardID})
}

func (w realLifecycleWorktree) ExistingOrBase(ctx context.Context, cardID, boardID string) (string, error) {
	return w.LifecycleManager.ExistingOrBase(ctx, worktree.LifecycleRequest{CardID: cardID, BoardID: boardID})
}

func writeBranchTestFile(t *testing.T, path, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(path, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func branchTestGitOutput(t *testing.T, path string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = path
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s: %v", strings.Join(args, " "), output, err)
	}
	return string(output)
}
