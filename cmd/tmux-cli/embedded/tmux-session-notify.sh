#!/usr/bin/env bash
#
# tmux-session-notify.sh
# Logs Claude Code session lifecycle events (start, end, stop)
#
# Usage: Called by SessionStart, SessionEnd, Stop hooks with event type as arg
# Environment: Expects $TMUX_WINDOW_UUID to be set
# Output: Creates individual log files in .tmux-cli/{agent_name}/log{timestamp}.md

set -euo pipefail

# Get event type from first argument
EVENT_TYPE="${1:-unknown}"

# Read hook input from stdin
HOOK_INPUT=$(cat)

# Validate session membership first
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if ! "$SCRIPT_DIR/tmux-validate-session.sh" 2>/dev/null <<< "$HOOK_INPUT"; then
    # Not a valid window - exit silently
    exit 0
fi

# Get window UUID
WINDOW_UUID="${TMUX_WINDOW_UUID:-}"

if [[ -z "$WINDOW_UUID" ]]; then
    # Should not happen after validation, but safeguard
    exit 0
fi

# Check for jq
if ! command -v jq &> /dev/null; then
    # jq not available - can't parse JSON
    exit 0
fi

# Get data from environment and current context
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TIMESTAMP_FILE=$(date -u +"%Y%m%d-%H%M%S")
SESSION_ID="${WINDOW_UUID}"  # Use window UUID as session ID
CWD="${PWD:-unknown}"

# Try to get window name from .tmux-session
SESSION_FILE=".tmux-session"
WINDOW_NAME="unknown"
TMUX_SESSION_ID=""
TMUX_WINDOW_ID=""
if [[ -f "$SESSION_FILE" ]]; then
    WINDOW_NAME=$(jq -r --arg uuid "$WINDOW_UUID" '.windows[]? | select(.uuid == $uuid) | .name // "unknown"' "$SESSION_FILE" 2>/dev/null || echo "unknown")
    # Read session ID and tmux window ID from session file for capture-pane targeting
    TMUX_SESSION_ID=$(jq -r '.sessionId // empty' "$SESSION_FILE" 2>/dev/null || echo "")
    TMUX_WINDOW_ID=$(jq -r --arg uuid "$WINDOW_UUID" \
        '.windows[]? | select(.uuid == $uuid) | .tmuxWindowId // empty' \
        "$SESSION_FILE" 2>/dev/null || echo "")
fi

# Capture terminal output directly from the pane instead of parsing transcript
FULL_CONTENT=""
LAST_CONTENT=""
CONTENT_TRIMMED=false
if [[ -n "$TMUX_SESSION_ID" && -n "$TMUX_WINDOW_ID" ]]; then
    FULL_CONTENT=$(tmux capture-pane -t "${TMUX_SESSION_ID}:${TMUX_WINDOW_ID}" \
        -p -S - 2>/dev/null || echo "")
    if [[ -n "$FULL_CONTENT" ]]; then
        TOTAL_LINES=$(printf '%s' "$FULL_CONTENT" | wc -l)
        if [[ "$TOTAL_LINES" -gt 50 ]]; then
            LAST_CONTENT=$(printf '%s' "$FULL_CONTENT" | tail -50)
            CONTENT_TRIMMED=true
        else
            LAST_CONTENT="$FULL_CONTENT"
        fi
    fi
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

# Create log directories
LOG_DIR=".tmux-cli/logs"
mkdir -p "$LOG_DIR"

# Create agent-specific log directory
AGENT_LOG_DIR=".tmux-cli/${WINDOW_NAME}"
mkdir -p "$AGENT_LOG_DIR"

# Use single log file per agent (overwrite mode)
LOG_FILE="$AGENT_LOG_DIR/agent.log"

# Only write agent.log on Stop events (avoids overwriting useful output on start/end)
if [[ "$EVENT_TYPE" == "stop" ]]; then
    echo "${FULL_CONTENT}" > "$LOG_FILE"
fi

# If this is a Stop event and not supervisor, notify supervisor window
if [[ "$EVENT_TYPE" == "stop" && "$WINDOW_NAME" != "supervisor" ]]; then
    # Check if tmux-cli is available
    if command -v tmux-cli &> /dev/null; then
        # Send notification message to supervisor window using tmux-cli with output
        NOTIFICATION_MESSAGE="${WINDOW_NAME} finished work
output:

${LAST_CONTENT}"

        if [[ "$CONTENT_TRIMMED" == true ]]; then
            NOTIFICATION_MESSAGE="${NOTIFICATION_MESSAGE}

[trimmed to last 50 lines — full output: .tmux-cli/${WINDOW_NAME}/agent.log]"
        else
            NOTIFICATION_MESSAGE="${NOTIFICATION_MESSAGE}

[full output: .tmux-cli/${WINDOW_NAME}/agent.log]"
        fi
        tmux-cli windows-message --receiver supervisor --message "$NOTIFICATION_MESSAGE" 2>/dev/null || true

        # Also write to a notifications file for persistence
        NOTIF_FILE="$LOG_DIR/notifications.log"
        echo "$(date -u +"%Y-%m-%dT%H:%M:%SZ") - Worker '$WINDOW_NAME' finished work" >> "$NOTIF_FILE"
    fi
fi

exit 0
