"""Tests for ProxyManager."""

from datetime import UTC
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock

import pytest

from kardbrd_agent.executor import ClaudeResult
from kardbrd_agent.manager import ActiveSession, ProxyManager
from kardbrd_agent.rules import Rule, RuleEngine


class TestProxyManager:
    """Tests for ProxyManager."""

    def test_init_defaults(self):
        """Test ProxyManager initialization with defaults."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)

        assert manager.state_manager == state_manager
        assert manager.mention_keyword == "@coder"
        assert manager.cwd == Path.cwd()
        assert manager.timeout == 3600
        assert manager.max_concurrent == 3
        assert manager._running is False
        assert manager._processing is False

    def test_init_creates_semaphore(self):
        """Test semaphore is initialized with max_concurrent."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, max_concurrent=5)
        assert manager._semaphore._value == 5

    def test_init_creates_active_sessions_dict(self):
        """Test active sessions tracking is initialized."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        assert manager._active_sessions == {}

    def test_init_custom_params(self):
        """Test ProxyManager initialization with custom parameters."""
        state_manager = MagicMock()
        manager = ProxyManager(
            state_manager,
            mention_keyword="@mybot",
            cwd="/tmp/work",
            timeout=300,
            max_concurrent=5,
        )

        assert manager.mention_keyword == "@mybot"
        assert manager.cwd == Path("/tmp/work")
        assert manager.timeout == 300
        assert manager.max_concurrent == 5

    def test_no_session_registry(self):
        """Test ProxyManager no longer has session_registry."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        assert not hasattr(manager, "session_registry")
        assert not hasattr(manager, "session")

    def test_no_mcp_port(self):
        """Test ProxyManager no longer has mcp_port."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        assert not hasattr(manager, "mcp_port")


class TestActiveSession:
    """Tests for ActiveSession dataclass."""

    def test_active_session_defaults(self):
        """Test ActiveSession with default values."""
        session = ActiveSession(card_id="abc123", worktree_path=Path("/tmp/wt"))
        assert session.card_id == "abc123"
        assert session.worktree_path == Path("/tmp/wt")
        assert session.process is None
        assert session.session_id is None

    def test_active_session_with_all_fields(self):
        """Test ActiveSession with all fields set."""
        mock_process = MagicMock()
        session = ActiveSession(
            card_id="abc123",
            worktree_path=Path("/tmp/wt"),
            process=mock_process,
            session_id="session-456",
        )
        assert session.process == mock_process
        assert session.session_id == "session-456"


class TestProxyManagerAsync:
    """Async tests for ProxyManager."""

    @pytest.mark.asyncio
    async def test_handle_board_event_ignores_no_mention(self):
        """Test that comments without @mention are ignored."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.mention_keyword = "@coder"

        manager._process_mention = AsyncMock()

        # Send a comment without @coder
        await manager._handle_board_event(
            {
                "event_type": "comment_created",
                "card_id": "abc123",
                "comment_id": "comm123",
                "content": "Just a regular comment",
                "author_name": "Paul",
            }
        )

        manager._process_mention.assert_not_called()

    @pytest.mark.asyncio
    async def test_handle_board_event_processes_mention(self):
        """Test that comments with @mention are processed."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.mention_keyword = "@coder"

        manager._process_mention = AsyncMock()

        # Send a comment with @coder
        await manager._handle_board_event(
            {
                "event_type": "comment_created",
                "card_id": "abc123",
                "comment_id": "comm123",
                "content": "@coder /kp",
                "author_name": "Paul",
            }
        )

        manager._process_mention.assert_called_once_with(
            card_id="abc123",
            comment_id="comm123",
            content="@coder /kp",
            author_name="Paul",
        )

    @pytest.mark.asyncio
    async def test_handle_board_event_skips_duplicate_card(self):
        """Test that duplicate card mentions are skipped."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.mention_keyword = "@coder"
        manager._active_sessions["abc123"] = ActiveSession(
            card_id="abc123", worktree_path=Path("/tmp")
        )

        manager._process_mention = AsyncMock()

        await manager._handle_board_event(
            {
                "event_type": "comment_created",
                "card_id": "abc123",
                "comment_id": "comm123",
                "content": "@coder /kp",
                "author_name": "Paul",
            }
        )

        # Should not process duplicate
        manager._process_mention.assert_not_called()

    @pytest.mark.asyncio
    async def test_handle_board_event_handles_card_moved(self):
        """Test that card_moved events are handled."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager._handle_card_moved = AsyncMock()

        await manager._handle_board_event(
            {
                "event_type": "card_moved",
                "card_id": "abc123",
                "list_name": "Done",
            }
        )

        manager._handle_card_moved.assert_called_once()

    @pytest.mark.asyncio
    async def test_process_mention_creates_worktree(self):
        """Test worktree is created before Claude execution."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)

        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Done")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/card-abc12345")
        manager._has_recent_bot_comment = MagicMock(return_value=False)

        await manager._process_mention("abc12345", "comm1", "@coder hi", "Paul")

        manager.worktree_manager.create_worktree.assert_called_once_with("abc12345")

    @pytest.mark.asyncio
    async def test_process_mention_passes_worktree_to_executor(self):
        """Test executor receives worktree path as cwd."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)

        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Done")
        )
        worktree_path = Path("/tmp/card-abc12345")
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = worktree_path
        manager._has_recent_bot_comment = MagicMock(return_value=False)

        await manager._process_mention("abc12345", "comm1", "@coder hi", "Paul")

        manager.executor.execute.assert_called_once()
        call_kwargs = manager.executor.execute.call_args[1]
        assert call_kwargs["cwd"] == worktree_path

    @pytest.mark.asyncio
    async def test_process_mention_passes_board_id_to_build_prompt(self):
        """Test build_prompt receives board_id from subscription info."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)

        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Done")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/card-abc12345")
        manager._has_recent_bot_comment = MagicMock(return_value=False)
        manager._subscription_info = {"board_id": "board789", "agent_name": "coder"}

        await manager._process_mention("abc12345", "comm1", "@coder hi", "Paul")

        call_kwargs = manager.executor.build_prompt.call_args[1]
        assert call_kwargs["board_id"] == "board789"

    @pytest.mark.asyncio
    async def test_process_mention_no_subscription_info_board_id_is_none(self):
        """Test build_prompt receives None board_id when no subscription info."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)

        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Done")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/card-abc12345")
        manager._has_recent_bot_comment = MagicMock(return_value=False)
        manager._subscription_info = None

        await manager._process_mention("abc12345", "comm1", "@coder hi", "Paul")

        call_kwargs = manager.executor.build_prompt.call_args[1]
        assert call_kwargs["board_id"] is None

    @pytest.mark.asyncio
    async def test_active_session_removed_on_completion(self):
        """Test session is removed from tracking after completion."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)

        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Done")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/wt")
        manager._has_recent_bot_comment = MagicMock(return_value=False)

        await manager._process_mention("abc12345", "comm1", "@coder hi", "Paul")

        assert "abc12345" not in manager._active_sessions

    @pytest.mark.asyncio
    async def test_active_session_removed_on_error(self):
        """Test session is removed even if execution fails."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)

        manager.client = MagicMock()
        manager.client.get_card_markdown.side_effect = Exception("API error")
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/wt")

        await manager._process_mention("abc12345", "comm1", "@coder hi", "Paul")

        assert "abc12345" not in manager._active_sessions

    @pytest.mark.asyncio
    async def test_start_no_subscriptions(self):
        """Test that start raises error when no subscriptions."""
        state_manager = MagicMock()
        state_manager.get_all_subscriptions.return_value = {}

        manager = ProxyManager(state_manager)

        with pytest.raises(RuntimeError, match="No subscriptions"):
            await manager.start()


class TestProxyManagerConfig:
    """Tests for ProxyManager configuration via constructor."""

    def test_setup_command_in_constructor(self):
        """Test setup_command is set via constructor."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, setup_command="npm install")
        assert manager.setup_command == "npm install"

    def test_defaults_are_none(self):
        """Test new config params default to None."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        assert manager.setup_command is None


class TestProxyManagerCardMoved:
    """Tests for card_moved event handling."""

    @pytest.mark.asyncio
    async def test_handle_card_moved_to_done_cleans_worktree(self):
        """Test worktree is removed when card moved to Done."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.worktree_manager = MagicMock()

        await manager._handle_card_moved(
            {
                "card_id": "abc12345",
                "list_name": "Done",
            }
        )

        manager.worktree_manager.remove_worktree.assert_called_once_with("abc12345")

    @pytest.mark.asyncio
    async def test_handle_card_moved_to_done_case_insensitive(self):
        """Test 'done' detection is case insensitive."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.worktree_manager = MagicMock()

        for list_name in ["Done", "DONE", "done", "Finished/Done"]:
            manager.worktree_manager.reset_mock()

            await manager._handle_card_moved(
                {
                    "card_id": "abc12345",
                    "list_name": list_name,
                }
            )

            manager.worktree_manager.remove_worktree.assert_called_once()

    @pytest.mark.asyncio
    async def test_handle_card_moved_to_other_list_no_cleanup(self):
        """Test worktree is NOT removed when moved to non-Done list."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.worktree_manager = MagicMock()

        await manager._handle_card_moved(
            {
                "card_id": "abc12345",
                "list_name": "In Progress",
            }
        )

        manager.worktree_manager.remove_worktree.assert_not_called()

    @pytest.mark.asyncio
    async def test_cleanup_worktree_kills_active_session(self):
        """Test active Claude process is killed during cleanup."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.worktree_manager = MagicMock()

        # Create mock process
        mock_process = MagicMock()
        manager._active_sessions["abc12345"] = ActiveSession(
            card_id="abc12345",
            worktree_path=Path("/tmp/wt"),
            process=mock_process,
        )

        await manager._cleanup_worktree("abc12345")

        mock_process.kill.assert_called_once()
        assert "abc12345" not in manager._active_sessions


class TestStopReaction:
    """Tests for 🛑 reaction-based stop handling."""

    @pytest.mark.asyncio
    async def test_stop_reaction_kills_process(self):
        """Test 🛑 reaction on triggering comment kills the Claude process."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()

        mock_process = MagicMock()
        manager._active_sessions["abc12345"] = ActiveSession(
            card_id="abc12345",
            worktree_path=Path("/tmp/wt"),
            comment_id="comm1",
            process=mock_process,
        )

        await manager._handle_reaction_added(
            {"emoji": "🛑", "card_id": "abc12345", "comment_id": "comm1", "user_name": "Paul"}
        )

        mock_process.kill.assert_called_once()
        assert "abc12345" not in manager._active_sessions

    @pytest.mark.asyncio
    async def test_stop_reaction_preserves_worktree(self):
        """Test 🛑 reaction does NOT remove worktree."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager.worktree_manager = MagicMock()

        manager._active_sessions["abc12345"] = ActiveSession(
            card_id="abc12345",
            worktree_path=Path("/tmp/wt"),
            comment_id="comm1",
            process=MagicMock(),
        )

        await manager._handle_stop_reaction("abc12345", "comm1")

        manager.worktree_manager.remove_worktree.assert_not_called()

    @pytest.mark.asyncio
    async def test_stop_reaction_no_active_session_noop(self):
        """Test 🛑 reaction does nothing if no active session."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()

        # No active session for this card
        await manager._handle_stop_reaction("abc12345", "comm1")

        # Should not raise, session dict unchanged
        assert len(manager._active_sessions) == 0

    @pytest.mark.asyncio
    async def test_stop_reaction_ignores_wrong_comment(self):
        """Test 🛑 reaction on a different comment is ignored."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()

        mock_process = MagicMock()
        manager._active_sessions["abc12345"] = ActiveSession(
            card_id="abc12345",
            worktree_path=Path("/tmp/wt"),
            comment_id="comm1",  # Session was triggered by comm1
            process=mock_process,
        )

        # React on a different comment (comm2)
        await manager._handle_stop_reaction("abc12345", "comm2")

        # Process should NOT be killed
        mock_process.kill.assert_not_called()
        assert "abc12345" in manager._active_sessions

    @pytest.mark.asyncio
    async def test_stop_reaction_routed_from_board_event(self):
        """Test 🛑 reaction is properly routed from _handle_board_event."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager._handle_stop_reaction = AsyncMock()

        mock_process = MagicMock()
        manager._active_sessions["abc12345"] = ActiveSession(
            card_id="abc12345",
            worktree_path=Path("/tmp/wt"),
            comment_id="comm1",
            process=mock_process,
        )

        await manager._handle_board_event(
            {
                "event_type": "reaction_added",
                "emoji": "🛑",
                "card_id": "abc12345",
                "comment_id": "comm1",
                "user_name": "Paul",
            }
        )

        manager._handle_stop_reaction.assert_called_once_with("abc12345", "comm1")

    @pytest.mark.asyncio
    async def test_stop_reaction_no_process_still_cleans_session(self):
        """Test 🛑 reaction cleans up session even if no process is running."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()

        manager._active_sessions["abc12345"] = ActiveSession(
            card_id="abc12345",
            worktree_path=Path("/tmp/wt"),
            comment_id="comm1",
            process=None,  # No process yet
        )

        await manager._handle_stop_reaction("abc12345", "comm1")

        assert "abc12345" not in manager._active_sessions

    @pytest.mark.asyncio
    async def test_word_stop_in_comment_no_longer_triggers_stop(self):
        """Test that the word 'stop' in a comment does NOT trigger stop behavior."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.mention_keyword = "@coder"
        manager.client = MagicMock()
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/wt")
        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "stop the presses"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Done")
        )

        # A comment with "stop" should now be processed normally, not trigger stop
        await manager._handle_comment_created(
            {
                "card_id": "abc12345",
                "comment_id": "comm1",
                "content": "@coder stop the presses",
                "author_name": "Paul",
            }
        )

        # Should have been processed as a normal mention
        manager.executor.execute.assert_called_once()


