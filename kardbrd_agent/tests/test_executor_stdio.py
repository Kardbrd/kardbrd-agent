"""Tests for ClaudeExecutor stdio MCP config (replacing SSE transport).

These tests validate the new behavior BEFORE the code is changed.
They will fail until the executor is rewritten to use stdio transport.
Once the changes are applied, these tests prove:

1. create_mcp_config() generates a valid stdio config (not SSE)
2. The config embeds the kardbrd-mcp command with --api-url and --token args
3. ClaudeExecutor accepts api_url/bot_token instead of mcp_port
4. The MCP config is only created when both api_url and bot_token are provided
5. The execute() method passes the correct MCP config to Claude CLI
"""

import json
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest


class TestCreateMcpConfigStdio:
    """Tests for the new stdio-based create_mcp_config function.

    PROVES: The MCP config file uses stdio transport (command + args)
    instead of SSE transport (type + url). This is the core architectural
    change — each Claude session gets its own kardbrd-mcp subprocess
    rather than connecting to a shared HTTP server.

    SAFETY: These tests ensure the config file is well-formed JSON,
    contains the correct command name, passes credentials correctly,
    and does NOT contain any SSE-specific keys. If any of these fail,
    Claude will not be able to connect to the kardbrd MCP server.
    """

    def test_creates_temp_file_with_credentials(self):
        """Test that create_mcp_config creates a temp file when given credentials.

        PROVES: The function produces a real file on disk that Claude CLI
        can read via --mcp-config. Without a valid file, Claude cannot
        discover any MCP tools.
        """
        from kardbrd_agent.executor import create_mcp_config

        config_path = create_mcp_config(
            api_url="http://localhost:8000", bot_token="test-token"
        )
        try:
            assert config_path.exists()
            assert config_path.suffix == ".json"
        finally:
            config_path.unlink(missing_ok=True)

    def test_config_uses_stdio_transport(self):
        """Test config specifies kardbrd-mcp command (stdio) not SSE URL.

        PROVES: The config uses 'command' key (stdio transport) instead of
        'type: sse' + 'url' (HTTP transport). This is the fundamental change:
        stdio subprocess per session vs shared HTTP server.

        SAFETY: If the config still has 'type' or 'url' keys, Claude would
        try to connect to a non-existent HTTP server and all MCP tools fail.
        """
        from kardbrd_agent.executor import create_mcp_config

        config_path = create_mcp_config(
            api_url="http://localhost:8000", bot_token="test-token"
        )
        try:
            with open(config_path) as f:
                config = json.load(f)

            server = config["mcpServers"]["kardbrd"]

            # Must have stdio transport keys
            assert server["command"] == "kardbrd-mcp"
            assert isinstance(server["args"], list)

            # Must NOT have SSE transport keys
            assert "type" not in server, "Config should not have SSE 'type' key"
            assert "url" not in server, "Config should not have SSE 'url' key"
        finally:
            config_path.unlink(missing_ok=True)

    def test_config_passes_api_url_and_token_as_args(self):
        """Test credentials are passed via CLI args to kardbrd-mcp.

        PROVES: The --api-url and --token arguments are embedded in the config
        so the kardbrd-mcp subprocess can authenticate with the API.

        SAFETY: If credentials are missing or in wrong order, kardbrd-mcp will
        fail to authenticate and all MCP tool calls will return auth errors.
        """
        from kardbrd_agent.executor import create_mcp_config

        config_path = create_mcp_config(
            api_url="https://api.example.com", bot_token="secret-bot-123"
        )
        try:
            with open(config_path) as f:
                config = json.load(f)

            args = config["mcpServers"]["kardbrd"]["args"]

            # Check that --api-url and its value are present
            assert "--api-url" in args
            api_url_idx = args.index("--api-url")
            assert args[api_url_idx + 1] == "https://api.example.com"

            # Check that --token and its value are present
            assert "--token" in args
            token_idx = args.index("--token")
            assert args[token_idx + 1] == "secret-bot-123"
        finally:
            config_path.unlink(missing_ok=True)

    def test_config_server_name_is_kardbrd(self):
        """Test the MCP server entry is named 'kardbrd'.

        PROVES: The server name matches what Claude expects (mcp__kardbrd__*
        tool prefix). If the name changes, Claude will not find the tools.
        """
        from kardbrd_agent.executor import create_mcp_config

        config_path = create_mcp_config(
            api_url="http://localhost:8000", bot_token="test-token"
        )
        try:
            with open(config_path) as f:
                config = json.load(f)

            assert "mcpServers" in config
            assert "kardbrd" in config["mcpServers"]
            assert len(config["mcpServers"]) == 1  # Only one server entry
        finally:
            config_path.unlink(missing_ok=True)

    def test_different_credentials_produce_different_configs(self):
        """Test that different credentials produce different config content.

        PROVES: Credentials are not hardcoded — each invocation correctly
        embeds the provided api_url and bot_token. Important for multi-board
        or credential rotation scenarios.
        """
        from kardbrd_agent.executor import create_mcp_config

        path1 = create_mcp_config(api_url="http://server1.com", bot_token="token-aaa")
        path2 = create_mcp_config(api_url="http://server2.com", bot_token="token-bbb")
        try:
            with open(path1) as f:
                config1 = json.load(f)
            with open(path2) as f:
                config2 = json.load(f)

            args1 = config1["mcpServers"]["kardbrd"]["args"]
            args2 = config2["mcpServers"]["kardbrd"]["args"]

            assert "http://server1.com" in args1
            assert "token-aaa" in args1
            assert "http://server2.com" in args2
            assert "token-bbb" in args2
            assert args1 != args2
        finally:
            path1.unlink(missing_ok=True)
            path2.unlink(missing_ok=True)


