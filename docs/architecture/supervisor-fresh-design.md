# Design — `/tmux:supervisor:fresh` — fresh-context wave handoff from a lone plan file

**Status:** Design (not yet implemented). Drafted 2026-07-21.
**Author:** drafted with Vojta.
**Scope:** A new embedded command `/tmux:supervisor:fresh <plan-file>` (shipped from
`cmd/tmux-cli/embedded/commands/tmux/supervisor/`, dir-namespaced like `worker/execute.md` →
`/tmux:worker:execute`), a marker-file handoff contract consumed by the existing
`tmux-supervisor-cycle.sh` Stop hook, and amendments to `supervisor.xml` (steps 0, 9b,
execution-rules). Goal-mode/daemon dispatch (`dispatchcmd.go`) is explicitly untouched.

---

## 1. Problem

A supervisor run accumulates context across waves. A restart-with-clean-context mechanism
exists, but **both** implementations key exclusively on *planned tasks* in a tasks.yaml:

| # | Mechanism | Actor | Trigger | Restart argument |
|---|-----------|-------|---------|------------------|
| 1 | `supervisor.xml` step 9b | the supervisor LLM itself, via two `windows-send` calls to its own window (`/clear`, then `/tmux:supervisor {tasks-path}`) queued while still mid-turn | "pending tasks remain in tasks-path AND cycle < max_cycles" | the same tasks-path |
| 2 | `tmux-supervisor-cycle.sh` (Claude Code **Stop hook**, installed by `WriteHookScripts`) | deterministic bash, fires when the supervisor's turn ends | unfinished `status: pending\|in_progress` entries in `.tmux-cli/tasks.yaml` (standalone `supervisor` window only; defers when `taskvisor-active`/`recurring-active`) | hardcoded `.tmux-cli/tasks.yaml` |

A next wave that exists **only as a plan document** (`plan-file-for-next-wave.md` — no
pending tasks.yaml entries) has *no* fresh-restart path today: the supervisor either replans
it in-context (§8a → §3, dragging the bloated context along) or stops.

Secondary problem: mechanism 1 is the fragile blind-queue dance — two inputs queued into the
supervisor's own input box mid-turn, relying on queued-input ordering surviving `/clear`. It
works, but it is the least reliable step of the flow executed at the moment of maximum
context exhaustion.

## 2. Goals / non-goals

**Goals**
1. A named, reusable handoff `/tmux:supervisor:fresh <plan-file>` that restarts the
   supervisor window onto a **lone plan .md** (or any `/tmux:supervisor`-acceptable
   argument) with a cleared context.
2. Deterministic delivery: the `/clear` + relaunch is performed by machinery *outside* the
   dying context, not by the dying context itself.
3. The classic supervisor invokes it at the wave boundary when the next wave is ready but
   exists only as a plan file.
4. Human-invocable: the user can run `/tmux:supervisor:fresh some-plan.md` in a supervisor
   window to force a clean restart onto that plan.
5. Cap integrity: `SELF_WAVE` and `cycle` counters survive the handoff — `/clear` must not
   become a cap-laundering loop.

**Non-goals**
- Goal-mode restarts: daemon-dispatched (`supervisor-{ns}`) sessions keep the existing
  step-9b dance and daemon retry machinery; the Stop hook already defers to the daemon via
  the `taskvisor-active` guard and this design preserves that ownership.
- No new idle-polling helper: the Stop hook already provides exact turn-end timing; a
  detached `waitForIdlePrompt`-style poller (the rejected alternative) would duplicate that
  with extra TUI coupling.
- No change to what `/tmux:supervisor` accepts — a plan .md is already valid free-form
  input (the supervisor self-specs / fans out from it).

## 3. Decision — command **and** automatic routine, each doing what it's good at

The split: the **LLM side only writes a file** (the most reliable action a
context-exhausted agent can perform) and ends its turn; the **Stop hook** — deterministic,
already installed in every tmux-cli session, already performing this exact send sequence —
delivers `/clear` + relaunch.

Rejected alternatives:
- **Pure embedded command** (self-directed `windows-send` dance, 9b-style): keeps the blind
  queue-ordering gamble and asks a bloated context to execute a multi-step choreography
  correctly. Works for 9b today, but is the wrong thing to *extend*.
- **Detached Go helper with idle polling**: reinvents Stop-hook timing, imports the
  Claude-TUI-coupled idle probe (`e2e.go` `waitForIdlePrompt`) into a second consumer, and
  adds a new plumbing binary surface for no gain.
