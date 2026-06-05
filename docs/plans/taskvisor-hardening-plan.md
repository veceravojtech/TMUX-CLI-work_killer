# Taskvisor Hardening Plan

Derived from the testproject autonomous run (2026-06-04/05): four plan-audit passes
(15% → 71% → 70% → 100%) plus live daemon incidents. Every item below is a fix in
**this repo** — generator XML, templates, daemon, or MCP tools. No testproject edits.

Evidence sources:
- `testproject/docs/architecture/plan-failure-analysis.md`, `plan-audit-2.md`, `plan-audit-3.md`,
  `plan-approval.md`, `plan-remediation-recommendation.md`, `plan-goal-validation.md`
- Live incidents: stale daemon executing pre-HEAD code (binary rebuilt 17:05, daemon started 15:33);
  approval ledger overstated the goal-006 `depends_on` fix (`goals.yaml:222-223` still lists only goal-002).

| ID | Priority | Title | Area |
|----|----------|-------|------|
| H1 | P0 | ENSURE-STACK runtime-state convention | generator XML + templates |
| H2 | P0 | Codify plan-audit loop as `/tmux:plan-audit` | new embedded command |
| H3 | P1 | Dispatch-time goal.md ↔ goals.yaml drift gate | daemon |
| H4 | P1 | Stale-binary guard (daemon + MCP) | daemon, MCP server |
| H5 | P2 | HTTP-WAIT convention (kill `sleep N && curl`) | generator XML + templates |
| H6 | P2 | Cross-goal dependency inference in spec-validate | MCP tools |
| H7 | P2 | Decompose task-plan-generate.xml (spine + per-step shards) | generator XML structure |

