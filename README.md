# kardbrd

[![Docs](https://github.com/Kardbrd/kardbrd-agent/actions/workflows/docs.yml/badge.svg)](https://kardbrd.github.io/kardbrd-agent/)

Single Go binary for Kardbrd board automation and CLI access. `kardbrd agent ...` runs the agent daemon; every other `kardbrd ...` command is the Kardbrd client CLI.

## Prerequisites

- Go 1.24+ to build from source
- git
- One executor CLI for agent work:
  - Claude CLI (`claude`) - default executor
  - Goose (`goose`)
  - Codex CLI (`codex`)
  - Pi (`pi`)

## Build

```bash
git clone https://github.com/Kardbrd/kardbrd-agent.git
cd kardbrd-agent
go build -o kardbrd ./cmd/kardbrd
```

## Quick Start

```bash
export KARDBRD_API_URL=https://app.kardbrd.com
export KARDBRD_TOKEN=<bot-token>
export KARDBRD_AGENT_BOARD_ID=<board-id>
export KARDBRD_AGENT_NAME=<agent-name>
export KARDBRD_AGENT_CWD=/path/to/your/repo
export ANTHROPIC_API_KEY=<api-key>
# For Codex executor:
# export OPENAI_API_KEY=<api-key>

./kardbrd agent start
```

Equivalent flags:

```bash
./kardbrd --token <bot-token> agent start \
  --board-id <board-id> \
  --name <agent-name> \
  --cwd /path/to/your/repo
```

## Commands

| Command | Description |
| --- | --- |
| `kardbrd agent start` | Start the agent daemon |
| `kardbrd agent validate [kardbrd.yml]` | Validate rules |
| `kardbrd board ...` | Board client commands |
| `kardbrd card ...` | Card client commands |
| `kardbrd comment ...` | Comment client commands |
| `kardbrd checklist ...` | Checklist client commands |
| `kardbrd attachment ...` | Attachment client commands |
| `kardbrd link ...` | Link client commands |
| `kardbrd search ...` | Search cards |
| `kardbrd activity ...` | Read activity |

## Agent Configuration

| Environment variable | Flag | Default |
| --- | --- | --- |
| `KARDBRD_API_URL` | `--api-url` | `https://app.kardbrd.com` |
| `KARDBRD_TOKEN` | `--token` | required |
| `KARDBRD_AGENT_BOARD_ID` | `--board-id` | required |
| `KARDBRD_AGENT_NAME` | `--name` | required |
| `KARDBRD_AGENT_CWD` | `--cwd` | current directory |
| `KARDBRD_AGENT_TIMEOUT` | `--timeout` | `3600` |
| `KARDBRD_AGENT_MAX_CONCURRENT` | `--max-concurrent` | `3` |
| `KARDBRD_AGENT_WORKTREES_DIR` | `--worktrees-dir` | parent of cwd |
| `KARDBRD_AGENT_SETUP_CMD` | `--setup-cmd` | none |
| `KARDBRD_AGENT_RULES_FILE` | `--rules` | `<cwd>/kardbrd.yml` |
| `KARDBRD_AGENT_EXECUTOR` | `--executor` | `claude` |

Legacy names such as `KARDBRD_ID`, `KARDBRD_AGENT`, `KARDBRD_URL`, and `AGENT_*` are rejected with explicit rename messages.

## Rules

Create `kardbrd.yml` in the target repository:

```yaml
board_id: 0gl5MlBZ
agent: MyBot
executor: codex

rules:
  - name: Explore new ideas
    event: card_created
    list: Ideas
    model: sonnet
    action: /ke

  - name: Stop on red flag
    event: reaction_added
    emoji: "🛑"
    action: __stop__
```

Validate it:

```bash
kardbrd agent validate
kardbrd agent validate path/to/kardbrd.yml
```

## Docker

```bash
docker build -t kardbrd .
docker run --rm \
  -e KARDBRD_API_URL=https://app.kardbrd.com \
  -e KARDBRD_TOKEN=<bot-token> \
  -e KARDBRD_AGENT_BOARD_ID=<board-id> \
  -e KARDBRD_AGENT_NAME=<agent-name> \
  -e KARDBRD_AGENT_CWD=/home/agent/repository \
  -e ANTHROPIC_API_KEY=<api-key> \
  -e OPENAI_API_KEY=<api-key> \
  -v ./repository:/home/agent/repository \
  -v ./workspaces:/home/agent/workspaces \
  -v ./codex:/home/agent/.codex \
  kardbrd
```

The Docker image builds the Go binary and does not install Python or uv for normal runtime.

## Development

```bash
go test ./...
go run ./cmd/kardbrd --help
go run ./cmd/kardbrd agent --help
```
