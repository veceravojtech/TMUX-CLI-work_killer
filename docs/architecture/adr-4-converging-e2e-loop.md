# ADR-4: Converging E2E Loop — batch-verify, batch self-update guard, followup handoff, and step-1b/7b rewiring

- **Status:** Accepted
- **Date:** 2026-07-05
- **Bounded Context:** internal/e2e (pure run-state/report logic) + cmd/tmux-cli (e2e-state cobra adapter) + the embedded e2e-evaluator command spec
- **Related ADRs:** adr-2-self-update-and-start-attach-positional (the self-update/start-attach resume-state seam this loop drives)

## Context

`/tmux:e2e-evaluator` already files defects on FAIL, monitors the backend ledger,
and can self-upgrade via `tmux-cli self-update --restart session --resume-state`.
But the loop converges ONE task at a time: the state ledger's `verify` field
(`State.Verify *VerifyState`, `internal/e2e/e2e.go:74`) holds a SINGLE
`{signature, task_id}`, and a failing cycle routinely files SEVERAL defects
(e.g. resolved tasks 386/387/388/389 filed minutes apart). One restart +
verification cycle per task means N rebuilds and N e2e reruns where one would do,
and the `/clear`-then-rerun boundary loses dedup memory and timing baselines.

This ADR records the architecture for making a multi-defect cycle converge in a
single pass: batch the pending verifications, guard one restart per batch, carry
a lossless followup handoff across the fresh-context restart, and rewire the
command spec's step-1b/7b accordingly.

### Decision Drivers

- **Prior art is mature and tested** — the `e2e-state report` triplet (typed
  struct → pure clock/fs-free `RenderCycleReport` → `writeFileAtomic` temp+rename
  → read-only ledger cross-check → one-JSON-line cobra wrapper) is the exact
  deterministic-writer shape the followup needs. Mirror it; do not invent a new
  writer.
- **`internal/e2e` is pure by charter** (`e2e.go:6-11`: inputs in, values out, no
  exec/clock) — the CLI layer injects the clock. Batch-hash + render logic belong
  in `internal/e2e`; the cobra layer stays a thin parse-delegate-print shell.
- **TDD is mandatory** (AGENTS.md:149) — every symbol lands test-first.
- **The 2000-line file-length gate is real** — prefer a new
  `internal/e2e/followup.go` over inflating `report.go`/`e2e.go`.
- **Edit the EMBEDDED command spec** (AGENTS.md:163) —
  `cmd/tmux-cli/embedded/commands/tmux/e2e-evaluator.xml`, never the auto-generated
  `.claude/commands/tmux/` copy; `TestEmbeddedCommands_ReferenceErrorReporting`
  must stay green.
- **design-doc §6 is stale** (no `verify` field) — the authoritative spec is code
  doc-comments + the XML glossary (`e2e.go:71-85`, `e2e-evaluator.xml:35`/`:220-221`).
- **The verify single→array migration is a 6-site lock-step change** — a partial
  migration compiles but silently drops verifications.

## Decision

Adopt **Option 1**: a new pure module `internal/e2e/followup.go` mirroring the
`report.go` quartet, a thin `e2e-state followup` cobra wrapper mirroring
`runE2EStateReport`, the `verify`→`[]VerifyState` 6-site lock-step widening, a
batch self-update guard keyed on a sorted-set hash, a `State.Followup` link field,
a `followup-*` fresh-run sweep matcher, and the step-1b/7b embedded-XML rewiring.

**Style mandate:** preserve the existing `e2e-state` style even where imperfect
(pointer-for-optional, `failE2EState` sentinel, duplicated app-up enum checks) —
mirror the exemplar, do not "improve" it; the resolved gates still bind.

**Test strategy:** tests required (test-first deliverable) — all non-test gates
also enforced.

**Authorization seam:** N/A — a local, machine-written state ledger with no
tenant/user data-exposure surface.

### Final approved naming manifest (frozen — Stages 3–4 use verbatim)

