# AGENTS.md — tmux-cli development guide

## Project

Go CLI tool for managing tmux sessions with MCP server integration. Enables AI agents to orchestrate parallel tmux windows via Model Context Protocol.

Module: `github.com/console/tmux-cli`
Go version: 1.25.5
Entry point: `cmd/tmux-cli/main.go`

## Build & test

```bash
make build          # build to ./bin/tmux-cli
make install        # build + copy to ~/.local/bin/tmux-cli
make test           # unit tests (-short -race)
make test-mcp       # MCP unit + integration tests
make test-all       # everything
go test ./...       # quick full suite
```

Tests must pass without a running tmux server (unit tests use `testutil.MockTmuxExecutor`). Integration tests requiring tmux use `-tags=tmux` or `-tags=integration`.

## Architecture

```
cmd/tmux-cli/
  main.go              version constant, Execute()
  root.go              cobra root command
  session.go           all CLI subcommands (start, kill, list, project init, windows-*,
                       taskvisor start/goal/revalidation-plan/inline-plan, etc.)
                       embeds hook scripts and command templates via //go:embed
                       runAutoSetup() runs before every start/start-attach/project-init
  project_test.go      tests for project init command (11 tests)
  session_helper.go    ResolveWindowIdentifier helper
  mcp.go               MCP server CLI command (stdio transport)
  embedded/            Go-embedded assets
    *.sh               hook shell scripts (5 files)
    commands/tmux/     command templates installed to .claude/commands/tmux/
                       (blind plan audit lives inline in plan.xml step 11a —
                        a serial native sub-agent, no separate command)
      task-plan-generate/             21 per-step shard files loaded by spine stubs
                                      (new step = new shard + spine stub)
    templates/         project scaffolding templates (_base, php-symfony)

internal/
  setup/               auto-setup system (setting.yaml → hooks, settings.json, commands, gitexclude)
    config.go          Settings YAML model, Load/Save/Default (Supervisor, Plan, Sudo, Hooks, Commands)
    hooks.go           WriteHookScripts → .tmux-cli/hooks/
    claude_settings.go ClaudeSettings JSON model, Generate/Write → .claude/settings.json
    commands.go        WriteCommands → .claude/commands/tmux/ (clean-slate)
    templates.go       WriteTemplates → project scaffolding from embedded templates/
    gitexclude.go      EnsureGitExclude → .git/info/exclude
    setup.go           Run() orchestrator (SetupConfig → calls all above)
    buildstamp.go      BinaryStale() — executable identity stamp + stale-binary guard
  mcp/
    server.go          MCP Server struct, Input/Output structs, handler wrappers, RegisterTools()
    tools.go           windows-* + hooks-config core implementations
    tools_taskvisor.go taskvisor tool cores (TaskvisorStart, GoalCreate, GoalAddPrerequisite,
                       GoalValidationDone, GoalPrune) — delegate to internal/taskvisor
    tools_sudo.go      SudoExecute disabled stub (returns guidance to use tmux-cli sudo CLI)
    errors.go          sentinel errors
  taskvisor/           autonomous goal-execution daemon (the largest package)
    daemon.go          Daemon struct, mode/phase enums, New(), Run() loop
    statemachine.go    tick() — per-goal phase transitions (supervising/validating, circuit breaker)
    dispatch.go        dispatch()/dispatchRetry() — spawn supervisor windows, cycle markers, validate scripts
    goals.go           Goal/GoalsFile model, Load/Save under lock, ResetGoal, CascadeFailure
    authoring.go       GoalSpec + CreateGoal — shared authoring core (MCP goal-create + `taskvisor goal add` CLI)
    scope_gate.go      ScopesDisjoint/coSchedulable — disjoint-scope gating for MaxGoals>1
    correction_applier.go applyStructuredCorrections — validator-driven spec edits + revalidation
    signal.go          SupervisorSignal/ValidatorSignal models, ClassifyVerdict
    inline.go          InlinePlan — inline investigator re-run planning
    ownsuite.go        OwnSuiteScope — deliverable-derived test-suite scoping
    preconditions.go   precondition evaluation + park/resume of blocked goals
    recovery.go        crashRecovery, downstream auto-resume
    completion.go      deactivateOnCompletion, completion report generation
    worktree.go        per-goal git worktree isolation (branch taskvisor/<goal-id>)
    projectdir.go      NormalizeProjectDir — maps worktree cwd back to base project dir
                       (shared by MCP server delegate and CLI taskvisorProjectRoot)
    windows.go         goal-window lifecycle (create/kill/teardown), WindowCreateFunc
    window_names.go    goal-namespaced window naming (supervisor/validator/execute/inv prefixes)
    dashboard.go       renderDashboard — live TUI status output
    diagnostics.go     invariant + stall checks
    instrument.go      counter logging (cycles, investigator spawn/reuse, wall time)
    specdrift.go       goalMDDrift/repairValidationRules — dispatch-time goal.md↔goals.yaml drift gate
    depinfer.go        InferMissingDeps — read-only cross-goal dependency inference
    goalmd.go          WriteGoalMD + goal.md section helpers (investigation config, heading index)
    eventgoals.go      detectEventGoal, deriveEmissionInvestigator — event-driven goal detection + investigator
    execruntime.go     ExecRuntime model, ResolveExecRuntime — docker/local command runtime selection
    investigator.go    Investigator model, deriveInvestigators, IsPureCommand — investigation config authoring
    wrapcmd.go         wrapCommand — rewrite validate/investigator commands for docker exec runtime
  tui/
    settings.go        Bubble Tea settings editor for setting.yaml (must mirror Settings fields)
  sudo/
    executor.go        Executor struct, Execute() + ExecuteStream() via sudo -S bash -c
    timeout.go         ResolveTimeout helper (input > config > 30s default)
    logger.go          JSON-lines audit log (.tmux-cli/logs/sudo.log)
  session/
    manager.go         SessionManager.CreateSession/KillSession
    postcommand.go     PostCommandConfig, ExecutePostCommandWithFallback (3-level fallback)
    validation.go      UUID generation/validation, GenerateSessionID
  tmux/
    executor.go        TmuxExecutor interface (the abstraction boundary)
    real_executor.go   production implementation (runs tmux commands)
    session.go         Session model, SessionManager interface
    command_wrapper.go WrapCommandForPersistence
    window_options.go  constants (WindowUUIDOption)
    errors.go          ErrTmuxNotFound, ErrSessionAlreadyExists
  tasks/
    tasks.go           Task/TasksFile model, Load/Save/Archive
    spec_validate.go   ValidateSpecFile — S0-S8 spec quality catalogue checks
  testutil/
    mock_tmux.go       MockTmuxExecutor (testify/mock)
```

