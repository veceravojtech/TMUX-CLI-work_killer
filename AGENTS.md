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
  session.go           all CLI subcommands (start, kill, list, windows-*, etc.)
                       embeds hook scripts and command templates via //go:embed
                       runAutoSetup() runs before every start/start-attach
  session_helper.go    ResolveWindowIdentifier helper
  mcp.go               MCP server CLI command (stdio transport)
  embedded/            Go-embedded assets
    *.sh               hook shell scripts (5 files)
    commands/tmux/     command templates installed to .claude/commands/tmux/

internal/
  setup/               auto-setup system (setting.yaml → hooks, settings.json, commands, gitexclude)
    config.go          Settings YAML model, Load/Save/Default (Supervisor, Plan, Sudo, Hooks, Commands)
    hooks.go           WriteHookScripts → .tmux-cli/hooks/
    claude_settings.go ClaudeSettings JSON model, Generate/Write → .claude/settings.json
    commands.go        WriteCommands → .claude/commands/tmux/ (clean-slate)
    gitexclude.go      EnsureGitExclude → .git/info/exclude
    setup.go           Run() orchestrator (SetupConfig → calls all above)
  mcp/
    server.go          MCP Server struct, handlers, RegisterTools()
    tools.go           tool implementations (WindowsList/Create/Send/Message/Kill, HooksConfig)
    tools_sudo.go      SudoExecute disabled stub (returns guidance to use tmux-cli sudo CLI)
    errors.go          sentinel errors
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
    command_wrapper.go WrapCommandForPersistence
    window_options.go  constants (WindowUUIDOption)
    errors.go          ErrTmuxNotFound, ErrSessionAlreadyExists
  tasks/
    tasks.go           Task/TasksFile model, Load/Save/Archive
  testutil/
    mock_tmux.go       MockTmuxExecutor (testify/mock)
```

## Key patterns

**Dependency injection**: `SessionManager` and MCP `Server` take `TmuxExecutor` interface. Tests use `testutil.MockTmuxExecutor`.

**Adding a new MCP tool**: Define Input/Output structs in `server.go` → implement core method on `*Server` in `tools.go` → add handler wrapper in `server.go` → register in `RegisterTools()`. Use `jsonschema:"Description text"` tags (no `description=` prefix).

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

- `.tmux-cli/setting.yaml` — the single config file (hooks toggle, custom hooks, commands enable, supervisor.max_cycles)
- `.tmux-cli/tasks.yaml` — can be pre-created to queue planned work for the supervisor

## Testing conventions

- **TDD is mandatory**: always write tests BEFORE implementation. Red-green-refactor cycle: write a failing test, make it pass with minimal code, then refactor. No production code without a failing test first.
- Use `t.TempDir()` for filesystem isolation
- Use `github.com/stretchr/testify` (assert/require)
- Mock tmux via `testutil.MockTmuxExecutor` with `.On().Return()` chains
- MCP tools that touch the filesystem (like `HooksConfig`) use real temp dirs, not mocks
- Test naming: `TestFunctionName_Scenario`

## Invariants

- **Goal description is a short title (max 120 chars)**: Detailed criteria belong in `--acceptance` and `--validate`. Both the MCP `goal-create` tool and the `goal add` CLI command enforce this limit at write time. `LoadGoals` does NOT validate length (read tolerance).
- **TUI settings must reflect all fields in `setting.yaml`**: Every field in the `Settings` struct (`internal/setup/config.go`) must be editable in the TUI (`internal/tui/settings.go`). If a new field is added to `Settings`/`setting.yaml`, the TUI `items` list and `ToSettings()` must be updated in the same PR — including tests. `ToSettings()` must overlay displayed fields onto the loaded settings (not `DefaultSettings()`), so undisplayed fields are preserved. If this invariant is broken, fix it immediately including tests.

## Common pitfalls

- `jsonschema` struct tags use bare description text, NOT `description=...` prefix — the go-sdk panics on startup with the wrong format
- `windows-kill` MCP tool takes window NAME (e.g. "supervisor"), not @ID — it rejects `@N` format
- `SendMessageWithDelay` waits 1s before Enter — use for multi-line formatted messages
- `PostCommandConfig` has a 3-level fallback chain for launching Claude in new windows — errors from one level trigger the next
- The `install` command was removed — all setup is automatic via `start`/`start-attach`

## MCP tools (11 total)

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
