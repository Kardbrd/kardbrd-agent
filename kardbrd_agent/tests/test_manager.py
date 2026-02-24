"""Tests for ProxyManager."""

from datetime import UTC
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock

import pytest

from kardbrd_agent.executor import AuthStatus, ClaudeResult
from kardbrd_agent.manager import ActiveSession, ProxyManager, _sanitize_name
from kardbrd_agent.rules import Rule, RuleEngine

# Default test values for ProxyManager constructor
_DEFAULTS = {
    "board_id": "board123",
    "api_url": "https://test.kardbrd.com",
    "bot_token": "test-token",
    "agent_name": "coder",
}


def _make_manager(**overrides):
    """Create a ProxyManager with test defaults."""
    kwargs = {**_DEFAULTS, **overrides}
    return ProxyManager(**kwargs)


class TestProxyManager:
    """Tests for ProxyManager."""

    def test_init_defaults(self):
        """Test ProxyManager initialization with defaults."""
        manager = _make_manager()

        assert manager.board_id == "board123"
        assert manager.api_url == "https://test.kardbrd.com"
        assert manager.bot_token == "test-token"
        assert manager.agent_name == "coder"
        assert manager.mention_keyword == "@coder"
        assert manager.cwd == Path.cwd()
        assert manager.timeout == 3600
        assert manager.max_concurrent == 3
        assert manager._running is False
        assert manager._processing is False

    def test_init_creates_semaphore(self):
        """Test semaphore is initialized with max_concurrent."""
        manager = _make_manager(max_concurrent=5)
        assert manager._semaphore._value == 5

    def test_init_creates_active_sessions_dict(self):
        """Test active sessions tracking is initialized."""
        manager = _make_manager()
        assert manager._active_sessions == {}

    def test_init_custom_params(self):
        """Test ProxyManager initialization with custom parameters."""
        manager = _make_manager(
            agent_name="mybot",
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
        manager = _make_manager()
        assert not hasattr(manager, "session_registry")
        assert not hasattr(manager, "session")

    def test_no_mcp_port(self):
        """Test ProxyManager no longer has mcp_port."""
        manager = _make_manager()
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
        manager = _make_manager()
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
        manager = _make_manager()
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
        """Test that duplicate card mentions don't spawn an executor."""
        manager = _make_manager()
        manager._active_sessions["abc123"] = ActiveSession(
            card_id="abc123", worktree_path=Path("/tmp")
        )

        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock()

        await manager._handle_board_event(
            {
                "event_type": "comment_created",
                "card_id": "abc123",
                "comment_id": "comm123",
                "content": "@coder /kp",
                "author_name": "Paul",
            }
        )

        # Duplicate check inside semaphore should prevent auth check / execution
        manager.executor.check_auth.assert_not_called()

    @pytest.mark.asyncio
    async def test_handle_board_event_handles_card_moved(self):
        """Test that card_moved events are handled."""
        manager = _make_manager()
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
        manager = _make_manager()

        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
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
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/card-abc12345")
        manager._has_recent_bot_comment = MagicMock(return_value=False)

        await manager._process_mention("abc12345", "comm1", "@coder hi", "Paul")

        manager.worktree_manager.create_worktree.assert_called_once_with("abc12345")

    @pytest.mark.asyncio
    async def test_process_mention_passes_worktree_to_executor(self):
        """Test executor receives worktree path as cwd."""
        manager = _make_manager()

        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
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
        """Test build_prompt receives board_id from constructor."""
        manager = _make_manager(board_id="board789")

        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
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
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/card-abc12345")
        manager._has_recent_bot_comment = MagicMock(return_value=False)

        await manager._process_mention("abc12345", "comm1", "@coder hi", "Paul")

        call_kwargs = manager.executor.build_prompt.call_args[1]
        assert call_kwargs["board_id"] == "board789"

    @pytest.mark.asyncio
    async def test_active_session_removed_on_completion(self):
        """Test session is removed from tracking after completion."""
        manager = _make_manager()

        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
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
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/wt")
        manager._has_recent_bot_comment = MagicMock(return_value=False)

        await manager._process_mention("abc12345", "comm1", "@coder hi", "Paul")

        assert "abc12345" not in manager._active_sessions

    @pytest.mark.asyncio
    async def test_active_session_removed_on_error(self):
        """Test session is removed even if execution fails."""
        manager = _make_manager()

        manager.client = MagicMock()
        manager.client.get_card_markdown.side_effect = Exception("API error")
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/wt")

        await manager._process_mention("abc12345", "comm1", "@coder hi", "Paul")

        assert "abc12345" not in manager._active_sessions


class TestProxyManagerConfig:
    """Tests for ProxyManager configuration via constructor."""

    def test_setup_command_in_constructor(self):
        """Test setup_command is set via constructor."""
        manager = _make_manager(setup_command="npm install")
        assert manager.setup_command == "npm install"

    def test_defaults_are_none(self):
        """Test new config params default to None."""
        manager = _make_manager()
        assert manager.setup_command is None


class TestProxyManagerCardMoved:
    """Tests for card_moved event handling."""

    @pytest.mark.asyncio
    async def test_handle_card_moved_to_done_cleans_worktree(self):
        """Test worktree is removed when card moved to Done."""
        manager = _make_manager()
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
        manager = _make_manager()
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
        manager = _make_manager()
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
        manager = _make_manager()
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
        """Test 🛑 stop rule kills the Claude process via _check_rules."""
        stop_rule = Rule(name="stop", events=["reaction_added"], action="__stop__", emoji="🛑")
        engine = RuleEngine(rules=[stop_rule])
        manager = _make_manager(rule_engine=engine)
        manager.client = MagicMock()

        mock_process = MagicMock()
        manager._active_sessions["abc12345"] = ActiveSession(
            card_id="abc12345",
            worktree_path=Path("/tmp/wt"),
            comment_id="comm1",
            process=mock_process,
        )

        await manager._check_rules(
            "reaction_added",
            {"emoji": "🛑", "card_id": "abc12345", "comment_id": "comm1", "user_name": "Paul"},
        )

        mock_process.kill.assert_called_once()
        assert "abc12345" not in manager._active_sessions
        manager.client.add_comment.assert_called_once_with(
            "abc12345",
            "**Agent stopped** 🛑\n\nThe active session was terminated.",
        )

    @pytest.mark.asyncio
    async def test_stop_reaction_preserves_worktree(self):
        """Test 🛑 reaction does NOT remove worktree."""
        manager = _make_manager()
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
        manager = _make_manager()
        manager.client = MagicMock()

        # No active session for this card
        await manager._handle_stop_reaction("abc12345", "comm1")

        # Should not raise, session dict unchanged
        assert len(manager._active_sessions) == 0
        manager.client.add_comment.assert_not_called()

    @pytest.mark.asyncio
    async def test_stop_reaction_ignores_wrong_comment(self):
        """Test 🛑 reaction on a different comment is ignored."""
        manager = _make_manager()
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
        manager.client.add_comment.assert_not_called()

    @pytest.mark.asyncio
    async def test_stop_reaction_routed_from_board_event(self):
        """Test 🛑 reaction is routed through _check_rules from _handle_board_event."""
        stop_rule = Rule(name="stop", events=["reaction_added"], action="__stop__", emoji="🛑")
        engine = RuleEngine(rules=[stop_rule])
        manager = _make_manager(rule_engine=engine)
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
        manager = _make_manager()
        manager.client = MagicMock()

        manager._active_sessions["abc12345"] = ActiveSession(
            card_id="abc12345",
            worktree_path=Path("/tmp/wt"),
            comment_id="comm1",
            process=None,  # No process yet
        )

        await manager._handle_stop_reaction("abc12345", "comm1")

        assert "abc12345" not in manager._active_sessions
        manager.client.add_comment.assert_called_once_with(
            "abc12345",
            "**Agent stopped** 🛑\n\nThe active session was terminated.",
        )

    @pytest.mark.asyncio
    async def test_stop_reaction_handles_comment_failure_gracefully(self):
        """Test 🛑 reaction continues cleanup even if comment posting fails."""
        manager = _make_manager()
        manager.client = MagicMock()
        manager.client.add_comment.side_effect = Exception("API error")

        mock_process = MagicMock()
        manager._active_sessions["abc12345"] = ActiveSession(
            card_id="abc12345",
            worktree_path=Path("/tmp/wt"),
            comment_id="comm1",
            process=mock_process,
        )

        # Should not raise even if comment posting fails
        await manager._handle_stop_reaction("abc12345", "comm1")

        mock_process.kill.assert_called_once()
        assert "abc12345" not in manager._active_sessions

    @pytest.mark.asyncio
    async def test_word_stop_in_comment_no_longer_triggers_stop(self):
        """Test that the word 'stop' in a comment does NOT trigger stop behavior."""
        manager = _make_manager()
        manager.client = MagicMock()
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/wt")
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
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
        manager = _make_manager()
        assert manager._processing is False

    @pytest.mark.asyncio
    async def test_processing_true_during_execution(self):
        """Test _processing is True while processing a mention."""
        manager = _make_manager()
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
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
        manager = _make_manager()
        manager.client = MagicMock()
        manager.client.get_card_markdown.side_effect = Exception("API error")
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/wt")

        await manager._process_mention("card1", "comm1", "@coder hi", "Paul")

        assert manager._processing is False


class TestReactionRuleIntegration:
    """Tests for reaction events handled through rule engine."""

    @pytest.mark.asyncio
    async def test_reaction_rule_triggers_process_rule(self):
        """Test a reaction_added rule with emoji triggers _process_rule."""
        ship_rule = Rule(name="ship", events=["reaction_added"], action="ship the card", emoji="📦")
        engine = RuleEngine(rules=[ship_rule])
        manager = _make_manager(rule_engine=engine)
        manager._process_rule = AsyncMock()

        message = {
            "card_id": "abc123",
            "comment_id": "comm1",
            "emoji": "📦",
            "user_name": "Paul",
        }
        await manager._check_rules("reaction_added", message)

        manager._process_rule.assert_called_once_with(
            card_id="abc123", rule=ship_rule, message=message
        )

    @pytest.mark.asyncio
    async def test_reaction_rule_no_match_wrong_emoji(self):
        """Test a reaction_added rule does not match the wrong emoji."""
        ship_rule = Rule(name="ship", events=["reaction_added"], action="ship the card", emoji="📦")
        engine = RuleEngine(rules=[ship_rule])
        manager = _make_manager(rule_engine=engine)
        manager._process_rule = AsyncMock()

        await manager._check_rules(
            "reaction_added",
            {"card_id": "abc123", "emoji": "👍", "user_name": "Paul"},
        )

        manager._process_rule.assert_not_called()

    @pytest.mark.asyncio
    async def test_unmatched_emoji_is_ignored(self):
        """Test emojis without matching rules are silently ignored."""
        manager = _make_manager()
        manager._process_rule = AsyncMock()

        await manager._handle_reaction_added(
            {"emoji": "👍", "card_id": "abc123", "comment_id": "comm1", "user_name": "Paul"}
        )

        manager._process_rule.assert_not_called()


class TestResumeToPublish:
    """Tests for _resume_to_publish with API-based verification."""

    @pytest.mark.asyncio
    async def test_resume_success_with_api_confirmed_post(self):
        """Test no fallback when API confirms bot posted after resume."""
        manager = _make_manager()
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
        manager = _make_manager()
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

        manager = _make_manager()
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

        manager = _make_manager()
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

        manager = _make_manager()
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
        manager = _make_manager()
        manager.client = MagicMock()
        manager.client.get_card.side_effect = Exception("API error")

        # Should return False (fail open) so fallback can proceed
        assert manager._has_recent_bot_comment("card123") is False

    def test_returns_false_when_no_comments(self):
        """Test returns False when card has no comments."""
        manager = _make_manager()
        manager.client = MagicMock()
        manager.client.get_card.return_value = {"comments": []}

        assert manager._has_recent_bot_comment("card123") is False


class TestFallbackCommentGuard:
    """Tests for fallback comment duplicate prevention."""

    @pytest.mark.asyncio
    async def test_fallback_skipped_when_bot_already_posted(self):
        """Test fallback comment is skipped when bot posted recently."""
        manager = _make_manager()
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
        manager = _make_manager()
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
        manager = _make_manager()
        assert isinstance(manager.rule_engine, RuleEngine)
        assert len(manager.rule_engine.rules) == 0

    def test_init_accepts_rule_engine(self):
        """Test ProxyManager accepts a custom RuleEngine."""
        engine = RuleEngine(rules=[Rule(name="test", events=["card_moved"], action="/ke")])
        manager = _make_manager(rule_engine=engine)
        assert len(manager.rule_engine.rules) == 1

    @pytest.mark.asyncio
    async def test_check_rules_called_on_board_event(self):
        """Test _check_rules is called for every board event."""
        manager = _make_manager()
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
        manager = _make_manager()
        manager._process_rule = AsyncMock()

        await manager._check_rules("card_moved", {"card_id": "abc123"})

        manager._process_rule.assert_not_called()

    @pytest.mark.asyncio
    async def test_check_rules_triggers_matching_rule(self):
        """Test matching rule triggers _process_rule."""
        rule = Rule(name="ideas", events=["card_moved"], action="/ke", list="Ideas")
        engine = RuleEngine(rules=[rule])
        manager = _make_manager(rule_engine=engine)
        manager._process_rule = AsyncMock()

        message = {"card_id": "abc123", "list_name": "Ideas"}
        await manager._check_rules("card_moved", message)

        manager._process_rule.assert_called_once_with(card_id="abc123", rule=rule, message=message)

    @pytest.mark.asyncio
    async def test_check_rules_skips_active_card(self):
        """Test rules are skipped if card is already being processed."""
        rule = Rule(name="ideas", events=["card_moved"], action="/ke", list="Ideas")
        engine = RuleEngine(rules=[rule])
        manager = _make_manager(rule_engine=engine)
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock()
        manager._active_sessions["abc123"] = ActiveSession(
            card_id="abc123", worktree_path=Path("/tmp")
        )

        await manager._check_rules("card_moved", {"card_id": "abc123", "list_name": "Ideas"})

        # Duplicate check inside semaphore should prevent auth check / execution
        manager.executor.check_auth.assert_not_called()

    @pytest.mark.asyncio
    async def test_process_rule_spawns_claude(self):
        """Test _process_rule creates worktree and spawns Claude."""
        rule = Rule(name="ideas", events=["card_moved"], action="/ke", model="haiku")
        manager = _make_manager()

        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
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
        rule = Rule(name="ideas", events=["card_moved"], action="/ke")
        manager = _make_manager()

        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
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
        rule = Rule(name="ideas", events=["card_moved"], action="/ke")
        manager = _make_manager()

        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
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
        manager = _make_manager()
        manager.client = MagicMock()

        manager._add_reaction("card123", None, "✅")

        manager.client.toggle_reaction.assert_not_called()

    @pytest.mark.asyncio
    async def test_resume_to_publish_handles_none_comment_id(self):
        """Test _resume_to_publish works with comment_id=None (rule triggers)."""
        manager = _make_manager()
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

    @pytest.mark.asyncio
    async def test_check_rules_stop_rule_kills_session(self):
        """Test stop rule via _check_rules kills active session."""
        stop_rule = Rule(name="stop", events=["reaction_added"], action="__stop__", emoji="🛑")
        engine = RuleEngine(rules=[stop_rule])
        manager = _make_manager(rule_engine=engine)
        manager.client = MagicMock()

        mock_process = MagicMock()
        manager._active_sessions["abc12345"] = ActiveSession(
            card_id="abc12345",
            worktree_path=Path("/tmp/wt"),
            comment_id="comm1",
            process=mock_process,
        )

        await manager._check_rules(
            "reaction_added",
            {"emoji": "🛑", "card_id": "abc12345", "comment_id": "comm1"},
        )

        mock_process.kill.assert_called_once()
        assert "abc12345" not in manager._active_sessions


class TestAuthCheckInMention:
    """Tests for authentication check before processing mentions."""

    @pytest.mark.asyncio
    async def test_process_mention_aborts_when_not_authenticated(self):
        """Test that _process_mention posts error and returns when auth fails."""
        manager = _make_manager()
        manager.client = MagicMock()
        manager.worktree_manager = MagicMock()
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(
                authenticated=False,
                error="Claude CLI is not logged in",
                auth_hint="Run `claude auth login` on the host.",
            )
        )
        manager.executor.execute = AsyncMock()

        await manager._process_mention("card1", "comm1", "@coder hi", "Paul")

        # Should NOT spawn Claude
        manager.executor.execute.assert_not_called()
        # Should NOT create worktree
        manager.worktree_manager.create_worktree.assert_not_called()
        # Should post error comment
        manager.client.add_comment.assert_called_once()
        comment = manager.client.add_comment.call_args[0][1]
        assert "not authenticated" in comment.lower()
        assert "not logged in" in comment
        assert "@Paul" in comment
        # Should add stop reaction
        manager.client.toggle_reaction.assert_any_call("card1", "comm1", "🛑")

    @pytest.mark.asyncio
    async def test_process_mention_proceeds_when_authenticated(self):
        """Test that _process_mention proceeds normally when auth succeeds."""
        manager = _make_manager()
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
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
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/wt")
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        await manager._process_mention("card1", "comm1", "@coder hi", "Paul")

        # Should proceed to execute Claude
        manager.executor.execute.assert_called_once()

    @pytest.mark.asyncio
    async def test_process_rule_aborts_when_not_authenticated(self):
        """Test that _process_rule posts error when auth fails."""
        rule = Rule(name="auto-ke", events=["card_moved"], action="/ke")
        manager = _make_manager()
        manager.client = MagicMock()
        manager.worktree_manager = MagicMock()
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(
                authenticated=False,
                error="Claude CLI is not logged in",
                auth_hint="Run `claude auth login` on the host.",
            )
        )
        manager.executor.execute = AsyncMock()

        await manager._process_rule("card1", rule, {"card_id": "card1"})

        # Should NOT spawn Claude
        manager.executor.execute.assert_not_called()
        # Should post error comment
        manager.client.add_comment.assert_called_once()
        comment = manager.client.add_comment.call_args[0][1]
        assert "Automation Error" in comment
        assert "auto-ke" in comment
        assert "not logged in" in comment

    @pytest.mark.asyncio
    async def test_process_mention_clears_session_on_auth_failure(self):
        """Test that active session is cleaned up when auth fails."""
        manager = _make_manager()
        manager.client = MagicMock()
        manager.worktree_manager = MagicMock()
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=False, error="Not logged in")
        )

        await manager._process_mention("card1", "comm1", "@coder hi", "Paul")

        # Session should be cleaned up
        assert "card1" not in manager._active_sessions
        assert manager._processing is False

    @pytest.mark.asyncio
    async def test_auth_hint_appears_in_mention_error_comment(self):
        """Test auth_hint from AuthStatus is included in error comment on card."""
        manager = _make_manager()
        manager.client = MagicMock()
        manager.worktree_manager = MagicMock()
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(
                authenticated=False,
                error="GOOSE_PROVIDER not set",
                auth_hint="Set GOOSE_PROVIDER to your LLM provider. Run `goose configure`.",
            )
        )

        await manager._process_mention("card1", "comm1", "@coder hi", "Paul")

        comment = manager.client.add_comment.call_args[0][1]
        assert "GOOSE_PROVIDER not set" in comment
        assert "Set GOOSE_PROVIDER to your LLM provider" in comment
        assert "`goose configure`" in comment

    @pytest.mark.asyncio
    async def test_auth_hint_appears_in_rule_error_comment(self):
        """Test auth_hint from AuthStatus is included in rule error comment."""
        rule = Rule(name="auto-ke", events=["card_moved"], action="/ke")
        manager = _make_manager()
        manager.client = MagicMock()
        manager.worktree_manager = MagicMock()
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(
                authenticated=False,
                error="ANTHROPIC_API_KEY not set",
                auth_hint="Set ANTHROPIC_API_KEY env var or run `goose configure`.",
            )
        )

        await manager._process_rule("card1", rule, {"card_id": "card1"})

        comment = manager.client.add_comment.call_args[0][1]
        assert "ANTHROPIC_API_KEY not set" in comment
        assert "Set ANTHROPIC_API_KEY env var" in comment


