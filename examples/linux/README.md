# Linux Systemd Setup

Run kardbrd-agent as a systemd user service with Podman or Docker.

## Prerequisites

- Linux with systemd (Debian, Ubuntu, Fedora, etc.)
- **Podman** (recommended, rootless) or **Docker**
- A git repository the agent will work on

## Quick Start

```bash
# 1. Clone kardbrd-agent
git clone https://github.com/kardbrd/kardbrd-agent.git
cd kardbrd-agent/examples/linux

# 2. Run provisioning script (auto-detects podman/docker, builds image, installs units)
./provision.sh

# 3. Configure environment (board credentials, API key, git identity)
vi ~/.config/kardbrd-agent/agent.env

# 4. Set up SSH key and clone your repo (see sections below)

# 5. Start the service
systemctl --user enable --now kardbrd-agent
```

**Re-provisioning (update existing installation):**

```bash
./provision.sh --override
```

## Container Runtime

All scripts and systemd units support both **Podman** and **Docker**. The runtime is configured via `CONTAINER_CMD` in `~/.config/kardbrd-agent/agent.env`:

```bash
# Set during provisioning, or change manually:
CONTAINER_CMD=podman   # or docker
```

- **Systemd units** read `CONTAINER_CMD` from the env file (defaults to `podman`)
- **Shell scripts** auto-detect: prefer podman, fall back to docker, or respect `CONTAINER_CMD` env var

Podman is recommended for systemd user services because it's daemonless — no root Docker daemon required.

## Directory Structure

After provisioning:

```
~/.config/
├── kardbrd-agent/
│   └── agent.env                          # Environment variables
└── systemd/user/
    ├── kardbrd-agent.service              # Main agent service
    ├── kardbrd-agent-update.service       # Image update oneshot
    ├── kardbrd-agent-update.timer         # Auto-update (every 30 min)
    ├── kardbrd-agent-prune.service        # Image cleanup oneshot
    └── kardbrd-agent-prune.timer          # Weekly cleanup

~/.local/
├── bin/
│   ├── kardbrd-agent-update.sh            # Pull image and restart
│   └── kardbrd-agent-shell.sh             # Interactive container shell
└── share/kardbrd-agent/
    └── workspaces/
        ├── repo/                          # Your cloned git repository
        └── kbn-<id>/                      # Worktrees (created automatically)
```

## SSH Key Setup

Generate a **dedicated** SSH key for the agent — never use your personal keys. The agent runs Claude with `--dangerously-skip-permissions`, so any key it has access to could be used in unpredictable ways.

```bash
# Generate a dedicated key
ssh-keygen -t ed25519 -f ~/.ssh/kardbrd-agent -N "" -C "kardbrd-agent"

# Add the public key as a deploy key on your git repo (with write access)
cat ~/.ssh/kardbrd-agent.pub
```

Create an SSH config for the agent:

```bash
cat > ~/.ssh/kardbrd-agent-config << 'EOF'
Host *
    IdentityFile /home/agent/.ssh/id_ed25519
    StrictHostKeyChecking accept-new
EOF
```

Then clone your repo:

```bash
# Use the agent's SSH key to clone
GIT_SSH_COMMAND="ssh -i ~/.ssh/kardbrd-agent" \
    git clone git@github.com:yourorg/yourrepo.git \
    ~/.local/share/kardbrd-agent/workspaces/repo
```

## Configuration

### Environment File

Edit `~/.config/kardbrd-agent/agent.env`:

```bash
# Container runtime (set by provision.sh)
CONTAINER_CMD=podman

# Board configuration (required)
KARDBRD_ID=<board-id>
KARDBRD_TOKEN=<bot-token>
KARDBRD_AGENT=<agent-name>

# Anthropic API key (optional if ~/.claude is mounted)
ANTHROPIC_API_KEY=sk-ant-...

# Logging: DEBUG, INFO (default), WARNING, ERROR
LOG_LEVEL=INFO

# Git identity
GIT_AUTHOR_NAME=Kardbrd Agent
GIT_AUTHOR_EMAIL=agent@example.com
```

