# Story 1.2: Session Store with Atomic File Operations

Status: review

## Story

As a developer,
I want a JSON session store with atomic file writes,
So that session state is reliably persisted without risk of data corruption.

## Acceptance Criteria

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

## Tasks / Subtasks

- [x] Update SessionStore interface to match PRD specification (AC: Interface definition)
  - [x] Change `Save(id string, data interface{})` to `Save(session *Session) error`
  - [x] Change `Load(id string) (interface{}, error)` to `Load(id string) (*Session, error)`
  - [x] Add `Move(id string, destination string) error` method
  - [x] Update `List() ([]string, error)` to `List() ([]*Session, error)`

- [x] Define Session and Window data structures (AC: Data structures)
  - [x] Create `types.go` with Session struct matching JSON spec
  - [x] Create Window struct with tmuxWindowId, name, recoveryCommand
  - [x] Add JSON tags matching camelCase from PRD (sessionId, projectPath)
  - [x] Write unit tests verifying JSON marshaling/unmarshaling

- [x] Implement atomic file write pattern (AC: Atomic writes)
  - [x] Create `atomic_write.go` with atomicWrite function
  - [x] Use `os.CreateTemp(dir, "session-*.tmp")` for temp file
  - [x] Write JSON with 2-space indentation for readability
  - [x] Perform atomic rename: `os.Rename(tmpPath, finalPath)`
  - [x] Add cleanup with `defer os.Remove(tmpPath)` on all error paths
  - [x] Write comprehensive tests simulating process crashes

- [x] Implement directory management (AC: Directory management)
  - [x] Create `constants.go` with directory paths and permissions
  - [x] Define `SessionsDir = "~/.tmux-cli/sessions/"`
  - [x] Define `EndedDir = "~/.tmux-cli/sessions/ended/"`
  - [x] Implement `ensureDirectories()` with lazy creation
  - [x] Set directory permissions to 0755, file permissions to 0644
  - [x] Write tests verifying directory creation and permissions

- [x] Implement FileSessionStore (AC: SessionStore implementation)
  - [x] Create `file_store.go` with FileSessionStore struct
  - [x] Implement Save() using atomic write pattern
  - [x] Implement Load() with JSON parsing and error handling
  - [x] Implement Delete() with file removal
  - [x] Implement List() reading all .json files from sessions/
  - [x] Implement Move() for archiving to ended/ directory

- [x] Define sentinel errors (AC: Error handling)
  - [x] Create `errors.go` with ErrSessionNotFound
  - [x] Add ErrInvalidSession for JSON parse failures
  - [x] Add ErrStorageError for filesystem failures
  - [x] Use errors.Is() for error checking in tests

- [x] Write comprehensive unit tests (AC: TDD compliance)
  - [x] Test Save() with valid session
  - [x] Test Save() with atomic write failure scenarios
  - [x] Test Load() with existing and missing sessions
  - [x] Test Load() with corrupted JSON
  - [x] Test Delete() with existing and missing sessions
  - [x] Test List() with multiple sessions and empty directory
  - [x] Test Move() for archiving sessions to ended/
  - [x] Verify test coverage >80% with `make coverage`

- [x] Validate JSON format compliance (AC: JSON validation)
  - [x] Test JSON output matches PRD specification exactly
  - [x] Test JSON is human-readable (indented)
  - [x] Test JSON can be parsed by standard tools (jq, cat)
  - [x] Test round-trip: Save → Load preserves all data

## Dev Notes

### Architecture Integration Points

**SessionStore Interface (Updated from Story 1.1):**

Story 1.1 created a basic interface. This story updates it to match PRD specification:

```go
// OLD (from 1.1):
type SessionStore interface {
    Save(id string, data interface{}) error
    Load(id string) (interface{}, error)
    Delete(id string) error
    List() ([]string, error)
}

// NEW (Story 1.2):
type SessionStore interface {
    Save(session *Session) error
    Load(id string) (*Session, error)
    Delete(id string) error
    List() ([]*Session, error)
    Move(id string, destination string) error
}
```

