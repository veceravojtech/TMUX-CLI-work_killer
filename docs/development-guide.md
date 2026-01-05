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

### Debugging PostCommand Failures

When worker windows fail to start Claude properly, use the PostCommand logs to diagnose the issue.

#### What is PostCommand?

PostCommand is the fallback system that launches Claude in new tmux windows. It tries three commands in sequence:

1. `claude --session-id="$TMUX_WINDOW_UUID"` (preferred - uses window UUID)
2. `claude --resume "$TMUX_WINDOW_UUID"` (fallback - resumes conversation)
3. `claude --dangerously-skip-permissions` (final fallback - fresh start)

Each command has an error pattern that triggers the next fallback:
- Command 1 fails if session ID is "already in use"
- Command 2 fails if "No conversation found"
- Command 3 has no error pattern (always succeeds)

#### Log File Location

All PostCommand execution is logged to:
```
.tmux-cli/logs/postcommand.log
```

#### Reading PostCommand Logs

Each log entry contains:
- **Timestamp**: When the command was attempted
- **Window ID**: Tmux window identifier (e.g., `@3`)
- **Session ID**: tmux-cli session UUID
- **Command Index**: Which fallback command (e.g., `Cmd=2/3` = second of three commands)
- **Decision**: What happened (`Attempting`, `Output captured`, `SUCCESS`, `Failed`, etc.)
- **Command**: The actual command executed
- **Output**: First 200 characters of pane output
- **Error**: Error message if command failed

#### Example Log Output

```
[2026-01-05 16:30:12] Window=@3 SessionID=abc123 Cmd=0/3: Starting PostCommand fallback chain
[2026-01-05 16:30:12] Window=@3 SessionID=abc123 Cmd=1/3: Attempting | Command: claude --session-id="$TMUX_WINDOW_UUID"
[2026-01-05 16:30:14] Window=@3 SessionID=abc123 Cmd=1/3: Output captured | Command: claude --session-id="$TMUX_WINDOW_UUID" | Output: Error: Session ID abc123 is already in use...
[2026-01-05 16:30:14] Window=@3 SessionID=abc123 Cmd=1/3: Checking pattern "already in use"
[2026-01-05 16:30:14] Window=@3 SessionID=abc123 Cmd=1/3: Pattern "already in use" → MATCH
[2026-01-05 16:30:14] Window=@3 SessionID=abc123 Cmd=1/3: Failed → trying next fallback
[2026-01-05 16:30:14] Window=@3 SessionID=abc123 Cmd=2/3: Attempting | Command: claude --resume "abc123"
[2026-01-05 16:30:16] Window=@3 SessionID=abc123 Cmd=2/3: Output captured | Command: claude --resume "abc123" | Output: Error: No conversation found...
[2026-01-05 16:30:16] Window=@3 SessionID=abc123 Cmd=2/3: Checking pattern "No conversation found"
[2026-01-05 16:30:16] Window=@3 SessionID=abc123 Cmd=2/3: Pattern "No conversation found" → MATCH
[2026-01-05 16:30:16] Window=@3 SessionID=abc123 Cmd=2/3: Failed → trying next fallback
[2026-01-05 16:30:16] Window=@3 SessionID=abc123 Cmd=3/3: Attempting | Command: claude --dangerously-skip-permissions
[2026-01-05 16:30:18] Window=@3 SessionID=abc123 Cmd=3/3: Output captured | Command: claude --dangerously-skip-permissions | Output: <Claude Code startup messages>
[2026-01-05 16:30:18] Window=@3 SessionID=abc123 Cmd=3/3: No error pattern to check (final fallback)
[2026-01-05 16:30:18] Window=@3 SessionID=abc123 Cmd=3/3: SUCCESS
[2026-01-05 16:30:18] Window=@3 SessionID=abc123 Cmd=0/3: PostCommand chain completed successfully
```

#### Debugging Workflow

When a worker window is stuck at a shell prompt instead of running Claude:

```bash
# 1. Check the PostCommand log
tail -f .tmux-cli/logs/postcommand.log

# 2. Look for the window ID that's stuck (e.g., @3)
grep "@3" .tmux-cli/logs/postcommand.log

# 3. Find which command failed and why
grep "@3" .tmux-cli/logs/postcommand.log | grep -E "(Failed|Error|FAILURE)"

# 4. Check the captured output from failed commands
grep "@3" .tmux-cli/logs/postcommand.log | grep "Output captured"
```

#### Common Failure Patterns

**Pattern: All three commands show "MATCH" → final FAILURE**
```
Cmd=3/3: Pattern "some error" → MATCH
All fallbacks exhausted → FAILURE
```
- **Cause**: Third fallback command is failing but no error pattern is configured
- **Solution**: Check the actual output captured from command 3
- **Common Reason**: Claude CLI not installed or not in PATH

