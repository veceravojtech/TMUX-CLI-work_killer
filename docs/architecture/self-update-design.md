# Self-Update & Resumable Restart — Design

Status: **PARTIALLY IMPLEMENTED** — the `tmux-cli self-update` command and the
e2e-evaluator handoff (§6, 2026-07-02) are landed; the remaining §6 hooks
(repair-cycle `self-reinstall` phase, dispatcher local lane) are still design-only.
Scope of this doc: the two foundation
pieces — a positional argument for `start-attach`, and a new `tmux-cli self-update`
command. Downstream consumers (repair-cycle `self-reinstall` phase, e2e-evaluator
state handoff, dispatcher local lane) are sketched at the end as forward hooks only.

## 1. Problem

The tooling must be able to rebuild and reinstall *itself* mid-flow and keep working,
because tmux-cli is dogfooded to repair tmux-cli. Three things change when the binary
is replaced, and they adopt the new binary differently:

| Layer | What must happen | Already handled? |
|---|---|---|
| Daemon (`taskvisor --run`) | Run the new daemon code | **Yes** — `checkStaleBinary` → exec-replace + Pass-1 resume |
| Embedded command templates (`.claude/commands/tmux/*.xml`) | Re-emit from new binary's embedded FS | **Yes** — `refreshCommands()` on the stale path |
| A running orchestrator/supervisor **Claude** process | Re-freeze MCP tools against the new binary | **No** — MCP schemas are frozen at Claude startup |

So the *only* genuinely-missing capability is a controlled way to (a) trigger the
rebuild+install from inside the flow, and (b) restart the **Claude** layer while
preserving continuity. Everything daemon-side already exists.

## 2. The hard constraint (drives every decision)

MCP tools are frozen into a Claude process at startup. `make install` swaps the binary
and the MCP server, but a *running* orchestrator Claude keeps the stale in-process tool
schemas. Adoption therefore has three restart granularities, cheapest first:

- **(A) Daemon-only** — the daemon exec-replaces itself. Already automatic on
  `BinaryStale()`. Keeps all windows; refreshes command templates. Does **not** refresh
  any Claude's MCP tools.
- **(B) Claude-process** — kill the `claude` in a window, relaunch via post-command
  `claude --resume "$TMUX_WINDOW_UUID"` (postcommand.go:47). Conversation preserved by
  Claude Code's native `--resume`; MCP re-freezes against the new binary.
- **(C) Full session** — `kill-session` + `start-attach`. Brand-new everything;
  in-Claude context is lost unless a **state file** bridges it. Required only when the
  Claude that needs the new binary is the orchestrator itself (e2e-evaluator case).

**Chosen policy: (A) by default, escalate to (B)/(C) only when a Claude actually needs
new MCP/command schemas.** `self-update` exposes the granularity as a flag and defaults
to the cheapest sufficient mode.

## 3. Piece 1 — `start-attach <project-path>` positional argument

### Current behavior
`runStartAttach` (cmd/tmux-cli/session.go:557) resolves the target from `os.Getwd()`
only. There is no positional argument; the session is keyed by
`TMUX_CLI_PROJECT_PATH = cwd`.

