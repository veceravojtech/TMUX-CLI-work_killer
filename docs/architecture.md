# Architecture Documentation

## Executive Summary

**tmux-cli** is a Go-based CLI tool for managing tmux sessions with automatic recovery capabilities. The architecture follows clean separation of concerns with interface-driven design, enabling comprehensive testing while maintaining production reliability.

**Key Characteristics:**
- **Type**: Command-Line Interface (CLI) Tool
- **Language**: Go 1.25.5
- **Pattern**: Layered architecture with dependency injection
- **Testing**: TDD approach with ~51% test file coverage
- **Storage**: File-based JSON persistence with atomic writes
- **Recovery**: Automatic detection and recreation of killed sessions

## Technology Stack

| Component | Technology | Version | Purpose |
|-----------|-----------|---------|---------|
| Language | Go | 1.25.5 | Core implementation |
| CLI Framework | Cobra | v1.10.2 | Command routing and parsing |
| Testing | Testify | v1.11.1 | Assertions and mocking |
| UUID | google/uuid | v1.6.0 | Session ID generation |
| Build | Make | - | Build automation |
| External Dependency | tmux | 2.0+ | Terminal multiplexer |

## Architectural Pattern

### Command-Based Architecture (Cobra Pattern)

```
User Input → Cobra CLI → Command Handler → Business Logic → External Systems
             (cmd/)       (cmd/)            (internal/)      (tmux + files)
```

**Design Principles:**
1. **Separation of Concerns**: Clear boundaries between CLI, business logic, and external systems
2. **Interface-Driven**: All external dependencies behind interfaces (TmuxExecutor, SessionStore)
3. **Dependency Injection**: Components receive dependencies via constructors
4. **Testability**: Mocks enable fast unit tests without external dependencies
5. **Atomic Operations**: File operations use atomic write-then-rename pattern
6. **Error Handling**: Explicit error returns, no panics in business logic

## System Architecture

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        User Commands                        │
│              (tmux-cli session create, list, etc.)          │
└────────────────────────┬────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│                   CLI Layer (cmd/tmux-cli)                  │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────────┐ │
│  │  Root    │  │ Session  │  │ Recovery │  │   Helpers  │ │
│  │ Command  │  │ Commands │  │  Helper  │  │            │ │
│  └──────────┘  └──────────┘  └──────────┘  └────────────┘ │
└────────────────────────┬────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│              Business Logic Layer (internal/)               │
│  ┌───────────────────────────────────────────────────────┐ │
│  │            SessionManager (session/)                   │ │
│  │  - CreateSession, KillSession, EndSession             │ │
│  │  - Coordinates tmux + store with rollback             │ │
│  └─────────────┬─────────────────┬────────────────────────┘ │
│                │                 │                           │
│    ┌───────────▼──────┐  ┌──────▼────────────┐             │
│    │ RecoveryManager  │  │  Session          │             │
│    │  (recovery/)     │  │  Validation       │             │
│    │                  │  │  (session/)       │             │
│    └───────────┬──────┘  └───────────────────┘             │
│                │                                             │
└────────────────┼─────────────────────────────────────────────┘
                 │
     ┌───────────┴────────────┐
     │                        │
     ▼                        ▼
┌─────────────────┐  ┌──────────────────┐
│ TmuxExecutor    │  │  SessionStore    │
│  (tmux/)        │  │   (store/)       │
│ ┌─────────────┐ │  │ ┌──────────────┐ │
│ │Real         │ │  │ │FileStore     │ │
│ │Executor     │ │  │ │(JSON)        │ │
│ └─────────────┘ │  │ └──────────────┘ │
│ ┌─────────────┐ │  │ ┌──────────────┐ │
│ │Mock         │ │  │ │Atomic Write  │ │
│ │(testutil)   │ │  │ │Operations    │ │
│ └─────────────┘ │  │ └──────────────┘ │
└────────┬────────┘  └────────┬─────────┘
         │                    │
         ▼                    ▼
   ┌─────────┐          ┌──────────┐
   │  tmux   │          │  JSON    │
   │ binary  │          │  Files   │
   └─────────┘          └──────────┘
