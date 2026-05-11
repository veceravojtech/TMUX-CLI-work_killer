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
    *.sh               hook shell scripts (3 files)
    commands/tmux/     command templates installed to .claude/commands/tmux/

internal/
  setup/               auto-setup system (settings.yaml → hooks, settings.json, commands, gitexclude)
    config.go          Settings YAML model, Load/Save/Default
    hooks.go           WriteHookScripts → .tmux-cli/hooks/
    claude_settings.go ClaudeSettings JSON model, Generate/Write → .claude/settings.json
    commands.go        WriteCommands → .claude/commands/tmux/ (clean-slate)
    gitexclude.go      EnsureGitExclude → .git/info/exclude
    setup.go           Run() orchestrator (SetupConfig → calls all above)
  mcp/
    server.go          MCP Server struct, handlers, RegisterTools()
    tools.go           tool implementations (WindowsList/Create/Send/Message/Kill, HooksConfig)
    errors.go          sentinel errors
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
  testutil/
    mock_tmux.go       MockTmuxExecutor (testify/mock)
```

## Key patterns

**Dependency injection**: `SessionManager` and MCP `Server` take `TmuxExecutor` interface. Tests use `testutil.MockTmuxExecutor`.

**Adding a new MCP tool**: Define Input/Output structs in `server.go` → implement core method on `*Server` in `tools.go` → add handler wrapper in `server.go` → register in `RegisterTools()`. Use `jsonschema:"Description text"` tags (no `description=` prefix).

**Adding a new CLI command**: Define `cobra.Command` var in `session.go` → implement `runX` function → add flags in `init()` → `rootCmd.AddCommand()` in `init()`.

**Auto-setup flow**: Every `start`/`start-attach` calls `runAutoSetup(projectPath)` which reads `.tmux-cli/settings.yaml` and regenerates all artifacts. The `internal/setup` package is the single source of truth — no manual install step.

**Embedded assets**: Hook scripts and command templates are `//go:embed`-ed in `session.go`. Command templates use `embed.FS` walked at runtime to build a `map[string]string`.

## What tmux-cli owns (auto-generated, do not hand-edit)

- `.tmux-cli/hooks/` — shell scripts written from embedded content
- `.claude/settings.json` — fully overwritten from `.tmux-cli/settings.yaml`
- `.claude/commands/tmux/` — command templates from embedded content (clean-slate on every start)
- `.git/info/exclude` entries for the above

## What users edit

- `.tmux-cli/settings.yaml` — the single config file (hooks toggle, custom hooks, commands enable)

## Testing conventions

- TDD: write tests first, then implementation
- Use `t.TempDir()` for filesystem isolation
- Use `github.com/stretchr/testify` (assert/require)
- Mock tmux via `testutil.MockTmuxExecutor` with `.On().Return()` chains
- MCP tools that touch the filesystem (like `HooksConfig`) use real temp dirs, not mocks
- Test naming: `TestFunctionName_Scenario`

## Common pitfalls

- `jsonschema` struct tags use bare description text, NOT `description=...` prefix — the go-sdk panics on startup with the wrong format
- `windows-kill` MCP tool takes window NAME (e.g. "supervisor"), not @ID — it rejects `@N` format
- `SendMessageWithDelay` waits 1s before Enter — use for multi-line formatted messages
- `PostCommandConfig` has a 3-level fallback chain for launching Claude in new windows — errors from one level trigger the next
- The `install` command was removed — all setup is automatic via `start`/`start-attach`

## MCP tools (6 total)

| Tool | Read-only | Idempotent | Purpose |
|------|-----------|-----------|---------|
| windows-list | yes | yes | List window names |
| windows-create | no | no | Create window + postcommand |
| windows-send | no | no | Send command (with optional sudo) |
| windows-message | no | no | Formatted inter-window message |
| windows-kill | no | yes | Kill window by name |
| hooks-config | no | yes | List/enable/disable hooks in settings.yaml |

## Supervisor/execute protocol

The `/supervisor` command spawns parallel `/execute` workers via tmux-cli MCP. Workers have full read+write access. Communication uses tagged messages (`[EXECUTE:DONE]`, `[EXECUTE:NEED_INPUT]`, `[EXECUTE:FAILED]`). Command templates live in `cmd/tmux-cli/embedded/commands/tmux/` and are installed to `.claude/commands/tmux/` in target projects.
