"""Pi coding agent executor for running Pi as a subprocess."""

import asyncio
import json
import logging
import os
import shutil
from collections.abc import Awaitable, Callable
from pathlib import Path

from .executor import AuthStatus, ExecutorResult, build_prompt, extract_command
from .rules import MODEL_MAP

logger = logging.getLogger("kardbrd_agent")

# Map of LLM provider names to their expected API key env vars
PI_PROVIDER_KEY_MAP = {
    "anthropic": "ANTHROPIC_API_KEY",
    "openai": "OPENAI_API_KEY",
    "google": "GEMINI_API_KEY",
    "deepseek": "DEEPSEEK_API_KEY",
    "groq": "GROQ_API_KEY",
    "mistral": "MISTRAL_API_KEY",
    "xai": "XAI_API_KEY",
    "openrouter": "OPENROUTER_API_KEY",
    "fireworks": "FIREWORKS_API_KEY",
    "together": "TOGETHER_API_KEY",
    "cerebras": "CEREBRAS_API_KEY",
    "huggingface": "HF_TOKEN",
}

# Backwards-compatible alias — single source of truth is rules.MODEL_MAP
PI_MODEL_MAP = MODEL_MAP


class PiExecutor:
    """
    Executes Pi coding agent as a subprocess.

    Spawns `pi --mode json -p - --no-session -a` and parses
    the streaming JSON output to extract the result.
    """

    @staticmethod
    async def check_auth() -> AuthStatus:
        """
        Check if Pi CLI is installed and provider is configured.

        Validates:
        1. pi binary exists (pi --version)
        2. PI_PROVIDER env var is set
        3. Provider-specific API key exists in env (best-effort)

        Returns:
            AuthStatus with authentication details or error information.
        """
        # 1. Check pi binary exists
        pi_bin = shutil.which("pi")
        if not pi_bin:
            return AuthStatus(
                authenticated=False,
                error="Pi CLI not found in PATH",
                auth_hint=("Install Pi: curl -fsSL https://pi.dev/install.sh | sh"),
            )

        try:
            process = await asyncio.create_subprocess_exec(
                pi_bin,
                "--version",
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
            stdout, stderr = await asyncio.wait_for(process.communicate(), timeout=10)
            if process.returncode != 0:
                error_msg = stderr.decode().strip() or stdout.decode().strip()
                return AuthStatus(
                    authenticated=False,
                    error=f"pi --version failed: {error_msg}",
                    auth_hint=("Install Pi: curl -fsSL https://pi.dev/install.sh | sh"),
                )
        except TimeoutError:
            return AuthStatus(
                authenticated=False,
                error="pi --version timed out",
                auth_hint="Check Pi installation.",
            )

        # 2. Check PI_PROVIDER is set
        provider = os.environ.get("PI_PROVIDER")
        if not provider:
            return AuthStatus(
                authenticated=False,
                error="PI_PROVIDER env var not set",
                auth_hint=(
                    "Set PI_PROVIDER to your LLM provider "
                    "(e.g. anthropic, openai, google). "
                    "Or set the provider API key directly "
                    "(e.g. ANTHROPIC_API_KEY)."
                ),
            )

        # 3. Check provider-specific API key
        expected_key = PI_PROVIDER_KEY_MAP.get(provider.lower())
        if expected_key and not os.environ.get(expected_key):
            return AuthStatus(
                authenticated=False,
                error=f"Missing {expected_key} environment variable for provider '{provider}'",
                auth_hint=(
                    f"Set {expected_key} env var for the '{provider}' provider.\n\n"
                    f"For headless/server deployments, env vars are required."
                ),
            )

        return AuthStatus(
            authenticated=True,
            auth_method=f"pi/{provider}",
        )

    def __init__(
        self,
        cwd: str | Path | None = None,
        timeout: int = 3600,
        api_url: str | None = None,
        bot_token: str | None = None,
    ):
        """
        Initialize the Pi executor.

        Args:
            cwd: Working directory for Pi (defaults to current directory)
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
        Resolve a model string to provider and model name for Pi.

        Args:
            model: Model string (short name like "opus", or "provider/model" format)

        Returns:
            Tuple of (provider, model_name) — either may be None
        """
        if model is None:
            return None, None

        # Check short name map first
        resolved = PI_MODEL_MAP.get(model.lower())
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
        on_chunk: Callable[[str, str], Awaitable[None]] | None = None,
    ) -> ExecutorResult:
        """
        Execute Pi CLI with the given prompt.

        Args:
            prompt: The prompt to send to Pi
            resume_session_id: Optional session ID to resume
            cwd: Optional working directory override
            model: Optional model specification
            on_chunk: Optional async callback for streaming output chunks.
                Called with (content: str, chunk_type: str) where chunk_type
                is "assistant" or "tool_use".

        Returns:
            ExecutorResult with the execution outcome
        """
        working_dir = cwd or self.cwd

        pi_bin = shutil.which("pi")
        if not pi_bin:
            return ExecutorResult(
                success=False,
                result_text="",
                error="Pi CLI not found in PATH",
            )

        cmd = [
            pi_bin,
            "--mode",
            "json",
            "-p",
            "-",
            "--no-session",
            "-a",
        ]

        # Add model flags if specified
        provider, model_name = self._resolve_model(model)
        if provider:
            cmd.extend(["--provider", provider])
        if model_name:
            cmd.extend(["--model", model_name])

        # Add resume if session ID provided
        if resume_session_id:
            # Replace --no-session with --session <id>
            cmd = [c for c in cmd if c != "--no-session"]
            cmd.extend(["--session", resume_session_id])

        # Set env vars for kardbrd CLI access
        env = os.environ.copy()
        if self.api_url and self.bot_token:
            env["KARDBRD_TOKEN"] = self.bot_token
            env["KARDBRD_API_URL"] = self.api_url

        logger.info(f"Spawning Pi in {working_dir}")
        logger.debug(f"Prompt length: {len(prompt)} chars")

        try:
            process = await asyncio.create_subprocess_exec(
                *cmd,
                stdin=asyncio.subprocess.PIPE,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                cwd=working_dir,
                env=env,
                limit=10 * 1024 * 1024,  # 10 MiB — JSON lines can exceed default 64 KB
            )

            # Pipe prompt via stdin to avoid ARG_MAX limit
            try:
                if on_chunk:
                    stdout, stderr = await asyncio.wait_for(
                        self._read_with_chunks(process, prompt, on_chunk),
                        timeout=self.timeout,
                    )
                else:
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
                    error=f"Pi execution timed out after {self.timeout}s",
                )

            return self._parse_output(stdout.decode(), stderr.decode(), process.returncode, cmd=cmd)

        except FileNotFoundError:
            return ExecutorResult(
                success=False,
                result_text="",
                error=f"Working directory not found: {working_dir}",
            )
        except Exception as e:
            logger.exception("Error executing Pi")
            return ExecutorResult(
                success=False,
                result_text="",
                error=str(e),
            )

    @staticmethod
    async def _read_with_chunks(
        process: asyncio.subprocess.Process,
        prompt: str,
        on_chunk: Callable[[str, str], Awaitable[None]],
    ) -> tuple[bytes, bytes]:
        """Read subprocess output line-by-line, forwarding chunks via callback.

        Pi emits JSON events line-by-line. ``message_update`` events contain
        assistant text; ``tool_execution_start``/``tool_execution_update``
        events contain tool invocations.

        Returns:
            Tuple of (stdout_bytes, stderr_bytes).
        """
        process.stdin.write(prompt.encode())
        await process.stdin.drain()
        process.stdin.close()

        lines: list[bytes] = []
        async for line in process.stdout:
            lines.append(line)
            try:
                parsed = json.loads(line)
                msg_type = parsed.get("type")
                if msg_type == "message_update":
                    event = parsed.get("assistantMessageEvent", {})
                    if isinstance(event, dict):
                        text = event.get("text", "")
                        if text:
                            await on_chunk(text, "assistant")
                elif msg_type in ("tool_execution_start", "tool_execution_update"):
                    await on_chunk(json.dumps(parsed), "tool_use")
            except (json.JSONDecodeError, Exception):
                pass

        stderr_data = await process.stderr.read()
        await process.wait()
        return b"".join(lines), stderr_data

    def _parse_output(
        self,
        stdout: str,
        stderr: str,
        returncode: int | None,
        cmd: list[str] | None = None,
    ) -> ExecutorResult:
        """
        Parse Pi's JSON event stream output.

        Pi emits different event types:
        - session: first line with session ID
        - message_end: contains complete assistant message
        - tool_execution_end: tool results (may include errors)
        We aggregate text from message_end events for the result.
        """
        result_text = ""
        session_id = None
        error = None

        for line in stdout.strip().split("\n"):
            if not line:
                continue

            try:
                data = json.loads(line)
                msg_type = data.get("type")

                if msg_type == "session":
                    session_id = data.get("id")

                elif msg_type == "message_end":
                    message = data.get("message", {})
                    if isinstance(message, dict):
                        content = message.get("content", "")
                        if isinstance(content, str) and content:
                            result_text += content
                    elif isinstance(message, str) and message:
                        result_text += message

                elif msg_type == "tool_execution_end":
                    if data.get("isError"):
                        tool_name = data.get("toolName", "unknown")
                        tool_error = data.get("result", "Tool execution failed")
                        logger.warning(f"Pi tool error ({tool_name}): {tool_error}")

                elif msg_type == "error":
                    error = data.get("message", data.get("error", "Unknown error"))

            except json.JSONDecodeError:
                logger.debug(f"Non-JSON output: {line[:100]}")
                continue

        if returncode != 0 and not error:
            error = f"Pi exited with code {returncode}"
            if stderr:
                error += f": {stderr[:500]}"

        return ExecutorResult(
            success=returncode == 0 and error is None,
            result_text=result_text.strip(),
            error=error,
            session_id=session_id,
            returncode=returncode,
            stderr=stderr if stderr else None,
            command=cmd,
        )

    def build_prompt(
        self,
        card_id: str,
        card_markdown: str,
        command: str,
        comment_content: str,
        author_name: str,
        board_id: str | None = None,
        cwd: str | Path | None = None,
    ) -> str:
        """Delegate to module-level build_prompt()."""
        return build_prompt(
            card_id=card_id,
            card_markdown=card_markdown,
            command=command,
            comment_content=comment_content,
            author_name=author_name,
            board_id=board_id,
            cwd=cwd,
        )

    def extract_command(self, comment_content: str, mention_keyword: str) -> str:
        """Delegate to module-level extract_command()."""
        return extract_command(comment_content, mention_keyword)