```

### Layer Responsibilities

#### CLI Layer (`cmd/tmux-cli`)
- **Responsibility**: User interaction and command routing
- **Components**:
  - `root.go` - Root command and application setup
  - `session.go` - Session management subcommands
  - `recovery_helper.go` - Recovery command integration
- **Dependencies**: Business logic layer only
- **Testing**: Integration tests with real components

#### Business Logic Layer (`internal/`)

##### SessionManager (`internal/session`)
- **Responsibility**: Orchestrates session lifecycle operations
- **Key Operations**:
  - `CreateSession`: Validates path → creates tmux session → saves to store (with rollback)
  - `KillSession`: Kills tmux session, preserves file for recovery
  - `EndSession`: Kills session + archives file to `ended/`
- **Pattern**: Facade over tmux + store
- **Error Handling**: Rollback on failures (e.g., kill tmux if store fails)

##### RecoveryManager (`internal/recovery`)
- **Responsibility**: Automatic session recovery
- **Key Operations**:
  - `IsRecoveryNeeded`: Detects killed sessions (file exists, tmux doesn't)
  - `RecoverSession`: Recreates session with original UUID + all windows
  - `VerifyRecovery`: Confirms session and windows are running
- **Recovery Flow**:
  1. Check if session file exists but tmux session doesn't
  2. Recreate tmux session with stored UUID
  3. Recreate all windows using stored recovery commands
  4. Update window IDs in storage
  5. Verify all components running

##### Session Validation (`internal/session`)
- **Responsibility**: Input validation
- **Operations**:
  - UUID format validation
  - Path existence checks
  - Input sanitization

#### External Interface Layer

##### TmuxExecutor (`internal/tmux`)
- **Interface**: Abstracts all tmux binary interactions
- **Implementations**:
  - `RealExecutor`: Production implementation using `os/exec`
  - `MockExecutor` (testutil): Test double for unit tests
- **Operations**:
  - CreateSession, KillSession, HasSession, ListSessions
  - CreateWindow, ListWindows, KillWindow
  - SendMessage (inter-window communication)
- **Testing Strategy**: Interface enables mocking without real tmux

##### SessionStore (`internal/store`)
- **Interface**: Session persistence abstraction
- **Implementation**: File-based JSON storage
- **Key Features**:
  - Atomic writes (write-then-rename to prevent corruption)
  - Directory structure: `active/` and `ended/`
  - JSON format with validation tests
- **Operations**:
  - Save, Load, Delete, List, Move
- **Data Model**:
  ```go
  type Session struct {
      SessionID   string
      ProjectPath string
      Windows     []Window
  }

  type Window struct {
      TmuxWindowID    string
      Name            string
      RecoveryCommand string
  }
  ```

## Data Architecture

### Session Data Model

```
Session
├── SessionID: string (UUID format)
├── ProjectPath: string (absolute path)
└── Windows: []Window
    ├── TmuxWindowID: string (e.g., "@0", "@1")
    ├── Name: string
    └── RecoveryCommand: string (command to recreate window)
```

### Storage Layout

```
~/.config/tmux-cli/
└── sessions/
    ├── active/          # Running or killed (recoverable) sessions
    │   ├── {uuid}.json
    │   ├── {uuid}.json
    │   └── ...
    └── ended/           # Permanently ended (archived) sessions
        ├── {uuid}.json
        └── ...
```

### File Format (JSON)

```json
{
  "SessionID": "3206dbb7-8f9a-4c8e-b123-456789abcdef",
  "ProjectPath": "/home/user/projects/myapp",
  "Windows": [
    {
      "TmuxWindowID": "@0",
      "Name": "editor",
      "RecoveryCommand": "nvim"
    },
    {
      "TmuxWindowID": "@1",
      "Name": "server",
      "RecoveryCommand": "npm run dev"
    }
  ]
}
```

## Key Design Decisions

### 1. Interface-Driven Design
**Decision**: All external dependencies behind interfaces
**Rationale**:
- Enables fast unit tests with mocks (no tmux required)
- Supports testing error scenarios impossible with real tmux
- Allows future implementation swaps (e.g., different storage backend)

### 2. Atomic File Operations
**Decision**: Write-then-rename pattern for all file saves
**Rationale**:
- Prevents corruption if process crashes mid-write
- Ensures file is always in valid state (never partial JSON)
- Implementation: Write to `.tmp` file, then atomic rename

### 3. Rollback on Errors
**Decision**: SessionManager cleans up partial state on failures
**Rationale**:
- Prevents orphaned tmux sessions if storage fails
- Maintains consistency between tmux and file system
- Example: If `store.Save()` fails, automatically kill created tmux session

### 4. UUID-Based Session IDs
**Decision**: Use UUIDs instead of sequential IDs
**Rationale**:
- Avoids collision in concurrent environments
- Enables distributed session management (future)
- Consistent session ID across recovery cycles

### 5. Recovery Commands in Storage
**Decision**: Store recovery command for each window
**Rationale**:
- Enables recreation of windows with correct working directory and command
- User can customize recovery behavior per window
- Supports complex multi-window workflows

### 6. Idempotent Operations
**Decision**: Kill operations don't error if already dead
**Rationale**:
- Makes scripts and automation more robust
- Aligns with Unix philosophy (exit 0 if desired state achieved)
- Prevents unnecessary error handling in user code

## Testing Strategy

### Test Pyramid

```
         ▲
        / \
       /   \
      / E2E \          verify-real.sh (real tmux)
     /───────\
    /         \
   / Integration\     *_integration_test.go (real tmux)
  /─────────────\
 /               \
