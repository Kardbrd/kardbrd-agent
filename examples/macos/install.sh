#!/bin/bash
# Install script for kardbrd-manager launchd service on macOS
#
# This script:
# 1. Creates required directories
# 2. Checks for existing subscription
# 3. Generates the plist from template with user-specific paths
# 4. Installs and loads the launchd plist

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="${REPO_DIR:-$(cd "$SCRIPT_DIR/../.." && pwd)}"
STATE_DIR="${STATE_DIR:-$HOME/.local/share/kardbrd/kardbrd-manager}"
PLIST_TEMPLATE="$SCRIPT_DIR/com.kardbrd.manager.plist"
PLIST_DST="$HOME/Library/LaunchAgents/com.kardbrd.manager.plist"
LABEL="com.kardbrd.manager"

log() {
    echo "[install] $*"
}

error() {
    echo "[install] ERROR: $*" >&2
    exit 1
}

# Check if already running
if launchctl list 2>/dev/null | grep -q "$LABEL"; then
    log "Service is currently running, stopping..."
    launchctl unload "$PLIST_DST" 2>/dev/null || true
fi

# Create directories
log "Creating directories..."
mkdir -p "$STATE_DIR/state"
mkdir -p "$STATE_DIR/logs"
mkdir -p "$HOME/Library/LaunchAgents"

# Check subscription exists
if [[ ! -d "$STATE_DIR/state" ]] || [[ -z "$(ls -A "$STATE_DIR/state" 2>/dev/null)" ]]; then
    log ""
    log "No subscription found. Please subscribe first:"
    log ""
    log "  AGENT_STATE_DIR=$STATE_DIR/state uv run kardbrd-manager sub <setup-url>"
    log ""
    log "Then run this script again."
    exit 1
fi

log "Subscription found in $STATE_DIR/state"

# Check that source plist template exists
if [[ ! -f "$PLIST_TEMPLATE" ]]; then
    error "Plist template not found: $PLIST_TEMPLATE"
fi

# Generate plist from template with user-specific paths
log "Generating plist (REPO_DIR=$REPO_DIR)..."
sed -e "s|__HOME__|$HOME|g" \
    -e "s|__REPO_DIR__|$REPO_DIR|g" \
    "$PLIST_TEMPLATE" > "$PLIST_DST"

# Load and start
log "Loading service..."
launchctl load "$PLIST_DST"

# Wait a moment for startup
sleep 2

# Verify it's running
if launchctl list 2>/dev/null | grep -q "$LABEL"; then
    PID=$(launchctl list | grep "$LABEL" | awk '{print $1}')
    log ""
    log "Kardbrd manager installed and started (PID: $PID)"
    log ""
    log "  Logs: tail -f $STATE_DIR/logs/stdout.log"
    log "  Status: launchctl list | grep kardbrd"
    log "  Stop: launchctl unload $PLIST_DST"
    log "  Restart: launchctl kickstart -k gui/\$(id -u)/$LABEL"
else
    log ""
    log "Service loaded but may not be running. Check logs:"
    log "  tail -f $STATE_DIR/logs/stderr.log"
fi
