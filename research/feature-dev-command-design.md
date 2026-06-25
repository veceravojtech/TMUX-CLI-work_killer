# Design: `/tmux:feature` — brownfield feature-development orchestrator

## Purpose

One command that takes a single feature/task for an **existing** system and drives it
end-to-end: ingest context → recommend architecture (DDD + resolved project rules) →
write feature docs + choose a test strategy → implement → validate. It is the brownfield
analogue of the greenfield `project-discovery → task-plan-generate` pair, and a thin
**composer** over existing primitives — not a new execution engine.

## Non-goals

- Not greenfield project generation (`task-plan-generate` already scaffolds whole projects).
- Not a fifth parallel full-cycle worker (`/worker:full-dev-e2e`, `/worker:pipeline`,
  `/worker:spec-and-dev`, `bmad-dev-story` already exist). `/tmux:feature` earns its place
  only as the **DDD- + test-strategy-aware brownfield composer** over `plan → supervisor`.
- No project-specific hardcoding. Every project-shaped value (root namespace, layer names,
  source roots, test runner, fixture command, quality-gate commands) is **resolved from
  discovery/project manifests, or asked** — never literal. P2 is a motivating example, not
  the target.

## Relationship to existing primitives (reuse, don't rebuild)

| Stage | Reuses |
|-------|--------|
| Context ingestion | `tmux-cli rules resolve/match` (load DDD + project rule packs), read-only `/tmux:investigate` workers, existing `AGENTS.md`/project docs |
| Arch recommendation | resolved rule packs (`ddd-conventions.md`, code-rules), `_base/adr.md` template |
| Feature docs | `/tmux:plan` spec sections (Intent…Test Plan…Acceptance) |
| Test strategy | `templates/_base/test-strategy.md`, manifest `when:` capability signals (`has_database`, `has_frontend`, `min_auth_flows`, `run_target`) |
| Plan → goals | `/tmux:plan` → `tasks.yaml` → goal DAG |
| Impl + validation | taskvisor goals (phases, depends_on chains), `/tmux:supervisor` → `/tmux:execute`, validation cycle, solo/full lane gate |

## Stage machine

Mirrors `task-plan-generate.xml`: one main `feature.xml` that loads `feature/step-N.xml`
sub-steps. The interactive/autonomous boundary matches the platform's native shape —
**Stage 2 is the only interactive phase; Stages 3–5 are autonomous.**

### Stage 0 — Preflight & capability resolution
- Resolve working dir, `lang`/`framework`, and capability signals
  (`has_database`, `has_frontend`, `min_auth_flows`, `run_target`, `e2e_runner_available`)
  from discovery state + project manifests.
- `tmux-cli rules resolve` → the rule packs that bind this project (DDD conventions +
  code-rules). Resolve the project's **source roots, layer names, root namespace, and
  quality-gate commands** here (these feed every later stage; see portability rule above).

### Stage 1 — Brownfield context ingestion (read-only)
- Locate the feature's blast radius in the existing tree: the bounded context/module it
  touches, the aggregates/handlers/controllers/contracts already there, and the conventions
  the resolved packs impose.
- Spawn read-only `/tmux:investigate` workers to gather it; **never edits**.
- Output: a **context dossier** — existing-arch map + the rules that apply to the change.

### Stage 2 — Architecture recommendation (INTERACTIVE — the only one)
- Using the dossier + DDD/project rules, propose the feature's architecture: which
  layer(s); new aggregate vs extend existing; command/query split; cross-context contracts;
  domain events; persistence shape. Offer 1–3 options with trade-offs.
- Record the decision as an ADR (`_base/adr.md`). **Gate on user approval** before any
  autonomous work begins.

### Stage 3 — Feature docs + test-strategy design (autonomous)
- **3a Docs:** emit a feature spec (Intent, Boundaries & Constraints, I/O & Edge-Case
  Matrix, Acceptance, Design Notes) — the `/tmux:plan` spec shape, scoped to one feature.
