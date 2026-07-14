# /tmux:feature × discovery-built projects — full-compatibility plan

**Goal:** make `/tmux:feature` fully compatible with, and capable of working on, projects
built by the greenfield flow (`/tmux:project-discovery` → `/tmux:task-plan-generate` →
taskvisor), with **every resolved rule enforced** across all three feature tiers
(fast path, atomic single-goal w/ plan-parity skip, full goal DAG).

**Audit basis:** 27 canonical gaps from a 6-lens multi-agent audit over both flows,
the rules engine, and the daemon. Verification status per gap:
- `3/3` — confirmed by three independent adversarial verifiers (evidence / already-handled / repro lenses)
- `HV` — hand-verified against primary sources (file:line quotes checked in this tree)
- `IC` — found by the independent completeness critic (fresh-context agent over the plan +
  dossiers); its four highest-impact claims were spot-checked in source before adoption

All 35 gaps are verified (8 × `3/3`, 19 × `HV`, 8 × `IC`); none remain plausible-only.
The critic also caught **three constraint violations in this plan's own fixes** (wrong-direction
phase mapping, an LLM-side probe that belongs in Go, an unimplementable E4) — corrected in place
below — and seven missing acceptance obligations (folded into Phase 6).

Reference project shape throughout: php-symfony DDD monorepo (`contexts/*/src`,
`projects/*/src`, `packages/*/src`, `Bundle` infra layer), `RUN_TARGET=docker` with
compose services, full rule packs (php-symfony ddd + php-symfony-common + php + docker
+ optionally vue), `docs/architecture/*` corpus incl. `test-environment.md`,
`layout.md`, `adrs/`, and a goals.yaml ledger owned by the incremental generator.

---

## Gap inventory