| DDD role | Proposed name | Exemplar counterpart | Target path |
|---|---|---|---|
| ledger field (widen) | `State.Verify []VerifyState` | `State.Verify *VerifyState` | internal/e2e/e2e.go |
| ledger field (new link) | `State.Followup string` json `followup,omitempty` | `State.Verify` optional field | internal/e2e/e2e.go |
| batch-key helper | `VerifyBatchKey(taskIDs []string) string` (sorted-set hash) | net-new | internal/e2e/e2e.go |
| render struct | `Followup{Scenario,Cycle,Verify,SignaturesSeen,TimingBaselines,CyclesSpent,MaxCycles,NextAction}` | `CycleReport` | internal/e2e/followup.go |
| pure renderer | `RenderFollowup(f Followup) string` | `RenderCycleReport` | internal/e2e/followup.go |
| validator | `ValidateFollowup(f Followup) error` | `ValidateCycleReport` | internal/e2e/followup.go |
| path authority | `FollowupPath(repoRoot, scenario, cycle)` → `followup-%s-cycle-%d.md` | `ReportFilePath` | internal/e2e/followup.go |
| fresh-run sweep matcher | `IsScenarioFollowup(name, scenario) bool` | `IsScenarioReport` | internal/e2e/followup.go |
| cobra command | `e2eStateFollowupCmd` | `e2eStateReportCmd` | cmd/tmux-cli/e2e_state.go |
| cobra run wrapper | `runE2EStateFollowup` | `runE2EStateReport` | cmd/tmux-cli/e2e_state.go |
| cobra flag parser | `parseE2EFollowupFlags` | `parseE2EReportFlags` | cmd/tmux-cli/e2e_state.go |
| cobra ledger op | `e2eStateFollowup(repoRoot, f)` | `e2eStateReport` | cmd/tmux-cli/e2e_state.go |
| [test] verify-array | extend `e2e_verify_test.go` (multi-element round-trip + batch RenderStateMD) | — | internal/e2e |
| [test] followup | `followup_test.go` (byte-stable render, path authority, no-tmp) | report_test.go | internal/e2e |
| [test] cobra followup | `e2e_state_followup_test.go` | e2e_state_report_test.go | cmd/tmux-cli |
| [test] batch guard | extend `e2e_state_test.go` (refuse same batch, allow superset/different) | — | cmd/tmux-cli |

### Goal sequence (strict dependency order — Stage 4 emits)

- **G1** — `verify`→`[]VerifyState` 6-site lock-step (schema, `RecordCycleOutcome`,
  `parseE2EVerifyFlags`→`StringArrayVar` zip, `BootstrapResult`, `readLedgerVerify`,
  `RenderStateMD`), test-first. Scope: `internal/e2e/**`, `cmd/tmux-cli/e2e_state.go`, `cmd/tmux-cli/e2e.go`.
- **G2** — batch self-update guard: `VerifyBatchKey` + `MarkSelfUpdate` keyed on the
  batch hash; `--task-id`→repeatable. depends_on G1. Scope: `internal/e2e/e2e.go`, `cmd/tmux-cli/e2e_state.go`.
- **G3** — followup module + `e2e-state followup` command + `State.Followup` link in
  `RenderStateMD` + `IsScenarioFollowup` sweep hook in `clearRunArtifacts`. depends_on G1.
  Scope: `internal/e2e/followup.go`, `internal/e2e/e2e.go`, `cmd/tmux-cli/e2e_state.go`, `cmd/tmux-cli/e2e.go`.
- **G4** — embedded-XML step-1b (`:84-96`, fix-verification `:95`) + step-7b
  (`:192-209`: batch record `:195`, batch guard `:196`, followup-write-before-restart,
  whole-batch monitor gate `:193`, escalate-on-first-failed/denied `:200-202`,
  continue-loop-after-clear `:198`). depends_on G1+G2+G3. Scope: `cmd/tmux-cli/embedded/commands/tmux/e2e-evaluator.xml`.

## Consequences

### Positive

- A multi-defect cycle converges in ONE restart + ONE verification pass instead of N.
- The followup handoff makes the fresh-context restart lossless (dedup memory +
  timing baselines survive the `/clear`).
- Mirrors tested prior art → low regression risk; each writer stays cohesive and
  under the file-length gate.

### Negative

- The `verify`→array widening is a breaking 6-site change; source + all
  `internal/e2e/*_test.go` and the CLI parse/bootstrap seams must move in lock-step
  (contained in G1 so the build never goes red mid-flight).
- Adds net-new surface (`followup.go`, a new subcommand) and a new fresh-run sweep
  path that must not orphan `followup-*` files.

### Neutral

- design-doc §6 stays stale unless a doc backfill goal is dispatched (optional,
  out of the core goal set).
- `e2eStateResult` is unchanged (`Path` is already `omitempty`); `record`/`mark`
  stdout stays byte-identical.

## Alternatives Considered

### Option 2 — followup renderer inside report.go
- Description: put `RenderFollowup`/`ValidateFollowup`/`FollowupPath` in the existing
  `internal/e2e/report.go` instead of a new `followup.go`.
- Pros: fewer files.
- Cons: grows `report.go` (118→~230 LOC) and mixes two distinct writers in one file,
  eroding cohesion and nudging the file-length gate.
- Reason rejected: the new-file split matches the existing `report.go` precedent
  (one writer per file) and keeps every file comfortably under the 2000-line gate.