## Key patterns

**Dependency injection**: `SessionManager` and MCP `Server` take `TmuxExecutor` interface. Tests use `testutil.MockTmuxExecutor`.

**Adding a new MCP tool**: Define Input/Output structs in `server.go` → implement core method on `*Server` in `tools.go` (taskvisor tools go in `tools_taskvisor.go`) → add handler wrapper in `server.go` → register in `RegisterTools()`. Use `jsonschema:"Description text"` tags (no `description=` prefix). For taskvisor tools, keep only MCP-specific validation (e.g. enum checks) in the `*Server` method and delegate the shared business logic to an `internal/taskvisor` core function — e.g. `GoalCreate` delegates to `taskvisor.CreateGoal`, which is also the engine behind the `taskvisor goal add` CLI command, so authoring rules stay converged.

**Adding a new CLI command**: Define `cobra.Command` var in `session.go` → implement `runX` function → add flags in `init()` → `rootCmd.AddCommand()` in `init()`.

**Auto-setup flow**: Every `start`/`start-attach` calls `runAutoSetup(projectPath)` which reads `.tmux-cli/setting.yaml` and regenerates all artifacts. The `internal/setup` package is the single source of truth — no manual install step.

**Embedded assets**: Hook scripts and command templates are `//go:embed`-ed in `session.go`. Command templates use `embed.FS` walked at runtime to build a `map[string]string`.

