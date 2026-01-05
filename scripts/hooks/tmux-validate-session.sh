#!/usr/bin/env bash
#
# tmux-validate-session.sh
# Validates that the current window UUID exists in .tmux-session
#
# Usage: Called by other hooks before logging
# Environment: Expects $TMUX_WINDOW_UUID to be set
# Exit: 0 if valid (continue), 1 if invalid (skip logging)

set -euo pipefail

# Get window UUID from environment
WINDOW_UUID="${TMUX_WINDOW_UUID:-}"

if [[ -z "$WINDOW_UUID" ]]; then
    # No UUID in environment - not a tmux-cli managed window
    exit 1
fi

# Find .tmux-session file in current directory
SESSION_FILE=".tmux-session"

if [[ ! -f "$SESSION_FILE" ]]; then
    # No session file - not a tmux-cli project
    exit 1
fi

# Check if UUID exists in session file
# Using jq to parse JSON and search for window UUID
if ! command -v jq &> /dev/null; then
    # jq not installed - silently fail
    exit 1
fi

# Search for UUID and get window name
WINDOW_NAME=$(jq -r --arg uuid "$WINDOW_UUID" '.windows[]? | select(.uuid == $uuid) | .name // empty' "$SESSION_FILE" 2>/dev/null || echo "")

if [[ -z "$WINDOW_NAME" ]]; then
    # UUID not found in session - unauthorized window
    exit 1
fi

if [[ "$WINDOW_NAME" == "supervisor" ]]; then
    # Supervisor window - skip hooks
    exit 1
fi

# Valid non-supervisor window - allow logging
exit 0
