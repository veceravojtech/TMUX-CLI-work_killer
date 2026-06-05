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

Recommended order: H1 → H3 → H2 → H4 → H6 → H5.
(H1/H3 are pure prevention of the two defect classes that consumed 3 of 4 audit passes;
H2 is the safety net that catches whatever H1/H3 don't.)

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
4. Register the command file in whatever embed manifest `consolidate_validate_xml_test.go`
   checks; keep `.md`/`.xml` pairing consistent with the other commands.

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
   `spliceInvestigationConfig` (`goalmd.go:10,178`). goals.yaml is always the source of
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
   (`check_command` entries, lines ~126-180): document the poll form as the canonical
   HTTP/DB readiness check.
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
   field on `SpecValidateOutput` (`internal/mcp/server.go:79`).
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
