"""Tests for the WorktreeManager."""

import subprocess
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from kardbrd_agent.worktree import WorktreeManager


class TestWorktreeManager:
    """Tests for WorktreeManager class."""

    def test_init_sets_base_repo(self, tmp_path: Path):
        """Test that initialization sets the base repository path."""
        manager = WorktreeManager(tmp_path)
        assert manager.base_repo == tmp_path.resolve()

    def test_init_sets_worktrees_base_as_parent(self, tmp_path: Path):
        """Test that worktrees base is parent of base repo (sibling dirs)."""
        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        manager = WorktreeManager(base_repo)
        assert manager.worktrees_base == tmp_path

    def test_init_creates_active_worktrees_dict(self, tmp_path: Path):
        """Test that active worktrees tracking is initialized."""
        manager = WorktreeManager(tmp_path)
        assert manager.active_worktrees == {}

    def test_get_worktree_path_not_exists(self, tmp_path: Path):
        """Test get_worktree_path returns None when worktree doesn't exist."""
        manager = WorktreeManager(tmp_path / "kbn")
        result = manager.get_worktree_path("nonexistent")
        assert result is None

    def test_get_worktree_path_exists(self, tmp_path: Path):
        """Test get_worktree_path returns path when worktree exists."""
        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        manager = WorktreeManager(base_repo)

        # Create the worktree directory as sibling
        worktree_path = tmp_path / "card-card1234"
        worktree_path.mkdir()

        result = manager.get_worktree_path("card12345678")
        assert result == worktree_path

    def test_list_worktrees_empty(self, tmp_path: Path):
        """Test list_worktrees returns empty list when no worktrees tracked."""
        manager = WorktreeManager(tmp_path)
        result = manager.list_worktrees()
        assert result == []

    def test_list_worktrees_returns_tracked(self, tmp_path: Path):
        """Test list_worktrees returns tracked worktrees."""
        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        manager = WorktreeManager(base_repo)

        # Create worktree directories with .git
        wt1 = tmp_path / "card-card1111"
        wt1.mkdir()
        (wt1 / ".git").mkdir()

        wt2 = tmp_path / "card-card2222"
        wt2.mkdir()
        (wt2 / ".git").mkdir()

        # Track them
        manager.active_worktrees["card1111"] = wt1
        manager.active_worktrees["card2222"] = wt2

        result = manager.list_worktrees()
        assert len(result) == 2

        card_ids = [card_id for card_id, _ in result]
        assert "card1111" in card_ids
        assert "card2222" in card_ids


class TestWorktreeManagerSiblingPaths:
    """Tests for sibling directory worktree paths."""

    def test_get_short_id_truncates_to_8_chars(self, tmp_path: Path):
        """Test that card IDs are truncated to 8 characters."""
        manager = WorktreeManager(tmp_path)
        assert manager._get_short_id("abc12345xyz") == "abc12345"
        assert manager._get_short_id("short") == "short"

    def test_get_worktree_path_uses_sibling_directory(self, tmp_path: Path):
        """Test worktree path is sibling to base repo."""
        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        manager = WorktreeManager(base_repo)
        path = manager._get_worktree_path("abc12345")
        assert path == tmp_path / "card-abc12345"
        assert path.parent == tmp_path  # Sibling, not child

    def test_get_branch_name_uses_card_prefix(self, tmp_path: Path):
        """Test branch naming convention."""
        manager = WorktreeManager(tmp_path)
        assert manager._get_branch_name("abc12345xyz") == "card/abc12345"


class TestWorktreeManagerSymlinks:
    """Tests for symlink setup."""

    def test_setup_symlinks_creates_mcp_json(self, git_repo: Path):
        """Test .mcp.json symlink creation."""
        worktree = git_repo.parent / "card-abc12345"
        worktree.mkdir()

        manager = WorktreeManager(git_repo)
        manager._setup_symlinks(worktree)

        assert (worktree / ".mcp.json").is_symlink()
        assert (worktree / ".mcp.json").resolve() == git_repo / ".mcp.json"

    def test_setup_symlinks_creates_env(self, git_repo: Path):
        """Test .env symlink creation."""
        worktree = git_repo.parent / "card-abc12345"
        worktree.mkdir()

        manager = WorktreeManager(git_repo)
        manager._setup_symlinks(worktree)

        assert (worktree / ".env").is_symlink()

    def test_setup_symlinks_creates_claude_settings(self, git_repo: Path):
        """Test .claude/settings.local.json symlink creation."""
        worktree = git_repo.parent / "card-abc12345"
        worktree.mkdir()

        manager = WorktreeManager(git_repo)
        manager._setup_symlinks(worktree)

        assert (worktree / ".claude" / "settings.local.json").is_symlink()

    def test_setup_symlinks_skips_missing_files(self, tmp_path: Path):
        """Test symlinks are not created for missing source files."""
        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        # No .mcp.json or .env created

        worktree = tmp_path / "card-abc12345"
        worktree.mkdir()

        manager = WorktreeManager(base_repo)
        manager._setup_symlinks(worktree)  # Should not raise

        assert not (worktree / ".mcp.json").exists()
        assert not (worktree / ".env").exists()

    def test_setup_symlinks_idempotent(self, git_repo: Path):
        """Test calling setup_symlinks twice doesn't fail."""
        worktree = git_repo.parent / "card-abc12345"
        worktree.mkdir()

        manager = WorktreeManager(git_repo)
        manager._setup_symlinks(worktree)
        manager._setup_symlinks(worktree)  # Should not raise

        assert (worktree / ".mcp.json").is_symlink()


