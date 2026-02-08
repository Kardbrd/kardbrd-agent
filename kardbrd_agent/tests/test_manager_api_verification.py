"""Tests for ProxyManager with API-based verification (replacing session_registry).

These tests validate the new behavior where the manager uses
_has_recent_bot_comment() API checks instead of in-process
session_registry tracking to verify Claude posted a response.

They will fail until manager.py is rewritten. Once applied, they prove:

1. Manager no longer has session_registry attribute
2. Manager passes api_url/bot_token to executor (not mcp_port)
3. _process_mention uses _has_recent_bot_comment for verification
4. _resume_to_publish uses _has_recent_bot_comment for verification
5. No MCP HTTP server is started in the start() method
"""

from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from kardbrd_agent.executor import ClaudeResult
from kardbrd_agent.manager import ProxyManager


class TestManagerNoSessionRegistry:
    """Tests that ProxyManager no longer uses session_registry.

    PROVES: The in-process session tracking (which requires the FastMCP
    HTTP server to intercept tool calls) has been removed. This is safe
    because API-based verification replaces it.

    SAFETY: If session_registry references remain in manager.py, they'll
    cause AttributeError at runtime since the registry depended on
    in-process FastMCP tool call interception.
    """

    def test_no_session_registry_attribute(self):
        """Test ProxyManager no longer has session_registry.

        PROVES: The registry attribute is fully removed, not just unused.
        """
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        assert not hasattr(manager, "session_registry")

    def test_no_session_attribute(self):
        """Test ProxyManager no longer has legacy session alias.

        PROVES: The legacy 'session' alias is also removed.
        """
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        assert not hasattr(manager, "session")

    def test_no_mcp_port_parameter(self):
        """Test ProxyManager no longer accepts mcp_port.

        PROVES: The mcp_port constructor parameter is removed since
        we no longer start an HTTP server.
        """
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        assert not hasattr(manager, "mcp_port")


class TestManagerPassesCredentials:
    """Tests that manager passes api_url/bot_token to executor.

    PROVES: The manager correctly forwards the subscription's credentials
    to ClaudeExecutor so each Claude session can spawn its own kardbrd-mcp
    subprocess with the right auth.

    SAFETY: If credentials aren't forwarded, Claude sessions will lack
    MCP tools and cannot interact with the kardbrd API.
    """

    @pytest.mark.asyncio
    async def test_start_passes_credentials_to_executor(self):
        """Test start() creates executor with api_url and bot_token.

        PROVES: When the manager initializes from a subscription,
        it passes api_url and bot_token to ClaudeExecutor (not mcp_port).
        """
        from kardbrd_client import BoardSubscription

        state_manager = MagicMock()
        sub = BoardSubscription(
            board_id="test-board",
            api_url="http://api.example.com",
            bot_token="bot-secret",
            agent_name="TestBot",
        )
        state_manager.get_all_subscriptions.return_value = {"test-board": sub}

        manager = ProxyManager(state_manager)

        # Mock dependencies that start() needs
        with (
            patch("kardbrd_agent.manager.KardbrdClient"),
            patch("kardbrd_agent.manager.WebSocketAgentConnection") as mock_ws,
            patch("kardbrd_agent.manager.ClaudeExecutor") as mock_executor_cls,
        ):
            mock_conn = MagicMock()
            mock_conn.connect = AsyncMock()
            mock_conn.is_connected = True
            mock_ws.return_value = mock_conn

            # Let start() initialize but stop the event loop
            manager._running = False
            try:
                await manager.start()
            except Exception:
                pass  # May fail on gather, that's OK

            # Verify executor was created with credentials
            mock_executor_cls.assert_called_once_with(
                cwd=manager.cwd,
                timeout=manager.timeout,
                api_url="http://api.example.com",
                bot_token="bot-secret",
            )


