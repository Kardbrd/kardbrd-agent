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
pytest

# Run a single test file
pytest kardbrd_agent/tests/test_merge_workflow.py

# Run a specific test
pytest kardbrd_agent/tests/test_integration.py::TestConcurrentProcessingIntegration

# Lint and format (via pre-commit)
pre-commit run --all-files

# Run ruff only
pre-commit run ruff --all-files

# CLI entry point
kardbrd-agent start
```

## Architecture

**ProxyManager** (`manager.py`) — Main orchestrator. Connects via WebSocket, detects @mention comments and card moves, spawns Claude in worktrees, manages concurrency with asyncio.Semaphore (max_concurrent default 3), tracks per-card sessions.

**ClaudeExecutor** (`executor.py`) — Spawns `claude -p` subprocess with `--output-format=stream-json`. Parses streaming output, extracts session IDs for resumption, enforces timeouts. Builds prompts with card context and detects skill commands ("/kp", "/ki").

**MCP Proxy** (`mcp_proxy.py`) — Data classes for session tracking (ProxySession, ProxySessionRegistry). Previously hosted a FastMCP HTTP/SSE server; now Claude CLI spawns `kardbrd-mcp` (from kardbrd-client) as a stdio subprocess per session.

**WorktreeManager** (`worktree.py`) — Creates git worktrees as sibling directories to the base repo (`~/src/kbn-abc12345/`). Sets up symlinks for .mcp.json, .env, .claude/settings.local.json. Runs `uv sync --quiet` in each worktree.

**MergeWorkflow** (`merge_workflow.py`) + **MergeTools** (`merge_tools.py`) — State machine for automated merging: commit uncommitted changes, fetch/rebase, resolve conflicts (LLM-assisted), run tests with fix loop, squash merge to main, cleanup. MergeTools handles deterministic git ops; MergeWorkflow orchestrates with LLM steps.

## Key Patterns

- Cards identified by `card_id` (public_id). Data flow: Comment → card_id → fetch card_markdown → build prompt → execute Claude → record session → cleanup.
- Async throughout: `asyncio.gather()`, semaphore concurrency, per-card session tracking prevents duplicate processing.
- Logging via `logging.getLogger("kardbrd_agent")` with module-level loggers.
- State stored in `state/subscriptions/<board_id>.json` as BoardSubscription dataclasses.
- Tests use `@pytest.mark.asyncio`, fixtures in `conftest.py` (git_repo, mock_kardbrd_client, mock_claude_result).
