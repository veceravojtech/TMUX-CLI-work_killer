# Design — `/tmux:director` and the two-tier roadmap → just-in-time elaboration flow

**Status:** Implemented (core). Increments 1–3 landed, tested (`go test ./...` green), and `make install`-shipped. Convention-application coverage in `/tmux:elaborate` + the bootstrap-goal conversion are tracked follow-ups (below).
**Author:** drafted 2026-06-24 from the productivityTool goal-014 failure forensics.
**Scope decision:** Full two-tier (option 1A). New top-level command `/tmux:director`, run in window 0, drives the whole flow.

### Implementation increments
- **[DONE] Increment 1 — Go data + selection foundation** (inert until the scheduler flip; ships dark, no behavior change):
  - `GoalRoadmap = "roadmap"` status (`goals.go`).
  - `Goal.DeliverableArea` field + `mcp.tvGoal` mirror (`goals.go`, `tools_taskvisor.go`) — `TestGoalTvGoalYamlTagParity` green.
  - `GoalsFile.ElaborationCandidates()` selector (`goals.go`) + 5 unit tests (`elaboration_test.go`).
  - `dispatchElaboration` routing constant reserved (`goals.go`).
- **[DONE] Increment 2 — scheduler wiring + elaboration runtime phase** (`elaboration.go`, `statemachine.go`, `daemon.go`, `diagnostics.go`): `phaseElaborating` runtime state; `dispatchElaborate()` sends `/tmux:elaborate` and keeps the goal `GoalRoadmap` (completion = elaborator flips it to pending via `goal-edit`); `driveElaboratingGoals` handles completion + a 20-min fail-safe timeout→block; the elaboration slot-fill pass (budget-shared with impl dispatch) + teardown-condition update; `dispatchCandidate` status route. Plus the two integration seams: `goal-create`/`CreateGoal` roadmap-skeleton support (`GoalCreateRoadmap` MCP method, no validate required) and `taskvisor-start` admitting roadmap-only plans. Tests: `elaboration_test.go`, `elaboration_drive_test.go`, `createroadmap_test.go`, `tvtools_goalcreate_roadmap_test.go`.
- **[DONE] Increment 3 — XML commands**: `task-plan-generate.xml` rewritten to roadmap-only (§5; 21 files, −1658 lines, Tier-1 gates + enumeration preserved, bootstrap goal-001/002 deliberately left fully-specced); new `/tmux:elaborate` worker skill (§6); new `/tmux:director` conductor (§3). 23 prompt-contract tests migrated to the roadmap contract.

### Tracked follow-ups (not yet done)
- **Strengthen `/tmux:elaborate` to author convention-application logic** — the per-phase convention APPLICATIONS (ensure-stack ordering, JWT keygen, E2E artifacts, HC-04 probe, storageState, per-BC fan-out, the EH-/AU-/EV-/FG- criteria catalogs) lived in the generator shards and are now only relocated *by reference*; the convention DEFINITIONS survive in `embedded/rules/` + `docs/task-plan-spec.md`, but elaboration must actually source and apply them at dispatch to preserve the old fidelity.
- **Validation-goal (`validates:`) elaboration semantics** — a roadmap validation goal spans a phase-cluster; `/tmux:elaborate` needs a heavy-vs-light branch on `validates:`.
- **depinfer coarse-`deliverable_area` tuning** — same-BC action goals share an area and may over-serialize (Go).
- **Bootstrap conversion (optional)** — goal-001/002 stay fully-specced; convert only if a pre-code roadmap tier is wanted.

---

## 1. Problem — concrete fields are authored before the code they target exists

`task-plan-generate.xml` emits **all** goals at once (`goals.yaml with all generated goals`), each already carrying concrete `validate` / `acceptance` / `scope` / `goal.md`, authored purely from `docs/architecture/*` — **before a single line of the codebase exists.** Every concrete field is therefore a *prediction* about a future tree. Predictions rot. The productivityTool run is four rotted-prediction failures:

