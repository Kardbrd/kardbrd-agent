"""Tests for bot card command handling."""

from datetime import UTC, datetime
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from kardbrd_agent.manager import ProxyManager
from kardbrd_agent.rules import RuleEngine

# Default test values for ProxyManager constructor
_DEFAULTS = {
    "board_id": "board123",
    "api_url": "https://test.kardbrd.com",
    "bot_token": "test-token",
    "agent_name": "TestBot",
}


def _make_manager(**overrides):
    """Create a ProxyManager with test defaults and mock client."""
    kwargs = {**_DEFAULTS, **overrides}
    manager = ProxyManager(**kwargs)
    manager.client = MagicMock()
    manager._bot_card_id = "bot-card-123"
    manager._start_time = datetime(2026, 4, 15, 12, 0, 0, tzinfo=UTC)
    return manager


class TestIsBotCard:
    """Tests for _is_bot_card detection."""

    def test_matches_by_title(self):
        manager = _make_manager()
        msg = {"card_title": "\U0001f916 TestBot", "card_id": "other-card"}
        assert manager._is_bot_card(msg) is True

    def test_matches_by_card_id(self):
        manager = _make_manager()
        msg = {"card_title": "Some Card", "card_id": "bot-card-123"}
        assert manager._is_bot_card(msg) is True

    def test_no_match(self):
        manager = _make_manager()
        msg = {"card_title": "Some Card", "card_id": "other-card"}
        assert manager._is_bot_card(msg) is False

    def test_no_bot_card_id_cached(self):
        manager = _make_manager()
        manager._bot_card_id = None
        msg = {"card_title": "Some Card", "card_id": "other-card"}
        assert manager._is_bot_card(msg) is False

    def test_empty_title(self):
        manager = _make_manager()
        msg = {"card_title": "", "card_id": "other-card"}
        assert manager._is_bot_card(msg) is False


class TestHandleCommentCreatedBotCardRouting:
    """Tests for bot card command routing in _handle_comment_created."""

    @pytest.mark.asyncio
    async def test_slash_command_on_bot_card_routes_to_handler(self):
        manager = _make_manager()
        manager._handle_bot_card_command = AsyncMock()

        message = {
            "card_id": "bot-card-123",
            "card_title": "\U0001f916 TestBot",
            "comment_id": "comment-1",
            "content": "/status",
            "author_name": "Paul",
            "author_is_bot": False,
        }
        await manager._handle_comment_created(message)
        manager._handle_bot_card_command.assert_awaited_once_with("bot-card-123", "/status", "Paul")

    @pytest.mark.asyncio
    async def test_regular_comment_on_bot_card_not_routed(self):
        manager = _make_manager()
        manager._handle_bot_card_command = AsyncMock()

        message = {
            "card_id": "bot-card-123",
            "card_title": "\U0001f916 TestBot",
            "comment_id": "comment-1",
            "content": "Just a regular comment",
            "author_name": "Paul",
            "author_is_bot": False,
        }
        await manager._handle_comment_created(message)
        manager._handle_bot_card_command.assert_not_awaited()

    @pytest.mark.asyncio
    async def test_bot_comment_ignored(self):
        manager = _make_manager()
        manager._handle_bot_card_command = AsyncMock()

        message = {
            "card_id": "bot-card-123",
            "card_title": "\U0001f916 TestBot",
            "comment_id": "comment-1",
            "content": "/status",
            "author_name": "TestBot",
            "author_is_bot": True,
        }
        await manager._handle_comment_created(message)
        manager._handle_bot_card_command.assert_not_awaited()

    @pytest.mark.asyncio
    async def test_slash_command_on_regular_card_not_routed(self):
        """Slash commands on regular cards go through normal @mention flow."""
        manager = _make_manager()
        manager._handle_bot_card_command = AsyncMock()

        message = {
            "card_id": "regular-card",
            "card_title": "Some Task",
            "comment_id": "comment-1",
            "content": "/status @TestBot",
            "author_name": "Paul",
            "author_is_bot": False,
        }
        await manager._handle_comment_created(message)
        manager._handle_bot_card_command.assert_not_awaited()