| ID | Sev | St | One-liner |
|----|-----|----|-----------|
| G1 | blocker | 3/3 | Fast path runs docker-resident gates **bare on the host** (worker pre-DONE gate + F.6 suite); triage has no run-target term; abort `git reset --hard` destroys the finished commit |
| G2 | blocker | 3/3 | TDD red-phase gate emitted as `sh <script>` → wrapCommand classifies it host; inner runner never container-wrapped; tests-first goals can never validate on docker |
| G3 | blocker | 3/3 | Standalone feature run adopts the daemon's last-writer-wins `taskvisor-current-goal` marker → research-root/handoffs land in a foreign goal's dir |
| G4 | major | 3/3 | Stage 0 never resolves test roots / `{{e2e_runner}}` / `{{fixture_command}}` that Stages 3b/4 consume by name |
| G5 | major | 3/3 | Feature-emitted event goals get daemon-derived emission-check grepping nonexistent root `src/` (task-529 fix was generator-side only) |
| G6 | major | 3/3 | Stage-4 GATE_SET routes rules by `validate_kind: machine\|review` — actual kind set differs (`mixed` exists); mixed `must` rules fall through the routing hole |
| G7 | major | 3/3 | Rules matched once over the *existing-code* blast radius; the naming manifest's NEW target paths are never re-matched → rules scoped to new files silently missing |
| G8 | major | 3/3 | Fast path never applies conditional `mono-schema`/`db-validate` validators |
| G9 | major | HV | At MaxGoals>1 on docker, wrapped commands pin `-p <main project>` (wrapcmd.go:46-48, deliberate) while task-275's per-worktree `taskvisor-<id>` stack (dispatch.go:161-166) is brought up precisely so validates DON'T hit the main stack — the two comments contradict; validates exec into the main container mounting BASE code, the migrated worktree stack sits unused |
| G10 | major | HV | Pre-product-complete, the incremental generator binds `LAST_GOAL` to ANY highest-ID finished goal → adopts failed feature goals, authors "corrective" duplicates; feature failures count toward the incremental halt |
| G11 | major | HV | plan.xml:360 / supervisor.xml:271 probe pwd for `"/.tmux-cli/worktrees/"` but Go creates `.tmux-cli-worktrees` (worktree.go:130) — branch 1 never fires; marker fallback exists only from validator creation → cycle-1 workers can edit the BASE tree at MaxGoals>1 |
| G12 | major | HV | `Settings` struct has no `Feature` field (config.go:285-294) + unconditional re-save → user's `feature: {tests, auto_approve}` block silently deleted from setting.yaml |
| G13 | major | HV | Mechanism B unsynchronized with an ACTIVE daemon: goal is dispatchable the tick after goal-create, before 4.3b writes the goal-root `ready` tasks.yaml |
| G14 | major | HV | 4.3b mechanism-A pre-seed writes `status: planning` tasks.yaml into `research/` — plan step 0b/0c only stats goal-root tasks-path → INCREMENTAL flip structurally impossible |
| G15 | major | HV | VALIDATION-RESUME globs only the NEWEST `feature-goals.md` → any interleaved second feature breaks the documented Stage-5 resume and re-emits a duplicate DAG |
| G16 | major | HV | GATE_SET has no stack-preparation slot: feature E2E/HTTP goals omit the pack-mandated `bash bin/ensure-test-stack.sh` line (validate against unmigrated/unseeded DB) |
| G17 | major | HV | Explicit `investigation_config` REPLACES derived investigators (goalmd.go:61) — the shard's "slots in ALONGSIDE" is false; passing convention-audit suppresses the test/static investigators |
| G18 | major | HV | Discovery's `Playwright: Not installed — needs Gate 0 setup` is never updated post-scaffold; `parseHasFrontend` checks negatives first → `has_frontend=no` forever on a vue project |
| G19 | minor | HV | Stage 4 never passes `scope` to goal-create; acceptance-mined derivation downgrades to UNKNOWN → promised parallel siblings serialize |
| G20 | minor | HV | Stage 2 scans/writes `docs/architecture/adr-*.md`; discovery's corpus lives in `docs/architecture/adrs/adr-00N-*.md` → numbering restarts at 1, split ADR home |
| G21 | minor | HV | Quality gates resolved from packs are self-described "illustrative" alternatives (`composer mono:phpstan` / `make stan`) — no deterministic entrypoint source, no baseline verification |
| G22 | minor | HV | `drivePlanNext` kills the plan-next window on ANY goals.yaml growth (plannext.go:278) — a concurrent feature goal-create aborts the generator mid-authoring |
| G23 | minor | HV | MaxWorkers counts by bare `strings.HasPrefix` over ALL windows (tools.go:768) — daemon's namespaced workers exhaust the feature-sup's spawn budget |
| G24 | minor | HV | Stage 4.2 prescribes PHASE_ORDER tokens (`event_listener`, `error_handling`, `final_gate`, `middleware`…) that goal-create's `allowedPhases` enum rejects mid-DAG |
| G25 | minor | HV | Stage 5.2's optional direct cross-goal `/tmux:investigate` invocation can't work outside the daemon — `GoalValidationDone` requires caller `TMUX_WINDOW_UUID` == the daemon-created validator window's UUID (tools_taskvisor.go:783-790) |
| G26 | minor | HV | 3b.3 decision table has no row for an FE-class unit when `has_frontend=false` (twig / stale-vue) — falls through both E2E rules |
| G27 | minor | HV | Feature goals never carry `[service]`/`[env]` preconditions — a down external DB burns code-retries instead of the generator-parity `BlockedByPrecondition` park |
| G28 | blocker | IC | Fast-path abort `git reset --hard <BASE>` on the shared base branch destroys concurrent daemon merge-backs landed in `BASE..HEAD` (worktree.go:653,1088); F.5's confinement gate also misreads those interleaved commits as scope escapes → false abort |
| G29 | major | IC | HTTP/E2E probes can't reach the per-worktree stack at MaxGoals>1: `portStripOverride` empties published ports (composestack.go:173-196), `curl` is classHost (never wrapped/re-pinned), and feature goals never route through elaborate's port-occupancy step (elaborate.xml:130) — probes hit the MAIN stack's port, validating base code |
| G30 | major | IC | `playwrightApplicable` (execruntime.go:196) is an independent parser with the same "not installed" trap as G18 — stale line ⇒ `NodeSvc=""` ⇒ `npx playwright` left bare on host (wrapcmd.go:33-36) ⇒ exit-127; B4's rules.go fix and scaffold update don't reach already-built projects |
| G31 | major | IC | Crash mid-Stage-4 emission: `feature-goals.md` is written only at 4.4 after ALL goal-creates — a partial emission leaves live (mechanism-B: dispatchable) goals with no resume key → re-run re-emits the whole DAG as duplicates; mid-fast-path crash after the worker's commit has no resume/abort path at all |
| G32 | major | IC | Stage 5 has no branch for blocked goals (`needs-merge` manual runbook, `BlockedByPrecondition` park — both non-terminal) → permanent VALIDATION-PENDING with no actionable remedy; C6 makes parks MORE likely |
| G33 | major | IC | `migrates: true` (run-ALONE shared-schema exclusion, goals.go:213 / scope_gate.go) is unreachable from feature emission — GoalCreate exposes no such param and Stage 4 never derives it; a feature adding a migration co-schedules against the shared DB |
| G34 | minor | IC | Only ENSURE-STACK-CONV gets a GATE_SET slot — HTTP-WAIT-CONV, HTTP-CONV (resolved port), NODE-TOOL-CONV, E2E-ENV/SIDEFX/DATA-ISOLATION/AUTH-STATE-CONV, and CMD-CONV's monorepo per-component test-scoping clause have no emission rule |
| G35 | minor | IC | GM-05 (credentials by test-environment.md reference, never inlined) has no feature-shard counterpart — auth/E2E specs and goal artifacts may inline seeded test credentials (also an org data-privacy exposure) |

