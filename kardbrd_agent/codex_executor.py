"""Codex CLI executor for running OpenAI Codex as a subprocess."""

import asyncio
import json
import logging
import os
from collections.abc import Awaitable, Callable
from pathlib import Path

from .executor import AuthStatus, ExecutorResult, build_prompt, extract_command

logger = logging.getLogger("kardbrd_agent")

# Map of Codex-specific model short names
CODEX_MODEL_MAP = {
    "gpt-5.4": "gpt-5.4",
    "gpt-5.4-mini": "gpt-5.4-mini",
    "gpt-5.3-codex": "gpt-5.3-codex",
    "gpt-5.3-codex-spark": "gpt-5.3-codex-spark",
    "gpt-5.2": "gpt-5.2",
}


class CodexExecutor:
    """
    Executes Codex CLI as a subprocess.

    Spawns `codex exec --dangerously-bypass-approvals-and-sandbox --json` and parses
    the JSONL output to extract the result.
    """

    @staticmethod
    async def check_auth() -> AuthStatus:
        """
        Check if Codex CLI is installed and authenticated.

        Uses `codex login status` which validates both subscription-based
        auth (via `codex login`) and API key auth (`CODEX_API_KEY`).

        Returns:
            AuthStatus with authentication details or error information.
        """
        # 1. Check codex binary exists
        try:
            process = await asyncio.create_subprocess_exec(
                "codex",
                "--version",
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
            stdout, stderr = await asyncio.wait_for(process.communicate(), timeout=10)
            if process.returncode != 0:
                error_msg = stderr.decode().strip() or stdout.decode().strip()
                return AuthStatus(
                    authenticated=False,
                    error=f"codex --version failed: {error_msg}",
                    auth_hint=("Install Codex CLI: npm install -g @openai/codex"),
                )
        except FileNotFoundError:
            return AuthStatus(
                authenticated=False,
                error="Codex CLI not found",
                auth_hint=("Install Codex CLI: npm install -g @openai/codex"),
            )
        except TimeoutError:
            return AuthStatus(
                authenticated=False,
                error="codex --version timed out",
                auth_hint="Check Codex installation.",
            )

        # 2. Check codex login status (covers both subscription and CODEX_API_KEY)
        try:
            process = await asyncio.create_subprocess_exec(
                "codex",
                "login",
                "status",
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
            stdout, stderr = await asyncio.wait_for(process.communicate(), timeout=10)
            if process.returncode != 0:
                return AuthStatus(
                    authenticated=False,
                    error="Codex authentication check failed",
                    auth_hint=(
                        "Run 'codex login' for subscription access, "
                        "or set CODEX_API_KEY environment variable."
                    ),
                )
        except TimeoutError:
            return AuthStatus(
                authenticated=False,
                error="codex login status timed out",
                auth_hint="Check Codex installation and network connectivity.",
            )

        return AuthStatus(
            authenticated=True,
            auth_method="codex",
        )

    def __init__(
        self,
        cwd: str | Path | None = None,
        timeout: int = 3600,
        api_url: str | None = None,
        bot_token: str | None = None,
    ):
        """
        Initialize the Codex executor.

        Args:
            cwd: Working directory for Codex (defaults to current directory)
            timeout: Maximum execution time in seconds (default 1 hour)
            api_url: API base URL for kardbrd-mcp
            bot_token: Bot authentication token for kardbrd-mcp
        """
        self.cwd = Path(cwd) if cwd else Path.cwd()
        self.timeout = timeout
        self.api_url = api_url
        self.bot_token = bot_token

    def _resolve_model(self, model: str | None) -> str | None:
        """
        Resolve a model string to a Codex model name.

        Args:
            model: Model string (short name like "o3", or full model name)

        Returns:
            Resolved model name, or None if no model specified.
        """
        if model is None:
            return None

        # Check Codex-specific short names first
        resolved = CODEX_MODEL_MAP.get(model.lower())
        if resolved:
            return resolved

        # Pass through as-is (including unknown names)
        return model

    async def execute(
        self,
        prompt: str,
        resume_session_id: str | None = None,
        cwd: Path | None = None,
        model: str | None = None,
        on_chunk: Callable[[str, str], Awaitable[None]] | None = None,
    ) -> ExecutorResult:
        """
        Execute Codex CLI with the given prompt.

        Args:
            prompt: The prompt to send to Codex
            resume_session_id: Ignored — Codex exec doesn't support session resumption
            cwd: Optional working directory override
            model: Optional model specification
            on_chunk: Optional async callback for streaming output chunks.
                Called with (content: str, chunk_type: str) where chunk_type
                is "assistant" or "tool_use".

        Returns:
            ExecutorResult with the execution outcome
        """
        working_dir = cwd or self.cwd

        cmd = [
            "codex",
            "exec",
            "--dangerously-bypass-approvals-and-sandbox",
            "--json",
        ]

        # Add model flag if specified
        resolved_model = self._resolve_model(model)
        if resolved_model:
            cmd.extend(["--model", resolved_model])

        # Set env vars for kardbrd CLI access
        env = os.environ.copy()
        if self.api_url and self.bot_token:
            env["KARDBRD_TOKEN"] = self.bot_token
            env["KARDBRD_API_URL"] = self.api_url

        logger.info(f"Spawning Codex in {working_dir}")
        logger.debug(f"Prompt length: {len(prompt)} chars")

        try:
            process = await asyncio.create_subprocess_exec(
                *cmd,
                stdin=asyncio.subprocess.PIPE,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                cwd=working_dir,
                env=env,
                limit=10 * 1024 * 1024,  # 10 MiB
            )

            # Pipe prompt via stdin
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
                    error=f"Codex execution timed out after {self.timeout}s",
                )

            return self._parse_output(stdout.decode(), stderr.decode(), process.returncode, cmd=cmd)

        except FileNotFoundError:
            return ExecutorResult(
                success=False,
                result_text="",
                error="Codex CLI not found. Install: npm install -g @openai/codex",
            )
        except Exception as e:
            logger.exception("Error executing Codex")
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

        Codex emits JSONL events. We look for ``item.*`` events containing
        agent messages and tool calls.

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
                event_type = parsed.get("type", "")
                if "message" in event_type:
                    # Extract text content from message items
                    content = parsed.get("content", "")
                    if isinstance(content, list):
                        for part in content:
                            if isinstance(part, dict) and part.get("type") == "text":
                                await on_chunk(part.get("text", ""), "assistant")
                    elif isinstance(content, str) and content:
                        await on_chunk(content, "assistant")
                elif "tool" in event_type or "function" in event_type:
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
        Parse Codex's JSONL output.

        Codex emits JSONL events including item.* events for agent messages.
        We aggregate text content for the result.
        """
        result_text = ""
        error = None

        for line in stdout.strip().split("\n"):
            if not line:
                continue

            try:
                data = json.loads(line)
                event_type = data.get("type", "")

                # Aggregate text from message-related events
                if "message" in event_type:
                    content = data.get("content", "")
                    if isinstance(content, list):
                        for part in content:
                            if isinstance(part, dict) and part.get("type") == "text":
                                result_text += part.get("text", "")
                    elif isinstance(content, str):
                        result_text += content

                elif event_type == "error":
                    error = data.get("message", data.get("error", "Unknown error"))

            except json.JSONDecodeError:
                logger.debug(f"Non-JSON output: {line[:100]}")
                continue

        if returncode != 0 and not error:
            error = f"Codex exited with code {returncode}"
            if stderr:
                error += f": {stderr[:500]}"

        return ExecutorResult(
            success=returncode == 0 and error is None,
            result_text=result_text.strip(),
            error=error,
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