- **Daemon-side detection**: does not cover standalone interactive supervisors — the primary
  audience of this feature — and the daemon already owns goal-mode restarts.

## 4. The handoff contract — `.tmux-cli/fresh-handoff`

A one-shot YAML marker in the project's `.tmux-cli/` dir (same family as
`auto-execute-guard`, `cancel-cycle`, `audit-done`):

```yaml
plan: .tmux-cli/research/2026-07-21-14/next-wave-2.md   # required; path passed verbatim to /tmux:supervisor
self_wave: 1        # optional; SELF_WAVE value the fresh instance must resume
cycle: 3            # optional; cycle counter the fresh instance must resume
requested_by: supervisor   # "supervisor" (step 9b) | "user" (manual /tmux:supervisor:fresh)
created: 2026-07-21T12:34:56Z
```

Lifecycle:
- **Written** atomically (temp + rename) by `/tmux:supervisor:fresh` or by step 9b's
  plan-file branch, as the writer's *last* action before ending the turn.
- **Consumed** (`rm`) by the Stop hook *before* it sends anything — one-shot by
  construction; a crashed send cannot re-fire on the next Stop.
- **Staleness**: a fresh supervisor boot (step 0, clean slate) deletes any leftover marker
  and logs it — a marker that survives to the next boot means the hook never fired
  (missing/failed hook) and must not ambush a later, unrelated Stop.

## 5. Components

### 5a. Embedded command `/tmux:supervisor:fresh` — `embedded/commands/tmux/supervisor/fresh.md` + `fresh.xml`

Follows the existing md (entry) + xml (procedure) convention. Procedure:

1. **Validate** `$ARGUMENTS`: exactly one path; the file exists and is non-empty. A missing
   file is a loud usage error — never write a marker pointing at nothing.
2. **Guards**: refuse when `.tmux-cli/taskvisor-active` or `.tmux-cli/recurring-active`
   exists (the daemon/recurring driver is the sole dispatcher there — use the goal flow
   instead). Refuse outside a supervisor window (the Stop hook only acts on the
   `supervisor` window; writing the marker elsewhere would strand it).
3. **Carryover**: if invoked from a running supervisor context that knows its counters
   (step 9b handoff), embed `self_wave`/`cycle` in the marker. A manual user invocation
   omits them (a human restart is a deliberate new invocation; caps reset).
4. **Write** the marker atomically; print one line:
   `FRESH HANDOFF armed → <plan-file> (self_wave=N, cycle=M)`.
5. **STOP** — end the turn. The Stop hook does the rest.

### 5b. Stop-hook extension — `embedded/tmux-supervisor-cycle.sh`

A new **marker branch**, placed after the existing guards (`taskvisor-active`,
`recurring-active`, goals-all-terminal, open `execute-*` workers, `auto-execute-guard`) and
*before* the tasks.yaml logic:

```
if .tmux-cli/fresh-handoff exists:
    parse plan path (+ optional self_wave/cycle)
    if plan file missing → log to notifications.log, rm marker, exit 0   # never restart onto nothing
    enforce max_cycles from setting.yaml against the marker's cycle value (same rule as tasks.yaml branch)
    rm marker                                   # consume BEFORE sending — one-shot
    cancellable countdown (reuse cycle_delay + cancel-cycle mechanism; cancel re-writes nothing — marker stays consumed, restart is simply skipped and logged)
    tmux send-keys "/clear" Enter; sleep 2; tmux send-keys "/tmux:supervisor <plan-path>" Enter
    exit 0
# else: existing tasks.yaml branch, unchanged
```

The marker branch takes precedence: an armed handoff is an explicit instruction and must
not be shadowed by leftover unfinished tasks (the fresh instance decides what to do about
those from its plan file).

### 5c. `supervisor.xml` amendments

- **Step 9b — new plan-file branch.** After the existing "pending tasks remain" check, add:
  *when no pending tasks remain in tasks-path BUT the §8a/§9 replan produced a next wave
  that is executable (SELF_WAVE rules applied as today) and exists as a plan document*:
  1. write the next-wave plan file to `{research-root}/next-wave-<N>.md` with a YAML
     frontmatter block carrying `self_wave` (post-increment) and `cycle`;
  2. **STANDALONE mode**: follow the `/tmux:supervisor:fresh` procedure (write
     `.tmux-cli/fresh-handoff` pointing at it) and STOP;
  3. **GOAL_MODE**: keep the existing windows-send dance (the hook defers on
     `taskvisor-active`, so the marker path would dead-end there).
