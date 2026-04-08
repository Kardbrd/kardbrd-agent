"""Tests for CodexExecutor."""

import pytest

from kardbrd_agent.codex_executor import CODEX_MODEL_MAP, CodexExecutor
from kardbrd_agent.executor import ExecutorResult, build_prompt, extract_command


class TestCodexExecutorInit:
    """Tests for CodexExecutor initialization."""

    def test_default_init(self):
        """Test CodexExecutor with default parameters."""
        executor = CodexExecutor()
        assert executor.timeout == 3600
        assert executor.api_url is None
        assert executor.bot_token is None

    def test_init_with_credentials(self):
        """Test CodexExecutor stores API credentials."""
        executor = CodexExecutor(
            cwd="/tmp",
            timeout=300,
            api_url="http://localhost:8000",
            bot_token="test-token",
        )
        assert executor.api_url == "http://localhost:8000"
        assert executor.bot_token == "test-token"
        assert executor.timeout == 300


class TestCodexModelResolution:
    """Tests for model name resolution."""

    def test_resolve_codex_model_names(self):
        """Test Codex-specific model short names resolve correctly."""
        executor = CodexExecutor()
        assert executor._resolve_model("gpt-5.4") == "gpt-5.4"
        assert executor._resolve_model("gpt-5.4-mini") == "gpt-5.4-mini"
        assert executor._resolve_model("gpt-5.3-codex") == "gpt-5.3-codex"
        assert executor._resolve_model("gpt-5.3-codex-spark") == "gpt-5.3-codex-spark"
        assert executor._resolve_model("gpt-5.2") == "gpt-5.2"

    def test_resolve_none_returns_none(self):
        """Test None model returns None."""
        executor = CodexExecutor()
        assert executor._resolve_model(None) is None

    def test_resolve_unknown_passthrough(self):
        """Test unknown model strings are passed through."""
        executor = CodexExecutor()
        assert executor._resolve_model("custom-model-123") == "custom-model-123"

    def test_resolve_case_insensitive(self):
        """Test model resolution is case-insensitive for known names."""
        executor = CodexExecutor()
        assert executor._resolve_model("GPT-5.4") == "gpt-5.4"
        assert executor._resolve_model("GPT-5.4-MINI") == "gpt-5.4-mini"


class TestCodexParseOutput:
    """Tests for Codex JSONL output parsing."""

    def test_parse_message_items(self):
        """Test parsing message events with text content."""
        executor = CodexExecutor()
        stdout = (
            '{"type": "item.message", "content": "Hello "}\n'
            '{"type": "item.message", "content": "World"}\n'
        )
        result = executor._parse_output(stdout, "", 0)
        assert result.success is True
        assert result.result_text == "Hello World"
        assert result.error is None

    def test_parse_message_with_content_parts(self):
        """Test parsing message events with structured content parts."""
        executor = CodexExecutor()
        stdout = (
            '{"type": "item.message", "content": [{"type": "text", "text": "Hello "}]}\n'
            '{"type": "item.message", "content": [{"type": "text", "text": "World"}]}\n'
        )
        result = executor._parse_output(stdout, "", 0)
        assert result.success is True
        assert result.result_text == "Hello World"

    def test_parse_error_event(self):
        """Test parsing error events."""
        executor = CodexExecutor()
        stdout = '{"type": "error", "message": "Rate limited"}\n'
        result = executor._parse_output(stdout, "", 1)
        assert result.success is False
        assert result.error == "Rate limited"

    def test_parse_non_zero_exit(self):
        """Test non-zero exit code produces error."""
        executor = CodexExecutor()
        result = executor._parse_output("", "codex: error", 1)
        assert result.success is False
        assert "exited with code 1" in result.error
        assert "codex: error" in result.error

    def test_parse_empty_output_success(self):
        """Test empty output with exit code 0 is success."""
        executor = CodexExecutor()
        result = executor._parse_output("", "", 0)
        assert result.success is True
        assert result.result_text == ""

    def test_parse_non_json_lines_skipped(self):
        """Test non-JSON lines are skipped."""
        executor = CodexExecutor()
        stdout = (
            'Starting codex...\n{"type": "item.message", "content": "Done"}\nSome debug output\n'
        )
        result = executor._parse_output(stdout, "", 0)
        assert result.success is True
        assert result.result_text == "Done"


