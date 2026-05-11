#!/usr/bin/env bash
#
# tmux-unplanned-audit.sh
# Claude Code Stop hook: injects an unplanned work audit prompt into the
# supervisor window when all tasks are done and no workers remain.
#
# Usage: Registered as a Stop hook — fires on every Claude Code exit
# Environment: Expects $TMUX_WINDOW_UUID and $CLAUDE_PROJECT_DIR

set -euo pipefail

HOOK_INPUT=$(cat)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# --- Session discovery (same pattern as tmux-supervisor-cycle.sh) ---

WINDOW_UUID="${TMUX_WINDOW_UUID:-}"
if [[ -z "$WINDOW_UUID" ]]; then
    exit 0
fi

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

while IFS='|' read -r wid wname; do
    uuid=$(tmux show-options -wv -t "${SESSION_ID}:${wid}" @window-uuid 2>/dev/null || echo "")
    if [[ "$uuid" == "$WINDOW_UUID" ]]; then
        WINDOW_NAME="$wname"
        TMUX_WINDOW_ID="$wid"
        break
    fi
done < <(tmux list-windows -t "$SESSION_ID" -F '#{window_id}|#{window_name}' 2>/dev/null)

# ONLY proceed for the supervisor window
if [[ "$WINDOW_NAME" != "supervisor" ]]; then
    exit 0
fi

# --- Check that no execute-* worker windows are still running ---

OPEN_WORKERS=$(tmux list-windows -t "$SESSION_ID" -F '#{window_name}' 2>/dev/null | grep -c '^execute-' || echo "0")

if [[ "$OPEN_WORKERS" -gt 0 ]]; then
    exit 0
fi

# --- Check tasks.yaml — ALL tasks must be done ---

TASKS_FILE="${PROJECT_DIR}/.tmux-cli/tasks.yaml"

if [[ ! -f "$TASKS_FILE" ]]; then
    exit 0
fi

# --- Skip when tasks.yaml is still in planning mode ---

FILE_STATUS=$(grep -E '^status:' "$TASKS_FILE" 2>/dev/null | sed 's/^status:\s*//' | tr -d ' ' || echo "")
if [[ "$FILE_STATUS" == "planning" ]]; then
    exit 0
fi

UNFINISHED=$(grep -c 'status: pending\|status: in_progress' "$TASKS_FILE" 2>/dev/null) || UNFINISHED=0

if [[ "$UNFINISHED" -gt 0 ]]; then
    exit 0
fi

# --- Guard file: prevent infinite audit loop ---

GUARD_FILE="${PROJECT_DIR}/.tmux-cli/audit-done"

if [[ -f "$GUARD_FILE" ]]; then
    exit 0
fi

# --- All conditions met: create guard and inject audit prompt ---

touch "$GUARD_FILE"

PANE_TARGET="${SESSION_ID}:${TMUX_WINDOW_ID}"

AUDIT_PROMPT="Unplanned work audit: scan the closest surrounding code context for both pre-existing issues and newly introduced problems not covered by completed tasks. Verify all work is truly done — nothing missed, nothing half-finished, no pre-existing solvable tasks. If any actionable items remain, add pending tasks to .tmux-cli/tasks.yaml. If everything is clean, report clean."

tmux send-keys -l -t "$PANE_TARGET" "$AUDIT_PROMPT"
sleep 0.1
tmux send-keys -t "$PANE_TARGET" Enter

# --- Log ---

LOG_DIR="${PROJECT_DIR}/.tmux-cli/logs"
mkdir -p "$LOG_DIR"
echo "$(date -u +"%Y-%m-%dT%H:%M:%SZ") - Unplanned work audit injected into supervisor" >> "$LOG_DIR/notifications.log"

exit 0
