"""Tests for the MergeWorkflow module."""

from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from kardbrd_agent.merge_workflow import (
    MergeStatus,
    MergeStep,
    MergeWorkflow,
    StepResult,
    WorkflowState,
)


class TestMergeStatus:
    """Tests for MergeStatus enum."""

    def test_all_statuses_defined(self):
        """Test that all expected statuses are defined."""
        assert MergeStatus.MERGED.value == "merged"
        assert MergeStatus.EMPTY.value == "empty"
        assert MergeStatus.STALE.value == "stale"
        assert MergeStatus.CONFLICT.value == "conflict"
        assert MergeStatus.UNCOMMITTED.value == "uncommitted"
        assert MergeStatus.NO_WORKTREE.value == "no_worktree"
        assert MergeStatus.TESTS_FAILED.value == "tests_failed"
        assert MergeStatus.SESSION_ACTIVE.value == "session_active"


class TestMergeStep:
    """Tests for MergeStep enum."""

    def test_all_steps_defined(self):
        """Test that all expected steps are defined."""
        assert MergeStep.CHECK_SESSION.value == "check_session"
        assert MergeStep.CHECK_WORKTREE.value == "check_worktree"
        assert MergeStep.REBASE.value == "rebase"
        assert MergeStep.RUN_TESTS.value == "run_tests"
        assert MergeStep.SQUASH_MERGE.value == "squash_merge"


class TestStepResult:
    """Tests for StepResult dataclass."""

    def test_step_result_defaults(self):
        """Test StepResult default values."""
        result = StepResult(step=MergeStep.CHECK_SESSION, success=True)
        assert result.data == {}
        assert result.error is None
        assert result.requires_llm is False
        assert result.report_to_card is False


class TestWorkflowState:
    """Tests for WorkflowState dataclass."""

    def test_workflow_state_initialization(self):
        """Test WorkflowState initialization."""
        state = WorkflowState(card_id="test123")
        assert state.card_id == "test123"
        assert state.current_step == MergeStep.CHECK_SESSION
        assert state.commits == []
        assert state.checkpoints == []

    def test_log_checkpoint(self):
        """Test checkpoint logging."""
        state = WorkflowState(card_id="test123")
        result = StepResult(step=MergeStep.CHECK_SESSION, success=True)

        state.log_checkpoint(MergeStep.CHECK_SESSION, result)

        assert len(state.checkpoints) == 1
        assert state.checkpoints[0]["step"] == "check_session"
        assert state.checkpoints[0]["success"] is True


class TestMergeWorkflowInit:
    """Tests for MergeWorkflow initialization."""

    def test_init_sets_properties(self, tmp_path: Path):
        """Test that initialization sets all properties correctly."""
        mock_client = MagicMock()
        mock_executor = MagicMock()

        workflow = MergeWorkflow(
            card_id="card1234",
            card_title="Test Card",
            main_repo_path=tmp_path,
            client=mock_client,
            executor=mock_executor,
            test_command="make test-custom",
        )

        assert workflow.card_id == "card1234"
        assert workflow.card_title == "Test Card"
        assert workflow.main_repo_path == tmp_path
        assert workflow.test_command == "make test-custom"


