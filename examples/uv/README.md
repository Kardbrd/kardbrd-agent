# Running with uv (no Docker)

Run kardbrd-agent directly on the host using [uv](https://docs.astral.sh/uv/). This is the simplest deployment when the agent's target repository is already checked out locally and you don't need container isolation.

**When to use this approach:**

- The agent works on a project that has its own toolchain installed on the host (Node, Python, Go, etc.)
- You want a lightweight setup without Docker overhead
- Each project gets its own kardbrd-agent process pointed at its own repo

> **Note:** kardbrd-agent's own CI/CD uses Docker (see [`examples/docker/`](../docker/)), but for other repositories running the agent directly with `uv` is a clean alternative.

## Prerequisites

- Python 3.12+
- [uv](https://docs.astral.sh/uv/) (`curl -LsSf https://astral.sh/uv/install.sh | sh`)
- git
- An AI executor CLI:
  - [Claude CLI](https://docs.anthropic.com/en/docs/claude-code) (`npm install -g @anthropic-ai/claude-code`)
  - or [Goose](https://block.github.io/goose/) (`curl -fsSL https://github.com/block/goose/releases/latest/download/install.sh | sh`)
- Your project's toolchain (npm, pnpm, cargo, etc.) installed on the host

## Quick Start

### Option A: Run directly with `uvx` (no clone needed)

`uvx` fetches kardbrd-agent from GitHub and runs it in a temporary venv. No local clone required:

```bash
# Set environment
export KARDBRD_ID=<board-id>
export KARDBRD_TOKEN=<bot-token>
export KARDBRD_AGENT=<agent-name>
export ANTHROPIC_API_KEY=<api-key>

# Run the agent, pointing at your project's repo
uvx --from "git+https://github.com/Kardbrd/kardbrd-agent.git" \
  kardbrd-agent start --cwd /path/to/your/repo
```

This always fetches the latest version. To pin a version, use a git ref:

```bash
uvx --from "git+https://github.com/Kardbrd/kardbrd-agent.git@v1.2.0" \
  kardbrd-agent start --cwd /path/to/your/repo
```

### Option B: Clone and run with `uv run`

Clone kardbrd-agent and run from source. Useful for development or when you want a fixed local copy:

```bash
git clone https://github.com/Kardbrd/kardbrd-agent.git
cd kardbrd-agent
uv sync

# Run the agent
uv run kardbrd-agent start --cwd /path/to/your/repo
```

## Directory Structure

When running with `uv`, the agent creates worktrees as siblings to the target repo (same as Docker):

```
~/projects/
├── my-project/                 # Your project repo (--cwd target)
│   ├── kardbrd.yml             # Optional automation rules
│   └── ...
├── kbn-abc12345/               # Worktrees (created automatically)
│   └── ...
└── kardbrd-agent/              # Only if using Option B
```

Override the worktree location with `--worktrees-dir`:

```bash
kardbrd-agent start \
  --cwd ~/projects/my-project \
  --worktrees-dir ~/kardbrd-workspaces
```

## Environment File

Create an `.env` file for your project and source it before starting the agent:

```bash
# ~/projects/my-project/.kardbrd.env
KARDBRD_ID=<board-id>
KARDBRD_TOKEN=<bot-token>
KARDBRD_AGENT=<agent-name>
ANTHROPIC_API_KEY=<api-key>

# Optional
# AGENT_EXECUTOR=goose
# GOOSE_PROVIDER=anthropic
# AGENT_SETUP_CMD="pnpm install"
# AGENT_TEST_CMD="pnpm test"
# AGENT_MAX_CONCURRENT=3
# AGENT_TIMEOUT=3600
# LOG_LEVEL=INFO
```

Then run:

```bash
set -a && source ~/projects/my-project/.kardbrd.env && set +a
uvx --from "git+https://github.com/Kardbrd/kardbrd-agent.git" \
  kardbrd-agent start --cwd ~/projects/my-project
```

> **Security:** Add `.kardbrd.env` to your `.gitignore` — it contains API keys.

## Running as a Background Service

### Using systemd (Linux)

Create a user service that runs the agent for a specific project:

```ini
# ~/.config/systemd/user/kardbrd-agent-myproject.service
[Unit]
Description=kardbrd-agent for my-project
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=%h/projects/my-project/.kardbrd.env
ExecStart=%h/.local/bin/uvx --from "git+https://github.com/Kardbrd/kardbrd-agent.git" kardbrd-agent start --cwd %h/projects/my-project
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
```

Enable and start:

```bash
systemctl --user daemon-reload
systemctl --user enable --now kardbrd-agent-myproject
journalctl --user -u kardbrd-agent-myproject -f
```

### Using launchd (macOS)

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.kardbrd.agent.myproject</string>
    <key>ProgramArguments</key>
    <array>
        <string>/Users/you/.local/bin/uvx</string>
        <string>--from</string>
        <string>git+https://github.com/Kardbrd/kardbrd-agent.git</string>
        <string>kardbrd-agent</string>
        <string>start</string>
        <string>--cwd</string>
        <string>/Users/you/projects/my-project</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>KARDBRD_ID</key>
        <string>your-board-id</string>
        <key>KARDBRD_TOKEN</key>
        <string>your-bot-token</string>
        <key>KARDBRD_AGENT</key>
        <string>your-agent-name</string>
        <key>ANTHROPIC_API_KEY</key>
        <string>your-api-key</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/kardbrd-agent-myproject.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/kardbrd-agent-myproject.err</string>
</dict>
</plist>
```

Save to `~/Library/LaunchAgents/com.kardbrd.agent.myproject.plist` and load:

```bash
launchctl load ~/Library/LaunchAgents/com.kardbrd.agent.myproject.plist
```

## Multiple Projects

Run one agent per project. Each gets its own board config, worktree space, and process:

```bash
# Project A
KARDBRD_ID=board-a KARDBRD_TOKEN=tok-a KARDBRD_AGENT=BotA \
  uvx --from "git+https://github.com/Kardbrd/kardbrd-agent.git" \
  kardbrd-agent start --cwd ~/projects/project-a &

# Project B
KARDBRD_ID=board-b KARDBRD_TOKEN=tok-b KARDBRD_AGENT=BotB \
  uvx --from "git+https://github.com/Kardbrd/kardbrd-agent.git" \
  kardbrd-agent start --cwd ~/projects/project-b &
```

For production, use separate systemd services or launchd plists per project (see above).

## Auto-Updates

### With `uvx`

`uvx` resolves the latest version from GitHub on each run. Restarting the service picks up new versions automatically. To force an update without restarting:

```bash
uv cache clean kardbrd-agent
```

### With `uv run` (cloned repo)

Pull and sync periodically:

```bash
cd ~/kardbrd-agent && git pull && uv sync
systemctl --user restart kardbrd-agent-myproject
```

Or use a systemd timer:

```ini
# ~/.config/systemd/user/kardbrd-agent-update.service
[Unit]
Description=Update kardbrd-agent

[Service]
Type=oneshot
WorkingDirectory=%h/kardbrd-agent
ExecStart=/bin/bash -c "git pull && %h/.local/bin/uv sync"
ExecStartPost=/usr/bin/systemctl --user restart kardbrd-agent-myproject

# ~/.config/systemd/user/kardbrd-agent-update.timer
[Unit]
Description=Check for kardbrd-agent updates

[Timer]
OnCalendar=*:0/30
Persistent=true

[Install]
WantedBy=timers.target
```

```bash
systemctl --user enable --now kardbrd-agent-update.timer
```

## Comparison with Docker

| | uv (this guide) | Docker ([`examples/docker/`](../docker/)) |
|---|---|---|
| **Isolation** | Shares host environment | Full container isolation |
| **Toolchain** | Uses host-installed tools | Baked into image |
| **Setup** | One command (`uvx`) | Dockerfile + Compose |
| **Multi-project** | One process per project | One container per project |
| **Updates** | Restart fetches latest (uvx) | Rebuild image |
| **Best for** | Dev machines, simple deployments | Production, reproducible envs |
