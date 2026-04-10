"""Claude CLI executor for running Claude as a subprocess."""

import asyncio
import contextlib
import json
import logging
import os
import shutil
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Protocol, runtime_checkable

import yaml

logger = logging.getLogger("kardbrd_agent")


def load_agent_files(cwd: Path | None = None) -> tuple[str, str]:
    """Load optional SOUL.md and RULES.md from the working directory.

    Returns:
        Tuple of (soul_content, rules_content) — empty strings if files don't exist.
    """
    soul = ""
    rules = ""
    if cwd is None:
        return soul, rules
    soul_path = Path(cwd) / "SOUL.md"
    rules_path = Path(cwd) / "RULES.md"
    if soul_path.is_file():
        with contextlib.suppress(OSError):
            soul = soul_path.read_text()
    if rules_path.is_file():
        with contextlib.suppress(OSError):
            rules = rules_path.read_text()
    return soul, rules


def load_knowledge(cwd: Path | None = None) -> str:
    """Load knowledge documents from knowledge/ directory.

    Reads an optional knowledge/index.yaml for metadata (priority, always_load).
    Files marked always_load: true or priority: high are included.
    Falls back to loading all .md files if no index exists.

    Returns:
        Concatenated knowledge content, or empty string.
    """
    if cwd is None:
        return ""
    knowledge_dir = Path(cwd) / "knowledge"
    if not knowledge_dir.is_dir():
        return ""

    index_path = knowledge_dir / "index.yaml"
    documents: list[str] = []

    if index_path.is_file():
        try:
            with open(index_path) as f:
                index = yaml.safe_load(f)
            if isinstance(index, dict) and "documents" in index:
                for doc in index["documents"]:
                    if not isinstance(doc, dict):
                        continue
                    if doc.get("always_load") or doc.get("priority") == "high":
                        doc_path = knowledge_dir / doc.get("path", "")
                        if doc_path.is_file():
                            try:
                                content = doc_path.read_text()
                                title = doc.get("title", doc_path.stem)
                                documents.append(f"### {title}\n\n{content}")
                            except OSError:
                                pass
        except Exception:
            pass
    else:
        # No index — load all .md files
        for md_file in sorted(knowledge_dir.glob("*.md")):
            try:
                content = md_file.read_text()
                documents.append(f"### {md_file.stem}\n\n{content}")
            except OSError:
                pass

    if not documents:
        return ""
    return "## Knowledge\n\n" + "\n\n".join(documents) + "\n\n"


@dataclass
class ExecutorResult:
    """Result from an agent executor."""

    success: bool
    result_text: str
    error: str | None = None
    cost_usd: float | None = None
    duration_ms: int | None = None
    session_id: str | None = None
    returncode: int | None = None
    stderr: str | None = None
    command: list[str] | None = None


# Backwards compatibility alias
ClaudeResult = ExecutorResult


@dataclass
class AuthStatus:
    """Result from checking executor authentication."""

    authenticated: bool
    error: str | None = None
    email: str | None = None
    auth_method: str | None = None
    subscription_type: str | None = None
    auth_hint: str | None = None  # Executor-specific re-auth instructions


@runtime_checkable
class Executor(Protocol):
    """Protocol for agent executors (Claude, Goose, etc.)."""

    async def execute(
        self,
        prompt: str,
        resume_session_id: str | None = None,
        cwd: Path | None = None,
        model: str | None = None,
        on_chunk: Callable[[str, str], Awaitable[None]] | None = None,
    ) -> ExecutorResult: ...

    def build_prompt(
        self,
        card_id: str,
        card_markdown: str,
        command: str,
        comment_content: str,
        author_name: str,
        board_id: str | None = None,
        cwd: str | Path | None = None,
    ) -> str: ...

    def extract_command(self, comment_content: str, mention_keyword: str) -> str: ...

    @staticmethod
    async def check_auth() -> AuthStatus: ...


