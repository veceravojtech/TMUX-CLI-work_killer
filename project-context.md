# Project Context: tmux-cli

> **CRITICAL:** This document is the authoritative source of truth for all AI dev agents working on this project. All rules marked as STRICT must be followed without exception.

## Project Overview

**Name:** tmux-cli
**Language:** Go 1.25.5
**Framework:** Cobra CLI
**Testing:** testify (github.com/stretchr/testify)
**Architecture:** Internal package structure with clear separation of concerns
**TDD:** Always use TDD workflow

### Package Structure
```
cmd/tmux-cli/          # CLI entry point and command definitions
internal/
  ├── store/           # Session storage and persistence
  ├── tmux/            # Tmux execution and session management
  ├── mcp/             # NEW: MCP server integration
  └── testutil/        # Testing utilities and mocks
```

### MCP Integration Stack (NEW)

**MCP SDK:** v1.2.0+ (`github.com/modelcontextprotocol/go-sdk`)
- Installation: `go get github.com/modelcontextprotocol/go-sdk@v1.2.0`
- **STRICT:** Must be v1.2.0 or later (earlier versions lack critical protocol features)
- **STRICT:** Use ONLY official SDK from modelcontextprotocol (no forks or alternatives)
- Requires: Go 1.21+ (compatible with project's Go 1.25.5)

**New Package:** `internal/mcp/` - MCP server integration
- 8 new files: server.go, tools.go, errors.go, + tests
- New command: `tmux-cli mcp` (Cobra integration)

---

## STRICT Testing Rules

> **WHY THESE RULES EXIST:** To prevent silent failures, infinite retry loops, and incomplete test diagnostics that waste development time.

### Rule 0: ALWAYS USE TDD (STRICT)
- ✅ **ALWAYS** use test-driven development workflow
- ❌ **NEVER** implement write code without writing tests first

### Rule 0.5: NEVER USER FALLBACK (STRICT)
- ✅ **ALWAYS** throw errors when something goes wrong
- ❌ **NEVER** goes to fallback logic when something fails

### Rule 1: Full Test Output Required (STRICT)
- ✅ **ALWAYS** capture complete test output - never truncate or summarize
- ✅ When running `go test`, pipe output to capture everything: `go test [path] -v 2>&1`
- ❌ **NEVER** use `head`, `tail`, or any command that truncates test output
- ❌ **NEVER** rely on summarized test results when exit code ≠ 0

**Correct approach:**
```bash
# Good: Full output captured
go test ./internal/store/... -v 2>&1

# Good: Save to file for analysis
go test ./internal/store/... -v 2>&1 | tee test-output.log

# BAD: Truncated output hides failures
go test ./internal/store/... -v 2>&1 | head -50
```

### Rule 1. A : Always us LSP
- ✅ **ALWAYS** use gopls-lsp for working with Go lang

### Rule 2: Exit Code Validation (STRICT)
- ✅ **ALWAYS** check exit code: `echo $?` after test commands
- ✅ Exit code 0 = ALL tests passed
- ✅ Exit code 1 = AT LEAST ONE test failed or build error occurred
- ❌ **NEVER** consider tests passing if you see PASS messages but exit code ≠ 0
- ❌ **NEVER** mark a test verification step as complete if exit code ≠ 0

**The trap to avoid:**
```
=== RUN   TestSessionStore_Interface_Defined
--- PASS: TestSessionStore_Interface_Defined (0.00s)
=== RUN   TestSession_JSONMarshaling_EmptyWindows
--- PASS: TestSession_JSONMarshaling_EmptyWindows (0.00s)
... (more tests)
=== RUN   TestSomethingElse
--- FAIL: TestSomethingElse (0.00s)  ← THIS is why exit code = 1!

Exit code: 1  ← THIS means the entire test suite FAILED
```

### Rule 3: No Blind Retries (STRICT)
- ✅ If a command fails, analyze WHY before retrying
- ✅ Maximum 1 retry with additional diagnostic flags
- ❌ **NEVER** retry the same failing command more than ONCE without modification
- ❌ **NEVER** run identical commands repeatedly hoping for different results

**Progressive diagnostic approach:**
```bash
# First attempt
go test ./internal/store/... -v

# If it fails, retry with MORE information (not same command)
go test ./internal/store/... -v -run TestSession 2>&1 | tee debug.log
go test ./internal/store/... -v -failfast  # Stop at first failure
go list -json ./internal/store/...  # Check package structure
```

### Rule 4: Test Failure Analysis Protocol (STRICT)
When tests fail (exit code ≠ 0), follow this sequence:

1. **Capture full output** - see Rule 1
2. **Identify the specific failing test(s)** - look for `--- FAIL:` lines
3. **Read the failure message** - understand WHY it failed
4. **Check test assumptions** - verify test data, mocks, setup
5. **Fix the root cause** - don't mask failures
6. **Re-run and verify** - confirm exit code = 0

### Rule 5: Working Directory Awareness (STRICT)
- ✅ **ALWAYS** verify you're in project root before running `./internal/...` paths
- ✅ Use `pwd` to confirm location if uncertain
- ✅ Alternatively, use absolute paths or `cd` to project root first
- ❌ **NEVER** assume current directory without verification

```bash
# Verify location first
pwd  # Should show: /home/console/PhpstormProjects/CLI/tmux-cli

# Then run tests
go test ./internal/store/... -v
```

### Rule 6: Real Command Execution Verification (STRICT)
- ✅ **ALWAYS** add a final task to execute new commands/functionality in real environment
- ✅ After implementing new functionality, actually run the built binary to verify behavior
- ✅ Verify the command works end-to-end, not just that tests pass
- ✅ Include this as the LAST item in every TodoList for new features
- ❌ **NEVER** mark implementation complete without real execution verification
- ❌ **NEVER** rely solely on unit tests - run the actual binary

**Verification protocol:**
```bash
# 1. Build the binary
go build -o tmux-cli ./cmd/tmux-cli

# 2. Run the new command in real environment
./tmux-cli [command] [subcommand] --help  # Verify help works
./tmux-cli [command] [subcommand]         # Actually execute

# 3. Verify expected behavior
# - Check output matches expectations
# - Verify side effects (files created, sessions started, etc.)
# - Test error cases if applicable
```

**Example TodoList pattern:**
```
- [ ] Implement [feature-name] logic
- [ ] Write unit tests for [feature-name]
- [ ] Execute `./tmux-cli [new-command]` in real environment and verify behavior ← ALWAYS LAST
```

**Why this matters:** Unit tests can pass while the actual command fails due to integration issues, missing dependencies, or runtime errors. Real execution catches what tests miss.

---

## Development Workflow Rules

### Test-Driven Development (TDD) Protocol
When implementing new features or fixing bugs:

1. **RED Phase:** Write failing test first
   - Verify test FAILS (exit code = 1) for the right reason
   - Confirm failure message matches expected behavior

2. **GREEN Phase:** Write minimal code to make test pass
   - Verify test PASSES (exit code = 0)
   - **CRITICAL:** If exit code ≠ 0, you are NOT in GREEN phase yet

3. **REFACTOR Phase:** Improve code while maintaining passing tests
   - Re-run tests after each refactor
   - Exit code must remain 0 throughout

### Code Quality Standards
- Use `gofmt` for formatting
- Run `go vet` before committing
- All exported functions/types must have godoc comments
- Test coverage should be meaningful, not just hitting metrics

---

## Package-Specific Guidelines

### internal/store
- Handles session persistence to filesystem
- All file operations must be atomic (see `atomic_write.go`)
- JSON validation tests ensure backward compatibility
- Mock file systems for testing when possible

### internal/tmux
- Wraps tmux command execution
- All tmux interactions go through executor pattern
- Use testutil mocks for testing (never call real tmux in tests)

### internal/testutil
- Shared testing utilities and mocks
- Keep mocks simple and focused
- Mock only external dependencies (tmux commands, filesystem)

---

## Common Pitfalls to Avoid

❌ **Don't assume tests pass based on partial output**
❌ **Don't retry commands without understanding why they failed**
❌ **Don't truncate test output - you'll miss the failure**
❌ **Don't ignore exit codes**
❌ **Don't run tests from wrong directory**

✅ **Do capture complete test output**
✅ **Do verify exit codes explicitly**
✅ **Do analyze failures before retrying**
✅ **Do run from project root or use absolute paths**
✅ **Do follow the TDD cycle strictly**

---

## Git Workflow
- Default branch: `main`
- Commit messages should be clear and descriptive
- Run full test suite before committing: `go test ./...`
- Ensure exit code = 0 before marking work complete

---

## Questions or Clarifications?
If you encounter ambiguity or need clarification on these rules, STOP and ask the human developer (Vojta) rather than making assumptions.

**Remember:** These rules exist to save time and prevent frustrating debugging cycles. Follow them strictly.

## MCP Integration Rules (STRICT)

> **WHY THESE RULES EXIST:** To prevent implementation conflicts when multiple AI agents work on MCP server integration. These patterns ensure consistent code across all MCP components.

### Rule 7: MCP Package & File Naming (STRICT)

- ✅ **ALWAYS** use simple descriptive names: `server.go`, `tools.go`, `errors.go`
- ❌ **NEVER** include package name in filename: `mcp_server.go`, `mcp_tools.go`
- ✅ Co-locate tests: `server_test.go` next to `server.go`
- ✅ Integration tests: `server_integration_test.go` with `// +build tmux,integration` tag

**Correct structure:**
```
internal/mcp/
  ├── server.go                    # MCP server implementation
  ├── server_test.go               # Unit tests
  ├── server_integration_test.go   # Integration tests (with build tag)
  ├── tools.go                     # MCP tool handlers
  ├── tools_test.go                # Tool unit tests
  ├── errors.go                    # Error type definitions
  ├── errors_test.go               # Error handling tests
  └── mock_store_test.go           # Mock SessionStore
```

### Rule 8: MCP Function Naming Pattern (STRICT)

- ✅ **ALWAYS** use Resource + Action pattern: `WindowsList`, `WindowsCreate`, `WindowsGet`
- ❌ **NEVER** use Action + Resource pattern: `ListWindows`, `CreateWindow`

**Examples:**
```go
// ✅ CORRECT
func (s *Server) WindowsList(sessionID string) ([]Window, error)
func (s *Server) WindowsCreate(sessionID, name string) (*Window, error)
func (s *Server) WindowsGet(sessionID, windowID string) (*Window, error)
func (s *Server) WindowsKill(sessionID, windowID string) (bool, error)
func (s *Server) WindowsCaptureOutput(sessionID, windowID string) (string, error)
func (s *Server) WindowsSendCommand(sessionID, windowID, command string) (bool, error)

// ❌ WRONG
func (s *Server) ListWindows(sessionID string)
func (s *Server) CreateWindow(sessionID, name string)
```

### Rule 9: MCP Error Handling (STRICT)

- ✅ **ALWAYS** use categorized error types with `%w` wrapping
- ✅ **ALWAYS** include context (session ID, window ID, directory)
- ❌ **NEVER** use generic `errors.New()` without category
- ❌ **NEVER** use fallback logic (always throw categorized errors)

**10 Required Error Categories** (define in `internal/mcp/errors.go`):
```go
var (
    // Session Errors
    ErrSessionNotFound     = errors.New("session file not detected")
    ErrSessionNotDetected  = errors.New("session auto-detection failed")
    
    // Window Errors
    ErrWindowNotFound      = errors.New("window not found")
    ErrWindowCreateFailed  = errors.New("window creation failed")
    
    // Tmux Errors
    ErrTmuxNotRunning      = errors.New("tmux session not running")
    ErrTmuxCommandFailed   = errors.New("tmux command execution failed")
    
    // Validation Errors
    ErrInvalidSessionID    = errors.New("invalid session ID format")
    ErrInvalidWindowID     = errors.New("invalid window ID format")
    
    // Filesystem Errors
    ErrWorkingDirNotFound  = errors.New("working directory not accessible")
    ErrSessionFileCorrupt  = errors.New("session file corrupted")
)
```

**Context Wrapping Pattern:**
```go
// ✅ CORRECT - Categorized with context
return nil, fmt.Errorf("%w: session=%s window=%s", ErrWindowNotFound, sessionID, windowID)
return nil, fmt.Errorf("%w in directory %s", ErrSessionNotFound, s.workingDir)
return nil, fmt.Errorf("%w: %s", ErrTmuxCommandFailed, err)

// ❌ WRONG - Generic errors
return nil, errors.New("window not found")
return nil, fmt.Errorf("session not found")
```

### Rule 10: MCP Import Organization (STRICT)

- ✅ **ALWAYS** group imports: stdlib → external → internal
- ✅ Use blank lines between groups
- ✅ Alphabetize within each group

**Example:**
```go
import (
    // Standard library
    "errors"
    "fmt"
    "os"
    "path/filepath"
    
    // External dependencies
    "github.com/modelcontextprotocol/go-sdk/server"
    
    // Internal packages
    "github.com/console/tmux-cli/internal/session"
    "github.com/console/tmux-cli/internal/store"
    "github.com/console/tmux-cli/internal/tmux"
)
```

### Rule 11: MCP Session Detection (STRICT)

- ✅ **ALWAYS** detect session from current working directory
- ✅ Session file MUST be `.tmux-cli-session.json` in working directory
- ❌ **NEVER** use directory tree walking, env vars, or config files
- ❌ **NEVER** traverse parent directories

**Implementation Pattern:**
```go
// ✅ CORRECT - Zero-config from working directory
func NewServer() (*Server, error) {
    workingDir, err := os.Getwd()
    if err != nil {
        return nil, fmt.Errorf("failed to get working directory: %w", err)
    }
    
    // Session file expected at: {workingDir}/.tmux-cli-session.json
    sessionFile := filepath.Join(workingDir, ".tmux-cli-session.json")
    
    // Verify file exists
    if _, err := os.Stat(sessionFile); err != nil {
        return nil, fmt.Errorf("%w in directory %s: expected %s",
            ErrSessionNotFound, workingDir, sessionFile)
    }
    
    // ... rest of initialization ...
}

// ❌ WRONG - Directory traversal
// ❌ for dir := workingDir; dir != "/"; dir = filepath.Dir(dir) { ... }
```

### Rule 12: MCP Direct Internal Calls (STRICT)

- ✅ **ALWAYS** call internal packages directly (session.Manager, store.SessionStore, tmux.Executor)
- ❌ **NEVER** execute CLI commands as subprocess
- ❌ **NEVER** use `exec.Command("tmux-cli", ...)`

**Pattern:**
```go
// ✅ CORRECT - Direct internal calls
type Server struct {
    sessionMgr  *session.Manager
    executor    tmux.TmuxExecutor
    store       store.SessionStore
    workingDir  string
}

func (s *Server) WindowsList(sessionID string) ([]Window, error) {
    session, err := s.store.Load(sessionID)
    if err != nil {
        return nil, fmt.Errorf("%w: session=%s", ErrSessionNotFound, sessionID)
    }
    return session.Windows, nil
}

// ❌ WRONG - Subprocess execution
// ❌ cmd := exec.Command("tmux-cli", "session", "list")
// ❌ output, err := cmd.Output()
```

### Rule 13: MCP Response Format (STRICT)

- ✅ **ALWAYS** return Go types directly: `([]Window, error)`, `(*Window, error)`, `(string, error)`, `(bool, error)`
- ✅ Let MCP SDK handle JSON serialization
- ❌ **NEVER** pre-serialize to JSON strings
- ❌ **NEVER** return `interface{}`

**Tool Return Signatures:**
```go
// ✅ CORRECT - Go types, SDK handles serialization
windows-list    → ([]Window, error)
windows-create  → (*Window, error)
windows-get     → (*Window, error)
windows-kill    → (bool, error)
windows-capture → (string, error)
windows-send    → (bool, error)
```

### Rule 14: MCP Testing with Build Tags (STRICT)

- ✅ **ALWAYS** use build tags to separate unit tests from integration tests
- ✅ Unit tests: No build tag (run by default with `go test`)
- ✅ Integration tests: `// +build tmux,integration` tag (require real tmux)
- ❌ **NEVER** call real tmux in unit tests (use mocks)

**File Structure:**
```go
// server_test.go - Unit tests (no build tag)
package mcp

import "testing"

func TestServer_WindowsList_Success(t *testing.T) {
    // Use mock SessionStore
    mockStore := &MockSessionStore{...}
    server := &Server{store: mockStore}
    // ...
}

// server_integration_test.go - Integration tests
// +build tmux,integration

package mcp

import "testing"

func TestServer_WindowsList_RealTmux(t *testing.T) {
    // Uses real tmux.Executor and real session files
    // ...
}
```

**Run commands:**
```bash
# Unit tests only (fast, no tmux required)
go test ./internal/mcp/... -v

# Integration tests (requires tmux)
go test -tags=tmux,integration ./internal/mcp/... -v
```

---
