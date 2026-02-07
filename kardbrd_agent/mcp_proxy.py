"""MCP proxy server that exposes kardbrd tools via FastMCP.

This module creates an MCP server that proxies tool calls to the kardbrd API
using the bot's authentication token from the subscription state.
"""

from __future__ import annotations

import asyncio
import logging
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any

from fastmcp import FastMCP
from fastmcp.tools import Tool
from kardbrd_client import (
    TOOLS,
    DirectoryStateManager,
    KardbrdClient,
    ToolExecutor,
)

if TYPE_CHECKING:
    from fastmcp.tools.tool import ToolResult

logger = logging.getLogger("kardbrd_agent.mcp_proxy")


@dataclass
class ProxySession:
    """Tracks tool calls during a Claude session for verification."""

    comment_posted: bool = False
    card_updated: bool = False
    tools_called: list[str] = field(default_factory=list)

    def reset(self) -> None:
        """Reset session state before a new execution."""
        self.comment_posted = False
        self.card_updated = False
        self.tools_called.clear()

    def record_tool_call(self, tool_name: str, arguments: dict[str, Any]) -> None:
        """Record a tool call and track significant actions."""
        self.tools_called.append(tool_name)

        if tool_name == "add_comment":
            self.comment_posted = True
            logger.debug("Session: comment posted")
        elif tool_name == "update_card":
            self.card_updated = True
            logger.debug("Session: card updated")


class ProxySessionRegistry:
    """Registry of per-card sessions for concurrent processing.

    This class manages session state per-card to avoid race conditions
    when multiple cards are being processed concurrently. Each card gets
    its own ProxySession instance, and tool calls are recorded to the
    current card's session based on context.
    """

    def __init__(self) -> None:
        """Initialize the registry."""
        self._sessions: dict[str, ProxySession] = {}
        self._current_card_id: str | None = None

    def set_current_card(self, card_id: str) -> None:
        """Set the current card context for tool calls.

        Args:
            card_id: The card ID to set as current context
        """
        self._current_card_id = card_id
        if card_id not in self._sessions:
            self._sessions[card_id] = ProxySession()

    def get_current_session(self) -> ProxySession | None:
        """Get session for current card.

        Returns:
            The ProxySession for the current card, or None if no card set
        """
        if self._current_card_id:
            return self._sessions.get(self._current_card_id)
        return None

    def get_session(self, card_id: str) -> ProxySession | None:
        """Get session for a specific card.

        Args:
            card_id: The card ID to get session for

        Returns:
            The ProxySession for the card, or None if not found
        """
        return self._sessions.get(card_id)

    def record_tool_call(self, tool_name: str, arguments: dict[str, Any]) -> None:
        """Record tool call to the appropriate card's session.

        For card-specific tools (add_comment, update_card), uses card_id from
        arguments to ensure correct session tracking during concurrent processing.
        This prevents a race condition where _current_card_id could be overwritten
        by another concurrent task before this tool call is recorded.

        Args:
            tool_name: Name of the tool being called
            arguments: Arguments passed to the tool
        """
        # For card-specific tools, use card_id from arguments if available
        card_id = arguments.get("card_id")
        if card_id and card_id in self._sessions:
            session = self._sessions[card_id]
        else:
            session = self.get_current_session()

        if session:
            session.record_tool_call(tool_name, arguments)

    def cleanup_card(self, card_id: str) -> None:
        """Remove session for a card.

        Args:
            card_id: The card ID to clean up
        """
        self._sessions.pop(card_id, None)
        if self._current_card_id == card_id:
            self._current_card_id = None

    # Legacy compatibility: allow the registry to act like a single session
    # for the MCP tools that receive a session reference
    @property
    def comment_posted(self) -> bool:
        """Check if current session has posted a comment."""
        session = self.get_current_session()
        return session.comment_posted if session else False

    @property
    def card_updated(self) -> bool:
        """Check if current session has updated a card."""
        session = self.get_current_session()
        return session.card_updated if session else False

    @property
    def tools_called(self) -> list[str]:
        """Get list of tools called in current session."""
        session = self.get_current_session()
        return session.tools_called if session else []

    def reset(self) -> None:
        """Reset current session state (legacy compatibility)."""
        session = self.get_current_session()
        if session:
            session.reset()


