"""Tests for PiExecutor."""

import os

import pytest

from kardbrd_agent.executor import ExecutorResult, build_prompt, extract_command
from kardbrd_agent.pi_executor import (
    PI_MODEL_MAP,
    PI_PROVIDER_KEY_MAP,
    PiExecutor,
)


class TestPiExecutorInit:
    """Tests for PiExecutor initialization."""

    def test_default_init(self):
        """Test PiExecutor with default parameters."""
        executor = PiExecutor()
        assert executor.timeout == 3600
        assert executor.api_url is None
        assert executor.bot_token is None

    def test_init_with_credentials(self):
        """Test PiExecutor stores API credentials."""
        executor = PiExecutor(
            cwd="/tmp",
            timeout=300,
            api_url="http://localhost:8000",
            bot_token="test-token",
        )
        assert executor.api_url == "http://localhost:8000"
        assert executor.bot_token == "test-token"
        assert executor.timeout == 300


class TestPiModelResolution:
    """Tests for model name resolution."""

    def test_resolve_short_names(self):
        """Test short model names resolve to full model IDs."""
        executor = PiExecutor()
        for short, full in PI_MODEL_MAP.items():
            provider, model = executor._resolve_model(short)
            assert provider is None
            assert model == full

    def test_resolve_short_name_case_insensitive(self):
        """Test short name resolution is case-insensitive."""
        executor = PiExecutor()
        _, model = executor._resolve_model("Opus")
        assert model == PI_MODEL_MAP["opus"]

    def test_resolve_provider_model_format(self):
        """Test provider/model format is split correctly."""
        executor = PiExecutor()
        provider, model = executor._resolve_model("openai/gpt-4")
        assert provider == "openai"
        assert model == "gpt-4"

    def test_resolve_none_returns_none(self):
        """Test None model returns (None, None)."""
        executor = PiExecutor()
        provider, model = executor._resolve_model(None)
        assert provider is None
        assert model is None

    def test_resolve_unknown_passthrough(self):
        """Test unknown model strings are passed through."""
        executor = PiExecutor()
        provider, model = executor._resolve_model("custom-model-123")
        assert provider is None
        assert model == "custom-model-123"


class TestPiParseOutput:
    """Tests for Pi JSON event stream output parsing."""

    def test_parse_message_end_events(self):
        """Test aggregating text from message_end events."""
        executor = PiExecutor()
        stdout = (
            '{"type": "session", "id": "sess-123"}\n'
            '{"type": "message_end", "message": {"content": "Hello "}}\n'
            '{"type": "message_end", "message": {"content": "World"}}\n'
        )
        result = executor._parse_output(stdout, "", 0)
        assert result.success is True
        assert result.result_text == "Hello World"
        assert result.error is None

    def test_parse_session_id_from_session_event(self):
        """Test extracting session ID from first session line."""
        executor = PiExecutor()
        stdout = (
            '{"type": "session", "id": "sess-abc-123", "version": "1.0"}\n'
            '{"type": "message_end", "message": {"content": "Done"}}\n'
        )
        result = executor._parse_output(stdout, "", 0)
        assert result.session_id == "sess-abc-123"

    def test_parse_error_non_zero_exit(self):
        """Test non-zero exit code produces error."""
        executor = PiExecutor()
        result = executor._parse_output("", "pi: error", 1)
        assert result.success is False
        assert "exited with code 1" in result.error
        assert "pi: error" in result.error

    def test_parse_empty_output_success(self):
        """Test empty output with exit code 0 is success."""
        executor = PiExecutor()
        result = executor._parse_output("", "", 0)
        assert result.success is True
        assert result.result_text == ""

    def test_parse_tool_execution_error(self):
        """Test tool_execution_end with isError: true is logged."""
        executor = PiExecutor()
        stdout = (
            '{"type": "message_end", "message": {"content": "Working..."}}\n'
            '{"type": "tool_execution_end", "toolName": "bash", '
            '"isError": true, "result": "Permission denied"}\n'
        )
        result = executor._parse_output(stdout, "", 0)
        assert result.success is True
        assert result.result_text == "Working..."

    def test_parse_non_json_lines_skipped(self):
        """Test non-JSON lines are skipped."""
        executor = PiExecutor()
        stdout = (
            "Starting pi...\n"
            '{"type": "message_end", "message": {"content": "Done"}}\n'
            "Some debug output\n"
        )
        result = executor._parse_output(stdout, "", 0)
        assert result.success is True
        assert result.result_text == "Done"

    def test_parse_error_event(self):
        """Test parsing error events."""
        executor = PiExecutor()
        stdout = '{"type": "error", "message": "Rate limited"}\n'
        result = executor._parse_output(stdout, "", 1)
        assert result.success is False
        assert result.error == "Rate limited"

    def test_parse_message_end_string_message(self):
        """Test message_end with string message (not dict)."""
        executor = PiExecutor()
        stdout = '{"type": "message_end", "message": "Simple text"}\n'
        result = executor._parse_output(stdout, "", 0)
        assert result.success is True
        assert result.result_text == "Simple text"


