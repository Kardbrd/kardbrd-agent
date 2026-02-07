"""Tests for the MergeTools module."""

import subprocess
from pathlib import Path
from unittest.mock import MagicMock, patch

from kardbrd_agent.merge_tools import (
    CheckSessionResult,
    CheckWorktreeResult,
    CommitCountResult,
    GitFetchResult,
    GitMergeSquashResult,
    GitRebaseResult,
    GitStatusResult,
    MergeTools,
    RunTestsResult,
)


class TestMergeToolsInit:
    """Tests for MergeTools initialization."""

    def test_init_sets_main_repo_path(self, tmp_path: Path):
        """Test that initialization sets the main repository path."""
        tools = MergeTools(tmp_path)
        assert tools.main_repo_path == tmp_path.resolve()

    def test_init_sets_worktrees_base_as_parent(self, tmp_path: Path):
        """Test that worktrees base is parent of main repo."""
        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        tools = MergeTools(base_repo)
        assert tools.worktrees_base == tmp_path


class TestMergeToolsHelpers:
    """Tests for MergeTools helper methods."""

    def test_get_short_id_truncates_to_8_chars(self, tmp_path: Path):
        """Test that card IDs are truncated to 8 characters."""
        tools = MergeTools(tmp_path)
        assert tools._get_short_id("abc12345xyz") == "abc12345"
        assert tools._get_short_id("short") == "short"

    def test_get_worktree_path_uses_sibling_directory(self, tmp_path: Path):
        """Test worktree path is sibling to main repo."""
        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        tools = MergeTools(base_repo)
        path = tools._get_worktree_path("abc12345")
        assert path == tmp_path / "kbn-abc12345"

    def test_get_branch_name_uses_card_prefix(self, tmp_path: Path):
        """Test branch naming convention."""
        tools = MergeTools(tmp_path)
        assert tools._get_branch_name("abc12345xyz") == "card/abc12345"


class TestCheckSession:
    """Tests for MergeTools.check_session."""

    def test_check_session_no_pid_file(self, tmp_path: Path):
        """Test session check when no PID file exists."""
        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        tools = MergeTools(base_repo)

        result = tools.check_session("card1234")
        assert isinstance(result, CheckSessionResult)
        assert result.active is False
        assert result.pid is None

    def test_check_session_pid_file_exists_process_not_running(self, tmp_path: Path):
        """Test session check when PID file exists but process is not running."""
        base_repo = tmp_path / "kbn"
        base_repo.mkdir()

        # Create worktree directory with PID file
        worktree = tmp_path / "kbn-card1234"
        worktree.mkdir()
        claude_dir = worktree / ".claude"
        claude_dir.mkdir()
        (claude_dir / "session-card1234.pid").write_text("99999999")  # Non-existent PID

        tools = MergeTools(base_repo)
        result = tools.check_session("card12345678")

        assert result.active is False

    def test_check_session_invalid_pid_content(self, tmp_path: Path):
        """Test session check when PID file has invalid content."""
        base_repo = tmp_path / "kbn"
        base_repo.mkdir()

        worktree = tmp_path / "kbn-card1234"
        worktree.mkdir()
        claude_dir = worktree / ".claude"
        claude_dir.mkdir()
        (claude_dir / "session-card1234.pid").write_text("not-a-number")

        tools = MergeTools(base_repo)
        result = tools.check_session("card12345678")

        assert result.active is False


class TestCheckWorktree:
    """Tests for MergeTools.check_worktree."""

    def test_check_worktree_not_exists(self, tmp_path: Path):
        """Test worktree check when directory doesn't exist."""
        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        tools = MergeTools(base_repo)

        result = tools.check_worktree("nonexistent")
        assert isinstance(result, CheckWorktreeResult)
        assert result.exists is False
        assert result.path is None

    @patch("kardbrd_agent.merge_tools.MergeTools._run_git_command")
    def test_check_worktree_exists(self, mock_run: MagicMock, tmp_path: Path):
        """Test worktree check when worktree exists."""
        base_repo = tmp_path / "kbn"
        base_repo.mkdir()

        worktree = tmp_path / "kbn-card1234"
        worktree.mkdir()

        mock_run.return_value = MagicMock(
            stdout=f"worktree {worktree}\nbranch refs/heads/card/card1234\n"
        )

        tools = MergeTools(base_repo)
        result = tools.check_worktree("card12345678")

        assert result.exists is True
        assert result.path == worktree
        assert result.branch == "card/card1234"