class TestCodexCheckAuth:
    """Tests for CodexExecutor.check_auth."""

    @pytest.mark.asyncio
    async def test_codex_not_installed(self):
        """Test check_auth when codex binary is missing."""
        from unittest.mock import patch

        with patch(
            "asyncio.create_subprocess_exec",
            side_effect=FileNotFoundError(),
        ):
            result = await CodexExecutor.check_auth()

        assert result.authenticated is False
        assert "not found" in result.error
        assert result.auth_hint is not None

    @pytest.mark.asyncio
    async def test_codex_version_fails(self):
        """Test check_auth when codex --version returns error."""
        from unittest.mock import AsyncMock, MagicMock, patch

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b"error"))
            mock_process.returncode = 1
            mock_exec.return_value = mock_process

            result = await CodexExecutor.check_auth()

        assert result.authenticated is False
        assert "version failed" in result.error

    @pytest.mark.asyncio
    async def test_codex_login_status_success(self):
        """Test check_auth when codex login status returns success."""
        from unittest.mock import AsyncMock, MagicMock, patch

        call_count = 0

        async def mock_create_subprocess(*args, **kwargs):
            nonlocal call_count
            call_count += 1
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"ok", b""))
            mock_process.returncode = 0
            return mock_process

        with patch("asyncio.create_subprocess_exec", side_effect=mock_create_subprocess):
            result = await CodexExecutor.check_auth()

        assert result.authenticated is True
        assert result.auth_method == "codex"
        assert call_count == 2  # --version and login status

    @pytest.mark.asyncio
    async def test_codex_login_status_failure(self):
        """Test check_auth when codex login status fails."""
        from unittest.mock import AsyncMock, MagicMock, patch

        call_count = 0

        async def mock_create_subprocess(*args, **kwargs):
            nonlocal call_count
            call_count += 1
            mock_process = MagicMock()
            if call_count == 1:
                # codex --version succeeds
                mock_process.communicate = AsyncMock(return_value=(b"1.0.0", b""))
                mock_process.returncode = 0
            else:
                # codex login status fails
                mock_process.communicate = AsyncMock(return_value=(b"", b"not logged in"))
                mock_process.returncode = 1
            return mock_process

        with patch("asyncio.create_subprocess_exec", side_effect=mock_create_subprocess):
            result = await CodexExecutor.check_auth()

        assert result.authenticated is False
        assert "codex login" in result.auth_hint
        assert "CODEX_API_KEY" in result.auth_hint

    @pytest.mark.asyncio
    async def test_codex_version_timeout(self):
        """Test check_auth when codex --version times out."""
        from unittest.mock import AsyncMock, MagicMock, patch

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(side_effect=TimeoutError())
            mock_exec.return_value = mock_process

            result = await CodexExecutor.check_auth()

        assert result.authenticated is False
        assert "timed out" in result.error

    @pytest.mark.asyncio
    async def test_codex_login_status_timeout(self):
        """Test check_auth when codex login status times out."""
        from unittest.mock import AsyncMock, MagicMock, patch

        call_count = 0

        async def mock_create_subprocess(*args, **kwargs):
            nonlocal call_count
            call_count += 1
            mock_process = MagicMock()
            if call_count == 1:
                # codex --version succeeds
                mock_process.communicate = AsyncMock(return_value=(b"1.0.0", b""))
                mock_process.returncode = 0
            else:
                # codex login status times out
                mock_process.communicate = AsyncMock(side_effect=TimeoutError())
            return mock_process

        with patch("asyncio.create_subprocess_exec", side_effect=mock_create_subprocess):
            result = await CodexExecutor.check_auth()

        assert result.authenticated is False
        assert "timed out" in result.error


