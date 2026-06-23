#!/usr/bin/env bash
#
# Tests for tmux-window-watchdog.sh (pure decision functions).
# Plain-bash harness — no bats dependency.
#
# Run: bash tmux-window-watchdog.test.sh
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WATCHDOG="${SCRIPT_DIR}/tmux-window-watchdog.sh"

if [[ ! -f "$WATCHDOG" ]]; then
    echo "FATAL: watchdog script not found at $WATCHDOG" >&2
    exit 1
fi

# Source in library mode (defines functions, runs nothing).
# shellcheck disable=SC1090
WATCHDOG_LIB_MODE=1 source "$WATCHDOG"

PASS=0
FAIL=0

ok() { PASS=$((PASS + 1)); printf '  ok   - %s\n' "$1"; }
no() { FAIL=$((FAIL + 1)); printf '  FAIL - %s\n' "$1"; }

# assert_rc EXPECTED_RC "desc" -- runs the remaining args as a command
assert_rc() {
    local expected="$1" desc="$2"; shift 2
    "$@"; local rc=$?
    if [[ "$rc" -eq "$expected" ]]; then ok "$desc"; else no "$desc (rc=$rc expected=$expected)"; fi
}

assert_eq() {
    local expected="$1" actual="$2" desc="$3"
    if [[ "$actual" == "$expected" ]]; then ok "$desc"; else no "$desc (got='$actual' want='$expected')"; fi
}

# ---------------------------------------------------------------------------
echo "## wd_matches (default patterns)"
DEF="$(wd_default_patterns)"

assert_rc 0 "matches 'API Error:'"            wd_matches "tool ran... API Error: 500 server error" "$DEF"
assert_rc 0 "matches 'Claude usage limit'"    wd_matches "Claude usage limit reached. Resets 5pm" "$DEF"
assert_rc 0 "matches 'rate limit'"            wd_matches "Error: rate limit exceeded, retry later" "$DEF"
assert_rc 0 "matches 'Overloaded'"            wd_matches "Overloaded (overloaded_error)" "$DEF"
assert_rc 1 "no match on healthy output"      wd_matches "Build passed. All tests green." "$DEF"
assert_rc 1 "empty pattern never matches"     wd_matches "API Error: boom" ""

echo "## wd_matches (custom)"
assert_rc 0 "custom pattern matches"          wd_matches "xx FLAKY_SIGNAL xx" "FLAKY_SIGNAL"
assert_rc 1 "custom pattern absent"           wd_matches "nothing here" "FLAKY_SIGNAL"

# ---------------------------------------------------------------------------
echo "## wd_effective_patterns"
assert_eq "$DEF" "$(wd_effective_patterns "$DEF" "")" "empty extra -> defaults only"
assert_eq "${DEF}|FOO|BAR" "$(wd_effective_patterns "$DEF" "FOO|BAR")" "extra appended to defaults"

# ---------------------------------------------------------------------------
echo "## wd_get_setting (scoped to watchdog: block)"
TMP_YAML="$(mktemp)"
cat >"$TMP_YAML" <<'YAML'
commands:
    enabled: false
api:
    enabled: true
watchdog:
    enabled: true
    nudge_text: "continue"
    cooldown_sec: 90
    detect_inactivity: false
    patterns: "MY_ERR|SECOND"
plan:
    enabled: false
YAML

assert_eq "true"        "$(wd_get_setting "$TMP_YAML" enabled missingdefault)"      "reads watchdog.enabled (not commands/api)"
assert_eq "continue"    "$(wd_get_setting "$TMP_YAML" nudge_text X)"                "quoted value stripped"
assert_eq "90"          "$(wd_get_setting "$TMP_YAML" cooldown_sec 60)"             "numeric value"
assert_eq "MY_ERR|SECOND" "$(wd_get_setting "$TMP_YAML" patterns "")"               "pattern string with pipe"
assert_eq "fallbackval" "$(wd_get_setting "$TMP_YAML" does_not_exist fallbackval)"  "missing key -> default"
assert_eq "dft"         "$(wd_get_setting /no/such/file.yaml enabled dft)"          "missing file -> default"
rm -f "$TMP_YAML"

# A yaml WITHOUT a watchdog block must not leak another section's key.
TMP_YAML2="$(mktemp)"
cat >"$TMP_YAML2" <<'YAML'
commands:
    enabled: true
api:
    enabled: true
YAML
assert_eq "false" "$(wd_get_setting "$TMP_YAML2" enabled false)" "no watchdog block -> default (no leak)"
rm -f "$TMP_YAML2"