class TestGitStatus:
    """Tests for MergeTools.git_status."""

    @patch("kardbrd_agent.merge_tools.MergeTools._run_git_command")
    def test_git_status_clean(self, mock_run: MagicMock, tmp_path: Path):
        """Test status check when working directory is clean."""
        mock_run.return_value = MagicMock(stdout="")

        tools = MergeTools(tmp_path)
        result = tools.git_status(tmp_path)

        assert isinstance(result, GitStatusResult)
        assert result.has_changes is False
        assert result.files == []

    @patch("kardbrd_agent.merge_tools.MergeTools._run_git_command")
    def test_git_status_with_changes(self, mock_run: MagicMock, tmp_path: Path):
        """Test status check when working directory has changes."""
        mock_run.return_value = MagicMock(stdout=" M file1.py\n?? file2.py\n")

        tools = MergeTools(tmp_path)
        result = tools.git_status(tmp_path)

        assert result.has_changes is True
        assert "file1.py" in result.files
        assert "file2.py" in result.files


class TestGitFetch:
    """Tests for MergeTools.git_fetch."""

    @patch("kardbrd_agent.merge_tools.MergeTools._run_git_command")
    def test_git_fetch_success(self, mock_run: MagicMock, tmp_path: Path):
        """Test successful git fetch."""
        mock_run.return_value = MagicMock(
            stderr="From origin\n   abc1234..def5678  main -> origin/main"
        )

        tools = MergeTools(tmp_path)
        result = tools.git_fetch(tmp_path)

        assert isinstance(result, GitFetchResult)
        assert result.success is True

    @patch("kardbrd_agent.merge_tools.MergeTools._run_git_command")
    def test_git_fetch_failure(self, mock_run: MagicMock, tmp_path: Path):
        """Test failed git fetch."""
        mock_run.side_effect = subprocess.CalledProcessError(1, "git", stderr="Network error")

        tools = MergeTools(tmp_path)
        result = tools.git_fetch(tmp_path)

        assert result.success is False


class TestGitRebase:
    """Tests for MergeTools.git_rebase."""

    @patch("kardbrd_agent.merge_tools.MergeTools._run_git_command")
    def test_git_rebase_success(self, mock_run: MagicMock, tmp_path: Path):
        """Test successful git rebase."""
        mock_run.return_value = MagicMock(stderr="")

        tools = MergeTools(tmp_path)
        result = tools.git_rebase(tmp_path, "main")

        assert isinstance(result, GitRebaseResult)
        assert result.success is True
        assert result.conflict is False

    @patch("kardbrd_agent.merge_tools.MergeTools._run_git_command")
    def test_git_rebase_conflict(self, mock_run: MagicMock, tmp_path: Path):
        """Test git rebase with conflicts."""
        # First call (rebase) fails, second call (diff) returns conflict files
        mock_run.side_effect = [
            subprocess.CalledProcessError(1, "git", stderr="CONFLICT"),
            MagicMock(stdout="file1.py\nfile2.py\n"),
        ]

        tools = MergeTools(tmp_path)
        result = tools.git_rebase(tmp_path, "main")

        assert result.success is False
        assert result.conflict is True
        assert result.conflict_files == ["file1.py", "file2.py"]


class TestGitRevListCount:
    """Tests for MergeTools.git_rev_list_count."""

    @patch("kardbrd_agent.merge_tools.MergeTools._run_git_command")
    def test_git_rev_list_count_with_commits(self, mock_run: MagicMock, tmp_path: Path):
        """Test counting commits ahead of base."""
        mock_run.side_effect = [
            MagicMock(stdout="3"),
            MagicMock(stdout="abc1234 Fix bug\ndef5678 Add feature\nghi9012 Update docs\n"),
        ]

        tools = MergeTools(tmp_path)
        result = tools.git_rev_list_count(tmp_path)

        assert isinstance(result, CommitCountResult)
        assert result.count == 3
        assert len(result.commits) == 3
        assert result.commits[0]["hash"] == "abc1234"

    @patch("kardbrd_agent.merge_tools.MergeTools._run_git_command")
    def test_git_rev_list_count_no_commits(self, mock_run: MagicMock, tmp_path: Path):
        """Test counting when no commits ahead."""
        mock_run.return_value = MagicMock(stdout="0")

        tools = MergeTools(tmp_path)
        result = tools.git_rev_list_count(tmp_path)

        assert result.count == 0
        assert result.commits == []