Recommended order: H1 → H3 → H2 → H4 → H6 → H5 → H7.
(H1/H3 are pure prevention of the two defect classes that consumed 3 of 4 audit passes;
H2 is the safety net that catches whatever H1/H3 don't. H7 is a mechanical restructure —
it goes last so it never blocks the prevention items; if it lands earlier, H1/H5's new
rules target the spine's `<conventions>` block instead of step 0c — see H7 interaction note.)

---

## H1 (P0) — ENSURE-STACK: generalize runtime-state gating

**Problem.** The single highest-leverage SEV-1 from `plan-failure-analysis.md`: host/E2E
validates (`npx playwright`, `curl`) run against a stack nobody guarantees is **up, migrated,
and fixture-loaded**. `DOCKER-RUNTIME-FRONTLOAD` (`task-plan-generate.xml:445`) only frontloads
`docker compose up -d --build` — it never migrates or seeds, and never re-asserts state before
each E2E goal. The fix that worked (`bin/ensure-test-stack.sh`: up → `migrations:migrate --env=test`
→ `fixtures:load --env=test`, invoked as the first validate line of every E2E/host-HTTP goal)
exists **only** in testproject. `grep -r ensure-test-stack cmd/tmux-cli/embedded/` = 0 hits.
Without this, every future plan with E2E reproduces SEV-1 and drains retry budgets to zero.

**Fix.** New binding rule `ENSURE-STACK-CONV` in the generator + template snippet.

**Steps.**
1. `cmd/tmux-cli/embedded/commands/tmux/task-plan-generate.xml` — add
   `<rule critical="true" id="ENSURE-STACK-CONV" condition="HAS_DATABASE">` next to
   `CMD-CONV`/`DOCKER-RUNTIME-FRONTLOAD` (step 0c block, ~line 150):
   - The scaffold goal (goal-002) MUST deliver `bin/ensure-test-stack.sh` (executable,
     `#!/bin/sh -e`): start stack (docker mode: `docker compose up -d`; local: no-op) →
     run migrations against the **test** env → load test fixtures. Acceptance criterion:
     `test -x bin/ensure-test-stack.sh` plus a one-line description of the three phases
     (mirror testproject `goals.yaml:59` SC-17 wording).
   - Every goal whose validate/acceptance contains an E2E or host-HTTP probe (playwright,
     `curl` against `{{BASE_URL}}`, `make test` reaching HTTP) MUST list
     `bash bin/ensure-test-stack.sh` as a **separate validate line immediately before** the
     probe line (separate line, not `&&`-joined — the daemon runs validate lines literally
     and a joined line hides which phase failed; this was flagged as drift in
     `plan-goal-validation.md`).
   - The script is emitted BARE per CMD-CONV; the daemon wraps the PHP-toolchain lines inside
     it via the container family (no hand `docker compose exec` prefixes inside the script —
     it runs migrations through `{{CMD_PREFIX}}`-style binding resolved at generation time,
     since the script itself executes on the host).
2. `cmd/tmux-cli/embedded/templates/php-symfony/fixtures.md` — append an
   "Ensure-stack script" section with the concrete Symfony script body
   (compose up → `bin/console doctrine:migrations:migrate -n --env=test` →
   `bin/console doctrine:fixtures:load -n --env=test`), referencing the existing
   `fixtures:load` lines at `fixtures.md:63-64`.
3. `cmd/tmux-cli/embedded/templates/_base/test-strategy.md` — add a stack-agnostic
   paragraph: "any plan with host-side E2E MUST have a single ensure-stack entrypoint
   produced by the scaffold goal and invoked by every E2E goal's validate".
4. Mirror the rule mention in `task-plan-generate.md` (quick-reference doc).

**Tests.**
- `cmd/tmux-cli/task_plan_generate_ensure_stack_test.go` (pattern:
  `task_plan_generate_template_test.go`): assert the embedded XML contains
  `ENSURE-STACK-CONV`, that it requires `test -x bin/ensure-test-stack.sh`, and the
  separate-line invariant ("separate validate line", not `&&`).
- Extend `embed_templates_test.go`: php-symfony `fixtures.md` contains
  `ensure-test-stack`.

**Acceptance.** A regenerated plan for a docker+DB project contains the producer in the
scaffold goal and one `bash bin/ensure-test-stack.sh` line before every E2E probe; greps
above pass; `go test ./cmd/tmux-cli/` green.

---

## H2 (P0) — `/tmux:plan-audit`: codify the audit/remediate loop

**Problem.** The process that took the plan 15% → 100% (`plan-remediation-recommendation.md`)
exists only as prose in testproject. No embedded command implements it
(`grep -r 'auditor|remediat|SEV-1' cmd/tmux-cli/embedded/` = 0 hits). Today it must be
re-invented by hand for every project.

**Fix.** New embedded command pair `plan-audit.md` + `plan-audit.xml` in
`cmd/tmux-cli/embedded/commands/tmux/`, runnable as a blocking stage between
`/tmux:task-plan-generate` and `/tmux:supervisor`.

**Design (transcribe from plan-remediation-recommendation.md, these are proven mechanics):**
- **Fresh-agent blind audit**: the auditor reads ONLY on-disk artifacts
  (`docs/architecture/*`, `.tmux-cli/goals.yaml`, `.tmux-cli/goals/*/goal.md`) — never the
  planner conversation. Spawn it as a worker (`windows-spawn-worker`), same pattern as
  `investigate.xml` inv-* workers.
- **Daemon's-eye evaluation**: judge every validate command "exactly as the daemon runs it —
  literally, in dependency order, with CMD-CONV wrapping applied".
- **8-dimension checklist**: validate executability / dependency correctness /
  runtime-state gating / host-container split / objective acceptance (no tautologies, no
  "appropriate/sufficient/properly") / spec-vs-discovery consistency / environment
  prerequisites / scope sanity.
- **Scoring rubric**: start 100; SEV-1 −25, SEV-2 −15, SEV-3 −8, SEV-4 −3.
  APPROVE iff ≥90 AND zero open SEV-1/SEV-2 → write `docs/architecture/plan-approval.md`.
  Otherwise write `docs/architecture/plan-audit-N.md` with file:line findings.
- **Role separation**: auditor is read-only and never edits; remediation is a separate step
  (separate worker or the supervisor itself) — "the auditor must not grade its own fixes".
- **Hard guardrail (verbatim)**: reach 90% by *fixing the plan*, never by weakening
  `validate`/`acceptance` to make red turn green.
- **Stop conditions**: approved, or 3 passes without reaching 90% → halt and escalate to
  human with the open-findings list.
- **NEW (learned from the goal-006 ledger failure)**: the auditor MUST re-verify every
  "RESOLVED" claim from prior passes by running the verifying command itself; it never
  trusts a prior pass's resolved-issue ledger. (Pass-4 approved a `depends_on` fix that is
  not on disk.)
