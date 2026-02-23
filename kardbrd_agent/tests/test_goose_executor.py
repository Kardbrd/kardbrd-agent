"""Tests for GooseExecutor."""

import os

import pytest

from kardbrd_agent.executor import ExecutorResult
from kardbrd_agent.goose_executor import (
    GOOSE_MODEL_MAP,
    LOCAL_PROVIDERS,
    PROVIDER_KEY_MAP,
    GooseExecutor,
)


class TestGooseExecutorInit:
    """Tests for GooseExecutor initialization."""

    def test_default_init(self):
        """Test GooseExecutor with default parameters."""
        executor = GooseExecutor()
        assert executor.timeout == 3600
        assert executor.api_url is None
        assert executor.bot_token is None

    def test_init_with_credentials(self):
        """Test GooseExecutor stores API credentials."""
        executor = GooseExecutor(
            cwd="/tmp",
            timeout=300,
            api_url="http://localhost:8000",
            bot_token="test-token",
        )
        assert executor.api_url == "http://localhost:8000"
        assert executor.bot_token == "test-token"
        assert executor.timeout == 300


class TestGooseModelResolution:
    """Tests for model name resolution."""

    def test_resolve_short_names(self):
        """Test short model names resolve to full model IDs."""
        executor = GooseExecutor()
        for short, full in GOOSE_MODEL_MAP.items():
            provider, model = executor._resolve_model(short)
            assert provider is None
            assert model == full

    def test_resolve_short_name_case_insensitive(self):
        """Test short name resolution is case-insensitive."""
        executor = GooseExecutor()
        _, model = executor._resolve_model("Opus")
        assert model == GOOSE_MODEL_MAP["opus"]

    def test_resolve_provider_model_format(self):
        """Test provider/model format is split correctly."""
        executor = GooseExecutor()
        provider, model = executor._resolve_model("openai/gpt-4")
        assert provider == "openai"
        assert model == "gpt-4"

    def test_resolve_none_returns_none(self):
        """Test None model returns (None, None)."""
        executor = GooseExecutor()
        provider, model = executor._resolve_model(None)
        assert provider is None
        assert model is None

    def test_resolve_unknown_passthrough(self):
        """Test unknown model strings are passed through."""
        executor = GooseExecutor()
        provider, model = executor._resolve_model("custom-model-123")
        assert provider is None
        assert model == "custom-model-123"


class TestGooseParseOutput:
    """Tests for Goose stream-json output parsing."""

    def test_parse_agent_message_chunks(self):
        """Test parsing AgentMessageChunk events."""
        executor = GooseExecutor()
        stdout = (
            '{"type": "AgentMessageChunk", "content": "Hello "}\n'
            '{"type": "AgentMessageChunk", "content": "World"}\n'
        )
        result = executor._parse_output(stdout, "", 0)
        assert result.success is True
        assert result.result_text == "Hello World"
        assert result.error is None

    def test_parse_error_event(self):
        """Test parsing error events."""
        executor = GooseExecutor()
        stdout = '{"type": "error", "message": "Rate limited"}\n'
        result = executor._parse_output(stdout, "", 1)
        assert result.success is False
        assert result.error == "Rate limited"

    def test_parse_non_zero_exit(self):
        """Test non-zero exit code produces error."""
        executor = GooseExecutor()
        result = executor._parse_output("", "goose: error", 1)
        assert result.success is False
        assert "exited with code 1" in result.error
        assert "goose: error" in result.error

    def test_parse_empty_output_success(self):
        """Test empty output with exit code 0 is success."""
        executor = GooseExecutor()
        result = executor._parse_output("", "", 0)
        assert result.success is True
        assert result.result_text == ""

    def test_parse_tool_call_failure(self):
        """Test ToolCallUpdate with failed status is logged."""
        executor = GooseExecutor()
        stdout = (
            '{"type": "AgentMessageChunk", "content": "Working..."}\n'
            '{"type": "ToolCallUpdate", "status": "failed", "result": "Permission denied"}\n'
        )
        result = executor._parse_output(stdout, "", 0)
        assert result.success is True
        assert result.result_text == "Working..."

    def test_parse_non_json_lines_skipped(self):
        """Test non-JSON lines are skipped."""
        executor = GooseExecutor()
        stdout = (
            "Starting goose...\n"
            '{"type": "AgentMessageChunk", "content": "Done"}\n'
            "Some debug output\n"
        )
        result = executor._parse_output(stdout, "", 0)
        assert result.success is True
        assert result.result_text == "Done"


