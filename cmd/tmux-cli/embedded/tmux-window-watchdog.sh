#!/usr/bin/env bash
#
# tmux-window-watchdog.sh
#
# Scans tmux windows for stall signals — "API Error:", usage/rate-limit
# messages, or general inactivity (pane unchanged between scans) — and nudges
# the stuck agent by sending the configured string (default "continue") + ENTER.
#
# Configurable via the `watchdog:` block in .tmux-cli/setting.yaml:
#
#   watchdog:
#       enabled: true            # master switch (default: false / opt-in)
#       nudge_text: "continue"   # string sent before ENTER
#       cooldown_sec: 60         # min seconds between nudges to the same window
#       detect_inactivity: false # also nudge when a pane is unchanged across scans
#       patterns: ""             # extra ERE patterns (pipe-separated), OR'd with defaults
#       min_idle_sec: 0          # ACTIVITY GATE: never nudge a window that produced
#                                #   output within this many seconds (0 = no gate).
#                                #   Stops pattern/inactivity nudges interrupting a
#                                #   window that is actively working.
#       idle_threshold_sec: 0    # IDLE TRIGGER: nudge a window idle this long even
#                                #   with no error pattern (0 = off). Uses tmux's
#                                #   real last-activity timestamp.
#       require_pending_work: true  # only nudge when the project's goals.yaml /
#                                #   tasks.yaml still has pending/running work. When
#                                #   all work is done -> never nudge. (default: true)
#       max_nudges: 5            # give up on a window after this many nudges with no
#                                #   recovery (0 = unlimited). Resets when it recovers.
#
# Settings are resolved per session from that session's project
# ($TMUX_CLI_PROJECT_PATH/.tmux-cli/setting.yaml), falling back to the global
# ~/.tmux-cli/setting.yaml.
#
# Usage:
#   tmux-window-watchdog.sh                 # one scan, then exit
#   tmux-window-watchdog.sh --watch [SECS]  # loop every SECS (default 30)
#   tmux-window-watchdog.sh --dry-run       # detect + log, never send keys
#
# Library mode: set WATCHDOG_LIB_MODE=1 (or `source` it) to define the pure
# decision functions without running a scan. Used by the test suite.

# ===========================================================================
# Pure decision functions (unit-tested in tests/tmux-window-watchdog.test.sh)
# ===========================================================================

# Default stall signatures. Pipe-separated ERE alternation. Case-sensitive
# matches the strings as they appear in Claude Code / API output.
wd_default_patterns() {
    printf '%s' 'API Error|usage limit|rate limit|Overloaded|overloaded_error|Connection error|Request timed out|529'
}

# wd_effective_patterns DEFAULTS EXTRA -> combined pattern string.
wd_effective_patterns() {
    local defaults="$1" extra="$2"
    if [[ -n "$extra" ]]; then
        printf '%s|%s' "$defaults" "$extra"
    else
        printf '%s' "$defaults"
    fi
}

# wd_matches TEXT PATTERNS -> rc 0 if any pattern matches TEXT.
wd_matches() {
    local text="$1" patterns="$2"
    [[ -z "$patterns" ]] && return 1
    printf '%s' "$text" | grep -Eq -- "$patterns"
}

