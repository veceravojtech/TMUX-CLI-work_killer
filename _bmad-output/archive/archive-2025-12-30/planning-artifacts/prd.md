---
stepsCompleted: ['step-01-init', 'step-02-discovery', 'step-03-success', 'step-04-journeys', 'step-07-project-type', 'step-08-scoping', 'step-09-functional', 'step-10-nonfunctional', 'step-11-complete']
inputDocuments:
  - /home/console/PhpstormProjects/CLI/tmux-cli/README.md
  - /home/console/PhpstormProjects/CLI/tmux-cli/coding-rules.md
workflowType: 'prd'
lastStep: 11
workflowComplete: true
completedAt: '2025-12-29'
briefCount: 0
researchCount: 0
brainstormingCount: 0
projectDocsCount: 2
---

# Product Requirements Document - tmux-cli

**Author:** Vojta
**Date:** 2025-12-29

## Executive Summary

**tmux-cli** is a Go-based MCP (Model Context Protocol) server that provides programmatic control of tmux sessions for AI-assisted development. This PRD defines **Phase 2: Core Session Management** - the foundational capability that enables reliable, stateful tmux session control through Claude and other AI assistants.

### The Problem

Current tmux automation is fragile and stateless. Developers (and AI assistants) must:
- Query tmux repeatedly to understand session state
- Use unpredictable session names (session-0, session-1)
- Manually track which sessions belong to which projects
- Have no audit trail of session history
- Risk session ID collisions across systems

This makes AI-assisted tmux control unreliable and difficult to reason about.

### The Solution

Phase 2 introduces **stateful, declarative tmux session management** with:

1. **UUID-based session identification** - Collision-free, predictable session IDs using UUID v4
2. **JSON session store** - Single source of truth tracking session state in `~/.tmux-cli/sessions/`
3. **Project-scoped sessions** - Each session tied to a specific project path
4. **Idempotent operations** - Safe session creation (create new or connect to existing)
5. **Real-time state synchronization** - Every tmux operation updates the JSON store immediately
6. **Session lifecycle management** - Automatic archival of ended sessions to `~/.tmux-cli/sessions/ended/`
7. **Hook-based cleanup** - When tmux session terminates, session file automatically moves to archive

### What Makes This Special

This transforms tmux from "run commands and hope" to "declare state and verify." Instead of ephemeral session control, tmux-cli provides:

- **Declarative session management** - The JSON file represents the desired/actual state
- **Audit trail** - Complete history of all sessions (active + ended)
- **Easy debugging** - Just read the JSON file to understand session state
- **AI-friendly** - Claude can reliably manage sessions without state uncertainty
- **Developer-friendly** - Simple file-based state that's easy to inspect and understand

The session store acts as a **database-like persistence layer** for tmux, making session state observable, predictable, and automatable.

## Project Classification

**Technical Type:** developer_tool (with CLI characteristics)
**Domain:** general (developer tooling)
**Complexity:** low
**Project Context:** Brownfield - extending existing tmux-cli skeleton

**Existing Foundation:**
- Go 1.21+ with strict TDD practices (>80% coverage target)
- MCP server architecture (planned)
- Test-first development workflow (Red → Green → Refactor)
- Internal package structure (`internal/tmux`, `internal/mcp`, `internal/claude`)

**Phase 2 Integration:**
- Implements core session management in `internal/tmux` package
- Follows existing TDD practices (tests first, always)
- Builds foundation for Phase 3 (window/pane control) and Phase 4 (MCP server implementation)
- Maintains architectural consistency with planned MCP integration

## Success Criteria

### User Success

**Phase 2 succeeds when:**

✅ **Session Persistence** - "I can start a session with UUID, disconnect, come back days later, and it just works"
- Create session with `tmux-cli session start --id {uuid} --path /project`
- Session file persists in `~/.tmux-cli/sessions/{uuid}.json`
- Accessing killed session triggers automatic recovery

✅ **Automatic Recovery** - "Killed sessions resurrect themselves when I try to use them"
- All windows recreate using their recovery commands
- Windows get same tmux IDs (@0, @1...) and names
- Recovery verification confirms all windows are running with correct identifiers
- May take time to verify - acceptable for reliability

✅ **State Transparency** - "The JSON file tells me exactly what's in my session"
- Session file accurately reflects tmux actual state
- Simple structure: tmux window ID + name + recovery command
- No custom IDs or redundant data
- Easy to inspect and debug

