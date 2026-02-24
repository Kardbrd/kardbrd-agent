"""Integration tests for worktree automation.

These tests use real filesystem operations but mock git commands.
"""

import asyncio
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from kardbrd_agent.executor import AuthStatus, ClaudeResult
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
        manager = ProxyManager(
            board_id="board123",
            api_url="https://test.kardbrd.com",
            bot_token="test-token",
            agent_name="coder",
            max_concurrent=3,
            cwd=git_repo,
        )
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        execution_order = []

        async def mock_execute(prompt, cwd=None, **kwargs):
            card_id = cwd.name if cwd else "unknown"
            execution_order.append(f"start-{card_id}")
            await asyncio.sleep(0.05)
            execution_order.append(f"end-{card_id}")
            return ClaudeResult(success=True, result_text="Done")

        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
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
        manager = ProxyManager(
            board_id="board123",
            api_url="https://test.kardbrd.com",
            bot_token="test-token",
            agent_name="coder",
            max_concurrent=1,
            cwd=git_repo,
        )
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        execution_count = {"current": 0, "max": 0}

        async def mock_execute(prompt, cwd=None, **kwargs):
            execution_count["current"] += 1
            execution_count["max"] = max(execution_count["max"], execution_count["current"])
            await asyncio.sleep(0.05)
            execution_count["current"] -= 1
            return ClaudeResult(success=True, result_text="Done")

        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
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
        manager = ProxyManager(
            board_id="board123",
            api_url="https://test.kardbrd.com",
            bot_token="test-token",
            agent_name="coder",
            max_concurrent=3,
            cwd=git_repo,
        )
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.get_comment.return_value = {
            "content": "@coder /kp",
            "author": {"display_name": "Paul"},
        }
        manager.client.toggle_reaction = MagicMock()
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        retry_attempted = asyncio.Event()

        async def slow_execute(prompt, cwd=None, **kwargs):
            # Signal that we're processing, then wait
            retry_attempted.set()
            await asyncio.sleep(0.2)
            return ClaudeResult(success=True, result_text="Done")

        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
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


class TestApiVerificationIntegration:
    """Integration tests for API-based verification during concurrent processing."""

    @pytest.mark.asyncio
    async def test_concurrent_cards_api_verification_independent(self, git_repo: Path):
        """Test API verification works independently for concurrent cards."""
        manager = ProxyManager(
            board_id="board123",
            api_url="https://test.kardbrd.com",
            bot_token="test-token",
            agent_name="coder",
            max_concurrent=2,
            cwd=git_repo,
        )
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()
        manager.client.add_comment = MagicMock()

        # Card1's Claude posts a comment; Card2's does not
        def api_check(card_id, seconds=60):
            return card_id == "card1"

        manager._has_recent_bot_comment = MagicMock(side_effect=api_check)

        async def mock_execute(prompt, cwd=None, **kwargs):
            await asyncio.sleep(0.02)
            return ClaudeResult(success=True, result_text="Done")

        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = mock_execute
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

        # Card1 should get success reaction (API says bot posted)
        toggle_calls = manager.client.toggle_reaction.call_args_list
        card1_success = any(call[0][0] == "card1" and call[0][2] == "✅" for call in toggle_calls)
        assert card1_success, "Card1 should get ✅ when API confirms bot posted"

    @pytest.mark.asyncio
    async def test_active_session_cleaned_up_on_success(self, git_repo: Path):
        """Test _active_sessions entry removed after successful processing."""
        manager = ProxyManager(
            board_id="board123",
            api_url="https://test.kardbrd.com",
            bot_token="test-token",
            agent_name="coder",
            cwd=git_repo,
        )
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Done")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = git_repo.parent / "card-card1"
        (git_repo.parent / "card-card1").mkdir(exist_ok=True)

        await manager._process_mention("card1", "comm1", "@coder /kp", "Paul")

        assert "card1" not in manager._active_sessions

    @pytest.mark.asyncio
    async def test_active_session_cleaned_up_on_error(self, git_repo: Path):
        """Test _active_sessions entry removed even after an exception."""
        manager = ProxyManager(
            board_id="board123",
            api_url="https://test.kardbrd.com",
            bot_token="test-token",
            agent_name="coder",
            cwd=git_repo,
        )
        manager.client = MagicMock()
        manager.client.get_card_markdown.side_effect = Exception("API error")
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = git_repo.parent / "card-card1"
        (git_repo.parent / "card-card1").mkdir(exist_ok=True)

        await manager._process_mention("card1", "comm1", "@coder /kp", "Paul")

        assert "card1" not in manager._active_sessions

    @pytest.mark.asyncio
    async def test_no_session_registry_attribute(self, git_repo: Path):
        """Test ProxyManager has no session_registry attribute."""
        manager = ProxyManager(
            board_id="board123",
            api_url="https://test.kardbrd.com",
            bot_token="test-token",
            agent_name="coder",
            cwd=git_repo,
        )
        assert not hasattr(manager, "session_registry")


