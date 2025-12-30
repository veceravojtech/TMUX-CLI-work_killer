# Project Context: tmux-cli

> **CRITICAL:** This document is the authoritative source of truth for all AI dev agents working on this project. All rules marked as STRICT must be followed without exception.

## Project Overview

**Name:** tmux-cli
**Language:** Go 1.25.5
**Framework:** Cobra CLI
**Testing:** testify (github.com/stretchr/testify)
**Architecture:** Internal package structure with clear separation of concerns

### Package Structure
```
cmd/tmux-cli/          # CLI entry point and command definitions
internal/
  ├── store/           # Session storage and persistence
  ├── tmux/            # Tmux execution and session management
  └── testutil/        # Testing utilities and mocks
```

---

## STRICT Testing Rules

> **WHY THESE RULES EXIST:** To prevent silent failures, infinite retry loops, and incomplete test diagnostics that waste development time.

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

### Rule 1. A : Alway us LSP
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
