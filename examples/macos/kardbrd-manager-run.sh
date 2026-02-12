#!/bin/bash
# Wrapper script for kardbrd-manager that handles:
# 1. Git updates before starting
# 2. Rollback to previous HEAD if new code crashes on startup
#
# This script is designed to be run by launchd which will restart it on exit.

set -e

# Configuration - these are set by the plist EnvironmentVariables,
# or can be overridden when running manually.
REPO_DIR="${REPO_DIR:?REPO_DIR must be set}"
STATE_DIR="${STATE_DIR:-$HOME/.local/share/kardbrd/kardbrd-manager}"
UV="${UV:-$(which uv 2>/dev/null || echo /opt/homebrew/bin/uv)}"

# Internal state files
LAST_GOOD_HEAD_FILE="$STATE_DIR/.last-good-head"
LAST_PULL_HEAD_FILE="$STATE_DIR/.last-pull-head"

# Ensure state directory exists
mkdir -p "$STATE_DIR/state"
mkdir -p "$STATE_DIR/logs"

cd "$REPO_DIR"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
}

# Check if we need to rollback from a failed update
check_rollback() {
    if [[ -f "$LAST_PULL_HEAD_FILE" ]]; then
        PULLED_HEAD=$(cat "$LAST_PULL_HEAD_FILE")
        CURRENT_HEAD=$(git rev-parse HEAD)

        if [[ "$PULLED_HEAD" == "$CURRENT_HEAD" ]] && [[ -f "$LAST_GOOD_HEAD_FILE" ]]; then
            LAST_GOOD=$(cat "$LAST_GOOD_HEAD_FILE")
            log "Previous run crashed after git pull, rolling back to $LAST_GOOD"
            git checkout "$LAST_GOOD" --quiet
            rm -f "$LAST_PULL_HEAD_FILE"
        fi
    fi
}

# Check for and apply updates
check_updates() {
    # Fetch latest from origin
    if ! git fetch origin main --quiet 2>/dev/null; then
        log "Warning: git fetch failed, continuing with current code"
        return 0
    fi

    LOCAL=$(git rev-parse HEAD)
    REMOTE=$(git rev-parse origin/main 2>/dev/null || echo "")

    if [[ -z "$REMOTE" ]]; then
        log "Warning: Could not get remote HEAD, skipping update"
        return 0
    fi

    if [[ "$LOCAL" != "$REMOTE" ]]; then
        log "Update available: $LOCAL -> $REMOTE"

        # Save current HEAD as last known good (only if we're not already rolled back)
        if [[ ! -f "$LAST_PULL_HEAD_FILE" ]]; then
            echo "$LOCAL" > "$LAST_GOOD_HEAD_FILE"
        fi

        # Pull the update
        if git pull origin main --quiet 2>/dev/null; then
            NEW_HEAD=$(git rev-parse HEAD)
            echo "$NEW_HEAD" > "$LAST_PULL_HEAD_FILE"
            log "Updated to $NEW_HEAD"

            # Sync dependencies
            log "Syncing dependencies..."
            if ! $UV sync --quiet 2>/dev/null; then
                log "Warning: uv sync failed, continuing anyway"
            fi
        else
            log "Warning: git pull failed, continuing with current code"
        fi
    else
        log "Code is up to date at $LOCAL"
    fi
}

# Cleanup on successful shutdown
cleanup_on_success() {
    rm -f "$LAST_PULL_HEAD_FILE"
    log "Clean shutdown, cleared pull marker"
}

# Trap for graceful shutdown
trap cleanup_on_success EXIT

# Main execution
log "Starting kardbrd-manager wrapper"
log "  REPO_DIR: $REPO_DIR"
log "  STATE_DIR: $STATE_DIR"

check_rollback
check_updates

log "Starting kardbrd-manager..."

# Export state directory for kardbrd-manager
export AGENT_STATE_DIR="$STATE_DIR/state"

# Remove trap before exec (exec replaces the process, trap won't run)
trap - EXIT

# Start kardbrd-manager (exec replaces this process)
exec $UV run --project "$REPO_DIR" kardbrd-manager start \
    --cwd "$REPO_DIR" \
    --timeout 7200 \
    --max-concurrent 7
