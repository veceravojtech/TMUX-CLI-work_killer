# Tech-Spec: Kill Window Command - Robustness Improvements

**Created:** 2025-12-30
**Status:** Ready for Development
**Parent Spec:** tech-spec-kill-window.md

## Overview

### Problem Statement

The initial implementation of the `windows kill` command (tech-spec-kill-window.md) is functional and passes all tests, but adversarial review identified 8 areas for improvement related to robustness, data integrity, and test coverage. These improvements will make the command production-hardened and eliminate edge case risks.

### Solution

Address the deferred findings from adversarial review through three improvement phases:
1. **Phase 1 (Critical)**: Data integrity and robustness improvements
2. **Phase 2 (High)**: Comprehensive test coverage
3. **Phase 3 (Medium)**: User experience enhancements

### Scope (In/Out)

**In Scope:**
- Fix race condition between tmux kill and file save (F3)
- Add atomic operation guarantee or rollback mechanism (F9)
- Validate window belongs to correct session before kill (F4)
- Add comprehensive edge case test coverage (F7)
- Add automated end-to-end integration test (F2)
- Add behavioral mock verification tests (F11)
- Expand test coverage for error scenarios (F15)
- Improve feedback for non-existent window kills (F5)

**Out of Scope:**
- Confirmation prompts before kill
- Undo/rollback functionality for users
- Multi-window batch operations
- Performance optimizations beyond what's needed for correctness

## Context for Development

### Current Implementation Issues

**F3: Race Condition (Critical)**
```go
// Current problematic flow in runWindowsKill:
// Line 737: Kill window in tmux
executor.KillWindow(sessionID, windowIDFlag)
// Line 753: Save updated session file
fileStore.Save(sess)
// Problem: If save fails, window is killed in tmux but still in file
// Result: Recovery will recreate the killed window (wrong!)
```

**F9: No Atomic Operations (Critical)**
- No transaction semantics between tmux and filesystem operations
- System crash between kill and save leaves inconsistent state
- No automatic recovery or consistency checking mechanism

**F4: No Session Validation (Critical)**
- Window ID like "@1" could exist in multiple sessions
- Current code doesn't verify window actually belongs to target session
- Could accidentally kill wrong window if IDs drifted

**F7, F2, F11, F15: Test Coverage Gaps (High)**
- No tests for killing last window in session
- No tests for killing @0 when @1/@2 exist
- No tests for corrupted data (duplicate window IDs)
- No automated integration test
- No behavioral tests for runWindowsKill with mocks

**F5: Silent Failure UX (Medium)**
- Killing non-existent window returns success
- User feedback says "killed and removed" even if window didn't exist
- Misleading for troubleshooting

### Codebase Patterns

**Transaction Pattern (for atomic operations):**
```go
// Pattern used in other parts of codebase
// 1. Prepare changes (in-memory)
// 2. Validate all preconditions
// 3. Execute all operations
// 4. On any error, rollback completed operations
// 5. Only on full success, return nil
```

**Validation Pattern:**
```go
// Before operating on resource, verify it exists in expected state
// 1. Load from authoritative source (tmux)
// 2. Compare with expected state (session file)
// 3. If mismatch, error with context
// 4. If match, proceed with operation
```

### Files to Reference

**Implementation Files:**
- `cmd/tmux-cli/session.go` - runWindowsKill (lines 683-760)
- `internal/tmux/real_executor.go` - KillWindow (lines 213-241)
- `internal/store/file_store.go` - Save/Load operations

**Test Files:**
- `cmd/tmux-cli/session_test.go` - Add integration tests
- `internal/tmux/real_executor_test.go` - Add edge case tests

**Related Recovery Logic:**
- `internal/recovery/manager.go` - Session recovery behavior
- `cmd/tmux-cli/recovery_helper.go` - MaybeRecoverSession

## Implementation Plan

### Phase 1: Data Integrity & Robustness (Critical)

#### Task 1.1: Fix Race Condition (F3)

**Approach A: Save-Before-Kill**
```go
// Safer flow:
// 1. Load session and find window
// 2. Remove window from session.Windows (in memory)
// 3. Save updated session to file FIRST
// 4. Kill window in tmux SECOND
// 5. On kill failure, restore window to file (rollback)

// Pros: File always accurate or behind tmux (safe for recovery)
// Cons: Requires rollback logic if kill fails
```

**Approach B: Two-Phase Commit**
```go
// 1. Mark window as "pending deletion" in file
// 2. Kill in tmux
// 3. Remove from file completely
// 4. Recovery checks "pending deletion" state

// Pros: Explicit state machine
// Cons: More complex, requires new file format
```

