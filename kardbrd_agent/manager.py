"""ProxyManager - WebSocket-based agent that spawns Claude for @mentions."""

import asyncio
import logging
from dataclasses import dataclass, field
from datetime import UTC
from pathlib import Path

from kardbrd_client import KardbrdClient, WebSocketAgentConnection

from .executor import ClaudeExecutor
from .rules import Rule, RuleEngine
from .worktree import WorktreeManager

logger = logging.getLogger("kardbrd_agent")


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
        board_id: str,
        api_url: str,
        bot_token: str,
        agent_name: str,
        cwd: str | Path | None = None,
        timeout: int = 3600,
        max_concurrent: int = 3,
        worktrees_dir: str | Path | None = None,
        setup_command: str | None = None,
        rule_engine: RuleEngine | None = None,
    ):
        self.board_id = board_id
        self.api_url = api_url
        self.bot_token = bot_token
        self.agent_name = agent_name
        self.mention_keyword = f"@{agent_name}".lower()
        self.cwd = Path(cwd) if cwd else Path.cwd()
        self.timeout = timeout
        self.max_concurrent = max_concurrent
        self.worktrees_dir = Path(worktrees_dir) if worktrees_dir else None
        self.setup_command = setup_command
        self.rule_engine = rule_engine or RuleEngine()

        # Will be initialized in start()
        self.connection: WebSocketAgentConnection | None = None
        self.client: KardbrdClient | None = None
        self.executor: ClaudeExecutor | None = None
        self.worktree_manager: WorktreeManager | None = None

        # Concurrency control: semaphore limits parallel Claude sessions
        self._semaphore = asyncio.Semaphore(max_concurrent)
        # Track active sessions: card_id → ActiveSession
        self._active_sessions: dict[str, ActiveSession] = {}
        self._running = False
        self._processing = False  # Track if currently processing any card

    async def start(self) -> None:
        """
        Start the proxy manager.

        Connects to WebSocket and begins listening for @mention events.
        Each Claude CLI session spawns its own kardbrd-mcp subprocess
        for MCP tools.
        """
        logger.info(f"Board: {self.board_id}")
        logger.info(f"Agent name: {self.agent_name}")
        logger.info(f"Mention keyword: {self.mention_keyword}")

        # Initialize client and executor
        self.client = KardbrdClient(
            base_url=self.api_url,
            token=self.bot_token,
        )
        self.executor = ClaudeExecutor(
            cwd=self.cwd,
            timeout=self.timeout,
            api_url=self.api_url,
            bot_token=self.bot_token,
        )

        # Check Claude CLI authentication
        auth_status = await ClaudeExecutor.check_claude_auth()
        if auth_status.authenticated:
            logger.info(
                f"Claude auth OK (method={auth_status.auth_method}, "
                f"email={auth_status.email}, plan={auth_status.subscription_type})"
            )
        else:
            logger.warning(f"Claude auth check failed: {auth_status.error}")

        # Initialize worktree manager
        self.worktree_manager = WorktreeManager(
            self.cwd, worktrees_dir=self.worktrees_dir, setup_command=self.setup_command
        )

        # Initialize WebSocket connection
        self.connection = WebSocketAgentConnection(
            base_url=self.api_url,
            token=self.bot_token,
        )

        # Register event handler for board events
        self.connection.register_handler("board_event", self._handle_board_event)

        # Start listening
        self._running = True
        logger.info(f"Starting WebSocket connection to {self.api_url}")
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
                    subscription_info = {
                        "board_id": self.board_id,
                        "agent_name": self.agent_name,
                    }
                    await self.connection.send_status_ping(
                        subscription_info=subscription_info,
                        active_cards=active_cards,
                    )
                    logger.debug(
                        f"Status ping: board={self.board_id[:8]}, active_cards={len(active_cards)}"
                    )
            except Exception as e:
                logger.warning(f"Failed to send status ping: {e}")

            # Wait 30 seconds before next ping
            await asyncio.sleep(30)

    async def _handle_board_event(self, message: dict) -> None:
        """
        Handle incoming board events from WebSocket.

        Routes events through the built-in handlers first (mentions, reactions,
        card-moved-to-done), then checks kardbrd.yml rules for additional actions.

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

        # Check kardbrd.yml rules for matching automation
        await self._check_rules(event_type, message)

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
        """Handle reaction events - delegates to rule engine via _check_rules."""
        # Reactions are handled entirely through kardbrd.yml rules.
        # This method is kept for logging only; _check_rules does the dispatch.
        pass

    def _add_reaction(self, card_id: str, comment_id: str | None, emoji: str) -> None:
        """Add emoji reaction to a comment (best effort, no raise on failure)."""
        if not comment_id:
            return
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
            self._processing = True  # Mark as processing
            # Add 👀 reaction to acknowledge receipt
            self._add_reaction(card_id, comment_id, "👀")

            session: ActiveSession | None = None

            try:
                # Check Claude CLI authentication before doing work
                auth_status = await ClaudeExecutor.check_claude_auth()
                if not auth_status.authenticated:
                    logger.error(f"Claude not authenticated: {auth_status.error}")
                    self._add_reaction(card_id, comment_id, "🛑")
                    self.client.add_comment(
                        card_id,
                        f"**Claude not authenticated**\n\n"
                        f"```\n{auth_status.error}\n```\n\n"
                        f"Please run `claude auth login` on the host.\n\n"
                        f"@{author_name}",
                    )
                    return

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

                # Build prompt (include board_id for label operations)
                prompt = self.executor.build_prompt(
                    card_id=card_id,
                    card_markdown=card_markdown,
                    command=command,
                    comment_content=content,
                    author_name=author_name,
                    board_id=self.board_id,
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
                # Clear processing flag and remove from active sessions
                self._processing = False
                self._active_sessions.pop(card_id, None)

    async def _resume_to_publish(
        self,
        card_id: str,
        comment_id: str | None,
        session_id: str,
        author_name: str,
        worktree_path: Path | None = None,
    ) -> None:
        """
        Resume a Claude session to publish its response.

        Called when Claude completed work but didn't post a comment or update the card.

        Args:
            card_id: The card ID to post to
            comment_id: The original comment ID (for reactions), or None for rule triggers
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

        # Post confirmation comment
        try:
            self.client.add_comment(
                card_id,
                "**Agent stopped** 🛑\n\nThe active session was terminated.",
            )
            logger.info(f"Posted stop confirmation to card {card_id}")
        except Exception as e:
            logger.warning(f"Failed to post stop confirmation: {e}")

    async def _check_rules(self, event_type: str, message: dict) -> None:
        """
        Check kardbrd.yml rules against an incoming event and process matches.

        If any rules use ``require_label``, fetches the card's current labels
        from the API and injects them into the message as ``card_labels`` so
        the rule engine can filter on them.

        Args:
            event_type: The WebSocket event type
            message: The full event message
        """
        if not self.rule_engine.rules:
            return

        # Enrich message with card labels when rules need them (exclude_label or require_label)
        needs_labels = any(r.exclude_label or r.require_label for r in self.rule_engine.rules)
        card_id = message.get("card_id")
        if needs_labels and card_id and "card_labels" not in message:
            try:
                card = self.client.get_card(card_id)
                labels = card.get("labels", [])
                message["card_labels"] = [lbl.get("name", "") for lbl in labels]
            except Exception as e:
                logger.warning(f"Failed to fetch card labels for {card_id}: {e}")
                message["card_labels"] = []

        matched_rules = self.rule_engine.match(event_type, message)
        if not matched_rules:
            return

        card_id = message.get("card_id")
        if not card_id:
            return

        for rule in matched_rules:
            logger.info(f"Rule matched: '{rule.name}' for card {card_id}")

            # Stop rules execute deterministic functions (kill active session)
            if rule.is_stop:
                comment_id = message.get("comment_id")
                await self._handle_stop_reaction(card_id, comment_id)
                continue

            # Skip if card is already being processed
            if card_id in self._active_sessions:
                logger.warning(f"Card {card_id} already processing, skipping rule '{rule.name}'")
                continue
            await self._process_rule(card_id=card_id, rule=rule, message=message)

    async def _process_rule(self, card_id: str, rule: Rule, message: dict) -> None:
        """
        Process a matched kardbrd.yml rule by spawning Claude.

        Unlike _process_mention, this handles events without a triggering comment
        (e.g. card_moved, card_created). The action from the rule is used as the
        command/prompt.

        Args:
            card_id: The card ID to act on
            rule: The matched Rule object
            message: The full WebSocket message
        """
        async with self._semaphore:
            self._processing = True
            session: ActiveSession | None = None

            try:
                # Check Claude CLI authentication before doing work
                auth_status = await ClaudeExecutor.check_claude_auth()
                if not auth_status.authenticated:
                    logger.error(f"Claude not authenticated: {auth_status.error}")
                    self.client.add_comment(
                        card_id,
                        f"**Automation Error** ({rule.name})\n\n"
                        f"Claude not authenticated: `{auth_status.error}`\n\n"
                        f"Please run `claude auth login` on the host.",
                    )
                    return

                worktree_path = self.worktree_manager.create_worktree(card_id)
                logger.info(f"Rule '{rule.name}': using worktree {worktree_path}")

                session = ActiveSession(card_id=card_id, worktree_path=worktree_path)
                self._active_sessions[card_id] = session

                card_markdown = self.client.get_card_markdown(card_id)

                # Build prompt using the rule's action
                prompt = self.executor.build_prompt(
                    card_id=card_id,
                    card_markdown=card_markdown,
                    command=rule.action,
                    comment_content=f"[Automation: {rule.name}]",
                    author_name="automation",
                    board_id=self.board_id,
                )

                logger.info(f"Rule '{rule.name}': spawning Claude in {worktree_path}")
                result = await self.executor.execute(
                    prompt,
                    cwd=worktree_path,
                    model=rule.model_id,
                )

                if session:
                    session.session_id = result.session_id

                if result.success:
                    logger.info(f"Rule '{rule.name}': Claude completed successfully")
                    if not self._has_recent_bot_comment(card_id) and result.session_id:
                        await self._resume_to_publish(
                            card_id=card_id,
                            comment_id=None,
                            session_id=result.session_id,
                            author_name="automation",
                            worktree_path=worktree_path,
                        )
                else:
                    logger.error(f"Rule '{rule.name}': Claude failed: {result.error}")
                    error_comment = (
                        f"**Automation Error** ({rule.name})\n\n```\n{result.error}\n```"
                    )
                    self.client.add_comment(card_id, error_comment)

            except Exception:
                import traceback

                logger.exception(f"Error processing rule '{rule.name}'")
                tb = traceback.format_exc()
                try:
                    self.client.add_comment(
                        card_id,
                        f"**Automation Error** ({rule.name})\n\n```\n{tb}\n```",
                    )
                except Exception:
                    logger.error("Failed to post error comment for rule")

            finally:
                self._processing = False
                self._active_sessions.pop(card_id, None)

    async def _handle_card_moved(self, message: dict) -> None:
        """
        Handle card_moved events.

        Cleans up worktree when card is moved to Done list.

        Args:
            message: The WebSocket message containing the event
        """
        card_id = message.get("card_id")
        list_name = message.get("list_name", "").lower()

        # Check if moved to "done" list
        if "done" in list_name:
            logger.info(f"Card {card_id} moved to Done, cleaning up worktree")
            await self._cleanup_worktree(card_id)
        else:
            logger.info(f"Card {card_id} moved to '{list_name}' - no action needed")

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
