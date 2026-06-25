# Plan Approval — Per-worktree compose stack (task 275 fix)

- **Verdict:** PASS
- **Score:** 100 / 100
- **Timestamp:** 2026-06-25T00:00:00 (audit run)
- **Auditor:** blind read-only plan auditor (no planning conversation; on-disk artifacts + real source tree only)
- **Plan:** `.tmux-cli/tasks.yaml` (status: ready) — execute-1 (T1 mechanism), execute-2 (T2 lifecycle wiring, depends_on execute-1)

## Scope audited
- `.tmux-cli/tasks.yaml`
- `.tmux-cli/research/2026-06-24-23/execute-1-per-worktree-compose-mechanism.md`
- `.tmux-cli/research/2026-06-24-23/execute-2-wire-worktree-stack-lifecycle.md`
- `.tmux-cli/research/2026-06-24-23/task-worktree-compose-mechanism.md`, `task-worktree-stack-lifecycle.md`, `code-rules.md`
- Real tree: `internal/taskvisor/{execruntime,wrapcmd,worktree,dispatch,daemon,statemachine,elaboration}.go`

## Per-dimension summary

1. **validate-executability** — PASS. T1/T2 Verification commands are bare and runnable: `go test ... -run '<regex>' [-race]`, `go build ./...`, `grep -n/-F`, `docker compose -p taskvisor-goal-015 ps`, `docker volume ls`. No shell-wrapper chains.
2. **dependency-correctness** — PASS. The execute-2 → execute-1 edge is a genuine produce/consume: T2 consumes `WorktreeComposeProject(goalID)`, `ComposeStack.Up/Down`, and the T1-defined `composeRunnerFn` type (bring-up, teardown, validate retarget, daemon field). No cycle, no dangling ref.
3. **runtime-state-gating** — PASS. Bring-up/db-lock gate on live `goalUsesWorktree` (verified `worktree.go:991`); teardown keys purely on `WorktreeComposeProject(goalID)` derivable from goalID alone (crash-safe, explicitly stated). No assertion against state absent at implementation time.
4. **host-container-split** — PASS. Port-strip override (`!reset []`) removes host `ports:` for app/db; validate execs over the internal compose network (`exec -T app`, `db:5432`); per-worktree named `db-data` volume isolates the DB and `down -v` reaps it. Collision/isolation handling is sound. (Docker Compose v5.1.4 present → `!reset` supported; spec also documents an `!override []` fallback.)
5. **objective-acceptance** — PASS. Acceptance Criteria are objective Given/When/Then and independently testable, anchored on the concrete goal-015 `/api/register` debug:router exit-0 → done → dependents-dispatch chain.
6. **spec-discovery-consistency** — PASS. Every Code Map file:line ref resolves to the real symbol (line numbers within 1–2): `resolveComposeProject`@59, `normalizeComposeName`@116, `composeProjectFromDocumentedField`@74, `dockerExec`@49, `worktreeBranch`@120/`worktreePath`@133, `ensureWorktree`@296, `discardWorktree`@797 (guard@799, remove@818, clear@825), `finalizeWorktreeOnDone`@881 (discard@895/898/935, BLOCK-skip@981, failed@1068), `pruneOrphanWorktrees`@835, `ScriptRunnerFunc`@20/`defaultScriptRunner`@52/`scriptRunnerFn`@161/@111, `goalWorkDir`@530, `pruneOrphanWorktrees` call@614, `ensureWorktree` sites dispatch@373/@538 + elaboration@58, `runValidateScript`@76 (cwd@96/env@97/db-lock@110), `regenerateValidateScript`@238/@240, statemachine `runValidateScript`@401/@544.
7. **environment-prerequisites** — PASS. All cited helpers/seams real: `ScriptRunnerFunc`/`scriptRunnerFn`/`defaultScriptRunner`, `dockerExec`+`shSingleQuote`, `ensureWorktree`, `discardWorktree`, `pruneOrphanWorktrees`, `safeToRemoveWorktree`, `withDBLock`, `regenerateValidateScript`, `goalUsesWorktree`, `gitRunnerFn`, `scriptReasonRunnerMissing`/`scriptReasonLockError`. Confirmed: no existing `compose up/down` anywhere in `internal/taskvisor` — T1 genuinely adds that responsibility (matches prior-learning).
8. **scope-sanity** — PASS. T1 CREATEs only `composestack.go` + `composestack_test.go` (its Code Map refs to existing files are all "CHANGE: none"); `composestack.go` does not yet exist. T2 modifies dispatch/worktree/daemon/elaboration. No write-collision (T1 writes no file T2 edits, and vice versa). Footprint contained.
9. **rule-coverage** — PASS. `code-rules.md` reports no `must` rules match the Go daemon files; neither spec carries a `## Code Rules` section — correct.

## Minor findings (no deduction)
- None material. Both specs explicitly handle the one version-sensitive surface (`!reset` vs `!override`) and the worst-case `down -v`-nukes-base risk (taskvisor- prefix guard, asserted in T1 TC-9). Teardown DRY-placement inside `discardWorktree` correctly inherits the needs-merge BLOCK preservation at `worktree.go:981` for free.

No open SEV-1/SEV-2 (or SEV-3). Score 100 ≥ 90 → PASS.
