"""Schedule manager for cron-based automation in kardbrd.yml."""

import asyncio
import logging
from datetime import UTC, datetime

from croniter import croniter

from .rules import Schedule

logger = logging.getLogger("kardbrd_agent")


class ScheduleManager:
    """
    Runs cron-based schedules from kardbrd.yml.

    Each schedule has a name (used as card title), a cron expression,
    and an action. When a schedule fires, it finds or creates a card
    with the schedule name and runs the action in that card's context.

    Designed to run as an asyncio task alongside the WebSocket listener.
    """

    def __init__(
        self,
        schedules: list[Schedule],
        board_id: str,
        client,  # KardbrdClient
        process_callback,  # async callable(card_id, schedule) -> None
    ):
        self._schedules = schedules
        self._board_id = board_id
        self._client = client
        self._process_callback = process_callback
        self._running = False
        # Track next fire time for each schedule
        self._cron_iters: dict[str, croniter] = {}
        self._next_times: dict[str, datetime] = {}

    def _init_cron_iters(self) -> None:
        """Initialize croniter instances and compute next fire times."""
        now = datetime.now(UTC)
        for schedule in self._schedules:
            cron = croniter(schedule.cron, now)
            self._cron_iters[schedule.name] = cron
            self._next_times[schedule.name] = cron.get_next(datetime)

    async def start(self) -> None:
        """Run the schedule loop. Checks every 30s for due schedules."""
        if not self._schedules:
            logger.info("No schedules configured, scheduler not starting")
            return

        self._running = True
        self._init_cron_iters()

        logger.info(f"Scheduler started with {len(self._schedules)} schedule(s)")
        for schedule in self._schedules:
            next_time = self._next_times[schedule.name]
            logger.info(f"  Schedule '{schedule.name}': next at {next_time.isoformat()}")

        while self._running:
            await asyncio.sleep(30)
            await self._check_schedules()

    async def stop(self) -> None:
        """Stop the schedule loop."""
        self._running = False

    async def _check_schedules(self) -> None:
        """Check all schedules and fire any that are due."""
        now = datetime.now(UTC)

        for schedule in self._schedules:
            next_time = self._next_times.get(schedule.name)
            if next_time is None:
                continue

            if now >= next_time:
                logger.info(f"Schedule '{schedule.name}' fired at {now.isoformat()}")
                try:
                    card_id = self._find_or_create_card(schedule)
                    await self._process_callback(card_id, schedule)
                except Exception:
                    logger.exception(f"Error processing schedule '{schedule.name}'")

                # Advance to next fire time
                cron = self._cron_iters[schedule.name]
                self._next_times[schedule.name] = cron.get_next(datetime)
                logger.info(
                    f"Schedule '{schedule.name}': "
                    f"next at {self._next_times[schedule.name].isoformat()}"
                )

    def _find_or_create_card(self, schedule: Schedule) -> str:
        """Find an existing card by title or create a new one.

        Searches the board for a card matching the schedule name. If found,
        returns its ID. Otherwise creates a new card with the schedule name
        as the title, optionally in the specified list with an assignee.

        Returns:
            The card_id of the found or created card.
        """
        # Search the board for a card with matching title
        board = self._client.get_board(self._board_id)
        lists = board.get("lists", [])

        for lst in lists:
            for card in lst.get("cards", []):
                if card.get("title", "").strip().lower() == schedule.name.strip().lower():
                    card_id = card.get("id")
                    logger.info(f"Schedule '{schedule.name}': found existing card {card_id}")
                    return card_id

        # Card not found — create one
        # Determine target list: use schedule.list if set, otherwise first list
        target_list_id = None
        if schedule.list:
            for lst in lists:
                if lst.get("name", "").strip().lower() == schedule.list.strip().lower():
                    target_list_id = lst.get("id")
                    break
            if not target_list_id:
                logger.warning(
                    f"Schedule '{schedule.name}': list '{schedule.list}' not found, "
                    f"using first list"
                )

        if not target_list_id and lists:
            target_list_id = lists[0].get("id")

        if not target_list_id:
            raise RuntimeError(f"Schedule '{schedule.name}': no lists on board {self._board_id}")

        new_card = self._client.create_card(
            board_id=self._board_id,
            list_id=target_list_id,
            title=schedule.name,
        )
        card_id = new_card.get("id")
        logger.info(f"Schedule '{schedule.name}': created card {card_id}")

        # Assign if specified
        if schedule.assignee and card_id:
            try:
                self._client.update_card(card_id, assignee_id=schedule.assignee)
                logger.info(f"Schedule '{schedule.name}': assigned card to {schedule.assignee}")
            except Exception:
                logger.warning(
                    f"Schedule '{schedule.name}': failed to assign card to {schedule.assignee}"
                )

        return card_id
