"""Tests for ClaudeExecutor."""

import json

import pytest

from kardbrd_agent.executor import (
    AuthStatus,
    ClaudeExecutor,
    ClaudeResult,
    build_prompt,
    extract_command,
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

        # Test /plan command
        command = executor.extract_command("@coder /plan", "@coder")
        assert command == "/plan"

        # Test /explore command
        command = executor.extract_command("@coder /explore", "@coder")
        assert command == "/explore"

        # Test /implement command
        command = executor.extract_command("@Coder /implement please", "@coder")
        assert command == "/implement please"

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

        command = executor.extract_command("@CODER /plan", "@coder")
        assert command == "/plan"

        command = executor.extract_command("@Coder /plan", "@coder")
        assert command == "/plan"

    def test_extract_command_with_extra_whitespace(self):
        """Test extracting command with extra whitespace."""
        executor = ClaudeExecutor()

        command = executor.extract_command("@coder   /plan  ", "@coder")
        assert command == "/plan"

    def test_build_prompt_skill_command(self):
        """Test building prompt for a skill command."""
        executor = ClaudeExecutor()

        prompt = executor.build_prompt(
            card_id="abc123",
            card_markdown="# Card Title\n\nDescription here",
            command="/plan",
            comment_content="@coder /plan",
            author_name="Paul",
        )

        assert "/plan" in prompt
        assert "Paul" in prompt
        assert "Card Title" in prompt
        assert "@coder /plan" in prompt
        assert "abc123" in prompt
        assert "kardbrd comment add" in prompt

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
        assert "kardbrd comment add" in prompt

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

        assert "kardbrd board labels" in prompt
        assert "board456" in prompt
        assert "--label-ids" in prompt
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
            command="/plan",
            comment_content="@coder /plan",
            author_name="Paul",
            board_id="board456",
        )

        assert "/plan" in prompt
        assert "kardbrd board labels" in prompt
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


