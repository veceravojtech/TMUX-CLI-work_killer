# Taskvisor Spec

Go daemon that sits above supervisor and drives autonomous goal completion through dispatch-validate-retry cycles.

## Window Hierarchy

```
tmux session
├── supervisor       (window 0)  — Claude LLM, human interactive / standalone. NEVER killed or reused for goal execution.
├── taskvisor        (window 1)  — Go daemon (permanent anchor, kill-protected)
├── supervisor-<ns>  (ephemeral) — per-goal goal-execution supervisor, spawned/killed by daemon (always namespaced, even at max_goals=1)
├── validator-<ns>   (ephemeral) — per-goal Claude LLM, created/killed by daemon after the goal supervisor
├── execute-<ns>-N   (ephemeral) — created by the goal supervisor's Claude, killed by supervisor/daemon
└── inv-<ns>-N       (ephemeral) — per-goal read-only investigator workers, spawned by the validator
```

Goal windows are ALWAYS namespaced by the goal's `<ns>` (the goal id with the `goal-` prefix stripped, e.g. `goal-008` → `008`), regardless of `max_goals`. Window-0 `supervisor` is the human's interactive/standalone window: the daemon never kills or recreates it for goal execution ([[never-kill-tmux-server-pid]]).

Both windows are created by `tmux-cli start-attach`:
1. Window 0 = "supervisor" with Claude Code (existing behavior)
2. Window 1 = "taskvisor" with daemon process (`tmux-cli taskvisor --run`)

The daemon starts in **IDLE** mode — hooks work normally, user interacts with Claude in window 0.

### Mode switch: IDLE → ACTIVE

Triggered explicitly by `tmux-cli taskvisor start` (CLI) or `taskvisor-start` MCP tool (Claude can call it).

On activation:
1. Write `.tmux-cli/taskvisor-active` guard file (suppresses hooks)
2. Sweep any leftover per-goal windows (`supervisor-<ns>` etc.) from a prior run; window-0 `supervisor` is left untouched
3. Begin dispatch-validate-retry loop — each dispatch spawns a fresh per-goal `supervisor-<ns>` window for goal execution

Taskvisor is the **sole authority** over the per-goal `supervisor-<ns>` lifecycle while ACTIVE. It never touches window-0 `supervisor`.

### Mode switch: ACTIVE → IDLE

When all goals are done/failed:
1. Tear down the in-flight goal's `supervisor-<ns>` / `validator-<ns>` / `execute-<ns>-` / `inv-<ns>-` windows
2. Ensure window-0 `supervisor` exists (create a bare `supervisor` ONLY if none is live — never kill/recreate the existing one)
3. Remove `.tmux-cli/taskvisor-active` guard file (hooks resume)
4. Return to IDLE, watching for new goals

## State Machine

### Daemon modes

```
┌──────────────────────────────────────────────────────┐
│                                                      │
│  ┌────────┐  taskvisor start  ┌─────────┐            │
│  │  IDLE  │──────────────────▶│  ACTIVE │            │
│  │        │◀──────────────────│         │            │
│  └────────┘  all goals done   └─────────┘            │
│   hooks ON   or failed         hooks OFF             │
│   watching                     owns supervisor       │
│                                                      │
└──────────────────────────────────────────────────────┘
```

### Goal states (4 states, tracked in goals.yaml)

