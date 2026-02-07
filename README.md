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

```bash
# Subscribe to a board (using the setup URL from kardbrd)
kardbrd-agent sub <setup-url>

# Start listening for @mentions
kardbrd-agent start

# Start in unified mode (enables MCP tools for Claude)
kardbrd-agent start --port 8765
```

## Commands

| Command | Description |
|---------|-------------|
| `kardbrd-agent sub <setup-url>` | Subscribe to a board |
| `kardbrd-agent start` | Start listening for @mentions |
| `kardbrd-agent status` | Show subscription status |
| `kardbrd-agent config <board-id>` | Configure merge settings for a board |
| `kardbrd-agent unsub --yes` | Unsubscribe from all boards |
| `kardbrd-agent proxy-mcp` | Run MCP proxy server (stdio) |

### Start Options

```bash
kardbrd-agent start \
  --cwd /path/to/repo \      # Working directory for Claude
  --timeout 7200 \            # Max execution time in seconds (default: 3600)
  --max-concurrent 5 \        # Max parallel Claude sessions (default: 3)
  --port 8765                 # MCP server port (enables unified mode)
```

## How It Works

1. You subscribe to a kardbrd board, which gives the agent a bot token
2. The agent connects via WebSocket and listens for @mention comments (e.g., `@coder fix the login bug`)
3. When mentioned, it creates an isolated git worktree for the card and spawns Claude CLI with the card context
4. Claude works in the worktree and can post comments/updates back to the card via MCP tools
5. Optionally, when a card is moved to a merge queue list, the agent runs an automated merge workflow (rebase, test, squash merge)

## Deployment

The recommended deployment model: your project's dev image is the base, and kardbrd-agent + Claude CLI are injected into it. This ensures the agent has access to your full toolchain (pnpm, uv, cargo, etc.).

### Docker Compose (recommended)

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
WORKDIR /home/agent/repository
ENTRYPOINT ["uvx", "--from", "git+https://github.com/kardbrd/kardbrd-agent.git", "kardbrd-agent"]
CMD ["start", "--cwd", "/home/agent/repository"]
```

The directory structure separates the repository and worktrees:

```
kardbrd-project/
├── docker-compose.yml
├── repository/             # Your cloned git repo (Dockerfile has agent target)
├── workspaces/             # Worktrees created automatically (one per card)
├── state/                  # Subscription state
├── claude/                 # Claude CLI home
└── ssh/                    # Dedicated SSH deploy key
```

See [`examples/docker/`](examples/docker/) for the full setup guide with docker-compose.yml, environment variables, per-stack examples, and `Dockerfile.agent` for repos without an existing Dockerfile.

### Platform-specific examples

- **Linux** (`examples/linux/`): systemd services with auto-update
- **macOS** (`examples/macos/`): launchd daemon with auto-update and rollback

## Development

```bash
# Run tests
uv run pytest

# Run a specific test
uv run pytest kardbrd_agent/tests/test_merge_workflow.py

# Lint and format
pre-commit run --all-files
```

## License

[MIT](LICENSE)
