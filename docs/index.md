# tmux-cli Documentation Index

> **Primary Entry Point**: This index is the authoritative source for AI-assisted development on the tmux-cli project.

**Last Updated**: 2025-12-30
**Project Version**: 0.1.0
**Documentation Version**: 1.0

---

## Project Overview

- **Name**: tmux-cli
- **Type**: Monolith (single cohesive codebase)
- **Primary Language**: Go 1.25.5
- **Architecture**: Command-Based CLI (Cobra pattern)
- **Purpose**: CLI tool for managing tmux sessions with automatic recovery

---

## Quick Reference

### Technology Stack
- **Language**: Go 1.25.5
- **Framework**: Cobra CLI v1.10.2
- **Testing**: Testify v1.11.1
- **Build**: Make
- **Storage**: JSON files (atomic writes)
- **Entry Point**: cmd/tmux-cli/main.go

### Architecture Pattern
- **Command-Based Architecture** (Cobra)
- **Layered Internal Packages** (session, store, tmux, recovery, testutil)
- **Interface-Driven Design** (TmuxExecutor, SessionStore, RecoveryManager)
- **Dependency Injection** with constructor functions
- **TDD Approach** (~51% test file coverage)

### Key Statistics
- **Total Files**: 37 Go files
- **Test Files**: 19 (51% coverage)
- **Production Code**: ~1,148 lines
- **Packages**: 6 (1 cmd + 5 internal)

---

## Generated Documentation

### Core Documentation

#### [Project Overview](./project-overview.md)
High-level summary of the project, features, and quick start guide.
- Project purpose and features
- Technology stack summary
- Core components overview
- Basic usage examples
- Project statistics

#### [Architecture](./architecture.md)
Comprehensive architectural documentation and design decisions.
- System architecture diagrams
- Layer responsibilities
- Data models and storage
- Design patterns and principles
- Testing strategy
- Extension points
- Security and performance considerations

#### [Source Tree Analysis](./source-tree-analysis.md)
Detailed breakdown of directory structure and code organization.
- Complete directory tree with annotations
- Critical directories explained
- Integration points and data flows
- File naming conventions
- Code statistics

#### [Development Guide](./development-guide.md)
Complete developer workflow and setup instructions.
- Prerequisites and installation
- TDD protocol (STRICT rules)
- Running tests (unit, integration, E2E)
- Build commands
- Code quality standards
- Package-specific guidelines
- Troubleshooting common issues
- Git workflow

---

## Existing Documentation

### Authoritative Sources

#### [project-context.md](../project-context.md) ⚠️ **CRITICAL**
**THIS IS THE AUTHORITATIVE SOURCE** for all development on this project.
- STRICT testing rules (must be followed without exception)
- Full test output required (never truncate)
- Exit code validation (must check `echo $?`)
- No blind retries protocol
- Working directory awareness
- Real command execution verification
- TDD protocol (Red-Green-Refactor)
- Package-specific guidelines
- Common pitfalls to avoid

**Important**: All rules marked as STRICT must be followed. These rules exist to prevent silent failures, infinite retry loops, and incomplete test diagnostics.

#### [TESTING_ANALYSIS.md](../TESTING_ANALYSIS.md)
Testing strategy and bug analysis documentation.
- Current testing state (unit + integration)
- Bug discovery case study
- Architecture review
- Testing gaps identified
- Recommendations for improvement

---

## Navigation by Task

### For New Feature Development
1. Read [project-context.md](../project-context.md) - **STRICT rules**
2. Review [architecture.md](./architecture.md) - Understand system design
3. Check [development-guide.md](./development-guide.md) - TDD workflow
4. Examine [source-tree-analysis.md](./source-tree-analysis.md) - Find relevant packages

### For Bug Fixes
1. Read [project-context.md](../project-context.md) - Testing protocol
2. Review [TESTING_ANALYSIS.md](../TESTING_ANALYSIS.md) - Known issues
3. Check [source-tree-analysis.md](./source-tree-analysis.md) - Locate affected code
4. Follow TDD protocol in [development-guide.md](./development-guide.md)

### For Understanding Codebase
1. Start with [project-overview.md](./project-overview.md) - High-level understanding
2. Read [architecture.md](./architecture.md) - System design
3. Explore [source-tree-analysis.md](./source-tree-analysis.md) - Code organization
4. Review [development-guide.md](./development-guide.md) - Development workflow

### For Testing and Verification
1. **CRITICAL**: Read [project-context.md](../project-context.md) - STRICT testing rules
2. Run `make test` - Unit tests (fast, use mocks)
3. Run `make test-tmux` - Integration tests (real tmux)
4. Run `make verify-real` - E2E verification (before PR)
5. Check `echo $?` - **Must be 0** for passing tests

---

## Package Organization