**Data Structures (PRD FR18):**

These exact structures are specified in the PRD and must be implemented precisely:

```go
type Session struct {
    SessionID   string   `json:"sessionId"`   // camelCase per PRD
    ProjectPath string   `json:"projectPath"` // camelCase per PRD
    Windows     []Window `json:"windows"`     // Initially empty array
}

type Window struct {
    TmuxWindowID    string `json:"tmuxWindowId"`    // @0, @1, @2... from tmux
    Name            string `json:"name"`            // Human-readable name
    RecoveryCommand string `json:"recoveryCommand"` // Command to execute on recovery
}
```

**File Paths:**
- Active sessions: `~/.tmux-cli/sessions/{uuid}.json`
- Ended sessions: `~/.tmux-cli/sessions/ended/{uuid}.json`
- Temp files: `~/.tmux-cli/sessions/session-*.tmp` (during atomic write)

### Technical Requirements

**Atomic File Write Pattern (CRITICAL - NFR17):**

This is the heart of Story 1.2. The atomic write pattern prevents data corruption:

```go
func atomicWrite(path string, session *Session) error {
    dir := filepath.Dir(path)

    // CRITICAL: Temp file MUST be in same directory for atomic rename
    tmpFile, err := os.CreateTemp(dir, "session-*.tmp")
    if err != nil {
        return fmt.Errorf("create temp file: %w", err)
    }
    tmpPath := tmpFile.Name()

    // CRITICAL: Always cleanup temp file on all error paths
    defer func() {
        tmpFile.Close()
        os.Remove(tmpPath)
    }()

    // Write JSON with 2-space indentation for human readability
    encoder := json.NewEncoder(tmpFile)
    encoder.SetIndent("", "  ")
    if err := encoder.Encode(session); err != nil {
        return fmt.Errorf("encode session: %w", err)
    }

    // Close before rename
    if err := tmpFile.Close(); err != nil {
        return fmt.Errorf("close temp file: %w", err)
    }

    // CRITICAL: Atomic rename (POSIX guarantees this is atomic)
    if err := os.Rename(tmpPath, path); err != nil {
        return fmt.Errorf("atomic rename: %w", err)
    }

    return nil
}
```

**Why Atomic Writes Matter:**
- Process can crash AFTER writing temp file but BEFORE rename → No data loss (old file intact)
- Process can crash DURING rename → POSIX guarantees file is either old or new, never partial
- File is NEVER in corrupted state - critical for NFR16, NFR17
- Protects against power loss, OOM kills, SIGKILL, etc.

**Directory Management:**

```go
const (
    SessionsDir = ".tmux-cli/sessions"
    EndedDir    = ".tmux-cli/sessions/ended"
    DirPerms    = 0755 // rwxr-xr-x
    FilePerms   = 0644 // rw-r--r--
)

func ensureDirectories() error {
    homeDir, err := os.UserHomeDir()
    if err != nil {
        return fmt.Errorf("get home dir: %w", err)
    }

    sessionsPath := filepath.Join(homeDir, SessionsDir)
    endedPath := filepath.Join(homeDir, EndedDir)

    // Create sessions directory
    if err := os.MkdirAll(sessionsPath, DirPerms); err != nil {
        return fmt.Errorf("create sessions dir: %w", err)
    }

    // Create ended subdirectory
    if err := os.MkdirAll(endedPath, DirPerms); err != nil {
        return fmt.Errorf("create ended dir: %w", err)
    }

    return nil
}
```

**Error Handling (AR8, FR28):**

Define sentinel errors for type-safe error checking:

```go
// errors.go
package store

import "errors"

var (
    // ErrSessionNotFound is returned when a session file does not exist
    ErrSessionNotFound = errors.New("session not found")

    // ErrInvalidSession is returned when JSON parsing fails
    ErrInvalidSession = errors.New("invalid session data")

    // ErrStorageError is returned for filesystem failures
    ErrStorageError = errors.New("storage operation failed")
)
```

Usage:
```go
session, err := store.Load("uuid")
if errors.Is(err, store.ErrSessionNotFound) {
    // Handle missing session
}
```