class TestExecutorTypeThreading:
    """Tests for executor_type being properly passed to WorktreeManager."""

    def test_executor_type_stored_on_manager(self):
        """Test executor_type is stored in manager."""
        manager = _make_manager(executor_type="goose")
        assert manager.executor_type == "goose"

    def test_executor_type_defaults_to_claude(self):
        """Test executor_type defaults to 'claude'."""
        manager = _make_manager()
        assert manager.executor_type == "claude"


class TestErrorSanitization:
    """Tests for S3: error messages don't contain full tracebacks."""

    @pytest.mark.asyncio
    async def test_mention_exception_posts_sanitized_error(self):
        """Test _process_mention exception posts error type only, no traceback."""
        manager = _make_manager()
        manager.client = MagicMock()
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.side_effect = ConnectionError(
            "Failed to connect to git remote https://internal.corp/repo.git"
        )

        await manager._process_mention("card1", "comm1", "@coder hi", "Paul")

        comment = manager.client.add_comment.call_args[0][1]
        # Should contain the error type name
        assert "ConnectionError" in comment
        # Should have the generic instruction
        assert "Check the agent logs for details" in comment
        # Should NOT contain the full error message (may have sensitive info)
        assert "internal.corp" not in comment
        # Should NOT contain traceback markers
        assert "Traceback" not in comment
        assert "File " not in comment

    @pytest.mark.asyncio
    async def test_rule_exception_posts_sanitized_error(self):
        """Test _process_rule exception posts error type only, no traceback."""
        rule = Rule(name="ideas", events=["card_moved"], action="/ke")
        manager = _make_manager()
        manager.client = MagicMock()
        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.side_effect = RuntimeError(
            "Sensitive internal path: /home/agent/.secrets/key.pem"
        )

        await manager._process_rule("card1", rule, {"card_id": "card1"})

        comment = manager.client.add_comment.call_args[0][1]
        # Should contain the error type name
        assert "RuntimeError" in comment
        # Should have the generic instruction
        assert "Check the agent logs for details" in comment
        # Should NOT contain the sensitive error details
        assert ".secrets" not in comment
        assert "key.pem" not in comment