- **Step 0 — clean slate**: delete a stale `.tmux-cli/fresh-handoff` (log it).
- **Step 0/0c — counter adoption**: when `$ARGUMENTS` is a `.md` plan file whose
  frontmatter carries `self_wave`/`cycle`, adopt those as the starting counters instead of
  0 — this is the cap-integrity half of the contract.
- **execution-rules**: the `windows-send` self-restart rule text gains "(GOAL_MODE only;
  standalone cycle/wave restarts go through the fresh-handoff marker)".

## 6. Cap and counter semantics

- `SELF_WAVE`: a purely self-generated wave handed off via `:fresh` carries
  `self_wave` in the plan frontmatter + marker; the fresh instance resumes it, so the cap
  of 2 self-generated waves per *chain* holds across any number of `/clear`s. Waves
  traceable to the original `$ARGUMENTS` requirements carry `self_wave` unchanged and
  remain uncapped, exactly as today.
- `cycle` / `max_cycles`: the hook enforces `max_cycles` against the marker's `cycle` on
  the marker branch (the tasks.yaml branch already enforces it); the fresh instance adopts
  the counter so a subsequent tasks.yaml continuation counts from the right place.
- Manual user invocation resets both (no frontmatter, no marker fields) — a human restart
  is a new authorization, symmetrical with typing `/tmux:supervisor plan.md` by hand.

## 7. Failure modes

| Failure | Behavior |
|---|---|
| Hook not installed / never fires | Marker survives; next supervisor boot's step-0 cleanup removes + logs it. No silent ambush restarts. |
| Plan file deleted between arming and Stop | Hook logs + consumes marker, exits without sending. |
| Restart loop | Structurally bounded: marker is one-shot, each wave needs a *newly written* plan file, and SELF_WAVE carries across. A plan file that yields no work produces no next marker — chain ends. |
| Daemon concurrency | `taskvisor-active` guard: command refuses to arm, hook refuses to fire. Goal-mode keeps its existing path. |
| User wants to abort an armed restart | Existing `cancel-cycle` countdown file works on the marker branch too. |
| `/clear` ordering | Solved by construction — sends happen from the hook after the turn ended (`send /clear` → `sleep 2` → send command is the proven pattern already in production). |

## 8. Testing

- **Embed/content assertions** (pattern: `e2e_evaluator_xml_test.go`,
  `docs_hardening_test.go`): `supervisor/fresh.md`+`fresh.xml` exist in the embedded FS and
  install under the `tmux:supervisor:fresh` name; `fresh.xml` contains the guard strings
  (`taskvisor-active`, atomic write, STOP); `supervisor.xml` 9b contains the plan-file
  branch and step 0 the stale-marker cleanup; `tmux-supervisor-cycle.sh` contains the
  marker branch *before* the tasks.yaml branch and consumes the marker before sending.
- **Marker parse/write**: if marker read/write helpers land in Go (optional — the hook can
  stay pure bash with `grep`/`sed` like its existing yaml reads), unit-test them; otherwise
  a bash-level test of the hook branch with a fake `tmux` shim on PATH.
- **Cross-surface contract test** (`supervisor_fresh_contract_test.go`): the three surfaces
  agree only by writing/grepping identical byte sequences, and no per-slice test can see
  the other side. One file spells every §4 token once and re-checks all three surfaces
  against it — marker path, the five field names, the command name, the armed-line prefix
  (literal `→`), consume-before-send, double `max_cycles` enforcement, the `self_wave`
  end-to-end linkage, and the `@window-uuid` window-identity rule.
- **E2E**: the `supervisor-fresh-handoff` scenario, §8.1.

### 8.1 E2E scenario — `supervisor-fresh-handoff`

Run with `/tmux:e2e-evaluator supervisor-fresh-handoff`. A **single-command-mode** scenario
per `e2e-evaluator-design.md` §8 (secondary): the end-state derives from this command's own
contract (§4/§5), not from a built app. It needs no Docker and no `goals.yaml` — it drives a
**standalone** supervisor only, so the daemon guards (`taskvisor-active`) stay clear.

**Fixture** — written into the disposable target dir by the scenario before DRIVE:

```
.tmux-cli/research/e2e-fresh/next-wave-1.md     # wave 1: frontmatter self_wave: 1, cycle: 1
.tmux-cli/setting.yaml                          # supervisor.max_cycles: 5, cycle_delay: 3
```

