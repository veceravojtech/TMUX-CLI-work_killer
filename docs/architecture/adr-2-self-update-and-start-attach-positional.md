# ADR-2: `tmux-cli self-update` command + `start-attach`/`start` positional project-path

- **Status:** Accepted
- **Date:** 2026-07-01
- **Bounded Context:** tmux-cli command layer (cmd/tmux-cli) + session/executor + taskvisor internals
- **Related ADRs:** none

## Context

tmux-cli is dogfooded to repair tmux-cli: a repair cycle can land a fix that changes the
running binary, and the flow must rebuild/reinstall itself and keep working. Three layers
adopt a rebuilt binary differently — the daemon (already auto-adopts via `checkStaleBinary`
→ exec-replace + crash-recovery Pass-1), the embedded command templates (`refreshCommands`
on the stale path), and a running orchestrator/supervisor **Claude** (whose MCP tools are
frozen at process startup and only refresh on a Claude restart). The only genuinely-missing
capability is a controlled way to (a) trigger the rebuild+install from inside the flow and
(b) restart the Claude layer while preserving continuity. Binding spec:
`docs/architecture/self-update-design.md`. Additionally, `start-attach`/`start` resolve the
project only from `os.Getwd()`, so `make install && tmux-cli start-attach $PROJECT` cannot
target a project from a different cwd (the self-update / dispatcher case).

Stage-1 dossier confirmed: daemon-side adoption is automatic once `make install` replaces the
installed binary (`internal/setup/buildstamp.go` `BinaryStale` mtime/size vs a startup stamp;
`internal/taskvisor/statemachine.go` `checkStaleBinary`→`restartStaleBinary`, ≤~70s); the
only net-new plumbing for a Claude restart is a window-preserving process interrupt; and
`install.sh` is a release-tarball downloader (NOT a source build) — `make install` is the
correct rebuild path.

### Decision Drivers
- Reuse over re-implementation: the daemon exec-replace + Pass-1 resume already exists; do not duplicate it.
- MCP tools freeze at Claude startup → restart granularity must be selectable (daemon | claude | session).
- TDD + no-tmux-server test mandate (AGENTS.md) → executor-interface do-core for mock-testability.
- Portability/safety: never build the target project; never use install.sh; adoption only fires when the daemon runs the installed binary.
- Backward compatibility: the positional arg defaults to `os.Getwd()`; existing invocations are unchanged.

## Decision

Adopt **Option 1** — an own-file command with an executor-injected do-core, reusing the
existing restart primitives.

- **New command** `cmd/tmux-cli/self_update.go` (+ `_test.go`) with its own `func init()`
  (modeled on `e2e.go`), a thin `runSelfUpdate` RunE, and a mock-testable
  `doSelfUpdate(cfg, executor)` core (modeled on `runTaskvisorRestart`→`doTaskvisorRestart`).
- **`start`/`start-attach`** gain `Args: cobra.MaximumNArgs(1)` + a `--resume-state` flag,
  sharing one `resolveProjectPath(args)` helper (`filepath.Abs`+`EvalSymlinks`, per
  `runProjectInit`); the positional and its cwd-default branch MUST share this one resolver.
- **Restart limbs:** `daemon` (default) writes the `.tmux-cli/taskvisor-restart` marker and
  lets `checkStaleBinary` exec-replace (already automatic); `claude` reuses
  `ExecutePostCommandWithFallback` + one new window-preserving `InterruptWindow` (C-c)
  executor method; `session` uses `stopDaemonProcess` + kill-session + `start-attach
  --resume-state` relaunch (refuses without a handoff file).
- **Source resolution:** `--source` → `TMUX_CLI_SRC` (recorded at session-create mirroring
  `--model`) → `setting.yaml self_update.source_dir` (non-surfaced, to avoid the TUI-parity
  test). Rebuild via `make install`; never `install.sh`; refuse if resolved source == target
  project; warn when the daemon's `os.Executable()` != INSTALL_PATH (worktree/dev launch →
  adoption won't fire). Success verified by the installed binary's mtime/size changing.
