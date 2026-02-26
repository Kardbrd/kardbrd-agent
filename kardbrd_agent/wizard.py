"""Onboarding wizard card — auto-created when the bot starts unconfigured."""

import logging

from kardbrd_client import KardbrdClient

logger = logging.getLogger("kardbrd_agent")

WIZARD_CARD_TITLE = "Kardbrd.yml Workflow Generator"

# List name heuristics for placing the wizard card (first match wins)
_PLACEMENT_HEURISTICS = ["to do", "todo", "backlog", "inbox", "ideas"]

WIZARD_CARD_DESCRIPTION = (  # noqa: E501 — long markdown template
    "## Kardbrd.yml Workflow Generator\n"
    "\n"
    "This card is an interactive onboarding wizard. "
    "When an agent reads this card, it should:\n"
    "\n"
    "1. **Discover** the board's lists and the repository's available skills\n"
    "2. **Generate** checklist-based questions for the user\n"
    '3. **Produce** a complete `kardbrd.yml` when the user says "generate"\n'
    "\n"
    "---\n"
    "\n"
    "### Phase 1: Discovery (Agent does this automatically)\n"
    "\n"
    "#### Board Discovery\n"
    "- Read the board's actual lists via the kardbrd API\n"
    "- Identify which lists map to workflow stages "
    "(discovery, planning, active work, review, done) "
    "using name heuristics\n"
    "\n"
    "#### Skill Discovery\n"
    "- **Read `.claude/commands/*.md`** files from the repository "
    "to find available skills\n"
    "- Each `.md` file is a skill — the filename (without extension) "
    "prefixed with `/` is the command "
    "(e.g., `explore.md` → `/explore`)\n"
    "- Read each skill file to understand what it does, "
    "then classify it into a workflow stage:\n"
    "  - **Discovery/exploration** skills → map to early-stage lists "
    "(Ideas, Backlog, To Do)\n"
    "  - **Planning** skills → map to planning-stage lists\n"
    "  - **Implementation/coding** skills → map to active-work lists "
    "(In Progress, Doing)\n"
    "  - **Review** skills → map to review/QA lists\n"
    "  - **Other** skills → present as optional automations\n"
    "- If no `.claude/commands/` directory exists, "
    "fall back to free-form prompt actions\n"
    "\n"
    "#### Built-in Actions (always available)\n"
    "- `__stop__` — kill active agent session "
    "(triggered by 🛑 reaction)\n"
    "- Free-form prompt text — any custom instruction "
    "as the `action` field\n"
    "\n"
    "---\n"
    "\n"
    "### Phase 2: Generate Checklists\n"
    "\n"
    "After discovery, the agent creates "
    "**two checklists** on this card:\n"
    "\n"
    "**General — Core Workflow** "
    "(~6 items based on what's available)\n"
    "- Respond to @mentions (comment_created event)\n"
    "- One item per discovered skill mapped to a board list "
    '(e.g., "Auto-run [skill] when cards move to [list]")\n'
    "- Stop agent with 🛑 reaction (`__stop__`)\n"
    "\n"
    "**Extras — Advanced Options** (~4 items)\n"
    "- Ship/merge PR with ✅ reaction\n"
    "- Multi-agent board with label filtering\n"
    "- Executor choice (Claude vs Goose)\n"
    "- Model preferences "
    "(opus for thinking, sonnet for coding, haiku for lightweight)\n"
    "\n"
    "The checklist items should reference the "
    "**actual skill names and list names** discovered "
    "from the repository and board — not hardcoded values.\n"
    "\n"
    "---\n"
    "\n"
    "### Phase 3: Generate kardbrd.yml\n"
    "\n"
    'When the user comments "generate my kardbrd.yml", '
    "the agent:\n"
    "\n"
    "1. Reads which checklist items are checked\n"
    "2. Combines checked preferences with the discovered "
    "skill→list mappings\n"
    "3. Produces a complete, ready-to-use `kardbrd.yml` file\n"
    "\n"
    "---\n"
    "\n"
    "### How Skills Are Defined\n"
    "\n"
    "Skills are **repository-specific**. They live in "
    "`.claude/commands/*.md` in the git repository. "
    "Different repos will have different skills. "
    "The agent must **always read the repo's actual skills** "
    "rather than assuming any particular set.\n"
    "\n"
    "Example: a repo might have:\n"
    "```\n"
    ".claude/commands/explore.md    → /explore\n"
    ".claude/commands/implement.md  → /implement\n"
    ".claude/commands/review.md     → /review\n"
    "```\n"
    "\n"
    "Or it might have completely different skills:\n"
    "```\n"
    ".claude/commands/test.md       → /test\n"
    ".claude/commands/deploy.md     → /deploy\n"
    ".claude/commands/docs.md       → /docs\n"
    "```\n"
    "\n"
    "The wizard adapts to whatever skills exist.\n"
    "\n"
    "---\n"
    "\n"
    "### kardbrd.yml Format Reference\n"
    "\n"
    "**Do NOT hardcode the format specification here.** "
    "Instead, the agent must fetch the canonical documentation "
    "from GitHub at generation time:\n"
    "\n"
    "- **Full specification:** "
    "[CLAUDE.md — kardbrd.yml Format]"
    "(https://github.com/Kardbrd/kardbrd-agent/blob/main/"
    "CLAUDE.md#kardbrdyml-format)\n"
    "- **Quick reference:** "
    "[README.md — Rules (kardbrd.yml)]"
    "(https://github.com/Kardbrd/kardbrd-agent/blob/main/"
    "README.md#rules-kardbrdyml)\n"
    "\n"
    "The agent should **read the markdown from the GitHub URL** "
    "to get the current format specification, including:\n"
    "- All supported top-level config fields "
    "(`board_id`, `agent`, `api_url`, `executor`)\n"
    "- All rule fields and conditions "
    "(`list`, `title`, `label`, `emoji`, `content_contains`, "
    "`require_label`, `exclude_label`, `require_user`)\n"
    "- Supported events, models, and actions\n"
    "- Working examples\n"
    "\n"
    "This ensures the format reference is always up-to-date "
    "with the latest `kardbrd-agent` release, "
    "rather than being a stale copy embedded in this card.\n"
    "\n"
    "---\n"
    "\n"
    "### Instructions\n"
    "\n"
    "1. Agent: run Phase 1 discovery, "
    "then Phase 2 to create checklists\n"
    "2. User: check the boxes that match your desired workflow\n"
    "3. User: comment `@<agent> generate my kardbrd.yml` "
    "when ready\n"
    "4. Agent: run Phase 3 — fetch the format spec from GitHub, "
    "read checked items, produce the configuration file\n"
    "5. Agent: after generating the `kardbrd.yml`, "
    "**find and update the executing agent's own robot card "
    "on the board** — look for a card on the board whose title "
    "matches the agent's name (the bot running this wizard). "
    "Add a link from the robot card to this generator card "
    "and update the robot card's description with a reference "
    "to the generated `kardbrd.yml` configuration "
    "(e.g., which rules file was produced, the repo it belongs to, "
    "and a link back to this generator card). "
    "This is generic — each bot updates its own robot card, "
    "not a hardcoded one.\n"
)


