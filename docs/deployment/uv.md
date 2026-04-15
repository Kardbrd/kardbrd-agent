# Running with uv (no Docker)

Run kardbrd-agent directly on the host using [uv](https://docs.astral.sh/uv/). This is the simplest deployment when the agent's target repository is already checked out locally and you don't need container isolation.

**When to use:**

- Your project's toolchain is already installed on the host
- You want a lightweight setup without Docker overhead
- Dev machines or simple single-project deployments

## Prerequisites

- Python 3.12+
- [uv](https://docs.astral.sh/uv/) (`curl -LsSf https://astral.sh/uv/install.sh | sh`)
- git
- An AI executor CLI (Claude, Goose, or Codex)
- Your project's toolchain installed on the host

## Quick start

### Option A: `uvx` (no clone needed)

```bash
export KARDBRD_ID=<board-id>
export KARDBRD_TOKEN=<bot-token>
export KARDBRD_AGENT=<agent-name>
export ANTHROPIC_API_KEY=<api-key>

uvx --from "git+https://github.com/Kardbrd/kardbrd-agent.git" \
  kardbrd-agent start --cwd /path/to/your/repo
```

Pin a specific version:

```bash
uvx --from "git+https://github.com/Kardbrd/kardbrd-agent.git@v1.0.0" \
  kardbrd-agent start --cwd /path/to/your/repo
```

### Option B: Clone and run

```bash
git clone https://github.com/Kardbrd/kardbrd-agent.git
cd kardbrd-agent && uv sync

uv run kardbrd-agent start --cwd /path/to/your/repo
```

## Environment file

Create a `.kardbrd.env` for your project:

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
# AGENT_MAX_CONCURRENT=3
```

Source it before running:

```bash
set -a && source ~/projects/my-project/.kardbrd.env && set +a
uvx --from "git+https://github.com/Kardbrd/kardbrd-agent.git" \
  kardbrd-agent start --cwd ~/projects/my-project
```

!!! warning "Security"
    Add `.kardbrd.env` to your `.gitignore` — it contains API keys.

## Background services

### systemd (Linux)

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

```bash
systemctl --user daemon-reload
systemctl --user enable --now kardbrd-agent-myproject
journalctl --user -u kardbrd-agent-myproject -f
```

### launchd (macOS)

Save to `~/Library/LaunchAgents/com.kardbrd.agent.myproject.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
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
        <key>KARDBRD_ID</key><string>your-board-id</string>
        <key>KARDBRD_TOKEN</key><string>your-bot-token</string>
        <key>KARDBRD_AGENT</key><string>your-agent-name</string>
        <key>ANTHROPIC_API_KEY</key><string>your-api-key</string>
    </dict>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>StandardOutPath</key>
    <string>/tmp/kardbrd-agent-myproject.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/kardbrd-agent-myproject.err</string>
</dict>
</plist>
```

```bash
launchctl load ~/Library/LaunchAgents/com.kardbrd.agent.myproject.plist
```

## Multiple projects

Run one agent per project with separate configs:

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

For production, use separate systemd services or launchd plists.

## Auto-updates

**With `uvx`:** Restarting the service fetches the latest version automatically. Force a cache refresh:

```bash
uv cache clean kardbrd-agent
```

**With `uv run` (cloned):** Pull and sync:

```bash
cd ~/kardbrd-agent && git pull && uv sync
systemctl --user restart kardbrd-agent-myproject
```
