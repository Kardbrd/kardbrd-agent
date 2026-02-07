"""Tests for ClaudeExecutor."""

import pytest

from kardbrd_agent.executor import (
    ClaudeExecutor,
    ClaudeResult,
)


class TestClaudeResult:
    """Tests for ClaudeResult dataclass."""

    def test_success_result(self):
        """Test creating a successful result."""
        result = ClaudeResult(
            success=True,
            result_text="Task completed",
            cost_usd=0.01,
            duration_ms=5000,
        )
        assert result.success is True
        assert result.result_text == "Task completed"
        assert result.error is None
        assert result.cost_usd == 0.01
        assert result.duration_ms == 5000

    def test_error_result(self):
        """Test creating an error result."""
        result = ClaudeResult(
            success=False,
            result_text="",
            error="Something went wrong",
        )
        assert result.success is False
        assert result.error == "Something went wrong"


class TestClaudeExecutor:
    """Tests for ClaudeExecutor."""

    def test_extract_command_with_skill(self):
        """Test extracting a skill command."""
        executor = ClaudeExecutor()

        # Test /kp command
        command = executor.extract_command("@coder /kp", "@coder")
        assert command == "/kp"

        # Test /ke command
        command = executor.extract_command("@coder /ke", "@coder")
        assert command == "/ke"

        # Test /ki command
        command = executor.extract_command("@Coder /ki please", "@coder")
        assert command == "/ki please"

    def test_extract_command_free_form(self):
        """Test extracting a free-form command."""
        executor = ClaudeExecutor()

        command = executor.extract_command("@coder fix the login bug", "@coder")
        assert command == "fix the login bug"

        command = executor.extract_command("@coder please review this code", "@coder")
        assert command == "please review this code"

    def test_extract_command_case_insensitive(self):
        """Test that command extraction is case-insensitive for mention."""
        executor = ClaudeExecutor()

        command = executor.extract_command("@CODER /kp", "@coder")
        assert command == "/kp"

        command = executor.extract_command("@Coder /kp", "@coder")
        assert command == "/kp"

    def test_extract_command_with_extra_whitespace(self):
        """Test extracting command with extra whitespace."""
        executor = ClaudeExecutor()

        command = executor.extract_command("@coder   /kp  ", "@coder")
        assert command == "/kp"

    def test_build_prompt_skill_command(self):
        """Test building prompt for a skill command."""
        executor = ClaudeExecutor()

        prompt = executor.build_prompt(
            card_id="abc123",
            card_markdown="# Card Title\n\nDescription here",
            command="/kp",
            comment_content="@coder /kp",
            author_name="Paul",
        )

        assert "/kp" in prompt
        assert "Paul" in prompt
        assert "Card Title" in prompt
        assert "@coder /kp" in prompt
        assert "abc123" in prompt
        assert "add_comment" in prompt

    def test_build_prompt_free_form(self):
        """Test building prompt for a free-form request."""
        executor = ClaudeExecutor()

        prompt = executor.build_prompt(
            card_id="xyz789",
            card_markdown="# Card Title\n\nDescription here",
            command="fix the login bug",
            comment_content="@coder fix the login bug",
            author_name="Paul",
        )

        assert "fix the login bug" in prompt
        assert "Paul" in prompt
        assert "Card Title" in prompt
        assert "Task Request" in prompt
        assert "xyz789" in prompt
        assert "add_comment" in prompt

    def test_parse_output_success(self):
        """Test parsing successful Claude output."""
        executor = ClaudeExecutor()

        stdout = (
            '{"type": "assistant", "content": "Working..."}\n'
            '{"type": "result", "result": "Done!", "cost_usd": 0.01,'
            ' "duration_ms": 5000, "session_id": "abc123"}'
        )

        result = executor._parse_output(stdout, "", 0)

        assert result.success is True
        assert result.result_text == "Done!"
        assert result.cost_usd == 0.01
        assert result.duration_ms == 5000
        assert result.session_id == "abc123"
        assert result.error is None

    def test_parse_output_error(self):
        """Test parsing Claude output with error."""
        executor = ClaudeExecutor()

        stdout = """{"type": "error", "error": {"message": "Rate limited"}}"""

        result = executor._parse_output(stdout, "", 1)

        assert result.success is False
        assert result.error == "Rate limited"

    def test_parse_output_non_zero_exit(self):
        """Test parsing output when Claude exits with non-zero code."""
        executor = ClaudeExecutor()

        stdout = """{"type": "assistant", "content": "Started..."}"""

        result = executor._parse_output(stdout, "Error: crashed", 1)

        assert result.success is False
        assert "exited with code 1" in result.error
        assert "crashed" in result.error

    def test_parse_output_empty(self):
        """Test parsing empty output."""
        executor = ClaudeExecutor()

        result = executor._parse_output("", "", 0)

        assert result.success is True
        assert result.result_text == ""


