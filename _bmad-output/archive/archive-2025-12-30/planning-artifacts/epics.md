---
stepsCompleted: ['step-01-validate-prerequisites', 'step-02-design-epics', 'step-03-create-stories', 'step-04-final-validation']
workflowComplete: true
completedAt: '2025-12-29'
inputDocuments:
  - /home/console/PhpstormProjects/CLI/tmux-cli/_bmad-output/planning-artifacts/prd.md
  - /home/console/PhpstormProjects/CLI/tmux-cli/_bmad-output/planning-artifacts/architecture.md
  - /home/console/PhpstormProjects/CLI/tmux-cli/_bmad-output/planning-artifacts/coding-rules.md
totalEpics: 3
totalStories: 12
requirementsCoverage:
  functionalRequirements: 38
  nonFunctionalRequirements: 29
  additionalRequirements: 26
  coveragePercentage: 100
---

# tmux-cli - Epic Breakdown

## Overview

This document provides the complete epic and story breakdown for tmux-cli, decomposing the requirements from the PRD, Architecture, and Coding Rules into implementable stories.

## Requirements Inventory

### Functional Requirements

- **FR1:** Developer can create a new tmux session with a UUID v4 identifier and project path
- **FR2:** Developer can kill a tmux session while preserving its session file for recovery
- **FR3:** Developer can explicitly end a session, moving its file to the ended directory
- **FR4:** Developer can list all active sessions
- **FR5:** Developer can check the status of a specific session by UUID
- **FR6:** Developer can create a new window in a session with a human-readable name
- **FR7:** Developer can specify a recovery command when creating a window
- **FR8:** Developer can list all windows in a session with their tmux IDs and names
- **FR9:** Developer can retrieve details of a specific window by its tmux window ID
- **FR10:** System assigns tmux-native window IDs (@0, @1, @2...) to each window
- **FR11:** System automatically detects when a session is killed but has a persisted session file
- **FR12:** System automatically recreates a killed session when the developer attempts to access it
- **FR13:** System recreates all windows from the session file using their stored recovery commands
- **FR14:** System verifies all windows are running with correct identifiers after recovery
- **FR15:** System preserves the original session UUID and window identifiers during recovery
- **FR16:** Developer experiences transparent recovery without manual intervention
- **FR17:** System stores session state in JSON format at `~/.tmux-cli/sessions/{uuid}.json`
- **FR18:** System stores sessionId, projectPath, and windows array in session file
- **FR19:** System stores tmuxWindowId, name, and recoveryCommand for each window
- **FR20:** System updates session file in real-time when windows are created or modified
- **FR21:** System moves session files to `~/.tmux-cli/sessions/ended/` when explicitly ended
- **FR22:** System maintains session files in active directory for recovery capability
- **FR23:** Developer can inspect session state by reading the JSON session file directly
- **FR24:** Developer can determine if a session is active or ended by file location
- **FR25:** System provides clear session file structure for easy manual inspection
- **FR26:** Developer can distinguish between active sessions and ended sessions
- **FR27:** Developer can execute all session operations via command-line interface
- **FR28:** System provides clear error messages when operations fail
- **FR29:** System follows POSIX exit code conventions for success/failure
- **FR30:** System accepts session UUID and project path as command arguments
- **FR31:** System accepts window names and recovery commands as command arguments
- **FR32:** Developer can send text messages to specific windows within a session using window IDs
- **FR33:** System validates that target window exists in the session JSON file before sending
- **FR34:** System validates that the session is running (not killed) before sending messages
- **FR35:** System executes `tmux send-keys -t <session>:<window> "<message>" Enter` to deliver messages
- **FR36:** Send operations return clear errors if session is killed or window doesn't exist
- **FR37:** Messages are delivered to the first pane of the target window
- **FR38:** System provides success/failure feedback for send operations

### NonFunctional Requirements

**Performance (NFR1-NFR5):**
- **NFR1:** Session create operations complete within 1 second
- **NFR2:** Session kill operations complete within 1 second
- **NFR3:** Window create operations complete within 1 second
- **NFR4:** Session list and status queries return within 500ms
- **NFR5:** Recovery operations may take longer but must complete within 30 seconds for verification

**Reliability (NFR6-NFR10):**
- **NFR6:** Session recovery succeeds 100% of the time for valid session files
- **NFR7:** All windows recreate with correct tmux IDs during recovery
- **NFR8:** Session state in JSON files always matches actual tmux state
- **NFR9:** System never loses session data when file writes succeed
- **NFR10:** Recovery verification confirms all windows running before reporting success

**Maintainability (NFR11-NFR15):**
- **NFR11:** All code maintains >80% test coverage per TDD practices
- **NFR12:** All tests pass before code is committed
- **NFR13:** Code follows Go best practices (gofmt, go vet, golint)
- **NFR14:** Functions maintain cyclomatic complexity <10
- **NFR15:** Public API is documented with Go doc comments

**Data Integrity (NFR16-NFR20):**
- **NFR16:** Session files use valid JSON format at all times
- **NFR17:** Session file writes are atomic (no partial writes)
- **NFR18:** Recovery commands are stored exactly as provided by user
- **NFR19:** Window IDs remain stable across recovery operations
- **NFR20:** File moves to ended/ directory preserve all session data

**Integration (NFR21-NFR25):**
- **NFR21:** System works with tmux 2.0+ (specify minimum version)
- **NFR22:** All tmux commands handle errors gracefully
- **NFR23:** System detects if tmux is not installed and provides clear error
- **NFR24:** Compatible with Linux and macOS tmux implementations
- **NFR25:** Future daemon integration possible through Go package API

**Inter-Window Communication (NFR26-NFR29):**
- **NFR26:** Send operations complete within 500ms
- **NFR27:** System validates window existence before attempting send
- **NFR28:** Send operations fail gracefully with clear errors when session is killed
- **NFR29:** Message delivery is atomic (all-or-nothing, no partial sends)

### Additional Requirements

**Architecture Requirements:**
- **AR1:** Use Cobra CLI framework for command structure (manually integrated into existing Go skeleton)
- **AR2:** Go 1.21+ runtime requirement (strict)
- **AR3:** Standard library preference for Phase 2 with justified exceptions:
  - `github.com/google/uuid` for RFC 4122 compliant UUID v4 generation
  - `github.com/spf13/cobra` for CLI framework
  - `github.com/stretchr/testify` for structured mocking in tests
