# ADR-1: Daemon-driven recurring supervisor task for taskvisor

- **Status:** Accepted
- **Date:** 2026-06-23
- **Bounded Context:** taskvisor (internal/taskvisor + internal/mcp), module github.com/console/tmux-cli
- **Related ADRs:** none

## Context

Operators want a *recurring* supervisor task: a free-text prompt (e.g. "check emulator for
model 3/Y and examine GW implement process, then create actionable tasks to push it further")
that is re-run against the `/tmux:supervisor` skill in cycles. Each cycle must start from a fresh
context (`/clear`), and the next cycle must only begin once the previous cycle's work has fully
drained (no activity, all spawned work done). A finite number of cycles (e.g. 10) runs, then the
recurring task stops. The active recurring task must be visible in the taskvisor dashboard.

A read-only investigation (four parallel investigators, dossiers under
`.tmux-cli/research/2026-06-23-23/`) established the seams and the nearest prior art.

### Decision Drivers

- **MCP handlers must not block, and `/clear` must not wipe the loop driver.** `/clear` +
  `/tmux:supervisor` are sent to the bare `supervisor` window-0. If the loop ran inside an MCP tool
  handler invoked from that window, the first `/clear` would erase the driver's own context, and a
  cycle takes minutes–hours. Therefore the MCP tool only *persists intent*; the long-lived
  taskvisor daemon *runs* the cycles — mirroring the existing `taskvisor-start` →
  signal-file → `poll()` → `activate()` pattern.
- **Prior art is uniform and copy-ready.** `taskvisor-start` is the end-to-end exemplar;
  `taskgoals.go` is the lock-free atomic-YAML state-file skeleton; the per-goal `type phase int` /
  `goalRuntime` / `checkProgress` machine is the per-cycle state-machine template.
- **DUAL-STRUCT invariant (AGENTS.md:120).** Persisted state touched by both the daemon and the MCP
  layer (`Goal` ↔ `mcp.tvGoal`) silently drops fields missing from one side on a load-resave. This
  is a documented hazard, not a feature to replicate.
- **File-length gate.** `make check-file-lengths` enforces 2000 lines/file; `statemachine.go`
  (1735) and `dispatch.go` are near the ceiling, so new logic must live in new files.
- **Stop-hook conflict.** `tmux-supervisor-cycle.sh` re-injects `/clear` + `/tmux:supervisor` on
  Claude exit in the supervisor window; between recurring cycles this would double-dispatch and
  corrupt the cycle counter. The hook already defers when `.tmux-cli/taskvisor-active` exists.

## Decision

Implement the feature as **Option 1 — a single shared state type across the MCP/daemon boundary**,
modeled on the cited exemplars (style preserved even where imperfect; `go vet` / `gofmt` /
`check-file-lengths` gates still bind).

- **Persistence role:** one exported `taskvisor.RecurringFile` / `RecurringTask` /
  `RecurringCycle` + `LoadRecurring` / `SaveRecurring` / `RecurringFilePath` in a new
  `internal/taskvisor/recurring.go`, mirroring `taskgoals.go`'s lock-free `atomicWrite` ledger.
  The MCP `recurring-*` tools import `taskvisor` and reuse these SAME types (the MCP layer already
  calls `taskvisor.WithGoalsLock`), so there is **no second `tv*` struct to drift** — a deliberate,
  justified departure from the `goals.go`↔`tvGoal` dual-struct quirk (DUAL-STRUCT hazard avoided).
- **MCP role:** `recurring-create` / `recurring-status` / `recurring-stop` handlers + core methods
  in a new `internal/mcp/tools_recurring.go`, mirroring `TaskvisorStart` (validate → atomic write →
  return immediately; never `SendMessage` from the handler). `recurring-stop` mirrors `GoalPrune`
  (idempotent state clear).
- **Control-loop role:** a `recurringRuntime` + `(d *Daemon) driveRecurring(goals)` in a new
  `internal/taskvisor/recurring_driver.go`, called from `tick()` immediately after the `modeActive`
  guard (`statemachine.go:81`), before the goal scheduler. `type cyclePhase int`
  (`cyclePhaseDispatching → cyclePhaseSettling → cyclePhaseSettled`) mirrors `type phase int`.
  `poll()`'s `modeIdle` branch gains a `recurring.yaml` pickup that activates the daemon with zero
  goals. The tick step-5 teardown predicate is extended with `&& !d.recurringActive()` so the
  daemon stays `modeActive` between cycles (never routing through `deactivateOnCompletion` /
  `notifyCompletion`).
- **Settle role (STRICT):** `(d *Daemon) recurringSettled(...)` is true only when, sustained for
  `idle_grace_sec` and after a `boot_min_sec` floor since dispatch: no running/runnable goals
  (`AnyRunning` / `RunnableCandidates`); no live worker windows (`listWindows` + `classifyWindow`);
  `tasks.yaml` has no pending/in_progress entries; and the supervisor pane FNV-1a digest
  (`hashPane` + `CaptureWindowOutput`) is static. A `max_cycle_wall_sec` cap force-settles with
  `outcome: timeout`. Timers use the injectable `d.now()` clock.
- **Presentation role:** `renderRecurringSection` + `collectRecurring` mirror
  `renderMappingsSection` / `collectMappings`; wired into `RenderBoard` so the section shows in both
  the standalone and daemon-foreground boards (file-backed, `exec`-independent).
- **Hook role:** a one-line `[[ -f "$PROJECT_DIR/.tmux-cli/recurring-active" ]] && exit 0` guard in
  the **embedded** `cmd/tmux-cli/embedded/tmux-supervisor-cycle.sh` right after the existing
  `taskvisor-active` check. The `recurring-active` marker is created/removed in lock-step with
  `taskvisor-active` and reconciled on daemon restart (no orphan-marker leak).

Each cycle dispatches the LITERAL same `/tmux:supervisor <prompt>` after `/clear` to the bare
`supervisor` window (fresh context per cycle — a recurring poll). Cycle count is finite.

**Frozen naming manifest (Stages 3–4 use verbatim):**

| Role | Name | cf. exemplar | Path |
|------|------|--------------|------|
| MCP input/output | `RecurringCreateInput/Output`, `RecurringStatusInput/Output`, `RecurringStopInput/Output` | `TaskvisorStartInput/Output` | `internal/mcp/server.go` |
| MCP handlers | `RecurringCreateHandler` / `RecurringStatusHandler` / `RecurringStopHandler` | `TaskvisorStartHandler` | `internal/mcp/server.go` |
| MCP core methods | `(s *Server) RecurringCreate/RecurringStatus/RecurringStop` | `TaskvisorStart` | `internal/mcp/tools_recurring.go` |
| tool registrations | `recurring-create` / `recurring-status` / `recurring-stop` | `taskvisor-start` | `internal/mcp/server.go` |
| persisted task model | `RecurringTask` | `Goal` | `internal/taskvisor/recurring.go` |
| per-cycle record | `RecurringCycle{Index,Phase,DispatchedAt,LastActivityAt,Outcome}` | `goalRuntime` fields | `internal/taskvisor/recurring.go` |
| file root struct | `RecurringFile` | `TaskGoalsFile` | `internal/taskvisor/recurring.go` |
| load/save/path | `LoadRecurring` / `SaveRecurring` / `RecurringFilePath` | `LoadTaskGoals` / `SaveTaskGoals` / `TaskGoalsFilePath` | `internal/taskvisor/recurring.go` |
| persisted statuses | `RecurringActive/RecurringStopped/RecurringDone` = `"active"/"stopped"/"done"` | `GoalPending`… | `internal/taskvisor/recurring.go` |
| runtime phase enum | `type cyclePhase int` + `cyclePhaseDispatching/Settling/Settled` + `cyclePhaseName` | `type phase int` + `phaseName` | `internal/taskvisor/recurring_driver.go` |
| driver runtime | `recurringRuntime` | `goalRuntime` | `internal/taskvisor/recurring_driver.go` |
| driver method | `(d *Daemon) driveRecurring(goals)` | `checkProgress` | `internal/taskvisor/recurring_driver.go` |
| settle predicate | `(d *Daemon) recurringSettled(...) bool` | — | `internal/taskvisor/recurring_driver.go` |
| activation | `poll()` modeIdle `recurring.yaml` pickup + `d.recurringActive()` guard | `taskvisor-start` pickup | `internal/taskvisor/daemon.go`, `recurring_driver.go` |
| dashboard section | `renderRecurringSection` + `collectRecurring` | `renderMappingsSection` / `collectMappings` | `internal/taskvisor/dashboard.go` |
| hook guard | `[[ -f recurring-active ]] && exit 0` | `taskvisor-active` guard | `cmd/tmux-cli/embedded/tmux-supervisor-cycle.sh` |
| [test] state+driver | `recurring_test.go` (round-trip + settle + cycle-advance, `fakeClock`) | `goals_test.go` / `statemachine_test.go` | `internal/taskvisor/recurring_test.go` |
| [test] mcp tools | `tools_recurring_test.go` | `tvtools_start_test.go` | `internal/mcp/tools_recurring_test.go` |
| [test] helper | `writeRecurring(t,dir,*RecurringFile)` | `writeGoals` | `internal/taskvisor/tv_helpers_test.go` |

`recurring.yaml` fields (`RecurringTask`): `id`, `prompt`, `target_window`, `total_cycles`,
`completed_cycles`, `status`, `clear_between`, `idle_grace_sec`, `boot_min_sec`, `cooldown_sec`,
`max_cycle_wall_sec`, `created_at`, `current_cycle{index,phase,dispatched_at,last_activity_at,
last_progress_hash}`, `history[]{cycle,started_at,finished_at,outcome}`.

**Test strategy:** tests required (test-first deliverable). All non-test gates also enforced.

## Consequences

### Positive
- Drift-proof persistence: one shared `RecurringFile` type, no `tv*` mirror to keep in sync.
- Maximal reuse: settle predicate, dispatch, dashboard, activation, and hook deferral are all built
  from existing daemon primitives; minimal new surface.
- Hook deferral is explicit and robust via the dedicated `recurring-active` marker; no fragile YAML
  parsing in bash.
- New code is isolated in new files (`recurring.go`, `recurring_driver.go`, `tools_recurring.go`),
  keeping `statemachine.go` / `dispatch.go` under the 2000-line gate.

### Negative
- Departs from the `goals.go`↔`tvGoal` dual-struct exemplar in one place (justified: that pattern is
  a documented hazard, not a target).
- The driver adds a second per-tick consumer of `CaptureWindowOutput` / `ListWindows`; kept bounded
  and best-effort (log-and-swallow), reusing the heartbeat capture where possible.

### Neutral
- A single active recurring task is supported (matches "redispatch the recurring task"); a queue is
  out of scope for v1.
- Cycle count is finite only; an infinite/run-until-stopped mode is deferred.

## Alternatives Considered

### Option 2 — Faithful dual-struct mirror
- Description: replicate the `goals.go`↔`mcp.tvGoal` pattern exactly — a daemon-side `RecurringFile`
  and a separate `mcp.tvRecurringFile` kept byte-compatible, guarded by a round-trip parity test.
- Pros: maximally faithful to the primary exemplar.
- Cons: carries the documented DUAL-STRUCT drift risk for no functional gain.
- Reason rejected: the MCP layer already imports `taskvisor`, so a single shared type is both
  simpler and removes the drift hazard; faithfulness to an imperfect quirk is not worth the risk.

### Standalone `tmux-cli recur` daemon / watchdog-hook driver
- Description: run the cycle loop in a separate process or extend the bash watchdog.
- Pros: looser coupling to taskvisor.
- Cons: duplicates activity-detection, census, and dashboard plumbing; bash is a poor fit for
  cycle-count/history state.
- Reason rejected: the taskvisor daemon already owns the poll loop, idle detection, dashboard, and
  atomic state — design decision A (locked) places the driver there.
