#!/bin/bash
# Uninstall script for kardbrd-manager launchd service on macOS
#
# This script:
# 1. Stops and unloads the service
# 2. Removes the plist
# 3. Optionally removes state/logs

set -e

PLIST="$HOME/Library/LaunchAgents/com.kardbrd.manager.plist"
STATE_DIR="$HOME/.local/share/kardbrd/kardbrd-manager"
LABEL="com.kardbrd.manager"

log() {
    echo "[uninstall] $*"
}

# Stop and unload if running
if launchctl list 2>/dev/null | grep -q "$LABEL"; then
    log "Stopping service..."
    launchctl unload "$PLIST" 2>/dev/null || true
fi

# Remove plist
if [[ -f "$PLIST" ]]; then
    log "Removing plist..."
    rm -f "$PLIST"
fi

log ""
log "Kardbrd manager uninstalled"
log ""

# Check if state exists
if [[ -d "$STATE_DIR" ]]; then
    log "State and logs preserved in: $STATE_DIR"
    log ""
    log "To remove all data, run:"
    log "  rm -rf $STATE_DIR"
fi
