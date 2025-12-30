# Story 1.3: Create Session Command

Status: review

## Story

As a developer,
I want to create a tmux session with UUID and project path,
So that I can start a persistent session tied to a specific project.

## Acceptance Criteria

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

## Tasks / Subtasks

- [x] Implement TmuxExecutor interface and RealTmuxExecutor (AC: TmuxExecutor interface)
  - [x] Define TmuxExecutor interface in `internal/tmux/executor.go`
  - [x] Implement RealTmuxExecutor.CreateSession() using `tmux new-session -d -s <id> -c <path>`
  - [x] Implement RealTmuxExecutor.SessionExists() using `tmux has-session -t <id>`
  - [x] Add tmux not found detection (check PATH, return exit code 126)
  - [x] Write unit tests using MockTmuxExecutor from testutil

- [x] Create session start command (AC: session start command)
  - [x] Create `cmd/tmux-cli/session.go` with session subcommand
  - [x] Add `start` subcommand with --id and --path flags
  - [x] Implement flag validation (required flags, UUID format, path exists)
  - [x] Wire up exit code handling per AR8 (0, 1, 2, 126)

- [x] Implement session creation workflow (AC: workflow)
  - [x] Create session manager in `internal/session/manager.go`
  - [x] Implement CreateSession(id, path) business logic
  - [x] Integrate TmuxExecutor for tmux operations
  - [x] Integrate SessionStore for persistence
  - [x] Add comprehensive error handling with context

- [x] Add UUID validation and generation (AC: UUID integration)
  - [x] Verify `github.com/google/uuid` dependency (added in Story 1.1)
  - [x] Add UUID format validation helper
  - [x] Add UUID generation helper: `uuid.New().String()`
  - [x] Write tests for UUID validation

- [x] Write comprehensive tests (AC: TDD compliance)
  - [x] Unit tests for TmuxExecutor (mock-based)
  - [x] Unit tests for session manager (mock tmux + store)
  - [x] Unit tests for session command (flag validation, error paths)
  - [x] Real tmux tests with build tag `// +build tmux`
  - [x] Table-driven tests for various scenarios
  - [x] Verify >80% test coverage

- [x] Validate performance and integration (AC: performance, integration)
  - [x] Run `make test` - all tests pass
  - [x] Run `make test-tmux` - real tmux integration works
  - [x] Verify session creation <1 second
  - [x] Test with real tmux: create session, verify in tmux list-sessions
  - [x] Test JSON file created correctly in ~/.tmux-cli/sessions/

## Dev Notes

### Developer Context: Critical Integration Points

**🔥 THIS STORY BRINGS EVERYTHING TOGETHER:**

This is the first story where we actually CREATE something in tmux and persist it. You're integrating:
1. **Cobra CLI** from Story 1.1 - the command layer
2. **SessionStore** from Story 1.2 - the persistence layer
3. **TmuxExecutor** (NEW) - the tmux integration layer
4. **UUID generation** - for session identification

**COMMON DEVELOPER MISTAKES TO AVOID:**
- ❌ Forgetting to validate UUID format before passing to tmux
- ❌ Not checking if path exists before creating session
- ❌ Not detecting if tmux is installed (should return exit code 126)
- ❌ Creating session in tmux but forgetting to save to store (or vice versa)
- ❌ Not handling partial failures (tmux succeeds but store fails)
- ❌ Hardcoding paths instead of using filepath.Join
- ❌ Not following TDD - write tests FIRST
- ❌ Not wrapping errors with context (use fmt.Errorf with %w)

### Architecture Compliance

**Three-Tier Architecture Pattern:**

```
┌─────────────────────────────────────┐
│   CLI Layer (cmd/tmux-cli/)         │  ← Cobra commands, flag parsing
│   - session.go: start command       │  ← Exit code handling
└────────────┬────────────────────────┘
             │
             ▼
┌─────────────────────────────────────┐
│   Business Logic (internal/session/)│  ← Session manager
│   - manager.go: CreateSession()     │  ← Orchestrates operations
└────────┬─────────────┬──────────────┘
         │             │
         ▼             ▼
┌─────────────┐  ┌──────────────────┐
│ TmuxExecutor│  │  SessionStore    │
│ (tmux ops)  │  │  (persistence)   │
└─────────────┘  └──────────────────┘
```

**Dependency Injection Pattern:**

