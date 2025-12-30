# Tech-Spec: Kill Window Command

**Created:** 2025-12-30
**Status:** Completed

## Review Notes
- Adversarial review completed
- Findings: 15 total, 7 fixed, 8 deferred
- Resolution approach: auto-fix
- Critical improvements: idempotency checking, error messages, documentation
- Deferred items documented in: **tech-spec-kill-window-improvements.md**
  - Phase 1 (Critical): Race condition, atomic operations, session validation
  - Phase 2 (High): Comprehensive test coverage
  - Phase 3 (Medium): User experience improvements

## Overview

### Problem Statement

The tmux-cli tool currently supports creating, listing, and inspecting windows within sessions, but lacks the ability to kill (remove) individual windows. Users need to manually kill windows using raw tmux commands or kill the entire session, which is inefficient and breaks the tool's abstraction.

### Solution

Add a `windows kill` subcommand under the existing `session` command structure that:
1. Kills the window in the running tmux session
2. Removes the window from the session's JSON file (prevents recovery)
3. Follows existing patterns for validation, recovery, and error handling

### Scope (In/Out)

**In Scope:**
- Add `KillWindow` method to TmuxExecutor interface and implementation
- Add `windows kill` subcommand to CLI
- Validate window ID format and existence
- Remove window from session file after killing
- Trigger automatic session recovery if session is killed
- Full test coverage (unit + integration)
- Real command execution verification

**Out of Scope:**
- Killing multiple windows at once
- Confirmation prompts (user should be intentional)
- Undoing window kills
- Preserving killed windows for manual recovery

## Context for Development

### Codebase Patterns

1. **TmuxExecutor Interface Pattern**
   - Interface defined in `internal/tmux/executor.go`
   - Implementation in `internal/tmux/real_executor.go`
   - Mocks in `internal/testutil/mock_tmux.go`
   - All tmux operations go through executor

2. **Command Structure Pattern**
   - Commands defined as `var windowsKillCmd = &cobra.Command{...}`
   - RunE functions named `runWindowsKill`
   - Session ID comes from persistent flag on parent `session` command
   - Window ID comes from `--window-id` flag on the kill command

3. **Window Management Pattern**
   ```go
   // 1. Validate inputs (session ID, window ID)
   // 2. Create dependencies (executor, fileStore)
   // 3. Check for recovery and trigger if needed
   // 4. Load session from store
   // 5. Find window in session.Windows slice
   // 6. Perform operation (kill window in tmux)
   // 7. Update session data structure (remove from slice)
   // 8. Save updated session to store
   // 9. Print success message
   ```

4. **Error Handling Pattern**
   - Return `NewUsageError()` for invalid input (exit code 2)
   - Return `fmt.Errorf()` for runtime errors (exit code 1)
   - Use `errors.Is()` for specific error checks (e.g., `store.ErrSessionNotFound`)

5. **Testing Pattern**
   - Use testify assertions: `assert.NoError`, `require.NotNil`, etc.
   - Table-driven tests for validation functions
   - Command existence tests: `TestWindowsKillCmd_Exists`
   - Mock executor for integration tests

### Files to Reference

**Primary Implementation Files:**
- `cmd/tmux-cli/session.go` - Contains all window commands (create/list/get)
- `internal/tmux/executor.go` - TmuxExecutor interface
- `internal/tmux/real_executor.go` - Real tmux command execution
- `internal/store/types.go` - Session and Window structs

**Helper Functions to Reuse:**
- `validateWindowID(windowID string) error` - Already exists in session.go:536
- `findWindowByID(sess *store.Session, windowID string) (*store.Window, error)` - Already exists in session.go:562
- `MaybeRecoverSession(sessionID, recoveryManager, fileStore)` - Recovery helper in session.go

**Testing Reference Files:**
- `cmd/tmux-cli/session_test.go` - Command and validation tests
- `internal/tmux/real_executor_test.go` - Executor implementation tests
- `internal/testutil/mock_tmux.go` - Mock implementations

**Documentation:**
- `project-context.md` - STRICT testing rules (must follow!)

### Technical Decisions

1. **Kill Method Design**
   - Use `tmux kill-window -t <session>:<window>` command
   - Make it idempotent (no error if window already dead)
   - Match pattern of `KillSession` in real_executor.go:38

2. **Session File Update**
   - Remove window from `sess.Windows` slice
   - Use slice filtering: keep all windows except the killed one
   - Save atomically using `fileStore.Save(sess)`

3. **Error Cases**
   - Session not found: Return error
   - Window not found in file: Return error (fail fast)
   - Tmux command fails: Return error
   - File save fails: Return error (window killed in tmux but not removed from file)

4. **Command Placement**
   - Under `session --id <uuid> windows kill --window-id @N`
   - Consistent with existing `windows create/list/get`

