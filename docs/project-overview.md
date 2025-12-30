# tmux-cli Project Overview

## Project Summary

**tmux-cli** is a command-line interface tool for managing tmux sessions with built-in automatic recovery capabilities. Written in Go 1.25.5 using the Cobra CLI framework, it provides session persistence and intelligent recovery when tmux sessions are killed.

**Version**: 0.1.0
**Type**: CLI Tool
**Primary Language**: Go
**Architecture**: Command-based with layered internal packages

## Purpose

The tool addresses the problem of lost work when tmux sessions are accidentally killed or system crashes occur. It maintains session state in JSON files and can automatically detect and recover killed sessions, restoring all windows with their original commands.

## Key Features

- **Session Management**: Create, kill, end, list, and get session status
- **Automatic Recovery**: Detect and recreate killed sessions with original UUIDs
- **Window Management**: Create, list, and kill windows within sessions
- **Inter-Window Communication**: Send messages between windows
- **Atomic Operations**: Crash-safe file writes prevent data corruption
- **Idempotent Commands**: Safe to run repeatedly without side effects

## Technology Stack Summary

| Component | Technology | Version |
|-----------|-----------|---------|
| **Language** | Go | 1.25.5 |
| **CLI Framework** | Cobra | v1.10.2 |
| **Testing** | Testify | v1.11.1 |
| **Build System** | Make | - |
| **Storage** | JSON Files | - |
| **External Dependency** | tmux | 2.0+ |

## Architecture Classification

**Repository Type**: Monolith
**Architecture Pattern**: Command-Based (Cobra) with Layered Internal Packages
**Design Approach**: Interface-Driven with Dependency Injection

```
User → CLI Commands → Business Logic → External Systems
       (Cobra)        (SessionManager)   (tmux + files)
```

## Repository Structure

```
tmux-cli/
├── cmd/tmux-cli/          # CLI entry point and commands
├── internal/
│   ├── session/           # Session orchestration
│   ├── store/             # JSON persistence layer
│   ├── tmux/              # Tmux execution wrapper
│   ├── recovery/          # Recovery detection and execution
│   └── testutil/          # Testing mocks
├── scripts/               # Build and verification scripts
├── Makefile              # Build automation
├── go.mod                # Go module definition
├── project-context.md    # Development rules (STRICT)
└── TESTING_ANALYSIS.md   # Testing strategy
```

## Core Components

### 1. SessionManager (`internal/session`)
Orchestrates session operations, coordinating between tmux and storage layers. Implements rollback on errors.

### 2. TmuxExecutor (`internal/tmux`)
Interface abstraction for tmux binary interactions. Enables mocking for fast unit tests.

### 3. SessionStore (`internal/store`)
File-based JSON persistence with atomic write operations. Prevents corruption during crashes.

### 4. RecoveryManager (`internal/recovery`)
Detects killed sessions and recreates them with original UUIDs and window configurations.

## Quick Start

### Prerequisites
- Go 1.25.5+
- tmux 2.0+
- Make

### Build and Install

```bash
# Clone repository
git clone <repository-url>
cd tmux-cli

# Build
make build

# Install to ~/.local/bin
make install

# Run
tmux-cli --help
```

### Basic Usage

```bash
# Create a session
tmux-cli session create myproject /path/to/project

# List sessions
tmux-cli session list

# Kill a session (preserves for recovery)
tmux-cli session kill <session-id>

# End a session permanently
tmux-cli session end <session-id>

# Create a window
tmux-cli session window create <session-id> editor nvim

# Recovery (automatic on list/status commands)
tmux-cli session list  # Automatically recovers killed sessions
```

## Development Approach

**TDD (Test-Driven Development)**:
- Write test first (RED)
- Implement minimum code (GREEN)
- Refactor while keeping tests green

**Test Coverage**:
- 37 total Go files
- 19 test files (~51% test coverage by file count)
- Unit tests use mocks for speed
- Integration tests use real tmux
- E2E verification with `make verify-real`

**Code Quality**:
- `make fmt` - Automatic formatting
- `make vet` - Static analysis
- `make test-all` - Full test suite
- Exit code validation (must be 0 for passing tests)

## Storage & Data

**Configuration Directory**: `~/.config/tmux-cli/`
**Session Storage**: `~/.config/tmux-cli/sessions/active/`
**Archived Sessions**: `~/.config/tmux-cli/sessions/ended/`

**File Format**: JSON
```json
{
  "SessionID": "uuid",
  "ProjectPath": "/path/to/project",
  "Windows": [
    {
      "TmuxWindowID": "@0",
      "Name": "editor",
      "RecoveryCommand": "nvim"
    }
  ]
}
```

## Testing Philosophy

From `project-context.md`:

**STRICT Rules**:
1. Capture **full** test output (never truncate)
2. Validate exit codes explicitly (`echo $?` must be 0)
3. No blind retries (understand failures first)
4. Real command execution verification (run built binary)

**Test Types**:
- **Unit Tests**: Use mocks, run by default
- **Integration Tests**: Use real tmux, run with `make test-tmux`
- **E2E Verification**: Full workflow testing with `make verify-real`

## Documentation

### Core Documentation
- **project-context.md** - Development rules and testing protocol (AUTHORITATIVE)
- **TESTING_ANALYSIS.md** - Testing strategy and bug analysis

### Generated Documentation
- **architecture.md** - Detailed architecture and design decisions
- **source-tree-analysis.md** - Directory structure and organization
- **development-guide.md** - Setup, workflow, and troubleshooting
- **index.md** - Master navigation document

## Common Commands

```bash
# Development
make build              # Build binary
make test               # Run unit tests
make test-all           # Run all tests
make verify-real        # E2E verification
make install            # Install to ~/.local/bin

# Quality
make fmt                # Format code
make vet                # Static analysis
make coverage           # Generate coverage report

# Utilities
make clean              # Remove build artifacts
make deps               # Download dependencies
make help               # Show all targets
```

## Project Statistics

- **Language**: Go 1.25.5
- **Total Files**: 37 Go files
- **Production Code**: ~1,148 lines (internal/ packages)
- **Test Files**: 19 files
- **Packages**: 6 (1 cmd + 5 internal)
- **External Dependencies**: 6 (Cobra, Testify, UUID, pflag, mousetrap, yaml)

## Design Highlights

### Interface-Driven
All external dependencies behind interfaces for testability:
- `TmuxExecutor` interface → Real or Mock implementation
- `SessionStore` interface → File or future alternatives

### Atomic Operations
Write-then-rename pattern prevents file corruption:
- Write to `.tmp` file
- Atomic rename on success
- Never have partial JSON

### Rollback on Errors
SessionManager cleans up partial state:
- If storage fails, kill created tmux session
- Maintains consistency between tmux and filesystem

### Idempotent Commands
Safe to run multiple times:
- Kill session doesn't error if already dead
- Enables robust automation

## Links to Detailed Documentation

- [Architecture](./architecture.md) - System design and patterns
- [Development Guide](./development-guide.md) - Setup and workflow
- [Source Tree](./source-tree-analysis.md) - Directory structure
- [Master Index](./index.md) - Navigation hub

## Questions or Contributions

See `project-context.md` for:
- Testing rules (STRICT protocols)
- Development workflow
- Common pitfalls
- Git workflow

For questions or clarifications, consult the human developer (Vojta) rather than making assumptions.