The session manager should take dependencies via constructor:
```go
type SessionManager struct {
    executor TmuxExecutor
    store    store.SessionStore
}

func NewSessionManager(executor TmuxExecutor, store store.SessionStore) *SessionManager {
    return &SessionManager{
        executor: executor,
        store:    store,
    }
}
```

This enables easy testing with mocks!

### Technical Requirements

**TmuxExecutor Implementation (CRITICAL):**

```go
// internal/tmux/executor.go
package tmux

import (
    "fmt"
    "os/exec"
)

type TmuxExecutor interface {
    CreateSession(id, path string) error
    SessionExists(id string) (bool, error)
}

type RealTmuxExecutor struct{}

func NewTmuxExecutor() *RealTmuxExecutor {
    return &RealTmuxExecutor{}
}

func (e *RealTmuxExecutor) CreateSession(id, path string) error {
    // Command: tmux new-session -d -s <id> -c <path>
    // -d: detached mode (don't attach immediately)
    // -s: session name (our UUID)
    // -c: working directory (project path)

    cmd := exec.Command("tmux", "new-session", "-d", "-s", id, "-c", path)
    output, err := cmd.CombinedOutput()
    if err != nil {
        // Check if tmux not found
        if cmd.Err == exec.ErrNotFound {
            return ErrTmuxNotFound
        }
        return fmt.Errorf("tmux new-session failed: %s: %w", output, err)
    }
    return nil
}

func (e *RealTmuxExecutor) SessionExists(id string) (bool, error) {
    // Command: tmux has-session -t <id>
    // Exit code 0: session exists
    // Exit code 1: session doesn't exist

    cmd := exec.Command("tmux", "has-session", "-t", id)
    err := cmd.Run()
    if err != nil {
        // Check if tmux not found
        if cmd.Err == exec.ErrNotFound {
            return false, ErrTmuxNotFound
        }
        // Exit code 1 means session doesn't exist (not an error)
        if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
            return false, nil
        }
        return false, fmt.Errorf("tmux has-session failed: %w", err)
    }
    return true, nil
}
```

**Error Definitions:**

```go
// internal/tmux/errors.go
package tmux

import "errors"

var (
    // ErrTmuxNotFound is returned when tmux is not installed or not in PATH
    ErrTmuxNotFound = errors.New("tmux not found")

    // ErrSessionAlreadyExists is returned when trying to create existing session
    ErrSessionAlreadyExists = errors.New("session already exists")
)
```

**Session Manager Implementation:**

```go
// internal/session/manager.go
package session

import (
    "fmt"
    "os"

    "github.com/console/tmux-cli/internal/store"
    "github.com/console/tmux-cli/internal/tmux"
)

type SessionManager struct {
    executor tmux.TmuxExecutor
    store    store.SessionStore
}

func NewSessionManager(executor tmux.TmuxExecutor, store store.SessionStore) *SessionManager {
    return &SessionManager{
        executor: executor,
        store:    store,
    }
}

func (m *SessionManager) CreateSession(id, path string) error {
    // 1. Validate path exists
    if _, err := os.Stat(path); os.IsNotExist(err) {
        return fmt.Errorf("path does not exist: %s", path)
    }

    // 2. Check if session already exists in tmux
    exists, err := m.executor.SessionExists(id)
    if err != nil {
        return fmt.Errorf("check session exists: %w", err)
    }
    if exists {
        return tmux.ErrSessionAlreadyExists
    }

    // 3. Create tmux session
    if err := m.executor.CreateSession(id, path); err != nil {
        return fmt.Errorf("create tmux session: %w", err)
    }

    // 4. Create session object
    session := &store.Session{
        SessionID:   id,
        ProjectPath: path,
        Windows:     []store.Window{}, // Empty initially
    }

    // 5. Save to store
    if err := m.store.Save(session); err != nil {
        // Cleanup: kill the tmux session if store fails
        // This prevents orphaned tmux sessions
        _ = m.executor.KillSession(id) // Best effort cleanup
        return fmt.Errorf("save session to store: %w", err)
    }

    return nil
}
```

**UUID Validation:**

```go
// internal/session/validation.go
package session

import (
    "errors"
    "github.com/google/uuid"
)

var ErrInvalidUUID = errors.New("invalid UUID format")

func ValidateUUID(id string) error {
    if _, err := uuid.Parse(id); err != nil {
        return ErrInvalidUUID
    }
    return nil
}

func GenerateUUID() string {
    return uuid.New().String()
}
```

