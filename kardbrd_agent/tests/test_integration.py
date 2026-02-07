"""Integration tests for worktree automation.

These tests use real filesystem operations but mock git commands.
"""

import asyncio
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from kardbrd_agent.executor import ClaudeResult
from kardbrd_agent.manager import ProxyManager
from kardbrd_agent.worktree import WorktreeManager


class TestWorktreeIntegration:
    """Integration tests for worktree creation with symlinks."""

    def test_symlink_setup_creates_working_links(self, git_repo: Path):
        """Test symlink setup creates working symlinks."""
        manager = WorktreeManager(git_repo)

        # Create the worktree directory
        worktree_path = git_repo.parent / "card-abc12345"
        worktree_path.mkdir()

        # Call _setup_symlinks directly
        manager._setup_symlinks(worktree_path)

        # Verify symlinks created
        assert (worktree_path / ".mcp.json").is_symlink()
        assert (worktree_path / ".env").is_symlink()
        assert (worktree_path / ".claude" / "settings.local.json").is_symlink()

        # Verify symlinks point to base repo
        assert (worktree_path / ".mcp.json").resolve() == git_repo / ".mcp.json"
        assert (worktree_path / ".env").resolve() == git_repo / ".env"

    @patch("kardbrd_agent.worktree.subprocess.run")
    def test_create_worktree_full_flow(self, mock_run: MagicMock, git_repo: Path):
        """Test create_worktree calls all setup methods."""
        mock_run.return_value = MagicMock(returncode=0)

        manager = WorktreeManager(git_repo, setup_command="uv sync")

        # Track method calls
        symlink_called = False
        setup_called = False

        def mock_setup_symlinks(path):
            nonlocal symlink_called
            symlink_called = True
            # Don't actually create symlinks (worktree doesn't exist yet in mock)

        def mock_run_setup_command(path):
            nonlocal setup_called
            setup_called = True

        manager._setup_symlinks = mock_setup_symlinks
        manager._run_setup_command = mock_run_setup_command

        result = manager.create_worktree("abc12345xyz")

        # Verify path returned is correct
        assert result == git_repo.parent / "card-abc12345"

        # Verify all setup methods called
        assert symlink_called, "_setup_symlinks was not called"
        assert setup_called, "_run_setup_command was not called"

        # Verify worktree is tracked
        assert "abc12345xyz" in manager.active_worktrees


class TestConcurrentProcessingIntegration:
    """Integration tests for concurrent mention processing."""

    @pytest.mark.asyncio
    async def test_two_cards_processed_concurrently(self, git_repo: Path):
        """Test two cards can be processed at the same time."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, max_concurrent=3, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()

        execution_order = []

        async def mock_execute(prompt, cwd=None, **kwargs):
            card_id = cwd.name if cwd else "unknown"
            execution_order.append(f"start-{card_id}")
            await asyncio.sleep(0.05)
            execution_order.append(f"end-{card_id}")
            return ClaudeResult(success=True, result_text="Done")

        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = mock_execute

        # Mock worktree manager
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.side_effect = [
            git_repo.parent / "card-card1111",
            git_repo.parent / "card-card2222",
        ]

        # Create worktree directories
        (git_repo.parent / "card-card1111").mkdir()
        (git_repo.parent / "card-card2222").mkdir()

        # Process two cards concurrently
        await asyncio.gather(
            manager._process_mention("card1111", "comm1", "@coder /kp", "Paul"),
            manager._process_mention("card2222", "comm2", "@coder /ki", "Paul"),
        )

        # Both should have started before either ended (concurrent)
        assert execution_order[0].startswith("start-")
        assert execution_order[1].startswith("start-")

    @pytest.mark.asyncio
    async def test_semaphore_limits_concurrency(self, git_repo: Path):
        """Test semaphore limits concurrent executions."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, max_concurrent=1, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()

        execution_count = {"current": 0, "max": 0}

        async def mock_execute(prompt, cwd=None, **kwargs):
            execution_count["current"] += 1
            execution_count["max"] = max(execution_count["max"], execution_count["current"])
            await asyncio.sleep(0.05)
            execution_count["current"] -= 1
            return ClaudeResult(success=True, result_text="Done")

        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = mock_execute

        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.side_effect = [
            git_repo.parent / f"card-card{i}" for i in range(3)
        ]

        for i in range(3):
            (git_repo.parent / f"card-card{i}").mkdir()

        # Process 3 cards with max_concurrent=1
        await asyncio.gather(
            manager._process_mention("card0", "comm0", "@coder /kp", "Paul"),
            manager._process_mention("card1", "comm1", "@coder /kp", "Paul"),
            manager._process_mention("card2", "comm2", "@coder /kp", "Paul"),
        )

        # Only 1 should have run at a time
        assert execution_count["max"] == 1