- **AR4:** Atomic file writes using temp file + rename pattern for all session file operations
- **AR5:** Three-tier testing strategy:
  - Unit tests (fast, mocked, no build tag) - default `make test`
  - Real tmux tests (build tag: `tmux`) - `make test-tmux`
  - Integration tests (build tag: `integration`) - `make test-all`
- **AR6:** JSON session store at `~/.tmux-cli/sessions/` with `ended/` subdirectory for archived sessions
- **AR7:** TmuxExecutor interface for dependency injection and testing (enables mocking)
- **AR8:** POSIX exit code conventions:
  - 0 = success
  - 1 = general error (session not found, tmux command failed, JSON parse error)
  - 2 = usage error (missing required flags, invalid arguments)
  - 126 = command not found (tmux not installed or not in PATH)
- **AR9:** Minimal logging for Phase 2 (errors to stderr, output to stdout, no logging framework)
- **AR10:** Package structure:
  - `cmd/tmux-cli/` - CLI command layer (Cobra commands)
  - `internal/store/` - Session state persistence (JSON operations, atomic writes)
  - `internal/tmux/` - Tmux command execution interface and implementation
  - `internal/recovery/` - Session recovery logic
  - `internal/testutil/` - Shared test utilities (mocks, fixtures, helpers)

**Coding Rules Requirements:**
- **CR1:** Strict TDD: Red → Green → Refactor cycle mandatory (write failing test before any code)
- **CR2:** Test coverage >80% mandatory for all packages
- **CR3:** Test naming convention: `TestFunctionName_Scenario_ExpectedBehavior`
- **CR4:** Table-driven tests where appropriate for comprehensive scenario coverage
- **CR5:** Mock external dependencies (tmux commands, file system) in unit tests
- **CR6:** Integration tests use build tag `// +build integration`
- **CR7:** Test helpers organized in `internal/testutil/` package
- **CR8:** All commits must have passing tests before submission
- **CR9:** Maximum function cyclomatic complexity: 10
- **CR10:** Keep functions small and focused (ideally <50 lines)
- **CR11:** Use Go formatting standards (gofmt, golint, go vet) - enforced via Makefile
- **CR12:** Return errors explicitly (never panic except unrecoverable initialization failures)
- **CR13:** Constants use CamelCase (not SCREAMING_CASE) per Go conventions
- **CR14:** Unit tests must run quickly (<5 seconds total for suite)
- **CR15:** Tests must be deterministic (no flaky tests allowed)
- **CR16:** Tests must clean up after themselves (no leftover files or tmux sessions)

### FR Coverage Map

**Epic 1 - Persistent Session Management:**
- FR1: Create session with UUID v4 + project path
- FR2: Kill session (preserves file)
- FR3: End session (move to ended/)
- FR4: List all active sessions
- FR5: Check session status by UUID
- FR17: JSON storage at ~/.tmux-cli/sessions/
- FR18: Store sessionId, projectPath, windows array
- FR23: Inspect session via JSON file
- FR24: Determine active vs ended by location
- FR25: Clear JSON structure for inspection
- FR26: Distinguish active/ended sessions
- FR27: CLI interface for all operations
- FR28: Clear error messages
- FR29: POSIX exit codes
- FR30: Accept UUID and path arguments

**Epic 2 - Window Management with Recovery Commands:**
- FR6: Create window with human-readable name
- FR7: Specify recovery command
- FR8: List windows with tmux IDs and names
- FR9: Get window details by tmux window ID
- FR10: System assigns tmux-native IDs (@0, @1...)
- FR19: Store tmux window ID, name, recovery command
- FR20: Real-time session file updates
- FR21: Move session files to ended/ when ended
- FR22: Maintain files in active directory
- FR31: Accept window names and recovery commands as arguments

**Epic 3 - Automatic Session Recovery:**
- FR11: Auto-detect killed sessions with persisted files
- FR12: Auto-recreate on access attempt
- FR13: Recreate windows using recovery commands
- FR14: Verify windows running with correct IDs
- FR15: Preserve original UUIDs and window IDs
- FR16: Transparent recovery (no manual intervention)
- FR32: Send text messages to specific windows using window IDs
- FR33: Validate target window exists in session JSON
- FR34: Validate session is running before sending
- FR35: Execute tmux send-keys command to deliver messages
- FR36: Clear errors if session killed or window doesn't exist
- FR37: Messages delivered to first pane of target window
- FR38: Success/failure feedback for send operations

**All Epics (Cross-Cutting Requirements):**
- NFR1-NFR29: Performance, reliability, maintainability, data integrity, integration, inter-window communication requirements
- AR1-AR10: Architecture patterns (Cobra, Go 1.21+, atomic writes, testing strategy, package structure)
- CR1-CR16: TDD practices, test coverage, Go standards, test organization

## Epic List

### Epic 1: Persistent Session Management

**Goal:** Developers can create, manage, and inspect persistent tmux sessions with UUID-based identification and JSON state storage.

**User Outcome:** You can create tmux sessions with UUIDs, manage their lifecycle (kill, end), list all sessions, check status, and inspect session state via JSON files. Sessions persist to disk and provide a foundation for window management and recovery.

**FRs Covered:** FR1, FR2, FR3, FR4, FR5, FR17, FR18, FR23, FR24, FR25, FR26, FR27, FR28, FR29, FR30

**Implementation Notes:**
- Includes foundational setup: Cobra CLI framework, project structure, testing infrastructure
- Implements JSON session store with atomic writes
- Establishes CLI command patterns
- All architecture requirements (AR1-AR10) and coding standards (CR1-CR16) apply
- TDD from the start with >80% coverage

### Epic 2: Window Management with Recovery Commands

**Goal:** Developers can create and manage windows within sessions, each with human-readable names and recovery commands that are persisted.

**User Outcome:** You can create windows in sessions, assign names, specify recovery commands, list windows with their tmux IDs, and retrieve window details. Window state is persisted in the session JSON file for future recovery.

**FRs Covered:** FR6, FR7, FR8, FR9, FR10, FR19, FR20, FR21, FR22, FR31

**Implementation Notes:**
- Builds upon Epic 1's session management foundation
- Implements window lifecycle within sessions
- Stores window metadata (tmux ID, name, recovery command) in session JSON
- Handles session archival to ended/ directory
- Standalone: Complete window management capability

### Epic 3: Automatic Session Recovery & Inter-Window Communication

