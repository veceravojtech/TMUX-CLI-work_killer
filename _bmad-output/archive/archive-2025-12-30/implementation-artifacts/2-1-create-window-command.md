# Story 2.1: Create Window Command

Status: review

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a developer,
I want to create windows in a session with names and recovery commands,
So that I can organize my work and ensure windows can be recreated after session recovery.

## Acceptance Criteria

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

## Tasks / Subtasks

- [x] Extend TmuxExecutor interface (AC: #1)
  - [x] Add CreateWindow(sessionId, name, command string) (windowId string, error) to interface
  - [x] Add ListWindows(sessionId string) ([]WindowInfo, error) to interface (for Story 2.2)
  - [x] Define WindowInfo struct with TmuxWindowID, Name, Running fields
  - [x] Update internal/tmux/executor.go with new methods

- [x] Implement RealTmuxExecutor.CreateWindow() (AC: #2)
  - [x] Write failing test: TestRealTmuxExecutor_CreateWindow_Success
  - [x] Write failing test: TestRealTmuxExecutor_CreateWindow_SessionNotFound
  - [x] Implement CreateWindow() executing: `tmux new-window -t <sessionId> -n <name> -P -F '#{window_id}' <command>`
  - [x] Parse output to extract window ID (format: @0, @1, @2...)
  - [x] Handle errors: session not found, invalid command, tmux errors
  - [x] Verify tests pass (exit code 0)

- [x] Implement windows create command (AC: #3)
  - [x] Add windows subcommand structure to cmd/tmux-cli/session.go
  - [x] Add create subcommand: `session --id <uuid> windows create --name <name> --command <command>`
  - [x] Add --name flag (required, string)
  - [x] Add --command flag (required, string)
  - [x] Implement workflow: validate inputs → load session → create window → update session → save
  - [x] Format output: "Window created: @0 (name: editor)"
  - [x] Handle all error cases with appropriate exit codes

- [x] Implement window persistence (AC: #4)
  - [x] Create Window struct if not exists (already in Session from Story 1.2)
  - [x] After CreateWindow(), append new Window to session.Windows array
  - [x] Call store.Save(session) to persist updated session with atomic write
  - [x] Verify JSON file contains new window entry
  - [x] Test: create window, kill CLI, load session, verify window persisted

- [x] Write comprehensive tests (AC: #5)
  - [x] Unit tests for TmuxExecutor.CreateWindow() method
  - [x] Unit tests for create command workflow (mocked executor and store)
  - [x] Table-driven tests for: success, session not found, invalid inputs, tmux failures
  - [x] Test window ID parsing from tmux output
  - [x] Test session persistence after window creation
  - [x] Test error handling for all failure scenarios
  - [x] Verify >80% test coverage for new code

- [x] Validate performance and integration (AC: #6)
  - [x] Run `make test` - all tests pass (exit code 0)
  - [x] Build binary: `go build -o tmux-cli ./cmd/tmux-cli`
  - [x] Create session: `./tmux-cli session start --id <uuid> --path /tmp/test`
  - [x] Create window: `./tmux-cli session --id <uuid> windows create --name editor --command vim`
  - [x] Verify window created in tmux: `tmux list-windows -t <uuid>`
  - [x] Verify window persisted: `cat ~/.tmux-cli/sessions/<uuid>.json`
  - [x] Verify performance: window creation <1 second
  - [x] Test error cases: session not found, invalid UUID, etc.

## Dev Notes

### 🔥 CRITICAL CONTEXT: What This Story Accomplishes

**Story Purpose:**
This story implements window creation within sessions, enabling developers to:
- **Create windows** with human-readable names in existing sessions
- **Specify recovery commands** that will be used to recreate windows after session crashes
- **Persist window metadata** to JSON for Epic 3's automatic recovery feature

**Why This Matters:**
- Windows are the organizational units within tmux sessions
- Recovery commands enable Epic 3's "session that wouldn't die" feature
- Real-time persistence ensures no window metadata is lost

**Architectural Integration:**
```
Window Creation Workflow:
User → windows create cmd → SessionStore.Load() → Verify session exists
                                     ↓
                    TmuxExecutor.CreateWindow() → Execute tmux new-window command
                                     ↓
                    Receive @0, @1, @2... window ID from tmux
                                     ↓
                    Append Window{@N, name, command} to session.Windows
                                     ↓
                    SessionStore.Save() → Atomic write updated JSON
                                     ↓
                    Display success: "Window created: @0 (name: editor)"
```

**Connection to Epic 1:**
- Builds on Session persistence (Story 1.2)
- Uses SessionStore.Load() and Save() (Story 1.2)
- Extends TmuxExecutor pattern (Story 1.3)
- Session.Windows array already defined in Story 1.2

**Foundation for Epic 3:**
- Recovery commands stored here will be used in Story 3.2
- Window IDs (@0, @1...) must be preserved for recovery verification in Story 3.3

### Developer Guardrails: Prevent These Mistakes

**COMMON PITFALLS IN THIS STORY:**

❌ **Mistake 1: Not parsing tmux window ID correctly**
- `tmux new-window -P -F '#{window_id}'` returns format like "@0\n"
- Must parse string, trim newline, validate format matches @\d+
- Don't hardcode window IDs or use sequential counters
- Tmux assigns IDs - we must respect what tmux tells us

❌ **Mistake 2: Forgetting to save session after window creation**
- Creating window in tmux is NOT enough
- Must append Window to session.Windows array
- Must call store.Save(session) with atomic write
- If you skip this, window won't survive recovery

❌ **Mistake 3: Not handling killed sessions gracefully**
- If session file exists but tmux session is dead, window creation should trigger recovery first
- This story's scope: detect and fail with clear error OR trigger recovery
- Epic 3 implements full recovery, but we can prepare for it
- Error message: "Session killed. Run any command to trigger recovery" is acceptable

❌ **Mistake 4: Incorrect command line structure**
- NOT: `tmux-cli windows create --session-id <uuid> ...`
- CORRECT: `tmux-cli session --id <uuid> windows create ...`
- The `--id` flag is on the parent `session` command
- Windows is a sub-resource of session

❌ **Mistake 5: Not validating recovery command**
- Recovery command can be any shell command
- Don't try to validate it's a "valid" command
- Just store it exactly as provided
- Tmux will execute it when window is created/recovered

❌ **Mistake 6: Breaking atomic writes from Story 1.2**
- Session persistence must use atomic write pattern from Story 1.2
- Create temp file → write JSON → rename (not direct write)
- This prevents corruption if process crashes mid-save

❌ **Mistake 7: Not testing with real tmux**
- Unit tests with mocks are necessary
- Integration tests with real tmux are MANDATORY
- Window IDs are tmux-assigned - must verify real behavior
- Use build tag `// +build tmux` for real tmux tests

### Technical Requirements from Previous Stories

**From Story 1.2 (Session Store):**

**Session and Window Data Structures (Already Exist):**
```go
// internal/store/session.go
type Session struct {
    SessionID   string   `json:"sessionId"`
    ProjectPath string   `json:"projectPath"`
    Windows     []Window `json:"windows"`  // Already defined!
}

type Window struct {
    TmuxWindowID    string `json:"tmuxWindowId"`    // @0, @1, @2...
    Name            string `json:"name"`            // Human-readable
    RecoveryCommand string `json:"recoveryCommand"` // Shell command
}
```

**SessionStore Interface (Already Exists):**
```go
// internal/store/store.go
type SessionStore interface {
    Save(session *Session) error  // ← Use this for persistence
    Load(id string) (*Session, error)
    Delete(id string) error
    Move(id string, destination string) error
    List() ([]*Session, error)
}
```

**Atomic Write Pattern (Already Implemented):**
```go
// internal/store/file_store.go
func (s *FileSessionStore) Save(session *Session) error {
    // 1. Marshal to JSON
    data, err := json.MarshalIndent(session, "", "  ")

    // 2. Create temp file in same directory
    tmpFile, err := os.CreateTemp(s.getSessionsDir(), "session-*.tmp")

    // 3. Write to temp
    _, err = tmpFile.Write(data)
    tmpFile.Close()

    // 4. Atomic rename
    finalPath := filepath.Join(s.getSessionsDir(), session.SessionID+".json")
    err = os.Rename(tmpFile.Name(), finalPath)

    return err
}
```

**From Story 1.3 (Create Session):**

**TmuxExecutor Interface (Extend This):**
```go
// internal/tmux/executor.go
type TmuxExecutor interface {
    CreateSession(id, path string) error
    SessionExists(id string) (bool, error)
    KillSession(id string) error
    // ADD FOR THIS STORY:
    CreateWindow(sessionId, name, command string) (windowId string, error)
    ListWindows(sessionId string) ([]WindowInfo, error)
}

// WindowInfo struct for listing windows (Story 2.2 will use this)
type WindowInfo struct {
    TmuxWindowID string // @0, @1, @2...
    Name         string
    Running      bool
}
```

**RealTmuxExecutor Pattern:**
```go
// internal/tmux/real_executor.go
type RealTmuxExecutor struct{}

func NewTmuxExecutor() TmuxExecutor {
    return &RealTmuxExecutor{}
}

// Existing methods: CreateSession, SessionExists, KillSession

// ADD THIS METHOD:
func (e *RealTmuxExecutor) CreateWindow(sessionId, name, command string) (string, error) {
    // Build command: tmux new-window -t <sessionId> -n <name> -P -F '#{window_id}' <command>
    args := []string{
        "new-window",
        "-t", sessionId,  // Target session
        "-n", name,       // Window name
        "-P",             // Print information
        "-F", "#{window_id}", // Format: only window ID
    }

    // Append command as separate argument
    if command != "" {
        args = append(args, command)
    }

    cmd := exec.Command("tmux", args...)
    output, err := cmd.Output()
    if err != nil {
        if exitErr, ok := err.(*exec.ExitError); ok {
            // Session doesn't exist or other tmux error
            return "", fmt.Errorf("tmux new-window failed: %w (stderr: %s)", err, exitErr.Stderr)
        }
        return "", fmt.Errorf("tmux new-window failed: %w", err)
    }

    // Parse window ID (format: @0\n or @1\n)
    windowID := strings.TrimSpace(string(output))

    // Validate format: must start with @ followed by digit(s)
    if !strings.HasPrefix(windowID, "@") {
        return "", fmt.Errorf("invalid window ID format: %s", windowID)
    }

    return windowID, nil
}

// Story 2.2 will implement ListWindows - stub it for now
func (e *RealTmuxExecutor) ListWindows(sessionId string) ([]WindowInfo, error) {
    // TODO: Implement in Story 2.2
    return nil, fmt.Errorf("not implemented yet")
}
```

**MockTmuxExecutor Pattern (for tests):**
```go
// internal/testutil/mock_tmux.go
type MockTmuxExecutor struct {
    mock.Mock
}

func (m *MockTmuxExecutor) CreateWindow(sessionId, name, command string) (string, error) {
    args := m.Called(sessionId, name, command)
    return args.String(0), args.Error(1)
}

func (m *MockTmuxExecutor) ListWindows(sessionId string) ([]WindowInfo, error) {
    args := m.Called(sessionId)
    if args.Get(0) == nil {
        return nil, args.Error(1)
    }
    return args.Get(0).([]WindowInfo), args.Error(1)
}
```

### Architecture Compliance

**Command Structure (cmd/tmux-cli/session.go):**

```go
// Parent session command already exists from Story 1.3
var sessionCmd = &cobra.Command{
    Use:   "session",
    Short: "Manage tmux sessions",
}

var sessionID string  // Global flag for session ID

func init() {
    // --id flag on parent session command
    sessionCmd.PersistentFlags().StringVar(&sessionID, "id", "", "Session UUID")

    // Add subcommands
    sessionCmd.AddCommand(sessionStartCmd)  // From Story 1.3
    sessionCmd.AddCommand(sessionKillCmd)   // From Story 1.4
    sessionCmd.AddCommand(sessionEndCmd)    // From Story 1.4
    sessionCmd.AddCommand(sessionListCmd)   // From Story 1.5
    sessionCmd.AddCommand(sessionStatusCmd) // From Story 1.5
    sessionCmd.AddCommand(windowsCmd)       // NEW: Add windows subcommand

    rootCmd.AddCommand(sessionCmd)
}

// NEW: Windows subcommand (parent for create, list, get)
var windowsCmd = &cobra.Command{
    Use:   "windows",
    Short: "Manage windows in a session",
    Long:  `Create and manage windows within a tmux session. Requires --id flag on parent session command.`,
}

func init() {
    // Add window subcommands
    windowsCmd.AddCommand(windowsCreateCmd)  // This story
    // windowsCmd.AddCommand(windowsListCmd)    // Story 2.2
    // windowsCmd.AddCommand(windowsGetCmd)     // Story 2.3
}

// NEW: Windows create subcommand
var windowsCreateCmd = &cobra.Command{
    Use:   "create",
    Short: "Create a new window in the session",
    Long: `Create a new window in the session with a name and recovery command.

Example:
  tmux-cli session --id abc-123 windows create --name editor --command "vim main.go"`,
    RunE: runWindowsCreate,
}

var (
    windowName    string
    windowCommand string
)

func init() {
    windowsCreateCmd.Flags().StringVar(&windowName, "name", "", "Window name (required)")
    windowsCreateCmd.Flags().StringVar(&windowCommand, "command", "", "Recovery command to run in window (required)")

    windowsCreateCmd.MarkFlagRequired("name")
    windowsCreateCmd.MarkFlagRequired("command")
}

func runWindowsCreate(cmd *cobra.Command, args []string) error {
    // 1. Validate session ID (from parent flag)
    if sessionID == "" {
        return newUsageError("--id flag is required on session command")
    }

    if err := validateUUID(sessionID); err != nil {
        return newUsageError(fmt.Sprintf("invalid UUID format: %s", sessionID))
    }

    // 2. Validate window name and command
    if windowName == "" {
        return newUsageError("--name flag is required")
    }
    if windowCommand == "" {
        return newUsageError("--command flag is required")
    }

    // 3. Create dependencies
    executor := tmux.NewTmuxExecutor()
    fileStore := store.NewFileSessionStore()

    // 4. Load session to verify it exists
    session, err := fileStore.Load(sessionID)
    if err != nil {
        if errors.Is(err, store.ErrSessionNotFound) {
            return fmt.Errorf("session %s not found", sessionID)
        }
        return fmt.Errorf("load session: %w", err)
    }

    // 5. Check if session is running in tmux
    running, err := executor.SessionExists(sessionID)
    if err != nil {
        return fmt.Errorf("check session exists: %w", err)
    }

    if !running {
        // Session is killed - for now, return error
        // Epic 3 will implement automatic recovery here
        return fmt.Errorf("session %s is not running (tmux session killed). Recovery not yet implemented.", sessionID)
    }

    // 6. Create window in tmux
    windowID, err := executor.CreateWindow(sessionID, windowName, windowCommand)
    if err != nil {
        return fmt.Errorf("create window: %w", err)
    }

    // 7. Create Window struct and append to session
    newWindow := store.Window{
        TmuxWindowID:    windowID,
        Name:            windowName,
        RecoveryCommand: windowCommand,
    }
    session.Windows = append(session.Windows, newWindow)

    // 8. Save updated session with atomic write
    err = fileStore.Save(session)
    if err != nil {
        // Window was created in tmux but persistence failed
        // This is a problem - window won't survive recovery
        return fmt.Errorf("save session: %w (window created in tmux but not persisted!)", err)
    }

    // 9. Success message
    fmt.Printf("Window created: %s (name: %s)\n", windowID, windowName)
    return nil
}

// Helper for usage errors (exit code 2)
func newUsageError(msg string) error {
    // Cobra automatically handles exit code 2 for usage errors
    return fmt.Errorf(msg)
}

// UUID validation helper (already exists from Story 1.3)
func validateUUID(id string) error {
    _, err := uuid.Parse(id)
    if err != nil {
        return fmt.Errorf("invalid UUID: %w", err)
    }
    return nil
}
```

### Library/Framework Requirements

**No New Dependencies!**

**Standard Library Usage:**
- `os/exec` - Execute tmux commands
- `strings` - Parse tmux output
- `fmt` - Error formatting and output
- `encoding/json` - Already used in Story 1.2
- `errors` - Error checking

**Existing Dependencies (from Epic 1):**
- `github.com/spf13/cobra` - CLI framework ✅
- `github.com/google/uuid` - UUID validation ✅
- `github.com/stretchr/testify` - Testing/mocking ✅

### File Structure Requirements

**Files to Modify:**
```
internal/
├── tmux/
│   ├── executor.go           # Extend interface with CreateWindow, ListWindows
│   ├── real_executor.go      # Implement CreateWindow and ListWindows methods
│   └── real_executor_test.go # Add CreateWindow tests
├── testutil/
│   └── mock_tmux.go          # Add CreateWindow and ListWindows mock methods
cmd/tmux-cli/
├── session.go                # Add windows subcommand structure
└── session_test.go           # Add windows create command tests (optional)
```

**Files Already Exist (No Changes Needed):**
- `internal/store/session.go` - Window struct already defined
- `internal/store/store.go` - SessionStore interface complete
- `internal/store/file_store.go` - Save/Load methods ready

**No New Files Needed** - extending existing components from Epic 1

### Testing Requirements

**TDD Workflow (MANDATORY - CR1):**

**Step 1: RED - Write Failing Tests**

```go
// internal/tmux/real_executor_test.go
func TestRealTmuxExecutor_CreateWindow_Success(t *testing.T) {
    // This test requires real tmux - use build tag
    // +build tmux

    executor := NewTmuxExecutor()

    // Create a test session first
    sessionID := uuid.New().String()
    err := executor.CreateSession(sessionID, "/tmp")
    require.NoError(t, err)
    defer executor.KillSession(sessionID)

    // Create window
    windowID, err := executor.CreateWindow(sessionID, "test-window", "vim")

    assert.NoError(t, err)
    assert.NotEmpty(t, windowID)
    assert.True(t, strings.HasPrefix(windowID, "@"), "Window ID should start with @")

    // Verify window exists in tmux
    cmd := exec.Command("tmux", "list-windows", "-t", sessionID, "-F", "#{window_id}")
    output, err := cmd.Output()
    require.NoError(t, err)
    assert.Contains(t, string(output), windowID)
}

func TestRealTmuxExecutor_CreateWindow_SessionNotFound(t *testing.T) {
    executor := NewTmuxExecutor()

    // Try to create window in non-existent session
    windowID, err := executor.CreateWindow("non-existent-session", "test", "vim")

    assert.Error(t, err)
    assert.Empty(t, windowID)
    assert.Contains(t, err.Error(), "tmux new-window failed")
}

func TestRealTmuxExecutor_CreateWindow_WindowIDParsing(t *testing.T) {
    // +build tmux

    executor := NewTmuxExecutor()

    sessionID := uuid.New().String()
    err := executor.CreateSession(sessionID, "/tmp")
    require.NoError(t, err)
    defer executor.KillSession(sessionID)

    // Create multiple windows, verify sequential IDs
    window1, err := executor.CreateWindow(sessionID, "window-1", "vim")
    assert.NoError(t, err)
    assert.Equal(t, "@0", window1, "First window should be @0")

    window2, err := executor.CreateWindow(sessionID, "window-2", "vim")
    assert.NoError(t, err)
    assert.Equal(t, "@1", window2, "Second window should be @1")
}
```

**Unit Tests with Mocks:**

```go
// cmd/tmux-cli/session_test.go (or create windows_test.go)
func TestWindowsCreate_Success(t *testing.T) {
    // Setup mock executor and store
    mockExecutor := new(testutil.MockTmuxExecutor)
    mockStore := new(testutil.MockSessionStore)

    sessionID := "test-uuid"
    session := &store.Session{
        SessionID:   sessionID,
        ProjectPath: "/tmp/test",
        Windows:     []store.Window{},
    }

    // Mock expectations
    mockStore.On("Load", sessionID).Return(session, nil)
    mockExecutor.On("SessionExists", sessionID).Return(true, nil)
    mockExecutor.On("CreateWindow", sessionID, "editor", "vim").Return("@0", nil)
    mockStore.On("Save", mock.AnythingOfType("*store.Session")).Return(nil)

    // Execute command (will need dependency injection or test helper)
    // This is simplified - actual implementation depends on how commands are structured

    // Verify Save was called with updated session
    mockStore.AssertCalled(t, "Save", mock.MatchedBy(func(s *store.Session) bool {
        return len(s.Windows) == 1 &&
               s.Windows[0].TmuxWindowID == "@0" &&
               s.Windows[0].Name == "editor" &&
               s.Windows[0].RecoveryCommand == "vim"
    }))
}

func TestWindowsCreate_SessionNotFound(t *testing.T) {
    mockStore := new(testutil.MockSessionStore)

    mockStore.On("Load", "nonexistent").Return(nil, store.ErrSessionNotFound)

    // Execute command
    // Assert error contains "session nonexistent not found"
    // Assert exit code 1
}

func TestWindowsCreate_SessionKilled(t *testing.T) {
    mockExecutor := new(testutil.MockTmuxExecutor)
    mockStore := new(testutil.MockSessionStore)

    sessionID := "test-uuid"
    session := &store.Session{
        SessionID:   sessionID,
        ProjectPath: "/tmp/test",
        Windows:     []store.Window{},
    }

    mockStore.On("Load", sessionID).Return(session, nil)
    mockExecutor.On("SessionExists", sessionID).Return(false, nil)  // Session killed

    // Execute command
    // Assert error contains "not running" or "killed"
    // Assert exit code 1
}

func TestWindowsCreate_MissingRequiredFlags(t *testing.T) {
    tests := []struct {
        name      string
        sessionID string
        winName   string
        winCmd    string
        wantErr   string
        wantCode  int
    }{
        {
            name:     "missing session ID",
            winName:  "editor",
            winCmd:   "vim",
            wantErr:  "--id flag is required",
            wantCode: 2,
        },
        {
            name:      "missing window name",
            sessionID: "test-uuid",
            winCmd:    "vim",
            wantErr:   "--name flag is required",
            wantCode:  2,
        },
        {
            name:      "missing window command",
            sessionID: "test-uuid",
            winName:   "editor",
            wantErr:   "--command flag is required",
            wantCode:  2,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Execute command with missing flags
            // Assert error message matches
            // Assert exit code matches
        })
    }
}
```

**Step 2: GREEN - Implement Methods**

(Implementation shown in "Architecture Compliance" section above)

**Step 3: REFACTOR - Improve While Keeping Tests Green**

### Performance Requirements

**From NFR3:**
- Window creation: <1 second

**Expected Timings:**
- UUID validation: <1ms
- SessionStore.Load(): ~50ms (from Story 1.5)
- SessionExists(): ~50ms (from Story 1.3)
- tmux new-window: ~100-200ms (spawns new window process)
- SessionStore.Save(): ~50ms (atomic write)
- **Total: ~250-350ms** (well within 1 second)

**Performance Considerations:**
- Most time is tmux command execution and process spawning
- Atomic write adds minimal overhead
- JSON serialization is fast for session metadata
- No optimization needed at this stage

### Integration with Project Context

**From project-context.md - Testing Rules:**

**Rule 1: Full Test Output (STRICT)**
```bash
# Always capture complete output
go test ./internal/tmux/... -v 2>&1
go test ./cmd/tmux-cli/... -v 2>&1

# NEVER truncate:
# ❌ go test ... | head -50
```

**Rule 1.A: Always use LSP (STRICT)**
```bash
# Use gopls for Go language server
# Enables intelligent code navigation, completion, refactoring
```

**Rule 2: Exit Code Validation (STRICT)**
```bash
go test ./internal/tmux/... -v
echo $?  # Must be 0

# If exit code != 0, you have failures!
```

**Rule 6: Real Command Execution Verification (STRICT)**
```bash
# 1. Build the binary
go build -o tmux-cli ./cmd/tmux-cli

# 2. Create a session
SESSION_ID=$(uuidgen)
./tmux-cli session start --id $SESSION_ID --path /tmp/test-project

# 3. Create a window
./tmux-cli session --id $SESSION_ID windows create --name editor --command "vim main.go"

# 4. Verify window exists in tmux
tmux list-windows -t $SESSION_ID

# 5. Verify window persisted to JSON
cat ~/.tmux-cli/sessions/$SESSION_ID.json

# 6. Verify window shows correct ID, name, and command in JSON
```

### Critical Implementation Considerations

**🔥 TMUX WINDOW ID PARSING:**

```go
// CORRECT - Parse window ID from tmux output
output, err := cmd.Output()  // Returns: "@0\n" or "@1\n"
windowID := strings.TrimSpace(string(output))  // Remove \n

// Validate format
if !strings.HasPrefix(windowID, "@") {
    return "", fmt.Errorf("invalid window ID format: %s", windowID)
}

// WRONG - Hardcoding or generating IDs
// ❌ windowID := fmt.Sprintf("@%d", len(session.Windows))
// ❌ windowID := "@0"  // Never hardcode!
```

**Command Argument Handling:**

```go
// CORRECT - Append command as final argument
args := []string{
    "new-window",
    "-t", sessionId,
    "-n", name,
    "-P",
    "-F", "#{window_id}",
    command,  // Command is last, tmux handles it specially
}

// WRONG - Quoting or shell escaping
// ❌ Don't do: args = append(args, fmt.Sprintf("'%s'", command))
// Tmux handles command execution, we just pass the string
```

**Session Persistence After Window Creation:**

```go
// CORRECT - Update session in memory, then save
newWindow := store.Window{
    TmuxWindowID:    windowID,  // From tmux
    Name:            windowName, // From flag
    RecoveryCommand: windowCommand, // From flag
}
session.Windows = append(session.Windows, newWindow)

err = fileStore.Save(session)  // Uses atomic write from Story 1.2
if err != nil {
    // This is serious - window exists in tmux but not in JSON
    return fmt.Errorf("save session: %w (window created but not persisted!)", err)
}

// WRONG - Not saving or saving before creating window
// ❌ Return after creating window without saving
// ❌ Save before calling CreateWindow() (window won't be in JSON)
```

**Error Context Wrapping:**

```go
// CORRECT - Wrap errors with context
if err != nil {
    return fmt.Errorf("create window: %w", err)
}

// WRONG - Losing context
// ❌ return err
// ❌ return errors.New("error creating window")
```

### Connection to Future Stories

**Story 2.2 (List Windows) Dependencies:**
- Uses ListWindows() method we stub in this story
- Will read session.Windows array we populate here
- Will verify window IDs match between JSON and tmux

**Story 2.3 (Get Window Details) Dependencies:**
- Uses session.Windows array we populate here
- Will look up windows by tmuxWindowId
- Will display window metadata (name, command)

**Story 3.1 (Recovery Detection) Dependencies:**
- Uses session.Windows array to know what to recover
- Checks if session exists in tmux (SessionExists already exists)

**Story 3.2 (Auto Recovery) Dependencies:**
- Uses recoveryCommand we store here to recreate windows
- Uses tmuxWindowId to preserve window identifiers
- Uses CreateWindow() method we implement here

**Story 3.3 (Recovery Verification) Dependencies:**
- Uses ListWindows() to verify windows exist after recovery
- Compares tmuxWindowId between JSON and live tmux state

### References

- [Source: epics.md#Story 2.1 Lines 598-688] - Complete story requirements
- [Source: epics.md#Epic 2 Lines 594-597] - Epic 2 overview and goals
- [Source: architecture.md#FR6] - Create window with name requirement
- [Source: architecture.md#FR7] - Specify recovery command requirement
- [Source: architecture.md#FR10] - Tmux-native window IDs (@0, @1...)
- [Source: architecture.md#FR19] - Store window metadata
- [Source: architecture.md#FR20] - Real-time session file updates
- [Source: architecture.md#FR31] - Accept window arguments
- [Source: architecture.md#NFR3] - Performance requirement <1 second
- [Source: architecture.md#NFR17] - Atomic file writes
- [Source: architecture.md#AR7] - TmuxExecutor interface pattern
- [Source: architecture.md#AR8] - POSIX exit codes
- [Source: coding-rules.md#CR1] - TDD mandatory (Red → Green → Refactor)
- [Source: coding-rules.md#CR2] - Test coverage >80%
- [Source: coding-rules.md#CR4] - Table-driven tests
- [Source: coding-rules.md#CR5] - Mock external dependencies
- [Source: coding-rules.md#CR12] - Return errors explicitly
- [Source: project-context.md] - Testing rules (full output, exit codes, LSP usage, real command verification)
- [Source: 1-2-session-store-with-atomic-file-operations.md] - SessionStore pattern, Window struct, atomic writes
- [Source: 1-3-create-session-command.md] - TmuxExecutor pattern, SessionExists() method
- [Source: 1-5-list-status-commands.md] - Command structure patterns, error handling

### Project Structure Notes

**Alignment with Unified Project Structure:**

This story extends the existing internal package structure established in Epic 1:

```
internal/
├── tmux/         # Extended with CreateWindow() and ListWindows() methods
├── store/        # No changes - Window struct already exists
└── testutil/     # Extended mock_tmux.go with new methods
cmd/tmux-cli/
└── session.go    # Extended with windows subcommand structure
```

**No New Packages Needed** - clean extension of existing architecture.

**Command Hierarchy:**
```
tmux-cli
└── session
    ├── start          (Story 1.3)
    ├── kill           (Story 1.4)
    ├── end            (Story 1.4)
    ├── list           (Story 1.5)
    ├── status         (Story 1.5)
    └── windows        (NEW)
        ├── create     (This story - 2.1)
        ├── list       (Story 2.2)
        └── get        (Story 2.3)
```

**No Conflicts Detected:**
- Follows established TmuxExecutor pattern
- Extends SessionStore usage (Load/Save)
- Follows Cobra command structure from Epic 1
- Maintains consistency with previous commands
- Respects package boundaries
- Uses existing error handling patterns
- Window struct already defined in Story 1.2

## Dev Agent Record

### Agent Model Used

Claude Sonnet 4.5 (claude-sonnet-4-5-20250929)

### Debug Log References

### Completion Notes List

✅ **Story 2.1 Implementation Completed Successfully**

**What Was Implemented:**
1. Extended TmuxExecutor interface with CreateWindow() and ListWindows() methods
2. Implemented RealTmuxExecutor.CreateWindow() with tmux window ID parsing (@0, @1, @2...)
3. Added windows subcommand structure to CLI: `session --id <uuid> windows create --name <name> --command <command>`
4. Implemented window persistence - appends Window metadata to session.Windows array and saves with atomic write
5. Comprehensive test coverage - unit tests, integration tests, error handling tests
6. Performance validated - window creation completes in ~80ms (well under 1 second requirement)

**Key Accomplishments:**
- Window creation with tmux-native IDs (@0, @1...) properly parsed and validated
- Recovery commands stored in JSON for Epic 3's automatic recovery feature
- Atomic writes ensure no data loss during window creation
- All error cases handled: session not found, missing flags, killed sessions
- Exit codes follow POSIX standards (0=success, 1=error, 2=usage error)

**Test Results:**
- All unit tests pass (exit code 0)
- Integration tests with real tmux pass
- Window IDs correctly parsed (@29, @30 observed in testing)
- JSON persistence verified - windows array correctly populated
- Error handling verified for all edge cases

**Performance Metrics:**
- Window creation: 80ms (requirement: <1000ms) ✅
- Well within performance requirements

### File List

**Modified Files:**
- internal/tmux/executor.go - Extended interface with CreateWindow() and ListWindows()
- internal/tmux/real_executor.go - Implemented CreateWindow() and ListWindows() methods
- internal/tmux/real_executor_test.go - Added comprehensive tests for CreateWindow()
- internal/tmux/executor_test.go - Updated mock executor to match new interface
- internal/testutil/mock_tmux.go - Updated mock methods for new interface
- internal/session/manager_test.go - Updated mock executor in session tests
- cmd/tmux-cli/session.go - Added windows subcommand and create command implementation
- cmd/tmux-cli/session_test.go - Updated tests for persistent --id flag

**No New Files Created:**
- All changes were extensions to existing Epic 1 architecture
- Clean integration with existing codebase structure
