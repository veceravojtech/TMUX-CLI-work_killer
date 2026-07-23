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
[[ -f "$PROJECT_DIR/.tmux-cli/taskvisor-active" ]] && exit 0
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

# --- Check that no worker windows are still running ---

# Fail-safe read mirroring tmux-supervisor-cycle.sh: capture the list separately
# (a failed tmux read must never count as zero workers — exit instead), and use
# `|| true`, NOT `|| echo "0"` — grep -c already prints "0" on no match while
# exiting 1, so the echo form appended a SECOND line ("0\n0") and tripped the
# -gt test below with an arithmetic error. supervisor-task-* counts as an open
# worker too: a delegated sub-supervisor may not have spawned its own
# execute-task-N-M workers yet, and that gap must not read as "no workers".
WINDOW_LIST=$(tmux list-windows -t "$SESSION_ID" -F '#{window_name}' 2>/dev/null) || exit 0
OPEN_WORKERS=$(grep -c -e '^execute-' -e '^supervisor-task-' <<< "$WINDOW_LIST" || true)
[[ "$OPEN_WORKERS" =~ ^[0-9]+$ ]] || OPEN_WORKERS=0

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

# --- Yield to a queued supervisor restart (per-Stop serialization) ---
# The cycle hook touches this sentinel before it queues a /tmux:supervisor
# relaunch. If it is present, a restart is already being sent into this pane on
# the same Stop event, so consume the sentinel and exit WITHOUT injecting — the
# audit re-arms naturally on the next Stop of the relaunched supervisor. This
# prevents the audit prompt from gluing onto the queued /tmux:supervisor args.
QUEUED_FILE="${PROJECT_DIR}/.tmux-cli/cycle-restart-queued"
if [[ -f "$QUEUED_FILE" ]]; then
    rm -f "$QUEUED_FILE"
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