---

## Design keystone: one runtime-execution primitive (fixes G1, G2, and de-risks G8/G16)

Most blockers share one root cause: **commands executed outside the daemon have no
wrapping actor**. The daemon wraps at goal.md-authoring time (`wrapcmd.go`); the fast
path, the generated red-phase/no-test-artifact gates, and emission-time baseline
verification all execute commands directly and inherit "bare, host-style" text that
only works when the toolchain lives on the host.

**New Go CLI helper (respects the determinism boundary — Go decides, XML consumes):**

```
tmux-cli exec-runtime [--json] -- <cmd...>
```

- Resolves `ExecRuntime` exactly as the daemon does (`ResolveExecRuntime` over
  `docs/architecture/test-environment.md`) from the CURRENT working directory
  (worktree-aware via `NormalizeProjectDir`).
- `RUN_TARGET=local` → exec the command verbatim. `docker` → route by the same
  first-token family classification as `wrapCommand` (PHP toolchain → app service,
  Node → node service, host/file commands → verbatim), `docker compose -p <project>
  exec -T <svc> sh -c '<cmd>'`.
- Exit code passthrough; `--json` mode prints `{wrapped_cmd, runtime, service}` for
  logging without executing.
- Implementation: thin wrapper over the existing `internal/taskvisor` internals —
  no new mechanics, one entry point, unit-tested against both runtimes.

Everything below that says "via exec-runtime" uses this helper.

---

## Workstream A — Runtime correctness (the docker blockers)

### A1. Fast path on docker (G1) — `feature.xml` triage 0.5 + `feature/stage-fast-path.xml`
1. **Triage guard:** add a fourth verdict term: `RUNTIME_EXECUTABLE` — TRUE when
   `RUN_TARGET=local`, or when `RUN_TARGET=docker` AND `tmux-cli exec-runtime --json`
   resolves the exec environment successfully. (Keeps the fast tier ALIVE on docker
   projects instead of guarding it off.) Fail-safe unchanged: any resolution error →
   full pipeline.
2. **F.2 worker contract:** the context file's `## Validation Rules` section instructs
   the worker to run each gate via `tmux-cli exec-runtime -- <gate>`. Delete the false
   "the daemon/exec-env wraps" comment — name the actual wrapping actor.
3. **F.6 suite gate:** feature-sup runs the full suite via `exec-runtime` too. When
   `bin/ensure-test-stack.sh` exists (database pack projects), run it FIRST as a
   separate step (G16 parity for the fast tier).
4. **F.8 abort:** before `git reset --hard <BASE>`, preserve the abandoned commit on a
   branch `fastpath-abandoned/<slug>` — a control action, recoverable work instead of
   destroyed work. The full-pipeline restart is unchanged.
5. **F.4 unchanged** (`tmux-cli rules check` is the host binary — correct as is).

### A2. Red-phase gate runnable on docker (G2) — `feature/stage-4-implementation.xml`
1. Generated red-phase / red-evidence / no-test-artifact gate scripts invoke the
   resolved runner **through `tmux-cli exec-runtime`** inside the script body; the
   outer `sh <gate>` stays a host command (correct: the script itself is host/file
   work; only the runner hop enters the container).
2. **False-pass fix:** gates distinguish exit 126/127 (toolchain missing → gate ERROR,
   never "meaningful red") from a genuine assertion-failure exit. Emission-time
   GREEN-path baseline asserts specifically "runner ran AND no meaningful red", not
   just "non-zero".
3. Keep the PREFER-STRUCTURED rule (JUnit XML/exit-code over banner-grep) — now
   actually exercisable because the runner is reachable at emission time via
   exec-runtime.

### A3. Worktree pwd literal (G11) — `plan.xml:360`, `supervisor.xml:263,271`
Replace the `"/.tmux-cli/worktrees/"` literal with `"/.tmux-cli-worktrees/"` (matching
`worktreesDirName`, worktree.go:130) in both probes and the E1-1a comment. One-line XML
fixes; add a Go test asserting the literal in the embedded XML matches the constant
(cheap drift guard, mirrors existing embedded-asset tests).