**Cobra Command Implementation:**

```go
// cmd/tmux-cli/session.go
package main

import (
    "fmt"
    "os"

    "github.com/console/tmux-cli/internal/session"
    "github.com/console/tmux-cli/internal/store"
    "github.com/console/tmux-cli/internal/tmux"
    "github.com/spf13/cobra"
)

var sessionCmd = &cobra.Command{
    Use:   "session",
    Short: "Manage tmux sessions",
    Long:  "Create, manage, and inspect persistent tmux sessions",
}

var sessionStartCmd = &cobra.Command{
    Use:   "start",
    Short: "Create a new tmux session",
    Long:  "Create a new detached tmux session with UUID and project path",
    RunE:  runSessionStart,
}

var (
    sessionID   string
    projectPath string
)

func init() {
    // Add flags
    sessionStartCmd.Flags().StringVar(&sessionID, "id", "", "Session UUID (required)")
    sessionStartCmd.Flags().StringVar(&projectPath, "path", "", "Project path (required)")

    // Mark flags as required
    sessionStartCmd.MarkFlagRequired("id")
    sessionStartCmd.MarkFlagRequired("path")

    // Add start command to session command
    sessionCmd.AddCommand(sessionStartCmd)

    // Add session command to root
    rootCmd.AddCommand(sessionCmd)
}

func runSessionStart(cmd *cobra.Command, args []string) error {
    // Validate UUID format
    if err := session.ValidateUUID(sessionID); err != nil {
        return newUsageError(fmt.Sprintf("invalid UUID format: %s", sessionID))
    }

    // Create dependencies
    executor := tmux.NewTmuxExecutor()
    fileStore := store.NewFileSessionStore()
    manager := session.NewSessionManager(executor, fileStore)

    // Create session
    if err := manager.CreateSession(sessionID, projectPath); err != nil {
        return err
    }

    fmt.Printf("Session %s created at %s\n", sessionID, projectPath)
    return nil
}
```

**Exit Code Handling (from root.go - Story 1.1):**

```go
// cmd/tmux-cli/root.go (add to existing)
func determineExitCode(err error) int {
    switch {
    case errors.Is(err, tmux.ErrTmuxNotFound):
        return ExitCommandNotFound // 126
    case errors.Is(err, usageError{}):
        return ExitUsageError // 2
    case errors.Is(err, session.ErrInvalidUUID):
        return ExitUsageError // 2
    default:
        return ExitGeneralError // 1
    }
}

type usageError struct {
    msg string
}

func (e usageError) Error() string {
    return e.msg
}

func newUsageError(msg string) error {
    return usageError{msg: msg}
}
```

### Library/Framework Requirements

**Dependencies (Already Added in Story 1.1):**
- `github.com/spf13/cobra` - CLI framework ✅
- `github.com/google/uuid` - UUID v4 generation ✅
- `github.com/stretchr/testify` - Testing/mocking ✅

**Standard Library Usage:**
- `os/exec` - Execute tmux commands
- `os` - File path validation, directory checks
- `fmt` - Error formatting
- `errors` - Error checking with Is()
- `path/filepath` - Path manipulation

**No New Dependencies Needed!**

### File Structure Requirements

**New Files to Create:**

```
internal/
├── tmux/
│   ├── executor.go           # TmuxExecutor interface + RealTmuxExecutor
│   ├── errors.go             # ErrTmuxNotFound, ErrSessionAlreadyExists
│   ├── executor_test.go      # Unit tests with mocks
│   └── executor_tmux_test.go # Real tmux tests (build tag: tmux)
├── session/
│   ├── manager.go            # SessionManager with CreateSession()
│   ├── validation.go         # UUID validation helpers
│   ├── errors.go             # Session-specific errors
│   ├── manager_test.go       # Unit tests with mocks
│   └── validation_test.go    # UUID validation tests
cmd/tmux-cli/
├── session.go                # session command + start subcommand
└── session_test.go           # Command tests
```

**Files to Modify:**
- `cmd/tmux-cli/root.go` - Add determineExitCode() cases for new errors

### Testing Requirements

**TDD Workflow (MANDATORY - CR1):**