✅ **Reliable Window Management** - "Windows are tracked with tmux-native identifiers"
- Each window has tmux's unique @ID (@0, @1, @2...)
- Human-readable names for context
- Recovery commands capture window state
- Window operations can fail inside (no complex error handling needed)

✅ **Inter-Window Communication** - "Claude instances can send status messages to each other"
- Worker window runs: `tmux-cli session --id $UUID send --window-id @0 --message "Task complete"`
- Main window (pane 0) receives the message as typed input
- System validates window exists before sending
- Clear errors if session killed or window not found

### Business Success

**Personal Productivity Milestones:**

**3 Months:**
- Phase 2 foundation is solid and reliable
- Can confidently build Phase 3 (macros/actions) on top
- Using tmux-cli daily for project session management

**6 Months:**
- Supervisor daemon built on Phase 2 foundation
- Multiple Claude instances coordinating through tmux
- AI-assisted development workflow is operational

**Success Metric:** "I trust the session management enough to not manually verify tmux state"

### Technical Success

**Code Quality:**
- ✅ Strict TDD maintained (>80% test coverage)
- ✅ All tests pass (Red → Green → Refactor cycle)
- ✅ Go best practices followed
- ✅ Idempotent operations (safe to retry commands)

**System Reliability:**
- ✅ Session recovery works 100% of the time
- ✅ All windows recreate with correct @IDs and names
- ✅ JSON session store never gets out of sync with tmux
- ✅ Recovery verification confirms all windows running

**Architecture:**
- ✅ JSON format is stable and minimal (only tmux-native IDs)
- ✅ Session file structure supports Phase 3 expansion
- ✅ Clean separation: CLI → daemon → Claude (future)

### Measurable Outcomes

**Reliability Metrics:**
- Session create/kill/recovery cycle: **100% success rate**
- Window recreation accuracy: **All windows with correct @IDs**
- State synchronization: **JSON always matches tmux reality**
- Recovery verification: **All windows confirmed running before proceeding**

**Developer Experience:**
- Session operations: **Subsecond for create/kill**
- Recovery time: **Acceptable delay for verification (may take seconds)**
- Error messages: **Clear indication when operations fail inside windows**
- Debugging: **Can cat session file to understand state**

## Product Scope

### MVP - Phase 2: Core Session Management

**In Scope:**

1. **Session Lifecycle**
   - Create session with UUID v4 + project path
   - Kill session (file persists for recovery)
   - Explicit end session (move file to `ended/`)
   - Auto-recovery on access to killed session

2. **Window Management**
   - Create windows in session
   - Generate tmux window ID (@0, @1...)
   - Set human-readable name (also sets tmux window_name)
   - Store recovery command for each window

3. **Inter-Window Communication**
   - Send text messages to specific windows by window ID
   - Validate target window exists in session JSON
   - Validate session is running before sending
   - Deliver messages to window's first pane with automatic Enter

4. **Session Store**
   - JSON files in `~/.tmux-cli/sessions/`
   - Ended sessions in `~/.tmux-cli/sessions/ended/`
   - Minimal structure: `{sessionId, projectPath, windows: [{tmuxWindowId, name, recoveryCommand}]}`

5. **Recovery System**
   - Detect killed session on access attempt
   - Recreate session with original UUID
   - Recreate all windows using recovery commands
   - Verify all windows running with correct identifiers
   - No error handling for operations inside windows (let them fail)

**Success Validation:**
- Can create session, add windows, kill it, access it → auto-recovery works
- All windows recreate with same @IDs, names, and recovery commands executed
- JSON file always reflects actual tmux state
- Can send messages between windows in same session with validation

### Growth Features - Phase 3: Control & Automation

**Out of Scope for Phase 2:**

1. **Macro System**
   - User-defined macros in `macros.yaml`
   - Execute macros in specific windows
   - Macro command: `tmux-cli session --id {uuid} windows macro {name} {window-id}`

2. **Action System**
   - Paste buffer to specific windows
   - Send keys to specific windows
   - Built-in actions in `tmux-actions.yaml`

3. **Advanced Window Control**
   - Pane splitting
   - Layout management
   - Window movement/reordering

### Vision - Phase 4+: AI Orchestration

**Future Capabilities:**

1. **Supervisor Daemon**
   - Long-running process managing tmux-cli
   - Claude supervisor coordinating multiple windows
   - Window list awareness for task distribution

2. **Multi-Agent Claude**
   - Claude instance per window
   - Supervisor delegates tasks to window instances
   - Inter-instance coordination through supervisor
   - Text injection and macro execution across windows

3. **Production Workflows**
   - AI-assisted development workflows
   - Automated testing and deployment
   - Multi-window task parallelization

