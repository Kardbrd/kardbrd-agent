"""Tests for on-demand WebSocket streaming functionality.

Covers:
- ClaudeExecutor._read_with_chunks() — line-by-line subprocess reading with callback
- GooseExecutor._read_with_chunks() — same pattern with Goose event types
- ProxyManager._handle_stream_requested() — stream WS connection lifecycle
- ProxyManager._make_on_chunk() — callback that sends stream_chunk messages
- ProxyManager._close_stream_ws() — graceful cleanup
- ProxyManager._handle_board_event() — stream_requested event routing
"""

import json
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from kardbrd_agent.executor import ClaudeExecutor
from kardbrd_agent.goose_executor import GooseExecutor
from kardbrd_agent.manager import ActiveSession, ProxyManager

# -- Helpers -----------------------------------------------------------------

_DEFAULTS = {
    "board_id": "board123",
    "api_url": "https://test.kardbrd.com",
    "bot_token": "test-token",
    "agent_name": "coder",
}


def _make_manager(**overrides):
    kwargs = {**_DEFAULTS, **overrides}
    return ProxyManager(**kwargs)


class _AsyncLineIterator:
    """Async iterator over a list of byte lines (mimics process.stdout)."""

    def __init__(self, lines: list[bytes]):
        self._lines = lines
        self._index = 0

    def __aiter__(self):
        return self

    async def __anext__(self):
        if self._index >= len(self._lines):
            raise StopAsyncIteration
        line = self._lines[self._index]
        self._index += 1
        return line


def _mock_process(stdout_lines: list[bytes], stderr: bytes = b"", returncode: int = 0):
    """Create a mock asyncio.subprocess.Process with async line iteration."""
    process = MagicMock()
    process.stdin = MagicMock()
    process.stdin.write = MagicMock()
    process.stdin.drain = AsyncMock()
    process.stdin.close = MagicMock()
    process.stderr = MagicMock()
    process.stderr.read = AsyncMock(return_value=stderr)
    process.wait = AsyncMock(return_value=returncode)
    process.returncode = returncode
    process.stdout = _AsyncLineIterator(stdout_lines)
    return process


# == ClaudeExecutor._read_with_chunks =========================================