class TestConcurrentDuplicatePrevention:
    """Tests for ST1: duplicate-card session check inside semaphore."""

    @pytest.mark.asyncio
    async def test_concurrent_mentions_for_same_card_only_one_proceeds(self):
        """Test two concurrent _process_mention calls for same card: only one executes."""
        import asyncio

        manager = _make_manager()
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/wt")
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        execution_count = 0
        execute_started = asyncio.Event()
        execute_proceed = asyncio.Event()

        async def slow_execute(prompt, cwd=None, **kwargs):
            nonlocal execution_count
            execution_count += 1
            execute_started.set()
            await execute_proceed.wait()
            return ClaudeResult(success=True, result_text="Done")

        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = slow_execute

        # Start first mention (will block inside execute)
        task1 = asyncio.create_task(
            manager._process_mention("card1", "comm1", "@coder /kp", "Paul")
        )
        # Wait for first one to start executing
        await execute_started.wait()

        # Start second mention for same card while first is still running
        task2 = asyncio.create_task(
            manager._process_mention("card1", "comm2", "@coder /kp", "Paul")
        )
        # Give task2 time to reach the duplicate check
        await asyncio.sleep(0.05)

        # Let the first task complete
        execute_proceed.set()
        await task1
        await task2

        # Only one execution should have happened
        assert execution_count == 1

    @pytest.mark.asyncio
    async def test_concurrent_rules_for_same_card_only_one_proceeds(self):
        """Test two concurrent _process_rule calls for same card: only one executes."""
        import asyncio

        rule = Rule(name="ideas", events=["card_moved"], action="/ke")
        manager = _make_manager()
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = Path("/tmp/wt")
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        execution_count = 0
        execute_started = asyncio.Event()
        execute_proceed = asyncio.Event()

        async def slow_execute(prompt, cwd=None, **kwargs):
            nonlocal execution_count
            execution_count += 1
            execute_started.set()
            await execute_proceed.wait()
            return ClaudeResult(success=True, result_text="Done")

        manager.executor = MagicMock()
        manager.executor.check_auth = AsyncMock(
            return_value=AuthStatus(authenticated=True, email="test@test.com")
        )
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = slow_execute

        task1 = asyncio.create_task(manager._process_rule("card1", rule, {"card_id": "card1"}))
        await execute_started.wait()

        task2 = asyncio.create_task(manager._process_rule("card1", rule, {"card_id": "card1"}))
        await asyncio.sleep(0.05)

        execute_proceed.set()
        await task1
        await task2

        assert execution_count == 1