# ---------------------------------------------------------------------------
echo "## wd_should_nudge_inactivity (prev, cur, enabled)"
assert_rc 0 "unchanged + enabled -> nudge"        wd_should_nudge_inactivity "hashA" "hashA" "true"
assert_rc 1 "changed -> no nudge"                 wd_should_nudge_inactivity "hashA" "hashB" "true"
assert_rc 1 "disabled -> no nudge"                wd_should_nudge_inactivity "hashA" "hashA" "false"
assert_rc 1 "empty prev -> no nudge (first scan)" wd_should_nudge_inactivity "" "hashA" "true"

# ---------------------------------------------------------------------------
echo "## wd_cooldown_ok (last_ts, now_ts, cooldown_sec)"
assert_rc 0 "elapsed >= cooldown -> ok"   wd_cooldown_ok 1000 1100 60
assert_rc 1 "elapsed < cooldown -> block" wd_cooldown_ok 1000 1030 60
assert_rc 0 "never nudged (empty) -> ok"  wd_cooldown_ok "" 1000 60
assert_rc 0 "exactly at boundary -> ok"   wd_cooldown_ok 1000 1060 60

# ---------------------------------------------------------------------------
echo "## wd_idle_seconds (activity_epoch, now) -> seconds since last output"
assert_eq "5"  "$(wd_idle_seconds 1000 1005)" "idle = now - activity"
assert_eq "0"  "$(wd_idle_seconds 1005 1000)" "clock skew clamps to 0"
assert_eq "0"  "$(wd_idle_seconds 1000 1000)" "same instant -> 0"
assert_eq "-1" "$(wd_idle_seconds '' 1000)"   "empty activity -> unknown (-1)"
assert_eq "-1" "$(wd_idle_seconds abc 1000)"  "non-numeric activity -> unknown (-1)"

echo "## wd_idle_gate_ok (idle, min_idle) -> may we nudge?"
assert_rc 0 "gate disabled (min_idle=0) -> allow"        wd_idle_gate_ok 0 0
assert_rc 0 "idle >= min_idle -> allow"                  wd_idle_gate_ok 30 10
assert_rc 0 "idle == min_idle -> allow"                  wd_idle_gate_ok 10 10
assert_rc 1 "active (idle < min_idle) -> block nudge"    wd_idle_gate_ok 2 10
assert_rc 0 "unknown idle (-1) -> allow (fail-open)"     wd_idle_gate_ok -1 10

echo "## wd_idle_trigger (idle, threshold) -> nudge purely for being idle?"
assert_rc 0 "idle >= threshold -> trigger"               wd_idle_trigger 120 60
assert_rc 1 "idle < threshold -> no trigger"             wd_idle_trigger 30 60
assert_rc 1 "threshold disabled (0) -> never trigger"    wd_idle_trigger 999 0
assert_rc 1 "unknown idle (-1) -> never trigger"         wd_idle_trigger -1 60

# ---------------------------------------------------------------------------
echo "## wd_work_state (goals_file, tasks_file) -> pending|done|unknown"
G="$(mktemp)"; T="$(mktemp)"
printf 'goals:\n  - status: running\n'  >"$G"; printf 'tasks:\n  - status: done\n' >"$T"
assert_eq "pending" "$(wd_work_state "$G" "$T")" "any running -> pending"
printf 'goals:\n  - status: done\n  - status: failed\n' >"$G"; printf 'tasks:\n  - status: done\n' >"$T"
assert_eq "done"    "$(wd_work_state "$G" "$T")" "all terminal -> done"
printf 'tasks:\n  - status: in_progress\n' >"$T"; printf 'goals:\n  - status: done\n' >"$G"
assert_eq "pending" "$(wd_work_state "$G" "$T")" "in_progress in tasks -> pending"
: >"$G"; : >"$T"
assert_eq "unknown" "$(wd_work_state "$G" "$T")" "empty files -> unknown"
assert_eq "unknown" "$(wd_work_state /no/file.yaml /no/file2.yaml)" "absent files -> unknown"
printf 'goals:\n  - status: done\n' >"$G"
assert_eq "done"    "$(wd_work_state "$G" /no/such.yaml)" "one file all-terminal -> done"
rm -f "$G" "$T"

echo "## wd_should_giveup (nudge_count, max_nudges)"
assert_rc 1 "count < max -> keep going"        wd_should_giveup 2 5
assert_rc 0 "count == max -> give up"          wd_should_giveup 5 5
assert_rc 0 "count > max -> give up"           wd_should_giveup 9 5
assert_rc 1 "max disabled (0) -> never giveup" wd_should_giveup 999 0

# ---------------------------------------------------------------------------
echo ""
echo "RESULT: ${PASS} passed, ${FAIL} failed"
[[ "$FAIL" -eq 0 ]]