- **3b Test strategy (the genuinely new decision step):** map every acceptance criterion to
  a test layer + driver, resolved from capabilities — see decision table below.

### Stage 4 — Plan → phased goal DAG (autonomous)
- Convert the approved spec + test-plan into goals via `/tmux:plan` → `tasks.yaml`.
- **TDD ordering:** for BE-logic units the test-first goal precedes its implementation goal
  (red→green gating below). Each goal's `validate` uses the **resolved** project commands
  (e.g. static-analysis, unit, deptrac, e2e), never skeleton `src/` literals.

### Stage 5 — Implementation + validation (autonomous)
- Hand off to taskvisor/`/tmux:supervisor`: phased execution, per-goal validation cycle,
  lane gate, convergence circuit breaker. The command ends with the daemon executing (or a
  named, actionable reason why not), matching `/tmux:task-list`'s consume contract.

## Test-strategy decision (Stage 3b) — generic, capability-gated

Classify each acceptance unit, then pick a driver from resolved signals:

| Unit kind | Condition | Strategy |
|-----------|-----------|----------|
| BE logic (domain/application) | always | **TDD**: failing unit test authored first (Domain + Application), red→green; integration test for infra/persistence units |
| Full-stack / FE behavior | `has_frontend` AND `e2e_runner_available` (Playwright) | Playwright E2E; reuse auth storageState when `min_auth_flows ≥ 1` |
| Full-stack, no browser runner | `has_frontend` false OR no Playwright | API/HTTP-level E2E (or flag for manual) — never silently skip |
| Infra/adapter | `has_database` / external | Integration test against the resolved test stack (`ensure-test-stack` contract) |

All thresholds come from the manifest's existing capability signals — no project literals.

## TDD red→green gating (how the goal DAG encodes test-first)

- **Goal A (tests-first, `phase=fixtures` or a new `phase=tests_first`):** `validate` asserts
  the new test files exist and **fail meaningfully** (assertion failure, not a
  collection/parse error). Goal is "done" when the tests are authored and correctly red.
- **Goal B (implementation, `depends_on: [A]`):** `validate` asserts the **same tests now
  pass**, plus the resolved static gates. Goal is "done" when green.
- This expresses red-before-impl / green-after as an ordinary dependency chain — no new
  daemon mechanics required.

## Portability principles (carried into every task)

1. Resolve `{source roots, layer names, root namespace, test runner, fixture cmd, quality
   gates}` from discovery/manifests; **ask** when unresolved; greenfield skeleton values are
   defaults only.
2. Phrase rules/templates by **role**, not project-specific suffix; cite concrete names as
   examples.
3. Keep the interactive surface to Stage 2; everything downstream is autonomous and
   re-runnable.

## Task breakdown (filed to the backend)

1. **F1 — Orchestrator skeleton & stage machine** (spine): `feature.xml` + `feature.md` +
   sub-step loader; Stage 0 preflight/capability+rules resolution; interactive/autonomous
   boundary; composition over existing primitives.
2. **F2 — Stage 1 brownfield context-ingestion**: read-only dossier via investigate workers
   + resolved rules; blast-radius location in an existing tree.
3. **F3 — Stage 2 interactive architecture recommendation**: DDD/project-rule-driven options
   + ADR + approval gate (the single interactive phase).
4. **F4 — Stage 3a feature design docs**: per-feature spec sections, brownfield-scoped.
5. **F5 — Stage 3b test-strategy decision step**: capability-gated TDD-BE / Playwright-FE
   driver selection; acceptance→layer+driver mapping (the novel piece; pairs with the
   already-filed test-first-ordering gap).
6. **F6 — Stage 4/5 TDD-aware phased goal emission + supervised handoff**: red→green
   two-goal gating, phase ordering, validates from resolved project commands, taskvisor
   handoff.
7. **F7 — End-to-end command test + docs**: exercise the full arc on a sample brownfield
   repo (one BE feature with TDD, one FE feature with Playwright when available); document
   the command and its boundary vs the greenfield/worker commands.