class TestKardbrdEnvVars:
    """Tests for kardbrd CLI env vars passed to subprocess."""

    @pytest.mark.asyncio
    async def test_execute_passes_kardbrd_env_vars_when_credentials_set(self):
        """When credentials are provided, KARDBRD_TOKEN and KARDBRD_API_URL should be in env."""
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

            call_kwargs = mock_exec.call_args[1]
            env = call_kwargs.get("env", {})
            assert env.get("KARDBRD_TOKEN") == "test-token"
            assert env.get("KARDBRD_API_URL") == "http://localhost:8000"

            # MCP flags should NOT be present
            call_args = list(mock_exec.call_args[0])
            assert "--mcp-config" not in call_args
            assert "--strict-mcp-config" not in call_args

    @pytest.mark.asyncio
    async def test_execute_no_mcp_flags_without_credentials(self):
        """Without credentials, MCP flags should NOT be in the command."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = ClaudeExecutor(cwd="/tmp", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test prompt")

            call_args = list(mock_exec.call_args[0])
            assert "--strict-mcp-config" not in call_args
            assert "--mcp-config" not in call_args


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


class TestClaudeExecutorStdinPiping:
    """Tests for prompt piping via stdin to avoid ARG_MAX limits."""

    @pytest.mark.asyncio
    async def test_prompt_not_in_command_args(self):
        """Test that the prompt text is NOT passed as a CLI argument."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = ClaudeExecutor(cwd="/tmp", timeout=60)
        long_prompt = "x" * 100_000  # 100KB prompt

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute(long_prompt)

            call_args = list(mock_exec.call_args[0])
            # Prompt should NOT be in positional args
            assert long_prompt not in call_args
            # "-" placeholder should be used instead
            assert "-" in call_args
            p_idx = call_args.index("-p")
            assert call_args[p_idx + 1] == "-"

    @pytest.mark.asyncio
    async def test_prompt_piped_via_stdin(self):
        """Test that the prompt is sent via stdin pipe."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = ClaudeExecutor(cwd="/tmp", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test prompt")

            # stdin=PIPE should be in kwargs
            call_kwargs = mock_exec.call_args[1]
            assert call_kwargs.get("stdin") == -1  # asyncio.subprocess.PIPE == -1

            # communicate() should be called with input=prompt.encode()
            mock_process.communicate.assert_called_once_with(input=b"test prompt")


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


class TestClaudeExecutorTimeout:
    """Tests for ST4: graceful timeout with terminate() before kill()."""

    @pytest.mark.asyncio
    async def test_timeout_calls_terminate_first(self):
        """Test that timeout sends SIGTERM before SIGKILL."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = ClaudeExecutor(cwd="/tmp", timeout=1)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(side_effect=TimeoutError())
            mock_process.terminate = MagicMock()
            mock_process.kill = MagicMock()
            # First wait (grace period via wait_for) times out, second wait (after kill) succeeds
            mock_process.wait = AsyncMock(side_effect=[TimeoutError(), None])
            mock_exec.return_value = mock_process

            result = await executor.execute("test prompt")

        assert result.success is False
        assert "timed out" in result.error
        mock_process.terminate.assert_called_once()
        mock_process.kill.assert_called_once()

    @pytest.mark.asyncio
    async def test_timeout_terminate_succeeds_no_kill(self):
        """Test that SIGKILL is NOT sent when process exits after SIGTERM."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = ClaudeExecutor(cwd="/tmp", timeout=1)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(side_effect=TimeoutError())
            mock_process.terminate = MagicMock()
            mock_process.kill = MagicMock()
            # Process exits gracefully after terminate
            mock_process.wait = AsyncMock(return_value=0)
            mock_exec.return_value = mock_process

            result = await executor.execute("test prompt")

        assert result.success is False
        assert "timed out" in result.error
        mock_process.terminate.assert_called_once()
        mock_process.kill.assert_not_called()

    @pytest.mark.asyncio
    async def test_bot_token_not_in_command_args(self):
        """Test S1: bot_token is passed via env var, not CLI args."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = ClaudeExecutor(
            cwd="/tmp",
            timeout=60,
            api_url="http://localhost:8000",
            bot_token="secret-token-xyz",
        )

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test prompt")

            # Token must NOT appear in command-line args (visible via ps aux)
            call_args = list(mock_exec.call_args[0])
            for arg in call_args:
                assert "secret-token-xyz" not in str(arg), f"Token found in command arg: {arg}"

            # Token must be in env vars
            call_kwargs = mock_exec.call_args[1]
            env = call_kwargs.get("env", {})
            assert env.get("KARDBRD_TOKEN") == "secret-token-xyz"


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


class TestModuleLevelFunctions:
    """Tests for ST5: module-level build_prompt() and extract_command()."""

    def test_module_level_build_prompt_skill(self):
        """Test module-level build_prompt with skill command."""
        prompt = build_prompt(
            card_id="abc123",
            card_markdown="# Card Title",
            command="/plan",
            comment_content="@coder /plan",
            author_name="Paul",
        )
        assert "/plan" in prompt
        assert "abc123" in prompt
        assert "Paul" in prompt

    def test_module_level_build_prompt_free_form(self):
        """Test module-level build_prompt with free-form command."""
        prompt = build_prompt(
            card_id="xyz789",
            card_markdown="# Card",
            command="fix the bug",
            comment_content="@coder fix the bug",
            author_name="Paul",
        )
        assert "Task Request" in prompt
        assert "fix the bug" in prompt

    def test_module_level_build_prompt_with_board_id(self):
        """Test module-level build_prompt includes label instructions with board_id."""
        prompt = build_prompt(
            card_id="abc123",
            card_markdown="# Card",
            command="fix bug",
            comment_content="@coder fix bug",
            author_name="Paul",
            board_id="board456",
        )
        assert "kardbrd board labels" in prompt
        assert "board456" in prompt

    def test_module_level_extract_command_skill(self):
        """Test module-level extract_command with skill."""
        assert extract_command("@coder /plan", "@coder") == "/plan"

    def test_module_level_extract_command_free_form(self):
        """Test module-level extract_command with free-form."""
        assert extract_command("@coder fix the bug", "@coder") == "fix the bug"

    def test_module_level_extract_command_no_mention(self):
        """Test module-level extract_command when mention not found."""
        assert extract_command("just text", "@coder") == "just text"

    def test_claude_executor_delegates_to_module_level(self):
        """Test ClaudeExecutor methods produce same result as module-level functions."""
        executor = ClaudeExecutor()
        kwargs = dict(
            card_id="abc123",
            card_markdown="# Card",
            command="/plan",
            comment_content="@coder /plan",
            author_name="Paul",
        )
        assert executor.build_prompt(**kwargs) == build_prompt(**kwargs)
        assert executor.extract_command("@coder /plan", "@coder") == extract_command(
            "@coder /plan", "@coder"
        )


