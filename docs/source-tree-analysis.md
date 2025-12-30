# Source Tree Analysis

## Project Structure Overview

```
tmux-cli/
├── cmd/
│   └── tmux-cli/          # CLI entry point and command definitions
│       ├── main.go        # Application entry point
│       ├── root.go        # Root Cobra command
│       ├── session.go     # Session management commands
│       ├── recovery_helper.go  # Recovery command helpers
│       └── *_test.go      # Command-level integration tests
│
├── internal/              # Internal packages (not importable by external code)
│   ├── session/           # Session orchestration layer
│   │   ├── manager.go     # SessionManager - coordinates tmux + store operations
│   │   ├── validation.go  # UUID and input validation
│   │   └── *_test.go
│   │
│   ├── store/             # Session persistence layer
│   │   ├── store.go       # SessionStore interface definition
│   │   ├── file_store.go  # File-based JSON implementation
│   │   ├── atomic_write.go # Atomic file write operations
│   │   ├── types.go       # Session and Window data structures
│   │   ├── constants.go   # File paths and defaults
│   │   ├── errors.go      # Storage error types
│   │   └── *_test.go      # Comprehensive storage tests
│   │
│   ├── tmux/              # Tmux execution layer
│   │   ├── executor.go    # TmuxExecutor interface
│   │   ├── real_executor.go    # Production implementation using os/exec
│   │   ├── command_wrapper.go  # Command wrapping for recovery
│   │   ├── session.go     # Session-specific operations
│   │   ├── errors.go      # Tmux error types
│   │   └── *_test.go      # Integration tests with real tmux
│   │
│   ├── recovery/          # Session recovery system
│   │   ├── recovery.go    # RecoveryManager implementation
│   │   └── *_test.go      # Recovery scenario tests
│   │
│   └── testutil/          # Testing utilities
│       ├── mock_tmux.go   # Mock TmuxExecutor for unit tests
│       └── *_test.go
│
├── scripts/               # Build and utility scripts
│   ├── verify-real-execution.sh  # E2E verification with real tmux
│   └── test-wrapping.go   # Test utilities
│
├── bin/                   # Compiled binaries (generated)
│   └── tmux-cli          # Built executable
│
├── Makefile              # Build automation
├── go.mod                # Go module definition
├── go.sum                # Dependency checksums
├── project-context.md    # Development guidelines and rules
└── TESTING_ANALYSIS.md   # Testing strategy documentation
```

## Critical Directories Explained

### `/cmd/tmux-cli`
**Purpose**: CLI command definitions and entry point
- Contains all Cobra command implementations
- Handles user input validation and help text
- Routes commands to appropriate managers
- Minimal business logic - delegates to internal packages
- **Entry Point**: `main.go` → `Execute()` → Cobra command tree

### `/internal/session`
**Purpose**: High-level session orchestration
- **Key File**: `manager.go` - SessionManager coordinates operations
- Combines tmux execution + file storage in atomic operations
- Implements business rules (e.g., cleanup on errors)
- Validates UUIDs and paths
- **Pattern**: Facade over tmux + store layers

### `/internal/store`
**Purpose**: Session state persistence
- **Key Files**:
  - `store.go` - SessionStore interface
  - `file_store.go` - JSON file implementation
  - `atomic_write.go` - Crash-safe file writes
  - `types.go` - Session and Window data models
- **Storage Location**: `~/.config/tmux-cli/sessions/`
- **Format**: JSON with atomic write-then-rename
- **Directories**:
  - `active/` - Running or killed sessions (recoverable)
  - `ended/` - Permanently ended sessions (archived)

### `/internal/tmux`
**Purpose**: Tmux command execution wrapper
- **Key Files**:
  - `executor.go` - Interface for testability
  - `real_executor.go` - Production implementation
  - `command_wrapper.go` - Command wrapping for recovery
- Abstracts all `tmux` binary interactions
- Parses tmux output into structured data
- **Testability**: Interface enables mocking in unit tests

### `/internal/recovery`
**Purpose**: Automatic session recovery
- Detects killed sessions (file exists, tmux session doesn't)
- Recreates sessions with original UUID
- Restores all windows using stored recovery commands
- Verifies recovery succeeded
- **Key Operations**: IsRecoveryNeeded, RecoverSession, VerifyRecovery

### `/internal/testutil`
**Purpose**: Shared testing infrastructure
- Mock TmuxExecutor for unit testing
- Avoids calling real tmux in fast tests
- Configurable mock behaviors for error scenarios

## Integration Points

### Command → Manager → Executor + Store
```
User runs: tmux-cli session create myproject /path

1. cmd/session.go validates input
2. Calls SessionManager.CreateSession()
3. SessionManager:
   - Calls executor.CreateSession() → tmux new-session
   - Creates Session object
   - Calls store.Save() → writes JSON file
   - On error: rolls back (kills tmux session)
```

### Recovery Flow
```
User runs: tmux-cli session list

1. Recovery detection runs automatically
2. RecoveryManager.IsRecoveryNeeded() checks each session:
   - Load session file (store)
   - Check if tmux session exists (executor)
3. If needed: RecoverSession() recreates session + windows
4. List command shows recovered sessions
```

## File Naming Conventions

- `*_test.go` - Test files (19 files total)
- `mock_*.go` - Mock implementations for testing
- `*_integration_test.go` - Integration tests (require real tmux)
- `errors.go` - Package-specific error definitions
- `types.go` - Data structure definitions
- `constants.go` - Package constants and defaults

## Code Statistics

- **Total Go Files**: 37
- **Production Code**: ~1,148 lines (internal/)
- **Test Files**: 19 (~51% test coverage by file count)
- **Packages**: 5 internal + 1 cmd
- **Interfaces**: 3 (TmuxExecutor, SessionStore, RecoveryManager)

## Build Artifacts

- `bin/tmux-cli` - Compiled binary (generated by make build)
- `coverage.out` - Test coverage data
- `coverage.html` - HTML coverage report
- `*.test` - Test binaries (temporary)