def build_prompt(
    card_id: str,
    card_markdown: str,
    command: str,
    comment_content: str,
    author_name: str,
    board_id: str | None = None,
    cwd: str | Path | None = None,
) -> str:
    """
    Build the prompt for an executor from card context and user request.

    Args:
        card_id: The id of the card (for posting comments)
        card_markdown: Full card content in markdown format
        command: The extracted command (e.g., "/kp", "/ki", or free-form)
        comment_content: The full comment that triggered the proxy
        author_name: Name of the user who triggered the proxy
        board_id: Optional board ID for label operations
        cwd: Optional working directory for loading SOUL.md, RULES.md, knowledge/

    Returns:
        Formatted prompt string
    """
    # Load agent identity, rules, and knowledge files
    cwd_path = Path(cwd) if cwd else None
    soul, rules = load_agent_files(cwd_path)
    knowledge = load_knowledge(cwd_path)

    agent_preamble = ""
    if soul:
        agent_preamble += f"## Agent Identity\n\n{soul}\n\n"
    if rules:
        agent_preamble += f"## Agent Rules\n\n{rules}\n\n"
    if knowledge:
        agent_preamble += knowledge

    # Common response instructions
    response_instructions = f"""
## IMPORTANT: How to Respond

When you complete this task, you MUST post your response as a comment on the card.
Use the kardbrd CLI via the Bash tool:
```
kardbrd comment add {card_id} "Your response here"
```

For multi-line or markdown responses, use a heredoc:
```
kardbrd comment add {card_id} "$(cat <<'EOF'
Your markdown response here.

@{author_name}
EOF
)"
```

End your comment by mentioning the requester: @{author_name}

DO NOT just output text - you must use the kardbrd CLI to post your response.
"""

    # Label instructions when board_id is available
    label_instructions = ""
    if board_id:
        label_instructions = f"""
## Labels

Cards may have labels (shown as "Labels: ..." in card markdown).
Available CLI commands:
- `kardbrd board labels {board_id}` to discover available labels
- `kardbrd card update {card_id} --label-ids ID1 ID2` to set labels

**Important:** `--label-ids` does a full replace — to add a label, \
first read current labels, then send the full list.
"""

    # CLI reference for kardbrd operations (only when board access is configured)
    cli_instructions = ""
    if board_id:
        cli_instructions = f"""
## kardbrd CLI Reference

The `kardbrd` CLI is available for board operations. Key commands:
- `kardbrd md card {card_id}` — get this card as markdown
- `kardbrd md board {board_id}` — get board as markdown
- `kardbrd comment add {card_id} "message"` — add comment to this card
- `kardbrd card update {card_id} --title "..." --description "..."` — update card
- `kardbrd card create --board {board_id} --list LIST_ID --title "..."` — create card
- `kardbrd card move {card_id} --list LIST_ID` — move card

Environment variables `KARDBRD_TOKEN` and `KARDBRD_API_URL` are pre-configured.
"""

    # Determine if this is a skill command or free-form request
    if command.startswith("/"):
        # Skill command - let Claude Code handle it
        prompt = f"""{agent_preamble}{command}

---

## Context

**Card ID:** {card_id}
**Triggered by:** @{author_name}
**Comment:** {comment_content}

## Card Content

{card_markdown}
{label_instructions}{cli_instructions}{response_instructions}
"""
    else:
        # Free-form request - provide card context and the request
        prompt = f"""{agent_preamble}## Task Request

{comment_content}

---

## Card Context

**Card ID:** {card_id}

{card_markdown}
{label_instructions}{cli_instructions}
---

**Requested by:** @{author_name}

Please complete this request.
{response_instructions}
"""

    return prompt