/   Unit Tests    \   *_test.go (mocks)
───────────────────
```

### Test Types

1. **Unit Tests** (~90% of tests)
   - Use mocks (`testutil.MockTmuxExecutor`)
   - Fast execution (no external dependencies)
   - Run by default: `make test`
   - Pattern: `*_test.go`

2. **Integration Tests** (~10%)
   - Use real tmux binary
   - Validate actual tmux behavior
   - Run explicitly: `make test-tmux`
   - Pattern: `*_integration_test.go` with `// +build tmux`

3. **E2E Verification**
   - Full workflow testing with real tmux
   - Script: `scripts/verify-real-execution.sh`
   - Validates tmux-cli state matches tmux reality
   - Run before release: `make verify-real`

### Test Coverage Goals

- **Code Coverage**: >70% (measured by `make coverage`)
- **Critical Paths**: 100% (atomic writes, recovery, session create)
- **Error Scenarios**: All error paths tested
- **Integration**: All tmux operations verified

## Development Workflow

### TDD Cycle (Strict)

```
1. RED:    Write failing test → verify it fails
2. GREEN:  Write minimal code → verify test passes (exit code 0)
3. REFACTOR: Improve code → keep tests passing
```

**Critical Rule**: Exit code must be 0 after tests. See `project-context.md` for complete protocol.

### Build Pipeline

```
Code Change
    ↓
make fmt (format)
    ↓
make vet (static analysis)
    ↓
make test (unit tests)
    ↓
make test-tmux (integration)
    ↓
make build (compile)
    ↓
make verify-real (E2E)
    ↓
Commit & Push
```

## Deployment Architecture

### Distribution

- **Binary**: Single statically-linked Go binary
- **Installation**:
  - `make install` → `~/.local/bin/tmux-cli`
  - Manual: Copy `bin/tmux-cli` to any PATH directory
- **Dependencies**: tmux 2.0+ (runtime dependency)

### Runtime Requirements

- **OS**: Linux, macOS (any Unix-like with tmux)
- **Tmux**: Version 2.0 or later
- **Permissions**: User-level (no root required)
- **Configuration Directory**: `~/.config/tmux-cli/` (created automatically)

### System Integration

- **PATH**: Add installation directory to PATH for CLI access
- **Tmux Integration**: Runs tmux commands via `os/exec`
- **No Daemon**: No background processes (stateless CLI)

## Extension Points

### Adding New Commands

1. Define command in `cmd/tmux-cli/*.go`
2. Add to root command tree in `root.go`
3. Implement business logic in appropriate `internal/` package
4. Write unit tests with mocks
5. Add integration test (if tmux interaction)
6. Update help text and documentation

### Adding Storage Backends

Implement `store.SessionStore` interface:
```go
type SessionStore interface {
    Save(session *Session) error
    Load(id string) (*Session, error)
    Delete(id string) error
    List() ([]*Session, error)
    Move(id string, destination string) error
}
```

Example: Database backend, cloud storage, etc.

### Adding Tmux Operations

1. Add method to `TmuxExecutor` interface (`internal/tmux/executor.go`)
2. Implement in `RealExecutor` (`internal/tmux/real_executor.go`)
3. Update `MockExecutor` (`internal/testutil/mock_tmux.go`)
4. Write unit + integration tests

## Security Considerations

- **Input Validation**: All user inputs validated (UUIDs, paths)
- **Path Traversal**: Absolute paths only, validated existence
- **Command Injection**: No shell execution (uses `exec.Command` with args)
- **File Permissions**: Session files created with user-only permissions
- **No Secrets**: No passwords, tokens, or sensitive data stored

## Performance Characteristics

- **Session Creation**: <100ms (dominated by tmux startup)
- **File Operations**: <10ms (atomic writes to local filesystem)
- **Recovery Detection**: O(n) where n = number of sessions
- **Memory**: Minimal (~10MB resident for CLI invocation)
- **Storage**: ~1KB per session JSON file

## Future Considerations

- **Remote Sessions**: Potential for network-based session management
- **Session Templates**: Pre-configured window layouts
- **Backup/Restore**: Export/import session configurations
- **Cloud Sync**: Synchronize sessions across machines
- **Multi-User**: Shared session management (requires permissions model)

## References

- **Project Context**: `project-context.md` - Development rules and testing protocol
- **Testing Analysis**: `TESTING_ANALYSIS.md` - Testing strategy and discovered bugs
- **Source Tree**: `source-tree-analysis.md` - Detailed directory structure
- **Development Guide**: `development-guide.md` - Setup and workflow instructions