class TestMergeWorkflowSteps:
    """Tests for MergeWorkflow individual steps."""

    @pytest.fixture
    def workflow(self, tmp_path: Path):
        """Create a MergeWorkflow for testing."""
        mock_client = MagicMock()
        mock_executor = MagicMock()

        return MergeWorkflow(
            card_id="card1234",
            card_title="Test Card",
            main_repo_path=tmp_path,
            client=mock_client,
            executor=mock_executor,
        )

    @pytest.mark.asyncio
    async def test_step_check_session_active(self, workflow):
        """Test session check when session is active."""
        with patch.object(workflow.tools, "check_session") as mock_check:
            mock_check.return_value = MagicMock(active=True, pid=1234)

            result = await workflow._step_check_session()

            assert result.success is False
            assert result.data["active"] is True

    @pytest.mark.asyncio
    async def test_step_check_session_inactive(self, workflow):
        """Test session check when session is not active."""
        with patch.object(workflow.tools, "check_session") as mock_check:
            mock_check.return_value = MagicMock(active=False, pid=None)

            result = await workflow._step_check_session()

            assert result.success is True
            assert result.data["active"] is False

    @pytest.mark.asyncio
    async def test_step_check_worktree_exists(self, workflow, tmp_path: Path):
        """Test worktree check when worktree exists."""
        worktree_path = tmp_path / "kbn-card1234"

        with patch.object(workflow.tools, "check_worktree") as mock_check:
            mock_check.return_value = MagicMock(
                exists=True,
                path=worktree_path,
                branch="card/card1234",
            )

            result = await workflow._step_check_worktree()

            assert result.success is True
            assert workflow.state.worktree_path == worktree_path

    @pytest.mark.asyncio
    async def test_step_check_worktree_not_exists(self, workflow):
        """Test worktree check when worktree doesn't exist."""
        with patch.object(workflow.tools, "check_worktree") as mock_check:
            mock_check.return_value = MagicMock(exists=False, path=None, branch=None)

            result = await workflow._step_check_worktree()

            assert result.success is False

    @pytest.mark.asyncio
    async def test_step_check_uncommitted_no_changes(self, workflow, tmp_path: Path):
        """Test uncommitted check when working directory is clean."""
        workflow.state.worktree_path = tmp_path

        with patch.object(workflow.tools, "git_status") as mock_status:
            mock_status.return_value = MagicMock(has_changes=False, files=[])

            result = await workflow._step_check_uncommitted()

            assert result.success is True
            assert result.data["has_changes"] is False

    @pytest.mark.asyncio
    async def test_step_check_uncommitted_with_changes(self, workflow, tmp_path: Path):
        """Test uncommitted check when working directory has changes."""
        workflow.state.worktree_path = tmp_path

        with patch.object(workflow.tools, "git_status") as mock_status:
            mock_status.return_value = MagicMock(
                has_changes=True,
                files=["file1.py", "file2.py"],
            )

            result = await workflow._step_check_uncommitted()

            assert result.success is True
            assert result.data["has_changes"] is True

    @pytest.mark.asyncio
    async def test_step_count_commits(self, workflow, tmp_path: Path):
        """Test commit counting."""
        workflow.state.worktree_path = tmp_path

        with patch.object(workflow.tools, "git_rev_list_count") as mock_count:
            mock_count.return_value = MagicMock(
                count=3,
                commits=[
                    {"hash": "abc1234", "message": "Fix bug"},
                    {"hash": "def5678", "message": "Add feature"},
                    {"hash": "ghi9012", "message": "Update docs"},
                ],
            )

            result = await workflow._step_count_commits()

            assert result.success is True
            assert result.data["count"] == 3
            assert workflow.state.commit_count == 3