class TestLoadAgentFiles:
    """Tests for load_agent_files()."""

    def test_load_soul_md(self, tmp_path):
        """SOUL.md content is returned when present."""
        from kardbrd_agent.executor import load_agent_files

        (tmp_path / "SOUL.md").write_text("You are a helpful bot.")
        soul, rules = load_agent_files(tmp_path)
        assert soul == "You are a helpful bot."
        assert rules == ""

    def test_load_rules_md(self, tmp_path):
        """RULES.md content is returned when present."""
        from kardbrd_agent.executor import load_agent_files

        (tmp_path / "RULES.md").write_text("Never push to main.")
        soul, rules = load_agent_files(tmp_path)
        assert soul == ""
        assert rules == "Never push to main."

    def test_load_both(self, tmp_path):
        """Both SOUL.md and RULES.md are returned when present."""
        from kardbrd_agent.executor import load_agent_files

        (tmp_path / "SOUL.md").write_text("Identity text")
        (tmp_path / "RULES.md").write_text("Rules text")
        soul, rules = load_agent_files(tmp_path)
        assert soul == "Identity text"
        assert rules == "Rules text"

    def test_no_files(self, tmp_path):
        """Empty strings when neither file exists."""
        from kardbrd_agent.executor import load_agent_files

        soul, rules = load_agent_files(tmp_path)
        assert soul == ""
        assert rules == ""

    def test_none_cwd(self):
        """cwd=None returns empty strings."""
        from kardbrd_agent.executor import load_agent_files

        soul, rules = load_agent_files(None)
        assert soul == ""
        assert rules == ""


class TestLoadKnowledge:
    """Tests for load_knowledge()."""

    def test_no_knowledge_dir(self, tmp_path):
        """Returns empty string when knowledge/ doesn't exist."""
        from kardbrd_agent.executor import load_knowledge

        assert load_knowledge(tmp_path) == ""

    def test_knowledge_with_index(self, tmp_path):
        """index.yaml with always_load doc is included."""
        from kardbrd_agent.executor import load_knowledge

        knowledge_dir = tmp_path / "knowledge"
        knowledge_dir.mkdir()
        (knowledge_dir / "api.md").write_text("API docs here.")
        (knowledge_dir / "index.yaml").write_text(
            "documents:\n"
            "  - path: api.md\n"
            "    title: API Reference\n"
            "    always_load: true\n"
            "  - path: internal.md\n"
            "    title: Internal\n"
            "    priority: low\n"
        )
        (knowledge_dir / "internal.md").write_text("Internal stuff.")

        result = load_knowledge(tmp_path)
        assert "## Knowledge" in result
        assert "### API Reference" in result
        assert "API docs here." in result
        assert "Internal" not in result

    def test_knowledge_without_index(self, tmp_path):
        """All .md files loaded when no index.yaml exists."""
        from kardbrd_agent.executor import load_knowledge

        knowledge_dir = tmp_path / "knowledge"
        knowledge_dir.mkdir()
        (knowledge_dir / "alpha.md").write_text("Alpha content.")
        (knowledge_dir / "beta.md").write_text("Beta content.")

        result = load_knowledge(tmp_path)
        assert "## Knowledge" in result
        assert "### alpha" in result
        assert "Alpha content." in result
        assert "### beta" in result
        assert "Beta content." in result

    def test_knowledge_priority_filtering(self, tmp_path):
        """Only high-priority and always_load docs included."""
        from kardbrd_agent.executor import load_knowledge

        knowledge_dir = tmp_path / "knowledge"
        knowledge_dir.mkdir()
        (knowledge_dir / "high.md").write_text("High priority.")
        (knowledge_dir / "low.md").write_text("Low priority.")
        (knowledge_dir / "always.md").write_text("Always loaded.")
        (knowledge_dir / "index.yaml").write_text(
            "documents:\n"
            "  - path: high.md\n"
            "    title: High\n"
            "    priority: high\n"
            "  - path: low.md\n"
            "    title: Low\n"
            "    priority: low\n"
            "  - path: always.md\n"
            "    title: Always\n"
            "    always_load: true\n"
        )

        result = load_knowledge(tmp_path)
        assert "High priority." in result
        assert "Always loaded." in result
        assert "Low priority." not in result

    def test_knowledge_empty_dir(self, tmp_path):
        """Empty knowledge/ returns empty string."""
        from kardbrd_agent.executor import load_knowledge

        (tmp_path / "knowledge").mkdir()
        assert load_knowledge(tmp_path) == ""

    def test_none_cwd(self):
        """cwd=None returns empty string."""
        from kardbrd_agent.executor import load_knowledge

        assert load_knowledge(None) == ""