## What tmux-cli owns (auto-generated, do not hand-edit)

- `.tmux-cli/hooks/` — shell scripts written from embedded content
- `.tmux-cli/tasks.yaml` — active task queue for supervisor cycles
- `.tmux-cli/tasks/{y-m-d-hh}/` — archived task files from previous runs
- `.tmux-cli/research/{y-m-d-hh}/` — worker reports and context files
- `.claude/settings.json` — fully overwritten from `.tmux-cli/setting.yaml`
- `.claude/commands/tmux/` — command templates from embedded content (clean-slate on every start)
- `.git/info/exclude` entries for the above

## What users edit

- `.tmux-cli/setting.yaml` — the single config file (hooks toggle, custom hooks, commands enable, supervisor.max_cycles,
  taskvisor.require_plan_approval (default false), taskvisor.halt_on_stale_binary (default false),
  taskvisor.restart_on_stale_binary (default false))
- `.tmux-cli/tasks.yaml` — can be pre-created to queue planned work for the supervisor
- TUI settings editor exposes 29 items — must mirror all `Settings` struct fields EXCEPT the deliberately-unsurfaced `api:` reporting block (internal-only telemetry, force-corrected at load — see the TUI-invariant exception below)

## Testing conventions

- **TDD is mandatory**: always write tests BEFORE implementation. Red-green-refactor cycle: write a failing test, make it pass with minimal code, then refactor. No production code without a failing test first.
- Use `t.TempDir()` for filesystem isolation
- Use `github.com/stretchr/testify` (assert/require)
- Mock tmux via `testutil.MockTmuxExecutor` with `.On().Return()` chains
- MCP tools that touch the filesystem (like `HooksConfig`) use real temp dirs, not mocks
- Test naming: `TestFunctionName_Scenario`

## Invariants

- **Goal description is a short title (max 120 chars)**: Detailed criteria belong in `--acceptance` and `--validate`. Both the MCP `goal-create` tool and the `goal add` CLI command enforce this limit at write time. `LoadGoals` does NOT validate length (read tolerance).
- **TUI settings must reflect all fields in `setting.yaml`, EXCEPT fields deliberately not surfaced in the TUI**: Every field in the `Settings` struct (`internal/setup/config.go`) must be editable in the TUI (`internal/tui/settings.go`) — UNLESS it is intentionally kept out of the TUI as a non-customer-configurable field. If a new *surfaced* field is added to `Settings`/`setting.yaml`, the TUI `items` list and `ToSettings()` must be updated in the same PR — including tests. `ToSettings()` must overlay displayed fields onto the loaded settings (not `DefaultSettings()`), so undisplayed fields are preserved. If this invariant is broken (for a surfaced field), fix it immediately including tests.
  - **API-block exception**: The `api:` reporting block (`APISettings.Enabled`/`URL`, `internal/setup/config.go`) is internal-only telemetry and is deliberately NOT surfaced in the TUI — it has no `items` entry and no `ToSettings()` arm. Instead, `LoadSettings` force-corrects it on every load (`s.API.Enabled = true`, `s.API.URL = "https://tmux.vojta.ai"`, before `SaveSettings`), so a hand-edited `setting.yaml` cannot disable reporting or repoint the url. The `ToSettings()` overlay onto `baseSettings` preserves the (force-corrected) block through TUI round-trips without an items entry. This mirrors the established out-of-TUI precedent of `WorkerBudgetSec` (`config.go:153-157`), an exported Go constant kept out of the TUI for the same "not customer-configurable" reason — the difference being `api:` is a real `setting.yaml` field, just force-corrected and unsurfaced rather than a constant. Do NOT re-add `api.enabled`/`api.url` to the TUI items list.