class TestClaudeReadWithChunks:
    """Tests for ClaudeExecutor._read_with_chunks()."""

    @pytest.mark.asyncio
    async def test_assistant_event_forwarded(self):
        """Claude 'assistant' events are forwarded as (content, 'assistant')."""
        line = json.dumps({"type": "assistant", "content": "Hello world"}).encode() + b"\n"
        process = _mock_process([line])
        on_chunk = AsyncMock()

        stdout, stderr = await ClaudeExecutor._read_with_chunks(process, "prompt", on_chunk)

        on_chunk.assert_awaited_once_with("Hello world", "assistant")
        assert line in stdout

    @pytest.mark.asyncio
    async def test_tool_use_event_forwarded(self):
        """Claude 'tool_use' events are forwarded as (json_str, 'tool_use')."""
        event = {"type": "tool_use", "tool": "Read", "input": {"path": "/foo"}}
        line = json.dumps(event).encode() + b"\n"
        process = _mock_process([line])
        on_chunk = AsyncMock()

        await ClaudeExecutor._read_with_chunks(process, "prompt", on_chunk)

        on_chunk.assert_awaited_once()
        sent_content, sent_type = on_chunk.await_args[0]
        assert sent_type == "tool_use"
        assert json.loads(sent_content) == event

    @pytest.mark.asyncio
    async def test_result_event_not_forwarded(self):
        """Claude 'result' events are not forwarded to the callback."""
        line = json.dumps({"type": "result", "result": "done"}).encode() + b"\n"
        process = _mock_process([line])
        on_chunk = AsyncMock()

        await ClaudeExecutor._read_with_chunks(process, "prompt", on_chunk)

        on_chunk.assert_not_awaited()

    @pytest.mark.asyncio
    async def test_non_json_lines_skipped(self):
        """Non-JSON lines are collected but not forwarded."""
        lines = [b"not json\n", b"also not json\n"]
        process = _mock_process(lines)
        on_chunk = AsyncMock()

        stdout, _ = await ClaudeExecutor._read_with_chunks(process, "prompt", on_chunk)

        on_chunk.assert_not_awaited()
        assert stdout == b"not json\nalso not json\n"

    @pytest.mark.asyncio
    async def test_mixed_output_lines(self):
        """Mix of assistant, tool_use, result, and non-JSON lines."""
        lines = [
            json.dumps({"type": "assistant", "content": "First"}).encode() + b"\n",
            b"plain text\n",
            json.dumps({"type": "tool_use", "tool": "Bash"}).encode() + b"\n",
            json.dumps({"type": "result", "result": "ok"}).encode() + b"\n",
            json.dumps({"type": "assistant", "content": "Second"}).encode() + b"\n",
        ]
        process = _mock_process(lines)
        on_chunk = AsyncMock()

        stdout, stderr = await ClaudeExecutor._read_with_chunks(process, "my prompt", on_chunk)

        assert on_chunk.await_count == 3  # 2 assistant + 1 tool_use
        calls = on_chunk.await_args_list
        assert calls[0][0] == ("First", "assistant")
        assert calls[1][0][1] == "tool_use"
        assert calls[2][0] == ("Second", "assistant")
        # All lines collected in stdout
        assert stdout == b"".join(lines)

    @pytest.mark.asyncio
    async def test_empty_content_forwarded(self):
        """Assistant events with empty content still fire callback."""
        line = json.dumps({"type": "assistant"}).encode() + b"\n"
        process = _mock_process([line])
        on_chunk = AsyncMock()

        await ClaudeExecutor._read_with_chunks(process, "prompt", on_chunk)

        on_chunk.assert_awaited_once_with("", "assistant")

    @pytest.mark.asyncio
    async def test_stdin_written_and_closed(self):
        """Prompt is written to stdin, drained, then stdin is closed."""
        process = _mock_process([])
        on_chunk = AsyncMock()

        await ClaudeExecutor._read_with_chunks(process, "my prompt", on_chunk)

        process.stdin.write.assert_called_once_with(b"my prompt")
        process.stdin.drain.assert_awaited_once()
        process.stdin.close.assert_called_once()

    @pytest.mark.asyncio
    async def test_stderr_collected(self):
        """Stderr is read and returned."""
        process = _mock_process([], stderr=b"some warning\n")
        on_chunk = AsyncMock()

        stdout, stderr = await ClaudeExecutor._read_with_chunks(process, "prompt", on_chunk)

        assert stderr == b"some warning\n"

    @pytest.mark.asyncio
    async def test_callback_exception_does_not_break_reading(self):
        """If on_chunk raises, remaining lines are still read."""
        lines = [
            json.dumps({"type": "assistant", "content": "First"}).encode() + b"\n",
            json.dumps({"type": "assistant", "content": "Second"}).encode() + b"\n",
        ]
        process = _mock_process(lines)
        on_chunk = AsyncMock(side_effect=[Exception("boom"), None])

        stdout, _ = await ClaudeExecutor._read_with_chunks(process, "prompt", on_chunk)

        # Both lines were still collected despite callback error
        assert stdout == b"".join(lines)
        assert on_chunk.await_count == 2


# == GooseExecutor._read_with_chunks ==========================================


