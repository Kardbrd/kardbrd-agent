"""ProxyManager - WebSocket-based agent that spawns Claude for @mentions."""

import asyncio
import logging
from dataclasses import dataclass, field
from datetime import UTC
from pathlib import Path

from kardbrd_client import DirectoryStateManager, KardbrdClient, WebSocketAgentConnection

from .executor import ClaudeExecutor
from .worktree import WorktreeManager

logger = logging.getLogger("kardbrd_agent")

# Emoji for triggering a retry of a previous @mention
RETRY_EMOJI = "🔄"
# Emoji for stopping an active session (user reacts on their triggering comment)
STOP_EMOJI = "🛑"
# Emojis to clear before retrying
COMPLETION_EMOJIS = ("✅", "🛑")

# Default name for the merge queue list
DEFAULT_MERGE_QUEUE_LIST = "merge queue"
# Default test command
DEFAULT_TEST_COMMAND = "make test"


@dataclass
class ActiveSession:
    """Tracks an active Claude session for a card."""

    card_id: str
    worktree_path: Path
    comment_id: str | None = field(default=None)
    process: asyncio.subprocess.Process | None = field(default=None)
    session_id: str | None = field(default=None)


class ProxyManager:
    """
    Proxy agent that listens for @mentions and spawns Claude to handle them.

    Uses WebSocket for real-time event handling. When a comment with @mention
    is detected, fetches the card context and spawns Claude CLI to process
    the request. Each Claude session spawns its own kardbrd-mcp subprocess
    for MCP tools.
    """

    def __init__(
        self,
        state_manager: DirectoryStateManager,
        mention_keyword: str = "@coder",
        cwd: str | Path | None = None,
        timeout: int = 3600,
        max_concurrent: int = 3,
        worktrees_dir: str | Path | None = None,
        setup_command: str | None = None,
        test_command: str | None = None,
        merge_queue_list: str | None = None,
    ):
        """
        Initialize the proxy manager.

        Args:
            state_manager: State manager for board subscriptions
            mention_keyword: The keyword to respond to (e.g., "@coder")
            cwd: Working directory for Claude (defaults to current directory)
            timeout: Maximum execution time in seconds for Claude (default 1 hour)
            max_concurrent: Maximum number of concurrent Claude sessions
            worktrees_dir: Optional directory for worktrees (defaults to cwd parent)
            setup_command: Shell command to run in worktrees after creation (e.g. "npm install")
            test_command: Command to run tests during merge workflow
            merge_queue_list: List name that triggers merge workflow
        """
        self.state_manager = state_manager
        self.mention_keyword = mention_keyword.lower()
        self.cwd = Path(cwd) if cwd else Path.cwd()
        self.timeout = timeout
        self.max_concurrent = max_concurrent
        self.worktrees_dir = Path(worktrees_dir) if worktrees_dir else None
        self.setup_command = setup_command
        self.test_command = test_command
        self.merge_queue_list = merge_queue_list

        # Will be initialized when subscription is loaded
        self.connection: WebSocketAgentConnection | None = None
        self.client: KardbrdClient | None = None
        self.executor: ClaudeExecutor | None = None
        self.worktree_manager: WorktreeManager | None = None
        self._subscription_info: dict | None = None  # Cached subscription info for status pings

        # Concurrency control: semaphore limits parallel Claude sessions
        self._semaphore = asyncio.Semaphore(max_concurrent)
        # Track active sessions: card_id → ActiveSession
        self._active_sessions: dict[str, ActiveSession] = {}
        self._running = False

    @property
    def _processing(self) -> bool:
        """True when any card is being actively processed."""
        return len(self._active_sessions) > 0

    async def start(self) -> None:
        """
        Start the proxy manager.

        Loads subscription, connects to WebSocket, and begins listening
        for @mention events. Each Claude CLI session spawns its own
        kardbrd-mcp subprocess for MCP tools.
        """
        # Load subscription
        subscriptions = self.state_manager.get_all_subscriptions()
        if not subscriptions:
            logger.error("No subscriptions found")
            raise RuntimeError(
                "No subscriptions configured. Use 'kardbrd-agent sub <setup-url>' to subscribe."
            )

        # Use first subscription (PoC: single board)
        board_id = next(iter(subscriptions.keys()))
        subscription = subscriptions[board_id]

        logger.info(f"Loading subscription for board {board_id}")
        logger.info(f"Agent name: {subscription.agent_name}")
        logger.info(f"Mention keyword: @{subscription.agent_name}")

        # Update mention keyword from subscription
        self.mention_keyword = f"@{subscription.agent_name}".lower()

        # Cache subscription info for status pings
        self._subscription_info = {
            "board_id": board_id,
            "agent_name": subscription.agent_name,
        }

        # Initialize client and executor
        self.client = KardbrdClient(
            base_url=subscription.api_url,
            token=subscription.bot_token,
        )
        self.executor = ClaudeExecutor(
            cwd=self.cwd,
            timeout=self.timeout,
            api_url=subscription.api_url,
            bot_token=subscription.bot_token,
        )

        # Initialize worktree manager
        self.worktree_manager = WorktreeManager(
            self.cwd, worktrees_dir=self.worktrees_dir, setup_command=self.setup_command
        )

        # Initialize WebSocket connection
        self.connection = WebSocketAgentConnection(
            base_url=subscription.api_url,
            token=subscription.bot_token,
        )

        # Register event handler for board events
        self.connection.register_handler("board_event", self._handle_board_event)

        # Start listening
        self._running = True
        logger.info(f"Starting WebSocket connection to {subscription.api_url}")
        logger.info(f"Working directory: {self.cwd}")
        logger.info(f"Listening for {self.mention_keyword} mentions...")

        await asyncio.gather(self.connection.connect(), self._status_ping_loop())

    async def stop(self) -> None:
        """Stop the proxy manager."""
        self._running = False
        if self.connection:
            await self.connection.close()
        if self.client:
            self.client.close()
        logger.info("Proxy manager stopped")

    async def _status_ping_loop(self) -> None:
        """
        Periodically send status pings with subscription and active card info.

        Runs every 30 seconds while the manager is running.
        """
        while self._running:
            try:
                # Wait for connection to be established
                if self.connection and self.connection.is_connected:
                    active_cards = list(self._active_sessions.keys())
                    await self.connection.send_status_ping(
                        subscription_info=self._subscription_info,
                        active_cards=active_cards,
                    )
                    board = self._subscription_info.get("board_id", "unknown")[:8]
                    logger.debug(f"Status ping: board={board}, active_cards={len(active_cards)}")
            except Exception as e:
                logger.warning(f"Failed to send status ping: {e}")

            # Wait 30 seconds before next ping
            await asyncio.sleep(30)

    async def _handle_board_event(self, message: dict) -> None:
        """
        Handle incoming board events from WebSocket.

        Args:
            message: The WebSocket message containing the event
        """
        event_type = message.get("event_type")
        card_id = message.get("card_id", "unknown")

        if event_type == "comment_created":
            logger.info(f"Signal received: comment_created for card {card_id}")
            await self._handle_comment_created(message)
        elif event_type == "reaction_added":
            emoji = message.get("emoji", "")
            logger.info(f"Signal received: reaction_added ({emoji}) for card {card_id}")
            await self._handle_reaction_added(message)
        elif event_type == "card_moved":
            list_name = message.get("list_name", "unknown")
            logger.info(f"Signal received: card_moved for card {card_id} to '{list_name}'")
            await self._handle_card_moved(message)
        else:
            logger.debug(f"Ignoring event type: {event_type}")

    async def _handle_comment_created(self, message: dict) -> None:
        """Handle new comment events."""
        card_id = message.get("card_id")
        comment_id = message.get("comment_id")
        content = message.get("content", "")
        author_name = message.get("author_name", "Unknown")

        logger.debug(f"Comment event: card={card_id}, author={author_name}")

        # Check for @mention
        if self.mention_keyword not in content.lower():
            logger.debug(f"No mention of {self.mention_keyword} in comment")
            return

        logger.info(f"Detected {self.mention_keyword} mention by {author_name} on card {card_id}")

        # Check if already processing THIS card
        if card_id in self._active_sessions:
            logger.warning(f"Card {card_id} already being processed, skipping")
            return

        # Process the mention (will acquire semaphore)
        await self._process_mention(
            card_id=card_id,
            comment_id=comment_id,
            content=content,
            author_name=author_name,
        )

    async def _handle_reaction_added(self, message: dict) -> None:
        """Handle reaction events - check for retry or stop emoji."""
        emoji = message.get("emoji")
        card_id = message.get("card_id")
        comment_id = message.get("comment_id")

        if emoji == STOP_EMOJI:
            await self._handle_stop_reaction(card_id, comment_id)
            return

        if emoji != RETRY_EMOJI:
            logger.info(f"Ignoring non-actionable emoji: {emoji}")
            return

        user_name = message.get("user_name", "Unknown")

        logger.info(f"Retry requested by {user_name} on comment {comment_id}")

        # Fetch the comment to check if it contains @mention
        try:
            comment = self.client.get_comment(card_id, comment_id)
        except Exception as e:
            logger.error(f"Failed to fetch comment for retry: {e}")
            return

        content = comment.get("content", "")
        author = comment.get("author", {})
        author_name = author.get("display_name", "Unknown")

        # Check for @mention
        if self.mention_keyword not in content.lower():
            logger.debug(f"Retry ignored: no {self.mention_keyword} in comment")
            return

        # Check if already processing
        if self._processing:
            logger.warning("Already processing a request, skipping retry")
            return

        # Clear old completion reactions before retrying
        for old_emoji in COMPLETION_EMOJIS:
            self._remove_reaction(card_id, comment_id, old_emoji)

        logger.info(f"Retrying {self.mention_keyword} mention by {author_name} on card {card_id}")

        # Process the mention
        await self._process_mention(
            card_id=card_id,
            comment_id=comment_id,
            content=content,
            author_name=author_name,
        )

    def _add_reaction(self, card_id: str, comment_id: str, emoji: str) -> None:
        """Add emoji reaction to a comment (best effort, no raise on failure)."""
        try:
            self.client.toggle_reaction(card_id, comment_id, emoji)
            logger.debug(f"Added {emoji} reaction to comment {comment_id}")
        except Exception as e:
            logger.warning(f"Failed to add {emoji} reaction: {e}")

    def _remove_reaction(self, card_id: str, comment_id: str, emoji: str) -> None:
        """Remove emoji reaction if present (best effort, no raise on failure)."""
        try:
            # Fetch comment to check if reaction exists
            comment = self.client.get_comment(card_id, comment_id)
            reactions = comment.get("reactions", {})

            # Check if our bot has this reaction
            if emoji in reactions:
                # Toggle will remove it since it exists
                self.client.toggle_reaction(card_id, comment_id, emoji)
                logger.debug(f"Removed {emoji} reaction from comment {comment_id}")
        except Exception as e:
            logger.warning(f"Failed to remove {emoji} reaction: {e}")

    def _has_recent_bot_comment(self, card_id: str, seconds: int = 60) -> bool:
        """
        Check if this bot posted a comment on the card recently.

        Used as a safety check before posting fallback comments to prevent duplicates.

        Args:
            card_id: The card ID to check
            seconds: Time window to consider "recent" (default 60s)

        Returns:
            True if bot posted recently, False otherwise (or on error - fails open)
        """
        from datetime import datetime, timedelta

        try:
            card = self.client.get_card(card_id)
            comments = card.get("comments", [])
            cutoff = datetime.now(UTC) - timedelta(seconds=seconds)

            for comment in comments:
                author = comment.get("author", {})
                created_at = comment.get("created_at")
                if (
                    author.get("is_bot")
                    and created_at
                    and datetime.fromisoformat(created_at.replace("Z", "+00:00")) > cutoff
                ):
                    return True
            return False
        except Exception as e:
            logger.warning(f"Failed to check recent comments: {e}")
            return False  # Fail open - allow fallback if check fails

    async def _process_mention(
        self,
        card_id: str,
        comment_id: str,
        content: str,
        author_name: str,
    ) -> None:
        """
        Process an @mention by spawning Claude.

        Creates a worktree for the card and runs Claude inside it.
        Uses semaphore to limit concurrent executions.

        Args:
            card_id: The card ID where the comment was posted
            comment_id: The comment ID
            content: The comment content
            author_name: Name of the comment author
        """
        # Check if already processing THIS card (before semaphore)
        if card_id in self._active_sessions:
            logger.warning(f"Card {card_id} already being processed")
            return

        # Acquire semaphore (blocks if max_concurrent reached)
        async with self._semaphore:
            # Add 👀 reaction to acknowledge receipt
            self._add_reaction(card_id, comment_id, "👀")

            session: ActiveSession | None = None

            try:
                # Create worktree for this card
                worktree_path = self.worktree_manager.create_worktree(card_id)
                logger.info(f"Using worktree: {worktree_path}")

                # Track active session (including triggering comment for stop-by-reaction)
                session = ActiveSession(
                    card_id=card_id, worktree_path=worktree_path, comment_id=comment_id
                )
                self._active_sessions[card_id] = session

                # Fetch card markdown for context
                logger.info(f"Fetching card {card_id} context...")
                card_markdown = self.client.get_card_markdown(card_id)

                # Extract command from comment
                command = self.executor.extract_command(content, self.mention_keyword)
                logger.info(f"Extracted command: {command[:50]}...")

                # Build prompt
                prompt = self.executor.build_prompt(
                    card_id=card_id,
                    card_markdown=card_markdown,
                    command=command,
                    comment_content=content,
                    author_name=author_name,
                )

                # Execute Claude in worktree directory
                logger.info(f"Spawning Claude in {worktree_path}...")
                result = await self.executor.execute(prompt, cwd=worktree_path)

                # Store session_id in active session
                if session:
                    session.session_id = result.session_id

                # Check result and verify response was posted
                if result.success:
                    logger.info("Claude completed successfully")

                    # Check if Claude posted via the kardbrd API
                    if self._has_recent_bot_comment(card_id):
                        logger.info("Claude posted response (verified via API)")
                        self._add_reaction(card_id, comment_id, "✅")
                    elif result.session_id:
                        # Claude didn't post - resume session with explicit instructions
                        logger.warning("Claude didn't post response, resuming session...")
                        await self._resume_to_publish(
                            card_id=card_id,
                            comment_id=comment_id,
                            session_id=result.session_id,
                            author_name=author_name,
                            worktree_path=worktree_path,
                        )
                    else:
                        # No session_id to resume - log warning but mark success
                        logger.warning(
                            "Claude completed but no response posted (no session to resume)"
                        )
                        self._add_reaction(card_id, comment_id, "✅")
                else:
                    logger.error(f"Claude failed: {result.error}")
                    self._add_reaction(card_id, comment_id, "🛑")
                    # Post error details for debugging
                    error_comment = f"**Error**\n\n```\n{result.error}\n```\n\n@{author_name}"
                    self.client.add_comment(card_id, error_comment)
                    logger.info(f"Posted error comment to card {card_id}")

            except Exception:
                import traceback

                logger.exception("Error processing mention")
                # Add 🛑 reaction for exception
                self._add_reaction(card_id, comment_id, "🛑")
                # Post full stack trace for debugging
                tb = traceback.format_exc()
                try:
                    self.client.add_comment(
                        card_id,
                        f"**Error processing request**\n\n```\n{tb}\n```\n\n@{author_name}",
                    )
                except Exception:
                    logger.error("Failed to post error comment")

            finally:
                # Remove from active sessions
                self._active_sessions.pop(card_id, None)

    async def _resume_to_publish(
        self,
        card_id: str,
        comment_id: str,
        session_id: str,
        author_name: str,
        worktree_path: Path | None = None,
    ) -> None:
        """
        Resume a Claude session to publish its response.

        Called when Claude completed work but didn't post a comment or update the card.

        Args:
            card_id: The card ID to post to
            comment_id: The original comment ID (for reactions)
            session_id: The Claude session ID to resume
            author_name: Name of the original requester
            worktree_path: Optional worktree path to run Claude in
        """
        resume_prompt = f"""You completed the task but forgot to publish your response.

Please do ONE of the following:
1. Post a summary comment using `mcp__kardbrd__add_comment` with card_id="{card_id}"
2. Update the card description using `mcp__kardbrd__update_card` if appropriate
3. If you made code changes, commit them with git

End your comment by mentioning @{author_name}

DO NOT do any new work - just publish what you already did."""

        logger.info(f"Resuming session {session_id} to publish response...")
        result = await self.executor.execute(
            resume_prompt,
            resume_session_id=session_id,
            cwd=worktree_path,
        )

        # Check if Claude posted via the API
        if result.success and self._has_recent_bot_comment(card_id):
            logger.info("Resume successful - response published")
            self._add_reaction(card_id, comment_id, "✅")
        elif result.success:
            logger.warning("Resume completed but still no response posted")
            # Last resort: post result_text if available
            # Safety check: don't post if bot already commented recently (prevents duplicates)
            if result.result_text:
                if self._has_recent_bot_comment(card_id):
                    logger.info("Bot already posted recently, skipping fallback comment")
                else:
                    self.client.add_comment(card_id, f"{result.result_text}\n\n@{author_name}")
            self._add_reaction(card_id, comment_id, "✅")
        else:
            logger.error(f"Resume failed: {result.error}")
            self._add_reaction(card_id, comment_id, "🛑")
            self.client.add_comment(
                card_id,
                f"**Error resuming session**\n\n```\n{result.error}\n```\n\n@{author_name}",
            )

    async def _handle_stop_reaction(self, card_id: str, comment_id: str) -> None:
        """
        Handle stop via 🛑 reaction on the triggering comment.

        When a user adds a 🛑 reaction to the comment that initiated work,
        the active Claude session for that card is killed. The worktree is preserved.

        Args:
            card_id: The card ID
            comment_id: The comment ID that received the 🛑 reaction
        """
        if card_id not in self._active_sessions:
            logger.info(f"No active session for card {card_id}, ignoring stop reaction")
            return

        session = self._active_sessions[card_id]

        # Only stop if the reaction is on the comment that triggered this session
        if session.comment_id and session.comment_id != comment_id:
            logger.info(
                f"Stop reaction on comment {comment_id} doesn't match "
                f"triggering comment {session.comment_id}, ignoring"
            )
            return

        if session.process:
            session.process.kill()
            logger.info(f"Killed Claude session for card {card_id} via stop reaction")

        # Remove from active sessions but preserve worktree
        del self._active_sessions[card_id]

    async def _handle_card_moved(self, message: dict) -> None:
        """
        Handle card_moved events.

        - Cleans up worktree when card is moved to Done list.
        - Triggers merge workflow when card is moved to Merge Queue list.

        Args:
            message: The WebSocket message containing the event
        """
        card_id = message.get("card_id")
        list_name = message.get("list_name", "").lower()

        # Check if moved to "done" list
        if "done" in list_name:
            logger.info(f"Card {card_id} moved to Done, cleaning up worktree")
            await self._cleanup_worktree(card_id)
            return

        # Check if merge queue is configured
        if not self.merge_queue_list:
            logger.debug("No merge queue configured, ignoring card move")
            return

        # Check if moved to merge queue list (case-insensitive substring match)
        merge_queue_list = self.merge_queue_list.lower()
        if merge_queue_list in list_name:
            logger.info(f"Card {card_id} moved to Merge Queue, starting merge workflow")
            test_command = self.test_command or DEFAULT_TEST_COMMAND
            await self._trigger_merge_workflow(card_id, test_command)
        else:
            logger.info(f"Card {card_id} moved to '{list_name}' - no action needed")

    async def _trigger_merge_workflow(self, card_id: str, test_command: str) -> None:
        """
        Trigger the merge workflow for a card.

        Args:
            card_id: The card ID to merge
            test_command: Command to run tests during merge
        """
        from .merge_workflow import MergeWorkflow

        # Get card title for commit message
        try:
            card = self.client.get_card(card_id)
            card_title = card.get("title", f"Card {card_id}")
        except Exception as e:
            logger.error(f"Failed to get card {card_id}: {e}")
            card_title = f"Card {card_id}"

        # Create and run merge workflow
        workflow = MergeWorkflow(
            card_id=card_id,
            card_title=card_title,
            main_repo_path=self.cwd,
            client=self.client,
            executor=self.executor,
            test_command=test_command,
        )

        try:
            status = await workflow.run()
            logger.info(f"Merge workflow completed for card {card_id}: {status.value}")
        except Exception as e:
            logger.exception(f"Merge workflow failed for card {card_id}: {e}")

    async def _cleanup_worktree(self, card_id: str) -> None:
        """
        Remove worktree for a card.

        Kills any active session and removes the worktree.

        Args:
            card_id: The card ID
        """
        # Kill active session if running
        if card_id in self._active_sessions:
            session = self._active_sessions[card_id]
            if session.process:
                session.process.kill()
            del self._active_sessions[card_id]

        # Remove worktree
        if self.worktree_manager:
            self.worktree_manager.remove_worktree(card_id)
