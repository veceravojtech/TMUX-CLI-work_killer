# Design: /tmux:feature skips the daemon /tmux:plan for atomic goals by INHERITING the plan audit

Status: Proposed (design only — not implemented)
Date: 2026-07-07
Scope: the `/tmux:feature` command flow (embedded shards) + the taskvisor dispatch contract. No daemon code change.

## 1. Problem

`/tmux:feature` Stages 0–3 already ARE planning: Stage 1 maps the blast radius, Stage 2
records the architecture ADR, Stage 3/3b emit a full implementation-ready spec + Test Plan.
Stage 4 then emits a goal, and the taskvisor daemon dispatches **`/tmux:plan` again** per goal
(`resolveDispatchKind` → `DispatchPlan`, `dispatchcmd.go:190-214`). For an **atomic single-goal**
feature the daemon's `/tmux:plan` is redundant overhead: it re-runs spec derivation (cache-hits
the Stage-4 pre-seed but still spends the validate/coverage/sanity/mull cycle — ~6+ min observed).

The blunt fix — mechanism **B**, pre-seed a `status: ready` goal-root `tasks.yaml` so the
plan-once guard downgrades `Plan → SpecRepair → /tmux:supervisor` (`dispatchcmd.go:237-248`,
`tasksYamlExists` via `dispatch.go:426` / `GoalTasksFilePath`) — skips `/tmux:plan` but also
**drops the quality gates `/tmux:plan` runs**: `spec-validate` (S0–S10), the manual S5/S6/S9
checks, and the **blind audit** (`plan.xml` step 11a).

## 2. Goal

Skip the daemon's `/tmux:plan` for atomic single-goal feature emissions **without losing any
plan-quality gate** — by running the SAME gates INSIDE `/tmux:feature` before emitting the goal
with a `ready` goal-root `tasks.yaml`. "We already planned; inherit the audit and skip the
re-plan." Multi-goal features keep the full `/tmux:plan` unchanged.

## 3. What /tmux:plan provides that must be inherited

| Gate | Source | Inherit as |
|------|--------|-----------|
| `spec-validate` S0–S4, S7, S8, S9, S10 | `spec-validate` MCP tool (`internal/tasks/spec_validate.go`) | Call the MCP tool on the feature spec |
| Manual S5 (deps), S6 (I/O matrix), S9 (CURRENT/CHANGE/PRESERVE) | `plan.xml` step 6 judgment checks | Orchestrator self-checks (same defs) |
| S10 must-rule satisfaction | `plan.xml` step 6 + `rules match` | Cross-check matched `must` rules vs spec `## Code Rules` |
| **Blind audit** | `plan.xml` step 11a (forced under SELF_SPEC; `lane=solo` slims, never skips) | Independent read-only audit sub-agent |
| Coverage-pass | `plan.xml` step 8b | Re-map acceptance units → spec sections |

## 4. Design — a new gated sub-stage "Stage 3c: Plan-parity audit"

Runs autonomously AFTER Stage 3b (Test Plan) and BEFORE Stage 4 emission. Composes `plan.xml`
steps 6 + 8b + 11a **by reference** (never re-implements them).

### 4.1 Applicability gate (decides skip-plan vs keep-plan)
Compute `ATOMIC_EMISSION` = the Stage-4 decomposition will emit **exactly one** goal for a
single-file / tightly-single-area footprint (the same discriminator Stage 4 already uses for the
single-goal recorded-red-evidence form + solo-lane G1/G3). 
- `ATOMIC_EMISSION == true` → run Stage 3c; on pass, emit via **mechanism B** (skip plan).
- `ATOMIC_EMISSION == false` (multi-goal DAG) → **skip Stage 3c entirely**, emit via mechanism A
  (current: `research/tasks.yaml` `status: planning`), let the daemon run `/tmux:plan` per goal
  (its cross-goal coverage-pass + dependency-edge derivation is still needed).

