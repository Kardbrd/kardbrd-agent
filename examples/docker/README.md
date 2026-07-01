# Docker Example

This example runs the Go `kardbrd` binary in a container and starts the agent with:

```bash
kardbrd agent start --cwd /home/agent/repository
```

## Setup

```bash
mkdir -p repository workspaces claude ssh
cp .env.example .env
```

Edit `.env`:

```bash
KARDBRD_API_URL=https://app.kardbrd.com
KARDBRD_TOKEN=<bot-token>
KARDBRD_AGENT_BOARD_ID=<board-id>
KARDBRD_AGENT_NAME=<agent-name>
ANTHROPIC_API_KEY=<api-key>
```

Mount your target repository at `./repository`.

## Run

```bash
docker compose up -d --build
docker compose logs -f agent
```

## Configuration

| Variable | Purpose |
| --- | --- |
| `KARDBRD_API_URL` | Kardbrd API base URL |
| `KARDBRD_TOKEN` | Bot token |
| `KARDBRD_AGENT_BOARD_ID` | Board ID |
| `KARDBRD_AGENT_NAME` | Agent mention name |
| `KARDBRD_AGENT_CWD` | Repository path in the container |
| `KARDBRD_AGENT_WORKTREES_DIR` | Worktree parent directory |
| `KARDBRD_AGENT_SETUP_CMD` | Optional setup command for new worktrees |
| `KARDBRD_AGENT_MAX_CONCURRENT` | Parallel sessions |
| `KARDBRD_AGENT_EXECUTOR` | `claude`, `goose`, `codex`, or `pi` |

The default Dockerfile installs Claude CLI. Add other executor CLIs to your image when using a different executor.
