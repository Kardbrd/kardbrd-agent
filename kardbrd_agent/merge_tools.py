"""Git operation tools for merge workflow.

Each tool has:
- Defined inputs/outputs
- Independent verification
- Deterministic behavior (no LLM involvement)
"""

import logging
import subprocess
from dataclasses import dataclass
from pathlib import Path

logger = logging.getLogger("kardbrd_agent.merge_tools")


@dataclass
class CheckSessionResult:
    """Result from checking Claude session status."""

    active: bool
    pid: int | None = None
    uptime_seconds: int | None = None


@dataclass
class CheckWorktreeResult:
    """Result from checking if worktree exists."""

    exists: bool
    path: Path | None = None
    branch: str | None = None


@dataclass
class GitStatusResult:
    """Result from git status check."""

    has_changes: bool
    files: list[str]
    porcelain: str


@dataclass
class GitFetchResult:
    """Result from git fetch."""

    success: bool
    stderr: str
    refs_updated: list[str]


@dataclass
class UpdateTargetResult:
    """Result from updating target branch."""

    success: bool
    old_head: str | None = None
    new_head: str | None = None
    error: str | None = None


@dataclass
class GitRebaseResult:
    """Result from git rebase."""

    success: bool
    conflict: bool
    conflict_files: list[str] | None = None
    stderr: str = ""


@dataclass
class CommitCountResult:
    """Result from counting commits."""

    count: int
    commits: list[dict]  # List of {hash: str, message: str}


@dataclass
class RunTestsResult:
    """Result from running tests."""

    success: bool
    exit_code: int
    stdout: str
    stderr: str
    failed_tests: list[str]


@dataclass
class GitMergeSquashResult:
    """Result from git merge --squash."""

    success: bool
    staged_files: list[str]
    error: str | None = None