class TestGooseCheckAuth:
    """Tests for GooseExecutor.check_auth."""

    @pytest.mark.asyncio
    async def test_goose_not_installed(self):
        """Test check_auth when goose binary is missing."""
        from unittest.mock import patch

        with patch(
            "asyncio.create_subprocess_exec",
            side_effect=FileNotFoundError(),
        ):
            result = await GooseExecutor.check_auth()

        assert result.authenticated is False
        assert "not found" in result.error
        assert result.auth_hint is not None

    @pytest.mark.asyncio
    async def test_goose_version_fails(self):
        """Test check_auth when goose version returns error."""
        from unittest.mock import AsyncMock, MagicMock, patch

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b"error"))
            mock_process.returncode = 1
            mock_exec.return_value = mock_process

            result = await GooseExecutor.check_auth()

        assert result.authenticated is False
        assert "version failed" in result.error

    @pytest.mark.asyncio
    async def test_goose_no_provider(self):
        """Test check_auth when GOOSE_PROVIDER is not set."""
        from unittest.mock import AsyncMock, MagicMock, patch

        with (
            patch("asyncio.create_subprocess_exec") as mock_exec,
            patch.dict(os.environ, {}, clear=True),
        ):
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"1.0.0", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            result = await GooseExecutor.check_auth()

        assert result.authenticated is False
        assert "GOOSE_PROVIDER" in result.error

    @pytest.mark.asyncio
    async def test_goose_local_provider_no_key_needed(self):
        """Test check_auth with local provider (ollama) doesn't need API key."""
        from unittest.mock import AsyncMock, MagicMock, patch

        with (
            patch("asyncio.create_subprocess_exec") as mock_exec,
            patch.dict(os.environ, {"GOOSE_PROVIDER": "ollama"}, clear=True),
        ):
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"1.0.0", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            result = await GooseExecutor.check_auth()

        assert result.authenticated is True
        assert "ollama" in result.auth_method

    @pytest.mark.asyncio
    async def test_goose_anthropic_provider_with_key(self):
        """Test check_auth with anthropic provider and API key set."""
        from unittest.mock import AsyncMock, MagicMock, patch

        env = {"GOOSE_PROVIDER": "anthropic", "ANTHROPIC_API_KEY": "sk-test"}
        with (
            patch("asyncio.create_subprocess_exec") as mock_exec,
            patch.dict(os.environ, env, clear=True),
        ):
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"1.0.0", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            result = await GooseExecutor.check_auth()

        assert result.authenticated is True
        assert "anthropic" in result.auth_method

    @pytest.mark.asyncio
    async def test_goose_provider_missing_api_key(self):
        """Test check_auth returns authenticated=False when API key env var is missing."""
        from unittest.mock import AsyncMock, MagicMock, patch

        # Provider is set but API key env var is missing
        env = {"GOOSE_PROVIDER": "anthropic"}
        with (
            patch("asyncio.create_subprocess_exec") as mock_exec,
            patch.dict(os.environ, env, clear=True),
        ):
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"1.0.0", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            result = await GooseExecutor.check_auth()

        assert result.authenticated is False
        assert "ANTHROPIC_API_KEY" in result.error
        assert result.auth_hint is not None
        assert "ANTHROPIC_API_KEY" in result.auth_hint


class TestGooseExecutorAsync:
    """Async tests for GooseExecutor."""

    @pytest.mark.asyncio
    async def test_execute_returns_result(self):
        """Test that execute returns an ExecutorResult."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = GooseExecutor(cwd="/tmp", timeout=60)

        stdout = '{"type": "AgentMessageChunk", "content": "Done"}\n'

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(stdout.encode(), b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            result = await executor.execute("test prompt")

        assert isinstance(result, ExecutorResult)
        assert result.success is True
        assert result.result_text == "Done"

    @pytest.mark.asyncio
    async def test_execute_with_model(self):
        """Test execute includes model flag."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = GooseExecutor(cwd="/tmp", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test", model="opus")

            call_args = list(mock_exec.call_args[0])
            assert "--model" in call_args
            model_idx = call_args.index("--model")
            assert call_args[model_idx + 1] == GOOSE_MODEL_MAP["opus"]

    @pytest.mark.asyncio
    async def test_execute_with_provider_model(self):
        """Test execute with provider/model format."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = GooseExecutor(cwd="/tmp", timeout=60)

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
    async def test_execute_with_mcp_extension(self):
        """Test execute includes MCP extension when credentials are set."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = GooseExecutor(
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

            call_args = list(mock_exec.call_args[0])
            assert "--with-extension" in call_args
            ext_idx = call_args.index("--with-extension")
            ext_cmd = call_args[ext_idx + 1]
            assert "kardbrd-mcp" in ext_cmd
            assert "http://localhost:8000" in ext_cmd
            # Token should be in env, NOT in extension command args
            assert "test-token" not in ext_cmd

    @pytest.mark.asyncio
    async def test_execute_bot_token_in_env_not_args(self):
        """Test bot_token is passed via env var, not in subprocess command args."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = GooseExecutor(
            cwd="/tmp",
            timeout=60,
            api_url="http://localhost:8000",
            bot_token="secret-token-123",
        )

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test")

            # Token must NOT appear in command args (visible via ps)
            call_args = list(mock_exec.call_args[0])
            for arg in call_args:
                assert "secret-token-123" not in str(arg)

            # Token must be in env vars
            call_kwargs = mock_exec.call_args[1]
            env = call_kwargs.get("env", {})
            assert env.get("KARDBRD_TOKEN") == "secret-token-123"

    @pytest.mark.asyncio
    async def test_execute_no_mcp_without_credentials(self):
        """Test execute does NOT include MCP extension without credentials."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = GooseExecutor(cwd="/tmp", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test")

            call_args = list(mock_exec.call_args[0])
            assert "--with-extension" not in call_args

    @pytest.mark.asyncio
    async def test_execute_timeout(self):
        """Test execute handles timeout."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = GooseExecutor(cwd="/tmp", timeout=1)

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
    async def test_execute_goose_not_found(self):
        """Test execute when goose binary is missing."""
        from unittest.mock import patch

        executor = GooseExecutor(cwd="/tmp", timeout=60)

        with patch(
            "asyncio.create_subprocess_exec",
            side_effect=FileNotFoundError(),
        ):
            result = await executor.execute("test")

        assert result.success is False
        assert "not found" in result.error.lower()

    @pytest.mark.asyncio
    async def test_execute_headless_env_vars(self):
        """Test execute sets headless mode env vars."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = GooseExecutor(cwd="/tmp", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test")

            call_kwargs = mock_exec.call_args[1]
            env = call_kwargs.get("env", {})
            assert env.get("GOOSE_MODE") == "auto"
            assert env.get("GOOSE_DISABLE_SESSION_NAMING") == "true"


