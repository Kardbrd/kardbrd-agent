# Linux (systemd)

Run kardbrd-agent as a systemd user service with Podman or Docker.

## Prerequisites

- Linux with systemd (Debian, Ubuntu, Fedora, etc.)
- **Podman** (recommended, rootless) or **Docker**
- A git repository the agent will work on

## Quick start

```bash
# 1. Clone kardbrd-agent
git clone https://github.com/kardbrd/kardbrd-agent.git
cd kardbrd-agent/examples/linux

# 2. Run provisioning script
./provision.sh

# 3. Configure environment
vi ~/.config/kardbrd-agent/agent.env

# 4. Set up SSH key and clone your repo

# 5. Start the service
systemctl --user enable --now kardbrd-agent
```

Re-provision an existing installation:

```bash
./provision.sh --override
```

## Container runtime

Scripts and systemd units support both Podman and Docker. Configure via `CONTAINER_CMD` in the env file:

```bash
CONTAINER_CMD=podman   # or docker
```

!!! tip
    Podman is recommended for systemd user services — it's daemonless and doesn't require a root daemon.

## Directory structure

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
        └── kbn-<id>/                      # Worktrees (automatic)
```

## SSH key setup

Generate a **dedicated** SSH key:

```bash
ssh-keygen -t ed25519 -f ~/.ssh/kardbrd-agent -N "" -C "kardbrd-agent"
cat ~/.ssh/kardbrd-agent.pub  # Add as deploy key with write access
```

Create an SSH config:

```bash
cat > ~/.ssh/kardbrd-agent-config << 'EOF'
Host *
    IdentityFile /home/agent/.ssh/id_ed25519
    StrictHostKeyChecking accept-new
EOF
```

Clone your repo:

```bash
GIT_SSH_COMMAND="ssh -i ~/.ssh/kardbrd-agent" \
    git clone git@github.com:yourorg/yourrepo.git \
    ~/.local/share/kardbrd-agent/workspaces/repo
```

## Configuration

Edit `~/.config/kardbrd-agent/agent.env`:

```bash
CONTAINER_CMD=podman

# Board configuration (required)
KARDBRD_ID=<board-id>
KARDBRD_TOKEN=<bot-token>
KARDBRD_AGENT=<agent-name>

# LLM provider
ANTHROPIC_API_KEY=sk-ant-...

# Optional
LOG_LEVEL=INFO
GIT_AUTHOR_NAME=Kardbrd Agent
GIT_AUTHOR_EMAIL=agent@example.com
```

## Service management

```bash
# Start / enable
systemctl --user enable --now kardbrd-agent

# Stop
systemctl --user stop kardbrd-agent

# Restart
systemctl --user restart kardbrd-agent

# Status
systemctl --user status kardbrd-agent

# Logs
journalctl --user -u kardbrd-agent -f
journalctl --user -u kardbrd-agent --since "1 hour ago"
```

### Shell access

```bash
~/.local/bin/kardbrd-agent-shell.sh
# Or: podman exec -it kardbrd-agent /bin/sh
```

## Image updates

### Manual

```bash
~/.local/bin/kardbrd-agent-update.sh
```

### Automatic (every 30 minutes)

```bash
systemctl --user enable --now kardbrd-agent-update.timer
systemctl --user list-timers  # Check status
```

Change frequency by editing `~/.config/systemd/user/kardbrd-agent-update.timer`:

```ini
[Timer]
OnCalendar=*:0/30      # Every 30 minutes (default)
OnCalendar=hourly      # Every hour
OnCalendar=daily       # Once a day
```

### Image cleanup

```bash
systemctl --user enable --now kardbrd-agent-prune.timer
```

## Troubleshooting

**Service won't start:**

```bash
systemctl --user status kardbrd-agent
journalctl --user -u kardbrd-agent --no-pager -n 50
podman ps -a | grep kardbrd-agent   # Check for stale containers
```

**SSH / git push fails:**

```bash
podman exec kardbrd-agent ssh -T git@github.com
podman exec kardbrd-agent ls -la /home/agent/.ssh/
```

**Permission errors:** The container runs as UID 1000:

```bash
sudo chown -R 1000:1000 ~/.local/share/kardbrd-agent
```
