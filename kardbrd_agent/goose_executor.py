"""Goose CLI executor for running Goose as a subprocess."""

import asyncio
import json
import logging
import os
from pathlib import Path

from .executor import AuthStatus, ExecutorResult, build_prompt, extract_command
from .rules import MODEL_MAP

logger = logging.getLogger("kardbrd_agent")

# Map of LLM provider names to their expected API key env vars
PROVIDER_KEY_MAP = {
    "anthropic": "ANTHROPIC_API_KEY",
    "openai": "OPENAI_API_KEY",
    "google": "GOOGLE_API_KEY",
    "groq": "GROQ_API_KEY",
    "openrouter": "OPENROUTER_API_KEY",
    "databricks": "DATABRICKS_TOKEN",
}

# Providers that run locally and don't need an API key
LOCAL_PROVIDERS = {"ollama"}

# Backwards-compatible alias — single source of truth is rules.MODEL_MAP
GOOSE_MODEL_MAP = MODEL_MAP


class GooseExecutor:
    """
    Executes Goose CLI as a subprocess.

    Spawns `goose run -t "..." --output-format stream-json` and parses
    the streaming JSON output to extract the result.
    """

    @staticmethod
    async def check_auth() -> AuthStatus:
        """
        Check if Goose CLI is installed and provider is configured.

        Validates:
        1. goose binary exists (goose version)
        2. GOOSE_PROVIDER env var is set
        3. Provider-specific API key exists in env (best-effort)

        Returns:
            AuthStatus with authentication details or error information.
        """
        # 1. Check goose binary exists
        try:
            process = await asyncio.create_subprocess_exec(
                "goose",
                "version",
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
            stdout, stderr = await asyncio.wait_for(process.communicate(), timeout=10)
            if process.returncode != 0:
                error_msg = stderr.decode().strip() or stdout.decode().strip()
                return AuthStatus(
                    authenticated=False,
                    error=f"goose version failed: {error_msg}",
                    auth_hint=(
                        "Install Goose: "
                        "curl -fsSL https://github.com/block/goose/releases/latest/"
                        "download/install.sh | sh"
                    ),
                )
        except FileNotFoundError:
            return AuthStatus(
                authenticated=False,
                error="Goose CLI not found",
                auth_hint=(
                    "Install Goose: "
                    "curl -fsSL https://github.com/block/goose/releases/latest/"
                    "download/install.sh | sh"
                ),
            )
        except TimeoutError:
            return AuthStatus(
                authenticated=False,
                error="goose version timed out",
                auth_hint="Check Goose installation.",
            )

        # 2. Check GOOSE_PROVIDER is set
        provider = os.environ.get("GOOSE_PROVIDER")
        if not provider:
            return AuthStatus(
                authenticated=False,
                error="GOOSE_PROVIDER env var not set",
                auth_hint=(
                    "Set GOOSE_PROVIDER to your LLM provider "
                    "(e.g. anthropic, openai, ollama). "
                    "Run `goose configure` for interactive setup."
                ),
            )

        # 3. Check provider-specific API key
        if provider.lower() in LOCAL_PROVIDERS:
            return AuthStatus(
                authenticated=True,
                auth_method=f"goose/{provider}",
            )

        expected_key = PROVIDER_KEY_MAP.get(provider.lower())
        if expected_key and not os.environ.get(expected_key):
            return AuthStatus(
                authenticated=False,
                error=f"Missing {expected_key} environment variable for provider '{provider}'",
                auth_hint=(
                    f"Set {expected_key} env var, or run `goose configure` "
                    f"to store credentials in the system keychain.\n\n"
                    f"For headless/server deployments, env vars are required."
                ),
            )

        return AuthStatus(
            authenticated=True,
            auth_method=f"goose/{provider}",
        )

    def __init__(
        self,
        cwd: str | Path | None = None,
        timeout: int = 3600,
        api_url: str | None = None,
        bot_token: str | None = None,
    ):
        """
        Initialize the Goose executor.

        Args:
            cwd: Working directory for Goose (defaults to current directory)
            timeout: Maximum execution time in seconds (default 1 hour)
            api_url: API base URL for kardbrd-mcp
            bot_token: Bot authentication token for kardbrd-mcp
        """
        self.cwd = Path(cwd) if cwd else Path.cwd()
        self.timeout = timeout
        self.api_url = api_url
        self.bot_token = bot_token

    def _resolve_model(self, model: str | None) -> tuple[str | None, str | None]:
        """
        Resolve a model string to provider and model name for Goose.

        Args:
            model: Model string (short name like "opus", or "provider/model" format)

        Returns:
            Tuple of (provider, model_name) — either may be None
        """
        if model is None:
            return None, None

        # Check short name map first
        resolved = GOOSE_MODEL_MAP.get(model.lower())
        if resolved:
            return None, resolved

        # Check for provider/model format
        if "/" in model:
            parts = model.split("/", 1)
            return parts[0], parts[1]

        # Pass through as-is
        return None, model

    async def execute(
        self,
        prompt: str,
        resume_session_id: str | None = None,
        cwd: Path | None = None,
        model: str | None = None,
    ) -> ExecutorResult:
        """
        Execute Goose CLI with the given prompt.

        Args:
            prompt: The prompt to send to Goose
            resume_session_id: Optional session name to resume
            cwd: Optional working directory override
            model: Optional model specification

        Returns:
            ExecutorResult with the execution outcome
        """
        working_dir = cwd or self.cwd

        cmd = [
            "goose",
            "run",
            "-t",
            "-",
            "--output-format",
            "stream-json",
            "--no-session",
        ]

        # Set env vars for headless mode
        env = os.environ.copy()
        env["GOOSE_MODE"] = "auto"
        env["GOOSE_DISABLE_SESSION_NAMING"] = "true"

        # Add model flags if specified
        provider, model_name = self._resolve_model(model)
        if provider:
            cmd.extend(["--provider", provider])
        if model_name:
            cmd.extend(["--model", model_name])

        # Add resume if session name provided
        if resume_session_id:
            # Replace --no-session with resume flags
            cmd = [c for c in cmd if c != "--no-session"]
            cmd.extend(["-r", "-n", resume_session_id])

        # Add MCP extension for kardbrd if credentials are set
        # Pass bot_token via env var (not CLI args) to avoid exposure in ps/proc
        if self.api_url and self.bot_token:
            extension_cmd = f"kardbrd-mcp --api-url {self.api_url}"
            cmd.extend(["--with-extension", extension_cmd])
            env["KARDBRD_TOKEN"] = self.bot_token

        logger.info(f"Spawning Goose in {working_dir}")
        logger.debug(f"Prompt length: {len(prompt)} chars")

        try:
            process = await asyncio.create_subprocess_exec(
                *cmd,
                stdin=asyncio.subprocess.PIPE,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                cwd=working_dir,
                env=env,
            )

            # Pipe prompt via stdin to avoid ARG_MAX limit
            try:
                stdout, stderr = await asyncio.wait_for(
                    process.communicate(input=prompt.encode()),
                    timeout=self.timeout,
                )
            except TimeoutError:
                # Graceful shutdown: SIGTERM first, then SIGKILL after 5s
                process.terminate()
                try:
                    await asyncio.wait_for(process.wait(), timeout=5)
                except TimeoutError:
                    process.kill()
                    await process.wait()
                return ExecutorResult(
                    success=False,
                    result_text="",
                    error=f"Goose execution timed out after {self.timeout}s",
                )

            return self._parse_output(stdout.decode(), stderr.decode(), process.returncode)

        except FileNotFoundError:
            return ExecutorResult(
                success=False,
                result_text="",
                error=(
                    "Goose CLI not found. Install: "
                    "curl -fsSL https://github.com/block/goose/releases/latest/"
                    "download/install.sh | sh"
                ),
            )
        except Exception as e:
            logger.exception("Error executing Goose")
            return ExecutorResult(
                success=False,
                result_text="",
                error=str(e),
            )

    def _parse_output(self, stdout: str, stderr: str, returncode: int | None) -> ExecutorResult:
        """
        Parse Goose's stream-json output.

        Goose emits different event types than Claude:
        - AgentMessageChunk: streaming text from agent
        - ToolCall / ToolCallUpdate: tool invocations
        We aggregate AgentMessageChunk content for the result.
        """
        result_text = ""
        error = None

        for line in stdout.strip().split("\n"):
            if not line:
                continue

            try:
                data = json.loads(line)
                msg_type = data.get("type")

                if msg_type == "AgentMessageChunk":
                    result_text += data.get("content", "")

                elif msg_type == "ToolCallUpdate":
                    status = data.get("status")
                    if status == "failed":
                        tool_error = data.get("result", "Tool call failed")
                        logger.warning(f"Goose tool call failed: {tool_error}")

                elif msg_type == "error":
                    error = data.get("message", data.get("error", "Unknown error"))

            except json.JSONDecodeError:
                logger.debug(f"Non-JSON output: {line[:100]}")
                continue

        if returncode != 0 and not error:
            error = f"Goose exited with code {returncode}"
            if stderr:
                error += f": {stderr[:500]}"

        return ExecutorResult(
            success=returncode == 0 and error is None,
            result_text=result_text.strip(),
            error=error,
        )

    def build_prompt(
        self,
        card_id: str,
        card_markdown: str,
        command: str,
        comment_content: str,
        author_name: str,
        board_id: str | None = None,
    ) -> str:
        """Delegate to module-level build_prompt()."""
        return build_prompt(
            card_id=card_id,
            card_markdown=card_markdown,
            command=command,
            comment_content=comment_content,
            author_name=author_name,
            board_id=board_id,
        )

    def extract_command(self, comment_content: str, mention_keyword: str) -> str:
        """Delegate to module-level extract_command()."""
        return extract_command(comment_content, mention_keyword)