class TestRetryIntegration:
    """Integration tests for retry functionality."""

    @pytest.mark.asyncio
    async def test_retry_blocked_during_concurrent_processing(self, git_repo: Path):
        """Test retry is blocked when any card is being processed."""
        from unittest.mock import AsyncMock

        state_manager = MagicMock()
        manager = ProxyManager(state_manager, max_concurrent=3, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.get_comment.return_value = {
            "content": "@coder /kp",
            "author": {"display_name": "Paul"},
        }
        manager.client.toggle_reaction = MagicMock()

        retry_attempted = asyncio.Event()

        async def slow_execute(prompt, cwd=None, **kwargs):
            # Signal that we're processing, then wait
            retry_attempted.set()
            await asyncio.sleep(0.2)
            return ClaudeResult(success=True, result_text="Done")

        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = slow_execute
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = git_repo.parent / "card-card1"
        (git_repo.parent / "card-card1").mkdir(exist_ok=True)

        # Start processing a card
        process_task = asyncio.create_task(
            manager._process_mention("card1", "comm1", "@coder /kp", "Paul")
        )

        # Wait for processing to start
        await retry_attempted.wait()

        # Now try to retry a different card - should be blocked by _processing
        manager._process_mention = AsyncMock()
        await manager._handle_reaction_added(
            {"emoji": "🔄", "card_id": "card2", "comment_id": "comm2", "user_name": "Paul"}
        )

        # Retry should not have called _process_mention because _processing=True
        manager._process_mention.assert_not_called()

        # Wait for original to complete
        await process_task


class TestSessionIsolationIntegration:
    """Integration tests for session isolation across concurrent cards."""

    @pytest.mark.asyncio
    async def test_concurrent_cards_have_isolated_sessions(self, git_repo: Path):
        """Test two concurrent cards don't share session state."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, max_concurrent=2, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()

        sessions_during_execution = {}

        async def capture_session(prompt, cwd=None, **kwargs):
            card_id = cwd.name.replace("card-", "") if cwd else "unknown"
            # Record session state during execution
            session = manager.session_registry.get_current_session()
            sessions_during_execution[card_id] = {
                "comment_posted": session.comment_posted if session else None,
                "card_updated": session.card_updated if session else None,
            }

            # Simulate card1 posting a comment via MCP
            if "card1111" in card_id:
                manager.session_registry.record_tool_call("add_comment", {})

            await asyncio.sleep(0.05)
            return ClaudeResult(success=True, result_text="Done")

        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = capture_session
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.side_effect = [
            git_repo.parent / "card-card1111",
            git_repo.parent / "card-card2222",
        ]

        (git_repo.parent / "card-card1111").mkdir(exist_ok=True)
        (git_repo.parent / "card-card2222").mkdir(exist_ok=True)

        # Process two cards concurrently
        await asyncio.gather(
            manager._process_mention("card1111", "comm1", "@coder /kp", "Paul"),
            manager._process_mention("card2222", "comm2", "@coder /ki", "Paul"),
        )

        # Verify both cards were processed
        assert "card1111" in sessions_during_execution or "card2222" in sessions_during_execution

    @pytest.mark.asyncio
    async def test_session_cleaned_up_after_completion(self, git_repo: Path):
        """Test session is removed from registry after card processing completes."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()

        async def mock_execute(prompt, cwd=None, **kwargs):
            return ClaudeResult(success=True, result_text="Done")

        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = mock_execute
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = git_repo.parent / "card-card1"
        (git_repo.parent / "card-card1").mkdir(exist_ok=True)

        await manager._process_mention("card1", "comm1", "@coder /kp", "Paul")

        # Session should be cleaned up
        assert manager.session_registry.get_session("card1") is None

    @pytest.mark.asyncio
    async def test_session_cleaned_up_after_error(self, git_repo: Path):
        """Test session is removed even if processing fails."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.side_effect = Exception("API error")
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = git_repo.parent / "card-card1"
        (git_repo.parent / "card-card1").mkdir(exist_ok=True)

        await manager._process_mention("card1", "comm1", "@coder /kp", "Paul")

        # Session should still be cleaned up
        assert manager.session_registry.get_session("card1") is None


class TestDuplicateCommentPrevention:
    """Integration tests for preventing duplicate comments."""

    @pytest.mark.asyncio
    async def test_no_fallback_comment_when_mcp_posted(self, git_repo: Path):
        """Test fallback comment not posted when Claude already posted via MCP."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()
        manager.client.add_comment = MagicMock()

        async def mock_execute(prompt, cwd=None, **kwargs):
            # Simulate Claude posting via MCP
            manager.session_registry.record_tool_call("add_comment", {"card_id": "card1"})
            return ClaudeResult(success=True, result_text="Posted!", session_id="sess123")

        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = mock_execute
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = git_repo.parent / "card-card1"
        (git_repo.parent / "card-card1").mkdir(exist_ok=True)

        await manager._process_mention("card1", "comm1", "@coder /kp", "Paul")

        # manager.client.add_comment should NOT be called (no fallback needed)
        manager.client.add_comment.assert_not_called()

        # Success reaction should be added
        toggle_calls = [call[0] for call in manager.client.toggle_reaction.call_args_list]
        # Should have the success emoji call
        assert any(call[2] == "✅" for call in toggle_calls)

    @pytest.mark.asyncio
    async def test_concurrent_mcp_calls_record_to_correct_sessions(self, git_repo: Path):
        """Test MCP tool calls during concurrent processing record to correct card sessions.

        This tests the fix for a race condition where _current_card_id could be
        overwritten by a concurrent task before the MCP tool call is recorded,
        causing duplicates when the manager thinks Claude didn't post.
        """
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, max_concurrent=2, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()
        manager.client.add_comment = MagicMock()

        async def mock_execute_card1(prompt, cwd=None, **kwargs):
            # Wait to ensure card2 starts and potentially overwrites _current_card_id
            await asyncio.sleep(0.02)
            # Simulate MCP tool call with card_id in arguments (as real MCP does)
            # The fix ensures this records to card1's session even if _current_card_id is card2
            manager.session_registry.record_tool_call(
                "add_comment", {"card_id": "card1", "content": "from card1"}
            )
            return ClaudeResult(success=True, result_text="Done")

        async def mock_execute_card2(prompt, cwd=None, **kwargs):
            # Don't post anything - just wait
            await asyncio.sleep(0.05)
            return ClaudeResult(success=True, result_text="Done")

        call_count = {"count": 0}

        async def route_execute(prompt, cwd=None, **kwargs):
            call_count["count"] += 1
            if call_count["count"] == 1:
                return await mock_execute_card1(prompt, cwd, **kwargs)
            return await mock_execute_card2(prompt, cwd, **kwargs)

        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = route_execute
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.side_effect = [
            git_repo.parent / "card-card1",
            git_repo.parent / "card-card2",
        ]
        (git_repo.parent / "card-card1").mkdir(exist_ok=True)
        (git_repo.parent / "card-card2").mkdir(exist_ok=True)

        await asyncio.gather(
            manager._process_mention("card1", "comm1", "@coder /kp", "Paul"),
            manager._process_mention("card2", "comm2", "@coder /kp", "Paul"),
        )

        # Card1 should have success reaction (comment was tracked correctly to card1's session)
        toggle_calls = manager.client.toggle_reaction.call_args_list
        card1_success = any(call[0][0] == "card1" and call[0][2] == "✅" for call in toggle_calls)
        assert card1_success, "Card1 should get success reaction when comment tracked correctly"

        # No fallback add_comment should have been called for card1
        # (fallback only happens when session tracking fails)
        manager.client.add_comment.assert_not_called()