class TestGooseExecutorGracefulTimeout:
    """Tests for ST4: graceful timeout with terminate() before kill()."""

    @pytest.mark.asyncio
    async def test_timeout_calls_terminate_first(self):
        """Test that timeout sends SIGTERM before SIGKILL."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = GooseExecutor(cwd="/tmp", timeout=1)

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

        executor = GooseExecutor(cwd="/tmp", timeout=1)

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


class TestGooseCheckAuthStrictness:
    """Tests for S4: check_auth returns authenticated=False when key missing."""

    @pytest.mark.asyncio
    async def test_openai_provider_missing_key_returns_false(self):
        """Test check_auth returns False when OpenAI API key is missing."""
        from unittest.mock import AsyncMock, MagicMock, patch

        env = {"GOOSE_PROVIDER": "openai"}
        with (
            patch("asyncio.create_subprocess_exec") as mock_exec,
            patch.dict(os.environ, env, clear=True),
        ):
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"1.0.0", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            result = await GooseExecutor.check_auth()

        assert result.authenticated is False
        assert "OPENAI_API_KEY" in result.error
        assert result.auth_hint is not None

    @pytest.mark.asyncio
    async def test_groq_provider_missing_key_returns_false(self):
        """Test check_auth returns False when Groq API key is missing."""
        from unittest.mock import AsyncMock, MagicMock, patch

        env = {"GOOSE_PROVIDER": "groq"}
        with (
            patch("asyncio.create_subprocess_exec") as mock_exec,
            patch.dict(os.environ, env, clear=True),
        ):
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"1.0.0", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            result = await GooseExecutor.check_auth()

        assert result.authenticated is False
        assert "GROQ_API_KEY" in result.error

    @pytest.mark.asyncio
    async def test_unknown_provider_returns_true(self):
        """Test check_auth returns True for unknown provider (no key to check)."""
        from unittest.mock import AsyncMock, MagicMock, patch

        env = {"GOOSE_PROVIDER": "custom-provider"}
        with (
            patch("asyncio.create_subprocess_exec") as mock_exec,
            patch.dict(os.environ, env, clear=True),
        ):
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"1.0.0", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            result = await GooseExecutor.check_auth()

        assert result.authenticated is True


class TestGooseExecutorPrompt:
    """Tests for GooseExecutor prompt building and command extraction."""

    def test_build_prompt_delegates_to_claude(self):
        """Test build_prompt produces valid prompt text."""
        executor = GooseExecutor()
        prompt = executor.build_prompt(
            card_id="abc123",
            card_markdown="# Card Title\n\nDescription",
            command="/kp",
            comment_content="@bot /kp",
            author_name="Paul",
        )
        assert "/kp" in prompt
        assert "Paul" in prompt
        assert "abc123" in prompt

    def test_extract_command_delegates_to_claude(self):
        """Test extract_command extracts the command correctly."""
        executor = GooseExecutor()
        command = executor.extract_command("@bot /kp", "@bot")
        assert command == "/kp"


class TestProviderKeyMap:
    """Tests for provider configuration constants."""

    def test_known_providers(self):
        """Test all expected providers are in PROVIDER_KEY_MAP."""
        assert "anthropic" in PROVIDER_KEY_MAP
        assert "openai" in PROVIDER_KEY_MAP

    def test_local_providers(self):
        """Test ollama is a local provider."""
        assert "ollama" in LOCAL_PROVIDERS

    def test_goose_model_map(self):
        """Test GOOSE_MODEL_MAP has expected short names."""
        assert "opus" in GOOSE_MODEL_MAP
        assert "sonnet" in GOOSE_MODEL_MAP
        assert "haiku" in GOOSE_MODEL_MAP