**Goal:** When a tmux session is killed (crash, reboot, accident), it automatically resurrects itself when you try to access it - completely transparent. Additionally, enable Claude instances in different windows to communicate with each other for AI-assisted workflow coordination.

**User Outcome:** You experience "the session that wouldn't die." Killed sessions are automatically detected and recreated with all their windows when you attempt any operation. Recovery is transparent - no manual intervention required. Claude instances running in different windows can send status messages to each other, enabling supervisor-worker coordination patterns.

**FRs Covered:** FR11, FR12, FR13, FR14, FR15, FR16, FR32, FR33, FR34, FR35, FR36, FR37, FR38

**Implementation Notes:**
- Builds upon Epic 1 (sessions) and Epic 2 (windows)
- Implements detection of killed sessions
- Auto-recovery trigger on any access attempt
- Window recreation using stored recovery commands
- Verification that all windows are running correctly
- Inter-window messaging for Claude-to-Claude coordination
- Session and window validation before message delivery
- Standalone: Complete automatic recovery system with inter-window communication

## Epic 1: Persistent Session Management

**Goal:** Developers can create, manage, and inspect persistent tmux sessions with UUID-based identification and JSON state storage.

### Story 1.1: Project Foundation & CLI Framework Setup

As a developer,
I want the project structure, Cobra CLI framework, and testing infrastructure set up,
So that I can build and test tmux-cli commands using established patterns.

**Acceptance Criteria:**

**Given** the existing Go project skeleton
**When** I set up the foundation
**Then** the following structure and capabilities exist:

**And** Cobra CLI framework is integrated:
- `go.mod` includes `github.com/spf13/cobra@latest`
- `cmd/tmux-cli/main.go` initializes Cobra root command
- `cmd/tmux-cli/root.go` defines root command with proper error handling
- Root command returns appropriate exit codes (AR8: 0=success, 1=error, 2=usage, 126=not found)

**And** package structure follows architecture:
- `internal/store/` package created for JSON operations
- `internal/tmux/` package created for tmux execution
- `internal/testutil/` package created for test utilities
- All packages follow Go naming conventions (AR10, singular lowercase)

**And** testing infrastructure is established:
- `github.com/stretchr/testify` dependency added for mocking (AR3, AR5)
- `internal/testutil/mock_tmux.go` defines MockTmuxExecutor interface
- Makefile includes test targets: `test`, `test-tmux`, `test-all` (AR5)
- Unit test example demonstrates TDD pattern (CR1, CR3)

**And** build and install targets work:
- `make build` compiles binary to `bin/tmux-cli`
- `make install` installs to `~/.local/bin/tmux-cli`
- `make test` runs unit tests and completes in <5 seconds (CR14)

**And** code quality standards are enforced:
- `make lint` runs gofmt, go vet, golint (CR11, NFR13)
- All code passes linting with zero errors
- Cyclomatic complexity <10 enforced (CR9, NFR14)

**And** TDD workflow is validated:
- At least one test file exists demonstrating Red→Green→Refactor (CR1)
- Test naming follows convention: `TestFunctionName_Scenario_ExpectedBehavior` (CR3)
- Test coverage report generation works: `make coverage` (CR2)

### Story 1.2: Session Store with Atomic File Operations

As a developer,
I want a JSON session store with atomic file writes,
So that session state is reliably persisted without risk of data corruption.

**Acceptance Criteria:**

**Given** the foundation from Story 1.1 is complete
**When** I implement the session store
**Then** the following capabilities exist:

**And** SessionStore interface is defined in `internal/store/`:
- `Save(session *Session) error` - saves session to JSON
- `Load(id string) (*Session, error)` - loads session from JSON
- `Delete(id string) error` - deletes session file
- `List() ([]*Session, error)` - lists all active sessions
- `Move(id string, destination string) error` - moves session file

**And** Session data structure matches PRD specification (FR18):
```go
type Session struct {
    SessionID   string   `json:"sessionId"`
    ProjectPath string   `json:"projectPath"`
    Windows     []Window `json:"windows"`
}

type Window struct {
    TmuxWindowID    string `json:"tmuxWindowId"`
    Name            string `json:"name"`
    RecoveryCommand string `json:"recoveryCommand"`
}
```

**And** atomic file writes are implemented (AR4, NFR17):
- Creates temp file in same directory: `os.CreateTemp(dir, "session-*.tmp")`
- Writes JSON to temp file with indentation (2 spaces for readability)
- Performs atomic rename: `os.Rename(tmpPath, finalPath)`
- Cleans up temp file on any error
- Unit tests verify no partial writes on simulated crashes

**And** directory management works (AR6):
- `ensureDirectories()` creates `~/.tmux-cli/sessions/` if missing
- `ensureDirectories()` creates `~/.tmux-cli/sessions/ended/` if missing
- Directory permissions set to 0755 (rwxr-xr-x)
- File permissions set to 0644 (rw-r--r--)
- Lazy creation on first use (no manual setup required)

**And** error handling is comprehensive (FR28, AR8):
- `ErrSessionNotFound` sentinel error defined in `internal/store/errors.go`
- File read errors wrapped with context: `fmt.Errorf("read session file: %w", err)`
- JSON parse errors return clear messages
- File system errors return appropriate exit codes

**And** TDD compliance is maintained (CR1-CR5):
- All functions have unit tests written first
- Mock filesystem operations in tests using interfaces
- Table-driven tests cover success and error scenarios
- Test coverage >80% for store package (CR2, NFR11)
- Tests run in <1 second (CR14)

**And** JSON format is validated (NFR16):
- Valid JSON produced in all cases
- Human-readable formatting with indentation
- Matches PRD specification exactly (FR18)
- Unit tests verify JSON can be parsed by standard tools

### Story 1.3: Create Session Command

As a developer,
I want to create a tmux session with UUID and project path,
So that I can start a persistent session tied to a specific project.

**Acceptance Criteria:**

**Given** Stories 1.1 and 1.2 are complete
**When** I implement the create session command
**Then** the following capabilities exist:

**And** TmuxExecutor interface is defined in `internal/tmux/`:
```go
type TmuxExecutor interface {
    CreateSession(id, path string) error
    SessionExists(id string) (bool, error)
    // Future methods for other operations
}
```