### 4.2 spec-validate gate (inherit plan step 6, tool half)
Call the `spec-validate` MCP tool on the pre-seed spec fragment
(`.tmux-cli/goals/{id}/research/execute-1-<slug>.md`, the one Stage 4.3b writes). Require
`valid == true`. On findings, revise the spec fragment and re-run (cap 3 rounds; record each in
the fragment's `## Spec Change Log`), mirroring the SELF_SPEC self-revision cap.

### 4.3 Manual S5/S6/S9/S10 gate (inherit plan step 6, judgment half)
Same checks the SELF_SPEC path runs: S5 dependencies current, S6 I/O matrix covers edge cases,
S9 every MODIFIED Code Map entry carries CURRENT/CHANGE/PRESERVE, S10 every matched `must` rule
(from Stage-0/Stage-1 `rules match`) has a satisfied `## Code Rules` line. Bounded revision as 4.2.

### 4.4 Blind audit (inherit plan step 11a) — THE key parity element
Spawn ONE independent, read-only **blind-audit sub-agent** (a `general-purpose`/`Explore`-class
worker or an `investigator-` window) that is BLIND to the orchestrator's authoring reasoning: it
re-derives correctness/completeness/rule-coverage from the spec fragment + the live tree +
`{research-root}/code-rules.md` alone, and returns a verdict (`pass` | findings with severity).
- This is exactly the audit `plan.xml` step 11a **forces under SELF_SPEC** because author ==
  verifier. In `/tmux:feature` the orchestrator authored the spec, so this blind audit is the
  ONLY independent review — it is **mandatory** for the skip path, never optional.
- `lane=solo` slimming: if the goal is solo-lane, slim the audit to the daemon's-eye validate
  evaluation (mirror plan's SELF_SPEC + lane=solo behavior) — slimmed, not skipped.
- Record the verdict durably as `{research-root}/plan-audit-<n>.md` + `plan-approval.md` (the SAME
  artifact names plan writes), so the skip is auditable and a resumed run can read it.

### 4.5 Emission branch (Stage 4.3b modification)
- **All Stage-3c gates pass** → Stage 4.3b writes the **goal-root** `.tmux-cli/goals/{id}/tasks.yaml`
  with `status: ready` (mechanism B), tasks pointing at the audited spec fragment. Run the
  `tasks-validate` MCP tool on it (a malformed file would be auto-cleared by
  `clearUnusableTasksForReplan` and fall back to full plan — so validate up front). Result:
  `tasksYamlExists == true` → `resolveDispatchKind` downgrades `Plan → SpecRepair` →
  `/tmux:supervisor {id}` directly. **No `/tmux:plan`.**
- **Any gate fails after bounded revision** → FALL BACK to current behavior: write
  `research/tasks.yaml` `status: planning` (mechanism A). The daemon runs `/tmux:plan`, which
  re-audits. Safe degradation — an un-audit-passable spec never reaches the supervisor unaudited.

## 5. What changes (design targets — NOT edited here)

- **New shard** `feature/stage-3c-plan-audit.xml` — spec-validate + S5/S6/S9/S10 + blind audit,
  composing `plan.xml` steps 6/8b/11a by reference. Loaded by the spine between Stage 3 and 4,
  gated on `ATOMIC_EMISSION`.
- **`feature/stage-4-implementation.xml`** — add the A-vs-B pre-seed branch in 4.3b keyed on the
  Stage-3c verdict: pass → goal-root `ready` tasks.yaml (B); fail/multi-goal → `research` `planning`
  tasks.yaml (A). Add `tasks-validate` before hand-off on the B path.
- **`feature.xml`** (spine) — register the Stage 3c load seam + a one-line glossary term
  (PLAN-PARITY-AUDIT) and the ATOMIC_EMISSION gate note.
- **No daemon / Go change.** `resolveDispatchKind`'s plan-once downgrade already does the skip when
  a goal-root `tasks.yaml` exists — the design only feeds it a properly-audited one.

## 6. Invariants & safety

- **Never ship an unaudited spec to the supervisor.** The B (skip-plan) path is reachable ONLY
  after spec-validate + S5/S6/S9/S10 + blind audit all pass; any failure degrades to A (full plan).
- **Multi-goal features never skip plan** — plan's cross-goal coverage-pass + dependency-edge
  derivation (`plan.xml` step 3 / `EnforceFileOverlapDeps`) has no in-feature equivalent yet.
- **Blind audit must be genuinely independent** (separate sub-agent, blind to author reasoning),
  else it is not equivalent to plan step 11a and the parity claim is false.
- **Plan-once is preserved**: a `ready` goal-root `tasks.yaml` is exactly what the existing
  downgrade consumes; no new dispatch state, no new daemon mechanics.
- Reuses existing surfaces only: `spec-validate` + `tasks-validate` MCP tools, the S0–S10
  catalogue, plan's blind-audit sub-agent pattern, plan's audit artifact names.

## 7. Tradeoffs

- **Cost moves, net ~wash, latency drops.** The audit work moves from the daemon's `/tmux:plan`
  phase into `/tmux:feature`, but removes the `/tmux:plan` window spawn + the redundant spec
  re-derivation round + the observed mull latency. One read-only blind-audit sub-agent replaces a
  full plan run (spec workers + audit + coverage) for atomic goals.
- **Complexity**: one new shard + a branch in 4.3b. Bounded — no daemon change, safe A-fallback.
- **Only atomic goals benefit**; multi-goal DAGs are unchanged (correct — they need full plan).

## 8. Open questions (to resolve at implementation)

1. Blind-audit worker type: reuse the `investigator-`/read-only spawn (Stage-1 pattern) or a
   dedicated `code-review`-class agent? (Leaning: the same read-only investigator spawn Stage 1
   uses, prompted with plan step 11a's blind-audit charter.)
2. Where to draw ATOMIC_EMISSION exactly — reuse Stage 4's single-file/single-unit discriminator
   verbatim, or a slightly looser "single goal, any file count" (our `--flag` case is single-goal
   but 5 files)? Looser is more useful; single-file is safer. Recommend: single-GOAL (not
   single-file), since the blind audit covers the multi-file correctness risk.
3. Revision cap coordination: 3 self-revision rounds (spec-validate/S5/S6/S9) + N blind-audit
   re-audit rounds — cap total to match plan's step 11b audit-replan budget to avoid unbounded loops.