class TestBotCardCommandRouter:
    """Tests for _handle_bot_card_command dispatch."""

    @pytest.mark.asyncio
    async def test_dispatches_status(self):
        manager = _make_manager()
        manager._cmd_status = AsyncMock()

        await manager._handle_bot_card_command("bot-card-123", "/status", "Paul")
        manager._cmd_status.assert_awaited_once_with("bot-card-123", "Paul")

    @pytest.mark.asyncio
    async def test_dispatches_pause(self):
        manager = _make_manager()
        manager._cmd_pause = AsyncMock()

        await manager._handle_bot_card_command("bot-card-123", "/pause", "Paul")
        manager._cmd_pause.assert_awaited_once_with("bot-card-123", "Paul")

    @pytest.mark.asyncio
    async def test_dispatches_resume(self):
        manager = _make_manager()
        manager._cmd_resume = AsyncMock()

        await manager._handle_bot_card_command("bot-card-123", "/resume", "Paul")
        manager._cmd_resume.assert_awaited_once_with("bot-card-123", "Paul")

    @pytest.mark.asyncio
    async def test_dispatches_reload(self):
        manager = _make_manager()
        manager._cmd_reload = AsyncMock()

        await manager._handle_bot_card_command("bot-card-123", "/reload", "Paul")
        manager._cmd_reload.assert_awaited_once_with("bot-card-123", "Paul")

    @pytest.mark.asyncio
    async def test_unknown_command_ignored(self):
        manager = _make_manager()

        # Should not raise
        await manager._handle_bot_card_command("bot-card-123", "/unknown", "Paul")

    @pytest.mark.asyncio
    async def test_command_case_insensitive(self):
        manager = _make_manager()
        manager._cmd_status = AsyncMock()

        await manager._handle_bot_card_command("bot-card-123", "/Status", "Paul")
        manager._cmd_status.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_command_with_extra_text(self):
        manager = _make_manager()
        manager._cmd_status = AsyncMock()

        await manager._handle_bot_card_command("bot-card-123", "/status some extra text", "Paul")
        manager._cmd_status.assert_awaited_once()


class TestCmdStatus:
    """Tests for /status command handler."""

    @pytest.mark.asyncio
    async def test_posts_status_comment(self):
        manager = _make_manager()
        manager._start_time = datetime(2026, 4, 15, 10, 0, 0, tzinfo=UTC)

        with patch("kardbrd_agent.manager.datetime") as mock_dt:
            mock_dt.now.return_value = datetime(2026, 4, 15, 12, 30, 0, tzinfo=UTC)
            mock_dt.side_effect = lambda *a, **kw: datetime(*a, **kw)
            await manager._cmd_status("bot-card-123", "Paul")

        manager.client.add_comment.assert_called_once()
        comment = manager.client.add_comment.call_args[0][1]
        assert "Online" in comment
        assert "2h 30m" in comment
        assert "Active cards" in comment
        assert "@Paul" in comment


class TestCmdReload:
    """Tests for /reload command handler."""

    @pytest.mark.asyncio
    async def test_reload_with_reloadable_engine(self, tmp_path):
        from kardbrd_agent.rules import ReloadableRuleEngine

        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text(
            "board_id: test\nagent: TestBot\nrules:\n"
            "  - name: test\n    event: card_moved\n    action: /test\n"
        )
        engine = ReloadableRuleEngine(rules_file)
        manager = _make_manager(rule_engine=engine)

        await manager._cmd_reload("bot-card-123", "Paul")

        manager.client.add_comment.assert_called_once()
        comment = manager.client.add_comment.call_args[0][1]
        assert "Reloaded" in comment
        assert "1 rule" in comment

    @pytest.mark.asyncio
    async def test_reload_with_static_engine(self):
        manager = _make_manager(rule_engine=RuleEngine())

        await manager._cmd_reload("bot-card-123", "Paul")

        manager.client.add_comment.assert_called_once()
        comment = manager.client.add_comment.call_args[0][1]
        assert "not reloadable" in comment


