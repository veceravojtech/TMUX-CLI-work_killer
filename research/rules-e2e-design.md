# Rules E2E — Design: full lifecycle + adapted Phase 2

**Date:** 2026-06-12
**Builds on:** `per-product-rule-resolution.md` (phase 1, shipped a38a4d2) — pack catalogue, `tmux-cli rules resolve`, conventions binding the planner, code-rules (php/, php-symfony/ incl. ddd-conventions.md) referenced in goals as a prose line.
**Goal:** rules flow through EVERY lifecycle surface with enforcement, not just reference — and the deferred phase 2 (per-step `condition=` extraction) is adapted now that the DDD rules live in the catalogue.

---

## 1. Current state and the gap

After a38a4d2 the catalogue exists and the planner *loads* it, but code-rules
reach only one surface: a "Code rules: read and honor <paths>" line in goal
descriptions. Nothing verifies a spec designed for the rules, an implementer
read them, a validator checked them, or the audit scored them. `local/` rules
have no falsifiability guard at all (the selftest covers only embedded packs).
That is the gap E2E closes.

Two consumption mechanics, fixed up front:

- **Agents read files; Go injects structure.** Rule *bodies* are never inlined
  into goals/specs (token bloat, drift). Goals carry rule *IDs + paths*;
  structured fields (acceptance, validate, investigators) carry rule-*derived*
  entries because only structured fields are daemon-visible (the
  validate-acceptance-mandate's own lesson).
- **Severity × validate_kind decides the enforcement channel** (§5), not the
  surface. `must`+automated = hard daemon gate; `should`+review = investigator
  finding, waivable with written justification (previo gate semantics).

## 2. The E2E lifecycle — surface by surface

### 2.1 Discovery (produces signals)
`task-plan-discover.xml` Step 7 writes test-environment.md. **Add one line to
the saved template: `**Stack:** {{LANG}}-{{FRAMEWORK}}`** (e.g. `php-symfony`).
`rules.Detect` gains a parser for it (priority: Stack line → manifests →
symfony-mention fallback). This removes the planner's `--lang/--framework`
flag dependency for greenfield and makes every later surface's resolve calls
deterministic with zero flags. (D-14 gate text extends to require the line.)

### 2.2 Signal authority — `tmux-cli rules resolve --signals` (phase 2a, §4)
New flag dumps the detected Signals as JSON (lang, framework, run_target,
has_database, has_frontend, n_auth_flows + the §4 extensions). One
deterministic source; the planner XML's scattered re-derivation prose
(grep composer.json, re-read Playwright line per step) collapses to "read the
signal dump captured in Step 0".

### 2.2b Matching authority — `tmux-cli rules match` (phase 2b; closes the determinism boundary)
Agents never glob-match. New subcommand:

```
tmux-cli rules match --files <p1,p2,...> [--phase <phase>] [--json]
```

resolves code-rules for the project, filters to rules whose `applies_to`
globs match any given file path (and whose `phase` matches when --phase is
passed), and emits per rule the PRE-RENDERED injection payload: id, severity,
validate_kind, paths, the `CR-<id>` acceptance line, and — for rules with a
signal — the fail-closed validate command. Consumers: generation (per goal,
with deliverable paths) and the plan supervisor (per task, with the task's
file footprint). Glob/severity/signal routing lives in `internal/rules`,
shared with resolve; the agent only copies payloads into goal-create /
task messages.

### 2.3 Generation — rule→goal injection (phase 2b; the core)
At every goal-creation step, generation runs `rules match --files
<deliverable paths> --phase <goal phase>` (§2.2b) — no agent-side glob
matching, no hand-built RULE_INDEX. For the returned set:

| rule shape              | injected into goal                                                                 |
|-------------------------|------------------------------------------------------------------------------------|
| any matching rule       | goal.md rules line: `Code rules: PHP-ARCH-002, PHP-PERS-001 — read <paths>` (IDs now, not just paths) |
| `must` (any kind)       | one acceptance criterion per rule, tagged `CR-<id>`, body = the rule's first acceptance entry |
| automated/mixed w/ signal | one fail-closed validate line: `sh -c '! grep -rE "<signal>" <goal-scope-globs>'` — ONLY after the plan-time baseline run passes (§6) |
| review/mixed            | folded into ONE `convention-audit` investigator per goal: paths = goal scope, fail = "any cited must rule violated; should violations reported as findings", commands = the automated signals (greps) for the mixed rules |

**Investigator-budget rule** (validateInvestigators enforces 2–4 entries):
when the step's goal template already emits a `convention-audit` investigator,
the rule checks MERGE into it (extend fail criteria + commands); when it
doesn't and the template emits 4 investigators, generation drops the
template's lowest-value investigator in favor of the rule investigator only
if `must` review rules apply — otherwise the rules ride spec citation alone.
Steps revised in 2b emit ≤3 template investigators when code-rules are
resolvable, reserving the fourth slot.

Anti-bloat caps (§6): acceptance injection for `must` only; broad-glob rules
(`src/**`) ride the investigator + spec citation, never per-goal acceptance,
unless severity=must AND the goal's phase matches the rule's phase.

`rule.phase` already uses the taskvisor phase enum (the previo2 catalogue and
taskvisor deliberately share it), so phase-mismatch filtering is a string
compare. `depends_on_rules` does NOT create goal dependencies in greenfield
(the DAG already orders phases); it is recorded in the goal.md rules line for
the implementer.

### 2.4 Spec (plan.xml spec workers + spec-validate)
- `buildTaskMessage` (internal/mcp/tools.go:600) gains a `CODE_RULES:` field.
  The supervisor derives it per task by running `rules match --files <task
  file footprint>` (§2.2b) — it never parses the goal.md prose line; the
  prose line stays human/implementer-facing only. Applies to plan.xml step 4
  worker dispatch and self-spec step 3d alike.
- Spec template gains a **`## Code Rules` section**: one line per `must` rule
  ID — how the design satisfies it; `should` rules listed with apply/skip+why.
- `SpecValidate` (internal/mcp/tools.go:764) gains **S9 code-rules coverage**:
  *when* the input spec declares rule IDs (section present), every declared
  `must` ID needs a non-empty satisfaction line. S9 is skipped entirely when
  the section is absent — keeps the tool backward-compatible for non-rule
  projects; the planner-side gate (S5/S6/S9 list in plan.xml) makes the
  section mandatory only when the goal carries rules. MCP change ⇒ ships
  behind one server restart (§6).

### 2.5 Implementation (execute.xml)
Step 1b ("Read project context") gains one rule: *if the task message or spec
cites Code rules, Read every cited file before writing code; `must` rules are
binding; deviating from a `should` requires a one-line justification in the
completion report.* Single XML edit; the report template gains an optional
`RULES:` line.

### 2.6 Validation (taskvisor investigators / investigate.xml)
No daemon change needed: the `convention-audit` investigator type already
exists in `allowedInvestigatorTypes` (tools_taskvisor.go:215) and the
generation-injected investigation_config flows through goal.md to the
investigate orchestrator automatically. Automated signal greps run as
pure-exit `command` investigators / validate lines (daemon's-eye — this is
what survives the solo lane, §5). investigate-worker.xml needs only a
classification note: convention-audit findings cite rule IDs so
goal-validation-done findings are traceable (`CR-<id>` ↔ finding).

### 2.7 Audit (plan.xml step 11a blind audit)
The audit sub-agent prompt adds one input: the resolved code-rules paths +
each audited task's claimed rule IDs. New scored dimension: **rule coverage**
— does every task whose files match a `must` rule's applies_to cite it (and
does the spec's §Code Rules satisfy it)? Plan-level enforcement (audits the
PLAN, not code). Solo-lane slim audit unchanged — it stays the daemon's-eye
validate evaluation, which already contains the injected signal greps.