class TestCodexExecutorAsync:
    """Async tests for CodexExecutor."""

    @pytest.mark.asyncio
    async def test_execute_returns_result(self):
        """Test that execute returns an ExecutorResult."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = CodexExecutor(cwd="/tmp", timeout=60)

        stdout = '{"type": "item.message", "content": "Done"}\n'

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
        """Test execute includes --model flag."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = CodexExecutor(cwd="/tmp", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test", model="gpt-5.4")

            call_args = list(mock_exec.call_args[0])
            assert "--model" in call_args
            model_idx = call_args.index("--model")
            assert call_args[model_idx + 1] == "gpt-5.4"

    @pytest.mark.asyncio
    async def test_execute_passes_kardbrd_env_vars(self):
        """When credentials are provided, KARDBRD_TOKEN and KARDBRD_API_URL should be in env."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = CodexExecutor(
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
        """Test execute does NOT override KARDBRD_TOKEN/KARDBRD_API_URL without credentials."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = CodexExecutor(cwd="/tmp", timeout=60)

        with (
            patch("asyncio.create_subprocess_exec") as mock_exec,
            patch.dict("os.environ", {}, clear=True),
        ):
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test")

            call_kwargs = mock_exec.call_args[1]
            env = call_kwargs.get("env", {})
            assert "KARDBRD_TOKEN" not in env
            assert "KARDBRD_API_URL" not in env

    @pytest.mark.asyncio
    async def test_execute_timeout(self):
        """Test execute handles timeout."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = CodexExecutor(cwd="/tmp", timeout=1)

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
    async def test_execute_codex_not_found(self):
        """Test execute when codex binary is missing."""
        from unittest.mock import patch

        executor = CodexExecutor(cwd="/tmp", timeout=60)

        with patch(
            "asyncio.create_subprocess_exec",
            side_effect=FileNotFoundError(),
        ):
            result = await executor.execute("test")

        assert result.success is False
        assert "not found" in result.error.lower()

    @pytest.mark.asyncio
    async def test_execute_resume_session_ignored(self):
        """Test that resume_session_id is ignored (codex exec doesn't support it)."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = CodexExecutor(cwd="/tmp", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test", resume_session_id="old-session-123")

            # Command should not contain any resume-related flags
            call_args = list(mock_exec.call_args[0])
            assert "--resume" not in call_args
            assert "old-session-123" not in call_args


class TestCodexExecutorStdinPiping:
    """Tests for prompt piping via stdin."""

    @pytest.mark.asyncio
    async def test_prompt_not_in_command_args(self):
        """Test that the prompt text is NOT passed as a CLI argument."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = CodexExecutor(cwd="/tmp", timeout=60)
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

    @pytest.mark.asyncio
    async def test_prompt_piped_via_stdin(self):
        """Test that the prompt is sent via stdin pipe."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = CodexExecutor(cwd="/tmp", timeout=60)

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


class TestCodexExecutorGracefulTimeout:
    """Tests for graceful timeout with terminate() before kill()."""

    @pytest.mark.asyncio
    async def test_timeout_calls_terminate_first(self):
        """Test that timeout sends SIGTERM before SIGKILL."""
        from unittest.mock import AsyncMock, MagicMock, patch

        executor = CodexExecutor(cwd="/tmp", timeout=1)

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

        executor = CodexExecutor(cwd="/tmp", timeout=1)

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


class TestCodexExecutorPrompt:
    """Tests for CodexExecutor prompt building and command extraction."""

    def test_build_prompt_delegates(self):
        """Test build_prompt produces valid prompt text."""
        executor = CodexExecutor()
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

    def test_extract_command_delegates(self):
        """Test extract_command extracts the command correctly."""
        executor = CodexExecutor()
        command = executor.extract_command("@bot /kp", "@bot")
        assert command == "/kp"

    def test_codex_executor_matches_module_level_functions(self):
        """Test CodexExecutor delegates to module-level functions."""
        executor = CodexExecutor()
        kwargs = dict(
            card_id="abc123",
            card_markdown="# Card",
            command="/kp",
            comment_content="@bot /kp",
            author_name="Paul",
        )
        assert executor.build_prompt(**kwargs) == build_prompt(**kwargs)
        assert executor.extract_command("@bot /kp", "@bot") == extract_command("@bot /kp", "@bot")


class TestCodexModelMap:
    """Tests for CODEX_MODEL_MAP constants."""

    def test_codex_model_map_has_expected_models(self):
        """Test CODEX_MODEL_MAP has the expected model short names."""
        assert "gpt-5.4" in CODEX_MODEL_MAP
        assert "gpt-5.4-mini" in CODEX_MODEL_MAP
        assert "gpt-5.3-codex" in CODEX_MODEL_MAP
        assert "gpt-5.3-codex-spark" in CODEX_MODEL_MAP
        assert "gpt-5.2" in CODEX_MODEL_MAP