class TestDuplicateCommentPrevention:
    """Integration tests for preventing duplicate comments via API check."""

    @pytest.mark.asyncio
    async def test_no_fallback_comment_when_api_confirms_posted(self, git_repo: Path):
        """Test fallback comment not posted when API confirms bot already posted."""
        manager = ProxyManager(
            board_id="board123",
            api_url="https://test.kardbrd.com",
            bot_token="test-token",
            agent_name="coder",
            cwd=git_repo,
        )
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()
        manager.client.add_comment = MagicMock()

        # API says bot posted
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        async def mock_execute(prompt, cwd=None, **kwargs):
            return ClaudeResult(success=True, result_text="Posted!", session_id="sess123")

        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
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
        assert any(call[2] == "✅" for call in toggle_calls)

    @pytest.mark.asyncio
    async def test_no_fallback_when_resume_api_confirms_posted(self, git_repo: Path):
        """Test fallback skipped when API confirms bot posted after resume."""
        manager = ProxyManager(
            board_id="board123",
            api_url="https://test.kardbrd.com",
            bot_token="test-token",
            agent_name="coder",
            cwd=git_repo,
        )
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()
        manager.client.add_comment = MagicMock()

        # First check: not posted → triggers resume; second check: posted
        call_count = {"n": 0}

        def progressive_api_check(card_id, seconds=60):
            call_count["n"] += 1
            return call_count["n"] > 1  # False first, True after

        manager._has_recent_bot_comment = MagicMock(side_effect=progressive_api_check)

        execute_call_count = {"n": 0}

        async def mock_execute(prompt, resume_session_id=None, cwd=None, **kwargs):
            execute_call_count["n"] += 1
            if execute_call_count["n"] == 1:
                return ClaudeResult(success=True, result_text="Done", session_id="sess123")
            return ClaudeResult(success=True, result_text="Posted via MCP")

        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = mock_execute
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = git_repo.parent / "card-card1"
        (git_repo.parent / "card-card1").mkdir(exist_ok=True)

        await manager._process_mention("card1", "comm1", "@coder /kp", "Paul")

        # No fallback add_comment for result text
        fallback_calls = [
            call
            for call in manager.client.add_comment.call_args_list
            if "Posted via MCP" in str(call) or "Done" in str(call)
        ]
        assert len(fallback_calls) == 0, "Should not post fallback when API confirms post"


class TestRequireLabelIntegration:
    """Integration tests for require_label card label enrichment in _check_rules."""

    @pytest.mark.asyncio
    async def test_check_rules_fetches_card_labels(self, git_repo: Path):
        """Test _check_rules fetches card labels from API when rules use require_label."""
        from kardbrd_agent.rules import Rule, RuleEngine

        rule_engine = RuleEngine(
            rules=[
                Rule(
                    name="agent only",
                    events=["card_moved"],
                    action="/ke",
                    list="Ideas",
                    require_label="Agent",
                ),
            ]
        )
        manager = ProxyManager(
            board_id="board123",
            api_url="https://test.kardbrd.com",
            bot_token="test-token",
            agent_name="coder",
            cwd=git_repo,
            rule_engine=rule_engine,
        )
        manager.client = MagicMock()
        manager.client.get_card.return_value = {
            "labels": [{"name": "Agent"}, {"name": "Workflow"}],
        }
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()
        manager._has_recent_bot_comment = MagicMock(return_value=True)
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Done")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = git_repo.parent / "card-test1"
        (git_repo.parent / "card-test1").mkdir(exist_ok=True)

        await manager._check_rules(
            "card_moved",
            {"card_id": "test1", "list_name": "Ideas"},
        )

        # Should have fetched card labels
        manager.client.get_card.assert_called_once_with("test1")
        # Rule should have matched and spawned Claude
        manager.executor.execute.assert_called_once()

    @pytest.mark.asyncio
    async def test_check_rules_skips_without_agent_label(self, git_repo: Path):
        """Test _check_rules skips card when it lacks the required label."""
        from kardbrd_agent.rules import Rule, RuleEngine

        rule_engine = RuleEngine(
            rules=[
                Rule(
                    name="agent only",
                    events=["card_moved"],
                    action="/ke",
                    list="Ideas",
                    require_label="Agent",
                ),
            ]
        )
        manager = ProxyManager(
            board_id="board123",
            api_url="https://test.kardbrd.com",
            bot_token="test-token",
            agent_name="coder",
            cwd=git_repo,
            rule_engine=rule_engine,
        )
        manager.client = MagicMock()
        manager.client.get_card.return_value = {
            "labels": [{"name": "Workflow"}],
        }
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
        manager.executor.execute = AsyncMock()
        manager.worktree_manager = MagicMock()

        await manager._check_rules(
            "card_moved",
            {"card_id": "test1", "list_name": "Ideas"},
        )

        # Card fetched for label check
        manager.client.get_card.assert_called_once_with("test1")
        # Rule should NOT match — no "Agent" label
        manager.executor.execute.assert_not_called()

    @pytest.mark.asyncio
    async def test_check_rules_no_api_call_without_require_label(self, git_repo: Path):
        """Test _check_rules doesn't fetch labels when no rules use require_label."""
        from kardbrd_agent.rules import Rule, RuleEngine

        rule_engine = RuleEngine(
            rules=[
                Rule(
                    name="all cards",
                    events=["card_moved"],
                    action="/ke",
                    list="Ideas",
                ),
            ]
        )
        manager = ProxyManager(
            board_id="board123",
            api_url="https://test.kardbrd.com",
            bot_token="test-token",
            agent_name="coder",
            cwd=git_repo,
            rule_engine=rule_engine,
        )
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager._has_recent_bot_comment = MagicMock(return_value=True)
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Done")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = git_repo.parent / "card-test1"
        (git_repo.parent / "card-test1").mkdir(exist_ok=True)

        await manager._check_rules(
            "card_moved",
            {"card_id": "test1", "list_name": "Ideas"},
        )

        # Should NOT call get_card (no require_label rules)
        manager.client.get_card.assert_not_called()