class TestGracefulShutdown:
    """Tests for ST3: stop() terminates active subprocesses."""

    @pytest.mark.asyncio
    async def test_stop_terminates_active_processes(self):
        """Test stop() sends SIGTERM to active sessions."""
        manager = _make_manager()
        manager.connection = AsyncMock()
        manager.client = MagicMock()

        mock_process = MagicMock()
        mock_process.returncode = None  # Still running
        manager._active_sessions["card1"] = ActiveSession(
            card_id="card1",
            worktree_path=Path("/tmp/wt"),
            process=mock_process,
        )

        with pytest.MonkeyPatch.context() as mp:
            mp.setattr("asyncio.sleep", AsyncMock())
            await manager.stop()

        mock_process.terminate.assert_called_once()

    @pytest.mark.asyncio
    async def test_stop_force_kills_after_grace_period(self):
        """Test stop() force-kills processes that don't exit after SIGTERM."""
        manager = _make_manager()
        manager.connection = AsyncMock()
        manager.client = MagicMock()

        mock_process = MagicMock()
        mock_process.returncode = None  # Still running after terminate

        manager._active_sessions["card1"] = ActiveSession(
            card_id="card1",
            worktree_path=Path("/tmp/wt"),
            process=mock_process,
        )

        with pytest.MonkeyPatch.context() as mp:
            mp.setattr("asyncio.sleep", AsyncMock())
            await manager.stop()

        mock_process.terminate.assert_called_once()
        mock_process.kill.assert_called_once()

    @pytest.mark.asyncio
    async def test_stop_skips_already_exited_processes(self):
        """Test stop() doesn't terminate already-exited processes."""
        manager = _make_manager()
        manager.connection = AsyncMock()
        manager.client = MagicMock()

        mock_process = MagicMock()
        mock_process.returncode = 0  # Already exited
        manager._active_sessions["card1"] = ActiveSession(
            card_id="card1",
            worktree_path=Path("/tmp/wt"),
            process=mock_process,
        )

        with pytest.MonkeyPatch.context() as mp:
            mp.setattr("asyncio.sleep", AsyncMock())
            await manager.stop()

        mock_process.terminate.assert_not_called()
        mock_process.kill.assert_not_called()

    @pytest.mark.asyncio
    async def test_stop_clears_active_sessions(self):
        """Test stop() clears all active sessions."""
        manager = _make_manager()
        manager.connection = AsyncMock()
        manager.client = MagicMock()

        mock_process = MagicMock()
        mock_process.returncode = None
        manager._active_sessions["card1"] = ActiveSession(
            card_id="card1",
            worktree_path=Path("/tmp/wt"),
            process=mock_process,
        )

        with pytest.MonkeyPatch.context() as mp:
            mp.setattr("asyncio.sleep", AsyncMock())
            await manager.stop()

        assert len(manager._active_sessions) == 0

    @pytest.mark.asyncio
    async def test_stop_with_no_active_sessions(self):
        """Test stop() works cleanly when no sessions are active."""
        manager = _make_manager()
        manager.connection = AsyncMock()
        manager.client = MagicMock()

        with pytest.MonkeyPatch.context() as mp:
            mp.setattr("asyncio.sleep", AsyncMock())
            await manager.stop()

        assert manager._running is False

    @pytest.mark.asyncio
    async def test_stop_handles_session_without_process(self):
        """Test stop() handles sessions where process hasn't been assigned."""
        manager = _make_manager()
        manager.connection = AsyncMock()
        manager.client = MagicMock()

        manager._active_sessions["card1"] = ActiveSession(
            card_id="card1",
            worktree_path=Path("/tmp/wt"),
            process=None,  # No process yet
        )

        with pytest.MonkeyPatch.context() as mp:
            mp.setattr("asyncio.sleep", AsyncMock())
            await manager.stop()

        assert len(manager._active_sessions) == 0