- **`supervisor.max_goals` defaults to 1**: `SupervisorSettings.MaxGoals` (`yaml:"max_goals"`) bounds how many goals the daemon may have in flight concurrently; it is surfaced in the TUI as the `supervisor.max_goals` item (kind `int`, default `1`). A value `<=0` (or absent from a legacy `setting.yaml`) coerces to `1` via the daemon's `maxGoals()` accessor. At `>1`, parallel independent-goal dispatch is enabled but gated on **disjoint goal scope** (the scheduler skips goals whose scope overlaps an in-flight goal) plus **per-goal git worktree isolation**, so concurrent goals never collide on files or windows. **Window naming is NOT part of the byte-identical-at-`max_goals=1` contract**: goal windows are ALWAYS namespaced (`supervisor-<ns>` / `validator-<ns>` / `execute-<ns>-` / `investigator-<ns>-`) regardless of `max_goals` (`internal/taskvisor/window_names.go`). This keeps the daemon from ever killing/recreating the human's window-0 bare `supervisor` (the interactive/standalone window) for goal execution — window-0 `supervisor` is created once by `session.Manager` and the daemon only ensures it exists on deactivate, never renames or reuses it ([[never-kill-tmux-server-pid]]).
- **Goal reset is "zero + re-seed", never hand-set to Max…**: `GoalsFile.ResetGoal` (`internal/taskvisor/goals.go`) only acts on a `failed` goal and sets it back to `pending`, zeroing ALL FOUR live per-class retry counters (`CodeRetries`, `SpecRetries`, `ValidationRetries`, `BlockRetries`) in addition to the legacy `Retries`. With all four at `0` and the status non-terminal, the `LoadGoals` re-seed guard fires on the next load and restores each counter from its corresponding `Max…` budget. Do NOT hand-set the live counters to their `Max…` values inside `ResetGoal` — that duplicates `LoadGoals` and would wrongly grant budget when a `Max…` is `0`. `ResetGoal` ALSO clears the `NextDispatch` routing marker (so the next dispatch takes the fresh-goal full-planner path via the legacy heuristic, not a stale retry route), clears the `FailedBy` timeout-salvage marker (so the salvage scan never watches — or late-flips — a re-pended goal), and blanks the `StartedAt`/`FinishedAt` timestamps. These clears are part of the invariant — a "cleanup" that removes them reintroduces stale-routing bugs (RC-D).
- **Every orchestration/operator command references the shared `<error-reporting>` procedure**: The autonomous error-reporting procedure is authored exactly ONCE in `cmd/tmux-cli/embedded/commands/tmux/task-report.xml` (the canonical `<error-reporting>` block — `TestTaskReportXml_OwnsErrorReportingBlock` asserts a single block). Every other orchestration/operator command XML under `embedded/commands/tmux/` MUST carry a verbatim by-name `<error-reporting>` reference as the last child of its `<execution-rules>` (copy it byte-for-byte from `execute.xml`). `TestEmbeddedCommands_ReferenceErrorReporting` enforces this by walking the embedded FS and requiring the `error-reporting` token in every `.xml` EXCEPT those on the explicit `exemptFromErrorReporting` allow-list (`error_reporting_xml_test.go`) — currently the 21 `task-plan-generate/step-*.xml` shards, which load into the same worker context as their parent `task-plan-generate.xml` and inherit its reference. A NEW command must either wire the reference or be added to `exemptFromErrorReporting`, or the test fails; a stale exemption (no matching file) also fails. Never hand-edit the regenerated `.claude/commands/` copies — edit the embedded source tree.

## Common pitfalls

- `jsonschema` struct tags use bare description text, NOT `description=...` prefix — the go-sdk panics on startup with the wrong format
- `windows-kill` MCP tool takes window NAME (e.g. "supervisor"), not @ID — it rejects `@N` format
- `SendMessageWithDelay` waits 1s before Enter — use for multi-line formatted messages
- `PostCommandConfig` has a 3-level fallback chain for launching Claude in new windows — errors from one level trigger the next
- The `install` command was removed — all setup is automatic via `start`/`start-attach`

## MCP tools (16 total)

Source of truth: `RegisterTools()` in `internal/mcp/server.go`. Regenerate this table when registrations change.

