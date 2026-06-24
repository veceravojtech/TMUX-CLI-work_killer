# Plan — director two-tier follow-ups

**Status:** Ready for planning/dispatch.
**Created:** 2026-06-24.
**Parent design:** `docs/architecture/director-two-tier-design.md` (core implemented + shipped; this file tracks the deferred work).
**Context:** The two-tier roadmap → just-in-time elaboration flow landed in three increments (Increment 1 foundation, Increment 2 daemon engine, Increment 3 XML commands). These four items were deliberately deferred — they are new scope, and #1 is load-bearing for fidelity parity with the old all-at-once generator.

Priority order: **F1 (load-bearing) → F2 → F3 → F4 (optional).**

---

## F1 — Strengthen `/tmux:elaborate` to author convention-application logic *(load-bearing)*

**Problem.** The roadmap-only rewrite moved per-goal concrete authoring out of `task-plan-generate`'s shards into `/tmux:elaborate`. But a large body of per-phase convention APPLICATION logic that lived in the shards is now only relocated *by reference* — `elaborate.xml` step 7 delegates to "the matching phase shard," which no longer carries it. The convention DEFINITIONS survive (`embedded/rules/`, `docs/task-plan-spec.md`), but elaboration does not yet actually source and apply them at dispatch. Until it does, a roadmap-driven run produces lower-fidelity goals than the old generator.

**What's affected (the relocated applications).** ensure-test-stack ordering (ENSURE-STACK-CONV / HTTP-WAIT-CONV), JWT keygen, E2E artifacts (E2E-ARTIFACT-CONV / E2E-ENV-CONV / E2E-SIDEFX-CONV / E2E-DATA-ISOLATION-CONV / E2E-AUTH-STATE-CONV), HC-04 health probe, Playwright storageState usage, per-BC fan-out application, and the per-phase acceptance-criteria catalogs (EH-01..EH-08, AU-01..AU-10, EV-01..EV-13, FG-01..FG-18, PD-*, SA-*).

**Scope.**
- `cmd/tmux-cli/embedded/commands/tmux/elaborate.xml` — step 7 (and the validate/acceptance authoring steps) must explicitly resolve the phase→criteria mapping from `docs/task-plan-spec.md` (or a relocated catalog) and apply the matching convention pack for the goal's `phase`, instead of pointing at a now-empty shard.
- Possibly a new/relocated catalog asset under `cmd/tmux-cli/embedded/` if `docs/task-plan-spec.md` is not reachable from a worker's runtime context — verify the elaborator can actually read it.

**Approach.** Give `/tmux:elaborate` an explicit "resolve conventions for this phase" sub-step: run `tmux-cli rules resolve --kind=convention --lang --framework` (already its pattern), map the goal's `phase` to the relevant criteria catalog, and author validate/acceptance from BOTH the live-tree read AND the resolved convention applications. Keep `[CODE-RULE-INJECTION]` as-is (already by-reference to the spine).

**Acceptance / verification.**
- A roadmap goal whose phase is `auth` (or `error_handling`, `event`, `final`) elaborates to a validate/acceptance set that includes the phase's convention applications (e.g. ensure-stack ordering for a stack-touching goal; the EH-/AU- criteria for the phase), demonstrably matching the pre-rewrite generator output for an equivalent goal.
- Add prompt-contract tests (mirror the migrated `internal/setup` style) asserting `elaborate.xml` references the criteria catalog + resolves conventions per phase.
- `go test ./...` green.

**Risk.** Medium-high — this is the fidelity-parity keystone; under-authoring here silently regresses generated-goal quality. Verify against a concrete before/after goal.

---

## F2 — Validation-goal (`validates:`) elaboration semantics

**Problem.** A roadmap validation goal carries `validates:` + a single coarse `deliverable_area`, but it validates a whole phase-cluster spanning multiple areas. The old step-3.30 "move heavy checks off impl goals, keep a light presence validate" logic dissolves under roadmap-only (impl goals have no validate at roadmap time). `elaborate.xml` does not yet branch on `validates:`.

**Scope.** `cmd/tmux-cli/embedded/commands/tmux/elaborate.xml` — add an explicit branch: a `validates:`-bearing goal authors a HEAVY validation stack across the cluster it validates; an ordinary impl goal authors a LIGHT presence validate over its own `deliverable_area`.

**Acceptance.** Elaborating a `validates:` goal yields the cluster-wide heavy gate; elaborating an impl goal yields a light presence validate. Prompt-contract test asserting the branch exists.

**Risk.** Medium — depends on F1's authoring machinery; do after F1.

---

## F3 — depinfer coarse-`deliverable_area` over-serialization tuning *(Go)*

**Problem.** Same-BC action goals share one coarse `deliverable_area` (e.g. `contexts/{BC}/app/src/Http/Controller/`). depinfer keys produce/consume overlap on `deliverable_area`, so it may serialize genuinely-independent same-BC actions that the old per-file scope kept parallel.

**Scope.** `internal/taskvisor/depinfer.go` — refine the roadmap-tier overlap test so a shared coarse area does not force a dependency edge between sibling action goals with no real producer/consumer relationship (e.g. require a directional produce/consume signal, not mere area equality).

**Acceptance.** Two independent same-BC roadmap action goals remain siblings (no injected `depends_on`); a genuine producer→consumer pair still gets its edge. Unit tests in `depinfer_test.go`.

**Risk.** Low-medium — pure Go, well-tested seam; watch for regressing the legitimate-edge case.

---

## F4 — Bootstrap goal conversion *(optional)*

**Problem / decision.** goal-001 (Gate-0 env check) and goal-002 (scaffold) are deliberately left fully-specced/born-pending — they run before any code exists, so there is no predecessor output for Tier-2 elaboration to read against (design §10 tolerates fully-specced born-pending goals). A generated plan is therefore a mix of 2 pending bootstrap goals + N roadmap goals.

**Scope (only if wanted).** `cmd/tmux-cli/embedded/commands/tmux/task-plan-generate/step-1-gate0.xml` + `step-2-scaffold.xml` — convert to roadmap-skeleton emission like the other shards. These carry the most complex env/precondition/fan-out logic, so this is the riskiest bootstrap change and is separately buildable.

**Recommendation.** Leave as-is unless a uniform all-roadmap plan is explicitly desired; the mixed plan is correct and lower-risk.

**Risk.** High (bootstrap logic) for marginal benefit — do last, or not at all.

---

## Sequencing

F1 first (unblocks parity and F2). F2 after F1 (shares its authoring machinery). F3 is independent Go and can run in parallel with F1/F2. F4 is optional and last. F1+F2 are `/tmux:elaborate` XML; F3 is `internal/taskvisor` Go; F4 is `task-plan-generate` shards — all file-disjoint, so F1/F2, F3 can be dispatched as parallel workers.