| Observed failure (productivityTool) | Mechanism | Root: authored-too-early field |
|---|---|---|
| goal-014 `failed_by: runner-missing` | `bin/ensure-test-stack.sh` runs `bin/console` on the **host** (no PHP); exit 127 → `haltRunnerMissing` → soft cascade parks 20 dependents | The `validate[]` and the generated stack script were written at plan time against a test-stack/runtime that did not yet exist |
| In-container boot fails: `config/services.yaml` imports `../contexts/identity/app/src/` "does not exist" | Kernel can't boot → every `bin/console` validate would fail even once the runner is fixed | The import path was a **guess**; the real tree (previo2 triad conversion, goal-037) diverged from the plan-time model |
| goals 009–011 marked `done` with **zero** migration files and no `doctrine_migrations.yaml` | `doctrine:migrations:migrate` is a no-op → empty schema → fixtures fail `42P01` | The up-front author cannot write a `validate` that **pins a deliverable it cannot see**, so "done" was vacuous (false-pass, defect-256/260 family) |
| Stranded worktree merge-backs (goal-013 salvaged) | Work lands on a branch the next goal's worktree never sees | Goals specced against an imagined end-state, not the tree actually present in the worktree at dispatch |

The common cause is **not** a daemon bug (the 20-goal cascade is a correct *soft* hold — `CascadeFailure(…, "env-config")` only hard-blocks on `fail`/`code-defect`). The common cause is: **a goal's concrete, enforceable fields are committed at the moment of least information.**

The fix is to move the authoring of concrete fields to the moment of **most** information: when the goal is about to run and its predecessors' real output is on disk.

---

## 2. The two-tier model

Split goal generation into two tiers separated in **time**, not just in content:

- **Tier 1 — Roadmap (up front).** Emit only the **skeleton**: `id`, `description`, `phase`, `depends_on`, and a coarse `deliverable_area`. No `validate`, no `acceptance`, no `scope`. The roadmap is the DAG and nothing concrete, so it has nothing that can rot. Everything the all-at-once view is genuinely good at survives here: phase ordering (G-04), the produce/consume `depends_on` inference (depinfer), and the over-serialization plan-audit all operate on the skeleton.

- **Tier 2 — Elaboration (just-in-time, at dispatch).** The instant a goal's deps go satisfied (`RunnableCandidates`), and **before** it is dispatched to an implementer, a dedicated elaboration cycle reads the **live codebase + the goal's worktree** and only then authors that goal's `validate` / `acceptance` / `scope` / `goal.md` — and runs the `[CODE-RULE-INJECTION]` step against the *real* deliverable paths. Concrete fields are now derived from truth.

A goal physically **cannot** be specced until its predecessors' real output exists. That is the single property every failure above lacked.

```
            ┌─────────────── Tier 1: Roadmap (once, up front) ───────────────┐
discovery → │ skeleton goals: id, description, phase, depends_on, deliv_area  │
docs/arch/* │ → DAG analysis: phase-order (G-04), depinfer, plan-audit       │
            └────────────────────────────────────────────────────────────────┘
                                      │  goals.yaml (status: roadmap)
                                      ▼
            ┌─────────────── Tier 2: per goal, at dispatch ──────────────────┐
 deps met → │ ELABORATE (reads live tree+worktree): author validate/         │
 (Runnable) │ acceptance/scope/goal.md + rules match → status: pending       │
            │                              │                                  │
            │                              ▼                                  │
            │   IMPLEMENT → INVESTIGATE → VALIDATE  (unchanged daemon flow)   │
            └────────────────────────────────────────────────────────────────┘
```

---

## 3. `/tmux:director` — the conductor

`/tmux:director` is a new top-level command (`.claude/commands/tmux/director.xml`) **run in window 0**, replacing the human-in-the-loop sequence of `discover → generate → supervisor`. It owns the whole flow as a single driver with a stable identity — the orchestrator the supervisor window *is*.

It composes existing skills rather than reimplementing them:

| Stage | Director action | Reuses |
|---|---|---|
| 0. Discover | Ensure `docs/architecture/*` exist & validate (Step 0 gate). If absent, run discovery. | `/tmux:task-plan-discover` |
| 1. Roadmap | Generate the **skeleton-only** goals.yaml (Tier 1). | `task-plan-generate` — now roadmap-only (§5) |
| 2. Approve | Present the roadmap (DAG + phases + plan-audit) for one approval gate. | plan-audit, `plan-approval.md` |
| 3. Run | Start taskvisor. The daemon now interleaves an **elaboration cycle** before each goal's first implementation cycle (§6). | taskvisor daemon |
| 4. Drive & self-heal | Monitor; when flow is blocked or something is missing, generate a **properly-wired follow-up roadmap node** to fix the flow (§8), never a blind retry. | escalation path + roadmap insert |