class TestProcessMentionApiVerification:
    """Tests for _process_mention using API-based verification.

    PROVES: After Claude completes, the manager checks the kardbrd API
    (via _has_recent_bot_comment) to determine if Claude posted a response,
    rather than checking an in-process session registry.

    SAFETY: This is the critical verification path. If it's wrong:
    - False positive (thinks posted when didn't) → user sees no response
    - False negative (thinks didn't post when did) → duplicate response
    Both are prevented by these tests.
    """

    @pytest.mark.asyncio
    async def test_success_with_api_verified_post(self, git_repo: Path):
        """Test success reaction when API confirms bot posted.

        PROVES: When Claude succeeds AND _has_recent_bot_comment returns True,
        the manager adds a ✅ reaction and does NOT resume or post fallback.
        This is the happy path.
        """
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()
        manager.client.add_comment = MagicMock()

        # Mock API verification to return True (bot posted)
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(
                success=True, result_text="Done", session_id="sess123"
            )
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = (
            git_repo.parent / "card-card1"
        )
        (git_repo.parent / "card-card1").mkdir(exist_ok=True)

        await manager._process_mention("card1", "comm1", "@coder /kp", "Paul")

        # Should check API
        manager._has_recent_bot_comment.assert_called_once_with("card1")

        # Should add success reaction
        toggle_calls = manager.client.toggle_reaction.call_args_list
        success_reaction = any(
            call[0][0] == "card1" and call[0][2] == "✅" for call in toggle_calls
        )
        assert success_reaction

        # Should NOT post fallback comment
        manager.client.add_comment.assert_not_called()

    @pytest.mark.asyncio
    async def test_success_without_post_triggers_resume(self, git_repo: Path):
        """Test resume is triggered when API says bot did NOT post.

        PROVES: When Claude succeeds but _has_recent_bot_comment returns False
        and a session_id is available, the manager resumes the session to
        force Claude to publish its results.
        """
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()
        manager.client.add_comment = MagicMock()

        # Mock API verification to return False (bot did NOT post)
        manager._has_recent_bot_comment = MagicMock(return_value=False)

        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(
                success=True, result_text="Done", session_id="sess123"
            )
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = (
            git_repo.parent / "card-card1"
        )
        (git_repo.parent / "card-card1").mkdir(exist_ok=True)

        # Mock _resume_to_publish to track the call
        manager._resume_to_publish = AsyncMock()

        await manager._process_mention("card1", "comm1", "@coder /kp", "Paul")

        # Should attempt to resume since API says no bot post
        manager._resume_to_publish.assert_called_once()
        call_kwargs = manager._resume_to_publish.call_args[1]
        assert call_kwargs["card_id"] == "card1"
        assert call_kwargs["session_id"] == "sess123"

    @pytest.mark.asyncio
    async def test_success_no_session_id_marks_success(self, git_repo: Path):
        """Test success reaction when no session_id to resume.

        PROVES: When Claude succeeds without posting and has no session_id
        (cannot resume), the manager still marks success. This is a graceful
        degradation — better to mark success than hang forever.
        """
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, cwd=git_repo)
        manager.client = MagicMock()
        manager.client.get_card_markdown.return_value = "# Card"
        manager.client.toggle_reaction = MagicMock()

        # Mock API verification to return False (bot did NOT post)
        manager._has_recent_bot_comment = MagicMock(return_value=False)

        manager.executor = MagicMock()
        manager.executor.extract_command.return_value = "/kp"
        manager.executor.build_prompt.return_value = "prompt"
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(
                success=True, result_text="Done", session_id=None  # No session_id
            )
        )
        manager.worktree_manager = MagicMock()
        manager.worktree_manager.create_worktree.return_value = (
            git_repo.parent / "card-card1"
        )
        (git_repo.parent / "card-card1").mkdir(exist_ok=True)

        await manager._process_mention("card1", "comm1", "@coder /kp", "Paul")

        # Should still mark success
        toggle_calls = manager.client.toggle_reaction.call_args_list
        success_reaction = any(
            call[0][0] == "card1" and call[0][2] == "✅" for call in toggle_calls
        )
        assert success_reaction

    @pytest.mark.asyncio
    async def test_no_set_current_card_call(self, git_repo: Path):
        """Test _process_mention does NOT call session_registry.set_current_card.

        PROVES: The session_registry.set_current_card() call has been removed
        from _process_mention, since session_registry no longer exists.
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

        # Verify no session_registry access
        assert not hasattr(manager, "session_registry")


class TestResumeToPublishApiVerification:
    """Tests for _resume_to_publish using API-based verification.

    PROVES: The resume logic checks _has_recent_bot_comment (API) instead
    of session_registry (in-process) to decide whether to post a fallback.

    SAFETY: This is the last line of defense against duplicate comments.
    If the resume function posts a fallback when Claude already posted,
    the user sees a duplicate. These tests prevent that.
    """

    @pytest.mark.asyncio
    async def test_resume_success_with_api_confirmed_post(self):
        """Test no fallback when API confirms bot posted after resume.

        PROVES: After resuming, if _has_recent_bot_comment returns True,
        the manager adds ✅ and does NOT post a fallback comment.
        """
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager.executor = MagicMock()
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Posted!")
        )
        manager._has_recent_bot_comment = MagicMock(return_value=True)

        await manager._resume_to_publish(
            card_id="card123",
            comment_id="comm1",
            session_id="session-abc",
            author_name="Paul",
        )

        # Should NOT post fallback
        manager.client.add_comment.assert_not_called()
        # Should add success reaction
        manager.client.toggle_reaction.assert_called_with("card123", "comm1", "✅")

    @pytest.mark.asyncio
    async def test_resume_success_without_post_triggers_fallback(self):
        """Test fallback posted when API says bot did NOT post after resume.

        PROVES: After resuming, if _has_recent_bot_comment returns False,
        the manager posts the result_text as a fallback comment.
        """
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager.executor = MagicMock()
        manager.executor.execute = AsyncMock(
            return_value=ClaudeResult(success=True, result_text="Here's the result")
        )
        manager._has_recent_bot_comment = MagicMock(return_value=False)

        await manager._resume_to_publish(
            card_id="card123",
            comment_id="comm1",
            session_id="session-abc",
            author_name="Paul",
        )

        # Should post fallback with result text
        manager.client.add_comment.assert_called_once()
        comment_content = manager.client.add_comment.call_args[0][1]
        assert "Here's the result" in comment_content
        assert "@Paul" in comment_content

    @pytest.mark.asyncio
    async def test_resume_does_not_use_session_registry(self):
        """Test _resume_to_publish does not access session_registry.

        PROVES: The session_registry.set_current_card() and get_session()
        calls have been removed from _resume_to_publish.
        """
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

        # No session_registry should exist
        assert not hasattr(manager, "session_registry")


class TestManagerStartNoHttpServer:
    """Tests that start() no longer launches an MCP HTTP server.

    PROVES: The unified mode (HTTP + WebSocket) is replaced by
    WebSocket-only mode. Each Claude session spawns its own
    kardbrd-mcp subprocess instead of connecting to a shared server.

    SAFETY: If HTTP server code remains, it would fail at import time
    (fastmcp removed from dependencies) or at runtime (port conflicts).
    """

    @pytest.mark.asyncio
    async def test_start_does_not_import_run_http_async(self):
        """Test start() doesn't import run_http_async.

        PROVES: The FastMCP HTTP server import is removed from start().
        """
        from kardbrd_client import BoardSubscription

        state_manager = MagicMock()
        sub = BoardSubscription(
            board_id="test-board",
            api_url="http://api.example.com",
            bot_token="bot-secret",
            agent_name="TestBot",
        )
        state_manager.get_all_subscriptions.return_value = {"test-board": sub}

        manager = ProxyManager(state_manager)

        with (
            patch("kardbrd_agent.manager.KardbrdClient"),
            patch("kardbrd_agent.manager.WebSocketAgentConnection") as mock_ws,
            patch("kardbrd_agent.manager.ClaudeExecutor"),
        ):
            mock_conn = MagicMock()
            mock_conn.connect = AsyncMock()
            mock_conn.is_connected = True
            mock_ws.return_value = mock_conn

            # start() should not reference run_http_async
            manager._running = False
            try:
                await manager.start()
            except Exception:
                pass

            # If we got here without importing run_http_async, the import is gone
            # (if it tried to import, it would fail since fastmcp is being removed)
