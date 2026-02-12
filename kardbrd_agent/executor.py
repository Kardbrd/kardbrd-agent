"""Claude CLI executor for running Claude as a subprocess."""

import asyncio
import json
import logging
import tempfile
from dataclasses import dataclass
from pathlib import Path

logger = logging.getLogger("kardbrd_agent")


def create_mcp_config(api_url: str, bot_token: str) -> Path:
    """
    Create a temporary MCP config file for Claude Code.

    The config tells Claude to spawn kardbrd-mcp as a stdio subprocess
    with the bot's credentials.

    Args:
        api_url: The kardbrd API base URL
        bot_token: The bot's authentication token

    Returns:
        Path to the temporary config file
    """
    config = {
        "mcpServers": {
            "kardbrd": {
                "command": "kardbrd-mcp",
                "args": [
                    "--api-url",
                    api_url,
                    "--token",
                    bot_token,
                ],
            }
        }
    }

    # Create temp file (will be cleaned up after Claude exits)
    fd, path = tempfile.mkstemp(suffix=".json", prefix="mcp-config-")
    with open(fd, "w") as f:
        json.dump(config, f, indent=2)

    logger.debug(f"Created MCP config at {path}")
    return Path(path)


@dataclass
class ClaudeResult:
    """Result from a Claude CLI execution."""

    success: bool
    result_text: str
    error: str | None = None
    cost_usd: float | None = None
    duration_ms: int | None = None
    session_id: str | None = None


class ClaudeExecutor:
    """
    Executes Claude CLI as a subprocess.

    Spawns `claude -p "..." --output-format=stream-json` and parses
    the streaming JSON output to extract the result.
    """

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
            api_url: API base URL for kardbrd-mcp (if set with bot_token, enables --mcp-config)
            bot_token: Bot authentication token for kardbrd-mcp
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
    ) -> ClaudeResult:
        """
        Execute Claude CLI with the given prompt.

        Args:
            prompt: The prompt to send to Claude
            resume_session_id: Optional session ID to resume a previous conversation
            cwd: Optional working directory override (uses default if not specified)

        Returns:
            ClaudeResult with the execution outcome
        """
        # Use passed cwd or default
        working_dir = cwd or self.cwd

        # Create MCP config if credentials are set
        mcp_config_path: Path | None = None
        if self.api_url and self.bot_token:
            mcp_config_path = create_mcp_config(self.api_url, self.bot_token)

        cmd = [
            "claude",
            "-p",
            prompt,
            "--output-format=stream-json",
            "--verbose",
            "--dangerously-skip-permissions",
        ]

        # Add resume flag if resuming a session
        if resume_session_id:
            cmd.extend(["--resume", resume_session_id])

        # Add MCP config if created
        if mcp_config_path:
            cmd.extend(["--mcp-config", str(mcp_config_path)])

        logger.info(f"Spawning Claude in {working_dir}")
        logger.debug(f"Prompt length: {len(prompt)} chars")
        if mcp_config_path:
            logger.debug(f"MCP config: {mcp_config_path}")

        try:
            process = await asyncio.create_subprocess_exec(
                *cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                cwd=working_dir,
            )

            # Collect output with timeout
            try:
                stdout, stderr = await asyncio.wait_for(
                    process.communicate(),
                    timeout=self.timeout,
                )
            except TimeoutError:
                process.kill()
                await process.wait()
                return ClaudeResult(
                    success=False,
                    result_text="",
                    error=f"Claude execution timed out after {self.timeout}s",
                )

            # Parse the stream-json output
            return self._parse_output(stdout.decode(), stderr.decode(), process.returncode)

        except FileNotFoundError:
            return ClaudeResult(
                success=False,
                result_text="",
                error=(
                    "Claude CLI not found. Please install it with: "
                    "npm install -g @anthropic-ai/claude-code"
                ),
            )
        except Exception as e:
            logger.exception("Error executing Claude")
            return ClaudeResult(
                success=False,
                result_text="",
                error=str(e),
            )
        finally:
            # Clean up temp MCP config file
            if mcp_config_path and mcp_config_path.exists():
                try:
                    mcp_config_path.unlink()
                    logger.debug(f"Cleaned up MCP config: {mcp_config_path}")
                except OSError:
                    pass

    def _parse_output(self, stdout: str, stderr: str, returncode: int | None) -> ClaudeResult:
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

        return ClaudeResult(
            success=returncode == 0 and error is None,
            result_text=result_text,
            error=error,
            cost_usd=cost_usd,
            duration_ms=duration_ms,
            session_id=session_id,
        )

    def build_prompt(
        self,
        card_id: str,
        card_markdown: str,
        command: str,
        comment_content: str,
        author_name: str,
    ) -> str:
        """
        Build the prompt for Claude from card context and user request.

        Args:
            card_id: The public_id of the card (for posting comments)
            card_markdown: Full card content in markdown format
            command: The extracted command (e.g., "/kp", "/ki", or free-form)
            comment_content: The full comment that triggered the proxy
            author_name: Name of the user who triggered the proxy

        Returns:
            Formatted prompt string
        """
        # Common response instructions
        response_instructions = f"""
## IMPORTANT: How to Respond

When you complete this task, you MUST post your response as a comment on the card.
Use the `mcp__kardbrd__add_comment` tool with:
- card_id: "{card_id}"
- content: Your response (markdown supported)

End your comment by mentioning the requester: @{author_name}

DO NOT just output text - you must use the add_comment tool to post your response.
"""

        # Determine if this is a skill command or free-form request
        if command.startswith("/"):
            # Skill command - let Claude Code handle it
            prompt = f"""{command}

---

## Context

**Card ID:** {card_id}
**Triggered by:** @{author_name}
**Comment:** {comment_content}

## Card Content

{card_markdown}
{response_instructions}
"""
        else:
            # Free-form request - provide card context and the request
            prompt = f"""## Task Request

{comment_content}

---

## Card Context

**Card ID:** {card_id}

{card_markdown}

---

**Requested by:** @{author_name}

Please complete this request.
{response_instructions}
"""

        return prompt

    def extract_command(self, comment_content: str, mention_keyword: str) -> str:
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