class TestWorktreeManagerSetupCommand:
    """Tests for setup command execution."""

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_run_setup_command_executes_in_worktree(self, mock_run: MagicMock, tmp_path: Path):
        """Test setup command is executed in worktree directory."""
        mock_run.return_value = MagicMock(returncode=0)

        manager = WorktreeManager(tmp_path, setup_command="npm install")
        worktree = tmp_path / "card-abc12345"
        worktree.mkdir()

        manager._run_setup_command(worktree)

        mock_run.assert_called_once()
        call_kwargs = mock_run.call_args
        assert call_kwargs[0][0] == "npm install"
        assert call_kwargs[1]["cwd"] == worktree
        assert call_kwargs[1]["check"] is True
        assert call_kwargs[1]["shell"] is True

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_run_setup_command_raises_on_failure(self, mock_run: MagicMock, tmp_path: Path):
        """Test setup command failure is propagated."""
        mock_run.side_effect = subprocess.CalledProcessError(1, "npm", stderr="error")

        manager = WorktreeManager(tmp_path, setup_command="npm install")
        worktree = tmp_path / "card-abc12345"
        worktree.mkdir()

        with pytest.raises(subprocess.CalledProcessError):
            manager._run_setup_command(worktree)

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_run_setup_command_skips_when_none(self, mock_run: MagicMock, tmp_path: Path):
        """Test setup command is skipped when not configured."""
        manager = WorktreeManager(tmp_path)  # No setup_command
        worktree = tmp_path / "card-abc12345"
        worktree.mkdir()

        manager._run_setup_command(worktree)

        mock_run.assert_not_called()

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_run_setup_command_skips_when_empty(self, mock_run: MagicMock, tmp_path: Path):
        """Test setup command is skipped when empty string."""
        manager = WorktreeManager(tmp_path, setup_command="")
        worktree = tmp_path / "card-abc12345"
        worktree.mkdir()

        manager._run_setup_command(worktree)

        mock_run.assert_not_called()


