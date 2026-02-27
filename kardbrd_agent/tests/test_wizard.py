"""Tests for the onboarding wizard card auto-creation."""

from unittest.mock import MagicMock

import pytest

from kardbrd_agent.wizard import (
    WIZARD_CARD_DESCRIPTION,
    WIZARD_CARD_TITLE,
    _card_already_exists,
    _find_target_list,
    ensure_wizard_card,
)

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_board(lists=None):
    """Build a minimal board dict."""
    return {"lists": lists or []}


def _make_list(name, id, cards=None):
    return {"name": name, "id": id, "cards": cards or []}


def _make_card(title, id="card1"):
    return {"title": title, "id": id}


# ---------------------------------------------------------------------------
# _find_target_list
# ---------------------------------------------------------------------------


class TestFindTargetList:
    def test_returns_none_when_no_lists(self):
        assert _find_target_list(_make_board([])) is None

    def test_matches_to_do_list(self):
        lists = [
            _make_list("In Progress", "l2"),
            _make_list("To Do", "l1"),
            _make_list("Done", "l3"),
        ]
        result = _find_target_list(_make_board(lists))
        assert result["id"] == "l1"

    def test_matches_todo_no_space(self):
        lists = [_make_list("todo", "l1"), _make_list("Done", "l2")]
        result = _find_target_list(_make_board(lists))
        assert result["id"] == "l1"

    def test_matches_backlog(self):
        lists = [_make_list("Backlog", "l1"), _make_list("Done", "l2")]
        result = _find_target_list(_make_board(lists))
        assert result["id"] == "l1"

    def test_matches_inbox(self):
        lists = [_make_list("Inbox", "l1")]
        result = _find_target_list(_make_board(lists))
        assert result["id"] == "l1"

    def test_matches_ideas(self):
        lists = [_make_list("Ideas", "l1")]
        result = _find_target_list(_make_board(lists))
        assert result["id"] == "l1"

    def test_falls_back_to_first_list(self):
        lists = [_make_list("Active", "l1"), _make_list("Review", "l2")]
        result = _find_target_list(_make_board(lists))
        assert result["id"] == "l1"

    def test_case_insensitive_match(self):
        lists = [_make_list("TO DO", "l1")]
        result = _find_target_list(_make_board(lists))
        assert result["id"] == "l1"

    def test_priority_order(self):
        """'to do' should beat 'backlog' per heuristic order."""
        lists = [
            _make_list("Backlog", "l2"),
            _make_list("To Do", "l1"),
        ]
        result = _find_target_list(_make_board(lists))
        assert result["id"] == "l1"


# ---------------------------------------------------------------------------
# _card_already_exists
# ---------------------------------------------------------------------------


class TestCardAlreadyExists:
    def test_returns_none_when_no_cards(self):
        board = _make_board([_make_list("To Do", "l1")])
        assert _card_already_exists(board, WIZARD_CARD_TITLE) is None

    def test_finds_existing_wizard_card(self):
        board = _make_board(
            [
                _make_list("To Do", "l1", [_make_card(WIZARD_CARD_TITLE, "wiz1")]),
            ]
        )
        assert _card_already_exists(board, WIZARD_CARD_TITLE) == "wiz1"

    def test_ignores_non_matching_titles(self):
        board = _make_board(
            [
                _make_list("To Do", "l1", [_make_card("Some Other Card", "c1")]),
            ]
        )
        assert _card_already_exists(board, WIZARD_CARD_TITLE) is None

    def test_searches_across_multiple_lists(self):
        board = _make_board(
            [
                _make_list("To Do", "l1", [_make_card("Foo", "c1")]),
                _make_list("Done", "l2", [_make_card(WIZARD_CARD_TITLE, "wiz2")]),
            ]
        )
        assert _card_already_exists(board, WIZARD_CARD_TITLE) == "wiz2"


# ---------------------------------------------------------------------------
# ensure_wizard_card
# ---------------------------------------------------------------------------