def _find_target_list(board: dict) -> dict | None:
    """Pick the best list for the wizard card using name heuristics.

    Falls back to the first list on the board if no heuristic matches.
    Returns ``None`` only when the board has no lists at all.
    """
    lists = board.get("lists", [])
    if not lists:
        return None

    for hint in _PLACEMENT_HEURISTICS:
        for lst in lists:
            if hint in lst.get("name", "").lower():
                return lst

    # No heuristic match — use the first list
    return lists[0]


def _card_already_exists(board: dict, title: str) -> str | None:
    """Scan board cards for a card with the exact *title*.

    Returns the card's ``public_id`` if found, ``None`` otherwise.
    """
    for lst in board.get("lists", []):
        for card in lst.get("cards", []):
            if card.get("title") == title:
                return card.get("public_id")
    return None


def ensure_wizard_card(
    client: KardbrdClient,
    board_id: str,
    agent_name: str,
) -> str | None:
    """Create the onboarding wizard card if the bot has no rules configured.

    This is **idempotent**: if a card with the wizard title already exists on
    the board the function returns its ``public_id`` without creating a
    duplicate.

    Args:
        client: An initialised ``KardbrdClient``.
        board_id: The board to create the card on.
        agent_name: The bot's display name (used in the welcome comment).

    Returns:
        The ``public_id`` of the wizard card (existing or newly created),
        or ``None`` if the board has no lists.
    """
    board = client.get_board(board_id)

    # Idempotency: check if wizard card already exists
    existing_id = _card_already_exists(board, WIZARD_CARD_TITLE)
    if existing_id:
        logger.info("Wizard card already exists: %s", existing_id)
        return existing_id

    target_list = _find_target_list(board)
    if target_list is None:
        logger.warning("Board %s has no lists — skipping wizard card creation", board_id)
        return None

    list_id = target_list["public_id"]
    logger.info(
        "Creating wizard card in list '%s' (%s)",
        target_list.get("name", "unknown"),
        list_id,
    )

    card = client.create_card(
        board_id=board_id,
        list_id=list_id,
        title=WIZARD_CARD_TITLE,
        description=WIZARD_CARD_DESCRIPTION,
    )

    card_id = card["public_id"]
    logger.info("Created onboarding wizard card: %s", card_id)

    # Post a welcome comment so the agent gets notified
    client.add_comment(
        card_id,
        f"@{agent_name} This is your onboarding wizard card. "
        f"Please run Phase 1 discovery and Phase 2 checklist generation.\n\n"
        f"*Created automatically because no `kardbrd.yml` was found.*",
    )

    return card_id