### Architecture Compliance

**From Previous Story (1.1):**
- Package `internal/store/` already exists
- Basic SessionStore interface defined but needs updating
- Testing infrastructure in place (testify)
- Makefile targets available (test, coverage, lint)

**Package Organization (AR10):**
```
internal/store/
├── store.go           # SessionStore interface (UPDATE)
├── types.go           # Session, Window structs (NEW)
├── file_store.go      # FileSessionStore implementation (NEW)
├── atomic_write.go    # Atomic file write helper (NEW)
├── constants.go       # Paths, permissions (NEW)
├── errors.go          # Sentinel errors (NEW)
├── store_test.go      # Interface tests (UPDATE)
├── file_store_test.go # Implementation tests (NEW)
└── atomic_write_test.go # Atomic write tests (NEW)
```

**Naming Conventions (AR10, CR13):**
- Files: snake_case (`file_store.go`, `atomic_write_test.go`)
- Types: PascalCase (`FileSessionStore`, `Session`)
- Functions: PascalCase exported, camelCase unexported
- Constants: CamelCase (`SessionsDir`, NOT `SESSIONS_DIR`)
- JSON fields: camelCase (`sessionId`, `projectPath` per PRD)

### Library/Framework Requirements

**Standard Library Usage (AR3):**
This story uses ONLY standard library - no external dependencies needed:

- `encoding/json` - JSON encoding/decoding with indentation
- `os` - File operations, temp files, atomic rename
- `path/filepath` - Path manipulation, directory joining
- `fmt` - Error formatting with %w
- `errors` - Sentinel error definitions and Is() checking

**Why No External Dependencies:**
- Atomic file writes: Standard `os.CreateTemp()` + `os.Rename()`
- JSON operations: Standard `encoding/json` with `SetIndent()`
- File I/O: Standard `os` package
- Path handling: Standard `filepath` package
- This keeps the store package simple and reliable

**Testing Dependencies (AR3):**
- `github.com/stretchr/testify/assert` - Already added in Story 1.1
- `github.com/stretchr/testify/require` - For test assertions
- No mocking needed for this story (testing real filesystem operations)

### File Structure Requirements

**JSON File Format (EXACT specification from PRD FR18):**

```json
{
  "sessionId": "550e8400-e29b-41d4-a716-446655440000",
  "projectPath": "/home/user/my-project",
  "windows": []
}
```

With windows (future stories):
```json
{
  "sessionId": "550e8400-e29b-41d4-a716-446655440000",
  "projectPath": "/home/user/my-project",
  "windows": [
    {
      "tmuxWindowId": "@0",
      "name": "editor",
      "recoveryCommand": "vim main.go"
    },
    {
      "tmuxWindowId": "@1",
      "name": "tests",
      "recoveryCommand": "go test -watch"
    }
  ]
}
```

**File Naming:**
- Active session: `~/.tmux-cli/sessions/{uuid}.json`
- Ended session: `~/.tmux-cli/sessions/ended/{uuid}.json`
- Temp file: `~/.tmux-cli/sessions/session-{random}.tmp`

**Directory Structure:**
```
~/.tmux-cli/
└── sessions/
    ├── 550e8400-e29b-41d4-a716-446655440000.json  # Active session
    ├── 7c9e6679-7425-40de-944b-e07fc1f90ae7.json  # Active session
    └── ended/
        └── old-session-uuid.json                   # Archived session
```

### Testing Requirements

**TDD Workflow (CR1 - MANDATORY):**

This is Story 1.2, so TDD is non-negotiable. The exact workflow:

1. **RED:** Write failing test first
   ```go
   func TestFileSessionStore_Save_ValidSession_CreatesFile(t *testing.T) {
       // Test fails because FileSessionStore doesn't exist yet
   }
   ```

2. **GREEN:** Write minimal code to pass
   ```go
   type FileSessionStore struct{}
   func (s *FileSessionStore) Save(session *Session) error {
       return nil  // Minimal implementation
   }
   ```

