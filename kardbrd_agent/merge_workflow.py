"""MergeWorkflow - Structured merge workflow for kardbrd cards.

This module implements a state machine-based merge workflow that:
1. Checks for active Claude sessions
2. Verifies worktree exists
3. Commits any uncommitted changes (LLM)
4. Rebases onto main (with LLM conflict resolution)
5. Runs tests (with LLM fix loop)
6. Squash merges to main
7. Cleans up worktree and branch
"""

import logging
from dataclasses import dataclass, field
from enum import Enum
from pathlib import Path
from typing import TYPE_CHECKING

from .merge_tools import (
    CheckSessionResult,
    CheckWorktreeResult,
    CommitCountResult,
    GitFetchResult,
    GitMergeSquashResult,
    GitRebaseResult,
    GitStatusResult,
    MergeTools,
    UpdateTargetResult,
)

if TYPE_CHECKING:
    from kardbrd_client import KardbrdClient

    from .executor import ClaudeExecutor

logger = logging.getLogger("kardbrd_agent.merge")


class MergeStatus(Enum):
    """Final status of a merge workflow."""

    MERGED = "merged"  # Success
    EMPTY = "empty"  # No commits to merge
    STALE = "stale"  # Already merged after rebase
    CONFLICT = "conflict"  # Rebase/merge conflict (LLM failed to resolve)
    UNCOMMITTED = "uncommitted"  # Had uncommitted changes (LLM failed to commit)
    NO_WORKTREE = "no_worktree"  # Worktree doesn't exist
    NO_BRANCH = "no_branch"  # Branch doesn't exist
    TESTS_FAILED = "tests_failed"  # Tests failed (LLM failed to fix)
    CLAUDE_ERROR = "claude_error"  # Claude failed during commit/fix
    SESSION_ACTIVE = "session_active"  # Claude session still running
    FETCH_FAILED = "fetch_failed"  # Git fetch failed
    UPDATE_FAILED = "update_failed"  # Update target branch failed
    REBASE_FAILED = "rebase_failed"  # Rebase failed (non-conflict error)
    MERGE_FAILED = "merge_failed"  # Squash merge failed


class MergeStep(Enum):
    """Steps in the merge workflow."""

    CHECK_SESSION = "check_session"
    CHECK_WORKTREE = "check_worktree"
    CHECK_UNCOMMITTED = "check_uncommitted"
    COMMIT_UNCOMMITTED = "commit_uncommitted"  # LLM
    FETCH = "fetch"
    UPDATE_TARGET = "update_target"
    REBASE = "rebase"
    RESOLVE_CONFLICTS = "resolve_conflicts"  # LLM
    COUNT_COMMITS = "count_commits"
    RUN_TESTS = "run_tests"
    FIX_TESTS = "fix_tests"  # LLM
    SQUASH_MERGE = "squash_merge"
    CREATE_COMMIT = "create_commit"  # LLM
    CLEANUP_WORKTREE = "cleanup_worktree"
    DELETE_BRANCH = "delete_branch"
    POST_COMMENT = "post_comment"


@dataclass
class StepResult:
    """Result from executing a workflow step."""

    step: MergeStep
    success: bool
    data: dict = field(default_factory=dict)
    error: str | None = None
    requires_llm: bool = False
    report_to_card: bool = False


@dataclass
class WorkflowState:
    """Tracks state during merge workflow execution."""

    card_id: str
    card_title: str = ""
    worktree_path: Path | None = None
    branch_name: str = ""
    main_repo_path: Path | None = None
    current_step: MergeStep = MergeStep.CHECK_SESSION
    commits: list[dict] = field(default_factory=list)
    commit_count: int = 0
    final_commit_hash: str | None = None
    checkpoints: list[dict] = field(default_factory=list)
    llm_attempts: dict = field(default_factory=dict)  # Track LLM retry attempts

    def log_checkpoint(self, step: MergeStep, result: StepResult) -> None:
        """Log a checkpoint for audit trail."""
        import time

        checkpoint = {
            "timestamp": time.time(),
            "step": step.value,
            "success": result.success,
            "data": result.data,
            "error": result.error,
        }
        self.checkpoints.append(checkpoint)
        logger.debug(f"Checkpoint: {step.value} - success={result.success}")


