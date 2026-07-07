# ADR-5: `--flag` passthrough on `start-attach`/`start` for universal Claude launch flags

- **Status:** Accepted
- **Date:** 2026-07-07
- **Bounded Context:** session / launch (`cmd/tmux-cli` CLI layer + `internal/session`)
- **Related ADRs:** adr-2-self-update-and-start-attach-positional

## Context

`tmux-cli start-attach` (and `start`) launches Claude in each window/worker via the
post-command fallback chain `claude --dangerously-skip-permissions [--model '…'] …`.
Today the only launch tuning knob is `--model`, which is recorded as the tmux
session-environment variable `TMUX_CLI_MODEL` and read back by the separate later
processes that spawn windows/workers (MCP worker spawn, the `windows-create` handler,
the taskvisor daemon window). There is no way to pass an arbitrary universal Claude CLI
flag (e.g. `--chrome`) through to those launches.

This feature adds a repeatable `--flag` option that threads arbitrary Claude flags into
every launch, plumbed exactly like `--model`. `--flag="--chrome"` yields
`claude --dangerously-skip-permissions --chrome …`.

### Decision Drivers
- **Prior art / exemplar:** the `--model` option is a clean, fully-traceable sibling —
  flag → `TMUX_CLI_MODEL` session-env → `SessionManager.WithModel` → `PostCommandConfigWithModel`
  → `claudeLaunchCommands` → reuse applier + 3 read-back sites. Mirror it role-for-role
  (STYLE MANDATE: preserve the existing style even where imperfect; only `gofmt`/`go vet` bind —
  `rules match` returned no code rule for the Go footprint).
- **Governance:** `AGENTS.md:122` (add flag in `init()` → `runX` → `session.go`),
  `AGENTS.md:147-154` (TDD mandatory; `TestFunctionName_Scenario`; `MockTmuxExecutor` — tests
  pass with no tmux server), `AGENTS.md:211-213` (`make install` after code changes).
- **Repeatability:** the feature is "universal flags" (plural) → the one deliberate divergence
  from `--model`'s single `String` is a repeatable `StringArray` (the repo's house idiom for
  repeatable flags; comma-safe, unlike `StringSlice`).
- **Single source of truth:** avoid a parallel launch-chain constructor that can drift from the
  existing one.
- **Trust model:** flag values are injected verbatim into a local shell command — identical to
  `--model` (user-controlled local input). No new authorization/ACL seam applies; the feature
  exposes no tenant-/user-scoped data.

## Decision

Adopt **Option 1 — mirror `--model` role-for-role, extending the ONE launch-config
constructor to carry both `model` + `flags`.**

- Register a repeatable `--flag` (`StringArray`, `nil` default) on `startCmd` and
  `startAttachCmd`.
- Serialize the `[]string` into a single session-env var `TMUX_CLI_FLAGS`, **newline-joined**
  (a flag value may contain spaces/commas but never a newline → collision-free delimiter).
- Add `SessionManager.flags []string` + `WithFlags([]string)` builder; window 0 (supervisor)
  reads `m.flags` directly, exactly as it reads `m.model`.
- Extend `claudeLaunchCommands(model, flags)` and `PostCommandConfigWithModel(model, flags)`
  in place (single source of truth); `DefaultPostCommandConfig()` passes `("", nil)`.
- Inject each flag value **verbatim / unquoted** (they are flag tokens, unlike `--model`'s
  single-quoted data value) **after** the `--model` segment and **before** the
  `--session-id`/`--resume` tail — the tail stays byte-identical, and **empty flags render a
  command chain byte-identical to today** (mirrors the `Empty_MatchesDefault` invariant).
- Reuse path: `applyFlagsToExistingSession(...)` (no-op on empty), alongside
  `applyModelToExistingSession`, at both reuse call sites.
- The 3 read-back sites (`internal/mcp/tools.go`, `cmd/tmux-cli/session.go` `runWindowsCreate`,
  `cmd/tmux-cli/session_taskvisor.go`) read `TMUX_CLI_FLAGS`, split via a new **exported**
  `session.SplitFlags(raw) []string` helper (split on `"\n"`, drop empties), and feed the
  extended constructor.

### Final approved naming manifest (frozen — Stages 3-4 use verbatim)

| Role | Proposed name | Exemplar counterpart | Target file |
|------|---------------|----------------------|-------------|
| CLI flag | `--flag` (`StringArray`, nil default) | `--model` (`String`) | `cmd/tmux-cli/session.go` `init()` |
| session-env key | `TMUX_CLI_FLAGS` | `TMUX_CLI_MODEL` | `manager.go` / `session.go` |
| manager field | `flags []string` | `model string` | `internal/session/manager.go` |
| builder method | `WithFlags(flags []string)` | `WithModel` | `internal/session/manager.go` |
| reuse applier | `applyFlagsToExistingSession(...)` | `applyModelToExistingSession` | `cmd/tmux-cli/session.go` |
| launch composer | `claudeLaunchCommands(model, flags)` + `PostCommandConfigWithModel(model, flags)` | `claudeLaunchCommands(model)` | `internal/session/postcommand.go` |
| rendered local | `flagArgs` | `modelFlag` | `internal/session/postcommand.go` |
| env-split helper | `SplitFlags(raw string) []string` (exported) | net-new | `internal/session/postcommand.go` |
| [test] flag-exists | `flag_option_test.go` (`TestStartCmd_HasFlagFlag`, `TestStartAttachCmd_HasFlagFlag`) | `model_flag_test.go` | `cmd/tmux-cli/` |
| [test] injection | cases in `postcommand_test.go` + `manager_test.go` | `Test…_InjectsModelFlag` | `internal/session/` |

**Test strategy:** tests required (test-first deliverable). All non-test gates
(`gofmt`, `go vet`, `go build`) apply regardless.

## Consequences

### Positive
- Single launch-chain constructor stays the source of truth — no parallel constructor drift.
- Empty-flags path is byte-identical to today; zero risk to existing `--model`/default launches.
- Repeatable, comma-safe, space-safe passthrough of any Claude flag to all windows/workers.

### Negative
- Touches all 4 `claudeLaunchCommands`/`PostCommandConfigWithModel` call sites once (mechanical,
  covered by tests).

### Neutral
- Two pre-existing `--model` gaps are **inherited, not fixed** (task-confined): the self-update
  supervisor relaunch (`cmd/tmux-cli/self_update.go:268` uses `DefaultPostCommandConfig()`, reads
  neither env var) and the e2e target builder (`cmd/tmux-cli/e2e.go:468` forwards `--model` only).
  Recorded as report-not-fix; out of this feature's scope.

## Alternatives Considered

### Option 2 — parallel launch-config constructor
- Description: add `PostCommandConfigWithFlags(model, flags)` and leave
  `PostCommandConfigWithModel(model)` delegating with `nil` flags.
- Pros: fewer edits to the 3 read-back call sites initially.
- Cons: introduces a second launch-chain constructor that can drift from the first.
- Reason rejected: violates the STYLE MANDATE's single-source-of-truth preference; both
  investigators independently recommended extending the one constructor.