class TestPiCheckAuth:
    """Tests for PiExecutor.check_auth."""

    @pytest.mark.asyncio
    async def test_pi_not_installed(self):
        """Test check_auth when pi binary is missing."""
        from unittest.mock import patch

        with patch(
            "kardbrd_agent.pi_executor.shutil.which",
            return_value=None,
        ):
            result = await PiExecutor.check_auth()

        assert result.authenticated is False
        assert "not found" in result.error
        assert result.auth_hint is not None

    @pytest.mark.asyncio
    async def test_pi_version_fails(self):
        """Test check_auth when pi --version returns error."""
        from unittest.mock import AsyncMock, MagicMock, patch

        with (
            patch("kardbrd_agent.pi_executor.shutil.which", return_value="/usr/bin/pi"),
            patch("asyncio.create_subprocess_exec") as mock_exec,
        ):
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b"error"))
            mock_process.returncode = 1
            mock_exec.return_value = mock_process

            result = await PiExecutor.check_auth()

        assert result.authenticated is False
        assert "version failed" in result.error

    @pytest.mark.asyncio
    async def test_pi_no_provider(self):
        """Test check_auth when PI_PROVIDER is not set."""
        from unittest.mock import AsyncMock, MagicMock, patch

        with (
            patch("kardbrd_agent.pi_executor.shutil.which", return_value="/usr/bin/pi"),
            patch("asyncio.create_subprocess_exec") as mock_exec,
            patch.dict(os.environ, {}, clear=True),
        ):
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"1.0.0", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            result = await PiExecutor.check_auth()

        assert result.authenticated is False
        assert "PI_PROVIDER" in result.error

    @pytest.mark.asyncio
    async def test_pi_provider_with_key(self):
        """Test check_auth with anthropic provider and API key set."""
        from unittest.mock import AsyncMock, MagicMock, patch

        env = {"PI_PROVIDER": "anthropic", "ANTHROPIC_API_KEY": "sk-test"}
        with (
            patch("kardbrd_agent.pi_executor.shutil.which", return_value="/usr/bin/pi"),
            patch("asyncio.create_subprocess_exec") as mock_exec,
            patch.dict(os.environ, env, clear=True),
        ):
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"1.0.0", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            result = await PiExecutor.check_auth()

        assert result.authenticated is True
        assert "anthropic" in result.auth_method

    @pytest.mark.asyncio
    async def test_pi_provider_missing_key(self):
        """Test check_auth returns False when API key env var is missing."""
        from unittest.mock import AsyncMock, MagicMock, patch

        env = {"PI_PROVIDER": "anthropic"}
        with (
            patch("kardbrd_agent.pi_executor.shutil.which", return_value="/usr/bin/pi"),
            patch("asyncio.create_subprocess_exec") as mock_exec,
            patch.dict(os.environ, env, clear=True),
        ):
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"1.0.0", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            result = await PiExecutor.check_auth()

        assert result.authenticated is False
        assert "ANTHROPIC_API_KEY" in result.error
        assert result.auth_hint is not None

    @pytest.mark.asyncio
    async def test_pi_unknown_provider_returns_true(self):
        """Test check_auth returns True for unknown provider (no key to check)."""
        from unittest.mock import AsyncMock, MagicMock, patch

        env = {"PI_PROVIDER": "custom-provider"}
        with (
            patch("kardbrd_agent.pi_executor.shutil.which", return_value="/usr/bin/pi"),
            patch("asyncio.create_subprocess_exec") as mock_exec,
            patch.dict(os.environ, env, clear=True),
        ):
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"1.0.0", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            result = await PiExecutor.check_auth()

        assert result.authenticated is True