### New behavior
```
tmux-cli start-attach [project-path] [--clean] [--sudo] [--model M] [--resume-state FILE]
tmux-cli start        [project-path] [ ... same flags ... ]
```
- `project-path` (optional positional): the project to start/attach.
  - Absolute or relative; relative resolves against `os.Getwd()`.
  - Omitted → `os.Getwd()` (today's behavior, fully backward-compatible).
- Resolution rules:
  1. `filepath.Abs` + `EvalSymlinks`.
  2. Must exist and be a directory → else non-zero exit with a clear message.
  3. The resolved path becomes `TMUX_CLI_PROJECT_PATH` and the session working dir,
     exactly as the cwd does today. Session identity (find-or-reuse by
     `TMUX_CLI_PROJECT_PATH`) is unchanged — a given project path maps to one session
     regardless of the caller's cwd.
- `--resume-state FILE` (new, optional): path to a handoff artifact
  (see §4.4). When present, after attach the kickoff prompt sent to the orchestrator
  window instructs it to read FILE and continue. This is what makes
  `make install && tmux-cli start-attach <path> --resume-state <file>` a resumable
  relaunch rather than a cold start.

### Why a positional arg is needed
Enables the flow `make install && tmux-cli start-attach $PROJECT` from a directory that
is *not* the target project (e.g. a self-update invoked from the CLI source checkout, or
the dispatcher launching an arbitrary lane). Today that would silently target the wrong
directory.

### Non-goals
- No change to session-identity semantics, `--clean`, `--sudo`, `--model`.
- No implicit auto-`--resume-state` — resume is always explicit.

## 4. Piece 2 — `tmux-cli self-update`

### 4.1 Signature
```
tmux-cli self-update
    [--source DIR]                 # CLI source checkout to build from
    [--restart daemon|claude|session|auto]   # default: auto
    [--project PATH]               # target project session (default: cwd)
    [--resume-state FILE]          # handoff artifact for claude/session restart
    [--build-cmd "make install"]   # override the rebuild+install command
    [--nudge]                      # force an immediate daemon stale-check
    [--dry-run]
```

### 4.2 Source resolution (`--source`)
`make install` must run from the CLI source tree, not the target project. Order:
1. `--source DIR` if given.
2. `TMUX_CLI_SRC` env (recorded in the tmux session env at `start` time — small addition
   to session creation so a self-update always knows where to build from).
3. A configured path in `setting.yaml` (`self_update.source_dir`).
4. Fail with a clear message (never guess / never build the target project).

### 4.3 Algorithm
```
1. Resolve source dir (§4.2); verify it is a git checkout containing the Makefile.
2. Record pre-state: installed binary path (os.Executable of the installed tmux-cli),
   its mtime/size, and `git rev-parse HEAD` of the source.
3. Run build-cmd (`make install`) in source dir.
   - On non-zero exit: abort, emit JSON {ok:false, stage:"build", ...}, change nothing.
4. Verify the installed binary actually changed (mtime/size differ from step 2), else
   warn "no-op: binary unchanged" (idempotent; still succeeds).
5. Choose effective restart mode:
     auto  -> if a diff touched MCP/command surfaces AND a Claude in this session must
              use them -> claude ; else -> daemon.
     (auto's default floor is `daemon`, because daemon adoption is automatic anyway.)
6. Apply restart mode:
   - daemon:  write .tmux-cli/taskvisor-restart marker (parity with checkStaleBinary),
              optionally --nudge: reset the daemon's lastStaleCheck throttle so the next
              tick exec-replaces immediately instead of waiting ≤60s. No further action;
              the daemon's own checkStaleBinary + Pass-1 recovery does the rest.
   - claude:  for each target window (supervisor and/or a named orchestrator):
                a. ensure conversation is checkpointed (Claude Code autosaves per UUID);
                b. kill the claude process in the window;
                c. re-run post-command → `claude --resume "$TMUX_WINDOW_UUID"`;
                d. if --resume-state, send a two-step kickoff: "read FILE, continue".
              Also performs the daemon step (new daemon binary + new MCP server).
   - session: `tmux-cli e2e-teardown`-style ordered reap is NOT used here; instead
                kill-session + `start-attach <project> --resume-state FILE`. Only valid
                when FILE exists (else refuse — a session restart with no handoff loses
                all context).
7. Emit one JSON line:
   {ok, stage, mode, source_head_before, source_head_after,
    binary_changed, restarted:[windows], resume_state, ts}.
```

### 4.4 Handoff artifact (`--resume-state FILE`)
Source of truth stays **JSON** (atomic write via temp + `rename(2)`, matching the
existing e2e state file), with a generated **Markdown rendering** alongside for humans.
`self-update` does not define the schema — it only *carries* the path into the relaunch
kickoff. Producers (e2e-evaluator, repair phase) own the schema. Minimum contract:
the file is self-describing enough that a freshly-launched Claude reading it knows
"what cycle/goal am I on and what is the next action."

### 4.5 Safety / idempotency
- **Never** builds the target project; only the resolved CLI source.
- Build failure is fully non-destructive (no marker, no restart).
- `daemon` mode is safe to call repeatedly — the marker + stale-check are idempotent,
  and `restartAttempted`/`restartStaleBinary` already guard against double exec-replace.
- `session` mode refuses without a handoff file.
- Concurrency: take the existing `goals.yaml.lock` (or a dedicated `self-update.lock`)
  so a self-update can't race a dispatch tick mid-install.

### 4.6 What self-update deliberately does NOT do
- It does not re-implement daemon adoption — it leans entirely on `checkStaleBinary`.
- It does not pull git (the fix is assumed already committed/landed; pulling is a
  separate concern owned by the caller/dispatcher).
- It does not decide *whether* a fix warrants reinstall — the caller decides and picks
  the restart mode.

## 5. How the two pieces compose

`make install && tmux-cli start-attach $PROJECT` (the literal request) is expressible two ways:

- **Loose form (today's shell):** `make install` in the source, then
  `tmux-cli start-attach $PROJECT` — works once §3 lands, but is a *cold* start (no
  resume) and relaunches from scratch.
- **Managed form (recommended):** `tmux-cli self-update --project $PROJECT
  --restart auto` — same effect, but chooses the cheapest restart, preserves the daemon
  and (in claude mode) the conversation, is idempotent, and reports structured JSON.

The managed form is what the daemon and e2e-evaluator call; the loose form remains
available for humans.

## 6. Forward hooks (design-later, not in this scope)

- **Repair-cycle `self-reinstall` phase.** When a repair goal's deliverable touches
  `cli/**`, the daemon inserts a `self-reinstall` step before the next validation that
  invokes `self-update --restart daemon`. Because daemon adoption + Pass-1 resume already
  exist, this phase is thin: rebuild, mark, let the next tick exec-replace, resume the
  goal in-place. This is the "plan self-reinstall, continue after restart" requirement.
- **e2e-evaluator handoff — IMPLEMENTED (2026-07-02).** No longer design-later. On a
  resolved defect task, e2e-evaluator.xml step 7b records the pending verification via
  `e2e-state record --verify-signature/--verify-task-id` (the ledger's `verify` field;
  every e2e-state write also renders `<scenario>.state.md` alongside the JSON), consults
  the `e2e-state mark-self-update` restart-loop guard (`last_self_update` stamp; a repeat
  task-id is refused → one session restart per resolved task), then runs
  `self-update --restart session --resume-state .tmux-cli/e2e-evaluator/<scenario>.state.md`
  (skipping the restart on guard refusal or `binary_changed:false`). On relaunch,
  `e2e-bootstrap --resume` surfaces `verify_signature`/`verify_task_id` in its JSON so
  the conductor runs the confirm-fix cycle — removing the dependency on the external
  dispatcher to rebuild the binary.
- **Dispatcher local lane.** Have the dispatcher's local lane call `self-update` instead
  of its inline self-build, unifying local and remote adoption on one primitive.

## 7. Open questions

1. `--source` discovery: is `TMUX_CLI_SRC` in session env acceptable, or should
   `self_update.source_dir` in `setting.yaml` be the single source of truth?
2. `auto` mode's "does the diff touch MCP/command surfaces" test — path-glob heuristic
   (`internal/mcp/**`, `cmd/tmux-cli/embedded/**`) vs. always escalate to `claude` when
   any orchestrator window exists. Heuristic is cheaper but can under-restart.
3. Should `session` mode reuse the e2e-teardown ordered reap for cleanliness, or a
   lighter kill-session (faster, but leaves worktrees/compose to the next bootstrap)?
