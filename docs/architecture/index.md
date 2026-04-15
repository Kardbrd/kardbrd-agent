# Architecture Overview

kardbrd-agent is an async Python proxy that bridges kardbrd boards with AI agent CLIs. It listens for board events via WebSocket, matches them against declarative rules, and spawns executors in isolated git worktrees.

## Event flow

```
                         ┌─────────────────┐
                         │  kardbrd Board   │
                         │   (WebSocket)    │
                         └────────┬────────┘
                                  │ events
                                  ▼
                         ┌─────────────────┐
                         │  ProxyManager    │
                         │   (manager.py)   │
                         └────────┬────────┘
                                  │
                    ┌─────────────┼─────────────┐
                    │             │             │
                    ▼             ▼             ▼
            ┌──────────┐  ┌──────────┐  ┌──────────────┐
            │   Rule    │  │  Status  │  │  Schedule    │
            │  Engine   │  │   Ping   │  │  Manager     │
            │(rules.py) │  │          │  │(scheduler.py)│
            └─────┬────┘  └──────────┘  └──────┬───────┘
                  │                             │
                  │     matched rules           │ cron triggers
                  └──────────┬──────────────────┘
                             │
                             ▼
                    ┌─────────────────┐
                    │ WorktreeManager  │
                    │  (worktree.py)   │
                    └────────┬────────┘
                             │ isolated directory
                             ▼
                    ┌─────────────────┐
                    │    Executor      │
                    │ (executor.py)    │
                    │ Claude/Goose/    │
                    │ Codex            │
                    └────────┬────────┘
                             │ results
                             ▼
                    ┌─────────────────┐
                    │  Card Updates    │
                    │ (comments, PRs)  │
                    └─────────────────┘
```

## Core components

### ProxyManager (`manager.py`)

The main orchestrator. Connects to kardbrd via WebSocket, detects @mention comments and rule-matched events, and spawns executors in worktrees.

Key responsibilities:

- WebSocket connection management and reconnection
- Event routing to the rule engine
- Concurrency control via `asyncio.Semaphore` (default: 3 parallel sessions)
- Per-card session tracking to prevent duplicate processing
- Bot card creation and management
- Skill discovery and registration

### RuleEngine (`rules.py`)

Declarative YAML-driven automation. Loads `kardbrd.yml` with rules and schedules. Each rule defines conditions that must all match (AND logic) for the rule to fire.

- **Hot-reload**: `ReloadableRuleEngine` watches the rules file and reloads every 60 seconds on change
- **Validation**: `validate_rules_file()` provides comprehensive error/warning checking
- **Label enrichment**: `require_label`/`exclude_label` conditions trigger on-demand API calls

### ScheduleManager (`scheduler.py`)

Cron-based automation running as a third `asyncio.gather()` task. Each schedule has a name that doubles as a card title — finds existing cards or creates new ones.

### Executors (`executor.py`, `goose_executor.py`, `codex_executor.py`)

Pluggable AI agent backends implementing the `Executor` Protocol. Factory pattern selects the executor based on `executor_type` configuration.

### WorktreeManager (`worktree.py`)

Creates isolated git worktrees as sibling directories. Each card gets its own worktree with symlinked configuration files.

### Merge Workflow

The merge workflow is handled by the executor as part of rule actions. When triggered (e.g., by a reaction rule), the executor performs: commit → fetch/rebase → resolve conflicts → run tests → squash merge → cleanup.

## Concurrency model

All I/O is async (`asyncio`). The agent runs three concurrent tasks via `asyncio.gather()`:

1. **WebSocket listener** — receives board events
2. **Status ping** — periodic heartbeat
3. **Schedule manager** — checks cron schedules every 30 seconds

Session concurrency is bounded by `asyncio.Semaphore` (configurable via `--max-concurrent`). Per-card session tracking prevents the same card from being processed twice simultaneously.

## Data flow

Cards are identified by `card_id` (public_id). The typical flow:

1. **Event received** — WebSocket delivers a board event
2. **Rule matching** — `RuleEngine.match(event_type, message)` returns matched rules
3. **Card context** — fetch card markdown via API
4. **Worktree creation** — `WorktreeManager.create_worktree(card_id)` sets up isolated directory
5. **Prompt building** — `Executor.build_prompt()` assembles context from card content
6. **Execution** — `Executor.execute()` spawns the CLI subprocess
7. **Session tracking** — results recorded, worktree cleaned up

## Configuration flow

```
Environment variables / CLI flags
         │
         ▼
    ProxyManager.__init__()
         │
         ├── kardbrd.yml → RuleEngine + BoardConfig
         │                    │
         │                    ├── Rules (event matching)
         │                    └── Schedules (cron automation)
         │
         ├── Executor factory (claude/goose/codex)
         │
         └── WorktreeManager (worktree isolation)
```

Board config can come from three sources (in priority order):

1. CLI flags
2. Environment variables
3. `kardbrd.yml` top-level config (`board_id`, `agent`, `api_url`, `executor`)