**And** RealTmuxExecutor implements the interface:
- `CreateSession()` executes: `tmux new-session -d -s <id> -c <path>`
- Returns error with context if tmux command fails
- Detects if tmux is not installed (NFR23, AR8: exit code 126)
- Unit tests use MockTmuxExecutor (AR7, CR5)
- Real tmux tests tagged with `// +build tmux` (AR5, CR6)

**And** UUID generation is integrated (AR3):
- `github.com/google/uuid` dependency added
- Session IDs use UUID v4: `uuid.New().String()`
- Collision-free session identification (FR1)

**And** `session start` command works (FR1, FR27, FR30):
```bash
tmux-cli session start --id <uuid> --path <project-path>
```
- `--id` flag accepts UUID v4 string (required)
- `--path` flag accepts absolute project path (required)
- Command validates inputs before execution
- Returns exit code 0 on success, 2 on invalid args (AR8, NFR29)

**And** session creation workflow executes (FR1, FR17):
1. Validates UUID format and path exist
2. Creates tmux session via TmuxExecutor
3. Creates Session struct with sessionId and projectPath
4. Saves session to JSON store using atomic write
5. Outputs success message: "Session <uuid> created at <path>"

**And** error handling is comprehensive (FR28):
- Missing `--id` or `--path`: exits with code 2, shows usage
- Invalid UUID format: clear error message, exit code 2
- Path does not exist: error message, exit code 1
- Tmux not installed: "tmux not found. Please install tmux.", exit code 126
- Session already exists: error message, exit code 1
- JSON save failure: error with context, exit code 1

**And** performance meets requirements (NFR1):
- Session creation completes in <1 second
- Unit tests verify timing with benchmarks if needed

**And** TDD compliance is maintained:
- Tests written before implementation (CR1)
- Table-driven tests for various scenarios (CR4)
- Mock tmux executor in unit tests (CR5)
- Real tmux tests verify actual tmux integration (AR5)
- Test coverage >80% (CR2, NFR11)

### Story 1.4: Kill & End Session Commands

As a developer,
I want to kill sessions (preserving files for recovery) or explicitly end sessions (archiving to ended/),
So that I can manage session lifecycle based on whether I need recovery capability.

**Acceptance Criteria:**

**Given** Story 1.3 is complete and sessions can be created
**When** I implement kill and end commands
**Then** the following capabilities exist:

**And** TmuxExecutor interface is extended:
```go
type TmuxExecutor interface {
    // ... existing methods
    KillSession(id string) error
}
```

**And** RealTmuxExecutor implements kill:
- `KillSession()` executes: `tmux kill-session -t <id>`
- Returns error if session doesn't exist in tmux
- Unit tests use MockTmuxExecutor (CR5)

**And** `session kill` command works (FR2, FR22):
```bash
tmux-cli session kill --id <uuid>
```
- Kills the tmux session (session process terminates)
- Session JSON file remains in `~/.tmux-cli/sessions/` (preserves state for recovery)
- Outputs: "Session <uuid> killed (file preserved for recovery)"
- Returns exit code 0 on success, 1 if session not found

**And** `session end` command works (FR3, FR21):
```bash
tmux-cli session end --id <uuid>
```
- Kills the tmux session if it's running
- Moves JSON file from `sessions/` to `sessions/ended/` directory (FR21, NFR20)
- File move preserves all data (atomic operation)
- Outputs: "Session <uuid> ended and archived"
- Returns exit code 0 on success, 1 if session not found

**And** kill workflow executes correctly (FR2):
1. Validates UUID format
2. Checks if session file exists (Load from store)
3. Kills tmux session via TmuxExecutor (ignore error if already dead)
4. Session file remains in active directory
5. Success message displayed

**And** end workflow executes correctly (FR3, FR21):
1. Validates UUID format
2. Loads session from store to verify it exists
3. Kills tmux session if running (ignore error if already dead)
4. Moves session file: `sessions/<uuid>.json` → `sessions/ended/<uuid>.json`
5. Verifies move succeeded (file exists in ended/, not in active)
6. Success message displayed

**And** error handling is comprehensive (FR28):
- Missing `--id`: exits with code 2, shows usage
- Invalid UUID format: error message, exit code 2
- Session file not found: "Session <uuid> not found", exit code 1
- File move failure: error with context, exit code 1
- All errors wrapped with context using `fmt.Errorf("...: %w", err)`

**And** performance meets requirements (NFR2):
- Kill operation completes in <1 second
- End operation completes in <1 second

**And** data integrity is maintained (NFR20):
- File move to ended/ preserves all JSON data
- No data loss during archival
- Unit tests verify JSON content identical after move

**And** TDD compliance is maintained:
- Tests written first (CR1)
- Table-driven tests for both commands (CR4)
- Mock tmux and filesystem operations (CR5)
- Test cleanup removes test files and sessions (CR16)
- Test coverage >80% (CR2)

### Story 1.5: List & Status Commands

As a developer,
I want to list all active sessions and check specific session status,
So that I can discover and inspect my tmux sessions.

**Acceptance Criteria:**

**Given** Stories 1.1-1.4 are complete
**When** I implement list and status commands
**Then** the following capabilities exist:

**And** SessionStore supports listing (FR4, FR23):
- `List()` returns all sessions from `~/.tmux-cli/sessions/` (excludes ended/)
- Reads all `*.json` files in sessions directory
- Parses each JSON file into Session struct
- Returns slice of Session pointers
- Returns error if directory read fails

**And** `session list` command works (FR4, FR23, FR26):
```bash
tmux-cli session list
```
- Lists all active sessions (from sessions/, not ended/)
- Output format:
  ```
  Active Sessions:

  ID: abc-123-def-456
  Path: /home/user/project-1
  Windows: 0

  ID: xyz-789-uvw-012
  Path: /home/user/project-2
  Windows: 2

  Total: 2 active sessions
  ```
- Shows "No active sessions" if directory is empty
- Performance: completes in <500ms (NFR4)
- Exit code 0 always (success even if no sessions)

**And** `session status` command works (FR5, FR24, FR25):
```bash
tmux-cli session status --id <uuid>
```
- Displays detailed information about a specific session
- Output format:
  ```
  Session Status:

  ID: abc-123-def-456
  Path: /home/user/project-1
  Status: Active (tmux session running)
  Location: ~/.tmux-cli/sessions/abc-123-def-456.json
  Windows: 0

  JSON File Preview:
  {
    "sessionId": "abc-123-def-456",
    "projectPath": "/home/user/project-1",
    "windows": []
  }
  ```