```
                  ┌───────────┐
                  │  PENDING  │
                  └─────┬─────┘
                        │ dispatch: write dispatch.md,
                        │ kill+create supervisor, send /tmux:plan
                        ▼
                  ┌───────────┐
            ┌────▶│  RUNNING  │◀────────────────────┐
            │     └─────┬─────┘                      │
            │           │                            │
            │     ┌─────┴──────────────┐             │
            │     │  phase:supervising │             │
            │     │  poll signal.json  │             │
            │     └─────┬──────────────┘             │
            │           │                            │
            │     signal(supervisor,done|stopped)     │
            │           │                            │
            │     ┌─────▼──────────────┐             │
            │     │  phase:validating  │             │
            │     │  poll signal.json  │             │
            │     └─────┬──────────────┘             │
            │           │                            │
            │     ┌─────┴─────┐                      │
            │     ▼           ▼                      │
            │   pass        fail                     │
            │     │           │                      │
            │     │     retries < max?               │
            │     │      ╱        ╲                  │
            │     │    yes         no                │
            │     │     │           │                │
            │     │  write         │                 │
            │     │  correction    │                 │
            │     │  retries++     │                 │
            │     │  status=       │                 │
            │     │  pending       │                 │
            │     │     │          │                 │
            │     │     └──────────┼──▶ next tick    │
            │     │                │    dispatches ──┘
            │     ▼                ▼
            │ ┌──────┐      ┌──────────┐
            │ │ DONE │      │  FAILED  │
            │ └──┬───┘      └────┬─────┘
            │    │               │
            │    └───────┬───────┘
            │            ▼
            │   advance current_goal
            │   to next pending
            │            │
            │      more goals?
            │       ╱       ╲
            │     yes        no
            └──────┘     switch to IDLE
```

Internal phase (supervising | validating) is tracked in daemon memory only. Goal status in goals.yaml is only: pending | running | done | failed.

## Goals Schema

### goals.yaml (tool-managed only, never manually edited)

```yaml
current_goal: "goal-001"
goals:
  - id: "goal-001"
    description: "Hotel booking page shows correct prices"
    acceptance:
      - "Price matches API response"
      - "No console errors on page load"
    validate:
      - "Open booking page, GET /api/price, compare displayed vs API price"
      - "Run go test ./internal/booking/... — all pass"
    status: pending
    retries: 0
    max_retries: 3
```

### Per-goal runtime directory

```
.tmux-cli/goals/goal-001/
├── signal.json          # written by supervisor OR validator (one file, one path)
├── dispatch.md          # goal + corrections context, written by daemon before dispatch
└── corrections/
    ├── cycle-1.md       # validation failure details from cycle 1
    └── cycle-2.md
```

### Signal file (unified schema, one path per goal)

From supervisor (written by Claude in step 9c):
```json
{"source": "supervisor", "status": "done", "timestamp": "2026-05-20T14:30:00Z"}
```

From validator (written by goal-validation-done MCP tool):
```json
{
  "source": "validator",
  "verdict": "pass",
  "findings": [{"rule": "price check", "status": "pass", "detail": "..."}],
  "next_action": "",
  "timestamp": "2026-05-20T14:35:00Z"
}
```

Daemon parses on `source` field: supervisor -> read `status` (both `done` and `stopped` proceed to validation — the validator checks acceptance criteria regardless of whether all tasks completed), validator -> read `verdict`.

## Daemon Flow

### Session startup (in `start-attach`)

`tmux-cli start-attach` creates both windows:
1. Window 0 = "supervisor" — existing behavior (Claude Code, post-command, UUID)
2. Window 1 = "taskvisor" — runs `tmux-cli taskvisor --run` (daemon process, foreground)

The `--run` flag is internal — it starts the daemon loop directly. The daemon begins in IDLE mode: polls goals.yaml every `poll_interval` seconds, waiting for `taskvisor start` signal.

### activate() — triggered by `taskvisor start` CLI or `taskvisor-start` MCP tool

1. Write `.tmux-cli/taskvisor-active` guard file (suppresses hooks)
2. Verify `plan.auto_approve` and `plan.auto_execute` are true in setting.yaml — if not, set them (required for autonomous operation)
3. **RequirePlanApproval gate**: if `require_plan_approval: true` in setting.yaml and `docs/architecture/plan-approval.md` does not exist, set `haltReason` and call `deactivate()`. The approval file is produced by `/tmux:plan`'s blind audit gate (plan.xml step 11a, a serial native sub-agent) — run a plan before starting the daemon when the gate is enabled.
4. Set `current_goal` to first pending goal in goals.yaml
5. Sweep leftover per-goal windows (`supervisor-<ns>` / `execute-<ns>-` / `validator-<ns>` / `inv-<ns>-`) from a prior run — window-0 `supervisor` is NOT in the sweep set
6. Begin dispatch loop