- **NEW (learned from pass-3)**: mandatory corruption re-sweep each pass — when a defect is
  found in one goal, grep the whole `.tmux-cli/` tree for the same defect *pattern*
  (pass-2 fixed goal-004's mangled phpunit token; pass-3 found the identical mangling in
  goal-003/goal-005).

**Steps.**
1. Author `plan-audit.xml` (structure mirrors `investigate.xml`: glossary, numbered steps,
   worker spawn/collect, verdict write) + `plan-audit.md` summary doc.
2. Wire the handoff: in `task-plan-generate.xml`, final step's completion message must
   instruct "run /tmux:plan-audit before /tmux:supervisor; execution is blocked until
   plan-approval.md records ≥90%".
3. Daemon gate (small, optional flag): in `internal/taskvisor/daemon.go` activation path,
   if `setup.TaskvisorSettings.RequirePlanApproval` (new bool, default **false** for
   backward compat) and `docs/architecture/plan-approval.md` is absent → refuse to activate
   with a loud reason (reuse the `haltReason` dashboard banner mechanism). Config plumbing
   in `internal/setup/config.go` next to `MaxWallClockSec` (line ~84), TUI toggle in
   `internal/tui/settings.go`.
4. No embed registration needed — `//go:embed embedded/commands/tmux` (`session.go:45`)
   picks new files up recursively. Keep `.md`/`.xml` pairing consistent with the other
   commands, and avoid dangling cross-references (the
   `consolidate_validate_xml_test.go` sweep walks every embedded command and fails on
   references to dead skills).

**Tests.**
- `cmd/tmux-cli/plan_audit_command_test.go`: embedded files exist; XML contains the rubric
  numbers (−25/−15/−8/−3, 90), the re-verify-ledger mandate, and the never-weaken-validate
  guardrail.
- `internal/taskvisor/`: activation-refusal test for `RequirePlanApproval=true` without the
  approval file (pattern: `wallclock_budget_test.go` uses the clock seam + halt assertions).
- `internal/setup/config_test.go`: default false, yaml round-trip.

**Acceptance.** `/tmux:plan-audit` is invocable on any project with `.tmux-cli/goals.yaml`;
produces `plan-audit-N.md`/`plan-approval.md`; daemon optionally refuses unapproved plans.

---

## H3 (P1) — Dispatch-time goal.md ↔ goals.yaml drift gate

**Problem.** Pass-2 and pass-3 each burned a full audit pass on the same defect class:
`goal.md` validate text corrupted (mangled `phpunit\Domain` tokens) while authoritative
`goals.yaml` stayed correct. Workers implement and self-validate against `goal.md`; the
daemon's final gate reads `goals.yaml` — so the goal deterministically fails its cycles and
drains budget while the plan "looks" correct. `authoring.go CreateGoal()` prevents drift at
*creation*, but nothing re-checks before *dispatch*, and hand edits (the actual root cause:
"a botched edit dropped `--filter=…`") bypass authoring entirely.

**Fix.** Mechanical consistency check + self-repair at dispatch, zero retry-budget cost.

**Steps.**
1. New `internal/taskvisor/specdrift.go`:
   - `func goalMDDrift(goalDir string, g *Goal) (drifted []string, err error)` — parse the
     goal.md "Validation Rules" + "Investigation Config" sections (reuse
     `indexOfHeading`/section helpers from `goalmd.go`) and report every `g.Validate` command
     (authoritative, from goals.yaml) that does not appear verbatim in goal.md, plus every
     goal.md command line that does not appear in `g.Validate`.
   - Normalization: trim whitespace only. Do NOT fuzzy-match — the corrupted token differed
     by a dropped flag, and fuzzy matching would have masked it.