class TestProcessingAttribute:
    """Tests for _processing attribute behavior."""

    def test_init_processing_false(self):
        """Test _processing is initialized to False."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        assert manager._processing is False

    @pytest.mark.asyncio
    async def test_processing_true_during_execution(self):
        """Test _processing is True while processing a mention."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager._has_recent_bot_comment = MagicMock(return_value=False)

        processing_during_execution = []

        async def capture_processing(prompt, cwd=None, **kwargs):
            processing_during_execution.append(manager._processing)
            return ClaudeResult(success=True, result_text="Done")

        manager.executor.execute = capture_processing
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/wt")

        await manager._process_mention("card1", "comm1", "@coder hi", "Paul")

        assert processing_during_execution[0] is True
        assert manager._processing is False  # Reset after completion

    @pytest.mark.asyncio
    async def test_processing_false_after_exception(self):
        """Test _processing is reset to False even after exception."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager.client.get_card_markdown.side_effect = Exception("API error")
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/wt")

        await manager._process_mention("card1", "comm1", "@coder hi", "Paul")

        assert manager._processing is False


class TestRetryHandler:
    """Tests for retry emoji (🔄) handling."""

    @pytest.mark.asyncio
    async def test_retry_skipped_when_processing(self):
        """Test retry is skipped when already processing a card."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.mention_keyword = "@coder"
        manager._processing = True  # Simulate active processing
        manager.client = MagicMock()
        manager.client.get_comment.return_value = {
            "content": "@coder /kp",
            "author": {"display_name": "Paul"},
        }
        manager._process_mention = AsyncMock()

        await manager._handle_reaction_added(
            {"emoji": "🔄", "card_id": "abc123", "comment_id": "comm1", "user_name": "Paul"}
        )

        manager._process_mention.assert_not_called()

    @pytest.mark.asyncio
    async def test_retry_proceeds_when_not_processing(self):
        """Test retry proceeds when not currently processing."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.mention_keyword = "@coder"
        manager._processing = False
        manager.client = MagicMock()
        manager.client.get_comment.return_value = {
            "content": "@coder /kp",
            "author": {"display_name": "Paul"},
        }
        manager._process_mention = AsyncMock()

        await manager._handle_reaction_added(
            {"emoji": "🔄", "card_id": "abc123", "comment_id": "comm1", "user_name": "Paul"}
        )

        manager._process_mention.assert_called_once()

    @pytest.mark.asyncio
    async def test_retry_clears_completion_emojis(self):
        """Test retry removes old ✅ and 🛑 reactions before retrying."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.mention_keyword = "@coder"
        manager._processing = False
        manager.client = MagicMock()
        manager.client.get_comment.return_value = {
            "content": "@coder /kp",
            "author": {"display_name": "Paul"},
            "reactions": {"✅": [{"user_id": "bot"}]},
        }
        manager._process_mention = AsyncMock()

        await manager._handle_reaction_added(
            {"emoji": "🔄", "card_id": "abc123", "comment_id": "comm1", "user_name": "Paul"}
        )

        # Verify toggle_reaction was called to remove old emojis
        calls = manager.client.toggle_reaction.call_args_list
        removed_emojis = [call[0][2] for call in calls]
        assert "✅" in removed_emojis

    @pytest.mark.asyncio
    async def test_retry_ignores_non_mention_comments(self):
        """Test retry is ignored for comments without @mention."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.mention_keyword = "@coder"
        manager._processing = False
        manager.client = MagicMock()
        manager.client.get_comment.return_value = {
            "content": "Just a regular comment",
            "author": {"display_name": "Paul"},
        }
        manager._process_mention = AsyncMock()

        await manager._handle_reaction_added(
            {"emoji": "🔄", "card_id": "abc123", "comment_id": "comm1", "user_name": "Paul"}
        )

        manager._process_mention.assert_not_called()

    @pytest.mark.asyncio
    async def test_retry_handles_comment_fetch_error(self):
        """Test retry gracefully handles comment fetch failure."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.mention_keyword = "@coder"
        manager._processing = False
        manager.client = MagicMock()
        manager.client.get_comment.side_effect = Exception("API error")
        manager._process_mention = AsyncMock()

        # Should not raise
        await manager._handle_reaction_added(
            {"emoji": "🔄", "card_id": "abc123", "comment_id": "comm1", "user_name": "Paul"}
        )

        manager._process_mention.assert_not_called()

    @pytest.mark.asyncio
    async def test_non_retry_emoji_ignored(self):
        """Test non-retry emojis are ignored."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager._process_mention = AsyncMock()

        await manager._handle_reaction_added(
            {"emoji": "👍", "card_id": "abc123", "comment_id": "comm1", "user_name": "Paul"}
        )

        manager._process_mention.assert_not_called()


class TestResumeToPublish:
    """Tests for _resume_to_publish with API-based verification."""

    @pytest.mark.asyncio
    async def test_resume_success_with_api_confirmed_post(self):
        """Test no fallback when API confirms bot posted after resume."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager.executor = MagicMock()
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Done")
        )
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        await manager._resume_to_publish(
            card_id="card123",
            comment_id="comm1",
            session_id="session-abc",
            author_name="Paul",
        )

        # Should NOT post fallback since API says bot posted
        manager.client.add_comment.assert_not_called()
        # Should add success reaction
        manager.client.toggle_reaction.assert_called_with("card123", "comm1", "✅")

    @pytest.mark.asyncio
    async def test_resume_success_without_post_triggers_fallback(self):
        """Test fallback posted when API says bot did NOT post after resume."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager.executor = MagicMock()
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Some result")
        )
        manager._has_recent_bot_comment = MagicMock(return_value=False)

        await manager._resume_to_publish(
            card_id="card123",
            comment_id="comm1",
            session_id="session-abc",
            author_name="Paul",
        )

        # Should post fallback
        manager.client.add_comment.assert_called_once()
        assert "Some result" in manager.client.add_comment.call_args[0][1]
        assert "@Paul" in manager.client.add_comment.call_args[0][1]


class TestHasRecentBotComment:
    """Tests for _has_recent_bot_comment helper."""

    def test_returns_true_when_bot_posted_recently(self):
        """Test returns True when bot posted within time window."""
        from datetime import datetime

        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()

        # Simulate recent bot comment
        recent_time = datetime.now(UTC).isoformat()
        manager.client.get_card.return_value = {
            "comments": [
                {
                    "author": {"is_bot": True},
                    "created_at": recent_time,
                    "content": "Bot response",
                }
            ]
        }

        assert manager._has_recent_bot_comment("card123") is True

    def test_returns_false_when_no_bot_comments(self):
        """Test returns False when no bot comments exist."""
        from datetime import datetime

        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()

        recent_time = datetime.now(UTC).isoformat()
        manager.client.get_card.return_value = {
            "comments": [
                {
                    "author": {"is_bot": False},  # Human comment
                    "created_at": recent_time,
                    "content": "Human comment",
                }
            ]
        }

        assert manager._has_recent_bot_comment("card123") is False

    def test_returns_false_when_bot_comment_is_old(self):
        """Test returns False when bot comment is older than time window."""
        from datetime import datetime, timedelta

        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()

        # Old comment (2 minutes ago, outside default 60s window)
        old_time = (datetime.now(UTC) - timedelta(seconds=120)).isoformat()
        manager.client.get_card.return_value = {
            "comments": [
                {
                    "author": {"is_bot": True},
                    "created_at": old_time,
                    "content": "Old bot comment",
                }
            ]
        }

        assert manager._has_recent_bot_comment("card123") is False

    def test_returns_false_on_api_error(self):
        """Test fails open (returns False) when API call fails."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager.client.get_card.side_effect = Exception("API error")

        # Should return False (fail open) so fallback can proceed
        assert manager._has_recent_bot_comment("card123") is False

    def test_returns_false_when_no_comments(self):
        """Test returns False when card has no comments."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager.client.get_card.return_value = {"comments": []}

        assert manager._has_recent_bot_comment("card123") is False


class TestFallbackCommentGuard:
    """Tests for fallback comment duplicate prevention."""

    @pytest.mark.asyncio
    async def test_fallback_skipped_when_bot_already_posted(self):
        """Test fallback comment is skipped when bot posted recently."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager.executor = MagicMock()
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Some result")
        )

        # Mock _has_recent_bot_comment to return True
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        await manager._resume_to_publish(
            card_id="card123",
            comment_id="comm1",
            session_id="session-abc",
            author_name="Paul",
        )

        # Fallback should be skipped
        manager.client.add_comment.assert_not_called()
        # But success reaction should still be added
        manager.client.toggle_reaction.assert_called_with("card123", "comm1", "✅")

    @pytest.mark.asyncio
    async def test_fallback_proceeds_when_no_recent_bot_comment(self):
        """Test fallback comment proceeds when no recent bot comment exists."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager.executor = MagicMock()
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Some result")
        )

        # Mock _has_recent_bot_comment to return False
        manager._has_recent_bot_comment = MagicMock(return_value=False)

        await manager._resume_to_publish(
            card_id="card123",
            comment_id="comm1",
            session_id="session-abc",
            author_name="Paul",
        )

        # Fallback should proceed
        manager.client.add_comment.assert_called_once()
        assert "Some result" in manager.client.add_comment.call_args[0][1]
        assert "@Paul" in manager.client.add_comment.call_args[0][1]


class TestRuleEngineIntegration:
    """Tests for kardbrd.yml rule engine integration in ProxyManager."""

    def test_init_default_empty_rule_engine(self):
        """Test ProxyManager creates an empty RuleEngine by default."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        assert isinstance(manager.rule_engine, RuleEngine)
        assert len(manager.rule_engine.rules) == 0

    def test_init_accepts_rule_engine(self):
        """Test ProxyManager accepts a custom RuleEngine."""
        state_manager = MagicMock()
        engine = RuleEngine(rules=[Rule(name="test", events=["card_moved"], action="/ke")])
        manager = ProxyManager(state_manager, rule_engine=engine)
        assert len(manager.rule_engine.rules) == 1

    @pytest.mark.asyncio
    async def test_check_rules_called_on_board_event(self):
        """Test _check_rules is called for every board event."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.mention_keyword = "@coder"
        manager._check_rules = AsyncMock()

        await manager._handle_board_event(
            {
                "event_type": "card_moved",
                "card_id": "abc123",
                "list_name": "Ideas",
            }
        )

        manager._check_rules.assert_called_once_with(
            "card_moved",
            {
                "event_type": "card_moved",
                "card_id": "abc123",
                "list_name": "Ideas",
            },
        )

    @pytest.mark.asyncio
    async def test_check_rules_no_rules_noop(self):
        """Test _check_rules does nothing with no rules."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager._process_rule = AsyncMock()

        await manager._check_rules("card_moved", {"card_id": "abc123"})

        manager._process_rule.assert_not_called()

    @pytest.mark.asyncio
    async def test_check_rules_triggers_matching_rule(self):
        """Test matching rule triggers _process_rule."""
        state_manager = MagicMock()
        rule = Rule(name="ideas", events=["card_moved"], action="/ke", list="Ideas")
        engine = RuleEngine(rules=[rule])
        manager = ProxyManager(state_manager, rule_engine=engine)
        manager._process_rule = AsyncMock()

        message = {"card_id": "abc123", "list_name": "Ideas"}
        await manager._check_rules("card_moved", message)

        manager._process_rule.assert_called_once_with(card_id="abc123", rule=rule, message=message)

    @pytest.mark.asyncio
    async def test_check_rules_skips_active_card(self):
        """Test rules are skipped if card is already being processed."""
        state_manager = MagicMock()
        rule = Rule(name="ideas", events=["card_moved"], action="/ke", list="Ideas")
        engine = RuleEngine(rules=[rule])
        manager = ProxyManager(state_manager, rule_engine=engine)
        manager._process_rule = AsyncMock()
        manager._active_sessions["abc123"] = ActiveSession(
            card_id="abc123", worktree_path=Path("/tmp")
        )

        await manager._check_rules("card_moved", {"card_id": "abc123", "list_name": "Ideas"})

        manager._process_rule.assert_not_called()

    @pytest.mark.asyncio
    async def test_process_rule_spawns_claude(self):
        """Test _process_rule creates worktree and spawns Claude."""
        state_manager = MagicMock()
        rule = Rule(name="ideas", events=["card_moved"], action="/ke", model="haiku")
        manager = ProxyManager(state_manager)

        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.executor = MagicMock()
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Done")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/card-abc12345")
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        await manager._process_rule("abc12345", rule, {"card_id": "abc12345"})

        manager.worktree_manager.create_worktree.assert_called_once_with("abc12345")
        manager.executor.execute.assert_called_once()
        call_kwargs = manager.executor.execute.call_args[1]
        assert call_kwargs["model"] == "claude-haiku-4-5-20251001"

    @pytest.mark.asyncio
    async def test_process_rule_cleans_up_session(self):
        """Test _process_rule cleans up active session after completion."""
        state_manager = MagicMock()
        rule = Rule(name="ideas", events=["card_moved"], action="/ke")
        manager = ProxyManager(state_manager)

        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.executor = MagicMock()
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Done")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/wt")
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        await manager._process_rule("abc12345", rule, {"card_id": "abc12345"})

        assert "abc12345" not in manager._active_sessions

    @pytest.mark.asyncio
    async def test_process_rule_posts_error_on_failure(self):
        """Test _process_rule posts error comment when Claude fails."""
        state_manager = MagicMock()
        rule = Rule(name="ideas", events=["card_moved"], action="/ke")
        manager = ProxyManager(state_manager)

        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.executor = MagicMock()
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=False, result_text="", error="Model error")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/wt")

        await manager._process_rule("abc12345", rule, {"card_id": "abc12345"})

        manager.client.add_comment.assert_called_once()
        comment = manager.client.add_comment.call_args[0][1]
        assert "Automation Error" in comment
        assert "ideas" in comment

    @pytest.mark.asyncio
    async def test_add_reaction_skips_none_comment_id(self):
        """Test _add_reaction is a no-op when comment_id is None."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()

        manager._add_reaction("card123", None, "✅")

        manager.client.toggle_reaction.assert_not_called()

    @pytest.mark.asyncio
    async def test_resume_to_publish_handles_none_comment_id(self):
        """Test _resume_to_publish works with comment_id=None (rule triggers)."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager.executor = MagicMock()
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Done")
        )
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        await manager._resume_to_publish(
            card_id="card123",
            comment_id=None,
            session_id="session-abc",
            author_name="automation",
        )

        # Should not try to add reaction (comment_id is None)
        manager.client.toggle_reaction.assert_not_called()


