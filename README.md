# tmux-cli

A Go CLI tool and MCP server for managing tmux sessions with automatic recovery. Built for AI agent coordination — lets Claude Code (or other assistants) create, manage, and communicate across tmux windows with persistent state and crash recovery.

## Features

- **Session lifecycle** — create, kill, end, list, and inspect tmux sessions with JSON-based persistence
- **Window management** — create, list, kill windows; send commands and messages between them
- **Automatic recovery** — transparently recreates sessions and windows after tmux crashes
- **MCP server** — Model Context Protocol integration so AI assistants can control windows directly
- **Inter-window messaging** — structured communication between agent windows with sender auto-detection
- **Claude Code hooks** — lifecycle event logging (start, end, stop) with embedded hook scripts
- **Atomic persistence** — write-then-rename file operations prevent corruption

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

# Create a worker window
tmux-cli windows-create --name worker

# Send a command to the worker
tmux-cli windows-send --window-id @1 --message "python train.py"

# Check session status
tmux-cli status

# End session when done
tmux-cli end
```

## Commands

### Session management

| Command | Description |
|---------|-------------|
| `tmux-cli start` | Create a new session (auto-detects project from cwd) |
| `tmux-cli kill [--id UUID]` | Kill session, preserving state for recovery |
| `tmux-cli end [--id UUID]` | End session permanently (archives state) |
| `tmux-cli list` | List all active sessions |
| `tmux-cli status [--id UUID]` | Show detailed session status |

### Window management

| Command | Description |
|---------|-------------|
| `windows-create [--name NAME]` | Create a new window in the current session |
| `windows-list` | List all windows with IDs and UUIDs |
| `windows-kill [--window-id @N]` | Kill a window (removes from recovery) |
| `windows-send [--window-id @N\|NAME] [--message TEXT]` | Send a command to a window |
| `windows-uuid [--window-id @N]` | Get persistent UUID of a window |
| `windows-message [--receiver @N\|NAME] [--message TEXT]` | Send formatted message with sender info |

### MCP server

```bash
# Start the MCP protocol server (stdin/stdout, zero-config)
tmux-cli mcp
```

Exposes these tools to AI assistants: `windows-list`, `windows-create`, `windows-send`, `windows-kill`, `windows-message`.

### Project setup

```bash
# Install Claude Code hooks and configuration
tmux-cli install-project-files [--force]
```

Creates `.claude/settings.json` with hook configuration and installs lifecycle scripts.

## Recovery

When a tmux session is killed (crash, `tmux kill-server`, etc.), tmux-cli automatically detects the missing session on the next operation and recreates it — same session ID, same windows, same UUIDs. No manual intervention needed.

## Session file

State is stored in `.tmux-session` at the project root:

```json
{
  "sessionId": "550e8400-e29b-41d4-a716-446655440000",
  "projectPath": "/path/to/project",
  "createdAt": "2025-02-27T15:30:45Z",
  "postCommand": {
    "enabled": true,
    "commands": ["claude --session-id=\"$TMUX_WINDOW_UUID\""],
    "errorPatterns": ["already in use"]
  },
  "windows": [
    { "tmuxWindowId": "@0", "name": "supervisor", "uuid": "550e8400-..." },
    { "tmuxWindowId": "@1", "name": "worker", "uuid": "f47ac10b-..." }
  ]
}
```

## Project structure

```
cmd/tmux-cli/         CLI entry point, cobra commands, embedded hooks
internal/
  store/              Session persistence (atomic JSON file store)
  tmux/               Tmux command execution layer
  session/            Session orchestration and validation
  recovery/           Crash detection and session recreation
  mcp/                MCP server and tool implementations
  testutil/           Mock tmux executor for testing
scripts/              Build and verification scripts
docs/                 Architecture documentation
```

## Testing

```bash
make test          # Unit tests (no tmux required)
make test-tmux     # Integration tests (requires tmux)
make test-mcp      # MCP server tests
make test-all      # Everything
make coverage      # Coverage report
```

## License

MIT
