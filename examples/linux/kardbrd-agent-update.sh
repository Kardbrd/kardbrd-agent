#!/bin/bash
set -e

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

IMAGE="kardbrd-agent"

# Get current image ID
old_id=$($CMD images --format '{{.ID}}' "$IMAGE" 2>/dev/null || echo "")

echo "Pulling $IMAGE (using $CMD)..."
if $CMD pull "$IMAGE"; then
    new_id=$($CMD images --format '{{.ID}}' "$IMAGE")

    echo "  Old ID: ${old_id:-<none>}"
    echo "  New ID: $new_id"

    if [ -z "$old_id" ]; then
        echo "  First pull — image is new"
    elif [ "$old_id" != "$new_id" ]; then
        echo "  Image changed"
    else
        echo "  Image unchanged"
        echo "Done!"
        exit 0
    fi

    # Restart if running
    if systemctl --user is-active --quiet kardbrd-agent; then
        echo "  Restarting kardbrd-agent..."
        systemctl --user restart kardbrd-agent
        echo "  Restarted"
    else
        echo "  kardbrd-agent is not running (skipped restart)"
    fi
else
    echo "  Failed to pull $IMAGE"
    exit 1
fi

echo "Done!"