## User Journeys

### Journey: Vojta - The Session That Wouldn't Die

Vojta is deep into building tmux-cli Phase 2, coding in one tmux window while Claude runs tests in another. He's been working for hours, and the session state is perfect - editor positioned just right, test output visible, everything flowing. Then his laptop battery dies unexpectedly. When he boots back up, his heart sinks - all that carefully arranged tmux state, gone.

Or so he thinks.

The next morning, still frustrated about losing yesterday's setup, he decides to test the recovery feature he built. He runs:

```bash
tmux-cli session --id abc-123 windows list
```

expecting an error. Instead, tmux-cli detects the dead session, reads the JSON file from `~/.tmux-cli/sessions/abc-123.json`, and quietly starts recreating everything. Window @0 opens with vim. Window @1 opens with the test runner. Within 10 seconds, his entire workspace is back - same UUIDs, same layout, same recovery commands executed.

The breakthrough comes a week later when he's switching between three different projects throughout the day. Each project has its own session with UUID. He doesn't think about "saving state" or "restoring windows" anymore - he just works. When a session dies (and they do - crashes, reboots, accidents), the recovery is invisible. He types a command expecting to interact with the session, and it just... works.

Three months later, he's building the Phase 3 macro system on top of this foundation, confident that the session management underneath is rock-solid. The supervisor daemon he's planning for Phase 4 will rely on this same recovery mechanism. He finally has the reliable tmux control he needs for AI-assisted development.

### Journey Addition: Claude-to-Claude Coordination

Three months into using tmux-cli, Vojta is ready to test his supervisor Claude workflow. He creates a session with two windows:

```bash
SESSION_ID=$(uuidgen)
tmux-cli session start --id $SESSION_ID --path /project

# Window @0: Main Claude (supervisor)
tmux-cli session --id $SESSION_ID windows create \
  --name "supervisor" \
  --command "claude code"

# Window @1: Worker Claude
tmux-cli session --id $SESSION_ID windows create \
  --name "worker" \
  --command "claude code"
```

The supervisor Claude delegates a task to the worker window, then waits. The worker Claude completes the implementation and needs to report back. It runs:

```bash
tmux-cli session --id $SESSION_ID send \
  --window-id @0 \
  --message "Worker: Unit tests passing, feature complete"
```

In window @0, the supervisor Claude's terminal receives this as if Vojta had typed it. The supervisor sees the status update, verifies the work, and delegates the next task. The communication is reliable because tmux-cli validates that window @0 exists in the session JSON before sending.

When Vojta accidentally kills the session and it auto-recovers, the same send commands still work - the window IDs are preserved, and the communication pipeline stays intact.

### Journey Requirements Summary

This core journey reveals the essential Phase 2 capabilities:

**Session Management:**
- Create session with UUID v4 + project path
- Kill session (file persists for recovery)
- Explicit end session (move to `ended/` directory)

**Window Management:**
- Create windows with tmux-native @IDs
- Assign human-readable names
- Store recovery commands for each window
- Track window state in JSON format

**Inter-Window Communication:**
- Send messages scoped to session context
- Validate target window exists in JSON
- Validate session is running
- Messages delivered to first pane of target window
- Consistent behavior across recovery cycles

**Auto-Recovery System:**
- Detect dead sessions on access attempt
- Recreate session with original UUID
- Recreate all windows using stored recovery commands
- Verify all windows running with correct identifiers
- Transparent to user (no manual recovery invocation)

**Developer Experience:**
- Subsecond session create/kill operations
- Inspectable JSON session files
- Reliable recovery (100% success rate)
- No manual intervention needed

## Developer Tool Specific Requirements

### Project-Type Overview

tmux-cli is a **developer tool** built in Go, providing a CLI interface for programmatic tmux session management. Phase 2 establishes the foundation with core session and window control commands, designed for integration with future daemon processes and AI assistants.

### Technical Architecture Considerations

**Language & Runtime:**
- Go 1.21+ (single runtime dependency)
- No external language bindings in Phase 2
- Future: Consider Go package API for daemon integration (Phase 4+)

**Distribution Model:**
- Compiled binary (single executable)
- Static linking for portability
- No runtime dependencies beyond tmux itself

### Installation Methods

**Phase 2 MVP Installation:**

1. **Local Build & Install:**
   ```bash
   make build
   make install  # Installs to ~/.local/bin
   ```

2. **Binary Distribution:**
   - Pre-compiled binaries for Linux, macOS
   - Direct download and place in PATH
   - No installer needed

