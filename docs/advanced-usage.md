# Advanced usage

## Settings reference

Run `tmux-cli setting` to open the TUI. Toggle with arrow keys and Enter.

| Setting | Default | Description |
|---------|---------|-------------|
| Session Notify | off | Log session start/stop events |
| Block Interactive | on | Block interactive commands in worker windows |
| Commands Enabled | on | Install `/tmux:plan`, `/tmux:supervisor`, `/tmux:execute` slash commands |
| Max Workers | 4 | Maximum parallel worker windows |
| Cycle Timeout (s) | 5 | Delay in seconds between supervisor cycles |
| Unplanned Audit | on | Require audit before unplanned supervisor runs |
| Plan Auto-Approve | on | Skip human approval of generated task plans |
| Plan Auto-Execute | on | Automatically start implementation after planning |
| Taskvisor Transient Retry Max Attempts | 3 | Total preflight/probe attempts before a transient infra failure escalates to a `blocked`/`infra-flake` finding (`transient_retry_max_attempts`; 1 = no retry) |
| Taskvisor Transient Retry Backoff (ms) | 500 | Delay between transient-failure retry attempts in milliseconds (`transient_retry_backoff_ms`; N attempts ⇒ N-1 sleeps) |

Settings are stored in `.tmux-cli/setting.yaml` per project.

## Planning and implementation (`/tmux:plan`)

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

## Direct execution (`/tmux:supervisor`)

Use when you already have context and want Claude to execute immediately — no planning phase.

```
/tmux:supervisor do deep reverse engineering of firmware.bin
```

What happens:
1. The supervisor spawns parallel worker windows
2. Each worker receives its task and runs independently
3. Workers report results back via tagged messages
4. The supervisor collects results and presents them

## Examples

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

## All commands

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

## MCP tools

The install script registers `tmux-cli` as an MCP server in Claude Code automatically. It exposes 10 tools that Claude uses internally for the `/tmux:plan` and `/tmux:supervisor` protocols:

`windows-list`, `windows-create`, `windows-send`, `windows-kill`, `windows-message`, `windows-spawn-worker`, `windows-recover-workers`, `tasks-validate`, `spec-validate`, `hooks-config`

## Restart protocol

After running `make install` (or replacing the `tmux-cli` binary by any other means), restart both:

1. **MCP server** — the Claude Code process caches the binary path; a new `tmux-cli mcp` must be spawned for the updated binary to serve tool calls.
2. **Taskvisor daemon** — if `tmux-cli taskvisor --run` is active, kill and restart it so the daemon runs the new code.

The stale-binary guard (see Taskvisor Spec) warns when the binary has changed — the dashboard shows a `BINARY STALE` banner and every MCP tool response is prefixed with a warning — but the warning is a detection prompt, not an auto-reload mechanism. You must still restart manually.

## Task tracking

The supervisor uses `.tmux-cli/tasks.yaml` as a persistent task queue:

- Tasks have statuses: `pending`, `in_progress`, `done`
- Each task points to a context `.md` file in `.tmux-cli/research/`
- The `cycle` counter tracks how many supervisor cycles have run
- `supervisor.max_cycles` in settings controls the limit (0 = unlimited)