The director never re-authors concrete goal fields itself — that is always Tier-2 elaboration, so the "specced against truth" invariant holds even for goals the director inserts mid-run. Every corrective action it takes is a **new roadmap node**, keeping the whole flow inside the two-tier model.

---

## 4. State-machine changes (minimal, precedent-following)

Three small additions; each mirrors an existing mechanism so the change is additive and dormant for legacy all-at-once `goals.yaml`.

### 4.1 New goal status `roadmap`
`internal/taskvisor/goals.go`:
```go
GoalPending  = "pending"
GoalRunning  = "running"
GoalDone     = "done"
GoalFailed   = "failed"
GoalBlocked  = "blocked"
GoalRoadmap  = "roadmap"   // NEW: skeleton, not yet elaborated
```
A `roadmap` goal has `depends_on` + `phase` + `deliverable_area` but **no** `validate`/`acceptance`/`scope`. It is invisible to dispatch until elaborated.

### 4.2 Elaboration gate in `RunnableCandidates()` (`goals.go:521`)
Today `RunnableCandidates` admits `pending` goals with deps satisfied. Add a sibling selector:

```go
// ElaborationCandidates: roadmap goals whose deps are satisfied — ready to be
// specced against the now-real tree. Same dep/precondition gate as Runnable.
func (gf *GoalsFile) ElaborationCandidates() []*Goal {
    for g in gf.Goals where g.Status == GoalRoadmap
        && !g.BlockedByPrecondition
        && g.DependsOnSatisfied(gf.Goals): out = append(out, g)
    // same Priority-desc, file-order-stable sort as RunnableCandidates
}
```
The daemon scheduler elaborates a candidate (one at a time, or up to the concurrency cap) → on success the goal is rewritten to `pending` with full fields → it then flows through `RunnableCandidates` exactly as today. **`DependsOnSatisfied` is the single source of "predecessors are real now"** — reused verbatim, no new readiness logic.

### 4.3 New `NextDispatch` route `"elaboration"` (`statemachine.go:233`, `dispatchCandidate`)
`NextDispatch` already routes `"generation"`/`"implementer"`. Add `dispatchElaboration`:

```go
func (d *Daemon) dispatchCandidate(goal *Goal, goals *GoalsFile) error {
    if goal.Status == GoalRoadmap {           // NEW — highest precedence
        return d.dispatchElaboration(goal, goals)
    }
    if goal.NextDispatch == dispatchGeneration { ... }   // unchanged below
    ...
}
```
`dispatchElaboration` spawns an **elaborator worker** (§6) instead of an implementer. Reuses the existing window/worktree/signal plumbing — it is "a dispatch whose worker writes goal fields instead of code."

> Legacy guarantee: a `goals.yaml` with no `roadmap`-status goals never enters any new branch — byte-identical behavior to today.

---

## 5. Tier 1 — `task-plan-generate` is rewritten to roadmap-only

`task-plan-generate.xml` is **rewritten**, not extended: it no longer authors concrete fields at all. It becomes the roadmap generator — full stop. It runs **Step 0 (consistency gate)** and the **goal-enumeration** half of the existing shards (3.16a–3.30 keep their *which goals exist for this phase* logic), and **stops before** authoring concrete fields. The old all-at-once `validate`/`acceptance`/`scope`/`goal.md` emission is deleted from this skill and relocated wholesale into the Tier-2 elaborator (§6) — same shard bodies, invoked later against real files. Per goal it emits only:

```yaml
- id: goal-014
  description: 'Implement global error handling: RFC 7807 responses, status mapping'
  phase: error_handling
  depends_on: [goal-013]
  deliverable_area: projects/api/src/Http/ErrorHandling/   # coarse, for depinfer + elaboration seeding
  status: roadmap
```

What stays in Tier 1 (operates fine on skeletons):
- **Step 0** cross-file consistency gate (BC_LIST/AGGREGATE_MAP/ENDPOINT_MAP).
- **depinfer** produce/consume `depends_on` (now keyed on `deliverable_area`, not invented file paths).
- **G-04** phase-ordering validation.
- **plan-audit** over-serialization warning.