# Maximum LLM fix attempts before giving up
MAX_LLM_ATTEMPTS = 3


class MergeWorkflow:
    """
    Orchestrates the merge workflow for a card.

    Uses structured tool calls with independent verification.
    LLM is only engaged for complex decisions:
    - Committing uncommitted changes
    - Resolving rebase conflicts
    - Fixing failing tests
    - Creating final commit message
    """

    def __init__(
        self,
        card_id: str,
        card_title: str,
        main_repo_path: Path,
        client: "KardbrdClient",
        executor: "ClaudeExecutor",
        test_command: str = "make test",
    ):
        """
        Initialize the merge workflow.

        Args:
            card_id: The card ID to merge
            card_title: The card title (for commit message)
            main_repo_path: Path to the main git repository
            client: KardbrdClient for posting comments
            executor: ClaudeExecutor for LLM operations
            test_command: Command to run tests (default: make test)
        """
        self.card_id = card_id
        self.card_title = card_title
        self.main_repo_path = main_repo_path
        self.client = client
        self.executor = executor
        self.test_command = test_command

        # Initialize tools
        self.tools = MergeTools(main_repo_path)

        # Initialize state
        self.state = WorkflowState(
            card_id=card_id,
            card_title=card_title,
            main_repo_path=main_repo_path,
        )

    async def run(self) -> MergeStatus:
        """
        Execute the merge workflow.

        Returns:
            MergeStatus indicating the final result
        """
        logger.info(f"Starting merge workflow for card {self.card_id}")

        try:
            # Pre-merge validation
            status = await self._run_pre_merge_checks()
            if status:
                return status

            # Core merge operations
            status = await self._run_merge_operations()
            if status:
                return status

            # Steps 11-12: Cleanup
            await self._cleanup()

            # Step 13: Post success comment
            await self._post_status_comment(MergeStatus.MERGED)

            return MergeStatus.MERGED

        except Exception as e:
            logger.exception(f"Merge workflow failed: {e}")
            await self._post_error_comment(str(e))
            return MergeStatus.CLAUDE_ERROR

    async def _run_pre_merge_checks(self) -> MergeStatus | None:
        """Run pre-merge validation steps. Returns status if should abort, None to continue."""
        # Step 1: Check Claude session
        result = await self._step_check_session()
        if not result.success:
            return MergeStatus.SESSION_ACTIVE

        # Step 2: Check worktree exists
        result = await self._step_check_worktree()
        if not result.success:
            return MergeStatus.NO_WORKTREE

        # Step 3: Check uncommitted changes
        result = await self._step_check_uncommitted()
        if result.data.get("has_changes"):
            # Step 3a: Commit uncommitted changes (LLM)
            result = await self._step_commit_uncommitted()
            if not result.success:
                return MergeStatus.UNCOMMITTED

        return None

    async def _run_merge_operations(self) -> MergeStatus | None:
        """Run core merge operations. Returns status if should abort, None to continue."""
        # Step 4: Git fetch
        result = await self._step_fetch()
        if not result.success:
            return MergeStatus.FETCH_FAILED

        # Step 5: Update target branch
        result = await self._step_update_target()
        if not result.success:
            return MergeStatus.UPDATE_FAILED

        # Step 6: Rebase (with potential conflict resolution loop)
        result = await self._step_rebase_with_conflicts()
        if not result.success:
            return (
                MergeStatus.CONFLICT if result.data.get("conflict") else MergeStatus.REBASE_FAILED
            )

        # Step 7: Count commits
        result = await self._step_count_commits()
        if result.data.get("count", 0) == 0:
            await self._post_status_comment(MergeStatus.STALE)
            await self._cleanup()
            return MergeStatus.STALE

        # Step 8: Run tests (with potential fix loop)
        result = await self._step_test_with_fixes()
        if not result.success:
            return MergeStatus.TESTS_FAILED

        # Step 9: Squash merge
        result = await self._step_squash_merge()
        if not result.success:
            return MergeStatus.MERGE_FAILED

        # Step 10: Create squash commit (LLM)
        result = await self._step_create_commit()
        if not result.success:
            return MergeStatus.CLAUDE_ERROR

        return None

    async def _step_check_session(self) -> StepResult:
        """Step 1: Check if Claude session is active."""
        logger.info(f"Step 1: Checking Claude session for card {self.card_id}")

        result: CheckSessionResult = self.tools.check_session(self.card_id)

        step_result = StepResult(
            step=MergeStep.CHECK_SESSION,
            success=not result.active,
            data={"active": result.active, "pid": result.pid},
            error="Claude session is still active" if result.active else None,
        )

        self.state.log_checkpoint(MergeStep.CHECK_SESSION, step_result)

        if result.active:
            logger.warning(f"Claude session active for card {self.card_id}")
            await self._post_status_comment(MergeStatus.SESSION_ACTIVE)

        return step_result

    async def _step_check_worktree(self) -> StepResult:
        """Step 2: Check if worktree exists."""
        logger.info(f"Step 2: Checking worktree for card {self.card_id}")

        result: CheckWorktreeResult = self.tools.check_worktree(self.card_id)

        step_result = StepResult(
            step=MergeStep.CHECK_WORKTREE,
            success=result.exists,
            data={
                "exists": result.exists,
                "path": str(result.path) if result.path else None,
                "branch": result.branch,
            },
            error="Worktree does not exist" if not result.exists else None,
        )

        self.state.log_checkpoint(MergeStep.CHECK_WORKTREE, step_result)

        if result.exists:
            self.state.worktree_path = result.path
            self.state.branch_name = result.branch or ""
        else:
            logger.warning(f"No worktree for card {self.card_id}")
            await self._post_status_comment(MergeStatus.NO_WORKTREE)

        return step_result

    async def _step_check_uncommitted(self) -> StepResult:
        """Step 3: Check for uncommitted changes."""
        logger.info("Step 3: Checking for uncommitted changes")

        result: GitStatusResult = self.tools.git_status(self.state.worktree_path)

        step_result = StepResult(
            step=MergeStep.CHECK_UNCOMMITTED,
            success=True,  # This step always succeeds
            data={
                "has_changes": result.has_changes,
                "files": result.files,
            },
        )

        self.state.log_checkpoint(MergeStep.CHECK_UNCOMMITTED, step_result)
        return step_result

    async def _step_commit_uncommitted(self) -> StepResult:
        """Step 3a: Commit uncommitted changes using LLM."""
        logger.info("Step 3a: Committing uncommitted changes (LLM)")

        # Build prompt for Claude to commit changes
        prompt = f"""You have uncommitted changes in a git worktree \
that need to be committed before merging.

Card ID: {self.card_id}
Worktree path: {self.state.worktree_path}

Please:
1. Review the uncommitted changes with `git status` and `git diff`
2. Stage appropriate files with `git add`
3. Create a commit with an appropriate message

The commit message should:
- Be concise and descriptive
- Reference the card ID: {self.card_id}
- End with: Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>

After committing, verify with `git status` that the working directory is clean."""

        result = await self.executor.execute(prompt, cwd=self.state.worktree_path)

        # Verify: check git status is clean
        status_result = self.tools.git_status(self.state.worktree_path)

        step_result = StepResult(
            step=MergeStep.COMMIT_UNCOMMITTED,
            success=result.success and not status_result.has_changes,
            data={
                "claude_success": result.success,
                "still_has_changes": status_result.has_changes,
            },
            error=result.error if not result.success else None,
            requires_llm=True,
        )

        self.state.log_checkpoint(MergeStep.COMMIT_UNCOMMITTED, step_result)
        return step_result

    async def _step_fetch(self) -> StepResult:
        """Step 4: Git fetch."""
        logger.info("Step 4: Fetching from origin")

        result: GitFetchResult = self.tools.git_fetch(self.state.worktree_path)

        step_result = StepResult(
            step=MergeStep.FETCH,
            success=result.success,
            data={"refs_updated": result.refs_updated},
            error=result.stderr if not result.success else None,
        )

        self.state.log_checkpoint(MergeStep.FETCH, step_result)
        return step_result

    async def _step_update_target(self) -> StepResult:
        """Step 5: Update target branch (main)."""
        logger.info("Step 5: Updating target branch (main)")

        result: UpdateTargetResult = self.tools.update_target_branch(self.main_repo_path)

        step_result = StepResult(
            step=MergeStep.UPDATE_TARGET,
            success=result.success,
            data={"old_head": result.old_head, "new_head": result.new_head},
            error=result.error if not result.success else None,
        )

        self.state.log_checkpoint(MergeStep.UPDATE_TARGET, step_result)
        return step_result

    async def _step_rebase_with_conflicts(self) -> StepResult:
        """Step 6: Rebase onto main with conflict resolution."""
        logger.info("Step 6: Rebasing onto main")

        result: GitRebaseResult = self.tools.git_rebase(self.state.worktree_path, onto="main")

        if result.conflict:
            # Try LLM conflict resolution
            logger.warning(f"Rebase conflicts: {result.conflict_files}")
            resolve_result = await self._step_resolve_conflicts(result.conflict_files)

            if not resolve_result.success:
                # Abort rebase
                self.tools._run_git_command(["rebase", "--abort"], cwd=self.state.worktree_path)
                await self._post_conflict_comment(result.conflict_files)

            return resolve_result

        step_result = StepResult(
            step=MergeStep.REBASE,
            success=result.success,
            data={"conflict": result.conflict},
            error=result.stderr if not result.success else None,
        )

        self.state.log_checkpoint(MergeStep.REBASE, step_result)
        return step_result

    async def _step_resolve_conflicts(self, conflict_files: list[str]) -> StepResult:
        """Step 6a: Resolve rebase conflicts using LLM."""
        logger.info(f"Step 6a: Resolving conflicts in {conflict_files} (LLM)")

        prompt = f"""You are in the middle of a git rebase that has conflicts.

Card ID: {self.card_id}
Worktree path: {self.state.worktree_path}
Conflicting files: {", ".join(conflict_files)}

Please:
1. Review each conflicting file
2. Resolve the conflicts appropriately (keep functionality from both sides where possible)
3. Stage resolved files with `git add`
4. Continue the rebase with `git rebase --continue`

If a conflict cannot be resolved automatically, explain why."""

        result = await self.executor.execute(prompt, cwd=self.state.worktree_path)

        # Verify: check rebase is complete
        rebase_in_progress = (self.state.worktree_path / ".git" / "rebase-merge").exists() or (
            self.state.worktree_path / ".git" / "rebase-apply"
        ).exists()

        step_result = StepResult(
            step=MergeStep.RESOLVE_CONFLICTS,
            success=result.success and not rebase_in_progress,
            data={
                "conflict_files": conflict_files,
                "rebase_complete": not rebase_in_progress,
            },
            error=result.error if not result.success else None,
            requires_llm=True,
            report_to_card=True,
        )

        self.state.log_checkpoint(MergeStep.RESOLVE_CONFLICTS, step_result)
        return step_result

    async def _step_count_commits(self) -> StepResult:
        """Step 7: Count commits after rebase."""
        logger.info("Step 7: Counting commits")

        result: CommitCountResult = self.tools.git_rev_list_count(self.state.worktree_path)

        step_result = StepResult(
            step=MergeStep.COUNT_COMMITS,
            success=True,
            data={"count": result.count, "commits": result.commits},
        )

        self.state.commit_count = result.count
        self.state.commits = result.commits

        self.state.log_checkpoint(MergeStep.COUNT_COMMITS, step_result)
        return step_result

    async def _step_test_with_fixes(self) -> StepResult:
        """Step 8: Run tests with LLM fix loop."""
        logger.info("Step 8: Running tests")

        for attempt in range(MAX_LLM_ATTEMPTS):
            test_result = self.tools.run_tests(self.state.worktree_path, self.test_command)

            if test_result.success:
                step_result = StepResult(
                    step=MergeStep.RUN_TESTS,
                    success=True,
                    data={"exit_code": test_result.exit_code, "attempt": attempt + 1},
                )
                self.state.log_checkpoint(MergeStep.RUN_TESTS, step_result)
                return step_result

            # Tests failed - try LLM fix
            logger.warning(f"Tests failed (attempt {attempt + 1}), trying LLM fix")

            fix_result = await self._step_fix_tests(
                test_result.failed_tests, test_result.stdout + test_result.stderr
            )

            if not fix_result.success:
                break

            # Re-rebase to ensure fixes are on top of latest main
            rebase_result = self.tools.git_rebase(self.state.worktree_path, onto="main")
            if not rebase_result.success:
                break

        # All attempts exhausted
        step_result = StepResult(
            step=MergeStep.RUN_TESTS,
            success=False,
            data={
                "exit_code": test_result.exit_code,
                "failed_tests": test_result.failed_tests,
                "attempts": attempt + 1,
            },
            error="Tests failed after max attempts",
        )

        self.state.log_checkpoint(MergeStep.RUN_TESTS, step_result)
        await self._post_test_failure_comment(test_result.failed_tests, test_result.stdout)
        return step_result

    async def _step_fix_tests(self, failed_tests: list[str], test_output: str) -> StepResult:
        """Step 8a: Fix failing tests using LLM."""
        logger.info(f"Step 8a: Fixing tests: {failed_tests} (LLM)")

        prompt = f"""Tests are failing and need to be fixed before merging.

Card ID: {self.card_id}
Worktree path: {self.state.worktree_path}
Failed tests: {", ".join(failed_tests) if failed_tests else "See output below"}

Test output:
```
{test_output[:5000]}
```

Please:
1. Analyze the test failures
2. Fix the code (not the tests, unless tests are wrong)
3. Commit your fixes with an appropriate message
4. End commit message with: Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>

Focus on fixing the actual bugs, not just making tests pass."""

        result = await self.executor.execute(prompt, cwd=self.state.worktree_path)

        step_result = StepResult(
            step=MergeStep.FIX_TESTS,
            success=result.success,
            data={"failed_tests": failed_tests},
            error=result.error if not result.success else None,
            requires_llm=True,
            report_to_card=True,
        )

        self.state.log_checkpoint(MergeStep.FIX_TESTS, step_result)
        return step_result

    async def _step_squash_merge(self) -> StepResult:
        """Step 9: Squash merge to main."""
        logger.info("Step 9: Squash merge")

        result: GitMergeSquashResult = self.tools.git_merge_squash(
            self.main_repo_path, self.state.branch_name
        )

        step_result = StepResult(
            step=MergeStep.SQUASH_MERGE,
            success=result.success,
            data={"staged_files": result.staged_files},
            error=result.error if not result.success else None,
        )

        self.state.log_checkpoint(MergeStep.SQUASH_MERGE, step_result)
        return step_result

    async def _step_create_commit(self) -> StepResult:
        """Step 10: Create squash commit using LLM."""
        logger.info("Step 10: Creating squash commit (LLM)")

        commits_summary = "\n".join(f"- {c['hash'][:7]} {c['message']}" for c in self.state.commits)

        prompt = f"""Create a squash commit for the merged changes.

Card ID: {self.card_id}
Card Title: {self.card_title}
Commits being squashed:
{commits_summary}

Create a commit with this format:
```
{self.card_id}: {self.card_title}

Squashed commits:
{commits_summary}

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
```

Use `git commit` to create the commit. The changes are already staged."""

        result = await self.executor.execute(prompt, cwd=self.main_repo_path)

        # Verify: check commit was created
        import subprocess

        verify = subprocess.run(
            ["git", "log", "-1", "--format=%H %s"],
            cwd=self.main_repo_path,
            capture_output=True,
            text=True,
        )

        commit_created = self.card_id in verify.stdout

        step_result = StepResult(
            step=MergeStep.CREATE_COMMIT,
            success=result.success and commit_created,
            data={"commit_created": commit_created},
            error=result.error if not result.success else None,
            requires_llm=True,
        )

        if commit_created:
            self.state.final_commit_hash = verify.stdout.split()[0]

        self.state.log_checkpoint(MergeStep.CREATE_COMMIT, step_result)
        return step_result

    async def _cleanup(self) -> None:
        """Steps 11-12: Cleanup worktree and branch."""
        logger.info("Cleanup: Removing worktree and branch")

        # Step 11: Remove worktree
        try:
            self.tools.remove_worktree(self.state.worktree_path)
        except Exception as e:
            logger.warning(f"Failed to remove worktree: {e}")

        # Step 12: Delete branch
        try:
            self.tools.delete_branch(self.main_repo_path, self.state.branch_name)
        except Exception as e:
            logger.warning(f"Failed to delete branch: {e}")

    async def _post_status_comment(self, status: MergeStatus) -> None:
        """Post final status comment to card."""
        if status == MergeStatus.MERGED:
            content = f"""**Merged successfully**

- **Branch:** {self.state.branch_name}
- **Commits squashed:** {self.state.commit_count}
- **Tests:** Passed
- **Commit:** {self.state.final_commit_hash[:7] if self.state.final_commit_hash else "N/A"}
- **Worktree:** Cleaned up"""

        elif status == MergeStatus.SESSION_ACTIVE:
            content = """**Merge blocked**

Claude session is still active for this card. Please wait for the \
session to complete or stop it with `@botName stop`."""

        elif status == MergeStatus.NO_WORKTREE:
            content = """**Merge blocked**

No worktree found for this card. The card may not have been worked on yet."""

        elif status == MergeStatus.STALE:
            content = """**No changes to merge**

The branch has no commits ahead of main after rebasing. The work may have already been merged."""

        else:
            content = f"""**Merge failed**

Status: {status.value}

Check the workflow logs for details."""

        try:
            self.client.add_comment(self.card_id, content)
        except Exception as e:
            logger.error(f"Failed to post status comment: {e}")

    async def _post_conflict_comment(self, conflict_files: list[str]) -> None:
        """Post conflict details to card."""
        files_list = "\n".join(f"- {f}" for f in conflict_files)
        content = f"""**Merge conflict**

The following files have conflicts that could not be automatically resolved:

{files_list}

Please resolve the conflicts manually and retry."""

        try:
            self.client.add_comment(self.card_id, content)
        except Exception as e:
            logger.error(f"Failed to post conflict comment: {e}")

    async def _post_test_failure_comment(self, failed_tests: list[str], output: str) -> None:
        """Post test failure details to card."""
        tests_list = "\n".join(f"- {t}" for t in failed_tests) if failed_tests else "See output"
        output_truncated = output[:2000] + "..." if len(output) > 2000 else output

        content = f"""**Tests failed**

Failed tests:
{tests_list}

Output:
```
{output_truncated}
```

Please fix the tests and retry."""

        try:
            self.client.add_comment(self.card_id, content)
        except Exception as e:
            logger.error(f"Failed to post test failure comment: {e}")

    async def _post_error_comment(self, error: str) -> None:
        """Post error details to card."""
        content = f"""**Merge workflow error**

```
{error}
```"""

        try:
            self.client.add_comment(self.card_id, content)
        except Exception as e:
            logger.error(f"Failed to post error comment: {e}")