1. **RED:** Write failing test FIRST
2. **GREEN:** Write minimal code to pass
3. **REFACTOR:** Improve while keeping tests green

**Example Test Progression:**

```go
// Step 1: RED - Write failing test
func TestSessionManager_CreateSession_ValidInput_CreatesSession(t *testing.T) {
    mockExec := new(testutil.MockTmuxExecutor)
    mockStore := new(MockSessionStore)

    mockExec.On("SessionExists", "test-uuid").Return(false, nil)
    mockExec.On("CreateSession", "test-uuid", "/tmp").Return(nil)
    mockStore.On("Save", mock.Anything).Return(nil)

    manager := NewSessionManager(mockExec, mockStore)
    err := manager.CreateSession("test-uuid", "/tmp")

    assert.NoError(t, err)
    mockExec.AssertExpectations(t)
    mockStore.AssertExpectations(t)
}

// Step 2: GREEN - Write minimal implementation
func (m *SessionManager) CreateSession(id, path string) error {
    return nil  // Minimal to make test pass
}

// Step 3: REFACTOR - Add full implementation while keeping tests green
func (m *SessionManager) CreateSession(id, path string) error {
    // Full implementation with error handling
}
```

**Table-Driven Tests (CR4):**

```go
func TestSessionManager_CreateSession(t *testing.T) {
    tests := []struct {
        name          string
        sessionID     string
        path          string
        existsResult  bool
        existsErr     error
        createErr     error
        saveErr       error
        wantErr       error
    }{
        {
            name:      "valid session creation",
            sessionID: "valid-uuid",
            path:      "/tmp",
            wantErr:   nil,
        },
        {
            name:         "session already exists",
            sessionID:    "existing-uuid",
            path:         "/tmp",
            existsResult: true,
            wantErr:      tmux.ErrSessionAlreadyExists,
        },
        {
            name:      "tmux not found",
            sessionID: "test-uuid",
            path:      "/tmp",
            createErr: tmux.ErrTmuxNotFound,
            wantErr:   tmux.ErrTmuxNotFound,
        },
        {
            name:      "store save fails",
            sessionID: "test-uuid",
            path:      "/tmp",
            saveErr:   errors.New("disk full"),
            wantErr:   errors.New("save session to store"),
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test implementation
        })
    }
}
```

**Real Tmux Integration Tests (AR5, CR6):**

```go
// +build tmux

package tmux

func TestRealTmuxExecutor_CreateSession_Integration(t *testing.T) {
    executor := NewTmuxExecutor()
    testID := "test-" + uuid.New().String()

    // Create session
    err := executor.CreateSession(testID, "/tmp")
    require.NoError(t, err)

    // Cleanup
    defer func() {
        exec.Command("tmux", "kill-session", "-t", testID).Run()
    }()

    // Verify session exists in tmux
    output, err := exec.Command("tmux", "list-sessions").CombinedOutput()
    require.NoError(t, err)
    assert.Contains(t, string(output), testID)
}
```

**Test Coverage Requirements:**
- Target: >80% for all new packages (CR2, NFR11)
- Run `make coverage` to verify
- All error paths must be tested
- All success paths must be tested

### Previous Story Intelligence

**From Story 1.1 (CLI Framework):**

**What Was Learned:**
- Cobra command structure is set up and working
- Exit code handling pattern established (0, 1, 2, 126)
- `determineExitCode()` function exists in root.go
- MockTmuxExecutor interface exists in testutil
- Test infrastructure working (make test, make coverage)

**What to Reuse:**
- Exit code constants (ExitSuccess, ExitGeneralError, etc.)
- Error handling pattern with determineExitCode()
- Test naming convention: TestFunctionName_Scenario_ExpectedBehavior
- Makefile targets (test, test-tmux, test-all)

**From Story 1.2 (Session Store):**

**What Was Learned:**
- SessionStore interface is fully implemented
- Atomic file write pattern works perfectly
- Session and Window types defined with correct JSON tags
- Directory management with lazy creation works
- Test coverage achieved: 80.4% (excellent!)

**What to Reuse:**
- SessionStore.Save() for persistence
- Session struct for data model
- Error wrapping pattern: `fmt.Errorf("context: %w", err)`
- JSON validation tests approach
- t.TempDir() for test cleanup

**Integration Points:**