What moves to Tier 2 (everything that needs real files):
- `validate[]`, `acceptance[]`, `scope`, `goal.md` body.
- `[CODE-RULE-INJECTION]` (`tmux-cli rules match --files <real deliverables>`).
- investigator config (`investigation_config`).

The `topology-binding` rule (monorepo path discipline) is **enforced harder** here: at elaboration the real `contexts/<bc>/...` paths either exist or the dep is genuinely unmet — the flat-skeleton guess can no longer slip through, because elaboration greps the actual tree.

---

## 6. Tier 2 — The elaborator worker

`dispatchElaboration` spawns a read-mostly worker (new skill, e.g. `/tmux:elaborate`, sibling to `/tmux:plan`) in the goal's worktree. Contract:

**Inputs**
- The roadmap goal (`description`, `phase`, `deliverable_area`, `depends_on`).
- The **live worktree tree** (its predecessors are merged in — this is the whole point).
- The resolved convention + code-rule catalogues (`tmux-cli rules resolve`).
- The matching `task-plan-generate` phase shard (3.16a–3.30) for this goal's `phase` — reused as the authoring template, now reading real files.

**Procedure**
1. Read the actual tree under `deliverable_area` and the wiring it touches (`services.yaml`, `doctrine.yaml`, `ensure-test-stack.sh`, routes).
2. Author `validate[]` against what is *really runnable here* — e.g. emit `docker compose exec -T app bin/console …` only if the app service is real and up; emit bare commands so the daemon's `wrapcmd` classifier wraps them (no pre-wrapped `sh -c` that defeats `classify()`).
3. **Deliverable-pinning (kills false-pass):** every goal MUST include ≥1 `validate` that fails when the deliverable is absent — e.g. a fail-closed presence grep / file existence for the migration class, not only a behavioral test that no-ops on an empty schema. This is the structural fix for 009–011.
4. Run `[CODE-RULE-INJECTION]` with `--files` = the real deliverable paths.
5. Derive `scope` globs from the real footprint; author `goal.md`.
6. Write fields back via `goal-edit` MCP; set `status: pending`. On unsatisfiable preconditions (a needed predecessor deliverable genuinely missing), **escalate as a new roadmap node** rather than emitting a doomed validate.