3. **REFACTOR:** Improve while keeping tests green
   ```go
   func (s *FileSessionStore) Save(session *Session) error {
       // Full atomic write implementation
   }
   ```

**Unit Test Examples (CR3, CR4):**

```go
// Table-driven test for Save()
func TestFileSessionStore_Save(t *testing.T) {
    tests := []struct {
        name    string
        session *Session
        wantErr bool
    }{
        {
            name: "valid session with no windows",
            session: &Session{
                SessionID:   "test-uuid",
                ProjectPath: "/tmp/test",
                Windows:     []Window{},
            },
            wantErr: false,
        },
        {
            name: "valid session with windows",
            session: &Session{
                SessionID:   "test-uuid",
                ProjectPath: "/tmp/test",
                Windows: []Window{
                    {TmuxWindowID: "@0", Name: "editor", RecoveryCommand: "vim"},
                },
            },
            wantErr: false,
        },
        {
            name:    "nil session",
            session: nil,
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            store := NewFileSessionStore()
            err := store.Save(tt.session)
            if (err != nil) != tt.wantErr {
                t.Errorf("Save() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

**Atomic Write Tests (CRITICAL):**

```go
func TestAtomicWrite_ProcessCrash_NoPartialFile(t *testing.T) {
    // Simulate crash after temp file write but before rename
    // Verify: old file intact, temp file exists, no corruption
}

func TestAtomicWrite_RenameFailure_CleanupTempFile(t *testing.T) {
    // Simulate rename failure (permissions, disk full)
    // Verify: temp file removed, old file intact
}