**Recommendation:** Approach A (simpler, sufficient)

Tasks:
- [ ] Modify runWindowsKill to save file before killing in tmux
- [ ] Add rollback logic to restore window to file if kill fails
- [ ] Add test for save-succeeds-kill-fails scenario
- [ ] Add test for both-succeed scenario

#### Task 1.2: Add Atomic Operation Guarantee (F9)

**Approach: Rollback on Failure**
```go
// Transaction-like flow:
// 1. Snapshot original state
// 2. Save file (operation 1)
// 3. Kill in tmux (operation 2)
// 4. If step 3 fails, restore file from snapshot (rollback)
// 5. Return error with context about what was rolled back
```

Tasks:
- [ ] Create snapshot of session before modifications
- [ ] Implement rollback function to restore session from snapshot
- [ ] Add error messages indicating rollback occurred
- [ ] Test rollback behavior (kill fails after save succeeds)
- [ ] Test crash recovery (manual verification needed)

#### Task 1.3: Validate Window Belongs to Session (F4)

**Approach: Query tmux before kill**
```go
// Before killing:
// 1. Call executor.ListWindows(sessionID)
// 2. Verify windowIDFlag exists in returned list
// 3. If not found, return error (window not in this session)
// 4. If found, proceed with kill
```

Tasks:
- [ ] Add validation step in runWindowsKill before kill operation
- [ ] Call ListWindows to get current window list from tmux
- [ ] Check windowIDFlag exists in list
- [ ] Return clear error if window not in session
- [ ] Add test for window-in-different-session scenario
- [ ] Add test for window-in-correct-session scenario

### Phase 2: Comprehensive Test Coverage (High)

#### Task 2.1: Edge Case Unit Tests (F7)

Tests to add:
- [ ] Test killing last window in session (check if session auto-killed)
- [ ] Test killing @0 when @1 and @2 exist (order matters)
- [ ] Test killing middle window @1 when @0, @1, @2 exist
- [ ] Test session with duplicate window IDs (corrupted data)
- [ ] Test window exists in tmux but not in JSON file
- [ ] Test window exists in JSON but not in tmux (already dead)
- [ ] Test killing window immediately after creation
- [ ] Test concurrent kills (if applicable)

Location: `internal/tmux/real_executor_test.go`

#### Task 2.2: Integration Test (F2)

**End-to-End Test:**
```go
func TestWindowsKill_Integration_FullFlow(t *testing.T) {
    // 1. Create real tmux session with UUID
    // 2. Add 3 windows via CLI
    // 3. Kill middle window via CLI
    // 4. Verify window gone from tmux (ListWindows)
    // 5. Verify window removed from JSON file
    // 6. Verify other 2 windows still present in both
    // 7. Clean up session
}
```

Tasks:
- [ ] Add integration test to cmd/tmux-cli/session_test.go
- [ ] Test requires real tmux (skip if not available)
- [ ] Verify both tmux state and file state
- [ ] Add cleanup even if test fails

#### Task 2.3: Mock Verification Tests (F11)

**Behavioral Tests for runWindowsKill:**
```go
func TestRunWindowsKill_CallsExecutorCorrectly(t *testing.T) {
    // Setup mock executor
    // Call runWindowsKill
    // Assert: executor.KillWindow called with correct args
    // Assert: fileStore.Save called after executor
    // Assert: error handling works correctly
}
```

Tasks:
- [ ] Add mock-based test for successful kill flow
- [ ] Add mock-based test for executor error handling
- [ ] Add mock-based test for file save error handling
- [ ] Add mock-based test for recovery integration
- [ ] Verify call order (recovery → load → kill → save)

#### Task 2.4: Expand Error Scenario Tests (F15)

Tests to add:
- [ ] Test successful kill of existing window (happy path)
- [ ] Test error propagation from executor.KillWindow
- [ ] Test error propagation from fileStore.Save
- [ ] Test invalid window ID format (executor validation)
- [ ] Test empty session ID
- [ ] Test permission denied error from tmux
- [ ] Test tmux binary not found error

Location: `cmd/tmux-cli/session_test.go` and `internal/tmux/real_executor_test.go`

### Phase 3: User Experience (Medium)

#### Task 3.1: Improve Non-Existent Window Feedback (F5)

**Current Behavior:**
```
$ tmux-cli session --id uuid windows kill --window-id @999
Window @999 (unknown) killed and removed from session
# Misleading - window didn't exist!
```