def extract_command(comment_content: str, mention_keyword: str) -> str:
    """
    Extract the command from the comment content.

    Examples:
        "@coder /kp" -> "/kp"
        "@coder /ke" -> "/ke"
        "@coder fix the login bug" -> "fix the login bug"

    Args:
        comment_content: The full comment text
        mention_keyword: The mention keyword (e.g., "@coder")

    Returns:
        The extracted command (without the mention)
    """
    # Remove the mention and strip whitespace
    content = comment_content.lower()
    mention = mention_keyword.lower()

    # Find the mention and extract what comes after
    idx = content.find(mention)
    if idx == -1:
        return comment_content.strip()

    # Get everything after the mention
    after_mention = comment_content[idx + len(mention) :].strip()

    return after_mention if after_mention else comment_content.strip()


class ClaudeExecutor:
    """
    Executes Claude CLI as a subprocess.

    Spawns `claude -p "..." --output-format=stream-json` and parses
    the streaming JSON output to extract the result.
    """

    @staticmethod
    async def check_auth() -> AuthStatus:
        """
        Check if Claude CLI is authenticated by running `claude auth status`.

        Returns:
            AuthStatus with authentication details or error information.
        """
        claude_bin = shutil.which("claude")
        if not claude_bin:
            return AuthStatus(
                authenticated=False,
                error="Claude CLI not found in PATH",
                auth_hint="Ensure `claude` is in PATH",
            )

        try:
            process = await asyncio.create_subprocess_exec(
                claude_bin,
                "auth",
                "status",
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
            stdout, stderr = await asyncio.wait_for(process.communicate(), timeout=10)

            if process.returncode != 0:
                error_msg = stderr.decode().strip() or stdout.decode().strip()
                return AuthStatus(
                    authenticated=False,
                    error=f"claude auth status exited with code {process.returncode}: {error_msg}",
                    auth_hint=(
                        "Run `claude auth login` on the host, "
                        "or set ANTHROPIC_API_KEY environment variable."
                    ),
                )

            try:
                data = json.loads(stdout.decode())
            except json.JSONDecodeError:
                return AuthStatus(
                    authenticated=False,
                    error=f"Failed to parse auth status output: {stdout.decode()[:200]}",
                    auth_hint=(
                        "Run `claude auth login` on the host, "
                        "or set ANTHROPIC_API_KEY environment variable."
                    ),
                )

            logged_in = data.get("loggedIn", False)
            if not logged_in:
                return AuthStatus(
                    authenticated=False,
                    error="Claude CLI is not logged in",
                    auth_hint=(
                        "Run `claude auth login` on the host, "
                        "or set ANTHROPIC_API_KEY environment variable."
                    ),
                )

            return AuthStatus(
                authenticated=True,
                email=data.get("email"),
                auth_method=data.get("authMethod"),
                subscription_type=data.get("subscriptionType"),
            )
        except TimeoutError:
            return AuthStatus(
                authenticated=False,
                error="claude auth status timed out",
                auth_hint=(
                    "Run `claude auth login` on the host, "
                    "or set ANTHROPIC_API_KEY environment variable."
                ),
            )
        except Exception as e:
            return AuthStatus(authenticated=False, error=str(e))

    # Backwards compatibility alias
    check_claude_auth = check_auth

    def __init__(
        self,
        cwd: str | Path | None = None,
        timeout: int = 3600,  # 1 hour default
        api_url: str | None = None,
        bot_token: str | None = None,
    ):
        """
        Initialize the executor.

        Args:
            cwd: Working directory for Claude (defaults to current directory)
            timeout: Maximum execution time in seconds (default 1 hour)
            api_url: API base URL for kardbrd (passed as KARDBRD_API_URL env var to subprocess)
            bot_token: Bot authentication token (passed as KARDBRD_TOKEN env var to subprocess)
        """
        self.cwd = Path(cwd) if cwd else Path.cwd()
        self.timeout = timeout
        self.api_url = api_url
        self.bot_token = bot_token

    async def execute(
        self,
        prompt: str,
        resume_session_id: str | None = None,
        cwd: Path | None = None,
        model: str | None = None,
        on_chunk: Callable[[str, str], Awaitable[None]] | None = None,
    ) -> ExecutorResult:
        """
        Execute Claude CLI with the given prompt.

        Args:
            prompt: The prompt to send to Claude
            resume_session_id: Optional session ID to resume a previous conversation
            cwd: Optional working directory override (uses default if not specified)
            model: Optional Claude model ID (e.g. "claude-haiku-4-5-20251001")
            on_chunk: Optional async callback for streaming output chunks.
                Called with (content: str, chunk_type: str) where chunk_type
                is "assistant" or "tool_use".

        Returns:
            ExecutorResult with the execution outcome
        """
        # Use passed cwd or default
        working_dir = cwd or self.cwd

        claude_bin = shutil.which("claude")
        if not claude_bin:
            return ExecutorResult(
                success=False,
                result_text="",
                error="Claude CLI not found in PATH",
            )

        cmd = [
            claude_bin,
            "-p",
            "-",
            "--output-format=stream-json",
            "--verbose",
            "--dangerously-skip-permissions",
        ]

        # Add model flag if specified
        if model:
            cmd.extend(["--model", model])

        # Add resume flag if resuming a session
        if resume_session_id:
            cmd.extend(["--resume", resume_session_id])

        # Set env vars for kardbrd CLI access
        env = os.environ.copy()
        if self.api_url and self.bot_token:
            env["KARDBRD_TOKEN"] = self.bot_token
            env["KARDBRD_API_URL"] = self.api_url

        logger.info(f"Spawning Claude in {working_dir}")
        logger.debug(f"Prompt length: {len(prompt)} chars")

        try:
            process = await asyncio.create_subprocess_exec(
                *cmd,
                stdin=asyncio.subprocess.PIPE,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                cwd=working_dir,
                env=env,
                limit=10 * 1024 * 1024,  # 10 MiB — stream-json lines can exceed default 64 KB
            )

            # Collect output with timeout — pipe prompt via stdin to avoid ARG_MAX
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
                    error=f"Claude execution timed out after {self.timeout}s",
                )

            # Parse the stream-json output
            return self._parse_output(stdout.decode(), stderr.decode(), process.returncode, cmd=cmd)

        except FileNotFoundError:
            return ExecutorResult(
                success=False,
                result_text="",
                error=f"Working directory not found: {working_dir}",
            )
        except Exception as e:
            logger.exception("Error executing Claude")
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

        Writes the prompt to stdin, then reads stdout line-by-line.  For each
        JSON line that represents an assistant message or tool_use event, the
        ``on_chunk`` callback is invoked.

        Returns:
            Tuple of (stdout_bytes, stderr_bytes) matching ``process.communicate()``
            signature.
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
                if msg_type == "assistant":
                    await on_chunk(parsed.get("content", ""), "assistant")
                elif msg_type == "tool_use":
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
        Parse Claude's stream-json output.

        The output consists of newline-delimited JSON objects.
        We look for the 'result' type message which contains the final output.
        """
        result_text = ""
        cost_usd = None
        duration_ms = None
        session_id = None
        error = None

        # Parse each line as JSON
        for line in stdout.strip().split("\n"):
            if not line:
                continue

            try:
                data = json.loads(line)
                msg_type = data.get("type")

                if msg_type == "result":
                    # Final result message
                    result_text = data.get("result", "")
                    cost_usd = data.get("cost_usd")
                    duration_ms = data.get("duration_ms")
                    session_id = data.get("session_id")

                elif msg_type == "assistant":
                    # Assistant message during streaming
                    # Could be used for progress updates in the future
                    pass

                elif msg_type == "error":
                    error = data.get("error", {}).get("message", "Unknown error")

            except json.JSONDecodeError:
                # Non-JSON line, might be debug output
                logger.debug(f"Non-JSON output: {line[:100]}")
                continue

        # Check return code
        if returncode != 0 and not error:
            error = f"Claude exited with code {returncode}"
            if stderr:
                error += f": {stderr[:500]}"

        return ExecutorResult(
            success=returncode == 0 and error is None,
            result_text=result_text,
            error=error,
            cost_usd=cost_usd,
            duration_ms=duration_ms,
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