class ProxyTool(Tool):
    """A Tool that proxies calls to a ToolExecutor."""

    _executor: ToolExecutor
    _tool_name: str
    _session: ProxySession | None

    def __init__(
        self,
        executor: ToolExecutor,
        tool_name: str,
        description: str,
        parameters: dict[str, Any],
        session: ProxySession | None = None,
    ):
        """Initialize the proxy tool.

        Args:
            executor: The ToolExecutor to proxy calls to
            tool_name: Name of the tool
            description: Tool description
            parameters: JSON schema for tool parameters
            session: Optional session tracker for recording tool calls
        """
        super().__init__(
            name=tool_name,
            description=description,
            parameters=parameters,
        )
        # Store as instance attributes (not model fields)
        object.__setattr__(self, "_executor", executor)
        object.__setattr__(self, "_tool_name", tool_name)
        object.__setattr__(self, "_session", session)

    async def run(self, arguments: dict[str, Any]) -> ToolResult:
        """Execute the tool via ToolExecutor.

        Args:
            arguments: Tool arguments from MCP client

        Returns:
            ToolResult wrapping the tool execution result
        """
        from fastmcp.tools.tool import ToolResult

        name = object.__getattribute__(self, "_tool_name")
        executor = object.__getattribute__(self, "_executor")
        session = object.__getattribute__(self, "_session")

        logger.info(f"Proxying tool call: {name}")
        logger.debug(f"Arguments: {_redact_sensitive(arguments)}")

        # Track the tool call in session
        if session:
            session.record_tool_call(name, arguments)

        try:
            # ToolExecutor is sync, run in thread pool
            result = await asyncio.to_thread(executor.execute, name, arguments)
            logger.debug(f"Tool {name} completed successfully")
            # Wrap result in ToolResult for FastMCP compatibility
            if isinstance(result, str):
                return ToolResult(content=result)
            return ToolResult(structured_content=result)
        except Exception as e:
            logger.error(f"Tool {name} failed: {e}")
            raise


def create_mcp_server(
    state_manager: DirectoryStateManager,
    session: ProxySession | None = None,
) -> FastMCP:
    """
    Create a FastMCP server with all kardbrd tools proxied.

    Args:
        state_manager: State manager containing the bot subscription
        session: Optional session tracker for recording tool calls

    Returns:
        Configured FastMCP server instance
    """
    mcp = FastMCP(
        name="kardbrd-proxy",
        instructions=(
            "Kardbrd MCP proxy server. Provides access to kardbrd board tools "
            "including reading/writing cards, comments, checklists, and more."
        ),
    )

    # Get the first subscription (Phase 1.1 only supports single board)
    subscriptions = state_manager.get_all_subscriptions()
    if not subscriptions:
        raise RuntimeError(
            "No subscriptions configured. Use 'kardbrd-agent sub <setup-url>' first."
        )

    # Use first subscription
    board_id, subscription = next(iter(subscriptions.items()))
    logger.info(f"Proxy configured for board {board_id} as @{subscription.agent_name}")

    # Create client and executor with bot's credentials
    client = KardbrdClient(
        base_url=subscription.api_url,
        token=subscription.bot_token,
    )
    executor = ToolExecutor(client)

    # Register all tools from TOOLS schema
    for tool_def in TOOLS:
        tool = _create_proxy_tool(executor, tool_def, session)
        mcp.add_tool(tool)

    return mcp


def _create_proxy_tool(
    executor: ToolExecutor,
    tool_def: dict[str, Any],
    session: ProxySession | None = None,
) -> ProxyTool:
    """
    Create a ProxyTool from a tool definition.

    Args:
        executor: The ToolExecutor to proxy calls to
        tool_def: Tool definition from TOOLS schema
        session: Optional session tracker for recording tool calls

    Returns:
        A ProxyTool instance
    """
    return ProxyTool(
        executor=executor,
        tool_name=tool_def["name"],
        description=tool_def["description"],
        parameters=tool_def["input_schema"],
        session=session,
    )


def _redact_sensitive(data: dict[str, Any]) -> dict[str, Any]:
    """Redact potentially sensitive data from logs."""
    redacted = {}
    sensitive_keys = {"token", "password", "secret", "key", "content"}

    for k, v in data.items():
        if any(s in k.lower() for s in sensitive_keys):
            redacted[k] = "[REDACTED]"
        elif isinstance(v, str) and len(v) > 200:
            redacted[k] = f"{v[:100]}... ({len(v)} chars)"
        else:
            redacted[k] = v

    return redacted


async def run_http_async(
    state_dir: str = "state",
    port: int = 8765,
    session: ProxySession | None = None,
) -> None:
    """
    Run the MCP proxy server with HTTP/SSE transport (async).

    Args:
        state_dir: Path to state directory
        port: Port to listen on (default 8765)
        session: Optional session tracker for recording tool calls
    """
    state_manager = DirectoryStateManager(state_dir)
    mcp = create_mcp_server(state_manager, session=session)

    logger.info(f"Starting kardbrd MCP proxy server (HTTP) on port {port}")
    await mcp.run_http_async(transport="sse", port=port)


def run_mcp_server(state_dir: str = "state") -> None:
    """
    Run the MCP proxy server with stdio transport.

    This is the main entry point for the proxy-mcp CLI command.
    """
    from kardbrd_client import configure_logging

    configure_logging()

    state_manager = DirectoryStateManager(state_dir)
    mcp = create_mcp_server(state_manager)

    logger.info("Starting kardbrd MCP proxy server (stdio)")
    mcp.run()