- Checks if tmux session exists (calls `SessionExists()`)
- Shows "Active" if running, "Killed" if file exists but tmux session doesn't
- Shows "Ended" if file is in ended/ directory
- Performance: completes in <500ms (NFR4)
- Exit code 0 on success, 1 if session not found

**And** session discovery works (FR23, FR24, FR25, FR26):
- Active sessions: located in `~/.tmux-cli/sessions/`
- Ended sessions: located in `~/.tmux-cli/sessions/ended/`
- JSON structure is clear and human-readable (FR25)
- Developer can distinguish active vs ended by file location (FR24, FR26)
- Developer can `cat` JSON file directly for inspection (FR23)

**And** error handling is comprehensive (FR28):
- List command: directory read errors show clear message
- Status command: missing `--id` exits with code 2
- Status command: invalid UUID format exits with code 2
- Status command: session not found shows helpful message
- All errors wrapped with context

**And** TDD compliance is maintained:
- Tests written first (CR1)
- Table-driven tests for various scenarios (CR4)
- Mock filesystem operations for unit tests (CR5)
- Test fixtures for JSON files in `internal/testutil/fixtures.go` (CR7)
- Tests verify output format and content
- Test coverage >80% (CR2)

## Epic 2: Window Management with Recovery Commands

**Goal:** Developers can create and manage windows within sessions, each with human-readable names and recovery commands that are persisted.

### Story 2.1: Create Window Command

As a developer,
I want to create windows in a session with names and recovery commands,
So that I can organize my work and ensure windows can be recreated after session recovery.

**Acceptance Criteria:**

**Given** Epic 1 is complete and sessions can be created
**When** I implement the create window command
**Then** the following capabilities exist:

**And** TmuxExecutor interface is extended for windows:
```go
type TmuxExecutor interface {
    // ... existing session methods
    CreateWindow(sessionId, name, command string) (windowId string, error)
    ListWindows(sessionId string) ([]WindowInfo, error)
}

type WindowInfo struct {
    TmuxWindowID string // @0, @1, @2...
    Name         string
    Running      bool
}
```

**And** RealTmuxExecutor implements CreateWindow:
- Executes: `tmux new-window -t <sessionId> -n <name> -P -F '#{window_id}' <command>`
- `-P -F '#{window_id}'` returns the tmux window ID (@0, @1, etc.)
- Sets window name using `-n` flag (also sets tmux window_name)
- Starts the specified command in the window
- Returns the tmux-assigned window ID (FR10)
- Unit tests use MockTmuxExecutor (CR5)

**And** `session windows create` command works (FR6, FR7, FR31):
```bash
tmux-cli session --id <uuid> windows create --name <name> --command <command>
```
- `--id` flag specifies the session UUID (required)
- `--name` flag specifies human-readable window name (required)
- `--command` flag specifies recovery command to run (required)
- Command validates all inputs before execution
- Returns exit code 0 on success, 2 on invalid args

**And** window creation workflow executes (FR6, FR7, FR10, FR19, FR20):
1. Validates session ID, window name, and command
2. Loads session from store to verify it exists
3. Creates window in tmux session via TmuxExecutor
4. Receives tmux-assigned window ID (e.g., "@0", "@1")
5. Creates Window struct with tmuxWindowId, name, recoveryCommand
6. Appends window to session.Windows array
7. Saves updated session to JSON using atomic write (FR20)
8. Outputs: "Window created: @0 (name: editor)"

**And** window metadata is persisted correctly (FR19, FR20):
- Window data structure in session JSON:
  ```json
  {
    "sessionId": "uuid",
    "projectPath": "/path",
    "windows": [
      {
        "tmuxWindowId": "@0",
        "name": "editor",
        "recoveryCommand": "vim main.go"
      }
    ]
  }
  ```
- Real-time update: JSON file updated immediately after window creation
- Atomic write ensures no partial updates (NFR17)

**And** error handling is comprehensive (FR28):
- Missing required flags: exits with code 2, shows usage
- Session not found: "Session <uuid> not found", exit code 1
- Session is killed (tmux not running): triggers recovery first, then creates window
- Tmux command failure: error with context, exit code 1
- JSON save failure: error with context, exit code 1

**And** performance meets requirements (NFR3):
- Window creation completes in <1 second

**And** TDD compliance is maintained:
- Tests written first (CR1)
- Table-driven tests for various scenarios (CR4)
- Mock tmux executor in unit tests (CR5)
- Test window creation in active sessions
- Test window creation triggers recovery in killed sessions
- Test coverage >80% (CR2)

### Story 2.2: List Windows Command

As a developer,
I want to list all windows in a session with their IDs and names,
So that I can see what windows exist and reference them by ID.

**Acceptance Criteria:**

**Given** Story 2.1 is complete and windows can be created
**When** I implement the list windows command
**Then** the following capabilities exist:

**And** RealTmuxExecutor implements ListWindows:
- Executes: `tmux list-windows -t <sessionId> -F '#{window_id} #{window_name}'`
- Parses output into WindowInfo structs
- Returns slice of WindowInfo with tmuxWindowID and name
- Returns error if session doesn't exist in tmux
- Unit tests use MockTmuxExecutor (CR5)

**And** `session windows list` command works (FR8):
```bash
tmux-cli session --id <uuid> windows list
```
- Lists all windows in the specified session
- Output format:
  ```
  Windows in session abc-123-def-456:

  ID: @0
  Name: editor
  Command: vim main.go

  ID: @1
  Name: tests
  Command: go test -watch

  Total: 2 windows
  ```
- Shows "No windows in session" if windows array is empty
- Exit code 0 on success, 1 if session not found

**And** list workflow executes correctly (FR8):
1. Validates session ID
2. Loads session from store
3. If session is killed (tmux not running), shows data from JSON file only
4. If session is active, optionally cross-checks with live tmux data
5. Displays all windows from session.Windows array
6. Shows window ID, name, and recovery command for each

**And** session recovery is triggered if needed (FR12):
- If session file exists but tmux session doesn't (killed state)
- List command can trigger recovery before listing
- Or display warning: "Session killed. Run any command to trigger recovery."
- User-friendly approach: show what WILL be recovered

**And** error handling is comprehensive (FR28):
- Missing `--id`: exits with code 2, shows usage
- Session not found: clear error message, exit code 1
- All errors wrapped with context

**And** performance meets requirements (NFR4):
- List operation completes in <500ms