### deactivate() — when all goals done/failed

1. Tear down the in-flight goal's per-goal windows (`supervisor-<ns>` / `validator-<ns>` / `execute-<ns>-` / `inv-<ns>-`)
2. Wait for killed windows to disappear (same poll-until-gone pattern as dispatch)
3. Ensure window-0 `supervisor` exists: list windows; if NONE is named `supervisor`, create a bare `supervisor` (`WindowsCreate("supervisor", "")`) + wait for Claude boot; otherwise no-op. NEVER kill/recreate an existing window-0 `supervisor` ([[never-kill-tmux-server-pid]]).
4. Remove `.tmux-cli/taskvisor-active` guard file (hooks resume)
5. Return to IDLE mode

### dispatch(goal)

1. Write `.tmux-cli/goals/<id>/dispatch.md` using the dispatch template (see below)
2. Write `.tmux-cli/taskvisor-current-goal` with goal ID (supervisor reads this in step 9b to know where to write signal)
3. Compute the per-goal `supervisor-<ns>` name and write it byte-exact to `.tmux-cli/goals/<id>/supervisor-window` (the marker the supervisor/plan agent reads to self-identify)
4. Kill any leftover windows under THIS goal's namespace (`supervisor-<ns>` / `execute-<ns>-` / `validator-<ns>` / `inv-<ns>-`) — sibling goals and window-0 `supervisor` are untouched
5. **Wait for killed windows to disappear**: poll `ListWindows()` until this goal's namespaced windows are gone (5s timeout). `WindowsCreate` enforces name uniqueness.
6. Create the per-goal `supervisor-<ns>` window via `WindowsCreate("supervisor-<ns>", cwd)` (cwd = the goal's git worktree at max_goals>1) — gets UUID + env + post-command setup
7. Wait for Claude to boot: poll `ListWindows()` until the goal supervisor's `CurrentCommand != "zsh"` (30s timeout, check every 2s)
8. Send `/tmux:plan .tmux-cli/goals/<id>/dispatch.md <id>` via tmux executor SendMessage
9. Set goal status=running, save goals.yaml
10. Record dispatch timestamp for timeout tracking

### dispatch.md template

Validation rules are deliberately excluded — the supervisor should implement to acceptance criteria, not teach to the test. Validate rules are internal to the daemon/validator cycle.

```markdown
# Goal: <description>

## Acceptance Criteria
- <acceptance[0]>
- <acceptance[1]>
- ...

## Corrections from Previous Cycles
<contents of corrections/cycle-1.md, cycle-2.md, etc. — or "None (first attempt)" if no corrections>
```

### Dispatch-time spec-drift gate

At dispatch time (both `dispatch()` and `dispatchRetry()`), the daemon detects divergence between the per-goal `goal.md` "Validation Rules" section and the canonical `goals.yaml` validate entries. goals.yaml is always the source of truth — if drift is detected, `repairValidationRules` splices only the "Validation Rules" section in goal.md back to match goals.yaml, preserving all other goal.md prose (Context, Not In Scope, Investigation Config, etc.).

**Detection**: symmetric set comparison — rules present in goal.md but absent in goals.yaml (extras) and rules in goals.yaml but missing from goal.md (missing) both count as drift.

**Repair**: splice-based via `repairValidationRules` — only the Validation Rules section between its `## ` heading and the next heading (or EOF) is replaced. A missing goal.md is a no-op (not an error).

**Budget**: drift repair incurs zero retry budget charge — the goal's retry counters are untouched. The daemon increments a `specRepairs` counter and the dashboard renders a `spec repairs: N` line when N > 0.

**Fail-loud**: if the splice repair itself fails (e.g. unwritable goal directory), dispatch aborts with an error.

### checkProgress(goal)

**Phase: supervising**
1. Check for `.tmux-cli/goals/<id>/signal.json`
2. If found with `source=supervisor` (status=done or status=stopped — both proceed to validation):
   - Store `last_supervisor_status` in daemon memory (needed for correction context)
   - Delete signal.json
   - Kill all execute-N windows (list, filter by prefix, kill via raw executor — name-to-ID resolution)
   - Kill supervisor window (via raw executor, not MCP — avoids last-window check since taskvisor window is always present)
   - Wait for killed windows to disappear (same poll-until-gone pattern as dispatch step 5)
   - Create "validator" window via `mcp.NewServer(workDir).WindowsCreate("validator", "")`
   - Wait for Claude boot (same poll pattern as dispatch)
   - Send `/tmux:validate` then after 2s send task payload (goal description + acceptance + validate rules)
   - Switch internal phase to "validating", record timestamp
3. If not found: check dispatch_timeout. Check if Claude exited (CurrentCommand="zsh" after boot confirmed + 5s grace -> no signal = crash -> treat as fail)

**Phase: validating**
1. Check for `.tmux-cli/goals/<id>/signal.json`
2. If found with `source=validator`:
   - verdict=pass -> kill validator, set goal status=done, advance current_goal to next pending, save
   - verdict=fail -> kill validator, write corrections/cycle-N.md (see below), increment retries
     - retries < max_retries -> set status=pending (next tick re-dispatches)
     - retries >= max_retries -> set status=failed, advance current_goal
   - Delete signal.json
3. If not found: check validate_timeout. Same Claude-exit detection.

**Correction context by supervisor exit status:**
When writing `corrections/cycle-N.md`, the daemon prefixes the validator's `next_action` with context based on `last_supervisor_status`:
- `done` (supervisor finished all tasks): correction is a standard bug/quality fix — the implementation completed but failed validation. Correction header: "Implementation completed but failed acceptance criteria."
- `stopped` (supervisor hit cycle limit): correction reflects incomplete work, not wrong work — the supervisor ran out of cycles before finishing. Correction header: "Previous cycle hit the supervisor cycle limit — work is incomplete. Prioritize the unmet criteria below over polish or cleanup." This tells the next supervisor to focus on the gaps rather than re-doing work that already exists.

### Crash recovery (daemon restart via `tmux-cli taskvisor --run`)

1. Check for `.tmux-cli/taskvisor-active` guard file:
   - If present: daemon was ACTIVE before crash, resume ACTIVE mode
   - If absent: start in IDLE mode (normal startup)
2. If resuming ACTIVE:
   a. Read goals.yaml, find goal with status=running
   b. Check for signal.json FIRST (may have been written before crash)
   c. If signal exists -> process normally
   d. If no signal -> check which windows exist (via ListWindows):
      - "validator" exists -> resume validating phase, reset timeout clock to now
      - "supervisor" exists -> resume supervising phase, reset timeout clock to now
      - Neither exists -> treat as failed cycle, set status=pending (retries permitting)

### Stale-binary guard

Detects when the `tmux-cli` binary on disk has changed since the daemon (or MCP server) started, indicating a `make install` was run without restarting.

**Detection**: `setup.BinaryStale()` compares the current mtime and size of `os.Executable()` against a snapshot taken at process init (`sync.Once`). Checked once per minute in `tick()` (throttled via `d.lastStaleCheck`). If `os.Executable()` fails at init, the guard degrades gracefully — `BinaryStale()` always returns false.

**Dashboard banner** (non-fatal): when staleness is detected, the dashboard renders a yellow/bold warning: `BINARY STALE — restart taskvisor to apply (<mtime>)`. The daemon continues operating — this is informational only.

**Opt-in halt** (`halt_on_stale_binary: true`): when enabled, after setting the banner the daemon calls `haltStaleBinary()` which mirrors the `haltWallClock()` pattern — sets `haltReason`, calls `deactivate()`, and leaves all goal statuses untouched (no forced failures). The in-flight goal finishes its current phase before the halt takes effect on the next tick.

**MCP tool-result warning**: independently, every MCP tool handler checks `BinaryStale()` via `prependStaleWarning`. When stale, the handler prepends a text warning `[tmux-cli mcp is stale: <detail>; restart the MCP server]` before the normal JSON output. No schema changes — the warning is an additional `TextContent` item.

**Version info**: `vcs.revision` from `debug.ReadBuildInfo()` is displayed in both the `--version` CLI output and the dashboard header (IDLE and ACTIVE modes). Falls back to `"dev"` when build info is unavailable.

### Runtime-resource co-scheduling guard

At `max_goals > 1` the daemon gates co-dispatch on disjoint file scope (`ScopesDisjoint` in `scope_gate.go`) and per-goal git worktrees isolate checkouts. However, the docker compose stack, its database, and host ports are SHARED runtime resources that worktrees do not isolate. Two co-scheduled goals each running `bash bin/ensure-test-stack.sh` would trigger concurrent `doctrine:fixtures:load` — one goal's fixture truncate wipes the DB under the other goal's in-flight Playwright run.

**Stack-consuming classifier**: `isStackConsuming(g *Goal) bool` mechanically scans a goal's `Validate` and `Acceptance` lines for a conservative substring marker set: `ensure-test-stack`, `npx playwright`, `docker compose`, `curl -sf http`, `curl -s http`, `curl -s -o`. Detection is case-sensitive (matching the generator's exact command conventions), pure (no I/O), and substring-based (so `bash bin/ensure-test-stack.sh && echo done` still matches). False positives (over-serialization) are safe; false negatives (concurrent stack access) are not.