2. In `internal/taskvisor/dispatch.go` `dispatch()` (line ~180) and `dispatchRetry()`,
   before payload assembly: run the drift check. On drift → log loudly
   (`drift detected: goal.md diverges from goals.yaml on N commands`), then **repair
   goal.md from goals.yaml** by re-rendering the affected sections via `WriteGoalMD` /
   `spliceInvestigationConfig` (`goalmd.go:10,206`). goals.yaml is always the source of
   truth (matches the daemon's final-gate semantics). Repair is mechanical → charge no
   retry budget (same principle as RC-C structured corrections in `statemachine.go`).
3. Surface a dashboard counter (`internal/taskvisor/dashboard.go`): `spec repairs: N` so
   silent hand-edit corruption becomes visible.
4. Guard: if repair itself fails (unwritable goal.md), fail the dispatch loudly
   (fail-loud doctrine, same as `writeSupervisorWindowMarker`).

**Tests.** `internal/taskvisor/specdrift_test.go`:
- corrupted goal.md (literally `vendor/bin/phpunit\Domain` vs yaml
  `vendor/bin/phpunit --filter=IdentityAccess\\Domain`) → drift detected, goal.md repaired
  byte-correct, dispatch proceeds;
- identical files → no-op;
- unwritable goalDir → dispatch returns error;
- repair charges no budget (assert retry counters unchanged).

**Acceptance.** Reproducing the pass-3 corruption in a fixture goal is self-healed on the
next tick with a log line and counter increment; no validation budget consumed.

---

## H4 (P1) — Stale-binary guard

**Problem.** Two recorded incidents: (a) `[[mcp-server-stale-after-install]]` — `make install`
replaces the binary but running MCP servers keep serving old code; (b) 2026-06-05: the
`taskvisor --run` daemon (started 15:33) executed testproject while the binary on disk
(rebuilt 17:05, post-HEAD) contained all P1–P7 fixes — i.e. the run was silently missing
every hardening just shipped. Nothing detects or reports this.

**Fix.** Self-staleness detection: compare the running process's executable identity with
the on-disk binary at the same path.

**Steps.**
1. New `internal/setup/buildstamp.go`:
   - At process start capture `os.Executable()` (resolve symlinks) + that file's
     mtime/size. `func BinaryStale() (stale bool, detail string)` re-stats the path and
     reports if mtime/size changed since start (the file was replaced under us).
   - Note: works on Linux even after replacement because the stat target is the new file
     while the process maps the deleted inode — exactly the signal we want.
2. Daemon: in `internal/taskvisor/daemon.go` `tick()`, check `BinaryStale()` at most once
   per minute (use the `d.now()` clock seam). On stale: set a dashboard banner
   (`haltReason`-style but non-fatal): `BINARY STALE — restart taskvisor to apply <mtime>`.
   New config `HaltOnStaleBinary bool` (default **false**): when true, finish the in-flight
   goal cycle, then halt-loud instead of dispatching the next goal (mirror the P3
   `haltWallClock` flow — statuses untouched, human restarts).
3. MCP server: in `internal/mcp/server.go`, on each tool call (cheap stat) prepend a
   warning line to the tool result when stale: `[tmux-cli mcp is stale: binary replaced
   <when>; restart the MCP server]`. This makes the `[[mcp-server-stale-after-install]]`
   failure self-announcing to the calling Claude.
4. `version` in `cmd/tmux-cli/main.go:5` is a constant `"0.1.0"` — additionally embed
   `vcs.revision` via `debug.ReadBuildInfo()` and print it in the dashboard header and
   `tmux-cli --version`, so "which code is this process running" is answerable.

**Tests.**
- `internal/setup/buildstamp_test.go`: copy a temp file as the "executable", stamp, touch
  /replace it, assert stale; untouched → not stale.
- `internal/taskvisor/`: stale + `HaltOnStaleBinary=true` → halts after current goal with
  banner (clock-seam test, pattern `wallclock_budget_test.go`); default false → banner only,
  dispatch continues.

**Acceptance.** Rebuilding the binary while a daemon runs produces a visible banner within
one tick; MCP tool results carry the staleness warning; opt-in halt works.

---

## H5 (P2) — HTTP-WAIT convention (replace `sleep N && curl`)

**Problem.** `plan-approval.md` non-blocking note: fixed `sleep 5`/`sleep 10` before `curl`
races a cold image build; today it's "recoverable via max_retries" — i.e. it burns a retry
on a timing flake. The generator has no rule against emitting fixed sleeps.

**Steps.**
1. `task-plan-generate.xml` step-0c conventions block: add
   `<rule critical="true" id="HTTP-WAIT-CONV">` — never emit `sleep N` before an HTTP
   probe. Emit either (docker mode, preferred) compose healthchecks +
   `docker compose up -d --wait`, or (fallback) a bounded poll:
   `i=0; until curl -sf {{BASE_URL}}/path; do i=$((i+1)); [ $i -ge 30 ] && exit 1; sleep 2; done`.
2. `templates/_base/environment-gate.md` + `templates/php-symfony/environment-gate.md`
   (check-command entries: `_base` lines ~47-124, `php-symfony` lines ~29-105): document
   the poll form as the canonical HTTP/DB readiness check.
3. Note interaction: ensure-stack (H1) already serializes "up" before probes; HTTP-WAIT
   covers the first-boot path inside scaffold/Gate-0 goals where ensure-stack doesn't exist
   yet.

**Tests.** Extend `task_plan_generate_template_test.go`-style assertion: XML contains
`HTTP-WAIT-CONV` and the literal `--wait`; embedded XML/templates contain no
`sleep 5 && curl` / `sleep 10 && curl` pattern (regression grep
`sleep [0-9]+ *&& *curl`).

**Acceptance.** Generated plans contain no fixed-sleep HTTP probes; greps pass.

---

## H6 (P2) — Cross-goal dependency inference in spec-validate

**Problem.** The SEV-2 class from pass-1: goal-006 consumes `User.php` that goal-003
produces, but `depends_on` listed only goal-002 — a dependency-ordered scheduler may
dispatch the consumer first. The hand-fix was applied and then **lost again** (verified
2026-06-05: `goals.yaml:222-223` lists only goal-002 despite the approval ledger claiming
otherwise). Humans and LLM auditors both missed the regression; this must be mechanical.

**Fix.** Producer/consumer analysis over the goals file, exposed through the existing
`spec-validate` MCP tool and as a daemon pre-activation warning.

**Steps.**
1. New `internal/taskvisor/depinfer.go`:
   - `func InferMissingDeps(goals *GoalsFile) []DepFinding` — build
     `path-stem → producing goal` from each goal's scope/deliverables (reuse the
     path-token extraction from `scope_gate.go` `DeriveScopeWithCompleteness`, including
     its `./`-strip and letterless-token hygiene). Then scan every other goal's
     acceptance+validate text for those stems; a hit on a stem produced by goal X without
     `depends_on` containing X (transitively — walk the DAG, direct edge not required)
     yields `DepFinding{Consumer, Producer, Stem, Evidence}`.
   - Conservative on purpose: report only file-path stems (contain `/` or a known source
     extension), never bare words — false positives here would train users to ignore it.
