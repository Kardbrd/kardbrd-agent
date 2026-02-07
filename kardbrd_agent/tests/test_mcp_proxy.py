"""Tests for the MCP proxy server."""

from unittest.mock import MagicMock, patch

import pytest
from kardbrd_client import BoardSubscription, DirectoryStateManager

from kardbrd_agent.mcp_proxy import (
    ProxyTool,
    _create_proxy_tool,
    _redact_sensitive,
    create_mcp_server,
)


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


class TestCreateMcpServer:
    """Tests for the create_mcp_server function."""

    def test_raises_error_when_no_subscriptions(self, tmp_path):
        """Should raise RuntimeError when no subscriptions exist."""
        state_manager = DirectoryStateManager(str(tmp_path))

        with pytest.raises(RuntimeError, match="No subscriptions configured"):
            create_mcp_server(state_manager)

    @pytest.mark.asyncio
    async def test_creates_server_with_subscription(self, tmp_path):
        """Should create MCP server when subscription exists."""
        state_manager = DirectoryStateManager(str(tmp_path))
        subscription = BoardSubscription(
            board_id="test-board",
            api_url="http://localhost:8000",
            bot_token="test-token",
            agent_name="TestBot",
        )
        state_manager.add_subscription(subscription)

        with patch("kardbrd_agent.mcp_proxy.KardbrdClient") as mock_client:
            mock_client.return_value = MagicMock()
            mcp = create_mcp_server(state_manager)

        assert mcp.name == "kardbrd-proxy"
        # Should have all tools registered
        tools = await mcp.get_tools()
        assert len(tools) > 0

    def test_uses_bot_token_for_client(self, tmp_path):
        """Should use the subscription's bot token for the client."""
        state_manager = DirectoryStateManager(str(tmp_path))
        subscription = BoardSubscription(
            board_id="test-board",
            api_url="http://api.example.com",
            bot_token="bot-secret-token",
            agent_name="TestBot",
        )
        state_manager.add_subscription(subscription)

        with patch("kardbrd_agent.mcp_proxy.KardbrdClient") as mock_client:
            mock_client.return_value = MagicMock()
            create_mcp_server(state_manager)

        mock_client.assert_called_once_with(
            base_url="http://api.example.com",
            token="bot-secret-token",
        )


class TestProxyTool:
    """Tests for the ProxyTool class."""

    def test_creates_tool_with_correct_name(self):
        """Should create tool with the correct name."""
        executor = MagicMock()
        tool = ProxyTool(
            executor=executor,
            tool_name="test_tool",
            description="A test tool",
            parameters={"type": "object", "properties": {}, "required": []},
        )

        assert tool.name == "test_tool"

    def test_creates_tool_with_correct_description(self):
        """Should create tool with the correct description."""
        executor = MagicMock()
        tool = ProxyTool(
            executor=executor,
            tool_name="my_tool",
            description="This is my tool description",
            parameters={"type": "object", "properties": {}, "required": []},
        )

        assert tool.description == "This is my tool description"

    @pytest.mark.asyncio
    async def test_tool_calls_executor(self):
        """Should call executor when tool is run and wrap result in ToolResult."""
        from fastmcp.tools.tool import ToolResult

        executor = MagicMock()
        executor.execute.return_value = {"success": True}
        tool = ProxyTool(
            executor=executor,
            tool_name="get_board",
            description="Get a board",
            parameters={
                "type": "object",
                "properties": {"board_id": {"type": "string"}},
                "required": ["board_id"],
            },
        )

        result = await tool.run({"board_id": "test-123"})

        # ToolExecutor.execute was called with correct args
        executor.execute.assert_called_once_with("get_board", {"board_id": "test-123"})
        # Result should be wrapped in ToolResult
        assert isinstance(result, ToolResult)
        assert result.structured_content == {"success": True}

    @pytest.mark.asyncio
    async def test_tool_wraps_string_result(self):
        """Should wrap string results in ToolResult with content field."""
        from fastmcp.tools.tool import ToolResult

        executor = MagicMock()
        executor.execute.return_value = "# Board Markdown\n\nSome content"
        tool = ProxyTool(
            executor=executor,
            tool_name="get_board_markdown",
            description="Get board as markdown",
            parameters={
                "type": "object",
                "properties": {"board_id": {"type": "string"}},
                "required": ["board_id"],
            },
        )

        result = await tool.run({"board_id": "test-123"})

        # String results should use content field, not structured_content
        assert isinstance(result, ToolResult)
        # content is a list of TextContent objects
        assert len(result.content) == 1
        assert result.content[0].text == "# Board Markdown\n\nSome content"
        assert result.structured_content is None


class TestCreateProxyTool:
    """Tests for the _create_proxy_tool function."""

    def test_creates_tool_from_definition(self):
        """Should create ProxyTool from tool definition."""
        executor = MagicMock()
        tool_def = {
            "name": "get_card",
            "description": "Get card details",
            "input_schema": {
                "type": "object",
                "properties": {"card_id": {"type": "string"}},
                "required": ["card_id"],
            },
        }

        tool = _create_proxy_tool(executor, tool_def)

        assert isinstance(tool, ProxyTool)
        assert tool.name == "get_card"
        assert tool.description == "Get card details"


class TestRunHttpAsync:
    """Tests for the run_http_async function."""

    @pytest.mark.asyncio
    async def test_run_http_async_raises_without_subscription(self, tmp_path):
        """Should raise RuntimeError when no subscriptions exist."""
        from kardbrd_agent.mcp_proxy import run_http_async

        with pytest.raises(RuntimeError, match="No subscriptions configured"):
            await run_http_async(state_dir=str(tmp_path), port=8765)

    @pytest.mark.asyncio
    async def test_run_http_async_creates_server(self, tmp_path):
        """Should create and start HTTP server with subscription."""
        from unittest.mock import AsyncMock

        from kardbrd_agent.mcp_proxy import run_http_async

        state_manager = DirectoryStateManager(str(tmp_path))
        subscription = BoardSubscription(
            board_id="test-board",
            api_url="http://localhost:8000",
            bot_token="test-token",
            agent_name="TestBot",
        )
        state_manager.add_subscription(subscription)

        # Mock the FastMCP.run_async
        with (
            patch("kardbrd_agent.mcp_proxy.KardbrdClient") as mock_client,
            patch("kardbrd_agent.mcp_proxy.create_mcp_server") as mock_create,
        ):
            mock_mcp = MagicMock()
            mock_mcp.run_http_async = AsyncMock()
            mock_create.return_value = mock_mcp
            mock_client.return_value = MagicMock()

            await run_http_async(state_dir=str(tmp_path), port=9999)

            mock_mcp.run_http_async.assert_called_once_with(transport="sse", port=9999)


class TestProxySession:
    """Tests for ProxySession dataclass."""

    def test_session_initial_state(self):
        """Test session starts with all flags False."""
        from kardbrd_agent.mcp_proxy import ProxySession

        session = ProxySession()
        assert session.comment_posted is False
        assert session.card_updated is False
        assert session.tools_called == []

    def test_session_reset(self):
        """Test reset clears all state."""
        from kardbrd_agent.mcp_proxy import ProxySession

        session = ProxySession()
        session.comment_posted = True
        session.card_updated = True
        session.tools_called = ["add_comment", "get_card"]

        session.reset()

        assert session.comment_posted is False
        assert session.card_updated is False
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