### 2.8 Growth loop (local rules + ingestion + promotion)
- **`tmux-cli rules lint`** (new subcommand): runs the falsifiability
  contract (schema completeness, signal compiles + matches bad / not good,
  review-lines prefix, no borrowed-green sole checks) against `local/` (and
  optionally the materialized embedded packs). Closes the guard gap — local
  rules currently bypass the embedded-only Go selftest. Shares the contract
  implementation with `rules_catalogue_test.go` by moving the checker into
  `internal/rules/lint.go`.
- **`/tmux:rules:add`** (new skill, generic /previo:code-rules:add): distill
  review/MR feedback interactively into a schema-valid rule → write to
  `.tmux-cli/rules/local/code-rules/` → run `rules lint` → report. The MR
  comment is a ready-made `examples.bad` fixture; provenance lands in
  `origin:` (fine in a project-local file — the publish boundary only forbids
  it in EMBEDDED packs).
- **Promotion:** a local `scope: generic` rule proven in practice is PR'd into
  the embedded pack by hand — provenance stripped, `adapted_from` set. Listed
  here as policy, not tooling.

### 2.9 Brownfield diff gate — `tmux-cli rules check` (the previo:code-rules:goals analog)
`rules check [--diff <range>] [--json]`: filter resolved code-rules to changed
files via applies_to; run automated signals against those files; emit
applicable/violated per rule. Go does the deterministic half; agents handle
review-kind rules from the JSON. Powers (a) an optional execute.xml
self-check before completion reporting, (b) a future generic enforce-as-goals
flow. Independent of the greenfield pipeline — last phase.

