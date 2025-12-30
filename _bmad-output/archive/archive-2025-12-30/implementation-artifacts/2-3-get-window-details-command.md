# Story 2.3: get-window-details-command

Status: review

<!-- Note: Validation is optional. Run validate-create-story for quality check before dev-story. -->

## Story

As a developer,
I want to retrieve detailed information about a specific window,
So that I can inspect a window's configuration and state.

## Acceptance Criteria

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

## Tasks / Subtasks

- [x] Implement windows get command (AC: #1)
  - [x] Add get subcommand to cmd/tmux-cli/session.go windows group
  - [x] Add --window-id flag to get subcommand (required)
  - [x] Implement workflow: validate inputs → load session → find window → display details
  - [x] Format output: show session ID, window ID, name, recovery command, status
  - [x] Verify exit codes: 0 on success, 1 if not found, 2 on usage error

- [x] Implement window lookup logic (AC: #2)
  - [x] Write failing test: TestFindWindowByID_Success
  - [x] Write failing test: TestFindWindowByID_NotFound
  - [x] Implement findWindowByID() function to search session.Windows array
  - [x] Return window if found, error if not found
  - [x] Verify tests pass (exit code 0)

- [x] Implement window status checking (AC: #2)
  - [x] Check if session is running via executor.HasSession()
  - [x] If session active, check if window exists in tmux via executor.ListWindows()
  - [x] Display "Running" if window found in live tmux
  - [x] Display "Not running (session killed)" if session not running
  - [x] Display "Not found in tmux" if session active but window missing

- [x] Validate window ID format (AC: #3)
  - [x] Write failing test: TestValidateWindowID_Valid
  - [x] Write failing test: TestValidateWindowID_Invalid
  - [x] Implement validateWindowID() function
  - [x] Check format: Must start with @ followed by digits (e.g., @0, @1, @123)
  - [x] Return clear error for invalid formats
  - [x] Verify tests pass (exit code 0)

- [x] Write comprehensive tests (AC: #4)
  - [x] Unit tests for findWindowByID() function
  - [x] Unit tests for validateWindowID() function
  - [x] Unit tests for windows get command workflow (mocked executor and store)
  - [x] Table-driven tests for: window found, window not found, invalid ID format, session not found
  - [x] Test output format and content
  - [x] Verify >80% test coverage for new code

- [x] Validate integration and performance (AC: #5)
  - [x] Run `make test` - all tests pass (exit code 0)
  - [x] Build binary: `go build -o tmux-cli ./cmd/tmux-cli`
  - [x] Create session and windows (from Story 2.2 verification)
  - [x] Get window details: `./tmux-cli session --id <uuid> windows get --window-id @0`
  - [x] Verify output shows all window details (ID, name, command, status)
  - [x] Test with non-existent window ID (verify error message)
  - [x] Test with invalid window ID format (verify usage error)
  - [x] Test with killed session (verify status shows "session killed")

## Dev Notes

### 🔥 CRITICAL CONTEXT: What This Story Accomplishes

**Story Purpose:**
This story implements window detail retrieval, enabling developers to:
- **Inspect specific windows** by their tmux-assigned window ID (@0, @1, etc.)
- **View complete window configuration** including name, recovery command, and runtime status
- **Verify window state** by checking if it exists in both JSON and live tmux
- **Debug window issues** by seeing if window disappeared from tmux but exists in session file

**Why This Matters:**
- Completes the window management trilogy: create, list, get
- Provides detailed inspection capability for debugging
- Shows discrepancies between persisted state and live tmux state
- Essential for understanding window configuration before recovery

**Architectural Integration:**
```
Window Get Workflow:
User → windows get cmd → Validate session ID and window ID format
                             ↓
              SessionStore.Load() → Verify session file exists
                             ↓
              findWindowByID() → Search session.Windows for matching ID
                             ↓
     If not found → Error: "Window @N not found in session"
                             ↓
     If found → TmuxExecutor.SessionExists() → Check if session running
                             ↓
     If session active → TmuxExecutor.ListWindows() → Check if window in tmux
                             ↓
     Display: Session ID, Window ID, Name, Recovery Command, Status
```

**Connection to Previous Stories:**
- **Story 2.1** - Uses window data structure and recovery command storage
- **Story 2.2** - Uses ListWindows() to verify window exists in live tmux
- **Epic 1** - Uses SessionStore.Load() and session data structure
- **Story 3.1-3.3** - Foundation for verifying windows after recovery

**Foundation for Recovery (Epic 3):**
- Recovery verification will use this to confirm windows recovered correctly
- Get by ID verifies specific window exists after recreation
- Status checking detects windows that failed to recreate

### Developer Guardrails: Prevent These Mistakes

**COMMON PITFALLS IN THIS STORY:**

❌ **Mistake 1: Invalid window ID format validation**
- Window IDs from tmux are: `@0`, `@1`, `@2`, `@123` (@ followed by digits)
- NOT: `0`, `window-0`, `@abc`, `@0x1`
- Must validate format before attempting lookup
- Clear error: "Window ID must be in format @N (e.g., @0, @1)"

❌ **Mistake 2: Inefficient window lookup**
- Don't call tmux to search for window by ID
- Don't load all windows from tmux and filter
- CORRECT: Iterate session.Windows array (already in memory)
- O(n) lookup is fine (sessions rarely have >10 windows)

❌ **Mistake 3: Incorrect status determination**
```go
// WRONG - Only checking tmux
status := "Not found"
if windowExistsInTmux {
    status = "Running"
}

// CORRECT - Check both session state and tmux state
if !sessionRunning {
    status = "Not running (session killed)"
} else if windowExistsInTmux {
    status = "Running"
} else {
    status = "Not found in tmux (may be dead)"
}
```

❌ **Mistake 4: Not displaying recovery command**
- Get window output MUST show recovery command
- This is critical metadata for understanding what window does
- User needs to know what command will run if window is recovered
- Don't omit it to "simplify" output

❌ **Mistake 5: Wrong error codes**
- Missing --window-id flag → exit code 2 (usage error)
- Invalid window ID format → exit code 2 (usage error)
- Session not found → exit code 1 (runtime error)
- Window not found in session → exit code 1 (runtime error)
- Don't use exit code 1 for usage errors!

❌ **Mistake 6: Not handling edge cases**
- Session exists but is killed (tmux session doesn't exist)
- Window exists in JSON but not in tmux (window died)
- Window ID format with large numbers (@123, @999)
- Session with no windows (empty array)

❌ **Mistake 7: Inconsistent command structure**
- NOT: `tmux-cli windows get --session-id <uuid> --window-id @0`
- NOT: `tmux-cli get window --id <uuid> --window @0`
- CORRECT: `tmux-cli session --id <uuid> windows get --window-id @0`
- Maintains consistency with create and list commands from Stories 2.1 and 2.2

### Technical Requirements from Previous Stories

**From Story 2.2 (List Windows):**

**TmuxExecutor.ListWindows() - ALREADY IMPLEMENTED:**
```go
// internal/tmux/executor.go
type TmuxExecutor interface {
    // ... existing methods
    ListWindows(sessionId string) ([]WindowInfo, error)  // Use this to check if window exists
}

type WindowInfo struct {
    TmuxWindowID string // @0, @1, @2...
    Name         string
    Running      bool   // True if in tmux list
}
```

**From Story 2.1 (Create Window):**

**Window Data Structure - ALREADY EXISTS:**
```go
// internal/store/session.go
type Session struct {
    SessionID   string   `json:"sessionId"`
    ProjectPath string   `json:"projectPath"`
    Windows     []Window `json:"windows"`  // Search this array
}

type Window struct {
    TmuxWindowID    string `json:"tmuxWindowId"`    // @0, @1, @2...
    Name            string `json:"name"`
    RecoveryCommand string `json:"recoveryCommand"` // Display in output
}
```

**From Story 1.2 (Session Store):**

**SessionStore Interface - ALREADY EXISTS:**
```go
// internal/store/store.go
type SessionStore interface {
    Load(id string) (*Session, error)  // Use this to get session
    // ... other methods
}
```

### Architecture Compliance

**Window ID Validation Function:**

```go
// cmd/tmux-cli/session.go or internal/tmux/validation.go

func validateWindowID(windowID string) error {
    // Window IDs must start with @ followed by one or more digits
    // Examples: @0, @1, @2, @123

    if !strings.HasPrefix(windowID, "@") {
        return fmt.Errorf("window ID must start with @ (e.g., @0, @1)")
    }

    // Extract numeric part
    numPart := windowID[1:]
    if len(numPart) == 0 {
        return fmt.Errorf("window ID must have a number after @ (e.g., @0, @1)")
    }

    // Verify numeric part is all digits
    for _, c := range numPart {
        if c < '0' || c > '9' {
            return fmt.Errorf("window ID must be @ followed by digits (e.g., @0, @1)")
        }
    }

    return nil
}
```

**Window Lookup Function:**

```go
// cmd/tmux-cli/session.go or internal/store/session.go

func findWindowByID(session *store.Session, windowID string) (*store.Window, error) {
    for i := range session.Windows {
        if session.Windows[i].TmuxWindowID == windowID {
            return &session.Windows[i], nil
        }
    }

    return nil, fmt.Errorf("window %s not found in session", windowID)
}
```

**Window Status Checking:**

```go
// cmd/tmux-cli/session.go

func getWindowStatus(executor tmux.TmuxExecutor, sessionID string, windowID string) (string, error) {
    // 1. Check if session is running
    sessionRunning, err := executor.SessionExists(sessionID)
    if err != nil {
        return "", fmt.Errorf("check session exists: %w", err)
    }

    if !sessionRunning {
        return "Not running (session killed)", nil
    }

    // 2. Check if window exists in tmux
    windows, err := executor.ListWindows(sessionID)
    if err != nil {
        // Session exists but can't list windows - unusual
        return "Unknown (error listing windows)", err
    }

    // 3. Search for window in live tmux list
    for _, w := range windows {
        if w.TmuxWindowID == windowID {
            return "Running", nil
        }
    }

    // Window not found in live tmux (may have died)
    return "Not found in tmux (may be dead)", nil
}
```

**Command Implementation:**

```go
// cmd/tmux-cli/session.go

var windowsGetCmd = &cobra.Command{
    Use:   "get",
    Short: "Get details of a specific window",
    Long: `Get detailed information about a specific window by its tmux window ID.

Shows window ID, name, recovery command, and current status (running or not).

Example:
  tmux-cli session --id abc-123 windows get --window-id @0`,
    RunE: runWindowsGet,
}

var windowIDFlag string

func init() {
    // Add to windowsCmd from Story 2.1
    windowsCmd.AddCommand(windowsGetCmd)

    // Add --window-id flag (required)
    windowsGetCmd.Flags().StringVar(&windowIDFlag, "window-id", "", "Tmux window ID (e.g., @0, @1)")
    windowsGetCmd.MarkFlagRequired("window-id")
}

func runWindowsGet(cmd *cobra.Command, args []string) error {
    // 1. Validate session ID (from parent flag)
    if sessionID == "" {
        return newUsageError("--id flag is required on session command")
    }

    if err := validateUUID(sessionID); err != nil {
        return newUsageError(fmt.Sprintf("invalid UUID format: %s", sessionID))
    }

    // 2. Validate window ID format
    if err := validateWindowID(windowIDFlag); err != nil {
        return newUsageError(err.Error())
    }

    // 3. Create dependencies
    executor := tmux.NewTmuxExecutor()
    fileStore := store.NewFileSessionStore()

    // 4. Load session from store
    session, err := fileStore.Load(sessionID)
    if err != nil {
        if errors.Is(err, store.ErrSessionNotFound) {
            return fmt.Errorf("session %s not found", sessionID)
        }
        return fmt.Errorf("load session: %w", err)
    }

    // 5. Find window in session
    window, err := findWindowByID(session, windowIDFlag)
    if err != nil {
        return fmt.Errorf("window %s not found in session %s", windowIDFlag, sessionID)
    }

    // 6. Get window status
    status, err := getWindowStatus(executor, sessionID, windowIDFlag)
    if err != nil {
        // Non-fatal error, show "Unknown" status
        status = "Unknown"
    }

    // 7. Display window details
    fmt.Println("Window Details:")
    fmt.Println()
    fmt.Printf("Session ID: %s\n", sessionID)
    fmt.Printf("Window ID: %s\n", window.TmuxWindowID)
    fmt.Printf("Name: %s\n", window.Name)
    fmt.Printf("Recovery Command: %s\n", window.RecoveryCommand)
    fmt.Printf("Status: %s\n", status)

    return nil
}
```

### Library/Framework Requirements

**No New Dependencies!**

**Standard Library Usage:**
- `strings` - HasPrefix, numeric validation ✅
- `fmt` - Error formatting and output ✅
- `errors` - Error checking (Is) ✅

**Existing Dependencies (from previous stories):**
- `github.com/spf13/cobra` - CLI framework ✅
- `github.com/google/uuid` - UUID validation ✅
- `github.com/stretchr/testify` - Testing/mocking ✅

**Reusing Existing Functionality:**
- TmuxExecutor.SessionExists() - from Story 1.3
- TmuxExecutor.ListWindows() - from Story 2.2
- SessionStore.Load() - from Story 1.2
- validateUUID() - from Story 1.3

### File Structure Requirements

**Files to Modify:**
```
cmd/tmux-cli/
└── session.go           # Add windowsGetCmd, runWindowsGet(), validateWindowID(), findWindowByID(), getWindowStatus()
```

**Files to Create for Tests:**
```
cmd/tmux-cli/
└── session_test.go      # Add tests for get command (may already exist from previous stories)
```

**Files Already Exist (No Changes Needed):**
- `internal/tmux/executor.go` - TmuxExecutor interface already has needed methods
- `internal/tmux/real_executor.go` - ListWindows() already implemented in Story 2.2
- `internal/testutil/mock_tmux.go` - MockTmuxExecutor already has needed mocks
- `internal/store/session.go` - Window struct already defined
- `internal/store/store.go` - SessionStore interface already defined

**No New Packages Needed** - pure implementation using existing infrastructure

### Testing Requirements

**TDD Workflow (MANDATORY - CR1):**

**Step 1: RED - Write Failing Tests**

**Validation Tests:**
```go
// cmd/tmux-cli/session_test.go

func TestValidateWindowID_ValidFormats(t *testing.T) {
    tests := []struct {
        name     string
        windowID string
    }{
        {"single digit", "@0"},
        {"double digit", "@12"},
        {"triple digit", "@123"},
        {"large number", "@9999"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := validateWindowID(tt.windowID)
            assert.NoError(t, err, "Expected %s to be valid", tt.windowID)
        })
    }
}

func TestValidateWindowID_InvalidFormats(t *testing.T) {
    tests := []struct {
        name     string
        windowID string
        errMsg   string
    }{
        {"missing @", "0", "must start with @"},
        {"@ only", "@", "must have a number"},
        {"non-numeric", "@abc", "must be @ followed by digits"},
        {"mixed", "@1a", "must be @ followed by digits"},
        {"space", "@ 1", "must be @ followed by digits"},
        {"negative", "@-1", "must be @ followed by digits"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := validateWindowID(tt.windowID)
            assert.Error(t, err)
            assert.Contains(t, err.Error(), tt.errMsg)
        })
    }
}
```

**Window Lookup Tests:**
```go
func TestFindWindowByID_Success(t *testing.T) {
    session := &store.Session{
        SessionID:   "test-uuid",
        ProjectPath: "/tmp/test",
        Windows: []store.Window{
            {
                TmuxWindowID:    "@0",
                Name:            "editor",
                RecoveryCommand: "vim",
            },
            {
                TmuxWindowID:    "@1",
                Name:            "tests",
                RecoveryCommand: "go test",
            },
        },
    }

    window, err := findWindowByID(session, "@1")

    assert.NoError(t, err)
    assert.Equal(t, "@1", window.TmuxWindowID)
    assert.Equal(t, "tests", window.Name)
    assert.Equal(t, "go test", window.RecoveryCommand)
}

func TestFindWindowByID_NotFound(t *testing.T) {
    session := &store.Session{
        SessionID:   "test-uuid",
        ProjectPath: "/tmp/test",
        Windows: []store.Window{
            {TmuxWindowID: "@0", Name: "editor", RecoveryCommand: "vim"},
        },
    }

    window, err := findWindowByID(session, "@99")

    assert.Error(t, err)
    assert.Nil(t, window)
    assert.Contains(t, err.Error(), "not found")
    assert.Contains(t, err.Error(), "@99")
}

func TestFindWindowByID_EmptySession(t *testing.T) {
    session := &store.Session{
        SessionID:   "test-uuid",
        ProjectPath: "/tmp/test",
        Windows:     []store.Window{},
    }

    window, err := findWindowByID(session, "@0")

    assert.Error(t, err)
    assert.Nil(t, window)
}
```

**Status Checking Tests:**
```go
func TestGetWindowStatus_SessionRunning_WindowExists(t *testing.T) {
    mockExecutor := new(testutil.MockTmuxExecutor)

    mockExecutor.On("SessionExists", "test-uuid").Return(true, nil)
    mockExecutor.On("ListWindows", "test-uuid").Return([]tmux.WindowInfo{
        {TmuxWindowID: "@0", Name: "editor", Running: true},
        {TmuxWindowID: "@1", Name: "tests", Running: true},
    }, nil)

    status, err := getWindowStatus(mockExecutor, "test-uuid", "@1")

    assert.NoError(t, err)
    assert.Equal(t, "Running", status)
}

func TestGetWindowStatus_SessionKilled(t *testing.T) {
    mockExecutor := new(testutil.MockTmuxExecutor)

    mockExecutor.On("SessionExists", "test-uuid").Return(false, nil)

    status, err := getWindowStatus(mockExecutor, "test-uuid", "@0")

    assert.NoError(t, err)
    assert.Equal(t, "Not running (session killed)", status)
}

func TestGetWindowStatus_SessionRunning_WindowNotInTmux(t *testing.T) {
    mockExecutor := new(testutil.MockTmuxExecutor)

    mockExecutor.On("SessionExists", "test-uuid").Return(true, nil)
    mockExecutor.On("ListWindows", "test-uuid").Return([]tmux.WindowInfo{
        {TmuxWindowID: "@1", Name: "tests", Running: true},
    }, nil)

    status, err := getWindowStatus(mockExecutor, "test-uuid", "@0")

    assert.NoError(t, err)
    assert.Contains(t, status, "Not found in tmux")
}
```

**Integration Tests (Full Command Workflow):**
```go
func TestWindowsGet_Success(t *testing.T) {
    mockExecutor := new(testutil.MockTmuxExecutor)
    mockStore := new(testutil.MockSessionStore)

    sessionID := "test-uuid"
    session := &store.Session{
        SessionID:   sessionID,
        ProjectPath: "/tmp/test",
        Windows: []store.Window{
            {
                TmuxWindowID:    "@0",
                Name:            "editor",
                RecoveryCommand: "vim main.go",
            },
        },
    }

    mockStore.On("Load", sessionID).Return(session, nil)
    mockExecutor.On("SessionExists", sessionID).Return(true, nil)
    mockExecutor.On("ListWindows", sessionID).Return([]tmux.WindowInfo{
        {TmuxWindowID: "@0", Name: "editor", Running: true},
    }, nil)

    // Execute command
    // Verify output contains:
    // - "Window Details:"
    // - "Session ID: test-uuid"
    // - "Window ID: @0"
    // - "Name: editor"
    // - "Recovery Command: vim main.go"
    // - "Status: Running"
}

func TestWindowsGet_WindowNotFound(t *testing.T) {
    mockStore := new(testutil.MockSessionStore)

    sessionID := "test-uuid"
    session := &store.Session{
        SessionID:   sessionID,
        ProjectPath: "/tmp/test",
        Windows: []store.Window{
            {TmuxWindowID: "@0", Name: "editor", RecoveryCommand: "vim"},
        },
    }

    mockStore.On("Load", sessionID).Return(session, nil)

    // Execute command with --window-id @99
    // Assert error contains "window @99 not found"
    // Assert exit code 1
}

func TestWindowsGet_InvalidWindowIDFormat(t *testing.T) {
    // Execute command with --window-id "invalid"
    // Assert error contains "Window ID must be in format @N"
    // Assert exit code 2 (usage error)
}

func TestWindowsGet_MissingFlags(t *testing.T) {
    // Execute command without --id flag
    // Assert error contains "--id flag is required"
    // Assert exit code 2

    // Execute command without --window-id flag
    // Assert error contains "--window-id"
    // Assert exit code 2
}
```

**Step 2: GREEN - Implement Functions**

(Implementation shown in "Architecture Compliance" section above)

**Step 3: REFACTOR - Improve While Keeping Tests Green**

### Performance Requirements

**From NFR4:**
- List/status queries: <500ms

**Expected Timings for Get Window:**
- UUID validation: <1ms
- Window ID validation: <1ms
- SessionStore.Load(): ~50ms (from Story 1.5)
- findWindowByID(): ~1ms (iterate small array)
- SessionExists(): ~50ms (from Story 1.3)
- ListWindows(): ~50-100ms (from Story 2.2)
- Display window: ~1ms
- **Total: ~153-203ms** (well within 500ms)

**Performance Considerations:**
- Window lookup is O(n) where n = number of windows (typically <10)
- No database queries or complex operations
- Single tmux command call (ListWindows)
- No optimization needed at this stage

### Integration with Project Context

**From project-context.md - Testing Rules:**

**Rule 1: Full Test Output (STRICT)**
```bash
# Always capture complete output
go test ./cmd/tmux-cli/... -v 2>&1
go test ./... -v 2>&1

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
go test ./cmd/tmux-cli/... -v
echo $?  # Must be 0

# If exit code != 0, you have failures!
```

**Rule 6: Real Command Execution Verification (STRICT)**
```bash
# 1. Build the binary
go build -o tmux-cli ./cmd/tmux-cli

# 2. Create session and windows (from Story 2.2)
SESSION_ID=$(uuidgen)
./tmux-cli session start --id $SESSION_ID --path /tmp/test-project
./tmux-cli session --id $SESSION_ID windows create --name editor --command "vim main.go"
./tmux-cli session --id $SESSION_ID windows create --name tests --command "go test -watch"

# 3. Get window details for @0
./tmux-cli session --id $SESSION_ID windows get --window-id @0

# 4. Verify output shows:
# - "Window Details:"
# - "Session ID: <uuid>"
# - "Window ID: @0"
# - "Name: editor"
# - "Recovery Command: vim main.go"
# - "Status: Running"

# 5. Get window details for @1
./tmux-cli session --id $SESSION_ID windows get --window-id @1

# 6. Verify output for second window

# 7. Test with non-existent window
./tmux-cli session --id $SESSION_ID windows get --window-id @99
# Verify error: "window @99 not found in session"
# Verify exit code: echo $? (should be 1)

# 8. Test with invalid window ID format
./tmux-cli session --id $SESSION_ID windows get --window-id invalid
# Verify error: "Window ID must be in format @N"
# Verify exit code: echo $? (should be 2)

# 9. Test with killed session
tmux kill-session -t $SESSION_ID
./tmux-cli session --id $SESSION_ID windows get --window-id @0
# Verify status shows: "Not running (session killed)"

# 10. Clean up
rm -f ~/.tmux-cli/sessions/$SESSION_ID.json
```

### Critical Implementation Considerations

**🔥 WINDOW ID VALIDATION:**

```go
// CORRECT - Proper format validation
func validateWindowID(windowID string) error {
    if !strings.HasPrefix(windowID, "@") {
        return fmt.Errorf("window ID must start with @ (e.g., @0, @1)")
    }

    numPart := windowID[1:]
    if len(numPart) == 0 {
        return fmt.Errorf("window ID must have a number after @ (e.g., @0, @1)")
    }

    // Verify all digits
    for _, c := range numPart {
        if c < '0' || c > '9' {
            return fmt.Errorf("window ID must be @ followed by digits (e.g., @0, @1)")
        }
    }

    return nil
}

// WRONG - Using regex or complex parsing
// ❌ matched, _ := regexp.MatchString(`^@\d+$`, windowID)
// While regex works, simple character checking is faster and clearer
```

**Efficient Window Lookup:**

```go
// CORRECT - Simple iteration
func findWindowByID(session *store.Session, windowID string) (*store.Window, error) {
    for i := range session.Windows {
        if session.Windows[i].TmuxWindowID == windowID {
            return &session.Windows[i], nil
        }
    }

    return nil, fmt.Errorf("window %s not found in session", windowID)
}

// WRONG - Building a map first
// ❌ windowMap := make(map[string]*store.Window)
// for i := range session.Windows {
//     windowMap[session.Windows[i].TmuxWindowID] = &session.Windows[i]
// }
// return windowMap[windowID]
// Overkill for small arrays, adds complexity
```

**Status Determination Logic:**

```go
// CORRECT - Check session first, then window
func getWindowStatus(executor tmux.TmuxExecutor, sessionID string, windowID string) (string, error) {
    // 1. If session not running, window can't be running
    sessionRunning, err := executor.SessionExists(sessionID)
    if err != nil {
        return "", err
    }

    if !sessionRunning {
        return "Not running (session killed)", nil
    }

    // 2. Session running, check if window exists in tmux
    windows, err := executor.ListWindows(sessionID)
    if err != nil {
        return "Unknown", err
    }

    for _, w := range windows {
        if w.TmuxWindowID == windowID {
            return "Running", nil
        }
    }

    return "Not found in tmux (may be dead)", nil
}

// WRONG - Checking window without checking session first
// ❌ windows, err := executor.ListWindows(sessionID)  // Fails if session not running!
```

**Error Code Consistency:**

```go
// CORRECT - Proper exit codes
if windowIDFlag == "" {
    return newUsageError("--window-id flag is required")  // Exit code 2
}

if err := validateWindowID(windowIDFlag); err != nil {
    return newUsageError(err.Error())  // Exit code 2 (invalid format)
}

if errors.Is(err, store.ErrSessionNotFound) {
    return fmt.Errorf("session %s not found", sessionID)  // Exit code 1
}

if err := findWindowByID(...); err != nil {
    return fmt.Errorf("window %s not found", windowID)  // Exit code 1
}

// WRONG - Using exit code 1 for usage errors
// ❌ return fmt.Errorf("--window-id flag is required")  // Should be exit code 2!
```

### Connection to Future Stories

**Story 3.1 (Recovery Detection) Dependencies:**
- Recovery will need to verify windows exist after recreation
- Get by ID provides verification mechanism
- Status checking detects if recovery succeeded

**Story 3.2 (Session Recreation) Dependencies:**
- After recreating windows, verify each one using get command
- Confirm window IDs match expected values
- Validate recovery command was executed correctly

**Story 3.3 (Recovery Verification) Dependencies:**
- Uses get window to verify specific windows recovered correctly
- Checks status to confirm window is running
- Validates all window metadata preserved during recovery

### References

- [Source: epics.md#Story 2.3 Lines 760-816] - Complete story requirements
- [Source: epics.md#Epic 2 Lines 594-597] - Epic 2 overview and goals
- [Source: epics.md#FR9] - Get window details by tmux window ID requirement
- [Source: architecture.md#NFR4] - Performance requirement <500ms
- [Source: architecture.md#AR8] - POSIX exit codes (0=success, 1=error, 2=usage)
- [Source: coding-rules.md#CR1] - TDD mandatory (Red → Green → Refactor)
- [Source: coding-rules.md#CR2] - Test coverage >80%
- [Source: coding-rules.md#CR4] - Table-driven tests for comprehensive scenarios
- [Source: project-context.md] - Testing rules (full output, exit codes, LSP usage, real verification)
- [Source: 2-1-create-window-command.md] - Window data structure, TmuxExecutor interface
- [Source: 2-2-list-windows-command.md] - ListWindows() implementation, command structure, previous learnings

### Project Structure Notes

**Alignment with Unified Project Structure:**

This story completes the window management command trilogy:

```
internal/
├── tmux/         # Use existing ListWindows(), SessionExists()
└── store/        # Use existing Load(), Window struct
cmd/tmux-cli/
└── session.go    # Add windowsGetCmd to windows subcommand
```

**No New Packages or Files** - extends existing command structure

**Command Hierarchy (Established in Stories 2.1-2.2):**
```
tmux-cli
└── session
    └── windows
        ├── create     (Story 2.1) ✅
        ├── list       (Story 2.2) ✅
        └── get        (This story - 2.3) ← Final window command
```

**No Conflicts Detected:**
- Uses TmuxExecutor methods from Stories 2.1-2.2
- Uses SessionStore from Story 1.2
- Follows command structure from Stories 2.1-2.2
- Maintains output formatting consistency with list command
- Follows error handling patterns from all previous stories

**Completing Window Management Capability:**
- Create windows (Story 2.1) ✅
- List all windows (Story 2.2) ✅
- Get specific window (Story 2.3) ← This story completes CRUD operations

## Dev Agent Record

### Agent Model Used

Claude Sonnet 4.5 (claude-sonnet-4-5-20250929)

### Debug Log References

No debug issues encountered during implementation.

### Completion Notes List

- ✅ Implemented `windows get` command following TDD (RED → GREEN → REFACTOR)
- ✅ Added comprehensive table-driven tests for validation, lookup, and command workflow
- ✅ All tests pass with exit code 0 (100% pass rate)
- ✅ Implemented `validateWindowID()` function with proper @ format validation
- ✅ Implemented `findWindowByID()` function using O(n) search through session.Windows
- ✅ Implemented `getWindowStatus()` function that checks both session and window state
- ✅ Proper error codes: exit code 0 (success), 1 (runtime error), 2 (usage error)
- ✅ Real command execution verified: get existing window, non-existent window, invalid format, killed session
- ✅ Output format matches specification: Session ID, Window ID, Name, Recovery Command, Status
- ✅ Status detection works correctly: "Running", "Not running (session killed)", "Not found in tmux"

### File List

cmd/tmux-cli/session.go
cmd/tmux-cli/session_test.go
