# TMUX-CLI work killer

A Go CLI tool and MCP server for managing tmux sessions. Built for AI agent coordination — lets Claude Code (or other assistants) create, manage, and communicate across tmux windows using tmux-native state management.

## Features

- **Session lifecycle** — create, kill, list, and inspect tmux sessions with tmux-native state management
- **Window management** — create, list, kill windows; send commands and messages between them
- **MCP server** — Model Context Protocol integration so AI assistants can control windows directly
- **Inter-window messaging** — structured communication between agent windows with sender auto-detection
- **Claude Code hooks** — lifecycle event logging (start, stop) with embedded hook scripts
- **No file-based state** — all session state stored in tmux environment variables and user-options

## Requirements

- **Go** 1.25+
- **tmux** 2.0+
- **jq** (optional, used by hook scripts)

## Installation

```bash
# Build
make build

# Install to ~/.local/bin
make install

# Or build manually
go build -ldflags "-s -w" -o bin/tmux-cli ./cmd/tmux-cli
```

## Quick start

```bash
# Create a session in the current project directory
tmux-cli start

# Or create and immediately attach to it
tmux-cli start-attach

# Create a worker window
tmux-cli windows-create --name worker

# Send a command to the worker
tmux-cli windows-send --window-id @1 --message "python train.py"

# Check session status
tmux-cli status

# Kill session when done
tmux-cli kill
```

## Commands

### Session management

| Command | Description |
|---------|-------------|
| `tmux-cli start` | Create a new session (auto-detects project from cwd, prompts if session exists) |
| `tmux-cli start-attach` | Create a new session and attach to it |
| `tmux-cli kill [session-id]` | Kill a tmux session |
| `tmux-cli list` | List all active sessions |
| `tmux-cli status` | Show detailed status of the session for this directory |

### Window management

| Command | Description |
|---------|-------------|
| `windows-create --name NAME` | Create a new window in the current session |
| `windows-list` | List all windows with IDs and UUIDs |
| `windows-kill --window-id @N` | Kill a window in the session |
| `windows-send --window-id @N\|NAME --message TEXT` | Send a text message to a window |
| `windows-uuid --window-id @N` | Get persistent UUID of a window |
| `windows-message --receiver @N\|NAME --message TEXT` | Send formatted message with sender auto-detection |

### MCP server

```bash
# Start the MCP protocol server (stdin/stdout, zero-config)
tmux-cli mcp
```

Exposes these tools to AI assistants: `windows-list`, `windows-create`, `windows-send`, `windows-kill`, `windows-message`.

### Project setup

```bash
# Install Claude Code hooks and configuration
tmux-cli install [--force]
```

Creates `.claude/settings.json` with hook configuration and sets up required directories.

## State management

Session state is stored entirely in tmux itself — no session files on disk:

- Sessions are discovered by the `TMUX_CLI_PROJECT_PATH` environment variable set on each tmux session
- Window UUIDs are stored as the `@window-uuid` tmux user-option on each window
- Post-command configuration is hardcoded in Go

## Project structure

```
cmd/tmux-cli/         CLI entry point, cobra commands, embedded hooks
internal/
  mcp/                MCP server and tool implementations
  session/            Session orchestration, validation, post-command
  testutil/           Mock tmux executor for testing
  tmux/               Tmux command execution layer (executor interface + real impl)
scripts/              Build and verification scripts
docs/                 Architecture documentation
```

## Testing

```bash
make test          # Unit tests (no tmux required)
make test-tmux     # Tmux-specific tests (requires tmux 2.0+)
make test-mcp      # MCP tests (unit + integration)
make test-all      # All tests (unit + tmux + integration + MCP)
make verify-real   # Build + E2E verification with real tmux
make coverage      # Coverage report
```

## Support

If you find this useful, you can [buy me a coffee](https://buymeacoffee.com/veceradev).

## License

MIT