class MergeTools:
    """
    Git operation tools for merge workflow.

    All tools are deterministic and independently verifiable.
    """

    def __init__(self, main_repo_path: Path):
        """
        Initialize merge tools.

        Args:
            main_repo_path: Path to the main git repository
        """
        self.main_repo_path = Path(main_repo_path).resolve()
        self.worktrees_base = self.main_repo_path.parent

    def _get_short_id(self, card_id: str) -> str:
        """Get truncated card ID (first 8 characters)."""
        return card_id[:8]

    def _get_worktree_path(self, card_id: str) -> Path:
        """Get the worktree path for a card (sibling directory)."""
        short_id = self._get_short_id(card_id)
        return self.worktrees_base / f"kbn-{short_id}"

    def _get_branch_name(self, card_id: str) -> str:
        """Get the branch name for a card."""
        short_id = self._get_short_id(card_id)
        return f"card/{short_id}"

    def _run_git_command(
        self,
        args: list[str],
        cwd: Path | None = None,
        check: bool = True,
    ) -> subprocess.CompletedProcess:
        """
        Run a git command.

        Args:
            args: Git command arguments (without 'git')
            cwd: Working directory (defaults to main_repo_path)
            check: Raise exception on non-zero exit code

        Returns:
            CompletedProcess with stdout/stderr
        """
        cmd = ["git", *args]
        working_dir = cwd or self.main_repo_path

        logger.debug(f"Running: {' '.join(cmd)} in {working_dir}")

        return subprocess.run(
            cmd,
            cwd=working_dir,
            capture_output=True,
            text=True,
            check=check,
        )

    def check_session(self, card_id: str) -> CheckSessionResult:
        """
        Check if a Claude session is active for the card.

        Verification: Check PID file and process status.

        Args:
            card_id: The card ID to check

        Returns:
            CheckSessionResult with session status
        """
        worktree_path = self._get_worktree_path(card_id)
        short_id = self._get_short_id(card_id)

        # Check for PID file in .claude directory
        pid_file = worktree_path / ".claude" / f"session-{short_id}.pid"

        if not pid_file.exists():
            return CheckSessionResult(active=False)

        try:
            pid = int(pid_file.read_text().strip())

            # Check if process is running
            import os

            os.kill(pid, 0)  # Signal 0 just checks if process exists

            return CheckSessionResult(active=True, pid=pid)

        except (ValueError, ProcessLookupError, PermissionError):
            # PID file exists but process is not running
            return CheckSessionResult(active=False)

    def check_worktree(self, card_id: str) -> CheckWorktreeResult:
        """
        Check if a worktree exists for the card.

        Verification: Parse `git worktree list --porcelain`.

        Args:
            card_id: The card ID to check

        Returns:
            CheckWorktreeResult with worktree details
        """
        expected_path = self._get_worktree_path(card_id)

        # Check if directory exists
        if not expected_path.exists():
            return CheckWorktreeResult(exists=False)

        # Verify it's a git worktree
        try:
            result = self._run_git_command(["worktree", "list", "--porcelain"])

            # Parse porcelain output
            worktrees = {}
            current_path = None

            for line in result.stdout.split("\n"):
                if line.startswith("worktree "):
                    current_path = line[9:]
                elif line.startswith("branch ") and current_path:
                    branch = line[7:]
                    worktrees[current_path] = branch

            # Check if our worktree is in the list
            str_path = str(expected_path)
            if str_path in worktrees:
                return CheckWorktreeResult(
                    exists=True,
                    path=expected_path,
                    branch=worktrees[str_path].replace("refs/heads/", ""),
                )

            # Directory exists but not a git worktree
            return CheckWorktreeResult(exists=False)

        except subprocess.CalledProcessError:
            return CheckWorktreeResult(exists=False)

    def git_status(self, worktree_path: Path) -> GitStatusResult:
        """
        Check git status for uncommitted changes.

        Verification: Non-empty porcelain output = has_changes.

        Args:
            worktree_path: Path to the worktree

        Returns:
            GitStatusResult with change details
        """
        result = self._run_git_command(
            ["status", "--porcelain"],
            cwd=worktree_path,
            check=False,
        )

        porcelain = result.stdout.strip()
        # Git status --porcelain format: XY filename (2 chars + space + filename)
        # Handle both " M file.py" and "?? file.py" formats
        files = []
        for line in porcelain.split("\n"):
            if line:
                # Skip the XY status prefix (2 chars) and any whitespace
                filename = line[2:].lstrip()
                if filename:
                    files.append(filename)

        return GitStatusResult(
            has_changes=bool(porcelain),
            files=files,
            porcelain=porcelain,
        )

    def git_fetch(self, worktree_path: Path, remote: str = "origin") -> GitFetchResult:
        """
        Fetch from remote.

        Verification: Exit code = 0.

        Args:
            worktree_path: Path to the worktree
            remote: Remote name (default: origin)

        Returns:
            GitFetchResult with fetch details
        """
        try:
            result = self._run_git_command(
                ["fetch", remote],
                cwd=worktree_path,
            )

            # Parse stderr for updated refs
            refs_updated = []
            for line in result.stderr.split("\n"):
                if " -> " in line:
                    refs_updated.append(line.strip())

            return GitFetchResult(
                success=True,
                stderr=result.stderr,
                refs_updated=refs_updated,
            )

        except subprocess.CalledProcessError as e:
            return GitFetchResult(
                success=False,
                stderr=e.stderr,
                refs_updated=[],
            )

    def update_target_branch(self, repo_path: Path, branch: str = "main") -> UpdateTargetResult:
        """
        Update target branch (checkout and pull).

        Verification: HEAD matches origin/{branch}.

        Args:
            repo_path: Path to the repository
            branch: Target branch name (default: main)

        Returns:
            UpdateTargetResult with update details
        """
        try:
            # Get current HEAD
            old_head = self._run_git_command(
                ["rev-parse", "HEAD"],
                cwd=repo_path,
            ).stdout.strip()

            # Checkout and pull
            self._run_git_command(["checkout", branch], cwd=repo_path)
            self._run_git_command(["pull", "--ff-only"], cwd=repo_path)

            # Get new HEAD
            new_head = self._run_git_command(
                ["rev-parse", "HEAD"],
                cwd=repo_path,
            ).stdout.strip()

            # Verify HEAD matches origin
            origin_head = self._run_git_command(
                ["rev-parse", f"origin/{branch}"],
                cwd=repo_path,
            ).stdout.strip()

            if new_head != origin_head:
                return UpdateTargetResult(
                    success=False,
                    old_head=old_head,
                    new_head=new_head,
                    error=f"HEAD ({new_head}) does not match origin/{branch} ({origin_head})",
                )

            return UpdateTargetResult(
                success=True,
                old_head=old_head,
                new_head=new_head,
            )

        except subprocess.CalledProcessError as e:
            return UpdateTargetResult(
                success=False,
                error=e.stderr,
            )

    def git_rebase(self, worktree_path: Path, onto: str = "main") -> GitRebaseResult:
        """
        Rebase branch onto target.

        Verification: Exit code + conflict file check.

        Args:
            worktree_path: Path to the worktree
            onto: Target branch to rebase onto

        Returns:
            GitRebaseResult with rebase details
        """
        try:
            result = self._run_git_command(
                ["rebase", onto],
                cwd=worktree_path,
            )

            return GitRebaseResult(
                success=True,
                conflict=False,
                stderr=result.stderr,
            )

        except subprocess.CalledProcessError as e:
            # Check for conflicts
            conflict_result = self._run_git_command(
                ["diff", "--name-only", "--diff-filter=U"],
                cwd=worktree_path,
                check=False,
            )

            conflict_files = [f for f in conflict_result.stdout.strip().split("\n") if f]

            if conflict_files:
                return GitRebaseResult(
                    success=False,
                    conflict=True,
                    conflict_files=conflict_files,
                    stderr=e.stderr,
                )

            return GitRebaseResult(
                success=False,
                conflict=False,
                stderr=e.stderr,
            )

    def git_rev_list_count(self, worktree_path: Path, base: str = "main") -> CommitCountResult:
        """
        Count commits ahead of base branch.

        Verification: Parse integer from rev-list output.

        Args:
            worktree_path: Path to the worktree
            base: Base branch (default: main)

        Returns:
            CommitCountResult with commit count and details
        """
        # Count commits
        count_result = self._run_git_command(
            ["rev-list", "--count", f"{base}..HEAD"],
            cwd=worktree_path,
            check=False,
        )

        count = int(count_result.stdout.strip()) if count_result.stdout.strip() else 0

        # Get commit details
        commits = []
        if count > 0:
            log_result = self._run_git_command(
                ["log", "--oneline", f"{base}..HEAD"],
                cwd=worktree_path,
                check=False,
            )

            for line in log_result.stdout.strip().split("\n"):
                if line:
                    parts = line.split(" ", 1)
                    commits.append(
                        {
                            "hash": parts[0],
                            "message": parts[1] if len(parts) > 1 else "",
                        }
                    )

        return CommitCountResult(count=count, commits=commits)

    def run_tests(self, worktree_path: Path, command: str = "make test") -> RunTestsResult:
        """
        Run test suite.

        Verification: Exit code = 0.

        Args:
            worktree_path: Path to the worktree
            command: Test command to run

        Returns:
            RunTestsResult with test details
        """
        import shlex

        cmd = shlex.split(command)

        try:
            result = subprocess.run(
                cmd,
                cwd=worktree_path,
                capture_output=True,
                text=True,
                timeout=600,  # 10 minute timeout
            )

            # Parse failed tests from output (basic heuristic)
            failed_tests = []
            output = result.stdout + result.stderr
            for line in output.split("\n"):
                if "FAILED" in line or "ERROR" in line:
                    # Extract test name if possible
                    if "::" in line:
                        failed_tests.append(line.split("::")[0].strip())
                    else:
                        failed_tests.append(line.strip())

            return RunTestsResult(
                success=result.returncode == 0,
                exit_code=result.returncode,
                stdout=result.stdout,
                stderr=result.stderr,
                failed_tests=failed_tests,
            )

        except subprocess.TimeoutExpired:
            return RunTestsResult(
                success=False,
                exit_code=-1,
                stdout="",
                stderr="Test timeout exceeded (10 minutes)",
                failed_tests=[],
            )
        except FileNotFoundError:
            return RunTestsResult(
                success=False,
                exit_code=-1,
                stdout="",
                stderr=f"Command not found: {command}",
                failed_tests=[],
            )

    def git_merge_squash(self, repo_path: Path, branch: str) -> GitMergeSquashResult:
        """
        Squash merge a branch into current branch.

        Verification: Staged files list.

        Args:
            repo_path: Path to the repository
            branch: Branch to merge

        Returns:
            GitMergeSquashResult with merge details
        """
        try:
            # Ensure we're on main
            self._run_git_command(["checkout", "main"], cwd=repo_path)

            # Squash merge
            self._run_git_command(["merge", "--squash", branch], cwd=repo_path)

            # Get staged files
            staged_result = self._run_git_command(
                ["diff", "--cached", "--name-only"],
                cwd=repo_path,
            )

            staged_files = [f for f in staged_result.stdout.strip().split("\n") if f]

            return GitMergeSquashResult(
                success=True,
                staged_files=staged_files,
            )

        except subprocess.CalledProcessError as e:
            return GitMergeSquashResult(
                success=False,
                staged_files=[],
                error=e.stderr,
            )

    def remove_worktree(self, worktree_path: Path, force: bool = True) -> None:
        """
        Remove a worktree.

        Args:
            worktree_path: Path to the worktree
            force: Force removal (default: True)
        """
        cmd = ["worktree", "remove", str(worktree_path)]
        if force:
            cmd.append("--force")

        self._run_git_command(cmd, check=False)

    def delete_branch(self, repo_path: Path, branch: str) -> None:
        """
        Delete a branch.

        Args:
            repo_path: Path to the repository
            branch: Branch name to delete
        """
        self._run_git_command(
            ["branch", "-D", branch],
            cwd=repo_path,
            check=False,
        )
