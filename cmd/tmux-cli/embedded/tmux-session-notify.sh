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
if [[ -f "$SESSION_FILE" ]]; then
    WINDOW_NAME=$(jq -r --arg uuid "$WINDOW_UUID" '.windows[]? | select(.uuid == $uuid) | .name // "unknown"' "$SESSION_FILE" 2>/dev/null || echo "unknown")
fi

# Parse transcript path from hook input
TRANSCRIPT_PATH=$(echo "$HOOK_INPUT" | jq -r '.transcript_path // empty')

# Get complete last message from transcript if available
LAST_CONTENT=""
if [[ -n "$TRANSCRIPT_PATH" && -f "$TRANSCRIPT_PATH" ]]; then
    # Extract ALL text from the last user/assistant message (handle multiple content blocks)
    LAST_CONTENT=$(tac "$TRANSCRIPT_PATH" | \
        jq -rs 'map(select(.type == "user" or .type == "assistant")) | first |
                if .message.content | type == "string" then .message.content
                else [.message.content[]? | select(.type == "text") | .text] | join("\n") end' 2>/dev/null || echo "")
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

# Overwrite log file with only the message content
echo "${LAST_CONTENT}" > "$LOG_FILE"

# If this is a Stop event and not supervisor, notify supervisor window
if [[ "$EVENT_TYPE" == "stop" && "$WINDOW_NAME" != "supervisor" ]]; then
    # Check if tmux-cli is available
    if command -v tmux-cli &> /dev/null; then
        # Send notification message to supervisor window using tmux-cli with output
        NOTIFICATION_MESSAGE="${WINDOW_NAME} finished work
output:

${LAST_CONTENT}"
        tmux-cli windows-message --receiver supervisor --message "$NOTIFICATION_MESSAGE" 2>/dev/null || true

        # Also write to a notifications file for persistence
        NOTIF_FILE="$LOG_DIR/notifications.log"
        echo "$(date -u +"%Y-%m-%dT%H:%M:%SZ") - Worker '$WINDOW_NAME' finished work" >> "$NOTIF_FILE"
    fi
fi

exit 0