**Pattern: Command succeeds but window still at prompt**
```
Cmd=3/3: SUCCESS
PostCommand chain completed successfully
```
- **Cause**: Claude started but then exited immediately
- **Solution**: Check Claude CLI authentication, check for startup errors in window pane
- **Debug**: Manually run the command in the window to see error output

**Pattern: No log entries for window**
- **Cause**: PostCommand not configured or disabled
- **Solution**: Check `.tmux-session` file has `postCommand.enabled: true`

**Pattern: Logs show timeout/no output captured**
```
Cmd=1/3: Output captured | Output:
```
- **Cause**: Command took longer than 2 seconds to produce output
- **Solution**: Check if network is slow, Claude CLI is hanging during auth

#### Manual Testing

Test PostCommand execution manually:

```bash
# 1. Create a new tmux session
tmux new-session -d -s test-session

# 2. Get the window ID
tmux list-windows -t test-session -F "#{window_id}"  # e.g., @0

# 3. Send the command
tmux send-keys -t test-session:@0 "claude --dangerously-skip-permissions" Enter

# 4. Wait 2 seconds and capture output
sleep 2
tmux capture-pane -t test-session:@0 -p

# 5. Check for errors
tmux capture-pane -t test-session:@0 -p | grep -i error

# 6. Cleanup
tmux kill-session -t test-session
```

#### Implementation Details

- **Log Location**: `.tmux-cli/logs/postcommand.log` (created automatically)
- **Timing**: Each command waits 2 seconds to capture output (increased from 1s to give Claude more startup time)
- **Output Truncation**: Log entries truncate output to first 200 characters for readability
- **Non-Fatal Logging**: Log write errors are silently suppressed - they never block window creation
- **Concurrent Writes**: Multiple windows may write to the log concurrently (append-only, acceptable race condition)

**Source Files:**
- `internal/session/postcommand.go:17-156` - PostCommand execution and logging
- `internal/tmux/real_executor.go:382-402` - SendMessageWithFeedback (2-second delay)
- `internal/store/types.go:33-46` - Default PostCommand configuration

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

## Claude Code Hooks Integration

### Overview

tmux-cli integrates with Claude Code's official hooks system to enable:
- **Session Validation**: Verify window membership before logging
- **Activity Logging**: Stream tool usage to window-specific log files
- **Lifecycle Tracking**: Record session start/end/stop events

### Hook Scripts Location

All hook scripts are in `scripts/hooks/`:
- `tmux-validate-session.sh` - Validates window UUID exists in `.tmux-session`
- `tmux-logger.sh` - Logs PostToolUse events to `.tmux-cli/logs/{uuid}.jsonl`
- `tmux-session-notify.sh` - Logs session lifecycle to `.tmux-cli/logs/sessions.jsonl`

### Configuration

Hooks are configured in `.claude/settings.json`:

```json
{
  "hooks": {
    "PostToolUse": {
      "command": "bash",
      "args": ["scripts/hooks/tmux-logger.sh"],
      "timeout": 10000,
      "stdin": "json"
    },
    "SessionStart": {
      "command": "bash",
      "args": ["scripts/hooks/tmux-session-notify.sh", "start"],
      "timeout": 30000,
      "stdin": "json"
    }
  }
}
```

### Log Files

Logs are written to `.tmux-cli/logs/`:
- `{window-uuid}.jsonl` - Window-specific activity logs (JSON Lines)
- `sessions.jsonl` - Session lifecycle events

### Testing Hooks

```bash
# 1. Test validation script
cd /path/to/project
echo '{"windows":[{"uuid":"test-uuid"}]}' > .tmux-session
TMUX_WINDOW_UUID=test-uuid scripts/hooks/tmux-validate-session.sh
echo $?  # Should be 0 (valid)

# 2. Test logger
echo '{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"pwd"}}' | \
  TMUX_WINDOW_UUID=test-uuid scripts/hooks/tmux-logger.sh

# 3. Verify log entry
cat .tmux-cli/logs/test-uuid.jsonl | jq '.'
```

### Documentation

See detailed hook documentation:
- `scripts/hooks/README.md` - Hook script details and troubleshooting
- `.tmux-cli/logs/README.md` - Log format and parsing examples

### Dependencies

**Required for hooks:**
- Bash 4.0+
- `jq` (JSON parsing)
- Claude Code CLI (with hooks support)

**Install jq:**
```bash
# Ubuntu/Debian
sudo apt-get install jq

# macOS
brew install jq
```

## Questions or Issues?

If you encounter ambiguity or need clarification on these rules, ask the human developer (Vojta) rather than making assumptions. These rules exist to save time and prevent frustrating debugging cycles.