class TestCheckRulesAssigneeFetch:
    """Tests for _check_rules fetching assignee data from API."""

    @pytest.mark.asyncio
    async def test_fetches_assignee_when_rules_use_assignee(self):
        """Test _check_rules fetches card assignee from API."""
        state_manager = MagicMock()
        rule = Rule(
            name="alice",
            events=["card_moved"],
            action="/ke",
            list="Ideas",
            assignee=["user-alice"],
        )
        engine = RuleEngine(rules=[rule])
        manager = ProxyManager(state_manager, rule_engine=engine)
        manager.client = MagicMock()
        manager.client.get_card.return_value = {
            "assignee": {"id": "user-alice", "display_name": "Alice"},
            "labels": [],
        }
        manager._process_rule = AsyncMock()

        message = {"card_id": "abc123", "list_name": "Ideas"}
        await manager._check_rules("card_moved", message)

        manager.client.get_card.assert_called_once_with("abc123")
        assert message["card_assignee_id"] == "user-alice"
        manager._process_rule.assert_called_once()

    @pytest.mark.asyncio
    async def test_skips_fetch_when_no_assignee_rules(self):
        """Test _check_rules skips API fetch when no rules use assignee."""
        state_manager = MagicMock()
        rule = Rule(name="all", events=["card_moved"], action="/ke", list="Ideas")
        engine = RuleEngine(rules=[rule])
        manager = ProxyManager(state_manager, rule_engine=engine)
        manager.client = MagicMock()
        manager._process_rule = AsyncMock()

        message = {"card_id": "abc123", "list_name": "Ideas"}
        await manager._check_rules("card_moved", message)

        manager.client.get_card.assert_not_called()

    @pytest.mark.asyncio
    async def test_unassigned_card_sets_empty_string(self):
        """Test unassigned card gets empty string for card_assignee_id."""
        state_manager = MagicMock()
        rule = Rule(
            name="alice",
            events=["card_moved"],
            action="/ke",
            assignee=["user-alice"],
        )
        engine = RuleEngine(rules=[rule])
        manager = ProxyManager(state_manager, rule_engine=engine)
        manager.client = MagicMock()
        manager.client.get_card.return_value = {
            "assignee": None,
            "labels": [],
        }
        manager._process_rule = AsyncMock()

        message = {"card_id": "abc123"}
        await manager._check_rules("card_moved", message)

        assert message["card_assignee_id"] == ""
        manager._process_rule.assert_not_called()

    @pytest.mark.asyncio
    async def test_api_error_defaults_empty_assignee(self):
        """Test API error defaults to empty assignee (rule won't match)."""
        state_manager = MagicMock()
        rule = Rule(
            name="alice",
            events=["card_moved"],
            action="/ke",
            assignee=["user-alice"],
        )
        engine = RuleEngine(rules=[rule])
        manager = ProxyManager(state_manager, rule_engine=engine)
        manager.client = MagicMock()
        manager.client.get_card.side_effect = Exception("API error")
        manager._process_rule = AsyncMock()

        message = {"card_id": "abc123"}
        await manager._check_rules("card_moved", message)

        assert message["card_assignee_id"] == ""
        manager._process_rule.assert_not_called()

    @pytest.mark.asyncio
    async def test_shared_api_call_for_labels_and_assignee(self):
        """Test single API call fetches both labels and assignee."""
        state_manager = MagicMock()
        rule = Rule(
            name="alice no agent",
            events=["card_moved"],
            action="/ke",
            assignee=["user-alice"],
            exclude_label="Agent",
        )
        engine = RuleEngine(rules=[rule])
        manager = ProxyManager(state_manager, rule_engine=engine)
        manager.client = MagicMock()
        manager.client.get_card.return_value = {
            "assignee": {"id": "user-alice", "display_name": "Alice"},
            "labels": [{"name": "Bug", "color": "red"}],
        }
        manager._process_rule = AsyncMock()

        message = {"card_id": "abc123"}
        await manager._check_rules("card_moved", message)

        # Only ONE API call despite needing both labels and assignee
        manager.client.get_card.assert_called_once_with("abc123")
        assert message["card_assignee_id"] == "user-alice"
        assert message["card_labels"] == ["Bug"]