**Co-schedulability extension**: `coSchedulable` is extended so that AFTER the existing `ScopesDisjoint` check, if the candidate is stack-consuming AND any in-flight goal is also stack-consuming, the candidate is rejected. Pure-unit goals co-schedule freely with stack-consumers. The check runs after scope disjointness (the common path stays cheap).

**Dashboard/log surfacing**: `Daemon.stackGateSkips` counts runnable stack-consuming candidates deferred due to an in-flight stack-consumer. A `log.Printf` line per skip includes the goal ID and "stack-gated". The dashboard renders a yellow `stack-gated: N` line when > 0, mirroring the `dep warnings` pattern. The counter resets in `activate()`.

**Byte-identical at `max_goals = 1`**: the stack-consuming gate runs inside `coSchedulable`, which is only consulted by `DisjointReadySet`. At `max_goals = 1` the dispatch budget caps to one goal regardless, so the gate is never the deciding factor. The `stackGateSkips` counter is only computed when `maxGoals() > 1`.

**Zero-config**: the guard is always-on at `max_goals > 1` — no new config keys. It is a correctness invariant, not a preference; disabling it re-opens the concurrent-fixture-load race.

**No schema changes**: `isStackConsuming` reads existing `Goal.Validate` and `Goal.Acceptance` fields. No new Goal struct fields, no new MCP tools, no modification of `modeIdle`/`modeActive` contract.

