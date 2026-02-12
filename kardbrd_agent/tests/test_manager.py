"""Tests for ProxyManager."""

from datetime import UTC
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from kardbrd_agent.executor import ClaudeResult
from kardbrd_agent.manager import ActiveSession, ProxyManager


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

    def test_test_command_in_constructor(self):
        """Test test_command is set via constructor."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, test_command="make test-all")
        assert manager.test_command == "make test-all"

    def test_merge_queue_list_in_constructor(self):
        """Test merge_queue_list is set via constructor."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager, merge_queue_list="Ready to Ship")
        assert manager.merge_queue_list == "Ready to Ship"

    def test_defaults_are_none(self):
        """Test new config params default to None."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        assert manager.setup_command is None
        assert manager.test_command is None
        assert manager.merge_queue_list is None


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


class TestProxyManagerMergeQueue:
    """Tests for merge queue workflow triggering (via constructor config)."""

    @pytest.mark.asyncio
    async def test_handle_card_moved_to_merge_queue_triggers_workflow(self):
        """Test merge workflow is triggered when card moved to Merge Queue."""
        state_manager = MagicMock()
        manager = ProxyManager(
            state_manager, merge_queue_list="merge queue", test_command="make test"
        )
        manager.worktree_manager = MagicMock()
        manager._trigger_merge_workflow = AsyncMock()

        await manager._handle_card_moved(
            {
                "card_id": "abc12345",
                "list_name": "Merge Queue",
            }
        )

        manager._trigger_merge_workflow.assert_called_once_with("abc12345", "make test")
        manager.worktree_manager.remove_worktree.assert_not_called()

    @pytest.mark.asyncio
    async def test_handle_card_moved_to_merge_queue_case_insensitive(self):
        """Test merge queue detection is case insensitive."""
        state_manager = MagicMock()
        manager = ProxyManager(
            state_manager, merge_queue_list="merge queue", test_command="make test"
        )
        manager.worktree_manager = MagicMock()
        manager._trigger_merge_workflow = AsyncMock()

        for list_name in ["Merge Queue", "MERGE QUEUE", "merge queue", "Ready for Merge Queue"]:
            manager._trigger_merge_workflow.reset_mock()

            await manager._handle_card_moved(
                {
                    "card_id": "abc12345",
                    "list_name": list_name,
                }
            )

            manager._trigger_merge_workflow.assert_called_once()

    @pytest.mark.asyncio
    async def test_handle_card_moved_to_custom_merge_queue(self):
        """Test custom merge queue list name is respected."""
        state_manager = MagicMock()
        manager = ProxyManager(
            state_manager, merge_queue_list="Ready to Ship", test_command="make test"
        )
        manager.worktree_manager = MagicMock()
        manager._trigger_merge_workflow = AsyncMock()

        await manager._handle_card_moved(
            {
                "card_id": "abc12345",
                "list_name": "Ready to Ship",
            }
        )

        manager._trigger_merge_workflow.assert_called_once_with("abc12345", "make test")

    @pytest.mark.asyncio
    async def test_handle_card_moved_default_queue_not_triggered_by_custom(self):
        """Test default 'merge queue' is not triggered when custom is set."""
        state_manager = MagicMock()
        manager = ProxyManager(
            state_manager, merge_queue_list="Ready to Ship", test_command="make test"
        )
        manager.worktree_manager = MagicMock()
        manager._trigger_merge_workflow = AsyncMock()

        await manager._handle_card_moved(
            {
                "card_id": "abc12345",
                "list_name": "Merge Queue",
            }
        )

        manager._trigger_merge_workflow.assert_not_called()

    @pytest.mark.asyncio
    async def test_handle_card_moved_no_merge_config_does_nothing(self):
        """Test no action when no merge queue configured."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)  # No merge_queue_list
        manager.worktree_manager = MagicMock()
        manager._trigger_merge_workflow = AsyncMock()

        await manager._handle_card_moved(
            {
                "card_id": "abc12345",
                "list_name": "Merge Queue",
            }
        )

        manager._trigger_merge_workflow.assert_not_called()

    @pytest.mark.asyncio
    async def test_handle_card_moved_uses_default_test_command(self):
        """Test default test command (make test) when test_command not set."""
        state_manager = MagicMock()
        manager = ProxyManager(
            state_manager,
            merge_queue_list="merge queue",  # No test_command
        )
        manager.worktree_manager = MagicMock()
        manager._trigger_merge_workflow = AsyncMock()

        await manager._handle_card_moved(
            {
                "card_id": "abc12345",
                "list_name": "Merge Queue",
            }
        )

        manager._trigger_merge_workflow.assert_called_once_with("abc12345", "make test")

    @pytest.mark.asyncio
    async def test_trigger_merge_workflow_creates_workflow(self):
        """Test merge workflow is created with correct parameters."""
        from kardbrd_agent.merge_workflow import MergeStatus

        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager.client.get_card.return_value = {"title": "Test Card Title"}
        manager.executor = MagicMock()
        manager.cwd = Path("/tmp/repo")

        with patch("kardbrd_agent.merge_workflow.MergeWorkflow") as MockWorkflow:
            mock_workflow_instance = MagicMock()
            mock_workflow_instance.run = AsyncMock(return_value=MergeStatus.MERGED)
            MockWorkflow.return_value = mock_workflow_instance

            await manager._trigger_merge_workflow("abc12345", "make test-all")

            MockWorkflow.assert_called_once_with(
                card_id="abc12345",
                card_title="Test Card Title",
                main_repo_path=Path("/tmp/repo"),
                client=manager.client,
                executor=manager.executor,
                test_command="make test-all",
            )
            mock_workflow_instance.run.assert_called_once()

    @pytest.mark.asyncio
    async def test_trigger_merge_workflow_handles_card_fetch_error(self):
        """Test merge workflow handles card fetch failure gracefully."""
        from kardbrd_agent.merge_workflow import MergeStatus

        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager.client.get_card.side_effect = Exception("API error")
        manager.executor = MagicMock()
        manager.cwd = Path("/tmp/repo")

        with patch("kardbrd_agent.merge_workflow.MergeWorkflow") as MockWorkflow:
            mock_workflow_instance = MagicMock()
            mock_workflow_instance.run = AsyncMock(return_value=MergeStatus.MERGED)
            MockWorkflow.return_value = mock_workflow_instance

            # Should not raise - uses fallback title
            await manager._trigger_merge_workflow("abc12345", "make test")

            MockWorkflow.assert_called_once()
            call_kwargs = MockWorkflow.call_args[1]
            assert call_kwargs["card_title"] == "Card abc12345"

    @pytest.mark.asyncio
    async def test_trigger_merge_workflow_handles_workflow_exception(self):
        """Test merge workflow handles workflow execution failure."""
        state_manager = MagicMock()
        manager = ProxyManager(state_manager)
        manager.client = MagicMock()
        manager.client.get_card.return_value = {"title": "Test Card"}
        manager.executor = MagicMock()
        manager.cwd = Path("/tmp/repo")

        with patch("kardbrd_agent.merge_workflow.MergeWorkflow") as MockWorkflow:
            mock_workflow_instance = MagicMock()
            mock_workflow_instance.run = AsyncMock(side_effect=Exception("Workflow failed"))
            MockWorkflow.return_value = mock_workflow_instance

            # Should not raise - exception is caught and logged
            await manager._trigger_merge_workflow("abc12345", "make test")


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
