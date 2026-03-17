#!/usr/bin/env bash
#
# tmux-session-notify.sh
# Logs Claude Code session lifecycle events (start, end, stop)
#
# Usage: Called by SessionStart, SessionEnd, Stop hooks with event type as arg
# Environment: Expects $TMUX_WINDOW_UUID to be set
# Output: Creates individual log files in .tmux-cli/{agent_name}/agent.log
#
# Session discovery: Uses tmux environment variables and window options
# instead of .tmux-session file. See tmux-validate-session.sh for details.

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

# Re-discover session info (since subshell exports don't propagate)
WINDOW_UUID="${TMUX_WINDOW_UUID:-}"
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$PWD}"
SESSION_ID=""
WINDOW_NAME=""
TMUX_WINDOW_ID=""

for sid in $(tmux list-sessions -F '#{session_name}' 2>/dev/null); do
    path=$(tmux show-environment -t "$sid" TMUX_CLI_PROJECT_PATH 2>/dev/null | sed 's/^TMUX_CLI_PROJECT_PATH=//' || echo "")
    if [[ "$path" == "$PROJECT_DIR" ]]; then
        SESSION_ID="$sid"
        break
    fi
done

if [[ -z "$SESSION_ID" ]]; then
    exit 0
fi

# Find window name and tmux window ID by UUID
while IFS='|' read -r wid wname; do
    uuid=$(tmux show-options -wv -t "${SESSION_ID}:${wid}" @window-uuid 2>/dev/null || echo "")
    if [[ "$uuid" == "$WINDOW_UUID" ]]; then
        WINDOW_NAME="$wname"
        TMUX_WINDOW_ID="$wid"
        break
    fi
done < <(tmux list-windows -t "$SESSION_ID" -F '#{window_id}|#{window_name}' 2>/dev/null)

if [[ -z "$WINDOW_NAME" || "$WINDOW_NAME" == "supervisor" ]]; then
    exit 0
fi

# Capture terminal output directly from the pane
FULL_CONTENT=""
LAST_CONTENT=""
CONTENT_TRIMMED=false
if [[ -n "$SESSION_ID" && -n "$TMUX_WINDOW_ID" ]]; then
    FULL_CONTENT=$(tmux capture-pane -t "${SESSION_ID}:${TMUX_WINDOW_ID}" \
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

# If this is a Stop event, notify supervisor window
if [[ "$EVENT_TYPE" == "stop" ]]; then
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