# wd_get_setting FILE KEY DEFAULT -> value of KEY inside the `watchdog:` block,
# or DEFAULT if the file/block/key is missing. Surrounding quotes are stripped.
# Scoped to the watchdog block so a same-named key in another section can't leak.
wd_get_setting() {
    local file="$1" key="$2" default="$3"
    if [[ ! -f "$file" ]]; then
        printf '%s' "$default"
        return
    fi
    local val
    val=$(awk '
        /^watchdog:[[:space:]]*$/ { inblock=1; next }
        inblock && /^[^[:space:]]/ { inblock=0 }
        inblock { print }
    ' "$file" \
        | grep -E "^[[:space:]]+${key}:" \
        | head -1 \
        | sed -E "s/^[[:space:]]+${key}:[[:space:]]*//")
    # strip trailing whitespace and matching surrounding quotes
    val=$(printf '%s' "$val" | sed -E 's/[[:space:]]+$//; s/^"(.*)"$/\1/; s/^'\''(.*)'\''$/\1/')
    if [[ -z "$val" ]]; then
        printf '%s' "$default"
    else
        printf '%s' "$val"
    fi
}

# wd_should_nudge_inactivity PREV_HASH CUR_HASH ENABLED -> rc 0 if a pane that
# is unchanged since the previous scan should be nudged. First scan (empty
# PREV_HASH) never fires.
wd_should_nudge_inactivity() {
    local prev="$1" cur="$2" enabled="$3"
    [[ "$enabled" == "true" ]] || return 1
    [[ -n "$prev" ]] || return 1
    [[ "$prev" == "$cur" ]] || return 1
    return 0
}

# wd_cooldown_ok LAST_TS NOW_TS COOLDOWN_SEC -> rc 0 if enough time has passed
# (or the window was never nudged).
wd_cooldown_ok() {
    local last="$1" now="$2" cooldown="$3"
    [[ -z "$last" ]] && return 0
    if (( now - last >= cooldown )); then
        return 0
    fi
    return 1
}

# wd_idle_seconds ACTIVITY_EPOCH NOW -> seconds since the window last produced
# output (from tmux's #{window_activity}). Prints -1 when activity is unknown
# (empty/non-numeric); clamps negative clock skew to 0.
wd_idle_seconds() {
    local activity="$1" now="$2"
    if [[ ! "$activity" =~ ^[0-9]+$ ]]; then
        printf '%s' -1
        return
    fi
    local idle=$((now - activity))
    (( idle < 0 )) && idle=0
    printf '%s' "$idle"
}

# wd_idle_gate_ok IDLE MIN_IDLE -> rc 0 if a nudge is permitted. Blocks (rc 1)
# only when the gate is enabled (min_idle > 0), idle is known (>= 0), and the
# window is still active (idle < min_idle). Fail-open on unknown idle.
wd_idle_gate_ok() {
    local idle="$1" min_idle="$2"
    (( min_idle <= 0 )) && return 0
    (( idle < 0 )) && return 0
    (( idle >= min_idle )) && return 0
    return 1
}

# wd_idle_trigger IDLE THRESHOLD -> rc 0 if the window should be nudged purely
# for being idle this long. Disabled when threshold <= 0 or idle unknown.
wd_idle_trigger() {
    local idle="$1" threshold="$2"
    (( threshold <= 0 )) && return 1
    (( idle < 0 )) && return 1
    (( idle >= threshold )) && return 0
    return 1
}

# wd_work_state GOALS_FILE TASKS_FILE -> pending | done | unknown.
#   pending  - at least one status is pending/running/in_progress
#   done     - statuses exist but all are terminal (no active work left)
#   unknown  - no files / no status lines (can't tell)
# This is the guard that stops endless nudging once all work is finished.
wd_work_state() {
    local goals="$1" tasks="$2" active=0 total=0 f
    for f in "$goals" "$tasks"; do
        [[ -f "$f" ]] || continue
        # Unanchored to tolerate both block (`    status: x`) and list
        # (`  - status: x`) YAML styles, matching the existing hooks' approach.
        if grep -Eq 'status:[[:space:]]*(pending|running|in_progress)' "$f"; then
            active=1
        fi
        if grep -Eq 'status:[[:space:]]*[A-Za-z]' "$f"; then
            total=1
        fi
    done
    if [[ "$active" -eq 1 ]]; then
        printf 'pending'
    elif [[ "$total" -eq 1 ]]; then
        printf 'done'
    else
        printf 'unknown'
    fi
}

# wd_should_giveup NUDGE_COUNT MAX_NUDGES -> rc 0 if we've nudged this window
# enough times without recovery and should stop. Disabled when max <= 0.
wd_should_giveup() {
    local count="$1" max="$2"
    (( max <= 0 )) && return 1
    (( count >= max )) && return 0
    return 1
}

# ===========================================================================
# Library mode: stop here when sourced / under test.
# ===========================================================================
if [[ -n "${WATCHDOG_LIB_MODE:-}" || "${BASH_SOURCE[0]}" != "${0}" ]]; then
    return 0 2>/dev/null || true
fi

# ===========================================================================
# Side-effecting scan (only runs when executed directly)
# ===========================================================================
set -uo pipefail

GLOBAL_SETTINGS="${HOME}/.tmux-cli/setting.yaml"
STATE_DIR="${HOME}/.tmux-cli/logs"
STATE_FILE="${STATE_DIR}/watchdog-state"
GLOBAL_LOG="${STATE_DIR}/watchdog.log"
DRY_RUN=0
WATCH=0
INTERVAL=30

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run) DRY_RUN=1; shift ;;
        --watch)   WATCH=1; shift; [[ "${1:-}" =~ ^[0-9]+$ ]] && { INTERVAL="$1"; shift; } ;;
        --once)    WATCH=0; shift ;;
        -h|--help) sed -n '2,40p' "$0"; exit 0 ;;
        *) echo "unknown arg: $1" >&2; exit 2 ;;
    esac
