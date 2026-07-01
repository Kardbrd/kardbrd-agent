# kardbrd

`kardbrd` is a single Go binary for Kardbrd board automation and CLI access.

- `kardbrd agent start` runs the automation agent.
- `kardbrd agent validate` validates `kardbrd.yml`.
- `kardbrd board`, `card`, `comment`, `checklist`, `attachment`, `link`, `search`, and `activity` provide client CLI commands.

## Quick Start

```bash
git clone https://github.com/kardbrd/kardbrd-agent.git
cd kardbrd-agent
go build -o kardbrd ./cmd/kardbrd

export KARDBRD_AGENT_BOARD_ID=<board-id>
export KARDBRD_TOKEN=<bot-token>
export KARDBRD_AGENT_NAME=<agent-name>
export KARDBRD_AGENT_CWD=/path/to/repo
export ANTHROPIC_API_KEY=<api-key>

./kardbrd agent start
```

[:material-rocket-launch: Getting Started](getting-started/installation.md){ .md-button .md-button--primary }
[:material-cog: Configuration](configuration/cli.md){ .md-button }
[:material-server: Deployment](deployment/index.md){ .md-button }

## Features

- Full client CLI and agent daemon in one binary.
- Git worktree isolation per card.
- Rules and schedules from `kardbrd.yml`.
- Claude, Goose, Codex, and Pi executor adapters.
- Bot card status, wizard card, and skill discovery.
