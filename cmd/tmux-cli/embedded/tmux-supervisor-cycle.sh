#!/usr/bin/env bash
#
# tmux-supervisor-cycle.sh
# Claude Code Stop hook: auto-restarts the supervisor when context is exhausted
# and unfinished tasks remain in .tmux-cli/tasks.yaml.
#
# Usage: Registered as a Stop hook — fires on every Claude Code exit
# Environment: Expects $TMUX_WINDOW_UUID and $CLAUDE_PROJECT_DIR
# Only acts on the "supervisor" window (inverse of tmux-session-notify.sh)

set -euo pipefail

HOOK_INPUT=$(cat)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# --- Session discovery (same pattern as tmux-session-notify.sh) ---

WINDOW_UUID="${TMUX_WINDOW_UUID:-}"
if [[ -z "$WINDOW_UUID" ]]; then
    exit 0
fi

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$PWD}"
[[ -f "$PROJECT_DIR/.tmux-cli/taskvisor-active" ]] && exit 0
# Defer to the recurring-task driver between its cycles: while a recurring run owns
# the supervisor window the daemon is the sole dispatcher, so this Stop hook must not
# inject a second /tmux:supervisor (mirrors the taskvisor-active guard above).
[[ -f "$PROJECT_DIR/.tmux-cli/recurring-active" ]] && exit 0

# --- All goals terminal? No work to restart ---
GOALS_FILE="${PROJECT_DIR}/.tmux-cli/goals.yaml"
if [[ -f "$GOALS_FILE" ]]; then
    TOTAL_GOALS=$(grep -cE '^\s*status:\s' "$GOALS_FILE" 2>/dev/null) || TOTAL_GOALS=0
    NON_TERMINAL=$(grep -cE '^\s*status:\s*(pending|running)' "$GOALS_FILE" 2>/dev/null) || NON_TERMINAL=0
    if [[ "$TOTAL_GOALS" -gt 0 && "$NON_TERMINAL" -eq 0 ]]; then
        exit 0
    fi
fi

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

# --- Skip if auto-execute guard is present (plan already restarted the supervisor) ---

GUARD_FILE="${PROJECT_DIR}/.tmux-cli/auto-execute-guard"
if [[ -f "$GUARD_FILE" ]]; then
    rm -f "$GUARD_FILE"
    exit 0
fi

# --- Fresh-context handoff marker (design §5b) ---
#
# An armed .tmux-cli/fresh-handoff is an explicit instruction and takes precedence
# over leftover unfinished tasks.yaml work — the fresh instance decides what to do
# about those from its plan file. One-shot by construction: the marker is consumed
# before anything is sent, so a crashed send cannot re-fire on the next Stop.

FRESH_MARKER="${PROJECT_DIR}/.tmux-cli/fresh-handoff"