2. Wire into `internal/mcp/tools.go SpecValidate` (line ~755) output: new `dep_warnings`
   field on `SpecValidateOutput` (`internal/mcp/server.go:80`).
3. Daemon: on activation (`activate()` in `daemon.go`), run `InferMissingDeps`; log each
   finding loudly and show a dashboard line `dep warnings: N`. Do NOT auto-add edges
   (ordering is plan semantics; auto-editing the DAG could serialize a valid parallel
   plan) — surfacing is the goal, the plan-audit command (H2) consumes the output.
4. Add the check to the `/tmux:plan-audit` dimension "dependency correctness" (H2):
   the auditor must run `spec-validate` and treat each `dep_warning` as a SEV-2 candidate.

**Tests.** `internal/taskvisor/depinfer_test.go`:
- consumer references producer's deliverable without edge → finding (reproduce
  goal-003/goal-006 shape verbatim from testproject);
- transitive edge present → no finding;
- bare-word stems ignored; `./`-prefixed and bare path forms unify (scope-gate hygiene);
- acyclic untouched DAG (function is read-only).

**Acceptance.** Running `spec-validate` against testproject's current `goals.yaml`
reports exactly the goal-006→goal-003 finding; daemon activation surfaces it.

---

## H7 (P2) — Decompose `task-plan-generate.xml` into a spine + per-step shards