## 3. What does NOT change
- Daemon goal lifecycle, lanes, retry budgets, convergence breakers — rules
  ride existing fields (acceptance/validate/investigation_config) only.
- Conventions (kind=convention) stay planner-only and binding; E2E concerns
  code-rules.
- previo2's own catalogue + commands — untouched; tmux-cli never reads
  `docs/ai/code-rules/` directly.

## 4. Phase 2, adapted — per-step `condition=` extraction

Survey of all `condition=` attributes across the 21 generation shards + spine
yields three classes:

- **Class A — capability/stack signals** (extract): `HAS_DATABASE`,
  `HAS_FRONTEND`, `RUN_TARGET=docker|local`, `SERVICES_EXTERNAL`,
  `Playwright available`, `N_auth_flows>=1`, `symfony/mailer|messenger in
  composer.json`, `uses_jwt`. Sources: test-environment.md, composer.json,
  cross-cutting.md security section (uses_jwt), discovery state.
- **Class B — discovery-content predicates** (extract the EVALUATION, not the
  branch): `is_cross_bc` / "cross-BC deps exist" (BC_LIST length + context
  maps), "acl_list non-empty", "BC has list/search actions", "domain services
  identified", CI/CD platform selected. Derivable from
  bounded-contexts.md / api-endpoints.md / cross-cutting.md.
- **Class C — generation control flow** (NEVER extract): goal-create error
  handling, ordering/cycle validation, audit re-entry.

**Adapted phase-2 thesis:** the original plan wanted to extract the
*branches*; that is still too entangled (the branch bodies ARE goal
generation). What extracts cleanly is (1) the **condition evaluation** —
Signals grows Class A + the cheap Class B predicates (`uses_jwt`,
`has_mailer`, `has_messenger`, `has_http_client`, `n_bounded_contexts`),
`Detect` parses them once, `resolve --signals` dumps them, and the XML
conditions become reads of a captured JSON instead of per-step re-derivation
prose; and (2) the **stack-specific instructional content** — step bodies
that restate what `ddd-conventions.md` + the PHP-ARCH/PERS rules now say
(domain module layout in 3.14, application shape in 3.15, persistence shape
in 3.16) dedup to rule-ID references, shrinking shards without behavior
change, guarded by the existing goal-content tests. Class B predicates that
need real inventory parsing (list/search actions per BC) stay in XML — the
agent already holds the parsed inventory; Go re-parsing markdown prose tables
for them buys determinism nobody consumes.

## 5. Enforcement matrix (severity × validate_kind × lane)