## Communication Protocol

### Supervisor -> Daemon

Supervisor writes signal file as last action. Amend step 9b in supervisor.xml — add taskvisor signal write into each terminal branch:

```xml
<step n="9b" title="Cycle completion and continuation">
  <condition>Step 9 synthesis is complete</condition>
  <action>Read .tmux-cli/tasks.yaml and mark all completed tasks as status=done.</action>
  <action>Read supervisor.max_cycles from .tmux-cli/setting.yaml (default: 0 = unlimited).</action>
  <action>Increment the cycle counter in tasks.yaml.</action>
  <check condition="pending tasks remain AND (max_cycles == 0 OR cycle &lt; max_cycles)">
    <!-- Taskvisor signal: NOT written here — supervisor continues cycling in-window -->
    <action>Send /clear to SUPERVISOR_WID via windows-send.</action>
    <action>Send /tmux:supervisor .tmux-cli/tasks.yaml to SUPERVISOR_WID via windows-send.</action>
    <action>STOP — a fresh supervisor instance takes over with the updated tasks.yaml as input.</action>
  </check>
  <check condition="max_cycles reached (cycle >= max_cycles AND max_cycles > 0)">
    <action>Print to user: "Cycle limit reached ({cycle}/{max_cycles}). Remaining pending tasks: [list task names]."</action>
    <action>Taskvisor signal: read .tmux-cli/taskvisor-current-goal. If file exists, write .tmux-cli/goals/GOAL_ID/signal.json: {"source":"supervisor","status":"stopped","timestamp":"ISO8601"}. This MUST be the last action before exit.</action>
    <action>STOP.</action>
  </check>
  <check condition="no pending tasks remain">
    <action>Taskvisor signal: read .tmux-cli/taskvisor-current-goal. If file exists, write .tmux-cli/goals/GOAL_ID/signal.json: {"source":"supervisor","status":"done","timestamp":"ISO8601"}. This MUST be the last action before exit.</action>
    <action>Normal stop — all work is complete.</action>
  </check>
</step>
```