class TestPiExecutorAsync:
    """Async tests for PiExecutor."""

    @pytest.mark.asyncio
    async def test_execute_returns_result(self):
        """Test that execute returns an ExecutorResult."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = PiExecutor(cwd="/tmp", timeout=60)

        stdout = (
            '{"type": "session", "id": "sess-1"}\n'
            '{"type": "message_end", "message": {"content": "Done"}}\n'
        )

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(stdout.encode(), b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            result = await executor.execute("test prompt")

        assert isinstance(result, ExecutorResult)
        assert result.success is True
        assert result.result_text == "Done"
        assert result.session_id == "sess-1"

    @pytest.mark.asyncio
    async def test_execute_with_model(self):
        """Test execute includes model flag."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = PiExecutor(cwd="/tmp", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test", model="opus")

            call_args = list(mock_exec.call_args[0])
            assert "--model" in call_args
            model_idx = call_args.index("--model")
            assert call_args[model_idx + 1] == PI_MODEL_MAP["opus"]

    @pytest.mark.asyncio
    async def test_execute_with_provider_model(self):
        """Test execute with provider/model format."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = PiExecutor(cwd="/tmp", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test", model="openai/gpt-4")

            call_args = list(mock_exec.call_args[0])
            assert "--provider" in call_args
            assert "--model" in call_args
            provider_idx = call_args.index("--provider")
            model_idx = call_args.index("--model")
            assert call_args[provider_idx + 1] == "openai"
            assert call_args[model_idx + 1] == "gpt-4"

    @pytest.mark.asyncio
    async def test_execute_passes_kardbrd_env_vars(self):
        """When credentials are provided, KARDBRD_TOKEN and KARDBRD_API_URL should be in env."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = PiExecutor(
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

            await executor.execute("test")

            call_kwargs = mock_exec.call_args[1]
            env = call_kwargs.get("env", {})
            assert env.get("KARDBRD_TOKEN") == "test-token"
            assert env.get("KARDBRD_API_URL") == "http://localhost:8000"

    @pytest.mark.asyncio
    async def test_execute_no_kardbrd_env_without_credentials(self):
        """Test execute does NOT set KARDBRD vars without credentials."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = PiExecutor(cwd="/tmp", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test")

            call_kwargs = mock_exec.call_args[1]
            env = call_kwargs.get("env", {})
            # Without credentials, these should not be set by the executor
            # (they may exist from parent env, but executor shouldn't add them)
            assert "KARDBRD_TOKEN" not in env or env.get("KARDBRD_TOKEN") != "test-token"

    @pytest.mark.asyncio
    async def test_execute_timeout(self):
        """Test execute handles timeout."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = PiExecutor(cwd="/tmp", timeout=1)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(side_effect=TimeoutError())
            mock_process.kill = MagicMock()
            mock_process.wait = AsyncMock()
            mock_exec.return_value = mock_process

            result = await executor.execute("test")

        assert result.success is False
        assert "timed out" in result.error

    @pytest.mark.asyncio
    async def test_execute_pi_not_found(self):
        """Test execute when pi binary is missing."""
        from unittest.mock import patch

        executor = PiExecutor(cwd="/tmp", timeout=60)

        with patch(
            "kardbrd_agent.pi_executor.shutil.which",
            return_value=None,
        ):
            result = await executor.execute("test")

        assert result.success is False
        assert "not found" in result.error.lower()

    @pytest.mark.asyncio
    async def test_execute_resume_session(self):
        """Test execute with resume_session_id replaces --no-session."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = PiExecutor(cwd="/tmp", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test", resume_session_id="sess-resume-123")

            call_args = list(mock_exec.call_args[0])
            assert "--no-session" not in call_args
            assert "--session" in call_args
            session_idx = call_args.index("--session")
            assert call_args[session_idx + 1] == "sess-resume-123"


class TestPiExecutorStdinPiping:
    """Tests for prompt piping via stdin to avoid ARG_MAX limits."""

    @pytest.mark.asyncio
    async def test_prompt_not_in_command_args(self):
        """Test that the prompt text is NOT passed as a CLI argument."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = PiExecutor(cwd="/tmp", timeout=60)
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
            # "-" placeholder should be used for -p flag
            p_idx = call_args.index("-p")
            assert call_args[p_idx + 1] == "-"

    @pytest.mark.asyncio
    async def test_prompt_piped_via_stdin(self):
        """Test that the prompt is sent via stdin pipe."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = PiExecutor(cwd="/tmp", timeout=60)

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


class TestPiExecutorGracefulTimeout:
    """Tests for graceful timeout with terminate() before kill()."""

    @pytest.mark.asyncio
    async def test_timeout_calls_terminate_first(self):
        """Test that timeout sends SIGTERM before SIGKILL."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = PiExecutor(cwd="/tmp", timeout=1)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(side_effect=TimeoutError())
            mock_process.terminate = MagicMock()
            mock_process.kill = MagicMock()
            # First wait (grace period) times out, second wait (after kill) succeeds
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

        executor = PiExecutor(cwd="/tmp", timeout=1)

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