## Implementation Plan

### Tasks

- [x] Add `KillWindow(sessionID, windowID string) error` to TmuxExecutor interface
- [x] Implement `KillWindow` in RealTmuxExecutor (use `tmux kill-window -t session:window`)
- [x] Add `KillWindow` mock method to MockTmuxExecutor in testutil
- [x] Add `windowsKillCmd` cobra.Command in session.go
- [x] Implement `runWindowsKill` function following existing pattern
- [x] Register `windowsKillCmd` under `windowsCmd.AddCommand()`
- [x] Write unit tests for `KillWindow` executor method
- [x] Write integration tests for kill flow
- [x] Write command registration tests
- [x] Write validation tests for window ID
- [x] Run full test suite and verify exit code = 0
- [x] Build binary and execute real command verification

### Acceptance Criteria

**AC1: TmuxExecutor Interface Extended**
- Given the TmuxExecutor interface
- When I add the KillWindow method
- Then it should accept sessionID and windowID strings
- And return an error

**AC2: KillWindow Executes Tmux Command**
- Given a valid sessionID and windowID
- When KillWindow is called
- Then it should execute `tmux kill-window -t <session>:<window>`
- And return nil on success
- And be idempotent (no error if window already dead)

**AC3: Command Registration**
- Given the CLI command structure
- When I run `tmux-cli session --id <uuid> windows kill --help`
- Then it should display help for the kill command
- And show required --window-id flag

**AC4: Full Kill Flow**
- Given a session with multiple windows
- When I run `tmux-cli session --id <uuid> windows kill --window-id @1`
- Then the window @1 should be killed in tmux
- And the window should be removed from the session JSON file
- And other windows should remain untouched
- And the command should print "Window @1 (name) killed and removed from session"

**AC5: Session Recovery Integration**
- Given a killed session
- When I run `tmux-cli session --id <uuid> windows kill --window-id @0`
- Then the session should be automatically recovered
- And the window kill should proceed normally

**AC6: Error Handling**
- Given invalid inputs
- When window ID format is invalid (e.g., "0" instead of "@0")
- Then return usage error with exit code 2
- When session doesn't exist
- Then return error "session <id> not found"
- When window doesn't exist in session file
- Then return error "window @N not found in session <id>"

**AC7: Tests Pass**
- Given the full test suite
- When I run `go test ./...`
- Then exit code must be 0
- And all tests must pass

**AC8: Real Execution Verification**
- Given the built binary
- When I create a session with multiple windows
- And run `./tmux-cli session --id <uuid> windows kill --window-id @1`
- Then the window should be removed from tmux
- And the window should be removed from the session file
- And other windows should still be present

## Additional Context

### Dependencies

**Standard Library:**
- `os/exec` - For tmux command execution
- `fmt` - Error formatting
- `errors` - Error type checks

**Third-Party:**
- `github.com/spf13/cobra` - CLI framework
- `github.com/stretchr/testify` - Testing assertions

**Internal:**
- `github.com/console/tmux-cli/internal/tmux` - Executor
- `github.com/console/tmux-cli/internal/store` - Session persistence
- `github.com/console/tmux-cli/internal/session` - Validation
- `github.com/console/tmux-cli/internal/recovery` - Auto-recovery

### Testing Strategy

**Unit Tests:**
- Test `KillWindow` executor with mock commands
- Test window ID validation (valid/invalid formats)
- Test command registration and flag requirements

**Integration Tests:**
- Test full flow with mock executor + real store
- Test window removal from session slice
- Test error cases (not found, invalid format, etc.)

**Real Execution Test:**
- Build binary: `go build -o tmux-cli ./cmd/tmux-cli`
- Create test session with 3 windows
- Kill middle window
- Verify window gone from tmux and file
- Verify other windows still present

### Notes

**Critical Testing Rules (from project-context.md):**
- ALWAYS capture full test output (never truncate)
- ALWAYS check exit code with `echo $?`
- Exit code 0 = all tests passed
- Exit code ≠ 0 = at least one test failed
- Never retry same command > 1 time without changes
- Always verify working directory before running tests
- MUST include real execution verification as final task

**Tmux Command Reference:**
```bash
# Kill window command format:
tmux kill-window -t <session>:<window_id>

# Example:
tmux kill-window -t my-session:@1
```

**Window Slice Removal Pattern:**
```go
// Remove window from slice
updatedWindows := make([]store.Window, 0, len(sess.Windows)-1)
for _, w := range sess.Windows {
    if w.TmuxWindowID != windowIDToKill {
        updatedWindows = append(updatedWindows, w)
    }
}
sess.Windows = updatedWindows
```

**Success Message Format:**
Follow the pattern from windowsCreate (session.go:463):
```
Window @1 (editor) killed and removed from session
```
