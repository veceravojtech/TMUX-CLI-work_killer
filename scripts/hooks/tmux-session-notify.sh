#!/usr/bin/env bash
#
# tmux-session-notify.sh
# Logs Claude Code session lifecycle events (start, end, stop)
#
# Usage: Called by SessionStart, SessionEnd, Stop hooks with event type as arg
# Environment: Expects $TMUX_WINDOW_UUID to be set
# Output: Appends JSON line to .tmux-cli/logs/sessions.jsonl

set -euo pipefail

# Get event type from first argument
EVENT_TYPE="${1:-unknown}"

# Validate session membership first
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if ! "$SCRIPT_DIR/tmux-validate-session.sh"; then
    # Not a valid window - exit silently
    exit 0
fi

# Get window UUID
WINDOW_UUID="${TMUX_WINDOW_UUID:-}"

if [[ -z "$WINDOW_UUID" ]]; then
    # Should not happen after validation, but safeguard
    exit 0
fi

# Read hook input from stdin
HOOK_INPUT=$(cat)

# Create log directory if doesn't exist
LOG_DIR=".tmux-cli/logs"
mkdir -p "$LOG_DIR"

# Check for jq
if ! command -v jq &> /dev/null; then
    # jq not available - can't parse JSON
    exit 0
fi

# Parse input and construct log entry
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
SESSION_ID=$(echo "$HOOK_INPUT" | jq -r '.session_id // "unknown"')
CWD=$(echo "$HOOK_INPUT" | jq -r '.cwd // "unknown"')

# Try to get window name from .tmux-session
SESSION_FILE=".tmux-session"
WINDOW_NAME="unknown"
if [[ -f "$SESSION_FILE" ]]; then
    WINDOW_NAME=$(jq -r --arg uuid "$WINDOW_UUID" '.windows[]? | select(.uuid == $uuid) | .name // "unknown"' "$SESSION_FILE" 2>/dev/null || echo "unknown")
fi

# Map event type to friendly name
case "$EVENT_TYPE" in
    start)
        EVENT="SessionStart"
        ;;
    end)
        EVENT="SessionEnd"
        ;;
    stop)
        EVENT="Stop"
        ;;
    *)
        EVENT="$EVENT_TYPE"
        ;;
esac

# Construct log entry as JSON
LOG_ENTRY=$(jq -n \
    --arg ts "$TIMESTAMP" \
    --arg event "$EVENT" \
    --arg uuid "$WINDOW_UUID" \
    --arg name "$WINDOW_NAME" \
    --arg sid "$SESSION_ID" \
    --arg cwd "$CWD" \
    '{
        timestamp: $ts,
        event: $event,
        window_uuid: $uuid,
        window_name: $name,
        session_id: $sid,
        cwd: $cwd
    }')

# Append to sessions log file
LOG_FILE="$LOG_DIR/sessions.jsonl"
echo "$LOG_ENTRY" >> "$LOG_FILE"

exit 0
