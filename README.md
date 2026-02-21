# kardbrd-agent

Proxy agent that listens for @mentions on [kardbrd](https://kardbrd.com) board cards, spawns [Claude CLI](https://docs.anthropic.com/en/docs/claude-code) in isolated git worktrees, and coordinates workflows including automated merging.

## Prerequisites

- Python 3.12+
- [uv](https://docs.astral.sh/uv/)
- [Claude CLI](https://docs.anthropic.com/en/docs/claude-code) (`npm install -g @anthropic-ai/claude-code`)
- git

## Installation

```bash
git clone https://github.com/kardbrd/kardbrd-agent.git
cd kardbrd-agent
uv sync --dev
```

## Quick Start

Configure the agent with environment variables or CLI flags, then start:

```bash
# Via environment variables
export KARDBRD_ID=<board-id>       # Board ID from kardbrd
export KARDBRD_TOKEN=<bot-token>   # Bot token from kardbrd
export KARDBRD_AGENT=<agent-name>  # Agent name for @mentions
kardbrd-agent start

# Or via CLI flags
kardbrd-agent start --board-id <board-id> --token <bot-token> --name <agent-name>
```

## Commands

| Command | Description |
|---------|-------------|
| `kardbrd-agent start` | Start listening for @mentions and rule-based events |
| `kardbrd-agent validate` | Validate a `kardbrd.yml` rules file |

### Start Options

```bash
kardbrd-agent start \
  --board-id <id> \           # Board ID (or KARDBRD_ID env var)
  --token <token> \           # Bot token (or KARDBRD_TOKEN env var)
  --name <name> \             # Agent name (or KARDBRD_AGENT env var)
  --api-url <url> \           # API URL (default: https://app.kardbrd.com, or KARDBRD_URL)
  --cwd /path/to/repo \       # Working directory for Claude (or AGENT_CWD)
  --timeout 7200 \            # Max execution time in seconds (default: 3600, or AGENT_TIMEOUT)
  --max-concurrent 5 \        # Max parallel Claude sessions (default: 3, or AGENT_MAX_CONCURRENT)
  --worktrees-dir /path \     # Worktree directory (default: parent of --cwd, or AGENT_WORKTREES_DIR)
  --setup-cmd 'npm install' \ # Setup command for worktrees (or AGENT_SETUP_CMD)
  --rules kardbrd.yml         # Rules file (default: <cwd>/kardbrd.yml, or AGENT_RULES_FILE)
```

### Rules (`kardbrd.yml`)

Create a `kardbrd.yml` in your repo root to define declarative automation rules. Rules match WebSocket events (mentions, card moves, reactions) and trigger Claude sessions or built-in actions.

```yaml
board_id: 0gl5MlBZ
agent: MyBot

rules:
  - name: Explore new ideas
    event: card_created
    list: Ideas
    model: opus
    action: /ke

  - name: Stop on red flag
    event: reaction_added
    emoji: "🛑"
    action: __stop__
```

See [CLAUDE.md](CLAUDE.md) for the full `kardbrd.yml` format and all available conditions.

Validate your rules file:

```bash
kardbrd-agent validate              # validates ./kardbrd.yml
kardbrd-agent validate path/to/kardbrd.yml
```

## How It Works

1. Configure the agent with your board ID, bot token, and agent name (via env vars or CLI flags)
2. The agent connects via WebSocket and listens for @mention comments (e.g., `@coder fix the login bug`) and rule-matched events
3. When triggered, it creates an isolated git worktree for the card and spawns Claude CLI with the card context
4. Claude works in the worktree and can post comments/updates back to the card via MCP tools
5. Optionally, the agent can run an automated merge workflow (rebase, test, squash merge) via rules

## Deployment

### Docker (recommended)

The recommended deployment model: your project's dev image is the base, and kardbrd-agent + Claude CLI are injected into it. This ensures the agent has access to your full toolchain (pnpm, uv, cargo, etc.).

#### Using the standalone Dockerfile

The repo includes a standalone [`Dockerfile`](Dockerfile) that builds a ready-to-run image with Python, Node.js, Claude CLI, and uv:

```bash
docker build -t kardbrd-agent .
docker run --rm \
  -e KARDBRD_ID=<board-id> \
  -e KARDBRD_TOKEN=<bot-token> \
  -e KARDBRD_AGENT=<agent-name> \
  -e ANTHROPIC_API_KEY=<api-key> \
  -v ./repository:/home/agent/repository \
  -v ./workspaces:/home/agent/workspaces \
  -v ./ssh/id_ed25519:/home/agent/.ssh/id_ed25519:ro \
  kardbrd-agent start --cwd /home/agent/repository
```

#### Using Docker Compose

Add an `agent` target to your existing Dockerfile that injects kardbrd-agent and Claude CLI:

```dockerfile
# your existing stages...
FROM node:22-slim AS base
RUN corepack enable
RUN apt-get update && apt-get install -y git openssh-client && rm -rf /var/lib/apt/lists/*

# add this at the end
FROM base AS agent
RUN apt-get update && apt-get install -y python3 python3-venv && rm -rf /var/lib/apt/lists/*
RUN npm install -g @anthropic-ai/claude-code
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
RUN useradd -m -s /bin/bash -u 1000 agent && mkdir -p /app/state && chown agent:agent /app/state
USER agent
WORKDIR /home/agent/repository
ENTRYPOINT ["uvx", "--from", "git+https://github.com/kardbrd/kardbrd-agent.git", "kardbrd-agent"]
CMD ["start", "--cwd", "/home/agent/repository"]
```

The directory structure separates the repository and worktrees:

```
kardbrd-project/
├── docker-compose.yml
├── .env                    # ANTHROPIC_API_KEY, KARDBRD_ID, KARDBRD_TOKEN, KARDBRD_AGENT
├── repository/             # Your cloned git repo (Dockerfile has agent target)
├── workspaces/             # Worktrees created automatically (one per card)
├── claude/                 # Claude CLI home
└── ssh/                    # Dedicated SSH deploy key
```

See [`examples/docker/`](examples/docker/) for the full setup guide with docker-compose.yml, environment variables, per-stack examples, and `Dockerfile.agent` for repos without an existing Dockerfile.

### Platform-specific examples

- **Linux** (`examples/linux/`): systemd services with Podman/Docker, auto-update
- **macOS** (`examples/macos/`): launchd daemon with auto-update and rollback

## Environment Variables

All configuration can be set via environment variables:

| Variable | CLI flag | Required | Description |
|---|---|---|---|
| `KARDBRD_ID` | `--board-id` | Yes | Board ID |
| `KARDBRD_TOKEN` | `--token` | Yes | Bot authentication token |
| `KARDBRD_AGENT` | `--name` | Yes | Agent name for @mentions |
| `KARDBRD_URL` | `--api-url` | No | API base URL (default: `https://app.kardbrd.com`) |
| `ANTHROPIC_API_KEY` | — | Yes | Anthropic API key (for Claude CLI) |
| `AGENT_CWD` | `--cwd` | No | Working directory for Claude |
| `AGENT_TIMEOUT` | `--timeout` | No | Max seconds per session (default: 3600) |
| `AGENT_MAX_CONCURRENT` | `--max-concurrent` | No | Parallel Claude sessions (default: 3) |
| `AGENT_WORKTREES_DIR` | `--worktrees-dir` | No | Where worktrees are created (default: parent of cwd) |
| `AGENT_SETUP_CMD` | `--setup-cmd` | No | Run in each worktree after creation (e.g. `npm install`) |
| `AGENT_RULES_FILE` | `--rules` | No | Path to `kardbrd.yml` (default: `<cwd>/kardbrd.yml`) |

## Development

```bash
# Run tests
uv run pytest

# Run a specific test
uv run pytest kardbrd_agent/tests/test_merge_workflow.py

# Lint and format
uv run pre-commit run --all-files
```

## License

[MIT](LICENSE)