class TestMergeWorkflowRun:
    """Tests for MergeWorkflow.run method."""

    @pytest.fixture
    def workflow(self, tmp_path: Path):
        """Create a MergeWorkflow for testing."""
        mock_client = MagicMock()
        mock_executor = AsyncMock()

        return MergeWorkflow(
            card_id="card1234",
            card_title="Test Card",
            main_repo_path=tmp_path,
            client=mock_client,
            executor=mock_executor,
        )

    @pytest.mark.asyncio
    async def test_run_session_active_returns_early(self, workflow):
        """Test that run returns SESSION_ACTIVE when session is active."""
        with patch.object(workflow, "_step_check_session") as mock_step:
            mock_step.return_value = StepResult(
                step=MergeStep.CHECK_SESSION,
                success=False,
            )

            result = await workflow.run()

            assert result == MergeStatus.SESSION_ACTIVE

    @pytest.mark.asyncio
    async def test_run_no_worktree_returns_early(self, workflow):
        """Test that run returns NO_WORKTREE when worktree doesn't exist."""
        with (
            patch.object(workflow, "_step_check_session") as mock_session,
            patch.object(workflow, "_step_check_worktree") as mock_worktree,
        ):
            mock_session.return_value = StepResult(
                step=MergeStep.CHECK_SESSION,
                success=True,
            )
            mock_worktree.return_value = StepResult(
                step=MergeStep.CHECK_WORKTREE,
                success=False,
            )

            result = await workflow.run()

            assert result == MergeStatus.NO_WORKTREE

    @pytest.mark.asyncio
    async def test_run_uncommitted_changes_commits_first(self, workflow):
        """Test that uncommitted changes are committed before merge."""
        with (
            patch.object(workflow, "_step_check_session") as mock_session,
            patch.object(workflow, "_step_check_worktree") as mock_worktree,
            patch.object(workflow, "_step_check_uncommitted") as mock_uncommitted,
            patch.object(workflow, "_step_commit_uncommitted") as mock_commit,
        ):
            mock_session.return_value = StepResult(step=MergeStep.CHECK_SESSION, success=True)
            mock_worktree.return_value = StepResult(step=MergeStep.CHECK_WORKTREE, success=True)
            mock_uncommitted.return_value = StepResult(
                step=MergeStep.CHECK_UNCOMMITTED,
                success=True,
                data={"has_changes": True},
            )
            mock_commit.return_value = StepResult(step=MergeStep.COMMIT_UNCOMMITTED, success=False)

            result = await workflow.run()

            assert result == MergeStatus.UNCOMMITTED
            mock_commit.assert_called_once()

    @pytest.mark.asyncio
    async def test_run_stale_branch_after_rebase(self, workflow, tmp_path: Path):
        """Test that STALE is returned when no commits after rebase."""
        workflow.state.worktree_path = tmp_path

        with (
            patch.object(workflow, "_run_pre_merge_checks") as mock_pre,
            patch.object(workflow, "_step_fetch") as mock_fetch,
            patch.object(workflow, "_step_update_target") as mock_update,
            patch.object(workflow, "_step_rebase_with_conflicts") as mock_rebase,
            patch.object(workflow, "_step_count_commits") as mock_count,
            patch.object(workflow, "_post_status_comment") as mock_comment,
            patch.object(workflow, "_cleanup") as mock_cleanup,
        ):
            mock_pre.return_value = None  # Pre-checks pass
            mock_fetch.return_value = StepResult(step=MergeStep.FETCH, success=True)
            mock_update.return_value = StepResult(step=MergeStep.UPDATE_TARGET, success=True)
            mock_rebase.return_value = StepResult(step=MergeStep.REBASE, success=True)
            mock_count.return_value = StepResult(
                step=MergeStep.COUNT_COMMITS,
                success=True,
                data={"count": 0},
            )

            result = await workflow.run()

            assert result == MergeStatus.STALE
            mock_comment.assert_called_with(MergeStatus.STALE)
            mock_cleanup.assert_called_once()

    @pytest.mark.asyncio
    async def test_run_tests_failed_returns_status(self, workflow, tmp_path: Path):
        """Test that TESTS_FAILED is returned when tests fail."""
        workflow.state.worktree_path = tmp_path
        workflow.state.commit_count = 3

        with (
            patch.object(workflow, "_run_pre_merge_checks") as mock_pre,
            patch.object(workflow, "_step_fetch") as mock_fetch,
            patch.object(workflow, "_step_update_target") as mock_update,
            patch.object(workflow, "_step_rebase_with_conflicts") as mock_rebase,
            patch.object(workflow, "_step_count_commits") as mock_count,
            patch.object(workflow, "_step_test_with_fixes") as mock_test,
        ):
            mock_pre.return_value = None
            mock_fetch.return_value = StepResult(step=MergeStep.FETCH, success=True)
            mock_update.return_value = StepResult(step=MergeStep.UPDATE_TARGET, success=True)
            mock_rebase.return_value = StepResult(step=MergeStep.REBASE, success=True)
            mock_count.return_value = StepResult(
                step=MergeStep.COUNT_COMMITS,
                success=True,
                data={"count": 3},
            )
            mock_test.return_value = StepResult(step=MergeStep.RUN_TESTS, success=False)

            result = await workflow.run()

            assert result == MergeStatus.TESTS_FAILED

    @pytest.mark.asyncio
    async def test_run_conflict_returns_status(self, workflow, tmp_path: Path):
        """Test that CONFLICT is returned when rebase conflicts cannot be resolved."""
        workflow.state.worktree_path = tmp_path

        with (
            patch.object(workflow, "_run_pre_merge_checks") as mock_pre,
            patch.object(workflow, "_step_fetch") as mock_fetch,
            patch.object(workflow, "_step_update_target") as mock_update,
            patch.object(workflow, "_step_rebase_with_conflicts") as mock_rebase,
        ):
            mock_pre.return_value = None
            mock_fetch.return_value = StepResult(step=MergeStep.FETCH, success=True)
            mock_update.return_value = StepResult(step=MergeStep.UPDATE_TARGET, success=True)
            mock_rebase.return_value = StepResult(
                step=MergeStep.REBASE,
                success=False,
                data={"conflict": True},
            )

            result = await workflow.run()

            assert result == MergeStatus.CONFLICT


