"""Tests for the MCP proxy session tracking and utilities."""

from kardbrd_agent.mcp_proxy import _redact_sensitive


class TestRedactSensitive:
    """Tests for the _redact_sensitive helper function."""

    def test_redacts_token_key(self):
        """Token values should be redacted."""
        data = {"board_id": "abc123", "token": "secret_token"}
        result = _redact_sensitive(data)
        assert result["board_id"] == "abc123"
        assert result["token"] == "[REDACTED]"

    def test_redacts_password_key(self):
        """Password values should be redacted."""
        data = {"password": "my_password", "user": "test"}
        result = _redact_sensitive(data)
        assert result["password"] == "[REDACTED]"
        assert result["user"] == "test"

    def test_redacts_content_key(self):
        """Content values should be redacted."""
        data = {"card_id": "xyz", "content": "markdown content here"}
        result = _redact_sensitive(data)
        assert result["card_id"] == "xyz"
        assert result["content"] == "[REDACTED]"

    def test_truncates_long_strings(self):
        """Long string values should be truncated."""
        long_text = "x" * 300
        data = {"description": long_text}
        result = _redact_sensitive(data)
        assert "100 chars" not in result["description"]  # Not content, so not fully redacted
        assert "(300 chars)" in result["description"]
        assert len(result["description"]) < 300

    def test_preserves_short_strings(self):
        """Short strings should not be truncated."""
        data = {"title": "Short title", "board_id": "abc"}
        result = _redact_sensitive(data)
        assert result == data


class TestProxySession:
    """Tests for ProxySession dataclass."""

    def test_session_initial_state(self):
        """Test session starts with all flags False."""
        from kardbrd_agent.mcp_proxy import ProxySession

        session = ProxySession()
        assert session.comment_posted is False
        assert session.card_updated is False
        assert session.labels_modified is False
        assert session.tools_called == []

    def test_session_reset(self):
        """Test reset clears all state."""
        from kardbrd_agent.mcp_proxy import ProxySession

        session = ProxySession()
        session.comment_posted = True
        session.card_updated = True
        session.labels_modified = True
        session.tools_called = ["add_comment", "get_card"]

        session.reset()

        assert session.comment_posted is False
        assert session.card_updated is False
        assert session.labels_modified is False
        assert session.tools_called == []

    def test_record_add_comment_sets_flag(self):
        """Test recording add_comment sets comment_posted flag."""
        from kardbrd_agent.mcp_proxy import ProxySession

        session = ProxySession()
        session.record_tool_call("add_comment", {"card_id": "abc", "content": "hi"})

        assert session.comment_posted is True
        assert "add_comment" in session.tools_called

    def test_record_update_card_sets_flag(self):
        """Test recording update_card sets card_updated flag."""
        from kardbrd_agent.mcp_proxy import ProxySession

        session = ProxySession()
        session.record_tool_call("update_card", {"card_id": "abc", "title": "New"})

        assert session.card_updated is True
        assert "update_card" in session.tools_called

    def test_record_other_tool_no_flags(self):
        """Test recording other tools doesn't set flags."""
        from kardbrd_agent.mcp_proxy import ProxySession

        session = ProxySession()
        session.record_tool_call("get_board", {"board_id": "xyz"})

        assert session.comment_posted is False
        assert session.card_updated is False
        assert "get_board" in session.tools_called