**Problem.** `task-plan-generate.xml` is 3,394 lines / 274 `<rule>` elements in one file,
and every generator H-item (H1, H5) grows it further. Three concrete costs:

1. **Context bloat**: the planner must ingest the entire prompt although 7 of the ~20
   generation steps are conditional and frequently skipped (3.16a auth-bootstrap, 3.19a
   seed-admin, 3.22 middleware, 3.23 api-docs, 3.24 messenger, 3.26 docker, 3.27 cicd).
   A docker-less, auth-less API project pays for ~800 lines of dead steps on every run.
2. **Convention drift surface**: the binding cross-step rules live *inside* steps —
   `CMD-CONV`/`HTTP-CONV`/`NODE-TOOL-CONV` in step 0c, `validate-acceptance-mandate` and
   `scope-derivation` in substep 1.7 — and are referenced from 2,000+ lines away by prose
   ("per the validate-acceptance-mandate in substep 1.7"). The file itself already carries
   anti-drift warnings ("IDENTICAL rule to step 0b / substep 1.2 / step 3.26 — do not
   drift") — i.e. the structure is fighting the author.
3. **Audit/test ergonomics**: content-guard tests (`task_plan_generate_template_test.go`,
   `generate_docker_goal_test.go`, …) grep one 3.4k-line blob; a step-scoped assertion
   can silently match text from an unrelated step.

**Fix.** Split into a small always-loaded **spine** plus a `task-plan-generate/` shard
directory with **one file per generation step**, loaded lazily by the planner only when
the step's firing condition holds.

**Target shape.**

```
cmd/tmux-cli/embedded/commands/tmux/
  task-plan-generate.md            ← companion (gains a shard index table)
  task-plan-generate.xml           ← spine, target ≤ ~700 lines
  task-plan-generate/              ← shards, XML ONLY (no .md — a subdir .md would
    step-1-gate0.xml                  register as a spurious slash command; the
    step-2-scaffold.xml               worker/execute.md precedent proves subdir .md
    step-3.14-domain.xml              ARE picked up as /tmux:worker:execute)
    step-3.15-application.xml
    step-3.16-infrastructure.xml
    step-3.16a-auth-bootstrap.xml
    step-3.17-fixtures.xml
    step-3.17.0-controller-path-resolver.xml   (own flow step, runs before 3.18/3.19 —
                                                its resolver rules bind both)
    step-3.18-controller-actions.xml
    step-3.19-auth-flows.xml
    step-3.19a-seed-admin.xml
    step-3.20-event-listeners.xml
    step-3.21-error-handling.xml
    step-3.22-middleware.xml
    step-3.23-api-docs.xml
    step-3.24-messenger.xml
    step-3.25-health-check.xml
    step-3.26-docker.xml
    step-3.27-cicd.xml
    step-3.28-dx.xml
    step-3.29-final-gates.xml
```

**Spine keeps** (always-run content only):
- header: `<objective>`, `<input>`, `<output>`, `<requirements>`, `<glossary>`, `<llm>` mandates;
- a NEW `<conventions>` block — every cross-step binding rule hoisted here verbatim:
  `CMD-CONV`, `HTTP-CONV`, `NODE-TOOL-CONV`, `DOCKER-RUNTIME-FRONTLOAD`,
  `validate-acceptance-mandate`, `scope-derivation` (+ `ENSURE-STACK-CONV` from H1 and
  `HTTP-WAIT-CONV` from H5 once landed). In-step originals become one-line pointers;
- steps 0 / 0b / 0c inline (validation + preconditions + runtime model — always execute);
- the `<flow>` skeleton: each generation step reduced to a **stub** carrying `n`, `title`,
  the firing condition, and a load directive:
  `<load file=".claude/commands/tmux/task-plan-generate/step-3.26-docker.xml">If the
  condition holds, READ that file NOW and execute its content as this step; if not,
  skip without reading.</load>` — a prompt-level include (the agent follows it; no XML
  processor involved). Conditions stay in the stub so skipped shards are never read;
- steps 4 (ordering verification) and 5 (escalation bounce) inline — small, always run;
- `<execution-rules>` and the final `<llm final="true">` block.

**Move-only mandate.** The split moves content verbatim. The ONLY permitted text changes:
(a) "see substep 1.7"-style cross-references re-pointed at the spine `<conventions>`
block, (b) the stub/load mechanics themselves. Never reword a rule during the split —
wording IS the contract (the content-guard tests grep literal strings, same principle as
H3's no-fuzzy-match rule). The "Insert new `<step>` elements ABOVE this comment block"
authoring comment before `</flow>` is updated to: new step = new shard file + spine stub.

**Mechanics are already in place** — verified: `//go:embed embedded/commands/tmux`
(`session.go:45`) embeds recursively, the `fs.WalkDir` at `session.go:1703` flattens to
relative paths, and `setup.WriteCommands` (`internal/setup/commands.go`) `MkdirAll`s
nested dirs (the `worker/` subdir ships this way today). Installed shards land at
`.claude/commands/tmux/task-plan-generate/` in target projects — the stub paths above
resolve from the project root where the planner runs.

**Steps.**
1. Hoist the conventions into the spine `<conventions>` block; replace in-step originals
   with pointers (this is the one step that touches text, keep it surgical).
2. Cut each generation step body into its shard file; leave the stub. Shard root element:
   `<step n="…" title="…">` byte-identical to the original opening tag.
3. Update `task-plan-generate.md`: add a shard index table (step → file → fires-when) so
   the companion stays the cheap orientation layer.
4. Test refactor: add a `readGenerateBundle(t)` helper (both in `cmd/tmux-cli` and
   `internal/setup` test packages — `seed_credentials_test.go` and
   `generate_docker_goal_test.go` read via relative path) that concatenates spine + all
   shards in flow order. Whole-prompt assertions (validate-mandate, docker front-load,
   controller-path resolver, H1/H5 grep guards) switch to the bundle; step-scoped
   assertions switch to reading their single shard (tightens them for free).
5. New `cmd/tmux-cli/task_plan_generate_shards_test.go` integrity test:
   - every spine stub's `<load file=…>` target exists in the embed FS;
   - every shard is referenced by exactly one stub;
   - shard filename step number == the `n` attr of its root `<step>`;
   - spine contains no leftover step bodies (no `<substep` outside steps 0/0b/0c/4/5);
   - `task-plan-generate/` contains zero `.md` files.

**Interaction.** If H7 lands before H1/H5, their new rules go straight into the spine
`<conventions>` block (and H1's per-goal validate-line requirement into the relevant
shards). If after — the hoist in step 1 picks `ENSURE-STACK-CONV`/`HTTP-WAIT-CONV` up
with the rest. H2's handoff edit (final completion message) targets the spine either way.

**Tests.** Steps 4–5 above; plus `go test ./cmd/tmux-cli/ ./internal/setup/` green with
zero assertion *content* changes (greps unchanged, only the read helper changes — proves
move-only).

**Acceptance.** Spine ≤ ~700 lines; all pre-existing content-guard greps pass against the
bundle; the integrity test passes; a planner run on a project with no docker/auth/messenger
ADRs never needs to read the corresponding shards (verifiable from the stub conditions);
`/tmux:task-plan-generate` remains the only registered slash command for this flow.

---

## Out of scope (deliberately)

- Editing testproject's plan/goals — that's project data, not engine.
- Auto-editing `depends_on` (H6 surfaces only; see rationale).
- Token/$ budget metering — wall-clock ceiling (P3, shipped) remains the proxy.

## Definition of done (whole plan)

1. `go test ./...` green; `go vet ./...` clean.
2. Each H-item's acceptance check passes.
3. `docs/taskvisor-spec.md` updated for: drift gate (H3), stale-binary banner/halt (H4),
   `RequirePlanApproval` (H2), new config keys with defaults
   (`require_plan_approval: false`, `halt_on_stale_binary: false`).
4. Restart protocol documented in README/advanced-usage: after `make install`, restart the
   MCP server AND any running `taskvisor --run` daemon (until H4 lands, nothing warns).