done

mkdir -p "$STATE_DIR"
touch "$STATE_FILE"

log() {
    printf '%s - %s\n' "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" "$1" >>"$GLOBAL_LOG"
}

# state file lines: UUID|last_nudge_ts|last_hash|nudge_count
state_field() { # KEY FIELDNO
    local key="$1" field="$2"
    grep -F "${key}|" "$STATE_FILE" 2>/dev/null | tail -1 | cut -d'|' -f"$field"
}
state_put() { # KEY TS HASH COUNT
    local key="$1" ts="$2" hash="$3" count="$4" tmp
    tmp=$(mktemp)
    grep -vF "${key}|" "$STATE_FILE" 2>/dev/null >"$tmp" || true
    printf '%s|%s|%s|%s\n' "$key" "$ts" "$hash" "$count" >>"$tmp"
    mv "$tmp" "$STATE_FILE"
}

session_project_dir() { # SESSION_ID -> project path ("" if unknown)
    local sid="$1"
    tmux show-environment -t "$sid" TMUX_CLI_PROJECT_PATH 2>/dev/null \
        | sed 's/^TMUX_CLI_PROJECT_PATH=//' || true
}

session_project_settings() { # PROJECT_DIR -> path to settings file
    local path="$1"
    if [[ -n "$path" && -f "${path}/.tmux-cli/setting.yaml" ]]; then
        printf '%s' "${path}/.tmux-cli/setting.yaml"
    else
        printf '%s' "$GLOBAL_SETTINGS"
    fi
}