class TestGooseReadWithChunks:
    """Tests for GooseExecutor._read_with_chunks()."""

    @pytest.mark.asyncio
    async def test_agent_message_chunk_forwarded(self):
        """Goose 'AgentMessageChunk' events map to (content, 'assistant')."""
        line = json.dumps({"type": "AgentMessageChunk", "content": "Hello"}).encode() + b"\n"
        process = _mock_process([line])
        on_chunk = AsyncMock()

        await GooseExecutor._read_with_chunks(process, "prompt", on_chunk)

        on_chunk.assert_awaited_once_with("Hello", "assistant")

    @pytest.mark.asyncio
    async def test_tool_call_update_forwarded(self):
        """Goose 'ToolCallUpdate' events map to (json_str, 'tool_use')."""
        event = {"type": "ToolCallUpdate", "tool": "shell", "status": "running"}
        line = json.dumps(event).encode() + b"\n"
        process = _mock_process([line])
        on_chunk = AsyncMock()

        await GooseExecutor._read_with_chunks(process, "prompt", on_chunk)

        on_chunk.assert_awaited_once()
        sent_content, sent_type = on_chunk.await_args[0]
        assert sent_type == "tool_use"
        assert json.loads(sent_content) == event

    @pytest.mark.asyncio
    async def test_other_goose_events_not_forwarded(self):
        """Other Goose event types (e.g. ToolCall) are not forwarded."""
        line = json.dumps({"type": "ToolCall", "tool": "shell"}).encode() + b"\n"
        process = _mock_process([line])
        on_chunk = AsyncMock()

        await GooseExecutor._read_with_chunks(process, "prompt", on_chunk)

        on_chunk.assert_not_awaited()

    @pytest.mark.asyncio
    async def test_mixed_goose_output(self):
        """Mix of Goose event types and non-JSON lines."""
        lines = [
            json.dumps({"type": "AgentMessageChunk", "content": "Hi"}).encode() + b"\n",
            b"debug output\n",
            json.dumps({"type": "ToolCallUpdate", "tool": "bash"}).encode() + b"\n",
            json.dumps({"type": "AgentMessageChunk", "content": "Done"}).encode() + b"\n",
        ]
        process = _mock_process(lines)
        on_chunk = AsyncMock()

        stdout, _ = await GooseExecutor._read_with_chunks(process, "prompt", on_chunk)

        assert on_chunk.await_count == 3
        assert stdout == b"".join(lines)

    @pytest.mark.asyncio
    async def test_stdin_written_and_closed(self):
        """Prompt is written to stdin, drained, then stdin is closed."""
        process = _mock_process([])
        on_chunk = AsyncMock()

        await GooseExecutor._read_with_chunks(process, "the prompt", on_chunk)

        process.stdin.write.assert_called_once_with(b"the prompt")
        process.stdin.drain.assert_awaited_once()
        process.stdin.close.assert_called_once()


# == ProxyManager._handle_stream_requested ====================================


class TestHandleStreamRequested:
    """Tests for ProxyManager._handle_stream_requested()."""

    @pytest.mark.asyncio
    async def test_connects_when_session_exists(self):
        """Opens WebSocket when there is an active session for the card."""
        manager = _make_manager()
        session = ActiveSession(card_id="abc123", worktree_path=Path("/tmp/wt"))
        manager._active_sessions["abc123"] = session

        mock_ws = AsyncMock()
        with patch("websockets.connect", new_callable=AsyncMock) as mock_connect:
            mock_connect.return_value = mock_ws
            await manager._handle_stream_requested("abc123", "wss://host/ws/stream/token/")

        assert session.stream_ws is mock_ws
        assert session.streaming is True
        mock_connect.assert_awaited_once_with("wss://host/ws/stream/token/")

    @pytest.mark.asyncio
    async def test_ignores_when_no_session(self):
        """Does nothing when there is no active session for the card."""
        manager = _make_manager()
        # No session in _active_sessions

        with patch("websockets.connect", new_callable=AsyncMock) as mock_connect:
            await manager._handle_stream_requested("abc123", "wss://host/ws/stream/token/")

        mock_connect.assert_not_awaited()

    @pytest.mark.asyncio
    async def test_skips_when_already_streaming(self):
        """Does not open a second WebSocket if one is already open."""
        manager = _make_manager()
        existing_ws = AsyncMock()
        session = ActiveSession(
            card_id="abc123",
            worktree_path=Path("/tmp/wt"),
            stream_ws=existing_ws,
        )
        manager._active_sessions["abc123"] = session

        with patch("websockets.connect", new_callable=AsyncMock) as mock_connect:
            await manager._handle_stream_requested("abc123", "wss://host/ws/stream/token/")

        mock_connect.assert_not_awaited()
        assert session.stream_ws is existing_ws  # unchanged

    @pytest.mark.asyncio
    async def test_handles_connection_failure(self):
        """Handles WebSocket connection failure gracefully."""
        manager = _make_manager()
        session = ActiveSession(card_id="abc123", worktree_path=Path("/tmp/wt"))
        manager._active_sessions["abc123"] = session

        with patch("websockets.connect", new_callable=AsyncMock) as mock_connect:
            mock_connect.side_effect = OSError("Connection refused")
            await manager._handle_stream_requested("abc123", "wss://host/ws/stream/token/")

        assert session.stream_ws is None
        assert session.streaming is False


