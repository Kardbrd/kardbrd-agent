"""Tests for ClaudeExecutor."""

import json

import pytest

from kardbrd_agent.executor import (
    AuthStatus,
    ClaudeExecutor,
    ClaudeResult,
)

# Save a reference to the real check_auth before autouse fixture patches it
_real_check_auth = ClaudeExecutor.check_auth


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

    def test_build_prompt_with_board_id_includes_label_instructions(self):
        """Test building prompt with board_id includes label instructions."""
        executor = ClaudeExecutor()

        prompt = executor.build_prompt(
            card_id="abc123",
            card_markdown="# Card Title\n\nDescription here",
            command="fix bug",
            comment_content="@coder fix bug",
            author_name="Paul",
            board_id="board456",
        )

        assert "get_board_labels" in prompt
        assert "board456" in prompt
        assert "label_ids" in prompt
        assert "full replace" in prompt

    def test_build_prompt_without_board_id_no_label_instructions(self):
        """Test building prompt without board_id omits label instructions."""
        executor = ClaudeExecutor()

        prompt = executor.build_prompt(
            card_id="abc123",
            card_markdown="# Card Title\n\nDescription here",
            command="fix bug",
            comment_content="@coder fix bug",
            author_name="Paul",
        )

        assert "get_board_labels" not in prompt
        assert "label_ids" not in prompt

    def test_build_prompt_skill_with_board_id_includes_label_instructions(self):
        """Test skill command prompt with board_id includes label instructions."""
        executor = ClaudeExecutor()

        prompt = executor.build_prompt(
            card_id="abc123",
            card_markdown="# Card Title\n\nDescription here",
            command="/kp",
            comment_content="@coder /kp",
            author_name="Paul",
            board_id="board456",
        )

        assert "/kp" in prompt
        assert "get_board_labels" in prompt
        assert "board456" in prompt

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

        config_path = create_mcp_config(api_url="http://localhost:8000", bot_token="test-token")
        try:
            assert config_path.exists()
            assert config_path.suffix == ".json"
        finally:
            config_path.unlink(missing_ok=True)

    def test_config_has_stdio_transport(self):
        """Test that the config uses stdio transport with kardbrd-mcp command."""
        import json

        from kardbrd_agent.executor import create_mcp_config

        config_path = create_mcp_config(api_url="http://localhost:8000", bot_token="test-token")
        try:
            with open(config_path) as f:
                config = json.load(f)

            assert "mcpServers" in config
            assert "kardbrd" in config["mcpServers"]
            server = config["mcpServers"]["kardbrd"]
            assert server["command"] == "kardbrd-mcp"
            assert "--api-url" in server["args"]
            assert "http://localhost:8000" in server["args"]
            assert "--token" in server["args"]
            assert "test-token" in server["args"]
            # Should NOT have SSE-style keys
            assert "type" not in server
            assert "url" not in server
        finally:
            config_path.unlink(missing_ok=True)

    def test_config_uses_provided_credentials(self):
        """Test that credentials are correctly embedded in config."""
        import json

        from kardbrd_agent.executor import create_mcp_config

        config_path = create_mcp_config(
            api_url="https://api.example.com", bot_token="secret-bot-123"
        )
        try:
            with open(config_path) as f:
                config = json.load(f)

            args = config["mcpServers"]["kardbrd"]["args"]
            assert "https://api.example.com" in args
            assert "secret-bot-123" in args
        finally:
            config_path.unlink(missing_ok=True)


class TestClaudeExecutorWithMcp:
    """Tests for ClaudeExecutor with MCP credentials."""

    def test_executor_stores_api_url_and_token(self):
        """Test that executor stores the API credentials."""
        executor = ClaudeExecutor(api_url="http://localhost:8000", bot_token="test-token")
        assert executor.api_url == "http://localhost:8000"
        assert executor.bot_token == "test-token"

    def test_executor_without_mcp_credentials(self):
        """Test that executor works without MCP credentials."""
        executor = ClaudeExecutor()
        assert executor.api_url is None
        assert executor.bot_token is None


class TestStrictMcpConfig:
    """Tests for --strict-mcp-config flag."""

    @pytest.mark.asyncio
    async def test_execute_includes_strict_mcp_config_when_credentials_set(self):
        """When MCP credentials are provided, --strict-mcp-config should be in the command."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = ClaudeExecutor(
            cwd="/tmp",
            timeout=60,
            api_url="http://localhost:8000",
            bot_token="test-token",
        )

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test prompt")

            # Get the positional args passed to create_subprocess_exec
            call_args = mock_exec.call_args[0]
            assert "--strict-mcp-config" in call_args
            assert "--mcp-config" in call_args

    @pytest.mark.asyncio
    async def test_execute_no_strict_mcp_config_without_credentials(self):
        """Without MCP credentials, --strict-mcp-config should NOT be in the command."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = ClaudeExecutor(cwd="/tmp", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test prompt")

            call_args = mock_exec.call_args[0]
            assert "--strict-mcp-config" not in call_args
            assert "--mcp-config" not in call_args

    @pytest.mark.asyncio
    async def test_strict_mcp_config_comes_after_mcp_config(self):
        """--strict-mcp-config should appear after --mcp-config in the command."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = ClaudeExecutor(
            cwd="/tmp",
            timeout=60,
            api_url="http://localhost:8000",
            bot_token="test-token",
        )

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test prompt")

            call_args = list(mock_exec.call_args[0])
            mcp_config_idx = call_args.index("--mcp-config")
            strict_idx = call_args.index("--strict-mcp-config")
            assert strict_idx > mcp_config_idx


class TestModelFlag:
    """Tests for --model flag in executor."""

    @pytest.mark.asyncio
    async def test_execute_includes_model_flag(self):
        """Test that --model flag is included when model is specified."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = ClaudeExecutor(cwd="/tmp", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test prompt", model="claude-haiku-4-5-20251001")

            call_args = list(mock_exec.call_args[0])
            assert "--model" in call_args
            model_idx = call_args.index("--model")
            assert call_args[model_idx + 1] == "claude-haiku-4-5-20251001"

    @pytest.mark.asyncio
    async def test_execute_no_model_flag_by_default(self):
        """Test that --model flag is NOT included when model is None."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = ClaudeExecutor(cwd="/tmp", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test prompt")

            call_args = list(mock_exec.call_args[0])
            assert "--model" not in call_args

    @pytest.mark.asyncio
    async def test_model_flag_comes_before_resume(self):
        """Test --model appears before --resume in the command."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = ClaudeExecutor(cwd="/tmp", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute(
                "test prompt",
                model="claude-haiku-4-5-20251001",
                resume_session_id="session-123",
            )

            call_args = list(mock_exec.call_args[0])
            model_idx = call_args.index("--model")
            resume_idx = call_args.index("--resume")
            assert model_idx < resume_idx


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