```go
// This story connects the two previous stories:

// From Story 1.1: Cobra CLI
sessionStartCmd := &cobra.Command{
    Use: "start",
    RunE: runSessionStart,
}

// From Story 1.2: SessionStore
fileStore := store.NewFileSessionStore()
session := &store.Session{
    SessionID:   id,
    ProjectPath: path,
    Windows:     []store.Window{},
}
fileStore.Save(session)
```

### Critical Implementation Notes

**🔥 FAILURE MODES YOU MUST HANDLE:**

1. **Tmux not installed:**
   ```go
   if cmd.Err == exec.ErrNotFound {
       return ErrTmuxNotFound // Maps to exit code 126
   }
   ```

2. **Session exists in tmux already:**
   ```go
   exists, _ := executor.SessionExists(id)
   if exists {
       return ErrSessionAlreadyExists
   }
   ```

3. **Path doesn't exist:**
   ```go
   if _, err := os.Stat(path); os.IsNotExist(err) {
       return fmt.Errorf("path does not exist: %s", path)
   }
   ```

4. **Tmux succeeds but store fails (CRITICAL!):**
   ```go
   if err := m.executor.CreateSession(id, path); err != nil {
       return err
   }

   if err := m.store.Save(session); err != nil {
       // CLEANUP: Kill the tmux session!
       _ = m.executor.KillSession(id)
       return fmt.Errorf("save session: %w", err)
   }
   ```
   This prevents orphaned tmux sessions when persistence fails.

5. **Invalid UUID format:**
   ```go
   if _, err := uuid.Parse(id); err != nil {
       return ErrInvalidUUID // Maps to exit code 2
   }
   ```

**TDD Discipline (CR1 - NON-NEGOTIABLE):**
- ✅ Write test FIRST
- ✅ Watch it FAIL (red)
- ✅ Write minimal code to pass (green)
- ✅ Refactor while keeping tests green
- ❌ NEVER write implementation without test

**Error Wrapping Pattern (CR12):**
```go
// BAD:
return err

// GOOD:
return fmt.Errorf("create tmux session: %w", err)
```

**Dependency Injection for Testing:**
```go
// Constructor injection enables mocking
func NewSessionManager(executor TmuxExecutor, store SessionStore) *SessionManager
```

**Cleanup on Partial Failures:**
```go
// If store fails after tmux succeeds, cleanup tmux session
if err := m.store.Save(session); err != nil {
    _ = m.executor.KillSession(id) // Best effort cleanup
    return fmt.Errorf("save session: %w", err)
}
```

### Performance Requirements

**From NFR1:**
- Session creation must complete in <1 second
- Includes: tmux command + JSON save

**Expected Timings:**
- UUID validation: <1ms
- Path check: <1ms
- tmux new-session: ~50-200ms
- SessionStore.Save(): ~10-50ms (from Story 1.2)
- **Total: ~60-250ms** (well within 1 second limit)

**Performance Testing:**
```go
func TestSessionManager_CreateSession_Performance(t *testing.T) {
    start := time.Now()

    // ... create session ...

    duration := time.Since(start)
    assert.Less(t, duration, 1*time.Second, "Should complete in <1s")
}
```

### Integration with Project Context

**From project-context.md - STRICT Testing Rules:**

**Rule 1: Full Test Output Required (STRICT)**
```bash
# Always use this pattern:
go test ./internal/session/... -v 2>&1

# NEVER truncate output:
# ❌ go test ./internal/session/... -v 2>&1 | head -50
```

**Rule 2: Exit Code Validation (STRICT)**
```bash
# After every test run:
go test ./internal/session/... -v
echo $?  # MUST be 0 for success
```

**Rule 3: No Blind Retries (STRICT)**
- If tests fail, analyze WHY
- Maximum 1 retry with additional diagnostic flags
- Don't loop on same failing command

**Rule 5: Working Directory Awareness (STRICT)**
```bash
# Always verify location first:
pwd  # Should show: /home/console/PhpstormProjects/CLI/tmux-cli

# Then run tests:
go test ./internal/session/... -v
```

**TDD Red-Green-Refactor (from project-context.md):**
1. **RED:** Write failing test, verify exit code = 1
2. **GREEN:** Write code, verify exit code = 0
3. **REFACTOR:** Improve code, exit code stays 0

### References