**And** TDD compliance is maintained:
- Tests written first (CR1)
- Table-driven tests for various scenarios (CR4)
- Test listing windows in active sessions
- Test listing windows in killed sessions
- Test listing when no windows exist
- Test coverage >80% (CR2)

### Story 2.3: Get Window Details Command

As a developer,
I want to retrieve detailed information about a specific window,
So that I can inspect a window's configuration and state.

**Acceptance Criteria:**

**Given** Stories 2.1 and 2.2 are complete
**When** I implement the get window command
**Then** the following capabilities exist:

**And** `session windows get` command works (FR9):
```bash
tmux-cli session --id <uuid> windows get --window-id <@N>
```
- Retrieves details for a specific window by its tmux window ID
- Output format:
  ```
  Window Details:

  Session ID: abc-123-def-456
  Window ID: @0
  Name: editor
  Recovery Command: vim main.go
  Status: Running (in active session)
  ```
- Exit code 0 on success, 1 if session or window not found

**And** get window workflow executes (FR9):
1. Validates session ID and window ID format (@N)
2. Loads session from store
3. Searches session.Windows array for matching tmuxWindowId
4. If found, displays window details
5. If session is active, checks if window is actually running in tmux
6. Displays status: "Running" or "Not found in tmux"

**And** window lookup is efficient:
- Iterates through session.Windows array to find matching window ID
- Returns first match (window IDs are unique within session)
- Returns error if window ID not found in session

**And** error handling is comprehensive (FR28):
- Missing `--id` or `--window-id`: exits with code 2, shows usage
- Invalid window ID format: "Window ID must be in format @N", exit code 2
- Session not found: clear error message, exit code 1
- Window not found in session: "Window <@N> not found in session", exit code 1
- All errors wrapped with context

**And** TDD compliance is maintained:
- Tests written first (CR1)
- Table-driven tests for various scenarios (CR4)
- Test getting existing windows
- Test getting non-existent windows
- Test various window ID formats
- Test coverage >80% (CR2)

## Epic 3: Automatic Session Recovery

**Goal:** When a tmux session is killed (crash, reboot, accident), it automatically resurrects itself when you try to access it - completely transparent.

### Story 3.1: Recovery Detection & Manager

As a developer,
I want the system to detect when sessions are killed but have persisted files,
So that recovery can be triggered automatically when I access them.

**Acceptance Criteria:**

**Given** Epics 1 and 2 are complete
**When** I implement the recovery detection system
**Then** the following capabilities exist:

**And** RecoveryManager package is created in `internal/recovery/`:
```go
type RecoveryManager interface {
    IsRecoveryNeeded(sessionId string) (bool, error)
    RecoverSession(session *Session) error
    VerifyRecovery(sessionId string) error
}

type SessionRecoveryManager struct {
    store    store.SessionStore
    executor tmux.TmuxExecutor
}
```

**And** IsRecoveryNeeded() detects killed sessions (FR11):
```go
func (m *SessionRecoveryManager) IsRecoveryNeeded(sessionId string) (bool, error) {
    // 1. Load session from store
    session, err := m.store.Load(sessionId)
    if err != nil {
        return false, err // Session file doesn't exist
    }

    // 2. Check if tmux session exists
    exists, err := m.executor.SessionExists(sessionId)
    if err != nil {
        return false, err
    }

    // 3. Recovery needed if: file exists but tmux session doesn't
    return !exists, nil
}
```
- Returns true if session file exists but tmux session is dead (FR11)
- Returns false if both file and tmux session exist (active session)
- Returns error if session file doesn't exist
- Unit tests use MockTmuxExecutor and mock store (CR5)

**And** recovery detection is efficient:
- Single file read to load session
- Single tmux command to check existence
- No unnecessary operations
- Completes in <100ms

**And** error handling is comprehensive (FR28):
- Session file read errors wrapped with context
- Tmux command errors handled gracefully
- Clear distinction between "no recovery needed" vs "error checking"
- All errors use `fmt.Errorf("...: %w", err)` pattern

**And** TDD compliance is maintained:
- Tests written first (CR1)
- Table-driven tests for scenarios (CR4):
  - Session file exists, tmux session exists → no recovery needed
  - Session file exists, tmux session killed → recovery needed
  - Session file doesn't exist → error
  - Tmux check fails → error
- Mock both store and executor in tests (CR5)
- Test coverage >80% (CR2)

### Story 3.2: Automatic Session & Window Recreation

As a developer,
I want killed sessions to automatically recreate themselves with all windows,
So that I don't lose my workspace setup.

**Acceptance Criteria:**

**Given** Story 3.1 is complete
**When** I implement the session recovery workflow
**Then** the following capabilities exist:

**And** RecoverSession() recreates session and windows (FR12, FR13, FR15):
```go
func (m *SessionRecoveryManager) RecoverSession(session *Session) error {
    // 1. Recreate tmux session with original UUID (FR15)
    err := m.executor.CreateSession(session.SessionID, session.ProjectPath)
    if err != nil {
        return fmt.Errorf("recreate session: %w", err)
    }

    // 2. Recreate all windows using stored recovery commands (FR13)
    for _, window := range session.Windows {
        windowId, err := m.executor.CreateWindow(
            session.SessionID,
            window.Name,
            window.RecoveryCommand,
        )
        if err != nil {
            // Log but don't fail - continue with other windows
            continue
        }

        // Update window ID if tmux assigned different ID
        window.TmuxWindowID = windowId
    }

    // 3. Save updated session to store
    return m.store.Save(session)
}
```
- Recreates tmux session with original UUID (FR12, FR15)
- Uses original projectPath for session working directory
- Recreates all windows from session.Windows array (FR13)
- Executes recovery command for each window (FR13)
- Preserves window names and identifiers (FR15)
- Updates session file if window IDs change

**And** window recreation is robust:
- Windows created in order (first to last)
- Each window executes its stored recoveryCommand
- Window creation failures don't stop recovery of other windows
- All successful windows are tracked
- Session file updated with final state

**And** recovery preserves session identity (FR15):
- Session UUID remains unchanged
- Project path remains unchanged
- Window names remain unchanged
- Window IDs preserved when possible (tmux may reassign @0, @1...)

**And** error handling is comprehensive (FR28):
- Session creation failure: error immediately, recovery fails
- Window creation failures: log, continue with other windows
- Store save failure: error with context
- All errors wrapped with context