Note: the signal write is conditional on `taskvisor-current-goal` file existing — when not running under taskvisor, the file is absent and the actions are skipped. No separate step 9c needed.

### Validator -> Daemon

Validator calls `goal-validation-done` MCP tool. The MCP handler writes signal.json atomically (tmp+rename).

### Hook Suppression

Guard file `.tmux-cli/taskvisor-active` is written on `activate()` and removed on `deactivate()`. While IDLE, no guard file exists — hooks work normally.

Both `tmux-supervisor-cycle.sh` and `tmux-unplanned-audit.sh` get one line near top:
```bash
[[ -f "$PROJECT_DIR/.tmux-cli/taskvisor-active" ]] && exit 0
```

## Skill Amendments

### supervisor.xml — Step 2 (self-identification)

Current fallback when multiple windows: "ask the user."
New fallback: "If a window named 'supervisor' exists in windows-list, that is your SUPERVISOR_WID. Use it without asking."

### plan.xml — Step 2 (self-identification)

Same amendment as supervisor.xml. Current fallback: "ask the user." New fallback: "If a window named 'supervisor' exists in windows-list, that is your PLAN_WID. Use it without asking."

Both skills run in the daemon-created "supervisor" window alongside taskvisor (window 1) and possibly execute-N workers. Without this amendment, the skills would prompt for user input when multiple windows exist — and there is no user under taskvisor.

### supervisor.xml — Step 9b (amended — taskvisor signal in terminal branches)

See Communication Protocol above. Signal write is integrated into each terminal branch of step 9b (max_cycles reached → status=stopped, no pending tasks → status=done). The "continue cycling" branch does NOT write a signal.

## MCP Tools (3 new)

### taskvisor-start

- Input: none
- Action: writes `.tmux-cli/taskvisor-start` signal file. Daemon polls for this file in IDLE mode and transitions to ACTIVE.
- Authorization: none (any Claude instance can trigger)
- Validation: returns error if goals.yaml has no pending goals

### goal-create

- Input: description (string), acceptance ([]string), validate ([]string), max_retries (int, default 3)
- Action: generates sequential ID (goal-001, goal-002, ...), appends to goals.yaml, creates per-goal directory
- Authorization: none (any Claude instance can queue goals)
- Write: atomic (tmp+rename) on goals.yaml
- Note on concurrency: concurrent calls from multiple Claude instances can collide (read-modify-write without lock). Acceptable for v1 — add flock if it becomes an issue.

### goal-validation-done

- Input: goal_id (string), verdict ("pass"|"fail"), findings ([]ValidationFinding), next_action (string)
- Action: writes `.tmux-cli/goals/<goal_id>/signal.json` atomically (tmp+rename)
- Authorization: checks TMUX_WINDOW_UUID matches the "validator" window UUID (list windows, match UUID to name)

### ValidationFinding struct
```go
type ValidationFinding struct {
    Rule       string `json:"rule"`
    Status     string `json:"status"`     // pass | fail
    Detail     string `json:"detail"`
    Correction string `json:"correction"`
}
```

## Validator Skill (/tmux:validate)

New Claude Code skill that:
- Receives task payload: goal description, acceptance criteria, validate rules
- Checks each validate rule using available tools (Chrome, terminal, file reading)
- Calls `goal-validation-done` MCP tool with verdict and findings
- Prompt-enforced read-only: "Do not modify source files. Your only write action is calling goal-validation-done."