class TestMergeWorkflowLLMSteps:
    """Tests for LLM-engaged workflow steps."""

    @pytest.fixture
    def workflow(self, tmp_path: Path):
        """Create a MergeWorkflow for testing."""
        mock_client = MagicMock()
        mock_executor = MagicMock()
        mock_executor.execute = AsyncMock()

        wf = MergeWorkflow(
            card_id="card1234",
            card_title="Test Card",
            main_repo_path=tmp_path,
            client=mock_client,
            executor=mock_executor,
        )
        wf.state.worktree_path = tmp_path
        return wf

    @pytest.mark.asyncio
    async def test_step_commit_uncommitted_success(self, workflow):
        """Test LLM commit step when successful."""
        workflow.executor.execute.return_value = MagicMock(success=True)

        with patch.object(workflow.tools, "git_status") as mock_status:
            mock_status.return_value = MagicMock(has_changes=False)

            result = await workflow._step_commit_uncommitted()

            assert result.success is True
            assert result.requires_llm is True

    @pytest.mark.asyncio
    async def test_step_commit_uncommitted_still_has_changes(self, workflow):
        """Test LLM commit step fails if changes remain."""
        workflow.executor.execute.return_value = MagicMock(success=True)

        with patch.object(workflow.tools, "git_status") as mock_status:
            mock_status.return_value = MagicMock(has_changes=True)

            result = await workflow._step_commit_uncommitted()

            assert result.success is False
            assert result.data["still_has_changes"] is True

    @pytest.mark.asyncio
    async def test_step_fix_tests_success(self, workflow):
        """Test LLM test fix step."""
        workflow.executor.execute.return_value = MagicMock(success=True)

        result = await workflow._step_fix_tests(["test_foo.py"], "FAILED: test_foo")

        assert result.success is True
        assert result.requires_llm is True
        assert result.report_to_card is True

    @pytest.mark.asyncio
    async def test_step_create_commit_success(self, workflow, tmp_path: Path):
        """Test LLM squash commit creation."""
        workflow.state.commits = [
            {"hash": "abc1234", "message": "Fix bug"},
            {"hash": "def5678", "message": "Add feature"},
        ]
        workflow.executor.execute.return_value = MagicMock(success=True)

        with patch("subprocess.run") as mock_run:
            mock_run.return_value = MagicMock(stdout="newcommit card1234: Test Card")

            result = await workflow._step_create_commit()

            assert result.success is True
            assert result.requires_llm is True

    @pytest.mark.asyncio
    async def test_step_create_commit_missing_card_id(self, workflow, tmp_path: Path):
        """Test LLM commit fails if card_id not in commit message."""
        workflow.state.commits = [{"hash": "abc1234", "message": "Fix bug"}]
        workflow.executor.execute.return_value = MagicMock(success=True)

        with patch("subprocess.run") as mock_run:
            mock_run.return_value = MagicMock(stdout="newcommit Some other message")

            result = await workflow._step_create_commit()

            assert result.success is False


class TestMergeWorkflowComments:
    """Tests for card comment posting."""

    @pytest.fixture
    def workflow(self, tmp_path: Path):
        """Create a MergeWorkflow for testing."""
        mock_client = MagicMock()
        mock_executor = MagicMock()

        return MergeWorkflow(
            card_id="card1234",
            card_title="Test Card",
            main_repo_path=tmp_path,
            client=mock_client,
            executor=mock_executor,
        )

    @pytest.mark.asyncio
    async def test_post_status_comment_merged(self, workflow):
        """Test success comment is posted."""
        workflow.state.branch_name = "card/card1234"
        workflow.state.commit_count = 3
        workflow.state.final_commit_hash = "abc1234567890"

        await workflow._post_status_comment(MergeStatus.MERGED)

        workflow.client.add_comment.assert_called_once()
        call_content = workflow.client.add_comment.call_args[0][1]
        assert "Merged successfully" in call_content
        assert "abc1234" in call_content

    @pytest.mark.asyncio
    async def test_post_status_comment_session_active(self, workflow):
        """Test session active comment is posted."""
        await workflow._post_status_comment(MergeStatus.SESSION_ACTIVE)

        workflow.client.add_comment.assert_called_once()
        call_content = workflow.client.add_comment.call_args[0][1]
        assert "session is still active" in call_content

    @pytest.mark.asyncio
    async def test_post_conflict_comment(self, workflow):
        """Test conflict comment lists files."""
        await workflow._post_conflict_comment(["file1.py", "file2.py"])

        workflow.client.add_comment.assert_called_once()
        call_content = workflow.client.add_comment.call_args[0][1]
        assert "file1.py" in call_content
        assert "file2.py" in call_content

    @pytest.mark.asyncio
    async def test_post_test_failure_comment(self, workflow):
        """Test test failure comment includes output."""
        await workflow._post_test_failure_comment(["test_foo"], "FAILED test_foo\nAssertionError")

        workflow.client.add_comment.assert_called_once()
        call_content = workflow.client.add_comment.call_args[0][1]
        assert "test_foo" in call_content
        assert "FAILED" in call_content

    @pytest.mark.asyncio
    async def test_post_comment_handles_api_error(self, workflow):
        """Test comment posting handles API errors gracefully."""
        workflow.client.add_comment.side_effect = Exception("API error")

        # Should not raise
        await workflow._post_status_comment(MergeStatus.MERGED)