class TestAuthStatus:
    """Tests for AuthStatus dataclass."""

    def test_authenticated_status(self):
        """Test creating an authenticated status."""
        status = AuthStatus(
            authenticated=True,
            email="user@example.com",
            auth_method="claude.ai",
            subscription_type="max",
        )
        assert status.authenticated is True
        assert status.email == "user@example.com"
        assert status.error is None

    def test_unauthenticated_status(self):
        """Test creating an unauthenticated status."""
        status = AuthStatus(authenticated=False, error="Not logged in")
        assert status.authenticated is False
        assert status.error == "Not logged in"
        assert status.email is None

    def test_auth_hint_field(self):
        """Test auth_hint field on AuthStatus."""
        status = AuthStatus(
            authenticated=False,
            error="Not logged in",
            auth_hint="Run `claude auth login`",
        )
        assert status.auth_hint == "Run `claude auth login`"

    def test_auth_hint_default_none(self):
        """Test auth_hint defaults to None."""
        status = AuthStatus(authenticated=True)
        assert status.auth_hint is None


class TestCheckClaudeAuth:
    """Tests for ClaudeExecutor.check_auth static method."""

    @pytest.mark.asyncio
    async def test_authenticated_returns_success(self, mock_claude_auth):
        """Test successful authentication check."""
        mock_claude_auth.return_value = AuthStatus(
            authenticated=True,
            email="user@example.com",
            auth_method="claude.ai",
            subscription_type="max",
        )

        result = await ClaudeExecutor.check_auth()
        assert result.authenticated is True
        assert result.email == "user@example.com"
        assert result.auth_method == "claude.ai"
        assert result.subscription_type == "max"

    @pytest.mark.asyncio
    async def test_not_logged_in_returns_failure(self, mock_claude_auth):
        """Test unauthenticated returns failure."""
        mock_claude_auth.return_value = AuthStatus(
            authenticated=False, error="Claude CLI is not logged in"
        )

        result = await ClaudeExecutor.check_auth()
        assert result.authenticated is False
        assert "not logged in" in result.error

    @pytest.mark.asyncio
    async def test_cli_not_found_returns_failure(self, mock_claude_auth):
        """Test missing CLI returns failure."""
        mock_claude_auth.return_value = AuthStatus(
            authenticated=False, error="Claude CLI not found"
        )

        result = await ClaudeExecutor.check_auth()
        assert result.authenticated is False
        assert "not found" in result.error

    @pytest.mark.asyncio
    async def test_non_zero_exit_returns_failure(self, mock_claude_auth):
        """Test non-zero exit code returns failure."""
        mock_claude_auth.return_value = AuthStatus(
            authenticated=False,
            error="claude auth status exited with code 1: some error",
        )

        result = await ClaudeExecutor.check_auth()
        assert result.authenticated is False
        assert "exited with code" in result.error

    @pytest.mark.asyncio
    async def test_invalid_json_returns_failure(self, mock_claude_auth):
        """Test invalid JSON output returns failure."""
        mock_claude_auth.return_value = AuthStatus(
            authenticated=False,
            error="Failed to parse auth status output: not json",
        )

        result = await ClaudeExecutor.check_auth()
        assert result.authenticated is False
        assert "parse" in result.error

    @pytest.mark.asyncio
    async def test_real_check_auth_authenticated(self):
        """Test the real check_auth with a mocked subprocess (authenticated)."""
        from unittest.mock import AsyncMock, MagicMock, patch

        auth_output = json.dumps(
            {
                "loggedIn": True,
                "authMethod": "api_key",
                "email": "dev@example.com",
                "subscriptionType": "pro",
            }
        )

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(auth_output.encode(), b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            result = await _real_check_auth()

        assert result.authenticated is True
        assert result.email == "dev@example.com"
        assert result.auth_method == "api_key"
        assert result.subscription_type == "pro"

    @pytest.mark.asyncio
    async def test_real_check_auth_not_logged_in(self):
        """Test the real check_auth with loggedIn=false."""
        from unittest.mock import AsyncMock, MagicMock, patch

        auth_output = json.dumps({"loggedIn": False})

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(auth_output.encode(), b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            result = await _real_check_auth()

        assert result.authenticated is False
        assert "not logged in" in result.error

    @pytest.mark.asyncio
    async def test_real_check_auth_cli_not_found(self):
        """Test the real check_auth when CLI is not installed."""
        from unittest.mock import patch

        with patch(
            "asyncio.create_subprocess_exec",
            side_effect=FileNotFoundError(),
        ):
            result = await _real_check_auth()

        assert result.authenticated is False
        assert "not found" in result.error
