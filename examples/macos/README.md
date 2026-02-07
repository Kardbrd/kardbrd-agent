# macOS Kardbrd Manager Daemon Setup

This guide explains how to run the kardbrd-manager as a daemon on macOS using launchd.

## Features

- **Auto-start on login**: Starts automatically when you log in
- **Auto-restart on crash**: launchd restarts the service if it exits unexpectedly
- **Auto-update**: Pulls latest code from git on each restart
- **Rollback on failure**: If new code crashes, automatically reverts to last known good commit
- **Backoff protection**: 30-second throttle between restarts prevents CPU burn

## Prerequisites

1. **uv installed**: Available at `~/.local/bin/uv` or `/opt/homebrew/bin/uv`
2. **Repository cloned**: The kardbrd-client repository on your Mac
3. **Git SSH access**: SSH key configured for git pull without password prompts

## Quick Setup

### 1. Subscribe to a board (one-time)

```bash
cd /path/to/kardbrd-client
AGENT_STATE_DIR=~/.local/share/kardbrd/kardbrd-manager/state \
  uv run kardbrd-manager sub <setup-url>
```

### 2. Install the service

```bash
# Run the install script (auto-detects repo path)
./examples/macos/install.sh

# Or specify a custom repo path
REPO_DIR=/path/to/kardbrd-client ./examples/macos/install.sh
```

The install script:

- Creates required directories
- Verifies subscription exists
- Generates the plist from template with your paths
- Copies plist to `~/Library/LaunchAgents/`
- Loads and starts the service

### 3. Verify it's running

```bash
# Check if loaded (shows PID and exit status)
launchctl list | grep kardbrd

# Expected output when running:
# 12345  0  com.kardbrd.manager
#  ^PID  ^exit code (0 = running)
```

## Directory Structure

```
~/.local/share/kardbrd/kardbrd-manager/
├── state/              # Subscription state (board config JSON)
├── logs/
│   ├── stdout.log      # Standard output
│   └── stderr.log      # Standard error
├── .last-good-head     # Last known working git commit
└── .last-pull-head     # Commit from most recent git pull
```

## Management Commands

### Check status

```bash
# Basic status
launchctl list | grep kardbrd

# Detailed info
launchctl print gui/$(id -u)/com.kardbrd.manager
```

### View logs

```bash
# Stdout log (startup info, update messages)
tail -f ~/.local/share/kardbrd/kardbrd-manager/logs/stdout.log

# Error log
tail -f ~/.local/share/kardbrd/kardbrd-manager/logs/stderr.log

# Both logs
tail -f ~/.local/share/kardbrd/kardbrd-manager/logs/*.log
```

### Stop the service

```bash
# Unload (stops and prevents restart)
launchctl unload ~/Library/LaunchAgents/com.kardbrd.manager.plist
```

### Restart the service

```bash
# Force restart (triggers update check)
launchctl kickstart -k gui/$(id -u)/com.kardbrd.manager

# Or unload + load
launchctl unload ~/Library/LaunchAgents/com.kardbrd.manager.plist
launchctl load ~/Library/LaunchAgents/com.kardbrd.manager.plist
```

## How Updates Work

The wrapper script (`kardbrd-manager-run.sh`) handles updates automatically:

1. **On each start** (including restarts):

   - Fetches `origin/main`
   - If new commits exist, pulls and runs `uv sync`
   - Records the new HEAD as "just pulled"

2. **If the service crashes after an update**:

   - launchd restarts the wrapper
   - Wrapper detects crash after pull (pull marker exists)
   - Checks out previous "last known good" commit
   - Starts with rolled-back code

3. **On clean shutdown** (SIGTERM):
   - Clears the pull marker
   - Next start won't trigger rollback

### Manual update trigger

To force an update check:

```bash
launchctl kickstart -k gui/$(id -u)/com.kardbrd.manager
```

## Configuration

### Customizing paths

The plist is a **template** — `install.sh` substitutes `__HOME__` and `__REPO_DIR__` placeholders with your actual paths at install time.

