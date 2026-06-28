# Plan brief — Per-worktree compose stack for goal validation (fixes task 275)

## Problem
The daemon's validator commands exec into the
MAIN compose stack (`docker compose -p <base-project> exec`), which bind-mounts **master**,
not the goal's git worktree. Worktree-isolated goals therefore validate against code that
isn't in the container → exit 1 every cycle → validation-exhausted → cascade. (task 275,
goal-015: `/api/register` written into `.tmux-cli-worktrees/goal-015/`, invisible to the
master-mounted `productivitytool-app-1`.)

The LLM validator and its verdict authority are KEPT — only
the runtime the validator commands exec into is fixed.

## Decision (locked by the operator)
**Path B — per-worktree compose stack.** The daemon brings up a goal-scoped compose stack
that mounts the goal's worktree, execs validate into it, and **tears it down (`docker
compose down`) when the goal finishes.** No inline/on-master fallback is the target end
state (an optional setting toggle MAY remain as an escape hatch).

## Key facts (already true in the daemon — internal/taskvisor/)
- The daemon NEVER brings up a compose stack today; it assumes an operator-run main stack.
  This change ADDS stack lifecycle as a new daemon responsibility.
- The validator ALREADY runs with `cwd = the goal's
  worktree` (`d.goalWorkDir`) and exports `WORKTREE_DIR`. The ONLY defect is the `-p
  <base-project>` pin in `wrapcmd.dockerExec` + `resolveComposeProject` normalizing
  worktree→base (execruntime.go:51-68).
- Worktree lifecycle seams already exist: `ensureWorktree` (worktree.go:296, create),
  `mergeWorktreeBack` (536, done), `discardWorktree` (797, failed),
  `pruneOrphanWorktrees`/`crashRecovery` (orphan cleanup).
- Per-worktree port allocation already has a hook: elaborate.xml step 4 port-occupancy
  writes a free host port into the worktree compose when a host-reachable port is needed.

## Lifecycle to implement
1. **Up** at dispatch (in/after `ensureWorktree`): `docker compose -p taskvisor-<goalID>
   up -d` with cwd = worktree, so the compose file's bind mounts resolve to the worktree.
2. **Exec** during validate: `dockerExec` targets `-p taskvisor-<goalID>` (worktree-aware
   ComposeProject), not the base project.
3. **Down** at goal finish: `docker compose -p taskvisor-<goalID> down -v` in
   `mergeWorktreeBack` AND `discardWorktree`; orphan `taskvisor-*` stacks reaped in
   crash-recovery / orphan-prune. `-v` discards the ephemeral per-worktree DB volume.

## Open questions for the spec workers (recommended resolutions — confirm/adjust)
- **Target bind-mount style (MUST investigate productivityTool's compose).** If the main
  compose uses RELATIVE bind mounts (`.:/app`), `up -d` from the worktree cwd mounts the
  worktree for free. If ABSOLUTE, generate a `docker-compose.override.yml` in the worktree
  pointing the bind at the worktree path (or set `COMPOSE_FILE`). → spec worker reads
  `/home/console/PhpstormProjects/productivityTool` compose + `.env`.
- **Ports.** Recommended: per-worktree stacks publish NO host ports for toolchain validate
  (exec uses the internal compose network). Only E2E browser-reachable ports get a
  free-port allocation (reuse the existing port-occupancy hook). Avoids collision with the
  main stack and between concurrent worktrees.
- **DB.** Recommended: per-worktree project gets its OWN ephemeral DB volume (compose
  prefixes volume names by project), migrated at bring-up; discarded on `down -v`. This
  ALSO removes the shared-schema db-lock the current validate path holds (dispatch.go:99) —
  per-worktree DBs are isolated, so the lock can be dropped for worktree mode.
- **Where bring-up lives.** Recommended: DAEMON owns up/down (deterministic anchor controls
  its own runtime); the worker's step-4c gate reuses the already-up stack.
- **ResolveExecRuntime.** Add a goal/worktree-aware ComposeProject = `taskvisor-<goalID>`;
  keep base-normalized name only for the no-worktree path. Threading:
  createValidatorAndSendPayload uses the validate-cwd routing (daemon.go:517) — it needs
  the worktree-aware project.

## Scope / files (the fix lands in THIS cli repo, daemon layer)
- `internal/taskvisor/execruntime.go` — worktree-aware ComposeProject resolution.
- `internal/taskvisor/wrapcmd.go` — dockerExec already takes `project`; feed it the
  worktree project.
- `internal/taskvisor/worktree.go` — stack up at create, down at merge/discard, orphan reap.
- `internal/taskvisor/dispatch.go` / `statemachine.go` — wire bring-up before validate;
  drop the shared-schema db-lock on the worktree path.
- New: a small compose-runner helper (mirrors `scriptRunnerFn` injection for testability).
- Investigate-only: `productivityTool` compose + `.env` (target bind-mount/port/DB shape).
- Optional: `setting.yaml` `taskvisor.validation_mode` escape hatch (inline-on-master).

## Acceptance
- goal-015 re-run in worktree mode: inside the validate container `bin/console debug:router
  | grep -F "/api/register"` exits 0 → the validator passes → `goal-015: running -> done`
  → dependents 016-021 dispatch (no cascade).
- After the goal reaches a terminal state, `docker compose -p taskvisor-goal-015 ps` shows
  no services (stack down) and the per-worktree DB volume is gone.
- `max_goals>1`: two concurrent goals' stacks don't collide on ports/containers/DB.
- The LLM validator's verdict authority and F1/F2 elaborate.xml behavior are
  unchanged; existing daemon test suites stay green.

## Operator convention — Stack Baseline (per-worktree migrate)

The fresh per-worktree `db-data` volume is empty on every `up`. To migrate it to a
usable baseline **before** the validator's commands touch it, a managed
project opts in by declaring a **`Stack Baseline:`** (alias `Baseline Command:`)
field in its `docs/architecture/test-environment.md` — e.g.
`**Stack Baseline:** bin/console doctrine:migrations:migrate -n`. The daemon reads
it via `stackBaselineCmd` (`internal/taskvisor/dispatch.go`) /
`resolveBaselineCmd` (`internal/taskvisor/composestack.go:126`) and runs it via
`exec -T <appSvc> sh -c <cmd>` after `up -d`; an empty/absent field SKIPS the
migrate step (opt-in, project-agnostic). Full field syntax, the exact parse
contract, and set-vs-unset behavior are documented in
[`../architecture/stack-baseline-convention.md`](../architecture/stack-baseline-convention.md).