## Service Management

### Start / Stop

```bash
# Enable and start (runs on boot)
systemctl --user enable --now kardbrd-agent

# Stop
systemctl --user stop kardbrd-agent

# Disable autostart
systemctl --user disable kardbrd-agent

# Restart
systemctl --user restart kardbrd-agent
```

### Check Status

```bash
systemctl --user status kardbrd-agent
```

### View Logs

```bash
# Follow logs
journalctl --user -u kardbrd-agent -f

# Recent logs
journalctl --user -u kardbrd-agent --since "1 hour ago"

# Filter by card ID
journalctl --user -u kardbrd-agent | grep "card_id=abc12345"
```

### Shell Access

```bash
# Interactive shell in a temporary container
~/.local/bin/kardbrd-agent-shell.sh

# Or exec into running container
podman exec -it kardbrd-agent /bin/sh   # or: docker exec -it ...
```

## Image Updates

### Manual Update

```bash
~/.local/bin/kardbrd-agent-update.sh
```

The script pulls the latest image and restarts the service only if the image changed.

### Automatic Updates

```bash
# Enable (checks every 30 minutes)
systemctl --user enable --now kardbrd-agent-update.timer

# Check timer status
systemctl --user list-timers

# Disable
systemctl --user disable --now kardbrd-agent-update.timer
```

To change the update frequency, edit `~/.config/systemd/user/kardbrd-agent-update.timer`:

```ini
[Timer]
OnCalendar=*:0/30      # Every 30 minutes (default)
OnCalendar=hourly      # Every hour
OnCalendar=daily       # Once a day
```

Then reload: `systemctl --user daemon-reload`

### Image Cleanup

Old images accumulate as updates are pulled. Enable weekly cleanup:

```bash
systemctl --user enable --now kardbrd-agent-prune.timer

# Manual cleanup
podman image prune -af --filter "until=24h"
```

## Debugging

### Debug Logging

1. Set `LOG_LEVEL=DEBUG` in `~/.config/kardbrd-agent/agent.env`
2. Restart: `systemctl --user restart kardbrd-agent`
3. View logs: `journalctl --user -u kardbrd-agent -f`

### Troubleshooting

**Service won't start:**

```bash
# Check service status and recent logs
systemctl --user status kardbrd-agent
journalctl --user -u kardbrd-agent --no-pager -n 50

# Check if container exists from a previous run
podman ps -a | grep kardbrd-agent

# Remove stale container (ExecStartPre does this automatically)
podman rm -f kardbrd-agent
```

**SSH / git push fails:**

```bash
# Test SSH from inside the container
podman exec kardbrd-agent ssh -T git@github.com

# Verify the key is mounted
podman exec kardbrd-agent ls -la /home/agent/.ssh/
```

**"Missing required config" on start:**

Ensure `KARDBRD_ID`, `KARDBRD_TOKEN`, and `KARDBRD_AGENT` are set in `~/.config/kardbrd-agent/agent.env`.

**Permission errors on volumes:**

The container runs as UID 1000. Ensure local directories are owned correctly:

```bash
sudo chown -R 1000:1000 ~/.local/share/kardbrd-agent
```

## Files Reference

| File | Purpose |
|---|---|
| `provision.sh` | Initial setup script |
| `kardbrd-agent-update.sh` | Pull image and restart service |
| `kardbrd-agent-shell.sh` | Interactive container shell |
| `env/kardbrd-agent.env.example` | Environment file template |
| `systemd/kardbrd-agent.service` | Main agent service unit |
| `systemd/kardbrd-agent-update.service` | Image update oneshot |
| `systemd/kardbrd-agent-update.timer` | Auto-update timer (30 min) |
| `systemd/kardbrd-agent-prune.service` | Image cleanup oneshot |
| `systemd/kardbrd-agent-prune.timer` | Weekly cleanup timer |