class TestWorktreeManagerCreate:
    """Tests for WorktreeManager.create_worktree."""

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_create_worktree_uses_sibling_path(self, mock_run: MagicMock, tmp_path: Path):
        """Test worktree is created as sibling directory."""
        mock_run.return_value = MagicMock(returncode=0)

        base_repo = tmp_path / "kbn"
        base_repo.mkdir()

        manager = WorktreeManager(base_repo)

        with patch.object(manager, "_setup_symlinks"), patch.object(manager, "_run_setup_command"):
            worktree_path = manager.create_worktree("abc12345xyz")

        assert worktree_path == tmp_path / "card-abc12345"

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_create_worktree_uses_card_branch_prefix(self, mock_run: MagicMock, tmp_path: Path):
        """Test branch name uses card/ prefix."""
        mock_run.return_value = MagicMock(returncode=0)

        manager = WorktreeManager(tmp_path)

        with patch.object(manager, "_setup_symlinks"), patch.object(manager, "_run_setup_command"):
            manager.create_worktree("abc12345")

        cmd = mock_run.call_args[0][0]
        assert "card/abc12345" in cmd

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_create_worktree_custom_branch(self, mock_run: MagicMock, tmp_path: Path):
        """Test creating a worktree with a custom branch name."""
        mock_run.return_value = MagicMock(returncode=0)

        manager = WorktreeManager(tmp_path)

        with patch.object(manager, "_setup_symlinks"), patch.object(manager, "_run_setup_command"):
            manager.create_worktree("test-card", branch_name="feature/my-feature")

        call_args = mock_run.call_args
        cmd = call_args[0][0]
        assert "feature/my-feature" in cmd

    def test_create_worktree_already_exists(self, tmp_path: Path):
        """Test that create_worktree returns existing path if worktree exists."""
        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        manager = WorktreeManager(base_repo)

        # Pre-create the worktree directory as sibling
        worktree_path = tmp_path / "card-existing"
        worktree_path.mkdir()

        result = manager.create_worktree("existing-card")
        assert result == worktree_path
        assert "existing-card" in manager.active_worktrees

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_create_worktree_branch_exists(self, mock_run: MagicMock, tmp_path: Path):
        """Test fallback when branch already exists."""
        # First call fails (branch exists), second call succeeds
        mock_run.side_effect = [
            subprocess.CalledProcessError(
                128, "git", stderr="fatal: a branch named 'card/test1234' already exists"
            ),
            MagicMock(returncode=0),
        ]

        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        manager = WorktreeManager(base_repo)

        with patch.object(manager, "_setup_symlinks"), patch.object(manager, "_run_setup_command"):
            worktree_path = manager.create_worktree("test1234")

        assert worktree_path == tmp_path / "card-test1234"
        assert mock_run.call_count == 2

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_create_worktree_calls_setup_symlinks(self, mock_run: MagicMock, git_repo: Path):
        """Test symlinks are set up after worktree creation."""
        mock_run.return_value = MagicMock(returncode=0)

        manager = WorktreeManager(git_repo)

        with (
            patch.object(manager, "_setup_symlinks") as mock_symlinks,
            patch.object(manager, "_run_setup_command"),
        ):
            manager.create_worktree("abc12345")
            mock_symlinks.assert_called_once()

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_create_worktree_calls_setup_command(self, mock_run: MagicMock, tmp_path: Path):
        """Test setup command is run after worktree creation."""
        mock_run.return_value = MagicMock(returncode=0)

        manager = WorktreeManager(tmp_path, setup_command="uv sync")

        with (
            patch.object(manager, "_setup_symlinks"),
            patch.object(manager, "_run_setup_command") as mock_setup,
        ):
            manager.create_worktree("abc12345")
            mock_setup.assert_called_once()

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_create_worktree_tracks_active(self, mock_run: MagicMock, tmp_path: Path):
        """Test worktree is tracked in active_worktrees."""
        mock_run.return_value = MagicMock(returncode=0)

        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        manager = WorktreeManager(base_repo)

        with patch.object(manager, "_setup_symlinks"), patch.object(manager, "_run_setup_command"):
            worktree_path = manager.create_worktree("abc12345")

        assert "abc12345" in manager.active_worktrees
        assert manager.active_worktrees["abc12345"] == worktree_path


class TestWorktreeManagerRemove:
    """Tests for WorktreeManager.remove_worktree."""

    def test_remove_worktree_not_exists(self, tmp_path: Path):
        """Test that remove_worktree does nothing when worktree doesn't exist."""
        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        manager = WorktreeManager(base_repo)
        # Should not raise
        manager.remove_worktree("nonexistent")

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_remove_worktree_success(self, mock_run: MagicMock, tmp_path: Path):
        """Test removing a worktree."""
        mock_run.return_value = MagicMock(returncode=0)

        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        manager = WorktreeManager(base_repo)

        worktree_path = tmp_path / "card-card1234"
        worktree_path.mkdir()
        manager.active_worktrees["card12345678"] = worktree_path

        manager.remove_worktree("card12345678")

        mock_run.assert_called_once()
        call_args = mock_run.call_args
        cmd = call_args[0][0]
        assert cmd[0] == "git"
        assert cmd[1] == "worktree"
        assert cmd[2] == "remove"

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_remove_worktree_clears_tracking(self, mock_run: MagicMock, tmp_path: Path):
        """Test that remove_worktree clears active_worktrees tracking."""
        mock_run.return_value = MagicMock(returncode=0)

        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        manager = WorktreeManager(base_repo)

        worktree_path = tmp_path / "card-card1234"
        worktree_path.mkdir()
        manager.active_worktrees["card12345678"] = worktree_path

        manager.remove_worktree("card12345678")

        assert "card12345678" not in manager.active_worktrees

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_remove_worktree_force(self, mock_run: MagicMock, tmp_path: Path):
        """Test removing a worktree with force flag."""
        mock_run.return_value = MagicMock(returncode=0)

        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        manager = WorktreeManager(base_repo)

        worktree_path = tmp_path / "card-card1234"
        worktree_path.mkdir()

        manager.remove_worktree("card12345678", force=True)

        call_args = mock_run.call_args
        cmd = call_args[0][0]
        assert "--force" in cmd

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_remove_worktree_failure(self, mock_run: MagicMock, tmp_path: Path):
        """Test that remove_worktree raises on failure."""
        mock_run.side_effect = subprocess.CalledProcessError(
            1, "git", stderr="fatal: could not remove"
        )

        base_repo = tmp_path / "kbn"
        base_repo.mkdir()
        manager = WorktreeManager(base_repo)

        worktree_path = tmp_path / "card-card1234"
        worktree_path.mkdir()

        with pytest.raises(RuntimeError, match="Failed to remove worktree"):
            manager.remove_worktree("card12345678")
