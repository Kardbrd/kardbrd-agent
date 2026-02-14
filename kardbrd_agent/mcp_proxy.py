"""Session tracking data classes and logging utilities.

ProxySession and ProxySessionRegistry track card processing state.
The _redact_sensitive helper is used for safe logging of tool arguments.

Note: Previously this module hosted a FastMCP HTTP/SSE proxy server.
MCP tools are now provided by the kardbrd-mcp stdio subprocess from
the kardbrd-client package.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
from typing import Any

logger = logging.getLogger("kardbrd_agent.mcp_proxy")


@dataclass
class ProxySession:
    """Tracks tool calls during a Claude session for verification."""

    comment_posted: bool = False
    card_updated: bool = False
    labels_modified: bool = False
    tools_called: list[str] = field(default_factory=list)

    def reset(self) -> None:
        """Reset session state before a new execution."""
        self.comment_posted = False
        self.card_updated = False
        self.labels_modified = False
        self.tools_called.clear()

    def record_tool_call(self, tool_name: str, arguments: dict[str, Any]) -> None:
        """Record a tool call and track significant actions."""
        self.tools_called.append(tool_name)

        if tool_name == "add_comment":
            self.comment_posted = True
            logger.debug("Session: comment posted")
        elif tool_name == "update_card":
            self.card_updated = True
            if "label_ids" in arguments:
                self.labels_modified = True
                logger.debug("Session: card labels modified")
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
    def labels_modified(self) -> bool:
        """Check if current session has modified card labels."""
        session = self.get_current_session()
        return session.labels_modified if session else False

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