- **Deferred:** `--nudge` (immediate stale pickup) — no cross-process hook exists today; the
  ≤~70s auto-detect suffices for the first cut.

Naming manifest (frozen — Stages 3–4 use verbatim):

| Role | Name | Mirrors | Target |
|---|---|---|---|
| cobra command var | `selfUpdateCmd` | `taskvisorRestartCmd`/`e2eBootstrapCmd` | cmd/tmux-cli/self_update.go |
| RunE runner | `runSelfUpdate` | `runTaskvisorRestart` | cmd/tmux-cli/self_update.go |
| testable core | `doSelfUpdate` | `doTaskvisorRestart` | cmd/tmux-cli/self_update.go |
| restart-mode type+consts | `restartMode` / `restartDaemon`,`restartClaude`,`restartSession`,`restartAuto` | existing string consts | cmd/tmux-cli/self_update.go |
| source-dir resolver | `resolveSourceDir` | `taskvisorProjectRoot` | cmd/tmux-cli/self_update.go |
| rebuild+install step | `rebuildAndInstall` | net-new (wraps `make install`) | cmd/tmux-cli/self_update.go |
| installed-binary verify | `binaryChanged` | reuses setup buildstamp | cmd/tmux-cli/self_update.go |
| window-preserving interrupt | `InterruptWindow(winID)` | `KillWindow`/`SendMessage` | internal/tmux/executor.go (+ real_executor.go, mock) |
| positional resolve helper | `resolveProjectPath(args)` | `runProjectInit` | cmd/tmux-cli/session.go |
| source session-env setter | `WithSource` / `SetSessionEnvironment(id,"TMUX_CLI_SRC",…)` | `WithModel` | internal/session/manager.go |
| resume-state kickoff | `sendResumeKickoff` | net-new (pre-AttachSession SendMessage) | cmd/tmux-cli/session.go |
| [test] self-update | `TestDoSelfUpdate_*` | — | cmd/tmux-cli/self_update_test.go |
| [test] positional arg | `TestResolveProjectPath_*`/`TestRunStartAttach_Positional` | — | cmd/tmux-cli/session_test.go |
| [test] interrupt method | `TestInterruptWindow_*` | — | internal/tmux/real_executor_test.go |

Test strategy: tests required (test-first deliverable; all non-test gates — `make build`
= check-file-lengths/fmt/vet, `go test ./...` — still enforced).

## Consequences

### Positive
- Delivers all three restart modes the real consumers (repair-cycle `self-reinstall` phase + e2e-evaluator handoff) will need.
- Reuses the proven `taskvisor-restart` + `checkStaleBinary` machinery; minimal net-new surface (one executor method + one config field).
- Backward-compatible positional; `make install && tmux-cli start-attach $PROJECT` becomes expressible.

### Negative
- One net-new executor method (`InterruptWindow`) across interface + real + mock.
- `self_update.source_dir` is a non-surfaced config field (deliberately kept out of the TUI to avoid the parity test).

### Neutral
- `--nudge` deferred; daemon adoption relies on the existing ≤~70s auto-detect for now.
- `session` restart mode refuses without a `--resume-state` handoff file (by design).

## Alternatives Considered

### Option 2 — daemon-mode-only self-update + start-attach arg
- Description: `make install` + marker + binary-changed verify; no claude/session limbs, no `InterruptWindow`.
- Pros: smallest diff; leans entirely on daemon auto-adopt.
- Cons: cannot refresh an orchestrator Claude's frozen MCP tools (the e2e-evaluator case).
- Reason rejected: blocks the design §6 forward hooks until claude/session modes exist anyway.

### Option 3 — full design now (incl. `--nudge` marker + surfaced source_dir)
- Description: Option 1 plus a net-new `taskvisor-nudge-stale` poll-loop marker and a TUI-surfaced `self_update.source_dir`.
- Pros: immediate stale pickup; source dir configurable in the TUI.
- Cons: largest surface + TUI-parity test burden for a latency-only optimization.
- Reason rejected: `--nudge` is net-new and merely optimizes latency over the existing ≤~70s auto-detect; not worth the surface in the first cut.
