# Story 1.4: Kill & End Session Commands

Status: review

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a developer,
I want to kill sessions (preserving files for recovery) or explicitly end sessions (archiving to ended/),
So that I can manage session lifecycle based on whether I need recovery capability.

## Acceptance Criteria

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

## Tasks / Subtasks

- [x] Extend TmuxExecutor interface (AC: #)
  - [x] Add KillSession(id string) error method to interface
  - [x] Implement RealTmuxExecutor.KillSession() using `tmux kill-session -t <id>`
  - [x] Handle "session not found" error (non-fatal for kill operation)
  - [x] Write unit tests using MockTmuxExecutor

- [x] Extend SessionStore interface for file moves (AC: #)
  - [x] Add Move(id string, destination string) error method
  - [x] Implement atomic file move operation
  - [x] Write unit tests verifying atomic move behavior
  - [x] Test data integrity after move

- [x] Implement session kill command (AC: #)
  - [x] Add `kill` subcommand to cmd/tmux-cli/session.go
  - [x] Add --id flag (required)
  - [x] Implement kill workflow in SessionManager
  - [x] Verify session file remains after kill
  - [x] Add appropriate error messages

- [x] Implement session end command (AC: #)
  - [x] Add `end` subcommand to cmd/tmux-cli/session.go
  - [x] Add --id flag (required)
  - [x] Implement end workflow in SessionManager with file move
  - [x] Verify file moved to ended/ directory
  - [x] Add appropriate success/error messages

- [x] Write comprehensive tests (AC: #)
  - [x] Unit tests for KillSession executor method
  - [x] Unit tests for SessionManager kill workflow
  - [x] Unit tests for SessionManager end workflow with file move
  - [x] Table-driven tests for both commands (success, errors, edge cases)
  - [x] Real tmux tests (optional, build tag: tmux)
  - [x] Verify >80% test coverage

- [x] Validate performance and integration (AC: #)
  - [x] Run `make test` - all tests pass
  - [x] Verify kill operation <1 second
  - [x] Verify end operation <1 second
  - [x] Test with real tmux: kill session, verify file remains
  - [x] Test with real tmux: end session, verify file moved to ended/

## Dev Notes

### 🔥 CRITICAL CONTEXT: What This Story Accomplishes

**Story Purpose:**
This story implements the two complementary session termination commands that give developers control over session lifecycle:
- **Kill**: Terminates tmux session but preserves JSON file for automatic recovery (Phase 1 of kill-recover pattern)
- **End**: Terminates tmux session AND archives JSON file to ended/ (permanent termination, no recovery)

**Why Two Commands Matter:**
- **Kill** is for temporary interruptions: system restarts, crashes, accidental kills - session can be recovered
- **End** is for project completion: explicitly signal "I'm done with this project" - clean up active sessions

**Architectural Integration:**
```
Kill Workflow:
User → kill cmd → SessionManager → TmuxExecutor.KillSession()
                              ↓
                        SessionStore (NO CHANGE - file stays in sessions/)

End Workflow:
User → end cmd → SessionManager → TmuxExecutor.KillSession()
                              ↓
                        SessionStore.Move(id, "ended/")
```

### Developer Guardrails: Prevent These Mistakes

**COMMON PITFALLS IN THIS STORY:**

❌ **Mistake 1: Treating "session not found in tmux" as fatal error for kill**
- Kill should be idempotent - if session already dead, that's fine
- Only fail if session file doesn't exist (can't track non-existent session)

❌ **Mistake 2: Not moving file atomically in end command**
- Don't use `os.Rename()` directly across directories (may not be atomic)
- Use copy + delete pattern OR verify same filesystem

❌ **Mistake 3: Forgetting to update SessionStore.Move() method**
- Story 1.2 defined the interface but Move() might not be implemented yet
- Need to add this method to FileSessionStore

❌ **Mistake 4: Not verifying file move succeeded before reporting success**
- Must check file exists in ended/ AND doesn't exist in sessions/
- Partial moves leave system in inconsistent state

❌ **Mistake 5: Not preserving JSON data during move**
- File move must be atomic - no data loss
- Test that JSON content identical before/after move

❌ **Mistake 6: Not cleaning up test files in ended/ directory**
- Tests must clean up both sessions/ and sessions/ended/ directories
- Use t.TempDir() pattern from Story 1.2 for isolation

### Technical Requirements from Previous Stories

**From Story 1.3 (Create Session):**

**TmuxExecutor Interface Pattern:**
```go
// internal/tmux/executor.go
type TmuxExecutor interface {
    CreateSession(id, path string) error
    SessionExists(id string) (bool, error)
    // ADD THESE:
    KillSession(id string) error
}
```

**Existing RealTmuxExecutor Structure:**
```go
type RealTmuxExecutor struct{}

func (e *RealTmuxExecutor) KillSession(id string) error {
    cmd := exec.Command("tmux", "kill-session", "-t", id)
    output, err := cmd.CombinedOutput()
    if err != nil {
        // CRITICAL: Check if session doesn't exist (exit code 1)
        // This is NOT an error for kill - session might already be dead
        if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
            return nil // Session already dead - that's fine
        }
        return fmt.Errorf("tmux kill-session failed: %s: %w", output, err)
    }
    return nil
}
```

**From Story 1.2 (Session Store):**

**SessionStore Interface Extension:**
```go
// internal/store/store.go
type SessionStore interface {
    Save(session *Session) error
    Load(id string) (*Session, error)
    Delete(id string) error
    List() ([]*Session, error)
    // ADD THIS:
    Move(id string, destination string) error
}
```

**File Move Implementation (Atomic):**
```go
func (s *FileSessionStore) Move(id, destination string) error {
    // Source path: ~/.tmux-cli/sessions/<id>.json
    sourcePath := s.sessionPath(id)

    // Destination path: ~/.tmux-cli/sessions/ended/<id>.json
    destDir := filepath.Join(s.baseDir, destination)
    if err := os.MkdirAll(destDir, 0755); err != nil {
        return fmt.Errorf("create destination directory: %w", err)
    }
    destPath := filepath.Join(destDir, id+".json")

    // Read source file
    data, err := os.ReadFile(sourcePath)
    if err != nil {
        if os.IsNotExist(err) {
            return ErrSessionNotFound
        }
        return fmt.Errorf("read source file: %w", err)
    }

    // Write to destination (atomic write pattern from Story 1.2)
    if err := s.atomicWrite(destPath, data); err != nil {
        return fmt.Errorf("write destination file: %w", err)
    }

    // Delete source (only after successful write)
    if err := os.Remove(sourcePath); err != nil {
        return fmt.Errorf("remove source file: %w", err)
    }

    return nil
}

// Helper for atomic write (reuse from Story 1.2)
func (s *FileSessionStore) atomicWrite(path string, data []byte) error {
    dir := filepath.Dir(path)
    tmpFile, err := os.CreateTemp(dir, "session-*.tmp")
    if err != nil {
        return fmt.Errorf("create temp file: %w", err)
    }
    tmpPath := tmpFile.Name()
    defer os.Remove(tmpPath)

    if _, err := tmpFile.Write(data); err != nil {
        tmpFile.Close()
        return fmt.Errorf("write temp file: %w", err)
    }
    tmpFile.Close()

    if err := os.Rename(tmpPath, path); err != nil {
        return fmt.Errorf("atomic rename: %w", err)
    }

    return nil
}
```

### Architecture Compliance

**Session Manager Methods (internal/session/manager.go):**

```go
// KillSession workflow (FR2, FR22)
func (m *SessionManager) KillSession(id string) error {
    // 1. Validate UUID format
    if err := ValidateUUID(id); err != nil {
        return err
    }

    // 2. Load session to verify it exists in store
    session, err := m.store.Load(id)
    if err != nil {
        return fmt.Errorf("load session: %w", err)
    }

    // 3. Kill tmux session (ignore error if already dead)
    // This is idempotent - session might already be killed
    _ = m.executor.KillSession(id)

    // 4. Session file remains in active directory (no store operation)
    // The file stays at ~/.tmux-cli/sessions/<id>.json for recovery

    return nil
}

// EndSession workflow (FR3, FR21)
func (m *SessionManager) EndSession(id string) error {
    // 1. Validate UUID format
    if err := ValidateUUID(id); err != nil {
        return err
    }

    // 2. Load session to verify it exists
    session, err := m.store.Load(id)
    if err != nil {
        return fmt.Errorf("load session: %w", err)
    }

    // 3. Kill tmux session if running (ignore error if already dead)
    _ = m.executor.KillSession(id)

    // 4. Move session file to ended/ directory
    if err := m.store.Move(id, "ended"); err != nil {
        return fmt.Errorf("move session to ended: %w", err)
    }

    return nil
}
```

**Cobra Command Implementation (cmd/tmux-cli/session.go):**

```go
var sessionKillCmd = &cobra.Command{
    Use:   "kill",
    Short: "Kill a tmux session (preserves file for recovery)",
    Long: `Kill a tmux session while preserving its JSON file in the active directory.
The session can be automatically recovered when accessed later.`,
    RunE: runSessionKill,
}

var sessionEndCmd = &cobra.Command{
    Use:   "end",
    Short: "End a session permanently (archives file to ended/)",
    Long: `Kill a tmux session and archive its JSON file to the ended/ directory.
This signals permanent completion - the session will NOT be recovered.`,
    RunE: runSessionEnd,
}

func init() {
    // Kill command flags
    sessionKillCmd.Flags().StringVar(&sessionID, "id", "", "Session UUID (required)")
    sessionKillCmd.MarkFlagRequired("id")

    // End command flags
    sessionEndCmd.Flags().StringVar(&sessionID, "id", "", "Session UUID (required)")
    sessionEndCmd.MarkFlagRequired("id")

    // Add commands to session command
    sessionCmd.AddCommand(sessionKillCmd)
    sessionCmd.AddCommand(sessionEndCmd)
}

func runSessionKill(cmd *cobra.Command, args []string) error {
    // Validate UUID
    if err := session.ValidateUUID(sessionID); err != nil {
        return newUsageError(fmt.Sprintf("invalid UUID format: %s", sessionID))
    }

    // Create manager
    executor := tmux.NewTmuxExecutor()
    fileStore := store.NewFileSessionStore()
    manager := session.NewSessionManager(executor, fileStore)

    // Kill session
    if err := manager.KillSession(sessionID); err != nil {
        return err
    }

    fmt.Printf("Session %s killed (file preserved for recovery)\n", sessionID)
    return nil
}

func runSessionEnd(cmd *cobra.Command, args []string) error {
    // Validate UUID
    if err := session.ValidateUUID(sessionID); err != nil {
        return newUsageError(fmt.Sprintf("invalid UUID format: %s", sessionID))
    }

    // Create manager
    executor := tmux.NewTmuxExecutor()
    fileStore := store.NewFileSessionStore()
    manager := session.NewSessionManager(executor, fileStore)

    // End session
    if err := manager.EndSession(sessionID); err != nil {
        return err
    }

    fmt.Printf("Session %s ended and archived\n", sessionID)
    return nil
}
```

### Library/Framework Requirements

**No New Dependencies!**

**Standard Library Usage:**
- `os/exec` - Execute tmux commands (already used)
- `os` - File operations, Move implementation
- `path/filepath` - Path manipulation for move
- `fmt` - Error formatting
- `errors` - Error checking

**Existing Dependencies (from Story 1.1-1.3):**
- `github.com/spf13/cobra` - CLI framework ✅
- `github.com/google/uuid` - UUID validation ✅
- `github.com/stretchr/testify` - Testing/mocking ✅

### File Structure Requirements

**Files to Modify:**
```
internal/
├── tmux/
│   ├── executor.go           # Add KillSession() to interface
│   ├── real_executor.go      # Implement KillSession()
│   └── real_executor_test.go # Add KillSession tests
├── store/
│   ├── store.go              # Add Move() to interface
│   ├── store_impl.go         # Implement Move() method
│   └── store_test.go         # Add Move() tests
├── session/
│   ├── manager.go            # Add KillSession(), EndSession() methods
│   └── manager_test.go       # Add tests for new methods
cmd/tmux-cli/
├── session.go                # Add kill and end subcommands
└── session_test.go           # Add command tests
```

**No New Files Needed** - extending existing components from Story 1.3

### Testing Requirements

**TDD Workflow (MANDATORY - CR1):**

**Step 1: RED - Write Failing Tests**

```go
// internal/session/manager_test.go
func TestSessionManager_KillSession_SessionExists_KillsSession(t *testing.T) {
    mockExec := new(testutil.MockTmuxExecutor)
    mockStore := new(MockSessionStore)

    // Setup: session exists in store
    session := &store.Session{
        SessionID:   "test-uuid",
        ProjectPath: "/tmp",
        Windows:     []store.Window{},
    }
    mockStore.On("Load", "test-uuid").Return(session, nil)
    mockExec.On("KillSession", "test-uuid").Return(nil)

    manager := NewSessionManager(mockExec, mockStore)
    err := manager.KillSession("test-uuid")

    assert.NoError(t, err)
    mockExec.AssertExpectations(t)
    mockStore.AssertExpectations(t)
}

func TestSessionManager_EndSession_SessionExists_EndsAndArchives(t *testing.T) {
    mockExec := new(testutil.MockTmuxExecutor)
    mockStore := new(MockSessionStore)

    session := &store.Session{
        SessionID:   "test-uuid",
        ProjectPath: "/tmp",
        Windows:     []store.Window{},
    }
    mockStore.On("Load", "test-uuid").Return(session, nil)
    mockExec.On("KillSession", "test-uuid").Return(nil)
    mockStore.On("Move", "test-uuid", "ended").Return(nil)

    manager := NewSessionManager(mockExec, mockStore)
    err := manager.EndSession("test-uuid")

    assert.NoError(t, err)
    mockExec.AssertExpectations(t)
    mockStore.AssertExpectations(t)
}
```

**Step 2: GREEN - Implement Methods**

```go
// internal/session/manager.go
func (m *SessionManager) KillSession(id string) error {
    if err := ValidateUUID(id); err != nil {
        return err
    }

    if _, err := m.store.Load(id); err != nil {
        return fmt.Errorf("load session: %w", err)
    }

    _ = m.executor.KillSession(id) // Idempotent

    return nil
}

func (m *SessionManager) EndSession(id string) error {
    if err := ValidateUUID(id); err != nil {
        return err
    }

    if _, err := m.store.Load(id); err != nil {
        return fmt.Errorf("load session: %w", err)
    }

    _ = m.executor.KillSession(id)

    if err := m.store.Move(id, "ended"); err != nil {
        return fmt.Errorf("move session to ended: %w", err)
    }

    return nil
}
```

**Step 3: REFACTOR - Improve While Keeping Tests Green**

**Table-Driven Tests for Comprehensive Coverage:**

```go
func TestSessionManager_KillSession(t *testing.T) {
    tests := []struct {
        name      string
        sessionID string
        loadErr   error
        killErr   error
        wantErr   bool
    }{
        {
            name:      "successful kill",
            sessionID: "valid-uuid",
            wantErr:   false,
        },
        {
            name:      "session not found in store",
            sessionID: "missing-uuid",
            loadErr:   store.ErrSessionNotFound,
            wantErr:   true,
        },
        {
            name:      "invalid UUID format",
            sessionID: "not-a-uuid",
            wantErr:   true,
        },
        {
            name:      "tmux session already dead (idempotent)",
            sessionID: "dead-uuid",
            killErr:   errors.New("session not found"),
            wantErr:   false, // Kill is idempotent
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            mockExec := new(testutil.MockTmuxExecutor)
            mockStore := new(MockSessionStore)

            if tt.sessionID == "not-a-uuid" {
                // Invalid UUID - no mocks needed
            } else if tt.loadErr != nil {
                mockStore.On("Load", tt.sessionID).Return(nil, tt.loadErr)
            } else {
                session := &store.Session{SessionID: tt.sessionID}
                mockStore.On("Load", tt.sessionID).Return(session, nil)
                mockExec.On("KillSession", tt.sessionID).Return(tt.killErr)
            }

            manager := NewSessionManager(mockExec, mockStore)
            err := manager.KillSession(tt.sessionID)

            if tt.wantErr {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
            }
        })
    }
}
```

**File Move Tests (Critical for Data Integrity):**

```go
// internal/store/store_test.go
func TestFileSessionStore_Move_PreservesData(t *testing.T) {
    // Setup temp directory
    baseDir := t.TempDir()
    store := NewFileSessionStore(baseDir)

    // Create a session
    session := &Session{
        SessionID:   "test-uuid",
        ProjectPath: "/tmp/project",
        Windows: []Window{
            {TmuxWindowID: "@0", Name: "editor", RecoveryCommand: "vim"},
        },
    }

    // Save session
    err := store.Save(session)
    require.NoError(t, err)

    // Verify file exists
    sourcePath := filepath.Join(baseDir, "sessions", "test-uuid.json")
    sourceData, err := os.ReadFile(sourcePath)
    require.NoError(t, err)

    // Move to ended/
    err = store.Move("test-uuid", "ended")
    require.NoError(t, err)

    // Verify file moved
    destPath := filepath.Join(baseDir, "sessions", "ended", "test-uuid.json")
    destData, err := os.ReadFile(destPath)
    require.NoError(t, err)

    // Verify source deleted
    _, err = os.ReadFile(sourcePath)
    assert.True(t, os.IsNotExist(err), "Source file should be deleted")

    // Verify data identical
    assert.Equal(t, sourceData, destData, "Data must be preserved during move")

    // Verify JSON still valid
    var loadedSession Session
    err = json.Unmarshal(destData, &loadedSession)
    require.NoError(t, err)
    assert.Equal(t, session.SessionID, loadedSession.SessionID)
    assert.Equal(t, len(session.Windows), len(loadedSession.Windows))
}
```

### Previous Story Intelligence

**From Story 1.3 (Create Session):**

**What Was Learned:**
- TmuxExecutor pattern works perfectly
- Dependency injection enables easy mocking
- SessionManager orchestrates business logic cleanly
- Error wrapping with %w provides great context
- Cobra command structure is intuitive

**Key Patterns to Reuse:**
```go
// 1. Error wrapping pattern
return fmt.Errorf("context: %w", err)

// 2. Manager constructor pattern
func NewSessionManager(executor TmuxExecutor, store SessionStore) *SessionManager

// 3. Command structure
var sessionKillCmd = &cobra.Command{
    Use: "kill",
    RunE: runSessionKill,
}

// 4. UUID validation
if err := session.ValidateUUID(sessionID); err != nil {
    return newUsageError(...)
}

// 5. Mock setup in tests
mockExec := new(testutil.MockTmuxExecutor)
mockExec.On("KillSession", "uuid").Return(nil)
```

**What NOT to Repeat:**
- Don't forget to handle already-dead sessions (kill should be idempotent)
- Don't make file move operations non-atomic
- Don't skip testing the ended/ directory creation

**From Story 1.2 (Session Store):**

**Atomic Write Pattern to Reuse:**
```go
// This pattern from Story 1.2 is CRITICAL for Move() implementation
func atomicWrite(path string, data []byte) error {
    tmpFile, err := os.CreateTemp(dir, "*.tmp")
    // ... write to temp
    os.Rename(tmpPath, finalPath) // Atomic!
}
```

**Directory Creation Pattern:**
```go
// From Story 1.2 - ensure directories exist
if err := os.MkdirAll(destDir, 0755); err != nil {
    return fmt.Errorf("create directory: %w", err)
}
```

### Integration with Project Context

**From project-context.md - Testing Rules:**

**Rule 1: Full Test Output (STRICT)**
```bash
# Always capture complete output
go test ./internal/session/... -v 2>&1
go test ./internal/store/... -v 2>&1

# NEVER truncate:
# ❌ go test ... | head -50
```

**Rule 2: Exit Code Validation (STRICT)**
```bash
go test ./internal/session/... -v
echo $?  # Must be 0

# If exit code != 0, you have failures!
```

**Rule 5: Working Directory Awareness (STRICT)**
```bash
pwd  # Verify: /home/console/PhpstormProjects/CLI/tmux-cli
go test ./internal/session/... -v
```

**TDD Cycle from Project Context:**
1. **RED**: Write test, verify it fails (exit code = 1)
2. **GREEN**: Write code, verify test passes (exit code = 0)
3. **REFACTOR**: Improve code, exit code stays 0

### Critical Implementation Considerations

**🔥 IDEMPOTENCY IS KEY:**

Both kill and end commands must be idempotent:
- Kill a killed session: No error
- End an ended session: Should fail (file not in sessions/)
- Kill a session that doesn't exist in tmux but has file: OK (kill is about cleanup)

**File Move Atomicity:**

```go
// WRONG - Not atomic, can lose data:
os.Rename(source, dest) // May fail across filesystems

// RIGHT - Atomic write pattern:
data := os.ReadFile(source)
atomicWrite(dest, data)
os.Remove(source) // Only after successful write
```

**Error Handling Philosophy:**

```go
// Kill: Ignore tmux errors (session might be dead)
_ = m.executor.KillSession(id) // Best effort

// End: Fail on store errors (file move is critical)
if err := m.store.Move(id, "ended"); err != nil {
    return fmt.Errorf("move failed: %w", err)
}
```

**Test Cleanup:**

```go
func TestEndSession(t *testing.T) {
    baseDir := t.TempDir() // Automatically cleaned up
    store := NewFileSessionStore(baseDir)

    // Test will clean up both sessions/ and sessions/ended/
    // when t.TempDir() is removed
}
```

### Performance Requirements

**From NFR2:**
- Kill operation: <1 second
- End operation: <1 second

**Expected Timings:**
- UUID validation: <1ms
- Store.Load(): ~10ms (from Story 1.2)
- tmux kill-session: ~50-100ms
- Store.Move(): ~20-50ms (read + write + delete)
- **Total: ~80-160ms** (well within 1 second)

**Performance Test Pattern:**

```go
func TestSessionManager_KillSession_Performance(t *testing.T) {
    // Only test with real operations, not mocks
    executor := tmux.NewTmuxExecutor()
    store := store.NewFileSessionStore()
    manager := NewSessionManager(executor, store)

    start := time.Now()
    err := manager.KillSession("test-uuid")
    duration := time.Since(start)

    assert.NoError(t, err)
    assert.Less(t, duration, 1*time.Second)
}
```

### Data Integrity Validation

**File Move Must Preserve:**
1. Session ID
2. Project Path
3. All Windows (array)
4. All Window properties (tmuxWindowId, name, recoveryCommand)
5. JSON formatting (human-readable)

**Test Pattern:**

```go
func TestMove_PreservesAllData(t *testing.T) {
    // Create session with complex data
    original := &Session{
        SessionID:   "uuid",
        ProjectPath: "/complex/path",
        Windows: []Window{
            {TmuxWindowID: "@0", Name: "editor", RecoveryCommand: "vim main.go"},
            {TmuxWindowID: "@1", Name: "tests", RecoveryCommand: "go test -watch"},
        },
    }

    // Save, move, load
    store.Save(original)
    store.Move("uuid", "ended")

    // Load from ended/
    moved := loadFromEnded("uuid") // Helper function

    // Deep equality check
    assert.Equal(t, original, moved)
}
```

### References

- [Source: epics.md#Story 1.4 Lines 420-501] - Complete story requirements
- [Source: architecture.md#FR2] - Kill session preserves file
- [Source: architecture.md#FR3] - End session archives to ended/
- [Source: architecture.md#FR21] - File move to ended/ requirement
- [Source: architecture.md#FR22] - Session file preservation
- [Source: architecture.md#NFR2] - Performance requirement <1s
- [Source: architecture.md#NFR20] - Data integrity during archival
- [Source: architecture.md#AR8] - POSIX exit codes
- [Source: coding-rules.md#CR1] - TDD mandatory
- [Source: coding-rules.md#CR5] - Mock external dependencies
- [Source: coding-rules.md#CR12] - Return errors explicitly
- [Source: project-context.md] - Testing rules (full output, exit codes)
- [Source: 1-3-create-session-command.md] - TmuxExecutor pattern, SessionManager pattern

### Project Structure Notes

**Alignment with Unified Project Structure:**

This story extends the existing internal package structure established in Stories 1.1-1.3:

```
internal/
├── tmux/         # Extended with KillSession()
├── store/        # Extended with Move()
└── session/      # Extended with KillSession(), EndSession()
```

No new packages needed - clean extension of existing architecture.

**No Conflicts Detected:**
- Follows established patterns
- Extends interfaces cleanly
- Maintains dependency injection
- Respects package boundaries
- Follows naming conventions

## Dev Agent Record

### Agent Model Used

Claude Sonnet 4.5 (claude-sonnet-4-5-20250929)

### Debug Log References

### Completion Notes List

- ✅ Extended TmuxExecutor interface with KillSession method and implemented idempotent behavior
- ✅ Extended SessionStore interface with Move method using atomic file operations
- ✅ Implemented SessionManager.KillSession workflow - kills tmux session while preserving file
- ✅ Implemented SessionManager.EndSession workflow - kills tmux session and archives file to ended/
- ✅ Added Cobra commands: `session kill` and `session end` with --id flags
- ✅ All tests pass including comprehensive unit tests and real environment verification
- ✅ Performance requirements met: both operations complete in <1 second
- ✅ Data integrity verified: file move preserves all JSON data
- ✅ Real environment testing confirmed: kill preserves file, end archives to ended/

### File List

- internal/tmux/executor.go - Extended interface with KillSession
- internal/tmux/real_executor.go - Implemented idempotent KillSession
- internal/tmux/real_executor_test.go - Added KillSession idempotency test
- internal/store/store.go - Extended interface with Move
- internal/store/file_store.go - Implemented atomic Move operation
- internal/store/file_store_test.go - Added Move data integrity tests
- internal/session/manager.go - Added KillSession and EndSession methods
- internal/session/manager_test.go - Added comprehensive tests for kill and end workflows
- cmd/tmux-cli/session.go - Added kill and end Cobra commands
