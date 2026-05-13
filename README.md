# TMUX-CLI

A Go CLI and MCP server for managing tmux sessions. Built for AI agent coordination — lets Claude Code spawn, manage, and communicate across parallel tmux windows.

## Install

```bash
curl -fsSL https://tmux.vojta.ai/install.sh | bash
```

### Manual install

**Linux:**

```bash
curl -fSL https://tmux.vojta.ai/releases/tmux-cli-linux-amd64.tar.gz | tar -xz
sudo mv tmux-cli /usr/local/bin/
```

**macOS:**

```bash
curl -fSL https://tmux.vojta.ai/releases/tmux-cli-darwin-arm64.tar.gz | tar -xz
mv tmux-cli ~/.local/bin/
```

## Getting started

### 1. Configure settings

```bash
tmux-cli setting
```

Opens a TUI where you toggle options with arrow keys and Enter:

| Setting | Default | Description |
|---------|---------|-------------|
| Session Notify | on | Log session start/stop events |
| Block Interactive | on | Block interactive commands in worker windows |
| Commands Enabled | on | Install `/tmux:plan`, `/tmux:supervisor`, `/tmux:execute` slash commands |
| Max Workers | 3 | Maximum parallel worker windows |
| Unplanned Audit | on | Require audit before unplanned supervisor runs |
| Plan Auto-Approve | off | Skip human approval of generated task plans |
| Plan Auto-Execute | off | Automatically start implementation after planning |

### 2. Start a session

```bash
tmux-cli start-attach
```

Creates a tmux session for the current project directory and attaches to it. Claude Code hooks and slash commands are auto-installed on every start.

### 3. Use Claude Code inside the session

Once attached, run `claude` inside the tmux session. The `/tmux:plan` and `/tmux:supervisor` slash commands are now available.

## Usage

### Planning and implementation (`/tmux:plan`)

Use when you have a task that needs to be broken into subtasks, specced, and implemented.

```
/tmux:plan refactor the auth module to support OAuth2 and SAML
```

What happens:
1. Claude analyzes the task and breaks it into 3–15 subtasks
2. Parallel spec workers spawn in tmux windows to write implementation specs
3. Specs are validated against quality checks (S0–S8)
4. A `tasks.yaml` file is produced with implementation-ready tasks
5. If auto-execute is enabled, the implementation supervisor starts automatically
6. Workers implement each task, verify, and report back
7. Done

### Direct execution (`/tmux:supervisor`)

Use when you already have context and want Claude to execute immediately — no planning phase.

```
/tmux:supervisor do deep reverse engineering of firmware.bin
```

What happens:
1. The supervisor spawns parallel worker windows
2. Each worker receives its task and runs independently
3. Workers report results back via tagged messages
4. The supervisor collects results and presents them

### Examples

**Feature implementation:**
```
/tmux:plan add WebSocket support to the notification service
```

**Research and analysis:**
```
/tmux:supervisor analyze all API endpoints and document their auth requirements
```

**Bug investigation:**
```
/tmux:supervisor investigate why the payment webhook fails on retry
```

**Codebase exploration:**
```
/tmux:supervisor reverse engineer the legacy billing module and document the data flow
```

## Commands

| Command | Description |
|---------|-------------|
| `tmux-cli start` | Create a new session for the current directory |
| `tmux-cli start-attach` | Create a session and attach to it |
| `tmux-cli kill` | Kill the session for the current directory |
| `tmux-cli list` | List all active sessions |
| `tmux-cli status` | Show session status |
| `tmux-cli setting` | Open settings TUI |
| `tmux-cli mcp` | Start MCP server (stdin/stdout) |
| `tmux-cli windows-create` | Create a new window |
| `tmux-cli windows-list` | List windows |
| `tmux-cli windows-kill` | Kill a window |
| `tmux-cli windows-send` | Send a command to a window |
| `tmux-cli windows-message` | Send a formatted inter-window message |

## MCP server

The install script registers `tmux-cli` as an MCP server in Claude Code automatically. It exposes 10 tools that Claude uses internally for the `/tmux:plan` and `/tmux:supervisor` protocols:

`windows-list`, `windows-create`, `windows-send`, `windows-kill`, `windows-message`, `windows-spawn-worker`, `windows-recover-workers`, `tasks-validate`, `spec-validate`, `hooks-config`

## Support

If you find this useful, you can [buy me a coffee](https://buymeacoffee.com/veceradev).

## License

MIT