**And** performance meets requirements (NFR5):
- Recovery may take time for verification
- Must complete within 30 seconds
- Window creation is sequential (not parallel)

**And** TDD compliance is maintained:
- Tests written first (CR1)
- Table-driven tests for scenarios (CR4):
  - Successful recovery with 0 windows
  - Successful recovery with multiple windows
  - Session creation fails
  - Some windows fail to create
  - All windows fail to create
- Mock store and executor (CR5)
- Test coverage >80% (CR2)

### Story 3.3: Recovery Verification & Integration

As a developer,
I want recovery to verify all windows are running before completing,
So that I can trust the recovery process succeeded.

**Acceptance Criteria:**

**Given** Stories 3.1 and 3.2 are complete
**When** I implement recovery verification and integration
**Then** the following capabilities exist:

**And** VerifyRecovery() confirms windows are running (FR14, NFR10):
```go
func (m *SessionRecoveryManager) VerifyRecovery(sessionId string) error {
    // 1. Load session from store
    session, err := m.store.Load(sessionId)
    if err != nil {
        return err
    }

    // 2. Check if tmux session exists
    exists, err := m.executor.SessionExists(sessionId)
    if err != nil || !exists {
        return fmt.Errorf("session not running after recovery")
    }

    // 3. List windows in tmux
    liveWindows, err := m.executor.ListWindows(sessionId)
    if err != nil {
        return fmt.Errorf("list windows: %w", err)
    }

    // 4. Verify each stored window exists in tmux
    for _, window := range session.Windows {
        found := false
        for _, liveWindow := range liveWindows {
            if liveWindow.TmuxWindowID == window.TmuxWindowID {
                found = true
                break
            }
        }
        if !found {
            return fmt.Errorf("window %s not found after recovery", window.TmuxWindowID)
        }
    }

    return nil // All windows verified
}
```
- Verifies tmux session is running
- Verifies all windows from JSON exist in tmux
- Confirms window IDs match between store and tmux (FR14)
- Returns error if verification fails
- May take time but ensures reliability (NFR5, NFR10)

**And** recovery is integrated into all session access commands (FR12, FR16):
- Every command that accesses a session checks if recovery is needed
- If recovery needed, trigger it transparently before executing command
- User sees brief message: "Recovering session..." followed by command result
- No manual recovery command needed (FR16)

**And** integration points in commands:
```go
// In every session command (list, status, windows list, windows create, etc.)
func executeCommand(sessionId string) error {
    // 1. Check if recovery needed
    recoveryNeeded, err := recoveryManager.IsRecoveryNeeded(sessionId)
    if err != nil {
        return err
    }

    // 2. If needed, recover transparently
    if recoveryNeeded {
        fmt.Fprintln(os.Stderr, "Recovering session...")

        session, _ := store.Load(sessionId)
        err = recoveryManager.RecoverSession(session)
        if err != nil {
            return fmt.Errorf("recovery failed: %w", err)
        }

        err = recoveryManager.VerifyRecovery(sessionId)
        if err != nil {
            return fmt.Errorf("recovery verification failed: %w", err)
        }

        fmt.Fprintln(os.Stderr, "Session recovered successfully")
    }

    // 3. Proceed with original command
    return executeOriginalCommand()
}
```

**And** transparent recovery experience (FR16):
- No manual `tmux-cli session recover` command needed
- Recovery happens automatically on any session access
- User sees: "Recovering session..." → brief delay → "Session recovered successfully"
- Then original command executes normally
- Feels like session never died

**And** recovery reliability (NFR6, NFR7, NFR8):
- 100% success rate for valid session files (NFR6)
- All windows recreate with correct tmux IDs (NFR7)
- JSON state matches tmux reality after recovery (NFR8)
- Verification ensures consistency before reporting success (NFR10)

**And** performance meets requirements (NFR5):
- Recovery + verification completes in <30 seconds
- Acceptable delay for reliability
- Progress indication during recovery

**And** error handling is comprehensive (FR28):
- Recovery failures show clear errors
- Verification failures show which windows failed
- User can see what went wrong
- Errors don't leave partial state

**And** TDD compliance is maintained:
- Tests written first (CR1)
- Integration tests with real tmux (build tag: `integration`) (AR5, CR6)
- Full workflow tests:
  - Create session with windows
  - Kill session
  - Access session (any command) → recovery triggers
  - Verify all windows restored
- Table-driven tests for edge cases (CR4)
- Test coverage >80% (CR2, NFR11)

**And** integration test validates end-to-end recovery:
```go
// +build integration

func TestFullRecoveryWorkflow(t *testing.T) {
    // 1. Create session with 2 windows
    sessionId := uuid.New().String()
    exec("tmux-cli", "session", "start", "--id", sessionId, "--path", "/tmp")
    exec("tmux-cli", "session", "--id", sessionId, "windows", "create", "--name", "editor", "--command", "vim")
    exec("tmux-cli", "session", "--id", sessionId, "windows", "create", "--name", "tests", "--command", "go test")

    // 2. Kill tmux session (simulate crash)
    exec("tmux", "kill-session", "-t", sessionId)

    // 3. List windows (triggers recovery)
    output := exec("tmux-cli", "session", "--id", sessionId, "windows", "list")

    // 4. Verify recovery happened
    assert.Contains(t, output, "Recovering session")
    assert.Contains(t, output, "editor")
    assert.Contains(t, output, "tests")

    // 5. Verify session actually running in tmux
    sessions := exec("tmux", "list-sessions")
    assert.Contains(t, sessions, sessionId)
}
```

### Story 3.4: Inter-Window Communication

As a developer,
I want to send text messages from one window to another within a session,
So that Claude instances can coordinate and communicate status updates between windows.

**Acceptance Criteria:**

**Given** Stories 3.1-3.3 are complete and session recovery works
**When** I implement inter-window communication
**Then** the following capabilities exist:

**And** TmuxExecutor interface is extended for message sending:
```go
type TmuxExecutor interface {
    // ... existing methods
    SendMessage(sessionId, windowId, message string) error
}
```

**And** RealTmuxExecutor implements SendMessage (FR35, FR37):
- Executes: `tmux send-keys -t <sessionId>:<windowId> "<message>" Enter`
- Targets specific window by session ID and window ID
- Delivers message to first pane (`.0`) of the target window
- Automatically appends Enter key to message
- Returns error if tmux command fails
- Unit tests use MockTmuxExecutor (CR5)