func TestAtomicWrite_Success_TempFileRemoved(t *testing.T) {
    // Verify successful write removes temp file
    // Verify final file has correct permissions (0644)
}
```

**Test Coverage Requirements (CR2, NFR11):**
- Minimum: >80% coverage for `internal/store` package
- Run: `make coverage` generates HTML report
- All functions must have tests
- All error paths must be tested

**Test Performance (CR14):**
- Unit tests must complete in <1 second
- Use temp directories for test files
- Clean up all test files in teardown

**Test Cleanup (CR16):**
```go
func TestFileSessionStore_Save(t *testing.T) {
    // Setup: Create temp directory for test
    tmpDir := t.TempDir() // Automatically cleaned up

    store := NewFileSessionStore(tmpDir)
    // ... test code ...

    // No manual cleanup needed - t.TempDir() handles it
}
```

### Previous Story Intelligence

**Story 1.1 Learnings:**

From the previous story file at `1-1-project-foundation-cli-framework-setup.md`:

**What Was Created:**
- Basic `internal/store/store.go` with SessionStore interface
- Testing infrastructure with testify
- Makefile targets: test, test-tmux, test-all, coverage, lint
- Go module with dependencies (cobra, uuid, testify)

**Interface That Needs Updating:**

Current (from 1.1):
```go
type SessionStore interface {
    Save(id string, data interface{}) error
    Load(id string) (interface{}, error)
    Delete(id string) error
    List() ([]string, error)
}
```

This story updates it to:
```go
type SessionStore interface {
    Save(session *Session) error
    Load(id string) (*Session, error)
    Delete(id string) error
    List() ([]*Session, error)
    Move(id string, destination string) error
}
```

**Testing Patterns Established:**
- Test naming: `TestFunctionName_Scenario_ExpectedBehavior`
- Table-driven tests for multiple scenarios
- Mock usage for external dependencies
- Test coverage >80%

**Build Infrastructure:**
- `make test` - runs unit tests
- `make coverage` - generates coverage report
- `make lint` - runs gofmt, go vet, golint
- All working and verified in Story 1.1

**What This Story Builds On:**
- Package structure exists: `internal/store/`
- Basic interface defined (needs updating)
- Testing infrastructure ready
- No need to setup tooling - it's all there

**What This Story Adds:**
- Concrete Session and Window types
- Atomic file write implementation
- FileSessionStore implementation
- Directory management
- Comprehensive error handling
- Full test suite for store package

### Critical Implementation Notes

**Atomic Write is Non-Negotiable:**

This is the CORE requirement of Story 1.2. From architecture (AR4, NFR17):
- MUST use temp file + atomic rename pattern
- NO direct file writes allowed
- NO buffered writes without atomicity
- Process crashes must NEVER corrupt data

**JSON Format is Exact:**

The PRD (FR18) specifies exact field names:
- `sessionId` (NOT `session_id` or `SessionID`)
- `projectPath` (NOT `project_path` or `ProjectPath`)
- `tmuxWindowId` (NOT `tmux_window_id` or `TmuxWindowID`)
- `recoveryCommand` (NOT `recovery_command` or `RecoveryCommand`)

Use JSON tags to enforce this:
```go
type Session struct {
    SessionID   string   `json:"sessionId"`
    ProjectPath string   `json:"projectPath"`
    Windows     []Window `json:"windows"`
}
```

**Error Wrapping Pattern (CR12):**

ALWAYS use `fmt.Errorf` with `%w` for error context:
```go
if err != nil {
    return fmt.Errorf("read session file: %w", err)
}
```

This allows error chain inspection with `errors.Is()` and `errors.As()`.

**Directory Creation Must Be Lazy (AR6):**

Don't require manual setup:
```go
func (s *FileSessionStore) Save(session *Session) error {
    // Call ensureDirectories() on every operation
    if err := s.ensureDirectories(); err != nil {
        return err
    }
    // ... continue with save
}
```

This means first Save() creates directories automatically.

**File Permissions Matter:**

Set explicit permissions per architecture:
- Directories: 0755 (rwxr-xr-x) - owner can write, others can read/execute
- Files: 0644 (rw-r--r--) - owner can write, others can read

**Move() for Archival (FR21):**

The Move() method is for archiving sessions to `ended/` directory:
```go
func (s *FileSessionStore) Move(id string, destination string) error {
    // destination is "ended" for archival
    srcPath := filepath.Join(s.sessionsPath, id + ".json")
    dstPath := filepath.Join(s.sessionsPath, destination, id + ".json")

    // Use os.Rename for atomic move
    return os.Rename(srcPath, dstPath)
}
```

### Performance Requirements

From NFR specifications:

**This Story's Performance Targets:**
- File operations are called from session commands
- Session create: <1 second (NFR1) - includes Save()
- Session list: <500ms (NFR4) - includes List()
- Other operations: <1 second

**Store Package Performance:**
- Save(): Should be <50ms for typical session
- Load(): Should be <10ms for typical session
- List(): Should be <100ms for 100 sessions
- Delete(): Should be <10ms
- Move(): Should be <10ms

**Why These Are Fast:**
- Small JSON files (<1KB typically)
- Local filesystem operations
- No network calls
- No complex computations

**Testing Performance:**

Add benchmark tests if needed:
```go
func BenchmarkFileSessionStore_Save(b *testing.B) {
    store := NewFileSessionStore()
    session := &Session{
        SessionID:   "bench-uuid",
        ProjectPath: "/tmp/bench",
        Windows:     []Window{},
    }

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        store.Save(session)
    }
}
```

### Project Context Reference

**Current Project State:**

From Story 1.1 completion:
- Go version: 1.25.5 (meets 1.21+ requirement)
- Cobra CLI framework: v1.10.2 (installed)
- UUID library: Available
- Testify: v1.11.1 (installed)
- Package structure created: `internal/store/`, `internal/tmux/`, `internal/testutil/`

**Existing Files in internal/store/:**
- `store.go` - Basic SessionStore interface (needs update)
- `store_test.go` - Basic tests (needs expansion)

**Files to Create:**
- `types.go` - Session and Window structs
- `file_store.go` - FileSessionStore implementation
- `atomic_write.go` - Atomic write helper
- `constants.go` - Paths and permissions
- `errors.go` - Sentinel errors
- `file_store_test.go` - Implementation tests
- `atomic_write_test.go` - Atomic write tests

**Makefile Targets Available:**
- `make test` - Run unit tests
- `make coverage` - Generate coverage report
- `make lint` - Run code quality checks
- All verified working in Story 1.1

**No Brownfield Issues:**
- Store package is essentially empty (just interface)
- No existing code to refactor
- Clean slate for implementation

### References

- [Source: epics.md#Story 1.2]
- [Source: prd.md#FR17-FR22] - Session file persistence requirements
- [Source: prd.md#FR18] - Exact JSON format specification
- [Source: architecture.md#AR4] - Atomic file write pattern
- [Source: architecture.md#AR6] - Session directory structure
- [Source: architecture.md#AR10] - Package organization
- [Source: coding-rules.md#CR1-CR5] - TDD requirements
- [Source: coding-rules.md#CR12] - Error handling pattern

## Dev Agent Record

### Agent Model Used

claude-sonnet-4-5-20250929

### Debug Log References

None - Implementation completed without issues.

### Completion Notes List

✅ **Story 1.2 Implementation Complete**

**What Was Implemented:**
1. **SessionStore Interface Update** - Updated interface to use typed Session structs instead of generic interface{}, added Move() method for archival
2. **Data Structures** - Created Session and Window types with exact JSON tags matching PRD specification (camelCase: sessionId, projectPath, etc.)
3. **Atomic File Write** - Implemented atomic write pattern using temp files and atomic rename to prevent data corruption
4. **Directory Management** - Lazy directory creation with proper permissions (0755 for dirs, 0644 for files)
5. **FileSessionStore Implementation** - Complete implementation of all SessionStore methods with comprehensive error handling
6. **Sentinel Errors** - Defined ErrSessionNotFound, ErrInvalidSession, ErrStorageError for type-safe error handling
7. **Comprehensive Tests** - 38 unit tests covering all methods, edge cases, error paths, and JSON validation
8. **JSON Validation** - Tests verify exact PRD specification compliance, human readability, and compatibility with standard tools (jq)

**Test Results:**
- All 38 tests passing
- Test coverage: 80.4% (exceeds >80% requirement)
- Test execution time: <1 second (meets CR14 requirement)
- All tests use table-driven approach where appropriate
- TDD red-green-refactor cycle followed for all implementations

**Technical Highlights:**
- Atomic write pattern prevents data corruption on crashes/power loss
- JSON format exactly matches PRD FR18 specification
- Error wrapping with %w enables proper error chain inspection
- Lazy directory creation eliminates manual setup requirements
- File permissions set explicitly per architecture requirements

**Architecture Compliance:**
- ✅ AR4: Atomic file writes implemented with temp file + rename pattern
- ✅ AR6: Directory structure with lazy creation
- ✅ AR8: Comprehensive error handling with sentinel errors
- ✅ AR10: Package organization follows Go conventions
- ✅ CR1-CR5: TDD followed throughout implementation
- ✅ CR12: Error wrapping with fmt.Errorf and %w
- ✅ NFR16: Valid JSON with human-readable formatting
- ✅ NFR17: Atomic operations prevent corruption

### File List

**Created:**
- internal/store/types.go - Session and Window data structures with JSON tags
- internal/store/errors.go - Sentinel error definitions (ErrSessionNotFound, ErrInvalidSession, ErrStorageError)
- internal/store/constants.go - Directory paths, permissions, and ensureDirectories() function
- internal/store/atomic_write.go - Atomic file write implementation with temp file + rename
- internal/store/file_store.go - FileSessionStore implementation (Save, Load, Delete, List, Move)
- internal/store/types_test.go - JSON marshaling/unmarshaling tests (5 tests)
- internal/store/atomic_write_test.go - Atomic write pattern tests (8 tests)
- internal/store/file_store_test.go - FileSessionStore method tests (19 tests)
- internal/store/json_validation_test.go - JSON format compliance tests (6 tests)

**Modified:**
- internal/store/store.go - Updated SessionStore interface to match PRD specification
- internal/store/store_test.go - Updated mock implementation to match new interface

## Change Log

- **2025-12-29**: Story 1.2 completed - Implemented session store with atomic file operations, all tests passing with 80.4% coverage