**Future Considerations (Post-Phase 2):**
- Package managers (homebrew, apt, yum)
- GitHub releases with auto-generated binaries
- Installation script for automated setup

### CLI Command Surface (Phase 2)

**Session Management:**
```bash
tmux-cli session start --id <uuid> --path <project-path>
tmux-cli session kill --id <uuid>
tmux-cli session end --id <uuid>  # Move to ended/
tmux-cli session list
tmux-cli session status --id <uuid>
```

**Window Management:**
```bash
tmux-cli session --id <uuid> windows create --name <name> --command <recovery-cmd>
tmux-cli session --id <uuid> windows list
tmux-cli session --id <uuid> windows get --window-id <@N>
```

**Inter-Window Communication:**
```bash
tmux-cli session --id <uuid> send --window-id <@N> --message "text message"
```

**Command Behavior:**
- Validates session exists and is running
- Validates window @N exists in session JSON
- Sends message to window's first pane
- Automatically appends Enter key
- Returns exit code 0 on success, 1 on failure

**Recovery Operations:**
- Auto-recovery triggered on access to killed session
- No explicit recovery command (transparent)

### Documentation Requirements

**Critical for Phase 2:**

1. **CLI Reference:**
   - Command syntax and options
   - Exit codes and error messages
   - Usage examples for each command

2. **JSON Session Format Specification:**
   ```json
   {
     "sessionId": "uuid-v4",
     "projectPath": "/absolute/path",
     "windows": [
       {
         "tmuxWindowId": "@0",
         "name": "human-readable-name",
         "recoveryCommand": "command to run"
       }
     ]
   }
   ```

3. **Recovery Behavior Documentation:**
   - When auto-recovery triggers
   - Verification process
   - What happens if recovery fails

4. **Go Package Documentation:**
   - Public API surface (for future daemon integration)
   - Internal package structure
   - Extension points

### Code Examples

**Essential Examples for Phase 2:**

1. **Basic Session Workflow:**
   ```bash
   # Create session
   tmux-cli session start --id $(uuidgen) --path /project

   # Add windows
   tmux-cli session --id <uuid> windows create --name "editor" --command "vim main.go"
   tmux-cli session --id <uuid> windows create --name "tests" --command "go test -watch"

   # List windows
   tmux-cli session --id <uuid> windows list

   # Kill and auto-recover
   tmux-cli session kill --id <uuid>
   tmux-cli session --id <uuid> windows list  # Triggers recovery
   ```

2. **JSON Session File Example:**
   - Show complete session file structure
   - Document each field's purpose
   - Explain window ID correlation

3. **Recovery Scenario:**
   - Demonstrate session dies (crash, reboot)
   - Show auto-recovery on next access
   - Verify windows restored correctly

4. **Inter-Window Communication:**
   ```bash
   # Main Claude in window @0 creates worker window
   tmux-cli session --id $UUID windows create \
     --name "worker" \
     --command "claude code --task 'run tests'"

   # Worker completes and sends status to supervisor
   tmux-cli session --id $UUID send \
     --window-id @0 \
     --message "Worker: All tests passing"

   # Error handling - window doesn't exist
   tmux-cli session --id $UUID send \
     --window-id @99 \
     --message "test"
   # Error: Window @99 not found in session

   # Error handling - session killed
   tmux-cli session kill --id $UUID
   tmux-cli session --id $UUID send \
     --window-id @0 \
     --message "test"
   # Error: Session not running (triggers auto-recovery first)
   ```

### Implementation Considerations

**TDD Requirements:**
- All CLI commands test-driven (>80% coverage)
- Mock tmux interactions in unit tests
- Integration tests for actual tmux operations
- Test recovery scenarios explicitly

**Error Handling:**
- Clear error messages for common failures
- Exit codes follow POSIX conventions
- JSON parsing errors handled gracefully

**Performance:**
- Session create/kill: subsecond
- Recovery: acceptable delay for verification (may take seconds)
- Window operations: subsecond

**Inter-Window Communication:**
- Validate session running state before send
- Verify target window exists in session JSON
- Use tmux target format: `<session-id>:<window-id>`
- Send to first pane of window (`.0` pane)
- Automatic Enter key appended to all messages
- Clear error messages for validation failures

## Functional Requirements

### Session Lifecycle Management

- **FR1:** Developer can create a new tmux session with a UUID v4 identifier and project path
- **FR2:** Developer can kill a tmux session while preserving its session file for recovery
- **FR3:** Developer can explicitly end a session, moving its file to the ended directory
- **FR4:** Developer can list all active sessions
- **FR5:** Developer can check the status of a specific session by UUID

