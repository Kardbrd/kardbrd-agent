# macOS (launchd)

Run kardbrd-agent as a macOS launchd daemon with auto-start, auto-restart, auto-update, and rollback on failure.

## Features

- **Auto-start on login** — starts automatically when you log in
- **Auto-restart on crash** — launchd restarts the service if it exits unexpectedly
- **Auto-update** — pulls latest code from git on each restart
- **Rollback on failure** — if new code crashes, automatically reverts to last known good commit
- **Backoff protection** — 30-second throttle between restarts

## Prerequisites

- [uv](https://docs.astral.sh/uv/) installed
- Repository cloned on your Mac
- Git SSH access configured

## Quick setup

### 1. Subscribe to a board

```bash
cd /path/to/kardbrd-client
AGENT_STATE_DIR=~/.local/share/kardbrd/kardbrd-manager/state \
  uv run kardbrd-manager sub <setup-url>
```

### 2. Install the service

```bash
./examples/macos/install.sh

# Or with custom repo path:
REPO_DIR=/path/to/repo ./examples/macos/install.sh
```

The install script creates directories, generates the plist, copies it to `~/Library/LaunchAgents/`, and starts the service.

### 3. Verify

```bash
launchctl list | grep kardbrd
# Expected: 12345  0  com.kardbrd.manager
```

## Directory structure

```
~/.local/share/kardbrd/kardbrd-manager/
├── state/              # Subscription state
├── logs/
│   ├── stdout.log      # Standard output
│   └── stderr.log      # Standard error
├── .last-good-head     # Last known working commit
└── .last-pull-head     # Commit from most recent pull
```

## Management

```bash
# Status
launchctl list | grep kardbrd
launchctl print gui/$(id -u)/com.kardbrd.manager

# Logs
tail -f ~/.local/share/kardbrd/kardbrd-manager/logs/*.log

# Stop
launchctl unload ~/Library/LaunchAgents/com.kardbrd.manager.plist

# Restart (triggers update check)
launchctl kickstart -k gui/$(id -u)/com.kardbrd.manager
```

## How updates work

1. **On each start** — fetches `origin/main`, pulls if new commits, runs `uv sync`
2. **On crash after update** — wrapper detects crash, rolls back to last known good commit
3. **On clean shutdown** (SIGTERM) — clears pull marker, next start won't roll back

Force an update:

```bash
launchctl kickstart -k gui/$(id -u)/com.kardbrd.manager
```

## Configuration

### Install paths

| Variable | Description | Default |
|----------|-------------|---------|
| `REPO_DIR` | Path to repository | Auto-detected |
| `STATE_DIR` | State and logs directory | `~/.local/share/kardbrd/kardbrd-manager` |

### Runtime options

Edit `kardbrd-manager-run.sh` to customize:

| Option | Default | Description |
|--------|---------|-------------|
| `--port` | `8765` | MCP HTTP server port |
| `--max-concurrent` | `7` | Max parallel sessions |
| `--timeout` | `7200` | Session timeout (seconds) |
| `--cwd` | `$REPO_DIR` | Working directory |

## Troubleshooting

**Service not starting** — check subscription and error log:

```bash
ls ~/.local/share/kardbrd/kardbrd-manager/state/
cat ~/.local/share/kardbrd/kardbrd-manager/logs/stderr.log
```

**Crash looping** — check for rollback:

```bash
tail -100 ~/.local/share/kardbrd/kardbrd-manager/logs/stderr.log
cat ~/.local/share/kardbrd/kardbrd-manager/.last-good-head
```

**Git pull fails** — verify SSH: `ssh-add -l` and manual pull.

**"Throttled"** — normal launchd behavior, 30-second wait between restarts.

## Uninstalling

```bash
./examples/macos/uninstall.sh

# Remove state:
rm -rf ~/.local/share/kardbrd/kardbrd-manager
```
