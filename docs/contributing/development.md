# Development Setup

## Prerequisites

- Go 1.24+
- git
- Optional executor CLIs for manual agent testing

## Clone

```bash
git clone https://github.com/kardbrd/kardbrd-agent.git
cd kardbrd-agent
```

## Project Structure

```text
cmd/kardbrd/          # binary entry point
internal/api/         # HTTP and WebSocket clients
internal/agent/       # agent manager and board automation
internal/cli/         # command tree
internal/config/      # env and flag config
internal/executor/    # executor adapters
internal/rules/       # kardbrd.yml loading and matching
internal/scheduler/   # cron schedules
internal/worktree/    # git worktrees
```

## Commands

```bash
go test ./...
go run ./cmd/kardbrd --help
go run ./cmd/kardbrd agent validate
go build -o kardbrd ./cmd/kardbrd
```

## Local Agent Run

```bash
export KARDBRD_AGENT_BOARD_ID=<board-id>
export KARDBRD_TOKEN=<bot-token>
export KARDBRD_AGENT_NAME=<agent-name>
export KARDBRD_AGENT_CWD=/path/to/test/repo

go run ./cmd/kardbrd agent start
```