### A4. Per-worktree compose project (G9 — confirmed design contradiction)
`wrapCommand`'s `-p <main>` pin (wrapcmd.go:46-48, "targets the MAIN running stack")
directly contradicts task-275's `bringUpWorktreeStack` (dispatch.go:161-166, "NOT the
operator's MAIN stack"): at MaxGoals>1 on docker, validates exec into the main
container (mounting BASE code, main DB) while the freshly migrated `taskvisor-<id>`
stack sits unused. Fix: resolve the compose-project pin from the **execution** cwd —
in a worktree whose `taskvisor-<goalID>` stack is up, pin to it; otherwise main.
Implement inside `exec-runtime` (keystone) and at dispatch-time wrapping (dispatch.md
authoring); table-driven tests: base-cwd → main pin, worktree-cwd + stack up →
worktree pin, worktree-cwd + no stack (deferred base compose file) → main pin.
Update BOTH code comments to state the resolved policy.

---

## Workstream B — Stage-0 resolution completeness

### B1. Full environment surface (G4) — `feature/stage-0-capability.xml` + small Go
1. Extend the existing exec-env parser (`execruntime.go` is already the authoritative
   reader of test-environment.md) with the remaining discovery fields — fixture
   command, base URL, published ports (already), Playwright status, stack baseline —
   and expose `tmux-cli exec-env --json` dumping the parsed struct.
2. Stage 0.3a consumes that JSON instead of hand-reading the file; the emitted
   TOPOLOGY block adds: TEST ROOTS (from convention packs / layout.md),
   `{{e2e_runner}}` (from the 0.2 Playwright/Cypress config probe), and
   `{{fixture_command}}` — the exact names Stage 3b/4 already consume.

### B2. Deterministic quality-gate entrypoints (G21) — Go helper + `stage-0-capability.xml`
**Ownership corrected by the completeness critic** (the probe-and-choose step is a
DECISION, so it belongs in Go, not shard Bash): new `tmux-cli gates resolve --json`
(or an extension of `exec-env --json`) that, given the pack-prescribed alternatives,
probes composer.json `scripts`, Makefile targets, and `vendor/bin`/
`node_modules/.bin` presence and emits the BOUND entrypoint per gate (+ `unresolved`
markers). The shard consumes the JSON, then **baseline-verifies** each bound gate once
via `exec-runtime` (must exit 0 or produce a recognized report on the clean tree).
An unresolved or baseline-red gate is a NAMED Stage-0 finding (surfaced at Stage 2),
never silently carried into goal validates. Also add one B1 sentence defining where
ARCHITECTURE is derived before 0.4 emits it (today: implicit via `rules.Detect`).

### B3. Research-root independence (G3) — `stage-0-capability.xml` 0.1/0.4, `stage-1-context.xml` 1.1, `stage-fast-path.xml` F.1
`/tmux:feature` resolves GOAL mode **only from an explicit dispatch-path goal id**
(same rationale as plan.xml:179's dispatch-path binding); it NEVER adopts the global
`taskvisor-current-goal` marker (daemon-owned, last-writer-wins). A standalone run is
always STANDALONE even while the daemon executes other goals.

### B4. Frontend signal correctness (G18) — Go + generator XML
1. `rules.go parseHasFrontend`: derive `has_frontend` primarily from the explicit
   `**Frontend:**` line (vue/twig → yes, none → no); use the Playwright line only for
   e2e-runner availability. Fixes the `"Not installed — needs Gate 0 setup"` → TriNo
   trap (negative substrings match first today). Unit tests over all three discovery
   phrasings.
2. Generator scaffold (SC-19/SC-20): after installing Playwright, update the
   test-environment.md Playwright line to `Installed and configured` (the file is the
   declared ExecRuntime source; keeping it current is generator business).

---

## Workstream C — Rule & gate fidelity (the "all rules" core)

### C1. validate_kind routing (G6) — `stage-4-implementation.xml` 4.3a + verify against `coderules.go`
Read the actual kind enum from `rules match --json` (implementation step: confirm the
real field values in coderules.go); route ALL kinds: machine → append validate_cmd;
review → convention-audit; **mixed → BOTH**. Add a Go test pinning the JSON field
names/values the XML references (schema-drift guard).

### C2. Re-match over new paths (G7) — `stage-3-docs.xml` or `stage-4-implementation.xml` 4.1
After the naming manifest freezes (Stage 2.4), run
`tmux-cli rules match --files <manifest target paths + test paths> --json` and UNION
with the Stage-1 blast-radius matches. The union feeds 4.3a's code-rule gate. (Go owns
which rules apply — this only widens the input file set to include the files the
feature will CREATE.)

