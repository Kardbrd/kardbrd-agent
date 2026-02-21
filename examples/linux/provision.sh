#!/bin/bash
set -e

# Parse command line arguments
OVERRIDE=false
while [[ $# -gt 0 ]]; do
    case $1 in
        --override)
            OVERRIDE=true
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [--override]"
            echo ""
            echo "Options:"
            echo "  --override    Override existing systemd units and env files"
            echo "                (env files are backed up to {name}.backup)"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

echo "=== Kardbrd Agent Linux Provisioning ==="
if [ "$OVERRIDE" = true ]; then
    echo "Mode: Override existing files"
fi
echo ""

# Detect container runtime
echo "Phase 1: Checking container runtime..."
if command -v podman &> /dev/null; then
    CMD=podman
    echo "  Found podman: $(podman --version)"
elif command -v docker &> /dev/null; then
    CMD=docker
    echo "  Found docker: $(docker --version)"
else
    echo "  Error: neither podman nor docker found."
    echo "  Install one of:"
    echo "    Podman: https://podman.io/docs/installation"
    echo "    Docker: https://docs.docker.com/engine/install/"
    exit 1
fi

echo ""
echo "Enabling user lingering for $USER..."
sudo loginctl enable-linger "$USER"

# Phase 2: User setup (no sudo)
echo ""
echo "Phase 2: Setting up user directories..."

mkdir -p ~/.config/systemd/user
mkdir -p ~/.config/kardbrd-agent
mkdir -p ~/.local/share/kardbrd-agent/workspaces
mkdir -p ~/.local/bin

echo "  Created ~/.config/systemd/user/"
echo "  Created ~/.config/kardbrd-agent/"
echo "  Created ~/.local/share/kardbrd-agent/workspaces/"
echo "  Created ~/.local/bin/"

# Get script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Copy systemd units to user directory
echo ""
echo "Installing systemd units..."
if [ "$OVERRIDE" = true ]; then
    cp "$SCRIPT_DIR"/systemd/*.service ~/.config/systemd/user/
    cp "$SCRIPT_DIR"/systemd/*.timer ~/.config/systemd/user/
    echo "  Overwritten service and timer units in ~/.config/systemd/user/"
else
    for unit in "$SCRIPT_DIR"/systemd/*.service "$SCRIPT_DIR"/systemd/*.timer; do
        unit_name=$(basename "$unit")
        if [ ! -f ~/.config/systemd/user/"$unit_name" ]; then
            cp "$unit" ~/.config/systemd/user/
            echo "  Installed $unit_name"
        else
            echo "  $unit_name already exists (skipped, use --override to replace)"
        fi
    done
fi
echo "  - Agent service: kardbrd-agent"
echo "  - Auto-update: kardbrd-agent-update.timer (every 30 min)"
echo "  - Image cleanup: kardbrd-agent-prune.timer (weekly)"

# Copy scripts (always override - these are idempotent)
echo ""
echo "Installing scripts..."
cp "$SCRIPT_DIR/kardbrd-agent-update.sh" ~/.local/bin/kardbrd-agent-update.sh
cp "$SCRIPT_DIR/kardbrd-agent-shell.sh" ~/.local/bin/kardbrd-agent-shell.sh
chmod +x ~/.local/bin/kardbrd-agent-update.sh
chmod +x ~/.local/bin/kardbrd-agent-shell.sh
echo "  Installed kardbrd-agent-update.sh to ~/.local/bin/"
echo "  Installed kardbrd-agent-shell.sh to ~/.local/bin/"

# Copy env template
echo ""
echo "Setting up environment file..."
env_file=~/.config/kardbrd-agent/agent.env
if [ ! -f "$env_file" ]; then
    cp "$SCRIPT_DIR/env/kardbrd-agent.env.example" "$env_file"
    # Set the detected runtime in the env file
    sed -i "s/^#CONTAINER_CMD=.*/CONTAINER_CMD=$CMD/" "$env_file"
    echo "  Created $env_file (CONTAINER_CMD=$CMD)"
elif [ "$OVERRIDE" = true ]; then
    cp "$env_file" "${env_file}.backup"
    cp "$SCRIPT_DIR/env/kardbrd-agent.env.example" "$env_file"
    sed -i "s/^#CONTAINER_CMD=.*/CONTAINER_CMD=$CMD/" "$env_file"
    echo "  Backed up to agent.env.backup, created new agent.env (CONTAINER_CMD=$CMD)"
else
    echo "  agent.env already exists (skipped, use --override to replace)"
fi

# Reload systemd user daemon
echo ""
echo "Reloading systemd user daemon..."
systemctl --user daemon-reload

# Build image
echo ""
echo "Building kardbrd-agent image..."
REPO_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
$CMD build -t kardbrd-agent "$REPO_DIR"

echo ""
echo "=== Provisioning Complete ==="
echo ""
echo "Next steps:"
echo "  1. Edit ~/.config/kardbrd-agent/agent.env"
echo "     - Set KARDBRD_ID, KARDBRD_TOKEN, KARDBRD_AGENT"
echo "     - Set ANTHROPIC_API_KEY (or mount ~/.claude)"
echo "     - Set GIT_AUTHOR_NAME and GIT_AUTHOR_EMAIL"
echo ""
echo "  2. Create a dedicated SSH key:"
echo "     ssh-keygen -t ed25519 -f ~/.ssh/kardbrd-agent -N ''"
echo "     # Add the public key as a deploy key on your git repo"
echo ""
echo "  3. Create SSH config at ~/.ssh/kardbrd-agent-config:"
echo "     echo -e 'Host *\n    IdentityFile /home/agent/.ssh/id_ed25519\n    StrictHostKeyChecking accept-new' > ~/.ssh/kardbrd-agent-config"
echo ""
echo "  4. Clone your repo into the workspaces directory:"
echo "     git clone git@github.com:yourorg/repo.git ~/.local/share/kardbrd-agent/workspaces/repo"
echo ""
echo "  5. Enable and start the service:"
echo "     systemctl --user enable --now kardbrd-agent"
echo ""
echo "  6. Optional: enable auto-update and image cleanup:"
echo "     systemctl --user enable --now kardbrd-agent-update.timer"
echo "     systemctl --user enable --now kardbrd-agent-prune.timer"
echo ""
echo "  View logs with:"
echo "     journalctl --user -u kardbrd-agent -f"