# == ProxyManager._make_on_chunk =============================================


class TestMakeOnChunk:
    """Tests for ProxyManager._make_on_chunk()."""

    @pytest.mark.asyncio
    async def test_sends_stream_chunk_format(self):
        """Callback sends correct stream_chunk JSON to the stream WebSocket."""
        manager = _make_manager()
        mock_ws = AsyncMock()
        session = ActiveSession(
            card_id="k941vD0Q",
            worktree_path=Path("/tmp/wt"),
            stream_ws=mock_ws,
        )
        manager._active_sessions["k941vD0Q"] = session

        on_chunk = manager._make_on_chunk("k941vD0Q")
        await on_chunk("I'll help you implement...", "assistant")

        mock_ws.send.assert_awaited_once()
        sent = json.loads(mock_ws.send.await_args[0][0])
        assert sent == {
            "type": "stream_chunk",
            "card_id": "k941vD0Q",
            "text": "I'll help you implement...",
            "chunk_type": "assistant",
            "sequence": 0,
        }

    @pytest.mark.asyncio
    async def test_sequence_increments(self):
        """Sequence number increments with each chunk."""
        manager = _make_manager()
        mock_ws = AsyncMock()
        session = ActiveSession(card_id="abc", worktree_path=Path("/tmp/wt"), stream_ws=mock_ws)
        manager._active_sessions["abc"] = session

        on_chunk = manager._make_on_chunk("abc")
        await on_chunk("First", "assistant")
        await on_chunk("Second", "tool_use")
        await on_chunk("Third", "assistant")

        assert mock_ws.send.await_count == 3
        sequences = [json.loads(call[0][0])["sequence"] for call in mock_ws.send.await_args_list]
        assert sequences == [0, 1, 2]

    @pytest.mark.asyncio
    async def test_tool_use_chunk_type(self):
        """Tool use events are sent with chunk_type 'tool_use'."""
        manager = _make_manager()
        mock_ws = AsyncMock()
        session = ActiveSession(card_id="abc", worktree_path=Path("/tmp/wt"), stream_ws=mock_ws)
        manager._active_sessions["abc"] = session

        on_chunk = manager._make_on_chunk("abc")
        tool_json = json.dumps({"type": "tool_use", "tool": "Read"})
        await on_chunk(tool_json, "tool_use")

        sent = json.loads(mock_ws.send.await_args[0][0])
        assert sent["chunk_type"] == "tool_use"
        assert sent["text"] == tool_json

    @pytest.mark.asyncio
    async def test_noop_when_no_stream_ws(self):
        """Callback does nothing when stream_ws is None."""
        manager = _make_manager()
        session = ActiveSession(card_id="abc", worktree_path=Path("/tmp/wt"), stream_ws=None)
        manager._active_sessions["abc"] = session

        on_chunk = manager._make_on_chunk("abc")
        await on_chunk("Hello", "assistant")  # Should not raise

    @pytest.mark.asyncio
    async def test_noop_when_no_session(self):
        """Callback does nothing when the session no longer exists."""
        manager = _make_manager()
        # No session in _active_sessions

        on_chunk = manager._make_on_chunk("abc")
        await on_chunk("Hello", "assistant")  # Should not raise

    @pytest.mark.asyncio
    async def test_send_error_nulls_stream_ws(self):
        """If send() raises, stream_ws is set to None (graceful degradation)."""
        manager = _make_manager()
        mock_ws = AsyncMock()
        mock_ws.send.side_effect = Exception("Connection lost")
        session = ActiveSession(card_id="abc", worktree_path=Path("/tmp/wt"), stream_ws=mock_ws)
        manager._active_sessions["abc"] = session

        on_chunk = manager._make_on_chunk("abc")
        await on_chunk("Hello", "assistant")

        assert session.stream_ws is None

    @pytest.mark.asyncio
    async def test_subsequent_chunks_after_error_are_noop(self):
        """After a send error nulls stream_ws, subsequent chunks are no-ops."""
        manager = _make_manager()
        mock_ws = AsyncMock()
        mock_ws.send.side_effect = Exception("Connection lost")
        session = ActiveSession(card_id="abc", worktree_path=Path("/tmp/wt"), stream_ws=mock_ws)
        manager._active_sessions["abc"] = session

        on_chunk = manager._make_on_chunk("abc")
        await on_chunk("Hello", "assistant")  # Triggers error → nulls ws
        await on_chunk("World", "assistant")  # Should be noop

        assert mock_ws.send.await_count == 1  # Only first call