class TestClaudeExecutorAsync:
    """Async tests for ClaudeExecutor."""

    @pytest.mark.asyncio
    async def test_execute_returns_result(self):
        """Test that execute returns a ClaudeResult object."""
        executor = ClaudeExecutor()

        # Execute with a simple prompt - Claude may or may not be installed
        result = await executor.execute("test prompt")

        # Should always return a ClaudeResult
        assert isinstance(result, ClaudeResult)
        assert isinstance(result.success, bool)
        assert isinstance(result.result_text, str)

        # If Claude is installed, success should be True
        # If not, success should be False with an error
        if not result.success:
            assert result.error is not None


class TestCreateMcpConfig:
    """Tests for the create_mcp_config function."""

    def test_creates_temp_file(self):
        """Test that create_mcp_config creates a temp file."""
        from kardbrd_agent.executor import create_mcp_config

        config_path = create_mcp_config()
        try:
            assert config_path.exists()
            assert config_path.suffix == ".json"
        finally:
            config_path.unlink(missing_ok=True)

    def test_config_has_correct_structure(self):
        """Test that the config file has the correct structure."""
        import json

        from kardbrd_agent.executor import DEFAULT_MCP_PORT, create_mcp_config

        config_path = create_mcp_config()
        try:
            with open(config_path) as f:
                config = json.load(f)

            assert "mcpServers" in config
            assert "kardbrd" in config["mcpServers"]
            assert config["mcpServers"]["kardbrd"]["type"] == "sse"
            assert f":{DEFAULT_MCP_PORT}/sse" in config["mcpServers"]["kardbrd"]["url"]
        finally:
            config_path.unlink(missing_ok=True)

    def test_config_uses_custom_port(self):
        """Test that create_mcp_config uses a custom port."""
        import json

        from kardbrd_agent.executor import create_mcp_config

        config_path = create_mcp_config(port=9999)
        try:
            with open(config_path) as f:
                config = json.load(f)

            assert ":9999/sse" in config["mcpServers"]["kardbrd"]["url"]
        finally:
            config_path.unlink(missing_ok=True)


class TestClaudeExecutorWithMcp:
    """Tests for ClaudeExecutor with MCP port."""

    def test_executor_stores_mcp_port(self):
        """Test that executor stores the mcp_port."""
        executor = ClaudeExecutor(mcp_port=8765)
        assert executor.mcp_port == 8765

    def test_executor_without_mcp_port(self):
        """Test that executor works without mcp_port."""
        executor = ClaudeExecutor()
        assert executor.mcp_port is None


class TestClaudeExecutorCwd:
    """Tests for cwd parameter in executor."""

    @pytest.mark.asyncio
    async def test_execute_uses_passed_cwd(self):
        """Test execute uses passed cwd over default."""
        from pathlib import Path
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = ClaudeExecutor(cwd="/default/path", timeout=60)
        custom_cwd = Path("/custom/worktree")

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("prompt", cwd=custom_cwd)

            call_kwargs = mock_exec.call_args[1]
            assert call_kwargs["cwd"] == custom_cwd

    @pytest.mark.asyncio
    async def test_execute_uses_default_cwd_when_not_passed(self):
        """Test execute uses default cwd when none passed."""
        from pathlib import Path
        from unittest.mock import AsyncMock, MagicMock, patch

        default_cwd = Path("/default/path")
        executor = ClaudeExecutor(cwd=default_cwd, timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("prompt")  # No cwd passed

            call_kwargs = mock_exec.call_args[1]
            assert call_kwargs["cwd"] == default_cwd