## Settings

Added to `internal/setup/config.go`:

```go
type TaskvisorSettings struct {
    DispatchTimeout    int  `yaml:"dispatch_timeout"`     // default 3600 (60 min) — covers planning + execution phases
    ValidateTimeout    int  `yaml:"validate_timeout"`     // default 300 (5 min)
    PollInterval       int  `yaml:"poll_interval"`        // default 5 (seconds)
    RequirePlanApproval bool `yaml:"require_plan_approval"` // default false
    HaltOnStaleBinary   bool `yaml:"halt_on_stale_binary"`  // default false
}
```

Note: dispatch_timeout covers the entire `/tmux:plan` → `/tmux:supervisor` chain. Complex goals with multiple spec-writing + implementation workers may need longer. Adjust per-project in setting.yaml.

Added as field to existing Settings struct:
```go
type Settings struct {
    // ... existing fields
    Taskvisor TaskvisorSettings `yaml:"taskvisor"`
}
```

### Hardening config keys

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `require_plan_approval` | bool | `false` | When true, `activate()` halts unless `docs/architecture/plan-approval.md` exists. `/tmux:plan`'s blind audit gate (step 11a) produces the approval file. |
| `halt_on_stale_binary` | bool | `false` | When true, the daemon halts (after finishing the in-flight goal phase) if the binary on disk has changed since startup. When false, only a dashboard banner is shown. |

## CLI Commands

```
tmux-cli taskvisor start              # trigger IDLE → ACTIVE (writes signal file, same as taskvisor-start MCP tool)
tmux-cli taskvisor goal add           # add goal (flags: --description, --acceptance, --validate, --max-retries)
tmux-cli taskvisor goal list          # print goals with status
tmux-cli taskvisor --run              # internal: start daemon loop (called by start-attach, not user-facing)
```

The `--run` flag is used by `start-attach` when creating window 1. It starts the daemon loop directly in that window as a foreground process.

## User Journey

```
$ cd my-project
$ tmux-cli start-attach              # creates window 0 (supervisor + Claude) and window 1 (taskvisor daemon in IDLE)
                                      # user is attached to window 0

You: "Plan goals for: rewrite booking to async, then verify in browser"
Claude: [calls goal-create twice, then calls taskvisor-start]

[daemon transitions IDLE → ACTIVE]
[daemon kills supervisor (window 0), recreates it for goal execution]
[daemon runs goals autonomously]
[all goals done → daemon recreates supervisor with fresh Claude, returns to IDLE]

$ cat .tmux-cli/goals.yaml           # review results (or just talk to Claude — it's back in window 0)
```

Alternative trigger — user can also start from CLI:
```
$ tmux-cli taskvisor start           # same effect as taskvisor-start MCP tool
```

## Goal Planning Entry Points

1. **Conversational** (recommended): talk to Claude in supervisor window (window 0), Claude calls goal-create MCP tool
2. **CLI**: `tmux-cli taskvisor goal add --description "..." --acceptance "..." --validate "..."`
3. **From any Claude instance**: any window can call goal-create MCP tool to queue goals

## Code Changes

| File | Action | ~Lines |
|---|---|---|
| `internal/taskvisor/taskvisor.go` | Create — daemon loop (IDLE/ACTIVE), state machine, activate/deactivate, dispatch, checkProgress, crash recovery, name-to-ID resolution helpers | ~500 |
| `internal/mcp/tools_taskvisor.go` | Create — taskvisor-start, goal-create, goal-validation-done MCP tools | ~180 |
| `internal/mcp/tools.go` | Modify — add kill protection for "taskvisor" window in WindowsKill | ~5 |
| `internal/setup/config.go` | Modify — add TaskvisorSettings to Settings struct | ~10 |
| `cmd/tmux-cli/session.go` | Modify — add taskvisor cobra commands, modify start-attach to create window 1 | ~70 |
| `internal/session/manager.go` | Modify — CreateSession creates taskvisor window (window 1) after supervisor setup | ~20 |
| `cmd/tmux-cli/embedded/commands/tmux/supervisor.xml` | Modify — amend step 9b terminal branches (signal write), amend step 2 fallback | ~15 |
| `cmd/tmux-cli/embedded/commands/tmux/plan.xml` | Modify — amend step 2 fallback (same as supervisor.xml) | ~5 |
| `cmd/tmux-cli/embedded/tmux-supervisor-cycle.sh` | Modify — add taskvisor-active guard check | 1 |
| `cmd/tmux-cli/embedded/tmux-unplanned-audit.sh` | Modify — add taskvisor-active guard check | 1 |
| `cmd/tmux-cli/embedded/commands/tmux/validate.xml` | Create — validator skill | ~100 |
| `cmd/tmux-cli/embedded/commands/tmux/validate.md` | Create — validator skill description | ~10 |
| **Total** | | **~900** |