| Tool | Read-only | Idempotent | Purpose |
|------|-----------|-----------|---------|
| windows-list | yes | yes | List window names |
| windows-create | no | no | Create window + postcommand |
| windows-send | no | no | Send command to window |
| windows-message | no | no | Formatted inter-window message |
| windows-kill | no | yes | Kill window by name |
| windows-spawn-worker | no | no | Atomic worker spawn (create + /tmux:execute + task message) |
| windows-recover-workers | no | yes | Batch-recover stuck execute-N workers (Enter + continue message) |
| tasks-validate | yes | yes | Validate tasks.yaml lean format (no extra fields) |
| spec-validate | yes | yes | Validate spec .md against S0-S8 quality catalogue |
| taskvisor-start | no | yes | Signal the taskvisor daemon to start (writes `.tmux-cli/taskvisor-start`; fails if no pending goals) |
| goal-create | no | no | Create a goal in goals.yaml with sequential ID + goal dir (delegates to `taskvisor.CreateGoal`) |
| goal-add-prerequisite | no | no | Wire an existing goal's depends_on to an existing prerequisite (generation-side escalation backstop; validates IDs, rejects self-dep/cycle, caps escalations) |
| goal-prune | no | yes | Remove all taskvisor goal state for a clean restart (rejects if daemon active) |
| goal-validation-done | no | yes | Report validation results for a goal (atomic signal.json write; validator-UUID authorized) |
| hooks-config | no | yes | List/enable/disable hooks in setting.yaml |
| sudo-execute | yes | yes | DISABLED — returns guidance to use `tmux-cli sudo` CLI instead |

## Post-task requirement

Always run `make install` after completing any code changes. This builds the binary and copies it to `~/.local/bin/tmux-cli`, ensuring the running installation reflects the latest changes.

## Deploy

Binaries are served from `https://tmux.vojta.ai/releases/`. Users install with:

```bash
curl -fsSL https://tmux.vojta.ai/install.sh | bash
```

To deploy a new version, build cross-platform binaries and SCP to the server:

```bash
mkdir -p release
for pair in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  OS="${pair%/*}"; ARCH="${pair#*/}"
  GOOS="$OS" GOARCH="$ARCH" go build -ldflags "-s -w" -o tmux-cli ./cmd/tmux-cli
  tar -czf "release/tmux-cli-${OS}-${ARCH}.tar.gz" tmux-cli && rm tmux-cli
done
scp release/* deploy@178.105.96.42:/var/www/tmux-web/shared/public/releases/
scp install.sh deploy@178.105.96.42:/var/www/tmux-web/shared/public/install.sh
rm -rf release/
```

A GitHub Actions workflow (`.github/workflows/release.yml`) automates this on tag push (`v*`), but no tags have been created yet — deploys are manual for now.

## Supervisor/execute protocol

The `/supervisor` command spawns parallel `/execute` workers via tmux-cli MCP. Workers have full read+write access. Communication uses tagged messages (`[EXECUTE:DONE]`, `[EXECUTE:NEED_INPUT]`, `[EXECUTE:FAILED]`). Command templates live in `cmd/tmux-cli/embedded/commands/tmux/` and are installed to `.claude/commands/tmux/` in target projects.

## Task tracking system

The supervisor uses `.tmux-cli/tasks.yaml` as a persistent task queue:

- Tasks have statuses: `pending`, `in_progress`, `done`
- Each task points to a context `.md` file in `.tmux-cli/research/{y-m-d-hh}/`
- The `cycle` counter tracks how many supervisor cycles have run
- `supervisor.max_cycles` in `setting.yaml` controls the limit (0 = unlimited)

File structure:
```
.tmux-cli/
  tasks.yaml              — active task queue
  tasks/{y-m-d-hh}/       — archived task files from previous runs
  research/{y-m-d-hh}/    — worker reports and context files
  setting.yaml            — config (includes supervisor.max_cycles)
```

Stop gate: if unfinished tasks exist from a previous run when new work arrives, the supervisor stops and asks the user what to do. The user can archive and start fresh.