# == ProxyManager._close_stream_ws ===========================================


class TestCloseStreamWs:
    """Tests for ProxyManager._close_stream_ws()."""

    @pytest.mark.asyncio
    async def test_closes_open_ws(self):
        """Closes the stream WebSocket and resets fields."""
        manager = _make_manager()
        mock_ws = AsyncMock()
        session = ActiveSession(
            card_id="abc",
            worktree_path=Path("/tmp/wt"),
            stream_ws=mock_ws,
            streaming=True,
        )
        manager._active_sessions["abc"] = session

        await manager._close_stream_ws("abc")

        mock_ws.close.assert_awaited_once()
        assert session.stream_ws is None
        assert session.streaming is False

    @pytest.mark.asyncio
    async def test_noop_when_no_stream_ws(self):
        """Does nothing when stream_ws is already None."""
        manager = _make_manager()
        session = ActiveSession(card_id="abc", worktree_path=Path("/tmp/wt"), stream_ws=None)
        manager._active_sessions["abc"] = session

        await manager._close_stream_ws("abc")  # Should not raise

    @pytest.mark.asyncio
    async def test_noop_when_no_session(self):
        """Does nothing when the session doesn't exist."""
        manager = _make_manager()
        await manager._close_stream_ws("nonexistent")  # Should not raise

    @pytest.mark.asyncio
    async def test_suppresses_close_exception(self):
        """Exceptions from ws.close() are suppressed."""
        manager = _make_manager()
        mock_ws = AsyncMock()
        mock_ws.close.side_effect = Exception("Close failed")
        session = ActiveSession(
            card_id="abc",
            worktree_path=Path("/tmp/wt"),
            stream_ws=mock_ws,
            streaming=True,
        )
        manager._active_sessions["abc"] = session

        await manager._close_stream_ws("abc")  # Should not raise

        assert session.stream_ws is None
        assert session.streaming is False


# == ProxyManager._handle_board_event routing ==================================