## Implementation Warnings

1. **Recovery timestamps**: on daemon restart, reset timeout clock to "now" (safer than treating unknown duration as expired)
2. **Hook guard variable**: use `$PROJECT_DIR` (matches existing hook scripts), not `$PROJECT_ROOT`
3. **Kill via raw executor**: use `executor.KillWindow()` for kills, not `mcp.Server.WindowsKill()` — avoids last-window safety check (taskvisor window is always present) and name-only restriction. Daemon must resolve window names to @N IDs via `ListWindows()` before calling `KillWindow()`
4. **Boot detection order**: do NOT apply the 5s crash-grace-period during initial boot wait — only after Claude confirmed running
5. **Window creation**: daemon uses `mcp.NewServer(workDir).WindowsCreate()` for creates (gets UUID + env + post-command setup for free), raw executor for kills
6. **Kill protection**: add name check in `WindowsKill` MCP tool (tools.go) — reject killing window named "taskvisor", same pattern as last-window check
7. **No rename**: do NOT rename window 0 from "supervisor" — taskvisor is an additive layer (window 1) that must not break existing non-taskvisor functionality
8. **AUTO_APPROVE/AUTO_EXECUTE**: defaults are already true (`config.go:68-70`). Daemon verifies on `activate()` and sets them if overridden to false — required for autonomous `/tmux:plan` → `/tmux:supervisor` chain
9. **Guard file lifecycle**: written on `activate()`, removed on `deactivate()`. On daemon restart, presence of guard file = resume ACTIVE mode. On SIGTERM/SIGINT (signal handler): check `executor.HasSession()` first — if session is gone (e.g. `tmux-cli kill` triggered the signal), only remove the guard file and exit. If session still exists, run full `deactivate()` cleanup (kill windows, recreate supervisor, remove guard) before exit
10. **Start-attach integration**: `manager.CreateSession()` must create window 1 after window 0 setup. Use `mcp.NewServer(workDir).WindowsCreate("taskvisor", "")` then send `tmux-cli taskvisor --run` to start daemon. Daemon must handle the case where session already exists (re-attach) — check if "taskvisor" window exists; if present but idle (CurrentCommand="zsh"), re-send `tmux-cli taskvisor --run`; if present and running, skip creation
11. **IDLE signal file**: daemon polls for `.tmux-cli/taskvisor-start` in IDLE mode. Both CLI (`taskvisor start`) and MCP tool (`taskvisor-start`) write this file. Daemon deletes it after reading

## What's NOT Built (v1)

- No file locking package (each file has single writer, except goal-create which is acceptable risk)
- No git stash before retries (next supervisor deals with existing state)
- No STALLED/INCONCLUSIVE states (timeout = failed, stopped = still validate)
- No goal-planning skill (conversational planning with Claude is sufficient)
- No SIGKILL-safe guard cleanup (signal handler covers SIGTERM/SIGINT; SIGKILL leaves stale guard — next daemon start cleans it up)

> **E1 capability (delivered):** parallel independent-goal execution (default `MaxGoals=1`, gated on disjoint goal scope + a per-goal git worktree), plus worktree isolation for the validator. Implemented by the E1 scheduler/disjoint-gate, per-goal worktree, db-lock, and validate-isolation tasks — superseding two earlier v1 non-goals.
