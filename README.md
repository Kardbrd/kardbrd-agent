# kardbrd-agent

Proxy agent that listens for @mentions on [kardbrd](https://kardbrd.com) board cards, spawns AI agents ([Claude CLI](https://docs.anthropic.com/en/docs/claude-code) or [Goose](https://block.github.io/goose/)) in isolated git worktrees, and coordinates workflows including automated merging.

## Prerequisites

- Python 3.12+
- [uv](https://docs.astral.sh/uv/)
- git
- **One of the following AI agent CLIs:**
  - [Claude CLI](https://docs.anthropic.com/en/docs/claude-code) (`npm install -g @anthropic-ai/claude-code`) — default executor
  - [Goose](https://block.github.io/goose/) (`curl -fsSL https://github.com/block/goose/releases/latest/download/install.sh | sh`) — open-source, multi-provider executor

## Installation

```bash
git clone https://github.com/kardbrd/kardbrd-agent.git
cd kardbrd-agent
uv sync --dev
```

## Authentication Setup

kardbrd-agent requires authentication with both **kardbrd** (for board access) and your **LLM provider** (for AI execution).

### kardbrd Authentication

Get your bot token from the kardbrd board settings:
1. Open your board → Settings → Bots
2. Create a bot or copy the existing bot token
3. Set `KARDBRD_TOKEN=<bot-token>` as environment variable

### LLM Provider Authentication

#### Option A: Claude CLI (default executor)

Claude CLI requires an Anthropic API key:

1. Get an API key from [console.anthropic.com](https://console.anthropic.com/)
2. Set the environment variable:
   ```bash
   export ANTHROPIC_API_KEY=sk-ant-...
   ```
3. Verify authentication:
   ```bash
   claude auth status
   ```

**Subscription note:** Claude CLI requires an active Anthropic API subscription. If the API key expires or is revoked, kardbrd-agent will post an error comment on the card with re-authentication instructions.

#### Option B: Goose (multi-provider executor)

Goose supports 20+ LLM providers. Configure your chosen provider:

1. Install Goose:
   ```bash
   curl -fsSL https://github.com/block/goose/releases/latest/download/install.sh | sh
   ```

2. Set your provider and API key:
   ```bash
   # For Anthropic
   export GOOSE_PROVIDER=anthropic
   export ANTHROPIC_API_KEY=sk-ant-...

   # For OpenAI
   export GOOSE_PROVIDER=openai
   export OPENAI_API_KEY=sk-...

   # For Ollama (local, no API key needed)
   export GOOSE_PROVIDER=ollama

   # For Google Gemini
   export GOOSE_PROVIDER=google
   export GOOGLE_API_KEY=...

   # For OpenRouter
   export GOOSE_PROVIDER=openrouter
   export OPENROUTER_API_KEY=...

   # For AWS Bedrock
   export GOOSE_PROVIDER=bedrock
   export AWS_ACCESS_KEY_ID=...
   export AWS_SECRET_ACCESS_KEY=...
   export AWS_REGION=us-east-1
   ```

3. Start kardbrd-agent with the Goose executor:
   ```bash
   kardbrd-agent start --executor goose
   ```

**Tip:** Run `goose configure` to interactively set up your provider. Goose can also store keys in your system keychain.

### Re-authentication

If your LLM provider credentials expire:
- kardbrd-agent checks authentication **at startup** and **before each card session**
- On auth failure, the agent posts an error comment on the card with specific re-auth instructions
- A 🛑 reaction is added to the triggering comment
- The agent continues running and will retry auth on the next card event

To re-authenticate without restarting:
- **Claude:** Run `claude auth login` or update `ANTHROPIC_API_KEY`
- **Goose:** Update the provider-specific API key env var, or run `goose configure`

## Quick Start

### With Claude (default)

```bash
export KARDBRD_ID=<board-id>
export KARDBRD_TOKEN=<bot-token>
export KARDBRD_AGENT=<agent-name>
export ANTHROPIC_API_KEY=<api-key>
kardbrd-agent start
```

### With Goose

```bash
export KARDBRD_ID=<board-id>
export KARDBRD_TOKEN=<bot-token>
export KARDBRD_AGENT=<agent-name>
export GOOSE_PROVIDER=anthropic
export ANTHROPIC_API_KEY=<api-key>
kardbrd-agent start --executor goose
```

### Via CLI flags

```bash
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
  --executor claude \         # Executor: "claude" (default) or "goose" (or AGENT_EXECUTOR)
  --cwd /path/to/repo \       # Working directory (or AGENT_CWD)
  --timeout 7200 \            # Max execution time in seconds (default: 3600, or AGENT_TIMEOUT)
  --max-concurrent 5 \        # Max parallel sessions (default: 3, or AGENT_MAX_CONCURRENT)
  --worktrees-dir /path \     # Worktree directory (default: parent of --cwd, or AGENT_WORKTREES_DIR)
  --setup-cmd 'npm install' \ # Setup command for worktrees (or AGENT_SETUP_CMD)
  --rules kardbrd.yml         # Rules file (default: <cwd>/kardbrd.yml, or AGENT_RULES_FILE)
```

### Rules (`kardbrd.yml`)

Create a `kardbrd.yml` in your repo root to define declarative automation rules. Rules match WebSocket events (mentions, card moves, reactions) and trigger Claude sessions or built-in actions.

```yaml
board_id: 0gl5MlBZ
agent: MyBot
executor: goose          # optional: "claude" (default) or "goose"

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

1. Configure the agent with your board ID, bot token, agent name, and executor (via env vars or CLI flags)
2. The agent connects via WebSocket and listens for @mention comments (e.g., `@coder fix the login bug`) and rule-matched events
3. When triggered, it creates an isolated git worktree for the card and spawns the configured executor (Claude CLI or Goose) with the card context
4. The executor works in the worktree and can post comments/updates back to the card via MCP tools
5. Optionally, the agent can run an automated merge workflow (rebase, test, squash merge) via rules

## Deployment

### Docker (recommended)

The recommended deployment model: your project's dev image is the base, and kardbrd-agent + your chosen executor CLI are injected into it. This ensures the agent has access to your full toolchain (pnpm, uv, cargo, etc.).

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

#### Docker with Goose

```bash
docker run --rm \
  -e KARDBRD_ID=<board-id> \
  -e KARDBRD_TOKEN=<bot-token> \
  -e KARDBRD_AGENT=<agent-name> \
  -e AGENT_EXECUTOR=goose \
  -e GOOSE_PROVIDER=anthropic \
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
# Add Goose (optional, for goose executor)
# RUN curl -fsSL https://github.com/block/goose/releases/latest/download/install.sh | sh
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
├── .env                    # KARDBRD_ID, KARDBRD_TOKEN, KARDBRD_AGENT, ANTHROPIC_API_KEY (+ AGENT_EXECUTOR, GOOSE_PROVIDER for Goose)
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
| `AGENT_EXECUTOR` | `--executor` | No | Executor type: `claude` (default) or `goose` |
| `ANTHROPIC_API_KEY` | — | Claude | Anthropic API key (for Claude CLI) |
| `GOOSE_PROVIDER` | — | Goose | LLM provider for Goose (e.g. `anthropic`, `openai`, `ollama`) |
| `AGENT_CWD` | `--cwd` | No | Working directory |
| `AGENT_TIMEOUT` | `--timeout` | No | Max seconds per session (default: 3600) |
| `AGENT_MAX_CONCURRENT` | `--max-concurrent` | No | Parallel sessions (default: 3) |
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