class TestBoardEventStreamRouting:
    """Tests for stream_requested event routing in _handle_board_event()."""

    @pytest.mark.asyncio
    async def test_stream_requested_event_dispatched(self):
        """stream_requested event calls _handle_stream_requested with correct args."""
        manager = _make_manager()
        manager._handle_stream_requested = AsyncMock()
        manager._check_rules = AsyncMock()

        message = {
            "type": "board_event",
            "event_type": "stream_requested",
            "card_id": "k941vD0Q",
            "stream_url": "wss://app.kardbrd.com/ws/stream/k941vD0Q:1hQ2Hp:abcdef123/",
        }

        await manager._handle_board_event(message)

        manager._handle_stream_requested.assert_awaited_once_with(
            "k941vD0Q",
            "wss://app.kardbrd.com/ws/stream/k941vD0Q:1hQ2Hp:abcdef123/",
        )

    @pytest.mark.asyncio
    async def test_stream_requested_still_checks_rules(self):
        """stream_requested events still run through _check_rules afterward."""
        manager = _make_manager()
        manager._handle_stream_requested = AsyncMock()
        manager._check_rules = AsyncMock()

        message = {
            "type": "board_event",
            "event_type": "stream_requested",
            "card_id": "k941vD0Q",
            "stream_url": "wss://host/ws/stream/token/",
        }

        await manager._handle_board_event(message)

        manager._check_rules.assert_awaited_once_with("stream_requested", message)

    @pytest.mark.asyncio
    async def test_stream_requested_with_missing_url_defaults_empty(self):
        """stream_requested event with missing stream_url defaults to empty string."""
        manager = _make_manager()
        manager._handle_stream_requested = AsyncMock()
        manager._check_rules = AsyncMock()

        message = {
            "type": "board_event",
            "event_type": "stream_requested",
            "card_id": "k941vD0Q",
            # No stream_url field
        }

        await manager._handle_board_event(message)

        manager._handle_stream_requested.assert_awaited_once_with("k941vD0Q", "")


# == End-to-end: event payload → stream_chunk message =========================


