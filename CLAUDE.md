# CLAUDE.md

This file provides guidance to Claude Code when working in this repository.

## Project

This repository builds one Go binary, `kardbrd`.

- `kardbrd agent ...` runs the board automation agent.
- `kardbrd ...` root commands provide client CLI parity for boards, cards, comments, checklists, attachments, links, search, and activity.

## Commands

```bash
go test ./...
go run ./cmd/kardbrd --help
go run ./cmd/kardbrd agent --help
go run ./cmd/kardbrd agent validate

KARDBRD_AGENT_BOARD_ID=board123 \
KARDBRD_TOKEN=tok_xxx \
KARDBRD_AGENT_NAME=mybot \
go run ./cmd/kardbrd agent start
```

Use the downloaded toolchain path in constrained environments when `go` is not on `PATH`:

```bash
GOCACHE=/private/tmp/kardbrd-go-build-cache \
GOMODCACHE=/private/tmp/kardbrd-go-mod-cache \
/private/tmp/go/bin/go test ./...
```

## Architecture

- `cmd/kardbrd`: binary entry point.
- `internal/cli`: Cobra command tree for root client commands and `agent`.
- `internal/api`: HTTP and WebSocket Kardbrd clients.
- `internal/agent`: agent manager, bot card, wizard, rule dispatch, and session state.
- `internal/executor`: Claude, Codex, Goose, and Pi subprocess adapters.
- `internal/rules`: `kardbrd.yml` loading, validation, and matching.
- `internal/scheduler`: cron schedule card creation and dispatch.
- `internal/worktree`: git worktree creation, config symlinks, setup, and cleanup.

## Environment

Root client commands and executor subprocesses use:

```text
KARDBRD_API_URL
KARDBRD_TOKEN
```

Agent-specific settings use:

```text
KARDBRD_AGENT_BOARD_ID
KARDBRD_AGENT_NAME
KARDBRD_AGENT_CWD
KARDBRD_AGENT_TIMEOUT
KARDBRD_AGENT_MAX_CONCURRENT
KARDBRD_AGENT_WORKTREES_DIR
KARDBRD_AGENT_SETUP_CMD
KARDBRD_AGENT_RULES_FILE
KARDBRD_AGENT_EXECUTOR
```

Legacy `KARDBRD_ID`, `KARDBRD_AGENT`, `KARDBRD_URL`, and `AGENT_*` names are rejected with rename diagnostics.