class TestBuildPromptWithAgentFiles:
    """Tests for build_prompt() with SOUL.md, RULES.md, and knowledge."""

    def test_build_prompt_includes_soul(self, tmp_path):
        """SOUL.md content appears in prompt."""
        (tmp_path / "SOUL.md").write_text("I am a coding assistant.")
        prompt = build_prompt(
            card_id="abc123",
            card_markdown="# Card",
            command="fix bug",
            comment_content="@coder fix bug",
            author_name="Paul",
            cwd=tmp_path,
        )
        assert "Agent Identity" in prompt
        assert "I am a coding assistant." in prompt

    def test_build_prompt_includes_rules(self, tmp_path):
        """RULES.md content appears in prompt."""
        (tmp_path / "RULES.md").write_text("Never delete production data.")
        prompt = build_prompt(
            card_id="abc123",
            card_markdown="# Card",
            command="fix bug",
            comment_content="@coder fix bug",
            author_name="Paul",
            cwd=tmp_path,
        )
        assert "Agent Rules" in prompt
        assert "Never delete production data." in prompt

    def test_build_prompt_includes_knowledge(self, tmp_path):
        """Knowledge content appears in prompt."""
        knowledge_dir = tmp_path / "knowledge"
        knowledge_dir.mkdir()
        (knowledge_dir / "standards.md").write_text("Coding standards here.")
        prompt = build_prompt(
            card_id="abc123",
            card_markdown="# Card",
            command="fix bug",
            comment_content="@coder fix bug",
            author_name="Paul",
            cwd=tmp_path,
        )
        assert "## Knowledge" in prompt
        assert "Coding standards here." in prompt

    def test_build_prompt_skill_with_agent_files(self, tmp_path):
        """Skill command + agent files are combined."""
        (tmp_path / "SOUL.md").write_text("Bot identity.")
        prompt = build_prompt(
            card_id="abc123",
            card_markdown="# Card",
            command="/plan",
            comment_content="@coder /plan",
            author_name="Paul",
            cwd=tmp_path,
        )
        assert "Agent Identity" in prompt
        assert "Bot identity." in prompt
        assert "/plan" in prompt

    def test_build_prompt_no_cwd_no_agent_files(self):
        """Without cwd, no agent file content appears."""
        prompt = build_prompt(
            card_id="abc123",
            card_markdown="# Card",
            command="fix bug",
            comment_content="@coder fix bug",
            author_name="Paul",
        )
        assert "Agent Identity" not in prompt
        assert "Agent Rules" not in prompt
        assert "## Knowledge" not in prompt
