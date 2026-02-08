"""Integration tests for API-based verification (replacing session isolation tests).

These tests validate the new behavior where concurrent card processing
uses _has_recent_bot_comment() API checks instead of in-process
session_registry for verifying Claude posted a response.

They will fail until the code is changed. Once applied, they prove:

1. Concurrent cards use independent API checks (no shared state)
2. API-based verification correctly handles per-card verification
3. _active_sessions cleanup still works without session_registry
4. Fallback comment prevention works via API check
5. No duplicate comments during concurrent processing
"""

import asyncio
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock

import pytest

from kardbrd_agent.executor import ClaudeResult
from kardbrd_agent.manager import ProxyManager


class TestConcurrentApiVerification:
    """Integration tests for API-based verification during concurrent processing.

    PROVES: When multiple cards are processed concurrently, the API-based
    verification correctly attributes bot comments to the right card.
    Unlike the old session_registry (which had race conditions with
    _current_card_id), API checks are inherently per-card.

    SAFETY: The old architecture had a real race condition where concurrent
    tasks could overwrite _current_card_id, causing tool calls to be
    recorded to the wrong card's session. API-based verification eliminates
    this entire class of bugs because each check fetches that card's comments.
    """

    @pytest.mark.asyncio
    async def test_concurrent_cards_api_verification_independent(self, git_repo: Path):
        """Test API verification works independently for concurrent cards.

        PROVES: When card1's Claude posts a comment and card2's does not,
        the API check correctly identifies this per-card, and card1 gets
        a success reaction while card2 triggers resume/fallback logic.

        This replaces the old test_concurrent_mcp_calls_record_to_correct_sessions
        which tested session_registry isolation.
        """
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, max_concurrent=2, cwd=git_repo)
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
        card1_success = any(
            call[0][0] == "card1" and call[0][2] == "✅" for call in toggle_calls
        )
        assert card1_success, "Card1 should get ✅ when API confirms bot posted"

        # No fallback add_comment for card1
        add_comment_calls = manager.client.add_comment.call_args_list
        card1_fallback = any(call[0][0] == "card1" for call in add_comment_calls)
        assert not card1_fallback, "Card1 should NOT get fallback comment"

    @pytest.mark.asyncio
    async def test_no_shared_state_between_concurrent_cards(self, git_repo: Path):
        """Test there's no shared mutable state that could cause race conditions.

        PROVES: The removal of session_registry eliminates the shared
        _current_card_id state that caused the original race condition.
        API checks are stateless — each call fetches fresh data from the API.
        """
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, max_concurrent=3, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()
        manager.client.add_comment = MagicMock()

        # All cards' Claude posts a comment
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        api_check_card_ids = []

        original_check = manager._has_recent_bot_comment

        def tracking_api_check(card_id, seconds=60):
            api_check_card_ids.append(card_id)
            return original_check(card_id, seconds)

        manager._has_recent_bot_comment = MagicMock(side_effect=tracking_api_check)

        async def mock_execute(prompt, cwd=None, **kwargs):
            await asyncio.sleep(0.02)
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
            (git_repo.parent / f"card-card{i}").mkdir(exist_ok=True)

        await asyncio.gather(
            manager._process_mention("card0", "comm0", "@coder /kp", "Paul"),
            manager._process_mention("card1", "comm1", "@coder /kp", "Paul"),
            manager._process_mention("card2", "comm2", "@coder /kp", "Paul"),
        )

        # Each card should have been verified independently
        assert "card0" in api_check_card_ids
        assert "card1" in api_check_card_ids
        assert "card2" in api_check_card_ids

        # Verify no session_registry exists (the source of race conditions)
        assert not hasattr(manager, "session_registry")