**And** `session send` command works (FR32):
```bash
tmux-cli session --id <uuid> send --window-id <@N> --message "text message"
```
- `--id` flag specifies the session UUID (required)
- `--window-id` flag specifies target window ID (required, format: @N)
- `--message` flag specifies text message to send (required)
- Command validates all inputs before execution
- Returns exit code 0 on success, 2 on invalid args, 1 on operational failure

**And** send message workflow executes (FR32-FR38):
1. Validates session ID, window ID format, and message
2. Loads session from store to access session data
3. Checks if recovery is needed and triggers if necessary (FR34)
4. Verifies session is running (not killed) (FR34)
5. Validates target window exists in session.Windows array (FR33)
6. Sends message via TmuxExecutor.SendMessage() (FR35)
7. Outputs success message: "Message sent to window @0" (FR38)
8. Returns appropriate exit code (FR38)

**And** session validation works (FR34):
- Before sending, checks if tmux session exists
- If session file exists but tmux session doesn't (killed state):
  - Automatically triggers recovery
  - Waits for recovery to complete
  - Then sends message to recovered session
- If session doesn't exist at all: returns error
- Clear error message: "Session <uuid> not running. Recovery failed."

**And** window validation works (FR33):
- Searches session.Windows array for matching window ID
- Returns error if window ID not found in session
- Error message: "Window <@N> not found in session <uuid>"
- Validation happens before tmux command execution

**And** message delivery is reliable (FR35, FR37, NFR29):
- Uses tmux send-keys with session:window target format
- Message delivered to first pane of target window (pane .0)
- Automatic Enter key appended for immediate execution
- Atomic operation: either message is delivered or error returned (NFR29)
- No partial message delivery

**And** error handling is comprehensive (FR36, FR28, NFR28):
- Missing required flags: exits with code 2, shows usage
- Invalid window ID format: "Window ID must be in format @N", exit code 2
- Session not found: "Session <uuid> not found", exit code 1
- Session killed and recovery fails: clear error with context, exit code 1
- Window not found: "Window <@N> not found in session", exit code 1
- Tmux command failure: error with context, exit code 1
- All errors wrapped using `fmt.Errorf("...: %w", err)` pattern

**And** success feedback is clear (FR38):
- Success message: "Message sent to window @0 in session <uuid>"
- Exit code 0 on successful delivery
- Optional verbose mode showing exact tmux command executed

**And** performance meets requirements (NFR26):
- Send operation completes in <500ms
- Validation steps are fast (file read + array search)
- Tmux command execution is subsecond

**And** integration with recovery is seamless (FR34, FR12):
- If session is killed when send is attempted:
  - Output: "Session killed. Recovering..."
  - Triggers automatic recovery (calls RecoveryManager)
  - Waits for recovery to complete
  - Then sends message to recovered session
  - Total time may exceed 500ms due to recovery, but acceptable
- Recovery is transparent to the user

**And** use case example works (from PRD user journey):
```bash
# Scenario: Supervisor Claude in window @0, Worker Claude in window @1

# Worker completes task and sends status to supervisor
tmux-cli session --id $SESSION_ID send \
  --window-id @0 \
  --message "Worker: Unit tests passing, feature complete"

# Output:
# Message sent to window @0 in session abc-123-def-456

# In window @0, supervisor Claude's terminal receives:
# Worker: Unit tests passing, feature complete
# [Enter is automatically pressed, so message appears as typed input]
```

**And** error scenarios are handled gracefully:
```bash
# Window doesn't exist
tmux-cli session --id $UUID send --window-id @99 --message "test"
# Error: Window @99 not found in session abc-123-def-456
# Exit code: 1

# Session killed (triggers recovery)
tmux-cli session kill --id $UUID
tmux-cli session --id $UUID send --window-id @0 --message "test"
# Session killed. Recovering...
# Session recovered successfully
# Message sent to window @0 in session abc-123-def-456
# Exit code: 0

# Session doesn't exist
tmux-cli session --id nonexistent send --window-id @0 --message "test"
# Error: Session nonexistent not found
# Exit code: 1
```

**And** TDD compliance is maintained:
- Tests written first (CR1)
- Table-driven tests for scenarios (CR4):
  - Send to existing window in active session
  - Send to existing window in killed session (triggers recovery)
  - Send to non-existent window
  - Send to non-existent session
  - Invalid window ID format
  - Empty message
  - Message with special characters/quotes
- Mock tmux executor and store in unit tests (CR5)
- Integration tests verify actual message delivery (build tag: `integration`)
- Test coverage >80% (CR2, NFR11)

**And** integration test validates end-to-end communication:
```go
// +build integration

func TestInterWindowCommunication(t *testing.T) {
    // 1. Create session with 2 windows
    sessionId := uuid.New().String()
    exec("tmux-cli", "session", "start", "--id", sessionId, "--path", "/tmp")
    exec("tmux-cli", "session", "--id", sessionId, "windows", "create",
         "--name", "supervisor", "--command", "cat")
    exec("tmux-cli", "session", "--id", sessionId, "windows", "create",
         "--name", "worker", "--command", "cat")

    // 2. Send message from worker to supervisor
    output := exec("tmux-cli", "session", "--id", sessionId, "send",
                   "--window-id", "@0", "--message", "Task complete")

    // 3. Verify success message
    assert.Contains(t, output, "Message sent to window @0")

    // 4. Capture pane content in window @0 to verify message received
    paneContent := exec("tmux", "capture-pane", "-t", sessionId+":@0", "-p")
    assert.Contains(t, paneContent, "Task complete")

    // 5. Test recovery scenario: kill session and send message
    exec("tmux", "kill-session", "-t", sessionId)
    output = exec("tmux-cli", "session", "--id", sessionId, "send",
                  "--window-id", "@0", "--message", "After recovery")

    // 6. Verify recovery triggered and message sent
    assert.Contains(t, output, "Recovering")
    assert.Contains(t, output, "Message sent")
}
```

**And** message validation ensures safety:
- Messages are properly escaped for shell execution
- Special characters (quotes, backticks, $) are handled safely
- No command injection vulnerabilities
- Messages are treated as literal text, not shell commands

**And** atomic delivery is guaranteed (NFR29):
- Either complete message is delivered or error is returned
- No partial message delivery
- If tmux command fails mid-send, operation fails entirely
- Transaction-like behavior: all-or-nothing