class TestSanitizeName:
    """Tests for S5: _sanitize_name prevents Markdown injection."""

    def test_normal_name_unchanged(self):
        """Test normal names pass through."""
        assert _sanitize_name("Paul") == "Paul"

    def test_name_with_spaces(self):
        """Test names with spaces are preserved."""
        assert _sanitize_name("Paul Smith") == "Paul Smith"

    def test_name_with_hyphen_and_underscore(self):
        """Test hyphens and underscores are preserved."""
        assert _sanitize_name("Paul-Smith_Jr") == "Paul-Smith_Jr"

    def test_name_with_period(self):
        """Test periods are preserved."""
        assert _sanitize_name("Paul.Smith") == "Paul.Smith"

    def test_markdown_link_injection(self):
        """Test Markdown link injection is stripped."""
        assert _sanitize_name("](http://evil.com)[x") == "httpevil.comx"

    def test_markdown_bold_injection(self):
        """Test Markdown bold formatting is stripped."""
        assert _sanitize_name("**bold**") == "bold"

    def test_html_tag_injection(self):
        """Test HTML tags are stripped."""
        assert _sanitize_name("<script>alert(1)</script>") == "scriptalert1script"

    def test_empty_after_sanitization_returns_unknown(self):
        """Test empty string after sanitization returns 'Unknown'."""
        assert _sanitize_name("[]()") == "Unknown"

    def test_empty_string_returns_unknown(self):
        """Test empty input returns 'Unknown'."""
        assert _sanitize_name("") == "Unknown"

    @pytest.mark.asyncio
    async def test_mention_comment_uses_sanitized_name(self):
        """Test _handle_comment_created sanitizes author_name before passing to _process_mention."""
        manager = _make_manager()
        manager._process_mention = AsyncMock()

        await manager._handle_comment_created(
            {
                "card_id": "card1",
                "comment_id": "comm1",
                "content": "@coder do thing",
                "author_name": "**injected**",
            }
        )

        # The author_name passed to _process_mention should be sanitized
        call_kwargs = manager._process_mention.call_args[1]
        assert call_kwargs["author_name"] == "injected"