`next-wave-1.md` carries frontmatter `self_wave: 1` / `cycle: 1` and prose describing one
trivially-executable wave (e.g. "create `WAVE1.md` containing the word `wave-one`") plus an
explicit follow-up wave the supervisor must hand off rather than run in-context.

**DRIVE**: send `/tmux:supervisor .tmux-cli/research/e2e-fresh/next-wave-1.md` to the target
pane (two-step send per e2e-evaluator §4).

**Assertions** (all read from durable artifacts — never by polling for the marker file,
which is consumed before the countdown and is therefore inherently racy to observe):

| # | Claim | Evidence (deterministic) |
|---|---|---|
| a1 | The marker was **armed** | pipe-pane log matches `FRESH HANDOFF armed → .*next-wave-` |
| a2 | The marker was **consumed** | `.tmux-cli/logs/notifications.log` matches `Supervisor fresh handoff restart -> ` |
| a3 | The marker is **gone** | `test ! -e .tmux-cli/fresh-handoff` after the relaunch lands |
| b | `/clear` **then** relaunch | pipe-pane log contains `/clear` at an index strictly before `/tmux:supervisor .*next-wave-` |
| c | Counters were **adopted** | pipe-pane log matches `Adopted handoff counters from .*: SELF_WAVE=1, cycle=1` (step 0c's mandated log line) |
| d | Cap held (no laundering) | across the whole run, `FRESH HANDOFF armed` occurs **at most twice** — SELF_WAVE reaches the cap of 2 per chain and the third wave is listed as RECOMMENDED NEXT WORK instead of armed |
| e | Wave 1 actually ran | `WAVE1.md` exists and contains `wave-one` (a handoff that restarts but does no work is a false pass) |

Assertion **d** is the one that matters most: it is the only check that distinguishes a
working cap from a `/clear` loop, and it fails loudly if step 0c's adoption regresses (a
laundering chain arms a marker every wave, unbounded, until `max_cycles` stops it at 5).

**Timing** (per §9 of the e2e design; this scenario is far cheaper than the full-flow one):

| Phase | Target (warn over) | Hard ceiling |
|---|---|---|
| Per wave (arm → fresh instance ready) | 90 s | 4 min |
| Whole scenario (2 waves + cap stop) | 6 min | 15 min |

**Failure triage**: a missing `Adopted handoff counters` line with everything else green is
a *tooling defect* (step 0c regressed) → file a task-report. A missing relaunch with the
marker still on disk means the **Stop hook never fired** — check hook installation first;
that is an environment fault, not a command defect, and is the §7 row this scenario exercises.

**Not covered** (deliberate): GOAL_MODE handoff (the daemon owns dispatch there and the hook
defers by design, §5c) and the manual `/tmux:supervisor:fresh` guard refusals (covered by the
content assertions above — they are refusals, so they produce no observable restart to drive).

## 9. Files touched

- `cmd/tmux-cli/embedded/commands/tmux/supervisor/fresh.md` (new)
- `cmd/tmux-cli/embedded/commands/tmux/supervisor/fresh.xml` (new)
- `cmd/tmux-cli/embedded/commands/tmux/supervisor.xml` (steps 0, 9b, execution-rules)
- `cmd/tmux-cli/embedded/tmux-supervisor-cycle.sh` (marker branch)
- tests per §8 — `supervisor_fresh_cmd_test.go`, `supervisor_fresh_hook_test.go`,
  `supervisor_fresh_xml_test.go` (per-slice) + `supervisor_fresh_contract_test.go`
  (cross-surface drift gate)
- `docs/architecture/e2e-evaluator-design.md` §8 — registers the `supervisor-fresh-handoff`
  scenario in the single-command catalogue

Rollout is inherent: `WriteHookScripts`/command installation refresh on session start, so
`tmux-cli self-update` + session restart picks everything up.

## 10. Open questions

1. Extend the hook's window match from exactly `supervisor` to `supervisor-*` later, so a
   goal-mode fresh handoff becomes possible once daemon ownership is reconciled? (Out of
   scope now; the guard structure already makes it a one-line change plus a daemon design
   discussion.)
2. Should step 9b's *tasks-path* continuation (standalone) also migrate from the
   windows-send dance to the marker, unifying all standalone restarts through the hook?
   Recommended as a fast-follow once the marker branch is proven — it deletes the last
   blind-queue dance outside GOAL_MODE.
