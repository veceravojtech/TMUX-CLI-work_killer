# Development Guide

## Prerequisites

### Required
- **Go**: 1.25.5 or later
- **Make**: Standard Unix make utility
- **Tmux**: 2.0+ (for integration tests and E2E verification)

### Optional
- **entr**: For watch mode (`make watch-test`)
- **golangci-lint**: For advanced linting (not currently used)

## Installation & Setup

### 1. Clone and Install Dependencies

```bash
# Clone the repository
git clone <repository-url>
cd tmux-cli

# Download dependencies
make deps

# Verify setup
go version  # Should show 1.25.5+
tmux -V     # Should show 2.0+
```

### 2. Build the Project

```bash
# Build binary to ./bin/tmux-cli
make build

# Or build and install to ~/.local/bin
make install
```

### 3. Environment Setup

No environment variables required. Configuration is file-based:
- **Session Storage**: `~/.config/tmux-cli/sessions/active/`
- **Ended Sessions**: `~/.config/tmux-cli/sessions/ended/`

Directories are created automatically on first run.

## Development Workflow

### TDD Protocol (STRICT)

This project follows **Test-Driven Development** rigorously. See `project-context.md` for complete testing rules.

#### Red-Green-Refactor Cycle

```bash
# 1. RED: Write failing test
go test ./internal/store/... -v -run TestNewFeature
# Exit code should be 1 (test fails)

# 2. GREEN: Implement minimal code
# Edit source file...
go test ./internal/store/... -v -run TestNewFeature
# Exit code should be 0 (test passes)

# 3. REFACTOR: Improve while tests pass
# Refactor code...
go test ./internal/store/... -v
# Exit code must remain 0
```

**Critical Rule**: Never mark a task complete if `echo $?` returns non-zero after tests.

### Running Tests

#### Quick Unit Tests (Default)
```bash
# Fast unit tests with mocks (no tmux required)
make test

# Or directly:
go test -v -short -race ./...
```

#### Integration Tests (Require Tmux)
```bash
# Run tests that use real tmux
make test-tmux

# Or with build tags:
go test -v -race -tags=tmux ./...
```

#### All Tests
```bash
# Run everything: unit + integration
make test-all

# Or:
go test -v -race -tags=tmux,integration ./...
```

#### Coverage Report
```bash
# Generate coverage.html
make coverage

# Open in browser
xdg-open coverage.html  # Linux
open coverage.html      # macOS
```

### Watch Mode

```bash
# Auto-run tests on file changes (requires entr)
make watch-test
```

### End-to-End Verification

**ALWAYS** run this before considering implementation complete:

```bash
# Build + verify with real tmux commands
make verify-real

# This runs scripts/verify-real-execution.sh
# Validates that tmux-cli state matches tmux reality
```

## Build Commands

```bash
make build        # Build binary to ./bin/tmux-cli
make install      # Build + install to ~/.local/bin
make clean        # Remove binaries and test artifacts
make fmt          # Format all Go code
make vet          # Run go vet
make lint         # Run fmt + vet
make deps         # Download and tidy dependencies
make run          # Build and run
make help         # Show all available targets
```

## Code Style & Quality

### Formatting
```bash
# Format code (required before commit)
make fmt

# Or:
go fmt ./...
```

### Linting
```bash
# Run static analysis
make vet

# Or:
go vet ./...
```

### Pre-Commit Checklist
- [ ] `make fmt` - Code formatted
- [ ] `make vet` - No vet warnings
- [ ] `make test-all` - All tests pass (exit code 0)
- [ ] `make verify-real` - E2E verification passes
- [ ] Commit messages are descriptive

## Testing Guidelines

### Test File Organization
- `*_test.go` - Co-located with source files
- `*_integration_test.go` - Integration tests (require real tmux)
- Build tags: `// +build tmux` for tmux-dependent tests

### Mock vs Real Tmux
- **Unit tests**: Use `testutil.MockTmuxExecutor`
- **Integration tests**: Use `tmux.NewRealExecutor()`
- **Rule**: Unit tests must NOT call real tmux

### Validation Protocol (STRICT)

From `project-context.md`:

1. **Capture full output** - Never truncate test results
```bash
# Good
go test ./internal/store/... -v 2>&1 | tee test.log

# Bad - hides failures
go test ./internal/store/... -v 2>&1 | head -50
```

2. **Check exit codes** - Always verify
```bash
go test ./...
echo $?  # Must be 0 for all tests passing
```

3. **No blind retries** - Understand failures before retrying
```bash
# If test fails, add diagnostics:
go test ./internal/store/... -v -run TestSpecific
go test ./internal/store/... -v -failfast
```

## Package-Specific Guidelines

### internal/store
- All file operations must be atomic (use `atomic_write.go`)
- JSON validation tests ensure backward compatibility
- Test with mock filesystem when possible

### internal/tmux
- All tmux interactions go through `TmuxExecutor` interface
- Production: `RealExecutor` using `os/exec`
- Testing: `testutil.MockTmuxExecutor`
- Integration tests use real tmux (tag: `tmux`)

### internal/session
- Coordinates tmux + store operations
- Implements rollback on errors (e.g., kill tmux if store fails)
- Validates UUIDs and paths

### internal/recovery
- Detects killed sessions automatically
- Recreates with original UUID
- Verifies recovery succeeded

### internal/testutil
- Provides mocks for all external dependencies
- Keep mocks simple and focused
- Document mock behavior in godoc

## Common Development Tasks

### Adding a New Command
```bash
# 1. Write test in cmd/tmux-cli/*_test.go
# 2. Implement command in cmd/tmux-cli/*.go
# 3. Add to root command tree in root.go
# 4. Run: make test
# 5. Run: make verify-real
# 6. Verify: ./bin/tmux-cli <newcommand> --help
```

### Adding a New Feature
```bash
# 1. Write failing test (RED)
# 2. Implement minimum code (GREEN)
# 3. Refactor (keep tests green)
# 4. Run full test suite: make test-all
# 5. E2E verification: make verify-real
# 6. Commit with descriptive message
```

### Debugging Test Failures
```bash
# 1. Capture full output
go test ./path/to/package -v 2>&1 | tee debug.log

# 2. Run specific test
go test ./path/to/package -v -run TestSpecificName

# 3. Stop at first failure
go test ./path/to/package -v -failfast

# 4. Check package structure
go list -json ./path/to/package
```

## Project Organization Rules

### Directory Structure (STRICT)
- `cmd/` - CLI entry points only
- `internal/` - Internal packages (not importable externally)
- `pkg/` - Public packages (if needed for external use)
- `scripts/` - Build and utility scripts

### Import Rules
- Use full import paths: `github.com/console/tmux-cli/internal/store`
- Group imports: stdlib, external, internal
- No circular dependencies

### Error Handling
- Wrap errors with context: `fmt.Errorf("operation failed: %w", err)`
- Define package-specific errors in `errors.go`
- Return errors, don't panic (except for truly unrecoverable cases)

## Troubleshooting

### Tests Pass But Exit Code ≠ 0
```bash
# This means at least one test failed despite seeing PASS messages
# Solution: Read FULL output to find the FAIL
go test ./... -v 2>&1 | grep -A 5 "FAIL:"
```

### Integration Tests Fail
```bash
# Check tmux is installed and version
tmux -V  # Should be 2.0+

# Run with verbose output
go test -v -tags=tmux ./internal/tmux/...
```

### Build Errors
```bash
# Clean and rebuild
make clean
make deps
make build
```

### Import Errors
```bash
# Tidy dependencies
go mod tidy

# Verify module
go list -m all
```

## Git Workflow

- **Default Branch**: `main`
- **Commit Messages**: Clear and descriptive
- **Pre-Push**: Run `make test-all` (exit code must be 0)

### Commit Message Format
```
<type>: <short description>

<optional longer description>

Implements: <FR/NFR reference if applicable>
```

Example:
```
feat: add window kill command

Implements kill-window subcommand with idempotent behavior.
Includes integration tests with real tmux.

Implements: FR18, FR19
```

## Questions or Issues?

If you encounter ambiguity or need clarification on these rules, ask the human developer (Vojta) rather than making assumptions. These rules exist to save time and prevent frustrating debugging cycles.
