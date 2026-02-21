# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

> if there's a file `kardbrd.md`, then read it as well.

## Project

kardbrd-agent is a proxy that listens for @mentions on kardbrd board cards, spawns Claude CLI in isolated git worktrees, and coordinates workflows including automated merging. Python 3.12+, built with Hatchling, managed with uv.

## Commands

```bash
# Install dependencies
uv sync --dev

# Run all tests
uv run pytest

# Run a single test file
uv run pytest kardbrd_agent/tests/test_merge_workflow.py

# Run a specific test
uv run pytest kardbrd_agent/tests/test_integration.py::TestConcurrentProcessingIntegration

# Lint and format (via pre-commit)
uv run pre-commit run --all-files

# Run ruff only
uv run pre-commit run ruff --all-files

# CLI entry point (env vars or flags)
KARDBRD_ID=board123 KARDBRD_TOKEN=tok_xxx KARDBRD_AGENT=mybot uv run kardbrd-agent start
uv run kardbrd-agent start --board-id board123 --token tok_xxx --name mybot

# Validate kardbrd.yml
uv run kardbrd-agent validate
uv run kardbrd-agent validate path/to/kardbrd.yml
```

## Architecture

**ProxyManager** (`manager.py`) — Main orchestrator. Connects via WebSocket, detects @mention comments and card moves, spawns Claude in worktrees, manages concurrency with asyncio.Semaphore (max_concurrent default 3), tracks per-card sessions. Accepts board config directly via constructor params (`board_id`, `api_url`, `bot_token`, `agent_name`).

**RuleEngine** (`rules.py`) — Declarative YAML-driven automation. Loads `kardbrd.yml` with top-level config (`board_id`, `agent`, optional `api_url`) and a `rules` list. Each Rule has conditions (`list`, `title`, `label`, `emoji`, `require_label`, `exclude_label`, `require_user`, `content_contains`) and triggers a Claude session or the built-in `__stop__` action. `ReloadableRuleEngine` hot-reloads rules every 60s on file change. `validate_rules_file()` provides comprehensive validation with errors/warnings.

**ClaudeExecutor** (`executor.py`) — Spawns `claude -p` subprocess with `--output-format=stream-json`. Parses streaming output, extracts session IDs for resumption, enforces timeouts. Builds prompts with card context and detects skill commands ("/kp", "/ki"). Accepts per-rule model via `--model` flag.

**MCP Proxy** (`mcp_proxy.py`) — Data classes for session tracking (ProxySession, ProxySessionRegistry). Previously hosted a FastMCP HTTP/SSE server; now Claude CLI spawns `kardbrd-mcp` (from kardbrd-client) as a stdio subprocess per session.

**WorktreeManager** (`worktree.py`) — Creates git worktrees as sibling directories to the base repo (`~/src/kbn-abc12345/`). Sets up symlinks for .mcp.json, .env, .claude/settings.local.json. Runs `uv sync --quiet` in each worktree.

**MergeWorkflow** (`merge_workflow.py`) + **MergeTools** (`merge_tools.py`) — State machine for automated merging: commit uncommitted changes, fetch/rebase, resolve conflicts (LLM-assisted), run tests with fix loop, squash merge to main, cleanup. MergeTools handles deterministic git ops; MergeWorkflow orchestrates with LLM steps.

## Key Patterns

- Cards identified by `card_id` (public_id). Data flow: Comment → card_id → fetch card_markdown → build prompt → execute Claude → record session → cleanup.
- Async throughout: `asyncio.gather()`, semaphore concurrency, per-card session tracking prevents duplicate processing.
- Logging via `logging.getLogger("kardbrd_agent")` with module-level loggers.
- Board config via env vars (`KARDBRD_ID`, `KARDBRD_TOKEN`, `KARDBRD_AGENT`, `KARDBRD_URL`) or CLI flags — no subscription statefile.
- Rule engine: WebSocket event → `RuleEngine.match(event_type, message)` → matched rules → spawn Claude or `__stop__`. Label-based conditions (`require_label`/`exclude_label`) trigger on-demand card label enrichment via API.
- `kardbrd.yml` supports multi-agent boards: use `require_label`/`exclude_label` to scope rules per agent (e.g. KABot handles "Agent"-labeled cards, MBPBot handles the rest on the same board).
- Tests use `@pytest.mark.asyncio`, fixtures in `conftest.py` (git_repo, mock_kardbrd_client, mock_claude_result). MBPBot fixture at `tests/fixtures/mbpbot_kardbrd.yml`.

## kardbrd.yml Format

```yaml
board_id: 0gl5MlBZ        # required
agent: MBPBot              # required — agent name for @mentions
api_url: http://app.kardbrd.com  # optional

rules:
  - name: Explore new ideas
    event:                 # string or YAML list
      - card_created
      - card_moved
    list: Ideas            # match list name (case-insensitive)
    exclude_label: Agent   # skip cards with this label
    model: opus            # opus | sonnet | haiku
    action: /ke            # skill command or inline prompt

  - name: Stop agent
    event: reaction_added
    emoji: "🛑"
    action: __stop__       # built-in: kills active session

  - name: Ship on approval
    event: reaction_added
    emoji: "✅"
    require_user: E21K9jmv # restrict to specific user ID
    require_label: Agent   # require card has this label
    model: sonnet
    action: |
      Merge the PR to main...
```

Rule conditions: `list`, `title`, `label`, `emoji`, `content_contains`, `require_label`, `exclude_label`, `require_user`. All conditions must match (AND logic).