class TestPiExecutorPrompt:
    """Tests for PiExecutor prompt building and command extraction."""

    def test_build_prompt_delegates(self):
        """Test build_prompt produces valid prompt text."""
        executor = PiExecutor()
        prompt = executor.build_prompt(
            card_id="abc123",
            card_markdown="# Card Title\n\nDescription",
            command="/plan",
            comment_content="@bot /plan",
            author_name="Paul",
        )
        assert "/plan" in prompt
        assert "Paul" in prompt
        assert "abc123" in prompt

    def test_extract_command_delegates(self):
        """Test extract_command extracts the command correctly."""
        executor = PiExecutor()
        command = executor.extract_command("@bot /plan", "@bot")
        assert command == "/plan"

    def test_pi_executor_matches_module_level_functions(self):
        """Test PiExecutor delegates to module-level functions."""
        executor = PiExecutor()
        kwargs = dict(
            card_id="abc123",
            card_markdown="# Card",
            command="/plan",
            comment_content="@bot /plan",
            author_name="Paul",
        )
        assert executor.build_prompt(**kwargs) == build_prompt(**kwargs)
        assert executor.extract_command("@bot /plan", "@bot") == extract_command(
            "@bot /plan", "@bot"
        )


class TestPiProviderKeyMap:
    """Tests for provider configuration constants."""

    def test_known_providers(self):
        """Test all expected providers are in PI_PROVIDER_KEY_MAP."""
        assert "anthropic" in PI_PROVIDER_KEY_MAP
        assert "openai" in PI_PROVIDER_KEY_MAP
        assert "google" in PI_PROVIDER_KEY_MAP
        assert "deepseek" in PI_PROVIDER_KEY_MAP

    def test_pi_model_map(self):
        """Test PI_MODEL_MAP has expected short names."""
        assert "opus" in PI_MODEL_MAP
        assert "sonnet" in PI_MODEL_MAP
        assert "haiku" in PI_MODEL_MAP


class TestPiExecutorValidExecutor:
    """Test Pi is recognized as a valid executor type."""

    def test_pi_in_valid_executors(self):
        """Test 'pi' is in VALID_EXECUTORS set."""
        from kardbrd_agent.rules import VALID_EXECUTORS

        assert "pi" in VALID_EXECUTORS

    def test_pi_executor_importable(self):
        """Test PiExecutor can be imported from package."""
        from kardbrd_agent import PiExecutor as PiExec

        assert PiExec is PiExecutor