### CLI Layer
- **cmd/tmux-cli/** - Command definitions and entry point
  - `main.go` - Application entry point
  - `root.go` - Root Cobra command
  - `session.go` - Session management commands
  - `recovery_helper.go` - Recovery command integration

### Business Logic Layer
- **internal/session/** - Session orchestration
  - `manager.go` - SessionManager (coordinates tmux + store)
  - `validation.go` - UUID and input validation

- **internal/recovery/** - Automatic recovery
  - `recovery.go` - RecoveryManager implementation
  - Detection, recreation, and verification

### External Interface Layer
- **internal/tmux/** - Tmux execution wrapper
  - `executor.go` - TmuxExecutor interface
  - `real_executor.go` - Production implementation
  - `command_wrapper.go` - Command wrapping

- **internal/store/** - Session persistence
  - `store.go` - SessionStore interface
  - `file_store.go` - JSON file implementation
  - `atomic_write.go` - Crash-safe writes
  - `types.go` - Data models

### Testing Layer
- **internal/testutil/** - Testing utilities
  - `mock_tmux.go` - Mock TmuxExecutor

---

## Critical Development Rules

> From [project-context.md](../project-context.md) - **MUST FOLLOW**

### Testing Rules (STRICT)

1. **Full Test Output Required**
   - ALWAYS capture complete output
   - NEVER truncate with `head`, `tail`, etc.
   - Use: `go test ./... -v 2>&1`

2. **Exit Code Validation**
   - ALWAYS check: `echo $?`
   - Exit code 0 = all tests passed
   - Exit code ≠ 0 = at least one test failed
   - NEVER mark complete if exit code ≠ 0

3. **No Blind Retries**
   - Maximum 1 retry with diagnostics
   - Understand failures before retrying
   - Add diagnostic flags on retry

4. **Test Failure Analysis**
   - Capture full output
   - Identify failing test(s)
   - Read failure message
   - Fix root cause
   - Re-run and verify exit code = 0

5. **Real Execution Verification**
   - ALWAYS run built binary to verify
   - Add as LAST task in TodoList
   - Build: `make build`
   - Execute: `./bin/tmux-cli <command>`
   - Verify expected behavior

### TDD Protocol

```
RED Phase:
  - Write failing test
  - Verify FAILS (exit code = 1)

GREEN Phase:
  - Write minimal code
  - Verify PASSES (exit code = 0)
  - CRITICAL: If exit code ≠ 0, NOT in GREEN

REFACTOR Phase:
  - Improve code
  - Keep tests passing (exit code = 0)
```

---

## Common Tasks

### Build and Test
```bash
make build        # Build to ./bin/tmux-cli
make test         # Unit tests (fast, mocks)
make test-tmux    # Integration tests (real tmux)
make test-all     # All tests
make verify-real  # E2E verification (REQUIRED before PR)
make coverage     # Generate coverage report
```

### Code Quality
```bash
make fmt          # Format code (required before commit)
make vet          # Static analysis
make lint         # fmt + vet
```

### Installation
```bash
make install      # Install to ~/.local/bin
make clean        # Remove build artifacts
make deps         # Download dependencies
```

---

## Getting Started

### For First-Time Development

1. **Read STRICT Rules**
   ```bash
   cat ../project-context.md
   # Pay special attention to Testing Rules section
   ```

2. **Setup Environment**
   ```bash
   make deps
   make build
   make test
   echo $?  # Must be 0
   ```

3. **Verify Installation**
   ```bash
   make verify-real  # E2E test
   ./bin/tmux-cli --help
   ```

4. **Understand Architecture**
   - Read: [architecture.md](./architecture.md)
   - Explore: [source-tree-analysis.md](./source-tree-analysis.md)

### For Implementing New Features

1. **Write Test First** (RED)
   ```bash
   # Add test to appropriate *_test.go
   go test ./internal/<package> -v -run TestNewFeature
   # Verify it FAILS (exit code = 1)
   ```

2. **Implement Minimum Code** (GREEN)
   ```bash
   # Edit source file
   go test ./internal/<package> -v -run TestNewFeature
   echo $?  # Must be 0
   ```

3. **Refactor** (keep tests green)
   ```bash
   go test ./internal/<package> -v
   echo $?  # Must remain 0
   ```

4. **Verify End-to-End**
   ```bash
   make build
   ./bin/tmux-cli <new-command> --help
   ./bin/tmux-cli <new-command>  # Actually execute
   ```

---

## Troubleshooting

### Tests Show PASS But Exit Code ≠ 0
**Problem**: Some tests failed despite seeing PASS messages
**Solution**: Read FULL output to find FAIL
```bash
go test ./... -v 2>&1 | grep -A 5 "FAIL:"
```

### Integration Tests Fail
**Problem**: tmux-related tests failing
**Solution**: Check tmux version and installation
```bash
tmux -V  # Should be 2.0+
go test -v -tags=tmux ./internal/tmux/...
```

### Build Errors
**Solution**: Clean and rebuild
```bash
make clean
make deps
make build
```

---

## Documentation Maintenance

### When to Update

- **New Features**: Update architecture.md and development-guide.md
- **New Commands**: Update project-overview.md
- **Structure Changes**: Update source-tree-analysis.md
- **Testing Changes**: Update TESTING_ANALYSIS.md
- **Rule Changes**: Update project-context.md (with approval)

### Documentation Standards

- Keep index.md current (primary entry point)
- Update "Last Updated" dates
- Cross-reference related documents
- Include code examples where helpful
- Maintain STRICT rule emphasis

---

## Contact and Support

**Developer**: Vojta
**Project Context**: [project-context.md](../project-context.md)
**Questions**: Ask human developer rather than making assumptions

---

## Document History

| Date | Version | Changes |
|------|---------|---------|
| 2025-12-30 | 1.0 | Initial comprehensive documentation generated |

---

**Remember**: The [project-context.md](../project-context.md) file contains STRICT rules that must be followed without exception. These rules exist to prevent frustrating debugging cycles and ensure development quality.