class TestStreamingEndToEnd:
    """End-to-end tests matching the documented event payloads."""

    @pytest.mark.asyncio
    async def test_documented_event_payload_to_stream_chunk(self):
        """Verify the full flow from documented server event to stream_chunk output.

        Server sends:
            {"type": "board_event", "event_type": "stream_requested",
             "card_id": "k941vD0Q",
             "stream_url": "wss://app.kardbrd.com/ws/stream/k941vD0Q:1hQ2Hp:abcdef123/"}

        Agent connects to stream_url, then executor output produces:
            {"type": "stream_chunk", "card_id": "k941vD0Q",
             "text": "I'll help you implement...", "chunk_type": "assistant",
             "sequence": 0}
        """
        manager = _make_manager()
        mock_ws = AsyncMock()

        # Set up an active session (simulating an agent already running)
        session = ActiveSession(card_id="k941vD0Q", worktree_path=Path("/tmp/wt"))
        manager._active_sessions["k941vD0Q"] = session

        # Simulate receiving stream_requested event
        with patch("websockets.connect", new_callable=AsyncMock) as mock_connect:
            mock_connect.return_value = mock_ws
            await manager._handle_stream_requested(
                "k941vD0Q",
                "wss://app.kardbrd.com/ws/stream/k941vD0Q:1hQ2Hp:abcdef123/",
            )

        assert session.stream_ws is mock_ws

        # Now simulate the on_chunk callback sending a chunk
        on_chunk = manager._make_on_chunk("k941vD0Q")
        await on_chunk("I'll help you implement...", "assistant")

        # Verify the exact documented stream_chunk message format
        sent = json.loads(mock_ws.send.await_args[0][0])
        assert sent == {
            "type": "stream_chunk",
            "card_id": "k941vD0Q",
            "text": "I'll help you implement...",
            "chunk_type": "assistant",
            "sequence": 0,
        }

    @pytest.mark.asyncio
    async def test_claude_executor_events_to_stream_chunks(self):
        """Claude assistant+tool_use events produce correctly typed stream_chunks."""
        manager = _make_manager()
        mock_ws = AsyncMock()
        session = ActiveSession(card_id="card1", worktree_path=Path("/tmp/wt"), stream_ws=mock_ws)
        manager._active_sessions["card1"] = session

        on_chunk = manager._make_on_chunk("card1")

        # Simulate what ClaudeExecutor._read_with_chunks would call
        # 1. assistant event
        await on_chunk("Let me read the file.", "assistant")
        # 2. tool_use event
        tool_event = json.dumps({"type": "tool_use", "tool": "Read", "input": {"path": "/foo"}})
        await on_chunk(tool_event, "tool_use")
        # 3. another assistant event
        await on_chunk("Here's what I found:", "assistant")

        assert mock_ws.send.await_count == 3
        chunks = [json.loads(c[0][0]) for c in mock_ws.send.await_args_list]

        assert chunks[0]["chunk_type"] == "assistant"
        assert chunks[0]["sequence"] == 0
        assert chunks[1]["chunk_type"] == "tool_use"
        assert chunks[1]["sequence"] == 1
        assert chunks[2]["chunk_type"] == "assistant"
        assert chunks[2]["sequence"] == 2

        # All have correct card_id and type
        for chunk in chunks:
            assert chunk["type"] == "stream_chunk"
            assert chunk["card_id"] == "card1"

    @pytest.mark.asyncio
    async def test_goose_executor_events_to_stream_chunks(self):
        """Goose AgentMessageChunk+ToolCallUpdate events produce correctly typed chunks."""
        manager = _make_manager()
        mock_ws = AsyncMock()
        session = ActiveSession(card_id="card2", worktree_path=Path("/tmp/wt"), stream_ws=mock_ws)
        manager._active_sessions["card2"] = session

        on_chunk = manager._make_on_chunk("card2")

        # Simulate what GooseExecutor._read_with_chunks would produce
        # GooseExecutor maps: AgentMessageChunk → "assistant", ToolCallUpdate → "tool_use"
        await on_chunk("Starting the task", "assistant")
        tool_event = json.dumps({"type": "ToolCallUpdate", "tool": "shell", "status": "running"})
        await on_chunk(tool_event, "tool_use")

        chunks = [json.loads(c[0][0]) for c in mock_ws.send.await_args_list]
        assert chunks[0]["chunk_type"] == "assistant"
        assert chunks[1]["chunk_type"] == "tool_use"

    @pytest.mark.asyncio
    async def test_full_lifecycle_connect_stream_cleanup(self):
        """Full lifecycle: connect → stream chunks → cleanup."""
        manager = _make_manager()
        mock_ws = AsyncMock()
        session = ActiveSession(card_id="abc", worktree_path=Path("/tmp/wt"))
        manager._active_sessions["abc"] = session

        # 1. Connect
        with patch("websockets.connect", new_callable=AsyncMock) as mock_connect:
            mock_connect.return_value = mock_ws
            await manager._handle_stream_requested("abc", "wss://host/ws/stream/tok/")

        assert session.stream_ws is mock_ws
        assert session.streaming is True

        # 2. Stream chunks
        on_chunk = manager._make_on_chunk("abc")
        await on_chunk("Hello", "assistant")
        await on_chunk("World", "assistant")
        assert mock_ws.send.await_count == 2

        # 3. Cleanup
        await manager._close_stream_ws("abc")
        mock_ws.close.assert_awaited_once()
        assert session.stream_ws is None
        assert session.streaming is False

    @pytest.mark.asyncio
    async def test_stream_ws_disconnect_midstream_graceful(self):
        """If stream WS disconnects mid-stream, execution continues."""
        manager = _make_manager()
        mock_ws = AsyncMock()
        # First send succeeds, second raises
        mock_ws.send.side_effect = [None, Exception("Connection closed")]
        session = ActiveSession(card_id="abc", worktree_path=Path("/tmp/wt"), stream_ws=mock_ws)
        manager._active_sessions["abc"] = session

        on_chunk = manager._make_on_chunk("abc")
        await on_chunk("First chunk", "assistant")  # succeeds
        await on_chunk("Second chunk", "assistant")  # fails → nulls ws
        await on_chunk("Third chunk", "assistant")  # noop

        assert session.stream_ws is None
        assert mock_ws.send.await_count == 2  # third was noop