| rule kind        | full lane                                    | solo lane                          |
|------------------|----------------------------------------------|------------------------------------|
| must + automated | validate grep (daemon, fail-closed) + investigator command | validate grep (daemon) — survives slim gate |
| must + review    | CR-acceptance + convention-audit investigator (fail) + spec S9 | CR-acceptance + spec S9 (no investigator) |
| must + mixed     | both rows above                              | grep + acceptance + S9             |
| should (any)     | convention-audit finding (non-fatal) + spec apply/skip+why | spec apply/skip+why only           |

The solo-lane column is the honest one: review-kind enforcement there rests
on spec + implementer compliance, by design (solo lane trades gates for
cheapness). Automated signals are therefore the highest-value rules to grow.

## 6. Safety rails

1. **Vacuous-gate (four-surfaces lesson), stated precisely:** falsifiability
   of a signal is established by its lint fixtures (signal matches
   examples.bad, not examples.good — `rules lint`/embedded selftest), NOT by
   a plan-time project run — on greenfield the tree is empty and an
   absence-grep is expectedly, vacuously green. The plan-time baseline run
   therefore checks only RUNNABLE-NESS: the rendered command must exit 0 or
   1 (grep's no-match/match), never 2 (bad pattern/path class errors); a
   command erroring at baseline is dropped with a logged warning, never
   injected dead. Only signals with lint-proven fixtures are eligible for
   injection at all.
2. **Acceptance bloat cap:** `must`-only injection; broad-glob (`src/**`)
   rules ride the single per-goal convention-audit investigator unless phase
   matches. One investigator per goal for ALL review rules — the 2–4
   investigator budget (validateInvestigators) is not consumed per rule.
3. **MCP staleness:** SpecValidate S9 + buildTaskMessage CODE_RULES land in
   one release; running servers need the restart (known gotcha — make install
   does not refresh).
4. **Determinism boundary:** Go owns matching (globs, signals, severity
   routing); agents own judgment (review rules, spec satisfaction prose).
   Nothing in the daemon parses rule YAML — generation resolves everything
   into existing goal fields.
5. **No double enforcement:** a signal injected as a daemon validate line is
   NOT also an investigator command for the same goal (one channel per rule
   per goal, severity-routed per §5).

## 7. Implementation phasing

| phase | content | touches |
|-------|---------|---------|
| 2a | Stack line in discovery + Detect parser; Signals + Detect grow Class A/B-cheap; `resolve --signals` | task-plan-discover.xml, internal/rules, rules.go CLI |
| 2b | `rules match` CLI (internal/rules, shared globs/routing) + rule→goal injection (acceptance/validate/investigator incl. budget rule) + runnable-ness baseline | rules.go CLI, internal/rules, task-plan-generate.xml (+ relevant shards), generation tests |
| 2c | Consumption: buildTaskMessage CODE_RULES, spec §Code Rules + SpecValidate S9, execute.xml 1b, audit prompt input, investigate-worker note | tools.go, plan.xml, execute.xml, investigate-worker.xml + MCP tests |
| 2d | `rules lint` (checker → internal/rules/lint.go, shared with catalogue test), `/tmux:rules:add` skill | rules.go CLI, new command XML |
| 2e | `rules check` diff gate; opportunistic shard dedup vs ddd-conventions; XML conditions → signal-dump reads | rules.go CLI, generation shards |

2a+2b are the value core (rules actually gate goals). 2c makes the chain
honest end-to-end. 2d closes the local-rule guard gap and opens the growth
loop. 2e is brownfield + cleanup.

## 8. Open questions
1. **S9 strictness:** should spec-validate FAIL a spec missing the §Code
   Rules section when the goal carries rules, or only gap-warn (planner gate
   decides)? Leaning: gap entry + planner-side hard gate (keeps the MCP tool
   policy-free).
2. **should-rule waivers:** record skip justifications in the goal report
   only, or persist a waivers file per goal dir for the audit to read?
   Leaning: report-only until audits prove they need the file.
3. **`rules check` placement in execute.xml:** mandatory self-check before
   completion vs optional. Leaning: optional first release (worker time cost
   on every task is real; measure).