class TestEnsureWizardCard:
    def _mock_client(self, board):
        client = MagicMock(spec=["get_board", "create_card", "add_comment"])
        client.get_board.return_value = board
        client.create_card.return_value = {"id": "new_wiz"}
        return client

    def test_creates_card_when_none_exists(self):
        board = _make_board([_make_list("To Do", "l1")])
        client = self._mock_client(board)

        result = ensure_wizard_card(client, "board1", "MBPBot")

        assert result == "new_wiz"
        client.create_card.assert_called_once_with(
            board_id="board1",
            list_id="l1",
            title=WIZARD_CARD_TITLE,
            description=WIZARD_CARD_DESCRIPTION,
        )
        client.add_comment.assert_called_once()
        # Verify welcome comment mentions the agent name
        comment_text = client.add_comment.call_args[0][1]
        assert "@MBPBot" in comment_text

    def test_noop_when_card_already_exists(self):
        board = _make_board(
            [
                _make_list("To Do", "l1", [_make_card(WIZARD_CARD_TITLE, "existing")]),
            ]
        )
        client = self._mock_client(board)

        result = ensure_wizard_card(client, "board1", "MBPBot")

        assert result == "existing"
        client.create_card.assert_not_called()
        client.add_comment.assert_not_called()

    def test_returns_none_when_board_has_no_lists(self):
        board = _make_board([])
        client = self._mock_client(board)

        result = ensure_wizard_card(client, "board1", "MBPBot")

        assert result is None
        client.create_card.assert_not_called()

    def test_places_card_in_heuristic_list(self):
        board = _make_board(
            [
                _make_list("In Progress", "l1"),
                _make_list("Backlog", "l2"),
                _make_list("Done", "l3"),
            ]
        )
        client = self._mock_client(board)

        ensure_wizard_card(client, "board1", "Bot")

        client.create_card.assert_called_once()
        assert client.create_card.call_args[1]["list_id"] == "l2"

    def test_falls_back_to_first_list(self):
        board = _make_board(
            [
                _make_list("Active Work", "l1"),
                _make_list("Review", "l2"),
            ]
        )
        client = self._mock_client(board)

        ensure_wizard_card(client, "board1", "Bot")

        assert client.create_card.call_args[1]["list_id"] == "l1"


# ---------------------------------------------------------------------------
# ProxyManager._ensure_wizard_card integration
# ---------------------------------------------------------------------------


class TestManagerEnsureWizardCard:
    """Test the integration point in ProxyManager."""

    @pytest.mark.asyncio
    async def test_skips_when_rules_present(self):
        from kardbrd_agent.manager import ProxyManager
        from kardbrd_agent.rules import Rule, RuleEngine

        rule = Rule(name="r1", events=["card_moved"], action="/ki")
        engine = RuleEngine(rules=[rule])
        manager = ProxyManager(
            board_id="b1",
            api_url="https://test.kardbrd.com",
            bot_token="tok",
            agent_name="Bot",
            rule_engine=engine,
        )
        # Set up a mock client so we can verify it's never called
        manager.client = MagicMock()

        await manager._ensure_wizard_card()

        manager.client.get_board.assert_not_called()

    @pytest.mark.asyncio
    async def test_calls_ensure_when_no_rules(self):
        from kardbrd_agent.manager import ProxyManager
        from kardbrd_agent.rules import RuleEngine

        manager = ProxyManager(
            board_id="b1",
            api_url="https://test.kardbrd.com",
            bot_token="tok",
            agent_name="Bot",
            rule_engine=RuleEngine(),
        )
        board = _make_board([_make_list("To Do", "l1")])
        mock_client = MagicMock()
        mock_client.get_board.return_value = board
        mock_client.create_card.return_value = {"id": "wiz1"}
        manager.client = mock_client

        await manager._ensure_wizard_card()

        mock_client.get_board.assert_called_once_with("b1")
        mock_client.create_card.assert_called_once()

    @pytest.mark.asyncio
    async def test_handles_api_errors_gracefully(self):
        from kardbrd_agent.manager import ProxyManager
        from kardbrd_agent.rules import RuleEngine

        manager = ProxyManager(
            board_id="b1",
            api_url="https://test.kardbrd.com",
            bot_token="tok",
            agent_name="Bot",
            rule_engine=RuleEngine(),
        )
        mock_client = MagicMock()
        mock_client.get_board.side_effect = Exception("API down")
        manager.client = mock_client

        # Should not raise — errors are caught and logged
        await manager._ensure_wizard_card()
