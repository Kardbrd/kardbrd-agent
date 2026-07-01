# Architecture Overview

`kardbrd` is a Go CLI with one binary and shared internals for client commands and agent automation.

## Event Flow

```text
Kardbrd WebSocket event
        |
        v
internal/agent.Manager
        |
        +--> internal/rules.Engine
        +--> internal/worktree.Manager
        +--> internal/executor adapter
        +--> internal/api.Client
```

## Core Packages

- `internal/cli`: command routing and output formatting.
- `internal/api`: HTTP, markdown, attachment, WebSocket, and stream helpers.
- `internal/agent`: daemon lifecycle, bot card, wizard, event routing, sessions, rules.
- `internal/rules`: YAML config loading, validation, model aliases, matching.
- `internal/scheduler`: cron schedules that find or create cards.
- `internal/worktree`: branch naming, git worktrees, symlinks, setup command, cleanup.
- `internal/executor`: Claude, Codex, Goose, and Pi subprocess adapters.

## Concurrency

The agent uses a buffered semaphore sized by `KARDBRD_AGENT_MAX_CONCURRENT`. A card ID can only have one active session at a time.

## Configuration

Client commands use `KARDBRD_API_URL` and `KARDBRD_TOKEN`. Agent-only settings use `KARDBRD_AGENT_*` names.