class TestCmdPauseResume:
    """Tests for /pause and /resume command handlers."""

    @pytest.mark.asyncio
    async def test_pause_sets_flag(self):
        manager = _make_manager()
        assert manager._paused is False

        await manager._cmd_pause("bot-card-123", "Paul")

        assert manager._paused is True
        manager.client.add_comment.assert_called_once()
        comment = manager.client.add_comment.call_args[0][1]
        assert "Paused" in comment

    @pytest.mark.asyncio
    async def test_resume_clears_flag(self):
        manager = _make_manager()
        manager._paused = True

        await manager._cmd_resume("bot-card-123", "Paul")

        assert manager._paused is False
        manager.client.add_comment.assert_called_once()
        comment = manager.client.add_comment.call_args[0][1]
        assert "Resumed" in comment


class TestCmdRestartShutdown:
    """Tests for /restart and /shutdown command handlers."""

    @pytest.mark.asyncio
    async def test_restart_posts_comment_and_exits(self):
        manager = _make_manager()
        manager.stop = AsyncMock()

        with pytest.raises(SystemExit) as exc_info:
            await manager._cmd_restart("bot-card-123", "Paul")

        assert exc_info.value.code == 0
        manager.client.add_comment.assert_called_once()
        comment = manager.client.add_comment.call_args[0][1]
        assert "Restarting" in comment
        manager.stop.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_shutdown_posts_comment_and_exits(self):
        manager = _make_manager()
        manager.stop = AsyncMock()

        with pytest.raises(SystemExit) as exc_info:
            await manager._cmd_shutdown("bot-card-123", "Paul")

        assert exc_info.value.code == 0
        manager.client.add_comment.assert_called_once()
        comment = manager.client.add_comment.call_args[0][1]
        assert "Shutting down" in comment
        manager.stop.assert_awaited_once()


class TestPausedRuleSkipping:
    """Tests for _check_rules skipping when paused."""

    @pytest.mark.asyncio
    async def test_check_rules_skipped_when_paused(self):
        from kardbrd_agent.rules import Rule

        rules = [Rule(name="test", events=["card_moved"], action="/test", list="Ideas")]
        manager = _make_manager(rule_engine=RuleEngine(rules=rules))
        manager._paused = True

        # Should not process any rules
        await manager._check_rules("card_moved", {"card_id": "card-1", "list_name": "Ideas"})
        # No sessions should be created
        assert len(manager._active_sessions) == 0

    @pytest.mark.asyncio
    async def test_check_rules_works_when_not_paused(self):
        from kardbrd_agent.rules import Rule

        rules = [Rule(name="test", events=["card_moved"], action="/test", list="Ideas")]
        manager = _make_manager(rule_engine=RuleEngine(rules=rules))
        manager._paused = False
        manager._process_rule = AsyncMock()

        await manager._check_rules("card_moved", {"card_id": "card-1", "list_name": "Ideas"})
        manager._process_rule.assert_awaited_once()


class TestEnsureBotCardCachesId:
    """Tests that _ensure_bot_card caches the bot card ID."""

    def test_caches_existing_card_id(self):
        manager = _make_manager()
        manager.client.get_board.return_value = {
            "lists": [
                {
                    "id": "list-1",
                    "cards": [{"id": "existing-bot-card", "title": "\U0001f916 TestBot"}],
                }
            ]
        }

        manager._ensure_bot_card()

        assert manager._bot_card_id == "existing-bot-card"
        manager.client.update_card.assert_called_once()

    def test_caches_created_card_id(self):
        manager = _make_manager()
        manager.client.get_board.return_value = {"lists": [{"id": "list-1", "cards": []}]}
        manager.client.create_card.return_value = {"id": "new-bot-card"}

        manager._ensure_bot_card()

        assert manager._bot_card_id == "new-bot-card"
        manager.client.create_card.assert_called_once()