### C3. Convention-audit without suppressing derived investigators (G17)
Preferred mechanism — **no Go change, no config override**: append MUST review-kind
rules to the goal's `validate` list as `review:`-prefixed entries (the native pack
idiom, cf. `analysis.yaml:39`), and let `deriveInvestigators` compose the config as it
does for generator goals. Stage 4 stops passing `investigation_config` for this
purpose entirely. Implementation step: verify `deriveInvestigators` routes `review:`
entries to review investigators (it derives from validate rules); if it doesn't, fall
back to Go: merge explicit entries with derived (derived first, cap 4, emission-check
and code-review protected) + tests.

### C4. Stack preparation slot (G16) — `stage-4-implementation.xml` 4.3a
Add GATE_SET slot 2b: when a goal's validate carries an E2E/HTTP probe AND the
database pack resolved (or `bin/ensure-test-stack.sh` exists), prepend
`bash bin/ensure-test-stack.sh` as a SEPARATE validate line (ENSURE-STACK-CONV
parity), and phrase HTTP probes with the markers `isStackConsuming` matches
(implementation step: read the exact marker set in dispatch.go and pin the phrasing in
the shard).

### C5. Conditional validators + rules on the fast path (G8) — `stage-fast-path.xml` F.4/F.6
When the confined diff touches manifest/schema or migration artifacts, run the
resolved `mono-schema` / `db-validate` validators (via exec-runtime) as part of the
hard gates; ensure-test-stack per A1.3. The reviewer already receives `agent_review`
rows — extend its charter with the MUST review-kind rules matched over the diff
(C2's union applies here too).

### C6. Preconditions parity (G27) — `stage-4-implementation.xml` 4.3a
New GATE_SET output: derive `[service] <host>:<port>` / `[env]` precondition lines
from test-environment.md's external services (exactly the generator's step-1-gate0
idiom) and pass them to goal-create. Local/external-services projects then get the
`BlockedByPrecondition` park instead of burning retries.

---

## Workstream D — Emission correctness (full pipeline tier)