class TestRunTests:
    """Tests for MergeTools.run_tests."""

    @patch("subprocess.run")
    def test_run_tests_success(self, mock_run: MagicMock, tmp_path: Path):
        """Test successful test run."""
        mock_run.return_value = MagicMock(
            returncode=0,
            stdout="All tests passed",
            stderr="",
        )

        tools = MergeTools(tmp_path)
        result = tools.run_tests(tmp_path, "make test")

        assert isinstance(result, RunTestsResult)
        assert result.success is True
        assert result.exit_code == 0

    @patch("subprocess.run")
    def test_run_tests_failure(self, mock_run: MagicMock, tmp_path: Path):
        """Test failed test run."""
        mock_run.return_value = MagicMock(
            returncode=1,
            stdout="FAILED tests/test_foo.py::test_bar",
            stderr="",
        )

        tools = MergeTools(tmp_path)
        result = tools.run_tests(tmp_path, "make test")

        assert result.success is False
        assert result.exit_code == 1
        assert len(result.failed_tests) > 0

    @patch("subprocess.run")
    def test_run_tests_timeout(self, mock_run: MagicMock, tmp_path: Path):
        """Test test run timeout."""
        mock_run.side_effect = subprocess.TimeoutExpired("make test", 600)

        tools = MergeTools(tmp_path)
        result = tools.run_tests(tmp_path, "make test")

        assert result.success is False
        assert result.exit_code == -1


class TestGitMergeSquash:
    """Tests for MergeTools.git_merge_squash."""

    @patch("kardbrd_agent.merge_tools.MergeTools._run_git_command")
    def test_git_merge_squash_success(self, mock_run: MagicMock, tmp_path: Path):
        """Test successful squash merge."""
        mock_run.side_effect = [
            MagicMock(),  # checkout main
            MagicMock(),  # merge --squash
            MagicMock(stdout="file1.py\nfile2.py\n"),  # diff --cached
        ]

        tools = MergeTools(tmp_path)
        result = tools.git_merge_squash(tmp_path, "card/abc12345")

        assert isinstance(result, GitMergeSquashResult)
        assert result.success is True
        assert "file1.py" in result.staged_files

    @patch("kardbrd_agent.merge_tools.MergeTools._run_git_command")
    def test_git_merge_squash_failure(self, mock_run: MagicMock, tmp_path: Path):
        """Test failed squash merge."""
        mock_run.side_effect = [
            MagicMock(),  # checkout main
            subprocess.CalledProcessError(1, "git", stderr="Merge conflict"),
        ]

        tools = MergeTools(tmp_path)
        result = tools.git_merge_squash(tmp_path, "card/abc12345")

        assert result.success is False


class TestCleanup:
    """Tests for MergeTools cleanup methods."""

    @patch("kardbrd_agent.merge_tools.MergeTools._run_git_command")
    def test_remove_worktree(self, mock_run: MagicMock, tmp_path: Path):
        """Test worktree removal."""
        mock_run.return_value = MagicMock()

        tools = MergeTools(tmp_path)
        tools.remove_worktree(tmp_path / "kbn-card1234")

        mock_run.assert_called_once()
        call_args = mock_run.call_args[0][0]
        assert "worktree" in call_args
        assert "remove" in call_args
        assert "--force" in call_args

    @patch("kardbrd_agent.merge_tools.MergeTools._run_git_command")
    def test_delete_branch(self, mock_run: MagicMock, tmp_path: Path):
        """Test branch deletion."""
        mock_run.return_value = MagicMock()

        tools = MergeTools(tmp_path)
        tools.delete_branch(tmp_path, "card/abc12345")

        mock_run.assert_called_once()
        call_args = mock_run.call_args[0][0]
        assert "branch" in call_args
        assert "-D" in call_args
        assert "card/abc12345" in call_args