class TestProxySessionRegistry:
    """Tests for ProxySessionRegistry."""

    def test_registry_creates_session_on_set_current(self):
        """Test setting current card creates session if not exists."""
        from kardbrd_agent.mcp_proxy import ProxySession, ProxySessionRegistry

        registry = ProxySessionRegistry()
        registry.set_current_card("card1")

        session = registry.get_current_session()
        assert session is not None
        assert isinstance(session, ProxySession)

    def test_registry_returns_same_session_for_same_card(self):
        """Test same session returned for same card."""
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()
        registry.set_current_card("card1")
        session1 = registry.get_current_session()

        registry.set_current_card("card1")
        session2 = registry.get_current_session()

        assert session1 is session2

    def test_registry_different_sessions_for_different_cards(self):
        """Test different cards get different sessions."""
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()

        registry.set_current_card("card1")
        session1 = registry.get_current_session()
        session1.comment_posted = True

        registry.set_current_card("card2")
        session2 = registry.get_current_session()

        assert session1 is not session2
        assert session2.comment_posted is False

    def test_registry_records_to_current_card(self):
        """Test tool calls recorded to current card's session."""
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()

        registry.set_current_card("card1")
        registry.record_tool_call("add_comment", {"card_id": "card1"})

        session1 = registry.get_current_session()
        assert session1.comment_posted is True

        # Switch to card2 - should have clean state
        registry.set_current_card("card2")
        session2 = registry.get_current_session()
        assert session2.comment_posted is False

    def test_registry_cleanup_removes_session(self):
        """Test cleanup removes card's session."""
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()
        registry.set_current_card("card1")

        registry.cleanup_card("card1")

        # Current should be None now
        assert registry.get_current_session() is None

    def test_registry_no_session_without_current_card(self):
        """Test get_current_session returns None without card set."""
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()
        assert registry.get_current_session() is None

    def test_registry_get_session_by_id(self):
        """Test get_session retrieves session by card ID."""
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()
        registry.set_current_card("card1")
        registry.record_tool_call("add_comment", {})

        # Switch to card2
        registry.set_current_card("card2")

        # Should still be able to get card1's session
        session1 = registry.get_session("card1")
        assert session1 is not None
        assert session1.comment_posted is True

    def test_registry_legacy_comment_posted_property(self):
        """Test legacy comment_posted property returns current session's value."""
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()
        registry.set_current_card("card1")
        registry.record_tool_call("add_comment", {})

        assert registry.comment_posted is True

    def test_registry_legacy_card_updated_property(self):
        """Test legacy card_updated property returns current session's value."""
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()
        registry.set_current_card("card1")
        registry.record_tool_call("update_card", {})

        assert registry.card_updated is True

    def test_registry_legacy_tools_called_property(self):
        """Test legacy tools_called property returns current session's value."""
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()
        registry.set_current_card("card1")
        registry.record_tool_call("get_board", {})
        registry.record_tool_call("add_comment", {})

        assert registry.tools_called == ["get_board", "add_comment"]

    def test_registry_legacy_reset(self):
        """Test legacy reset clears current session state."""
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()
        registry.set_current_card("card1")
        registry.record_tool_call("add_comment", {})

        registry.reset()

        assert registry.comment_posted is False
        assert registry.tools_called == []

    def test_record_tool_call_uses_card_id_from_arguments(self):
        """Tool calls should record to the card specified in arguments, not _current_card_id.

        This tests the fix for a race condition where concurrent card processing
        could cause tool calls to be recorded to the wrong session.
        """
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()

        # Set up sessions for both cards
        registry.set_current_card("card_a")
        registry.set_current_card("card_b")  # Now card_b is "current"

        # Simulate MCP tool call for card_a while card_b is current
        # (This happens during concurrent processing when _current_card_id gets overwritten)
        registry.record_tool_call("add_comment", {"card_id": "card_a", "content": "test"})

        # Should record to card_a (from arguments), not card_b (current)
        assert registry.get_session("card_a").comment_posted is True
        assert registry.get_session("card_b").comment_posted is False

    def test_record_tool_call_falls_back_to_current_when_no_card_id(self):
        """Tool calls without card_id should still use current session."""
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()
        registry.set_current_card("card_a")

        # Tool call without card_id in arguments
        registry.record_tool_call("get_board", {"board_id": "board123"})

        assert "get_board" in registry.get_session("card_a").tools_called

    def test_record_tool_call_falls_back_when_card_id_not_in_sessions(self):
        """Tool calls with unknown card_id should fall back to current session."""
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()
        registry.set_current_card("card_a")

        # Tool call with card_id that doesn't have a session
        registry.record_tool_call("add_comment", {"card_id": "unknown_card", "content": "test"})

        # Should fall back to current session (card_a)
        assert registry.get_session("card_a").comment_posted is True

    def test_registry_labels_modified_property(self):
        """Test legacy labels_modified property returns current session's value."""
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()
        registry.set_current_card("card1")
        registry.record_tool_call(
            "update_card", {"card_id": "card1", "label_ids": ["label1", "label2"]}
        )

        assert registry.labels_modified is True

    def test_registry_labels_modified_false_without_label_ids(self):
        """Test labels_modified is False when update_card has no label_ids."""
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()
        registry.set_current_card("card1")
        registry.record_tool_call("update_card", {"card_id": "card1", "title": "New Title"})

        assert registry.labels_modified is False

    def test_registry_labels_modified_false_no_session(self):
        """Test labels_modified is False when no session is set."""
        from kardbrd_agent.mcp_proxy import ProxySessionRegistry

        registry = ProxySessionRegistry()
        assert registry.labels_modified is False


class TestProxySessionLabels:
    """Tests for label tracking in ProxySession."""

    def test_update_card_with_label_ids_sets_labels_modified(self):
        """Test update_card with label_ids sets labels_modified flag."""
        from kardbrd_agent.mcp_proxy import ProxySession

        session = ProxySession()
        session.record_tool_call(
            "update_card", {"card_id": "abc", "label_ids": ["label1", "label2"]}
        )

        assert session.card_updated is True
        assert session.labels_modified is True

    def test_update_card_without_label_ids_no_labels_modified(self):
        """Test update_card without label_ids does not set labels_modified."""
        from kardbrd_agent.mcp_proxy import ProxySession

        session = ProxySession()
        session.record_tool_call("update_card", {"card_id": "abc", "title": "New"})

        assert session.card_updated is True
        assert session.labels_modified is False

    def test_update_card_with_empty_label_ids_sets_labels_modified(self):
        """Test update_card with empty label_ids (clear labels) sets labels_modified."""
        from kardbrd_agent.mcp_proxy import ProxySession

        session = ProxySession()
        session.record_tool_call("update_card", {"card_id": "abc", "label_ids": []})

        assert session.card_updated is True
        assert session.labels_modified is True

    def test_get_board_labels_tracked_in_tools_called(self):
        """Test get_board_labels is tracked in tools_called list."""
        from kardbrd_agent.mcp_proxy import ProxySession

        session = ProxySession()
        session.record_tool_call("get_board_labels", {"board_id": "board1"})

        assert "get_board_labels" in session.tools_called
        assert session.labels_modified is False  # Reading labels doesn't modify them