**Failure handling**: an elaboration cycle that fails (can't read tree, contradictory rules) routes like a spec failure — bounded `spec_retries`, then `blocked`/owner=human. It never produces a half-specced goal.

---

## 7. How each failure class is structurally prevented

| Failure | Prevented because |
|---|---|
| `runner-missing` (host `bin/console`) | `validate` authored at dispatch inspects the *actual* runtime; container-exec form emitted only when the container is real. Bare-command rule re-asserted so `wrapcmd` wraps it. |
| services.yaml import to non-existent path | Elaboration **reads** `services.yaml` and the real `contexts/` tree; a path that isn't there is an unmet-dep escalation, not a committed guess. |
| 009–011 false-pass (no migrations) | Deliverable-pinning (§6.3): a presence-validate fails closed when the migration file is absent, so "done" cannot be vacuous. |
| stranded merge-backs | Elaboration runs **in the worktree after predecessors merge**, so it specs against the tree the implementer will actually see. |

---

## 8. Self-healing — blocked / missing → a new roadmap node

The director must never leave the flow wedged or paper over a gap with a blind retry. When a goal is **blocked** (precondition unmet, unsatisfiable dep at elaboration) or **something is missing** (no migration, no `doctrine_migrations.yaml`, an absent service, a port collision it cannot resolve in-place), the director **generates a properly-wired follow-up goal and inserts it into the roadmap**:

1. **Diagnose the gap** from the elaboration/validation failure (the elaborator returns a structured `unmet:` reason, e.g. `missing-deliverable: doctrine migrations`).
2. **Author a follow-up roadmap node** — skeleton only (`id`, `description`, `phase`, `depends_on`, `deliverable_area`), exactly like a Tier-1 node. Example: a `migrations` goal at `phase: infrastructure`, `deliverable_area: migrations/`.
3. **Re-wire the DAG**: the follow-up's `depends_on` = the real predecessors of the gap; the blocked goal gains a `depends_on` edge on the follow-up. depinfer + G-04 + plan-audit re-run on the amended roadmap so ordering stays valid.
4. The follow-up then flows through the **same** Tier-2 elaboration → implement → validate path. No special case — it is just another roadmap node, which is the point: **self-healing stays inside the roadmap model.**

This converts the failure classes from "park 20 dependents forever" into "insert the one missing node and continue." It is the generative version of the existing prerequisite-escalation path (`pendingPrereqEscalation`, `statemachine.go`), but it produces a *roadmap node* instead of routing a doomed retry.

## 9. Port-occupancy-aware stack provisioning

The productivityTool failure was mechanically a **host-port collision**: every goal worktree's `docker-compose.yaml` publishes `5432:5432` and `8080:80`, so parallel worktree stacks (`goal-014-db-1`, `goal-014-app-1`) grab the host ports and every other stack's `docker compose up` fails partway → app stuck `Created` → `runner-missing`. The mailpit service already solved this (task 253: stop publishing host ports, reach it over the compose network). The two-tier flow generalizes that fix:

- **Default to network-internal, no host publish.** Elaboration authors stack-bring-up that reaches services over the compose network (`db:5432`, `app` via `docker compose exec`), so **no host port is published at all** for the common case — the structural cure, matching the mailpit precedent. `ensure-test-stack`-style scripts are elaborated to `docker compose exec -T app bin/console …`, never host `bin/console`.
- **When a host port IS required** (e.g. a browser-reachable dev server for E2E), elaboration must **respect occupied ports and allocate a free one**: probe the host (the daemon owns a `tmux-cli ports alloc`-style helper that records claims), pick an unused port, and write it into that worktree's compose + the goal's `validate`/HTTP-wait. Each worktree stack gets a **distinct, non-colliding** host port; the allocation is recorded so sibling worktrees and re-elaborations don't re-collide.
- **Collision at runtime → self-heal (§8):** if a stack still fails to bind because a port is occupied, that is a "something missing/blocked" signal — the director re-allocates and, if the compose itself is wrong, inserts a follow-up roadmap node to fix the stack definition rather than retrying into the same bind error.

Because ports are allocated at **elaboration time against the live host**, not guessed at plan time, the collision cannot be baked into the roadmap.

## 10. Migration / back-compat

- Legacy `goals.yaml` (no `roadmap` status) → zero new branches taken in the daemon; identical behavior. Safe to ship dark.
- `task-plan-generate` no longer emits all-at-once goals **at all** (§5) — roadmap is its only output. The daemon still *tolerates* a hand-written fully-specced legacy `goals.yaml` (those goals are born `pending`, skip elaboration), so existing plans keep running.
- New persisted field `deliverable_area` is `omitempty` (additive, like `Priority`/`NextDispatch`). `GoalRoadmap` is a new status string; the dashboard/reporting need a render case for it.
- DUAL-STRUCT parity: `deliverable_area` must be mirrored in `mcp.tvGoal` (the `TestGoalTvGoalYamlTagParity` guard) like every other persisted goal field.

---

## 11. Open decisions (for review)

1. **Elaboration concurrency** — one goal elaborated at a time (simplest, fully ordered), or up to the dispatch concurrency cap? Parallel elaboration of independent same-phase goals is safe (each in its own worktree) and faster; recommend cap-bounded parallel.
2. **Approval granularity** — single approval of the roadmap (recommended), or a per-goal elaboration approval gate too? The latter restores up-front-style review at the cost of throughput.
3. **Elaborator identity** — new dedicated `/tmux:elaborate` skill (recommended, single responsibility) vs. extending `/tmux:plan` with an `--elaborate` mode.
4. **Re-elaboration on spec drift** — if a predecessor changes after a goal is elaborated but before it runs, do we re-elaborate? Cheapest: re-elaborate only on a validation `spec` bounce (reuses `specdrift`).

---

## 12. Non-goals

- Not changing the daemon's implement/investigate/validate cycle, retries, or circuit-breakers.
- Not changing the soft-cascade semantics (they are correct).
- Not a discovery-phase change — `docs/architecture/*` generation is unchanged.
- Not a hand-edit ban: a human may still author a fully-specced `goals.yaml` by hand; the daemon runs it. Only the *generator* is now roadmap-only.