### Window Management

- **FR6:** Developer can create a new window in a session with a human-readable name
- **FR7:** Developer can specify a recovery command when creating a window
- **FR8:** Developer can list all windows in a session with their tmux IDs and names
- **FR9:** Developer can retrieve details of a specific window by its tmux window ID
- **FR10:** System assigns tmux-native window IDs (@0, @1, @2...) to each window

### Session Recovery

- **FR11:** System automatically detects when a session is killed but has a persisted session file
- **FR12:** System automatically recreates a killed session when the developer attempts to access it
- **FR13:** System recreates all windows from the session file using their stored recovery commands
- **FR14:** System verifies all windows are running with correct identifiers after recovery
- **FR15:** System preserves the original session UUID and window identifiers during recovery
- **FR16:** Developer experiences transparent recovery without manual intervention

### Session State Persistence

- **FR17:** System stores session state in JSON format at `~/.tmux-cli/sessions/{uuid}.json`
- **FR18:** System stores sessionId, projectPath, and windows array in session file
- **FR19:** System stores tmuxWindowId, name, and recoveryCommand for each window
- **FR20:** System updates session file in real-time when windows are created or modified
- **FR21:** System moves session files to `~/.tmux-cli/sessions/ended/` when explicitly ended
- **FR22:** System maintains session files in active directory for recovery capability

### Session Discovery & Inspection

- **FR23:** Developer can inspect session state by reading the JSON session file directly
- **FR24:** Developer can determine if a session is active or ended by file location
- **FR25:** System provides clear session file structure for easy manual inspection
- **FR26:** Developer can distinguish between active sessions and ended sessions

### CLI Interface

- **FR27:** Developer can execute all session operations via command-line interface
- **FR28:** System provides clear error messages when operations fail
- **FR29:** System follows POSIX exit code conventions for success/failure
- **FR30:** System accepts session UUID and project path as command arguments
- **FR31:** System accepts window names and recovery commands as command arguments

### Inter-Window Communication

- **FR32:** Developer can send text messages to specific windows within a session using window IDs
- **FR33:** System validates that target window exists in the session JSON file before sending
- **FR34:** System validates that the session is running (not killed) before sending messages
- **FR35:** System executes `tmux send-keys -t <session>:<window> "<message>" Enter` to deliver messages
- **FR36:** Send operations return clear errors if session is killed or window doesn't exist
- **FR37:** Messages are delivered to the first pane of the target window
- **FR38:** System provides success/failure feedback for send operations

## Non-Functional Requirements

### Performance

- **NFR1:** Session create operations complete within 1 second
- **NFR2:** Session kill operations complete within 1 second
- **NFR3:** Window create operations complete within 1 second
- **NFR4:** Session list and status queries return within 500ms
- **NFR5:** Recovery operations may take longer but must complete within 30 seconds for verification

### Reliability

- **NFR6:** Session recovery succeeds 100% of the time for valid session files
- **NFR7:** All windows recreate with correct tmux IDs during recovery
- **NFR8:** Session state in JSON files always matches actual tmux state
- **NFR9:** System never loses session data when file writes succeed
- **NFR10:** Recovery verification confirms all windows running before reporting success

### Maintainability

- **NFR11:** All code maintains >80% test coverage per TDD practices
- **NFR12:** All tests pass before code is committed
- **NFR13:** Code follows Go best practices (gofmt, go vet, golint)
- **NFR14:** Functions maintain cyclomatic complexity <10
- **NFR15:** Public API is documented with Go doc comments

### Data Integrity

- **NFR16:** Session files use valid JSON format at all times
- **NFR17:** Session file writes are atomic (no partial writes)
- **NFR18:** Recovery commands are stored exactly as provided by user
- **NFR19:** Window IDs remain stable across recovery operations
- **NFR20:** File moves to ended/ directory preserve all session data

### Integration

- **NFR21:** System works with tmux 2.0+ (specify minimum version)
- **NFR22:** All tmux commands handle errors gracefully
- **NFR23:** System detects if tmux is not installed and provides clear error
- **NFR24:** Compatible with Linux and macOS tmux implementations
- **NFR25:** Future daemon integration possible through Go package API

### Inter-Window Communication

- **NFR26:** Send operations complete within 500ms
- **NFR27:** System validates window existence before attempting send
- **NFR28:** Send operations fail gracefully with clear errors when session is killed
- **NFR29:** Message delivery is atomic (all-or-nothing, no partial sends)