if [[ -f "$FRESH_MARKER" ]]; then
    LOG_DIR="${PROJECT_DIR}/.tmux-cli/logs"
    mkdir -p "$LOG_DIR"

    # yaml reads stay grep/sed based (no jq on the host), same as this script's other reads
    FRESH_PLAN=$(grep -E '^\s*plan:' "$FRESH_MARKER" 2>/dev/null | head -n 1 | sed -e 's/^\s*plan:\s*//' -e 's/\s*$//' -e 's/^"//' -e 's/"$//' || echo "")
    FRESH_SELF_WAVE=$(grep -E '^\s*self_wave:' "$FRESH_MARKER" 2>/dev/null | head -n 1 | sed 's/.*self_wave:\s*//' | tr -d ' ' || echo "")
    FRESH_CYCLE=$(grep -E '^\s*cycle:' "$FRESH_MARKER" 2>/dev/null | head -n 1 | sed 's/.*cycle:\s*//' | tr -d ' ' || echo "")
    [[ "$FRESH_SELF_WAVE" =~ ^[0-9]+$ ]] || FRESH_SELF_WAVE=0
    [[ "$FRESH_CYCLE" =~ ^[0-9]+$ ]] || FRESH_CYCLE=0

    # Never restart onto nothing: a missing/unparseable plan consumes the marker and logs
    FRESH_PLAN_ABS="$FRESH_PLAN"
    if [[ -n "$FRESH_PLAN" && "$FRESH_PLAN" != /* ]]; then
        FRESH_PLAN_ABS="${PROJECT_DIR}/${FRESH_PLAN}"
    fi

    if [[ -z "$FRESH_PLAN" || ! -f "$FRESH_PLAN_ABS" ]]; then
        rm -f "$FRESH_MARKER"
        echo "$(date -u +"%Y-%m-%dT%H:%M:%SZ") - Supervisor fresh handoff aborted (plan file missing: '${FRESH_PLAN}'), marker consumed" >> "$LOG_DIR/notifications.log"
        exit 0
    fi

    # --- Read max_cycles / cycle_delay from setting.yaml (default 0 = unlimited) ---

    FRESH_SETTINGS_FILE="${PROJECT_DIR}/.tmux-cli/setting.yaml"
    FRESH_MAX_CYCLES=0
    FRESH_CYCLE_DELAY=5

    if [[ -f "$FRESH_SETTINGS_FILE" ]]; then
        FRESH_MAX_CYCLES=$(grep -E '^\s*max_cycles:' "$FRESH_SETTINGS_FILE" 2>/dev/null | sed 's/.*max_cycles:\s*//' | tr -d ' ' || echo "0")
        [[ "$FRESH_MAX_CYCLES" =~ ^[0-9]+$ ]] || FRESH_MAX_CYCLES=0
        FRESH_CYCLE_DELAY=$(grep -E '^\s*cycle_delay:' "$FRESH_SETTINGS_FILE" 2>/dev/null | sed 's/.*cycle_delay:\s*//' | tr -d ' ' || echo "5")
        [[ "$FRESH_CYCLE_DELAY" =~ ^[0-9]+$ ]] || FRESH_CYCLE_DELAY=5
    fi

    # Same cap rule as the tasks.yaml branch, enforced against the marker's cycle.
    # A capped marker is still consumed — leaving it armed would ambush a later Stop.
    if [[ "$FRESH_MAX_CYCLES" -gt 0 && "$FRESH_CYCLE" -ge "$FRESH_MAX_CYCLES" ]]; then
        rm -f "$FRESH_MARKER"
        echo "$(date -u +"%Y-%m-%dT%H:%M:%SZ") - Supervisor fresh handoff cycle limit reached (${FRESH_CYCLE}/${FRESH_MAX_CYCLES}), NOT restarting" >> "$LOG_DIR/notifications.log"
        exit 0
    fi

    # --- Consume the marker BEFORE any send — one-shot ---

    rm -f "$FRESH_MARKER"

    FRESH_PANE_TARGET="${SESSION_ID}:${TMUX_WINDOW_ID}"
    CANCEL_FILE="${PROJECT_DIR}/.tmux-cli/cancel-cycle"

    FRESH_WAVE_MSG=""
    if [[ "$FRESH_SELF_WAVE" -gt 0 ]]; then
        FRESH_WAVE_MSG=", self_wave ${FRESH_SELF_WAVE}"
    fi
    echo "$(date -u +"%Y-%m-%dT%H:%M:%SZ") - Supervisor fresh handoff restart -> ${FRESH_PLAN} (cycle ${FRESH_CYCLE}${FRESH_WAVE_MSG})" >> "$LOG_DIR/notifications.log"

    # --- Cancellable countdown (cancel leaves the marker consumed — no re-arm) ---

    rm -f "$CANCEL_FILE"

    if [[ "$FRESH_CYCLE_DELAY" -gt 0 ]]; then
        for i in $(seq "$FRESH_CYCLE_DELAY" -1 1); do
            if [[ -f "$CANCEL_FILE" ]]; then
                rm -f "$CANCEL_FILE"
                tmux display-message -t "$FRESH_PANE_TARGET" "Supervisor fresh restart cancelled."
                echo "$(date -u +"%Y-%m-%dT%H:%M:%SZ") - Supervisor fresh handoff cancelled by user (marker consumed, restart skipped)" >> "$LOG_DIR/notifications.log"
                exit 0
            fi
            tmux display-message -t "$FRESH_PANE_TARGET" "Supervisor fresh restart in ${i}s... (touch .tmux-cli/cancel-cycle to abort)"
            sleep 1
        done

        # Final cancel check after the last sleep
        if [[ -f "$CANCEL_FILE" ]]; then
            rm -f "$CANCEL_FILE"
            tmux display-message -t "$FRESH_PANE_TARGET" "Supervisor fresh restart cancelled."
            echo "$(date -u +"%Y-%m-%dT%H:%M:%SZ") - Supervisor fresh handoff cancelled by user (marker consumed, restart skipped)" >> "$LOG_DIR/notifications.log"
            exit 0
        fi
    fi

    # --- Send restart commands to the supervisor pane (same pattern as the tasks branch) ---

    rm -f "${PROJECT_DIR}/.tmux-cli/audit-done"
    tmux send-keys -t "$FRESH_PANE_TARGET" "/clear" Enter
    sleep 2
    tmux send-keys -t "$FRESH_PANE_TARGET" "/tmux:supervisor ${FRESH_PLAN}" Enter

    exit 0
fi

# --- Check tasks.yaml for unfinished tasks ---

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

if [[ "$UNFINISHED" -eq 0 ]]; then
    exit 0
fi

# --- Read max_cycles from setting.yaml (default 0 = unlimited) ---

SETTINGS_FILE="${PROJECT_DIR}/.tmux-cli/setting.yaml"
MAX_CYCLES=0
CYCLE_DELAY=5

if [[ -f "$SETTINGS_FILE" ]]; then
    MAX_CYCLES=$(grep -E '^\s*max_cycles:' "$SETTINGS_FILE" 2>/dev/null | sed 's/.*max_cycles:\s*//' | tr -d ' ' || echo "0")
    if [[ -z "$MAX_CYCLES" ]]; then
        MAX_CYCLES=0
    fi
    CYCLE_DELAY=$(grep -E '^\s*cycle_delay:' "$SETTINGS_FILE" 2>/dev/null | sed 's/.*cycle_delay:\s*//' | tr -d ' ' || echo "5")
    if [[ -z "$CYCLE_DELAY" ]]; then
        CYCLE_DELAY=5
    fi
fi

# --- Read current cycle from tasks.yaml ---

CURRENT_CYCLE=$(grep -E '^cycle:' "$TASKS_FILE" 2>/dev/null | sed 's/^cycle:\s*//' | tr -d ' ' || echo "0")
if [[ -z "$CURRENT_CYCLE" ]]; then
    CURRENT_CYCLE=0
fi

# --- Check cycle limit ---

if [[ "$MAX_CYCLES" -gt 0 && "$CURRENT_CYCLE" -ge "$MAX_CYCLES" ]]; then
    LOG_DIR="${PROJECT_DIR}/.tmux-cli/logs"
    mkdir -p "$LOG_DIR"
    echo "$(date -u +"%Y-%m-%dT%H:%M:%SZ") - Supervisor cycle limit reached (${CURRENT_CYCLE}/${MAX_CYCLES}), NOT restarting" >> "$LOG_DIR/notifications.log"
    exit 0
fi

# --- Increment cycle counter in tasks.yaml ---

NEW_CYCLE=$((CURRENT_CYCLE + 1))
sed -i "s/^cycle:.*/cycle: ${NEW_CYCLE}/" "$TASKS_FILE"

# --- Log the restart ---

LOG_DIR="${PROJECT_DIR}/.tmux-cli/logs"
mkdir -p "$LOG_DIR"
CYCLE_MSG="unlimited"
if [[ "$MAX_CYCLES" -gt 0 ]]; then
    CYCLE_MSG="${NEW_CYCLE}/${MAX_CYCLES}"
fi
echo "$(date -u +"%Y-%m-%dT%H:%M:%SZ") - Supervisor auto-cycle restart (cycle ${CYCLE_MSG}, ${UNFINISHED} unfinished tasks)" >> "$LOG_DIR/notifications.log"

# --- Cancellable countdown before restart ---

PANE_TARGET="${SESSION_ID}:${TMUX_WINDOW_ID}"
CANCEL_FILE="${PROJECT_DIR}/.tmux-cli/cancel-cycle"

rm -f "$CANCEL_FILE"

if [[ "$CYCLE_DELAY" -le 0 ]]; then
    # No countdown — restart immediately
    # Serialize with the unplanned-audit Stop hook: write the per-Stop sentinel
    # so the audit hook yields (consumes it and exits) instead of gluing its
    # prompt onto the queued /tmux:supervisor arguments. The audit-done re-arm
    # now lives in the relaunched supervisor's step-0 clean slate, so this path
    # no longer deletes the audit-done guard (that raced the audit hook's touch).
    touch "${PROJECT_DIR}/.tmux-cli/cycle-restart-queued"
    tmux send-keys -t "$PANE_TARGET" "/clear" Enter
    sleep 2
    tmux send-keys -t "$PANE_TARGET" "/tmux:supervisor .tmux-cli/tasks.yaml" Enter
    exit 0
fi

for i in $(seq "$CYCLE_DELAY" -1 1); do
    if [[ -f "$CANCEL_FILE" ]]; then
        rm -f "$CANCEL_FILE"
        tmux display-message -t "$PANE_TARGET" "Supervisor cycle cancelled."
        echo "$(date -u +"%Y-%m-%dT%H:%M:%SZ") - Supervisor cycle cancelled by user" >> "$LOG_DIR/notifications.log"
        # Undo the cycle increment since we're not restarting
        sed -i "s/^cycle:.*/cycle: ${CURRENT_CYCLE}/" "$TASKS_FILE"
        exit 0
    fi
    tmux display-message -t "$PANE_TARGET" "Supervisor restarting in ${i}s... (touch .tmux-cli/cancel-cycle to abort)"
    sleep 1
done

# Final cancel check after last sleep
if [[ -f "$CANCEL_FILE" ]]; then
    rm -f "$CANCEL_FILE"
    tmux display-message -t "$PANE_TARGET" "Supervisor cycle cancelled."
    echo "$(date -u +"%Y-%m-%dT%H:%M:%SZ") - Supervisor cycle cancelled by user" >> "$LOG_DIR/notifications.log"
    sed -i "s/^cycle:.*/cycle: ${CURRENT_CYCLE}/" "$TASKS_FILE"
    exit 0
fi

# --- Send restart commands to the supervisor pane ---

# Serialize with the unplanned-audit Stop hook (see the immediate-restart path
# above): write the per-Stop sentinel so the audit hook yields, and let the
# relaunched supervisor's step-0 clean slate own the audit-done re-arm.
touch "${PROJECT_DIR}/.tmux-cli/cycle-restart-queued"
tmux send-keys -t "$PANE_TARGET" "/clear" Enter
sleep 2
tmux send-keys -t "$PANE_TARGET" "/tmux:supervisor .tmux-cli/tasks.yaml" Enter

exit 0
