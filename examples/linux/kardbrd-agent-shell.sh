#!/bin/bash
#
# Interactive shell for the kardbrd-agent container.
#
# If the systemd service is running, exec into the existing container.
# Otherwise, start a temporary container with the same volumes.
#
# Usage: kardbrd-agent-shell.sh

# Auto-detect container runtime: prefer podman, fall back to docker
if [ -n "$CONTAINER_CMD" ]; then
    CMD="$CONTAINER_CMD"
elif command -v podman &> /dev/null; then
    CMD=podman
elif command -v docker &> /dev/null; then
    CMD=docker
else
    echo "Error: neither podman nor docker found"
    exit 1
fi

STATE_DIR="$HOME/.local/share/kardbrd-agent/state"
WORKSPACES_DIR="$HOME/.local/share/kardbrd-agent/workspaces"
IMAGE="kardbrd-agent"

# Check if container is already running
if $CMD ps --format "{{.Names}}" | grep -q "^kardbrd-agent$"; then
    echo "Entering running kardbrd-agent container..."
    if $CMD exec -it kardbrd-agent /bin/sh; then
        exit 0
    fi
    echo ""
    echo "Container exited. Starting a temporary container instead..."
    echo ""
fi

# Start temporary container
echo "Starting temporary container (using $CMD)..."
echo ""
echo "State directory: $STATE_DIR"
echo ""
echo "Useful commands:"
echo "  kardbrd-agent sub <setup-url>   # Subscribe to a board"
echo "  kardbrd-agent status            # Show subscription status"
echo "  kardbrd-agent unsub             # Unsubscribe from all boards"
echo ""

# Ensure directories exist
mkdir -p "$STATE_DIR" "$WORKSPACES_DIR"

EXTRA_ARGS=""
if [ -f "$HOME/.ssh/kardbrd-agent" ]; then
    EXTRA_ARGS="-v $HOME/.ssh/kardbrd-agent:/home/agent/.ssh/id_ed25519:ro"
fi
if [ -f "$HOME/.ssh/kardbrd-agent-config" ]; then
    EXTRA_ARGS="$EXTRA_ARGS -v $HOME/.ssh/kardbrd-agent-config:/home/agent/.ssh/config:ro"
fi
if [ -d "$HOME/.claude" ]; then
    EXTRA_ARGS="$EXTRA_ARGS -v $HOME/.claude:/home/agent/.claude"
fi

# shellcheck disable=SC2086
exec $CMD run --rm -it \
    --name kardbrd-agent-shell \
    --entrypoint /bin/sh \
    -e AGENT_STATE_DIR=/app/state \
    -v "$STATE_DIR:/app/state" \
    -v "$WORKSPACES_DIR:/home/agent/workspaces" \
    $EXTRA_ARGS \
    "$IMAGE"
