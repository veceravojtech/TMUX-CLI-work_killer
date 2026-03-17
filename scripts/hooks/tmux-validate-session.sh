#!/usr/bin/env bash
#
# tmux-validate-session.sh
# Validates that the current window belongs to a tmux-cli managed session
#
# Usage: Called by other hooks before logging
# Environment: Expects $TMUX_WINDOW_UUID to be set
# Exit: 0 if valid non-supervisor window (continue), 1 if invalid (skip)
#
# Session discovery: Uses tmux environment variables instead of .tmux-session file.
# Each tmux-cli session stores TMUX_CLI_PROJECT_PATH in its environment.
# Each window stores its UUID in the @window-uuid user-option.

set -euo pipefail

# Get window UUID from environment
WINDOW_UUID="${TMUX_WINDOW_UUID:-}"

if [[ -z "$WINDOW_UUID" ]]; then
    # No UUID in environment - not a tmux-cli managed window
    exit 1
fi

# Discover which tmux session we're in by finding one with matching TMUX_CLI_PROJECT_PATH
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$PWD}"
SESSION_ID=""

for sid in $(tmux list-sessions -F '#{session_name}' 2>/dev/null); do
    path=$(tmux show-environment -t "$sid" TMUX_CLI_PROJECT_PATH 2>/dev/null | sed 's/^TMUX_CLI_PROJECT_PATH=//' || echo "")
    if [[ "$path" == "$PROJECT_DIR" ]]; then
        SESSION_ID="$sid"
        break
    fi
done

if [[ -z "$SESSION_ID" ]]; then
    # No tmux-cli session found for this project
    exit 1
fi

# Find the window with matching UUID and get its name
WINDOW_NAME=""
while IFS='|' read -r wid wname; do
    uuid=$(tmux show-options -wv -t "${SESSION_ID}:${wid}" @window-uuid 2>/dev/null || echo "")
    if [[ "$uuid" == "$WINDOW_UUID" ]]; then
        WINDOW_NAME="$wname"
        break
    fi
done < <(tmux list-windows -t "$SESSION_ID" -F '#{window_id}|#{window_name}' 2>/dev/null)

if [[ -z "$WINDOW_NAME" ]]; then
    # UUID not found in any window
    exit 1
fi

if [[ "$WINDOW_NAME" == "supervisor" ]]; then
    # Supervisor window - skip hooks
    exit 1
fi

# Export discovered values for the calling script
export TMUX_CLI_SESSION_ID="$SESSION_ID"
export TMUX_CLI_WINDOW_NAME="$WINDOW_NAME"

# Valid non-supervisor window - allow logging
exit 0