**Proposed Behavior:**
```
$ tmux-cli session --id uuid windows kill --window-id @999
Window @999 not found in session (already removed or never existed)
# Clear - user knows window wasn't there
```

**Implementation:**
- Track whether window was actually killed or was already dead
- Modify success message based on actual vs idempotent kill
- Alternatively: Make idempotent kills non-silent (return special error)

Tasks:
- [ ] Decide on approach (verbose feedback vs special error)
- [ ] Modify KillWindow to return indicator of actual kill vs idempotent
- [ ] Update runWindowsKill success message based on indicator
- [ ] Add test for message when window exists
- [ ] Add test for message when window doesn't exist

## Acceptance Criteria

### Phase 1: Data Integrity & Robustness

**AC1: Race Condition Eliminated**
- Given runWindowsKill is called
- When file save succeeds but tmux kill fails
- Then session file is rolled back to original state
- And error message indicates rollback occurred

**AC2: Atomic Operations**
- Given system crash between file save and tmux kill
- When session is next accessed
- Then state is consistent (either window exists in both or neither)
- And recovery handles any inconsistencies

**AC3: Session Validation**
- Given window @1 exists in session A and session B
- When I kill @1 in session A
- Then only session A's window is killed
- And session B's window is untouched

### Phase 2: Comprehensive Test Coverage

**AC4: Edge Cases Tested**
- Given edge case test suite
- When I run tests for: last window, @0 kill, corrupted data
- Then all scenarios are covered
- And behavior is verified for each

**AC5: Integration Test**
- Given automated integration test
- When test runs in CI/CD with real tmux
- Then it verifies end-to-end flow
- And cleanup occurs even on failure

**AC6: Mock Verification**
- Given behavioral mock tests
- When I run mock test suite
- Then call order is verified (recovery → load → kill → save)
- And error paths are tested

**AC7: Error Coverage**
- Given error scenario tests
- When I run test suite
- Then all error paths have test coverage
- And error messages are verified

### Phase 3: User Experience

**AC8: Clear Feedback**
- Given I kill a non-existent window
- When command completes
- Then message clearly indicates window didn't exist
- And is distinguishable from actual kill success

## Testing Strategy

**Unit Tests:**
- Test each phase's changes in isolation
- Mock dependencies where appropriate
- Use table-driven tests for edge cases

**Integration Tests:**
- Test complete flow with real tmux
- Verify both tmux state and file state
- Test rollback scenarios

**Manual Verification:**
- Test crash scenarios (kill process between operations)
- Test with different tmux versions for last-window behavior
- Verify recovery works correctly after fixes

## Technical Decisions

### Decision 1: Save-Before-Kill Order
**Chosen:** Save file first, kill in tmux second
**Rationale:** Recovery always recreates from file, so file should be source of truth. If kill fails after save, we can rollback file. If save fails, we never kill.

### Decision 2: Rollback Mechanism
**Chosen:** Simple in-memory snapshot and restore
**Rationale:** Session objects are small, full snapshot is cheap. Alternative (logging operations for undo) is over-engineered.

### Decision 3: Session Validation Approach
**Chosen:** Query tmux before kill
**Rationale:** Extra tmux call is acceptable cost for correctness. Alternative (trust file) is unsafe.

### Decision 4: Non-Existent Window Feedback
**Chosen:** Distinct success messages for actual vs idempotent kill
**Rationale:** Idempotent behavior preserved, but user gets clear feedback. Alternative (error on idempotent) breaks idempotency contract.

## Dependencies

**Standard Library:**
- Same as parent spec

**Third-Party:**
- Same as parent spec

**Internal:**
- May need new helper functions in `internal/store` for snapshot/restore
- May need new validation helpers in `internal/tmux`

## Notes

**Migration Strategy:**
- All changes are backward compatible
- Existing session files work without modification
- No breaking changes to command interface

**Performance Impact:**
- Additional ListWindows call adds ~10-50ms per kill operation
- Snapshot/restore adds negligible memory overhead
- Acceptable for interactive CLI tool

**Rollout:**
- Can be implemented in phases (Phase 1 first, then 2, then 3)
- Each phase is independently valuable
- Phase 1 is highest priority (data integrity)

## Priority

**Phase 1 (Critical):** High priority - addresses data integrity risks
**Phase 2 (High):** Medium priority - improves confidence and maintainability
**Phase 3 (Medium):** Low priority - nice-to-have UX improvement

## Estimated Complexity

- **Phase 1:** Medium - Requires careful sequencing and error handling
- **Phase 2:** Medium - Substantial test writing but straightforward
- **Phase 3:** Low - Simple conditional message logic