To change paths after installation, edit `~/Library/LaunchAgents/com.kardbrd.manager.plist` and reload, or re-run install.sh with environment variables:

```bash
REPO_DIR=/custom/path STATE_DIR=/custom/state ./examples/macos/install.sh
```

Key environment variables for install.sh:

| Variable    | Description                 | Default                                    |
| ----------- | --------------------------- | ------------------------------------------ |
| `REPO_DIR`  | Path to repository          | Auto-detected from script location         |
| `STATE_DIR` | State and logs directory    | `~/.local/share/kardbrd/kardbrd-manager`   |

### Customizing kardbrd-manager options

Edit `kardbrd-manager-run.sh` to change the command-line arguments:

| Option               | Current Value | Description                  |
| -------------------- | ------------- | ---------------------------- |
| `--port 8765`        | 8765          | MCP HTTP server port         |
| `--max-concurrent 7` | 7             | Max parallel Claude sessions |
| `--timeout 7200`     | 7200          | Session timeout (2 hours)    |
| `--cwd`              | `$REPO_DIR`   | Working directory for Claude |

## Troubleshooting

### Service not starting

1. Check if subscription exists:

   ```bash
   ls ~/.local/share/kardbrd/kardbrd-manager/state/
   ```

   If empty, run the subscription command first.

2. Test running manually:

   ```bash
   REPO_DIR=/path/to/kardbrd-client ./examples/macos/kardbrd-manager-run.sh
   ```

3. Check error log:
   ```bash
   cat ~/.local/share/kardbrd/kardbrd-manager/logs/stderr.log
   ```

### Crash looping

With `KeepAlive` enabled, launchd restarts the service on crash. The 30-second throttle prevents rapid restarts.

If you see continuous restarts:

```bash
# Check recent errors
tail -100 ~/.local/share/kardbrd/kardbrd-manager/logs/stderr.log

# Check if rollback happened
cat ~/.local/share/kardbrd/kardbrd-manager/.last-good-head
git log -1 $(cat ~/.local/share/kardbrd/kardbrd-manager/.last-good-head)
```

### Git pull fails

The wrapper continues with current code if git fails. Check:

- SSH key is in ssh-agent: `ssh-add -l`
- Can pull manually: `cd /path/to/kardbrd-client && git pull origin main`

### "Throttled" in launchctl output

This is normal - launchd enforces the 30-second throttle between restarts. Wait and the service will start.

## Uninstalling

```bash
# Run the uninstall script
./examples/macos/uninstall.sh

# Or manually:
launchctl unload ~/Library/LaunchAgents/com.kardbrd.manager.plist
rm ~/Library/LaunchAgents/com.kardbrd.manager.plist
```

State is preserved in `~/.local/share/kardbrd/kardbrd-manager/`. To remove completely:

```bash
rm -rf ~/.local/share/kardbrd/kardbrd-manager
```

## Files

| File                        | Purpose                                   |
| --------------------------- | ----------------------------------------- |
| `kardbrd-manager-run.sh`    | Wrapper script with git update + rollback |
| `com.kardbrd.manager.plist` | launchd service definition (template)     |
| `install.sh`                | One-command installation                  |
| `uninstall.sh`              | Clean removal                             |

## Comparison with Linux systemd

| Feature      | macOS launchd                                 | Linux systemd                             |
| ------------ | --------------------------------------------- | ----------------------------------------- |
| Service file | `~/Library/LaunchAgents/*.plist`              | `~/.config/systemd/user/*.service`        |
| Load         | `launchctl load <plist>`                      | `systemctl --user enable --now <service>` |
| Restart      | `launchctl kickstart -k gui/$(id -u)/<label>` | `systemctl --user restart <service>`      |
| Logs         | Custom paths in plist                         | `journalctl --user -u <service>`          |
| Auto-restart | `<key>KeepAlive</key><dict>...</dict>`        | `Restart=on-failure`                      |