### D1. Phase token vocabulary (G24) — Go (`tools_taskvisor.go`) + `stage-4-implementation.xml` 4.2
**Direction corrected by the completeness critic:** the ledger vocabulary is the
pinned contract — `goals.go:34-38` mandates the literal `final_gate` ("never the bare
'final'") and the stall watchdog (`FinalGateBlockedByFailed`) keys on it; existing
discovery-built ledgers already carry `final_gate`/`event_listener` literals. So do
NOT map feature tokens down to the MCP enum (that would blind the watchdog and split
the ledger vocabulary). Instead **widen `allowedPhases`** to accept the generator's
PHASE_ORDER literals (`event_listener`, `error_handling`, `middleware`, `api_docs`,
`messenger`, `health_check`, `docker`, `cicd`, `dx`, `final_gate`) alongside the
existing tokens, and add a drift test tying `allowedPhases` ⊇ PHASE_ORDER tokens ∪
{`PhaseFinalGate`}. Stage 4.2 then states the accepted token list verbatim.

### D2. Scope emission (G19) — `stage-4-implementation.xml` 4.3
Every goal-create passes `scope`: the unit's naming-manifest target paths + its Test
Plan test path — exact file pathspecs per the task-436 convention (never `stem/**`,
zero-match rejected by authoring.go). Restores real sibling parallelism and the
in-scope/needs-merge classification.

### D3. Event-goal emission checks (G5) — Go (`eventgoals.go`) + shard note
Extend the task-529 class of fix to the daemon side: `deriveEmissionInvestigator`
resolves producer paths from the layout (`layout.md` `{src}` roots via
`internal/rules/layout.go`), never a root `src/` literal; feature Stage 4 additionally
passes the manifest-known producer path explicitly for event-class units. Table-driven
tests over the DDD monorepo layout.

### D4. Pre-seed that actually works (G14 + G13) — Go (`authoring.go`, `dispatchcmd.go`) + `stage-4-implementation.xml` 4.3b
1. **Status-gated plan-skip (G14):** `resolveDispatchKind`'s plan-once downgrade fires
   only when the goal-root tasks.yaml has `status: ready` (parse, don't stat). Then
   mechanism A's pre-seed moves to the goal ROOT with `status: planning` — plan step
   0c finds tasks-path, enters INCREMENTAL, preserves the seeded task; no false skip.
   The `research/` copies are dropped from the shard.
2. **Atomic seeding (G13):** extend `CreateGoal`/MCP goal-create with an optional
   `seed_dir` — contents moved into `.tmux-cli/goals/<id>/` under the goals lock,
   BEFORE the ledger append persists. 4.3b pre-stages spec fragment + tasks.yaml in a
   temp dir and passes it; the goal is never visible without its seed. (Authoring-time
   file placement, not a new daemon state.)

### D5. Multi-feature resume (G15) — `stage-0-capability.xml` 0.1
VALIDATION-RESUME globs ALL `.tmux-cli/research/*/feature-goals.md` and matches by
FEATURE description (newest only as tiebreak among matches). A non-matching newest
file no longer masks an older matching one.

### D6. ADR home (G20) — `stage-2-architecture.xml` 2.4
Scan BOTH `docs/architecture/adr-*.md` and `docs/architecture/adrs/adr-*.md` for the
next number; write into `adrs/` when that directory exists (the discovery corpus
home), else `docs/architecture/`.

### D7. FE fallback row (G26) — `stage-3b-test-strategy.xml` 3b.3
Add the explicit row: FE-class unit with `has_frontend=false` → HTTP-level E2E
fallback, recorded with reason + a stale-signal warning when `frontend_mode≠none`
contradicts `has_frontend=no` (that contradiction is exactly the G18/B4 signature).

---

## Workstream E — Lifecycle coexistence with the generator & daemon

### E1. Goal origin tag (G10 + G22) — Go (`goals.go`, `authoring.go`, `plannext.go`) + generator shard notes
Add `origin: generator|feature|manual` to the Goal model (default by author path:
task-plan-generate → generator; feature Stage 4 → feature; task-list consume → manual).
Then:
- `LAST_GOAL` binding, `trailingConsecutiveFailures`, `authoredThisRun`, and
  `planNext.baselineGoals` growth detection filter to **generator-origin** goals only.
- `drivePlanNext` no longer kills the plan-next window when a feature goal lands
  mid-episode.
Table-driven tests: interleaved ledgers (feature failure ≠ generator corrective;
feature growth ≠ episode close).

### E2. Namespaced worker budget (G23) — Go (`tools.go`)
MaxWorkers counts within the requesting supervisor's namespace: a bare
`investigator-` spawn counts only bare-namespaced windows; `investigator-055-*`
counts only within `055`. Test with mixed daemon + feature windows.

### E3. `feature:` settings persistence (G12) — Go (`internal/setup/config.go`) + `internal/tui/settings.go`
Add the typed block:
```go
Feature FeatureSettings `yaml:"feature"`
// FeatureSettings{ Tests string `yaml:"tests"`, AutoApprove bool `yaml:"auto_approve"` }
```
so the lossy re-save round-trip preserves it (same rationale as the api: block,
config.go:38). Mirror in the TUI settings editor (AGENTS.md: TUI must mirror Settings
fields). Round-trip test: load→save preserves `feature:`.

### E4. Cross-goal validation without impersonating the daemon (G25 — confirmed) — Go param + `stage-5-validation.xml` 5.2
Confirmed: `GoalValidationDone` authorizes strictly by caller `TMUX_WINDOW_UUID` ==
the daemon-created validator window's UUID (tools_taskvisor.go:783-790), and
investigate.xml binds to `GOAL_DIR/validator-window` — a marker naming a dead daemon
window after the goal is terminal. A feature-sup-invoked `/tmux:investigate` can never
complete its mandatory terminal call. Fix: delete the direct-invocation option from
5.2 and author a follow-up VALIDATION GOAL via goal-create instead. **Scope note from
the completeness critic:** a real validation goal needs the `validates:` semantics
(terminal-to-itself, CascadeFailure short-circuit) which GoalCreate does not expose,
and hand-editing goals.yaml is forbidden (tool-managed-only) — so this fix REQUIRES a
small Go addition: a `validates` param on GoalCreate (mirroring the existing Goal
field), with tests. Without it, an ordinary goal's failure would cascade to
dependents.

---

## Addendum — independent completeness pass (G28–G35 remediations)

### A5. Fast-path abort safety on the shared base branch (G28) — `stage-fast-path.xml` F.5/F.8
The base branch is SHARED with the daemon's merge-backs (`finalizeWorktreeOnDone`,
worktree.go:653,1088). (1) F.5's confinement gate compares against the worker's OWN
commits only: `git rev-list <BASE>..HEAD` — commits not authored by the worker are
excluded from the subset check (and logged), never treated as scope escapes.
(2) F.8 abort NEVER does a branch-wide `git reset --hard`: preserve the worker's
commit(s) on `fastpath-abandoned/<slug>`, then remove them via `git revert` (or
`rebase --onto` dropping only those commits) so interleaved daemon merge-backs
survive. Supersedes the A1.4 phrasing.

### A6. HTTP/E2E probe reachability at MaxGoals>1 (G29) — `stage-4-implementation.xml` GATE_SET + reuse of elaborate's port mechanic
Worktree stacks strip published ports (composestack.go:173-196) and `curl` is
host-class (never wrapped) — A4's compose-project pin cannot fix probes. Adopt the
generator's solved mechanic (elaborate.xml port-occupancy, design §9): when a feature
goal's validate carries an HTTP/E2E probe AND worktree stacks are enabled, the goal
must carry the allocated per-goal host port in its probe + HTTP-wait lines (derive in
GATE_SET via the same helper elaborate uses, or route such feature goals through the
elaborate step). Never emit a probe against the main stack's published port for a
worktree-validated goal.

### B4-ext. Stale Playwright line on ALREADY-built projects (G30) — Go (`execruntime.go`) + `stage-0-capability.xml` 0.3a
Mirror the B4 semantics fix in `playwrightApplicable` (execruntime.go:196) so the two
parsers cannot disagree (shared helper + tests over all three discovery phrasings).
Add a Stage-0.3a stale-line detection symmetric to the Run-Target hard stop: frontend
signals present + a Playwright config on disk + a "not installed" line in
test-environment.md ⇒ NAMED repair instruction (update the line) before emitting
goals — covers projects built before the scaffold fix.

### C7. Credential-by-reference parity, GM-05 (G35) — `stage-3b-test-strategy.xml` + `stage-4-implementation.xml`
Add the generator's GM-05 rule to both shards: auth/E2E specs, dossiers, and goal
artifacts reference credentials by test-environment.md pointer / env-var NAME only —
never inlined values. (Also an org data-privacy requirement.)

### D8. Crash-safe emission ledger (G31) — `stage-4-implementation.xml` 4.3/4.4 + `stage-0-capability.xml` 0.1
Persist `{research-root}/feature-emission.md` APPEND-PER-GOAL-CREATE (unit → goal id,
before 4.4's final handoff). Stage 4 consults it on entry and skips already-emitted
units (idempotent re-run); Stage 0's resume check treats a present emission ledger
without feature-goals.md as "resume Stage 4 mid-emission", not a fresh run. Fast path:
F.2 records the worker-commit SHA into fast-path-exec.md the moment DONE cites it, so
a crashed run's re-entry can find and revert/adopt the orphan commit.

### E5. Blocked-goal surfacing in Stage 5 (G32) — `stage-5-validation.xml` 5.3
Add a third branch: goals with `BlockedBy`/`BlockedByPrecondition` are NEITHER
in-flight nor terminal — surface the block reason + the concrete remedy (needs-merge:
the runbook from `needs-merge.md`; precondition: the failing `[service]`/`[env]`
probe) in the status table, and stop in VALIDATION-PENDING with that remedy named.

### C6-ext / D2-ext. `migrates` reachability (G33) — Go (`tools_taskvisor.go`) + GATE_SET
Add a `migrates` bool param to GoalCreate (mirroring Goal.Migrates, goals.go:213);
GATE_SET derives it in the same step that detects migration artifacts for
`db-validate` (a migration artifact in the footprint ⇒ `migrates: true`), restoring
the run-ALONE shared-schema exclusion for feature goals.

### C4-ext. Full binding-convention family (G34) — `stage-4-implementation.xml` 4.3a
Extend GATE_SET beyond ensure-stack: HTTP-WAIT-CONV (bounded wait, no `sleep N`),
HTTP-CONV (resolved base URL/published — or A6's per-goal — port, never hardcoded),
NODE-TOOL-CONV (no Node command emitted when NodeSvc unresolved), the E2E-ENV /
SIDEFX / DATA-ISOLATION / AUTH-STATE conventions for E2E rows, and CMD-CONV's
monorepo per-component scoping for the 1b test gate (`--component=<nearest
monorepo-component.json owner>`, never the repo-wide fork). Source: the convention
family in `embedded/rules/_base/command-execution.md` + `database/` + `frontend*/`.

---

## Phasing (dependency-ordered, one-goal-per-TDD-unit)

Every Go unit = red→green pair (or single recorded-red goal); every XML unit gates on
`make build` (embedded walk) + the drift tests added alongside.

**Phase 1 — Go foundations (no XML depends on them yet, all parallel-safe):**
`exec-runtime` CLI (keystone, incl. A4's cwd-aware compose-project pin) ·
G12 settings field · G11 literal fix + drift test · origin tag (E1) ·
MaxWorkers namespace (E2) · parseHasFrontend + playwrightApplicable shared helper
(B4.1/B4-ext) · status-gated plan-skip (D4.1) · exec-env --json (B1.1) ·
gates resolve --json (B2) · allowedPhases widening + drift test (D1) ·
GoalCreate params: `migrates` (C6-ext) + `validates` (E4) + `seed_dir` (D4.2)

**Phase 2 — Stage-0/topology shards:** B1.2 (TOPOLOGY completeness) · B2 (consume
bound entrypoints + baseline-verify) · B3 (research-root independence) · B4.2
(scaffold updates Playwright line) · B4-ext (0.3a stale-Playwright repair stop)

**Phase 3 — Emission shards:** C1 (kind routing) · C2 (re-match union) · C3
(review: validate entries) · C4 + C4-ext (binding-convention family) ·
C6 (preconditions) + C6-ext (migrates derivation) · C7 (GM-05) · D1 (token list) ·
D2 (scope) · D3 (event paths, incl. Go) · D5 (resume glob) · D6 (ADR home) ·
D7 (FE row) · D8 (emission ledger) · A6 (probe port reachability)

**Phase 4 — Fast path tier:** A1 (triage term + exec-runtime gates) · A5 (abort
safety — supersedes A1.4) · A2 (red-phase gates via exec-runtime) · C5 (conditional
validators on fast path)

**Phase 5 — Mechanism B + daemon-seam items:** D4.2 (atomic seed_dir wiring) ·
A4/G9 (worktree compose-project pin at dispatch) · E4/G25 (validation goal via
`validates` param) · E5 (blocked-goal branch in Stage 5)

**Phase 6 — Acceptance proof:**
1. `make test-all` green (all new unit tests, incl. the three drift tests: worktree
   literal, kind enum, phase vocabulary; ResolveExecRuntime fixtures over all three
   discovery Playwright phrasings).
2. **Fixture e2e:** a scripted disposable-session exercise (extend `internal/e2e` /
   `tmux:e2e-evaluator`) over a minimal discovery-built docker fixture:
   (a) precise brief → fast path lands via exec-runtime-wrapped gates;
   (b) one-unit feature → Stage 3c mechanism B → daemon skips /tmux:plan → validated;
   (c) multi-unit feature → full DAG, parallel siblings (scopes disjoint), all pack
   gates present in every goal's validate (assert `ensure-test-stack`, lint, phpstan,
   code-rule entries), TERMINAL SELF-CHECK passes;
   (d) **TDD pair on docker**: a BE-logic two-goal red→green pair validates
   end-to-end in the container (G2), plus a TESTS_MODE=off run exercising the
   no-test-artifact guard;
   (e) **concurrency**: active daemon + standalone feature run — marker independence
   (G3), seed race (G13), plan-next survival (G22), fast path with an interleaved
   daemon merge-back (G28: no false abort, no lost commits);
   (f) **fast-path ABORT**: forced scope escape → commit preserved on the abandoned
   branch, daemon commits intact, full pipeline resumes at Stage 1;
   (g) **reachability at MaxGoals>1**: an HTTP-probe feature goal's probe hits its
   OWN worktree stack port, not the main stack (G29);
   (h) **resume paths**: VALIDATION-PENDING re-entry at Stage 5, interleaved
   two-feature resume (D5), and mid-emission crash re-run consuming the D8 ledger
   without duplicate goals.
3. Rule-fidelity audit: for one emitted goal, diff its validate + investigator set
   against the equivalent generator-emitted goal — must be a superset minus
   generator-only scaffold gates.

**Constraints honored throughout:** composer-not-engine (feature shards keep
delegating; new logic lands in Go helpers or the primitives) · determinism boundary
(Go owns decisions — exec-runtime, entrypoint binding, kind enums; XML consumes JSON)
· CMD-CONV (daemon-dispatched validates stay bare; only *non-daemon* execution sites
gain the exec-runtime hop) · portability (no project-shaped literals; everything from
resolve/signals/manifests) · no new daemon state machine states (seed_dir and
status-gating are authoring/dispatch-time file semantics).

---

## Out of scope (explicitly)

- Widening fast-path triage to size-based (`mode==inline`) easy-but-vague briefs —
  separate feature; this plan only makes the existing tiers *correct* on
  discovery-built projects.
- TUI redesign beyond mirroring the new `feature:` settings block.
- Any change to project-discovery's interactive flow (it is upstream-compatible as-is;
  only the generator's scaffold gains the Playwright-status update).