class TestActiveSessionCleanupWithoutRegistry:
    """Tests for _active_sessions cleanup without session_registry.

    PROVES: The _active_sessions dict is properly cleaned up after
    processing completes, even though session_registry.cleanup_card()
    is no longer called.

    SAFETY: If _active_sessions leaks, the same card can't be processed
    again (blocked by duplicate detection), which would make the bot
    permanently unresponsive for that card.
    """

    @pytest.mark.asyncio
    async def test_active_session_cleaned_up_on_success(self, git_repo: Path):
        """Test _active_sessions entry removed after successful processing.

        PROVES: The card_id is removed from _active_sessions in the
        finally block, allowing the card to be processed again later.
        """
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Done")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = (
            git_repo.parent / "card-card1"
        )
        (git_repo.parent / "card-card1").mkdir(exist_ok=True)

        await manager._process_mention("card1", "comm1", "@coder /kp", "Paul")

        assert "card1" not in manager._active_sessions

    @pytest.mark.asyncio
    async def test_active_session_cleaned_up_on_error(self, git_repo: Path):
        """Test _active_sessions entry removed even after an exception.

        PROVES: Cleanup happens in the finally block so errors don't
        leave stale entries that block future processing.
        """
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.side_effect = Exception("API error")
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = (
            git_repo.parent / "card-card1"
        )
        (git_repo.parent / "card-card1").mkdir(exist_ok=True)

        await manager._process_mention("card1", "comm1", "@coder /kp", "Paul")

        assert "card1" not in manager._active_sessions

    @pytest.mark.asyncio
    async def test_no_session_registry_cleanup_call(self, git_repo: Path):
        """Test no session_registry.cleanup_card() call in finally block.

        PROVES: The finally block no longer calls session_registry.cleanup_card()
        since session_registry doesn't exist.
        """
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Done")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = (
            git_repo.parent / "card-card1"
        )
        (git_repo.parent / "card-card1").mkdir(exist_ok=True)

        await manager._process_mention("card1", "comm1", "@coder /kp", "Paul")

        # No session_registry attribute should exist
        assert not hasattr(manager, "session_registry")


class TestFallbackCommentPreventionApi:
    """Integration tests for fallback comment prevention via API.

    PROVES: The fallback comment (posted when resume completes but Claude
    still didn't post) is protected by a _has_recent_bot_comment check
    to prevent duplicates.

    SAFETY: Without this guard, if Claude posts via MCP during resume but
    the manager doesn't detect it, a duplicate fallback would be posted.
    """

    @pytest.mark.asyncio
    async def test_no_fallback_when_api_says_posted(self, git_repo: Path):
        """Test fallback skipped when API confirms bot already posted.

        PROVES: Even in the resume fallback path, the manager checks
        the API before posting, preventing duplicates.
        """
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()
        manager.client.add_comment = MagicMock()

        # First check (in _process_mention): bot did NOT post → triggers resume
        # After resume check: bot posted → skip fallback
        call_count = {"n": 0}

        def progressive_api_check(card_id, seconds=60):
            call_count["n"] += 1
            if call_count["n"] == 1:
                return False  # First check: not yet posted
            return True  # After resume: posted

        manager._has_recent_bot_comment = MagicMock(side_effect=progressive_api_check)

        # Execute returns success with session_id (triggers resume path)
        execute_call_count = {"n": 0}

        async def mock_execute(prompt, resume_session_id=None, cwd=None, **kwargs):
            execute_call_count["n"] += 1
            if execute_call_count["n"] == 1:
                # Original execution - has session_id for resume
                return ClaudeResult(
                    success=True, result_text="Done", session_id="sess123"
                )
            else:
                # Resume execution - succeeds
                return ClaudeResult(success=True, result_text="Posted via MCP")

        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = mock_execute
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = (
            git_repo.parent / "card-card1"
        )
        (git_repo.parent / "card-card1").mkdir(exist_ok=True)

        await manager._process_mention("card1", "comm1", "@coder /kp", "Paul")

        # Fallback should NOT have been posted since second API check says posted
        add_comment_calls = [
            call
            for call in manager.client.add_comment.call_args_list
            if "Error" not in str(call)
        ]
        # No non-error add_comment calls expected (the API said bot posted)
        fallback_calls = [
            call
            for call in add_comment_calls
            if "Posted via MCP" in str(call) or "Done" in str(call)
        ]
        assert len(fallback_calls) == 0, "Should not post fallback when API confirms post"