scan_once() {
    local now
    now=$(date +%s)
    local sid
    for sid in $(tmux list-sessions -F '#{session_name}' 2>/dev/null); do
        local pdir settings enabled
        pdir="$(session_project_dir "$sid")"
        settings="$(session_project_settings "$pdir")"
        enabled="$(wd_get_setting "$settings" enabled false)"
        [[ "$enabled" == "true" ]] || continue

        local nudge_text cooldown detect_inact extra patterns min_idle idle_threshold
        local require_pending max_nudges
        nudge_text="$(wd_get_setting "$settings" nudge_text continue)"
        cooldown="$(wd_get_setting "$settings" cooldown_sec 60)"
        detect_inact="$(wd_get_setting "$settings" detect_inactivity false)"
        extra="$(wd_get_setting "$settings" patterns "")"
        patterns="$(wd_effective_patterns "$(wd_default_patterns)" "$extra")"
        # Activity gate: don't nudge a window producing output within this many
        # seconds (0 = no gate). Idle trigger: nudge after this many idle
        # seconds even without a pattern (0 = off).
        min_idle="$(wd_get_setting "$settings" min_idle_sec 0)"
        idle_threshold="$(wd_get_setting "$settings" idle_threshold_sec 0)"
        # Completion guards against endless nudging.
        require_pending="$(wd_get_setting "$settings" require_pending_work true)"
        max_nudges="$(wd_get_setting "$settings" max_nudges 5)"

        # Work state is per-project (shared goals/tasks), computed once per session.
        local work_state="unknown"
        if [[ -n "$pdir" ]]; then
            work_state="$(wd_work_state "${pdir}/.tmux-cli/goals.yaml" "${pdir}/.tmux-cli/tasks.yaml")"
        fi

        local wid wname wactivity
        while IFS='|' read -r wid wname wactivity; do
            local pane text uuid key reason="" idle raw_stall=0
            pane="${sid}:${wid}"
            text="$(tmux capture-pane -p -t "$pane" 2>/dev/null)"
            [[ -z "$text" ]] && continue

            uuid="$(tmux show-options -wv -t "$pane" @window-uuid 2>/dev/null || echo "")"
            key="${uuid:-$pane}"

            local cur_hash prev_hash last_ts count
            cur_hash="$(printf '%s' "$text" | cksum | awk '{print $1}')"
            prev_hash="$(state_field "$key" 3)"
            last_ts="$(state_field "$key" 2)"
            count="$(state_field "$key" 4)"; count="${count:-0}"
            idle="$(wd_idle_seconds "$wactivity" "$now")"

            # Detect stall signals independently of the idle gate, so raw_stall
            # reflects window health and survives the gate's quiet window.
            local pat=0 inact=0 idlet=0
            wd_matches "$text" "$patterns" && pat=1
            wd_should_nudge_inactivity "$prev_hash" "$cur_hash" "$detect_inact" && inact=1
            wd_idle_trigger "$idle" "$idle_threshold" && idlet=1
            { [[ "$pat" -eq 1 || "$inact" -eq 1 || "$idlet" -eq 1 ]]; } && raw_stall=1

            # Pattern / inactivity nudges are gated on the window actually being
            # idle, so an actively-working window is never interrupted.
            if wd_idle_gate_ok "$idle" "$min_idle"; then
                if [[ "$pat" -eq 1 ]]; then
                    reason="pattern"
                elif [[ "$inact" -eq 1 ]]; then
                    reason="inactivity"
                fi
            fi
            [[ -z "$reason" && "$idlet" -eq 1 ]] && reason="idle"

            # --- Completion guards: never nudge finished/unowned work ---
            if [[ -n "$reason" && "$work_state" == "done" ]]; then
                log "skip ${sid} ${wname} (${pane}) reason=${reason}: all work done"
                reason=""
            fi
            if [[ -n "$reason" && "$work_state" == "unknown" && "$require_pending" == "true" ]]; then
                log "skip ${sid} ${wname} (${pane}) reason=${reason}: no pending work confirmed"
                reason=""
            fi
            # --- Give-up cap: stop after N nudges without recovery ---
            if [[ -n "$reason" ]] && wd_should_giveup "$count" "$max_nudges"; then
                log "skip ${sid} ${wname} (${pane}) reason=${reason}: give-up (max_nudges=${max_nudges})"
                reason=""
            fi

            local new_count="$count" new_ts="${last_ts:-0}"
            if [[ -n "$reason" ]]; then
                if wd_cooldown_ok "$last_ts" "$now" "$cooldown"; then
                    if [[ "$DRY_RUN" -eq 1 ]]; then
                        log "DRY-RUN would nudge ${sid} ${wname} (${pane}) reason=${reason} idle=${idle}s count=$((count + 1))"
                    else
                        tmux send-keys -t "$pane" "$nudge_text" Enter
                        log "nudged ${sid} ${wname} (${pane}) reason=${reason} idle=${idle}s count=$((count + 1)) sent='${nudge_text}'"
                    fi
                    new_count=$((count + 1))
                    new_ts="$now"
                else
                    log "skip ${sid} ${wname} (${pane}) reason=${reason} idle=${idle}s (cooldown)"
                fi
            elif [[ "$raw_stall" -eq 0 ]]; then
                # Window looks healthy this scan -> reset the give-up counter.
                new_count=0
            fi
            state_put "$key" "$new_ts" "$cur_hash" "$new_count"
        done < <(tmux list-windows -t "$sid" -F '#{window_id}|#{window_name}|#{window_activity}' 2>/dev/null)
    done
}

if [[ "$WATCH" -eq 1 ]]; then
    log "watchdog started (--watch ${INTERVAL}s, dry_run=${DRY_RUN})"
    while true; do
        scan_once
        sleep "$INTERVAL"
    done
else
    scan_once
fi