- [Source: epics.md#Story 1.3 Lines 352-420]
- [Source: prd.md#FR1] - Session creation with UUID + path
- [Source: prd.md#FR17] - JSON storage requirement
- [Source: prd.md#FR27-FR30] - CLI interface requirements
- [Source: architecture.md#AR3] - UUID library justification
- [Source: architecture.md#AR7] - TmuxExecutor interface pattern
- [Source: architecture.md#AR8] - POSIX exit code conventions
- [Source: coding-rules.md#CR1] - TDD mandatory
- [Source: coding-rules.md#CR5] - Mock external dependencies
- [Source: 1-1-project-foundation-cli-framework-setup.md] - Cobra setup, exit codes
- [Source: 1-2-session-store-with-atomic-file-operations.md] - SessionStore usage

## Dev Agent Record

### Agent Model Used

Claude Sonnet 4.5 (claude-sonnet-4-5-20250929)

### Debug Log References

- Test logs: /tmp/tmux-test-output.log, /tmp/session-validation-test.log, /tmp/session-manager-red.log, /tmp/all-tests.log
- Coverage report: /tmp/coverage.log
- Make test output: /tmp/make-test.log

### Completion Notes List

✅ **Story 1.3 Implementation Complete**

**What Was Implemented:**
1. **TmuxExecutor Interface & RealTmuxExecutor** (internal/tmux/)
   - Defined TmuxExecutor interface with CreateSession, HasSession, KillSession, ListSessions, CreateWindow, ListWindows methods
   - Implemented RealTmuxExecutor with full tmux command execution
   - Added tmux not found detection (exit code 126)
   - Comprehensive error handling with context wrapping

2. **UUID Validation & Generation** (internal/session/)
   - ValidateUUID() function validates UUID v4 format
   - GenerateUUID() function generates collision-free UUIDs
   - Full test coverage including edge cases

3. **Session Manager** (internal/session/manager.go)
   - CreateSession() orchestrates entire workflow
   - Validates path exists before creating session
   - Checks for existing sessions to prevent duplicates
   - Creates tmux session via executor
   - Persists to JSON store via SessionStore
   - **Critical**: Cleanup logic - kills tmux session if store fails (prevents orphaned sessions)

4. **Session Start Command** (cmd/tmux-cli/session.go)
   - Cobra command structure: `tmux-cli session start --id <uuid> --path <path>`
   - Required flags: --id and --path
   - UUID format validation before execution
   - Enhanced exit code mapping (0, 1, 2, 126 per AR8)
   - UsageError type for proper exit code 2 handling

5. **Comprehensive Testing**
   - Unit tests for all components (mock-based)
   - Table-driven tests for SessionManager (5 scenarios)
   - UUID validation tests (valid & invalid cases)
   - Command tests (flag validation, existence)
   - Test coverage: session package 100%, store 80.4%

6. **Integration & Performance Validation**
   - make test: ✅ All tests pass
   - Real tmux integration: ✅ Session created and verified
   - JSON persistence: ✅ File created in ~/.tmux-cli/sessions/
   - Performance: Session creation <<1 second (meets NFR1)

**Technical Decisions:**
- Dependency injection pattern for SessionManager enables easy mocking
- Error wrapping with %w for error chain inspection
- Atomic file operations inherited from Story 1.2
- TDD red-green-refactor cycle followed strictly

**Key Files Modified/Created:**
- internal/tmux/real_executor.go (new)
- internal/tmux/errors.go (new)
- internal/session/manager.go (new)
- internal/session/validation.go (new)
- cmd/tmux-cli/session.go (new)
- go.mod (added github.com/google/uuid dependency)

**All Acceptance Criteria Satisfied:**
✅ TmuxExecutor interface defined and implemented
✅ UUID integration working
✅ Session start command functional
✅ Workflow executes: validate → create tmux → persist → success
✅ Error handling comprehensive (all failure modes covered)
✅ Performance <1 second
✅ TDD compliance maintained (tests written first)
✅ Test coverage >80%

### File List

**New Files:**
- internal/tmux/real_executor.go
- internal/tmux/real_executor_test.go
- internal/session/manager.go
- internal/session/manager_test.go
- internal/session/validation.go
- internal/session/validation_test.go
- cmd/tmux-cli/session.go
- cmd/tmux-cli/session_test.go

**Modified Files:**
- cmd/tmux-cli/root.go (enhanced determineExitCode)
- go.mod (added UUID dependency)
- go.sum (UUID dependency checksums)