class TestClaudeExecutorCredentials:
    """Tests for ClaudeExecutor with api_url/bot_token instead of mcp_port.

    PROVES: The executor constructor accepts the new credential parameters
    and stores them for later use when creating MCP configs.

    SAFETY: If the executor doesn't store these correctly, Claude will be
    spawned without MCP tools and cannot interact with the kardbrd API.
    """

    def test_executor_stores_api_url_and_bot_token(self):
        """Test executor stores both credential fields.

        PROVES: Constructor properly assigns api_url and bot_token to
        instance attributes that create_mcp_config() will use later.
        """
        from kardbrd_agent.executor import ClaudeExecutor

        executor = ClaudeExecutor(
            api_url="http://localhost:8000", bot_token="test-token"
        )
        assert executor.api_url == "http://localhost:8000"
        assert executor.bot_token == "test-token"

    def test_executor_defaults_to_no_credentials(self):
        """Test executor works without MCP credentials.

        PROVES: The executor can be used without MCP (e.g., for merge
        workflow or standalone usage) by defaulting to None.
        """
        from kardbrd_agent.executor import ClaudeExecutor

        executor = ClaudeExecutor()
        assert executor.api_url is None
        assert executor.bot_token is None

    def test_executor_no_mcp_port_attribute(self):
        """Test executor no longer has mcp_port attribute.

        PROVES: The old mcp_port parameter has been removed, preventing
        accidental use of the SSE transport path.
        """
        from kardbrd_agent.executor import ClaudeExecutor

        executor = ClaudeExecutor()
        assert not hasattr(executor, "mcp_port")


class TestExecutorMcpConfigIntegration:
    """Tests for execute() creating MCP config from credentials.

    PROVES: The execute() method correctly creates an MCP config when
    credentials are provided, and skips it when they're not.

    SAFETY: These tests mock the subprocess to avoid actually running
    Claude, but verify the correct --mcp-config flag is passed.
    """

    @pytest.mark.asyncio
    async def test_execute_creates_mcp_config_with_credentials(self):
        """Test execute() creates MCP config when api_url and bot_token are set.

        PROVES: When credentials are provided, execute() creates a temporary
        MCP config file and passes it to Claude via --mcp-config.
        """
        from kardbrd_agent.executor import ClaudeExecutor

        executor = ClaudeExecutor(
            api_url="http://localhost:8000",
            bot_token="test-token",
            timeout=60,
        )

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test prompt")

            # Verify --mcp-config was in the command
            call_args = mock_exec.call_args[0]
            assert "--mcp-config" in call_args

    @pytest.mark.asyncio
    async def test_execute_skips_mcp_config_without_credentials(self):
        """Test execute() does NOT create MCP config when no credentials.

        PROVES: Without api_url/bot_token, no --mcp-config is passed.
        This is the correct behavior for merge workflow executions
        that don't need MCP tools.
        """
        from kardbrd_agent.executor import ClaudeExecutor

        executor = ClaudeExecutor(timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test prompt")

            call_args = mock_exec.call_args[0]
            assert "--mcp-config" not in call_args

    @pytest.mark.asyncio
    async def test_execute_skips_mcp_config_with_partial_credentials(self):
        """Test execute() skips MCP config when only one credential is set.

        PROVES: Both api_url AND bot_token must be set for MCP config
        to be created. Partial credentials are treated as no credentials.
        """
        from kardbrd_agent.executor import ClaudeExecutor

        # Only api_url, no bot_token
        executor = ClaudeExecutor(api_url="http://localhost:8000", timeout=60)

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            await executor.execute("test prompt")

            call_args = mock_exec.call_args[0]
            assert "--mcp-config" not in call_args

    @pytest.mark.asyncio
    async def test_mcp_config_cleaned_up_after_execution(self):
        """Test MCP config temp file is deleted after Claude exits.

        PROVES: Temp files with credentials don't accumulate on disk.
        Important for security since the bot token is in the config file.
        """
        from kardbrd_agent.executor import ClaudeExecutor

        executor = ClaudeExecutor(
            api_url="http://localhost:8000",
            bot_token="test-token",
            timeout=60,
        )

        config_paths = []

        with patch("asyncio.create_subprocess_exec") as mock_exec:
            mock_process = MagicMock()
            mock_process.communicate = AsyncMock(return_value=(b"", b""))
            mock_process.returncode = 0
            mock_exec.return_value = mock_process

            # Capture the config path before cleanup
            original_unlink = Path.unlink

            def capture_unlink(self_path, *args, **kwargs):
                if "mcp-config" in str(self_path):
                    config_paths.append(self_path)
                return original_unlink(self_path, *args, **kwargs)

            with patch.object(Path, "unlink", capture_unlink):
                await executor.execute("test prompt")

            # The config file should have been cleaned up
            assert len(config_paths) > 0, "MCP config file should have been cleaned up"
            for p in config_paths:
                assert not p.exists(), f"Config file {p} should be deleted"


class TestDefaultMcpPortRemoved:
    """Tests that DEFAULT_MCP_PORT constant is removed.

    PROVES: The SSE port constant has been removed since we no longer
    run an HTTP server. Prevents any code from accidentally referencing it.
    """

    def test_no_default_mcp_port_constant(self):
        """Test DEFAULT_MCP_PORT is no longer exported.

        PROVES: The old constant is fully removed, not just unused.
        """
        import kardbrd_agent.executor as executor_module

        assert not hasattr(executor_module, "DEFAULT_MCP_PORT")
