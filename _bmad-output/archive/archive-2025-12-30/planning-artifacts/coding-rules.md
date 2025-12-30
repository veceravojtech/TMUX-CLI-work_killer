# Coding Rules - tmux-cli

## Test-Driven Development (TDD) Only

This project follows **strict TDD practices**. All code must be developed using the Red-Green-Refactor cycle.

### Core TDD Principles

1. **Red**: Write a failing test first
2. **Green**: Write the minimum code to make the test pass
3. **Refactor**: Improve the code while keeping tests green

### Mandatory Rules

#### Before Writing Any Code

- ✅ **MUST** write the test first
- ✅ **MUST** see the test fail for the right reason
- ❌ **NEVER** write production code without a failing test

#### Test Requirements

- All functions must have corresponding tests
- Tests must be in `*_test.go` files
- Test coverage should be maintained above 80%
- Use table-driven tests where appropriate
- Mock external dependencies (tmux commands, file system, etc.)

#### Test Naming Convention

```go
func TestFunctionName_Scenario_ExpectedBehavior(t *testing.T)
```

Example:
```go
func TestCreateWindow_ValidName_ReturnsWindowID(t *testing.T)
func TestListSessions_NoActiveSessions_ReturnsEmptyList(t *testing.T)
```

### Code Organization

#### Package Structure

```
tmux-cli/
├── cmd/tmux-cli/          # Main application entry point
│   └── main.go
├── internal/              # Private application code
│   ├── tmux/             # Tmux control logic
│   ├── mcp/              # MCP server implementation
│   └── claude/           # Claude integration
├── pkg/                   # Public packages (if needed)
└── testdata/             # Test fixtures
```

#### Dependencies

- Use standard library where possible
- External dependencies must be justified
- All dependencies must be mockable for testing

### Go Best Practices

#### Code Style

- Follow official Go formatting (`gofmt`)
- Use `golint` and `go vet` for linting
- Maximum function complexity: 10 cyclomatic complexity
- Keep functions small and focused (ideally < 50 lines)

#### Error Handling

```go
// ✅ DO: Return errors explicitly
func CreateWindow(name string) (int, error) {
    if name == "" {
        return 0, fmt.Errorf("window name cannot be empty")
    }
    // ...
}

// ❌ DON'T: Panic or ignore errors
```

#### Naming Conventions

- Use descriptive names
- Interfaces: `-er` suffix (e.g., `Runner`, `Commander`)
- Packages: singular, lowercase, no underscores
- Constants: CamelCase (not SCREAMING_CASE)

### Testing Guidelines

#### Unit Tests

- Test one thing per test
- Use subtests for related scenarios
- Mock external calls (tmux commands, system calls)

```go
func TestWindowManager_CreateWindow(t *testing.T) {
    tests := []struct {
        name        string
        windowName  string
        wantErr     bool
        expectedID  int
    }{
        {"valid window", "test-window", false, 1},
        {"empty name", "", true, 0},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test implementation
        })
    }
}
```

#### Integration Tests

- Use build tags: `// +build integration`
- Keep separate from unit tests
- Test actual tmux interactions in controlled environment

#### Test Helpers

- Create test utilities in `internal/testutil/`
- Provide mock implementations for interfaces
- Use `testing.T.Helper()` for helper functions

### MCP Server Guidelines

- Follow MCP protocol specification
- Implement proper JSON-RPC 2.0 handlers
- All MCP tools must have:
  - Input validation with tests
  - Error handling with tests
  - Documentation in code

### Tmux Integration

- Never execute tmux commands without tests
- Mock tmux command execution in unit tests
- Validate tmux command syntax before execution
- Handle tmux session/window lifecycle properly

### Git Workflow

- Commit messages must reference tests
- Format: `feat: add window creation (with tests)`
- All commits must have passing tests
- Use conventional commits format

### Continuous Integration

Tests must:
- Run quickly (< 5 seconds for unit tests)
- Be deterministic (no flaky tests)
- Clean up after themselves
- Run in parallel where possible

### Code Review Checklist

Before submitting:
- [ ] All tests written first and passing
- [ ] Test coverage maintained/improved
- [ ] No production code without tests
- [ ] All edge cases tested
- [ ] Error paths tested
- [ ] Documentation updated
- [ ] `make test` passes
- [ ] `make build` succeeds

### Anti-Patterns to Avoid

❌ Writing code before tests
❌ Testing implementation details
❌ Mocking everything (test behavior, not implementation)
❌ Ignoring error returns
❌ Global state
❌ Hard-coded values (use constants or config)

### Performance Considerations

- Benchmark performance-critical code
- Profile before optimizing
- Document performance requirements
- Use `testing.B` for benchmarks

---

**Remember: If it's not tested, it doesn't exist.**
