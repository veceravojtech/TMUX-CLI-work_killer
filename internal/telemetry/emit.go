package telemetry

import (
	"path/filepath"
	"sync"
)

// SpoolDir returns the canonical spool directory for a project root.
func SpoolDir(projectDir string) string {
	return filepath.Join(projectDir, ".tmux-cli", "logs", "spool")
}

// NewProjectWriter builds a Writer for a project root, resolving identity, gating
// and spool dir from the ambient environment. This is the one-liner Go emitters
// and the CLI subcommand use to construct a writer.
func NewProjectWriter(projectDir string) *Writer {
	return NewWriter(Options{
		Dir:      SpoolDir(projectDir),
		Identity: ResolveIdentity(projectDir),
		Enabled:  Enabled(projectDir),
	})
}

var (
	defaultMu sync.Mutex
	defaultW  *Writer
)

// SetDefault installs the process-wide default writer used by the package-level
// Emit. Real binary entrypoints (the MCP server, the taskvisor daemon) call this
// ONCE at startup with a project-scoped writer, opting the process into
// telemetry. Until it is called, the package-level Emit is a silent no-op — so
// unit tests that exercise instrumented code paths never write spool files.
// Safe for concurrent use.
func SetDefault(w *Writer) {
	defaultMu.Lock()
	defaultW = w
	defaultMu.Unlock()
}

// InstallDefault is the one-line startup helper: build a project-scoped writer
// and install it as the process default. No-op-safe to call more than once.
func InstallDefault(projectDir string) {
	SetDefault(NewProjectWriter(projectDir))
}

// getDefault returns the installed default writer (nil until SetDefault). A nil
// writer's Emit is a no-op, so callers need no guard.
func getDefault() *Writer {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	return defaultW
}

// Emit is the fire-and-forget, process-wide emitter for in-process Go callers
// (taskvisor, mcp, session). It NEVER returns an error and NEVER panics the
// caller — telemetry must never fail the thing it observes. Until a real
// entrypoint calls SetDefault/InstallDefault, Emit is a silent no-op.
func Emit(event, window string, payload map[string]any) {
	defer func() { _ = recover() }()
	_ = getDefault().Emit(event, window, payload)
}

// EmitTo is the fire-and-forget variant for callers that hold an explicit writer
// (e.g. a session-scoped writer built with the just-created session id). Same
// never-fail contract as Emit.
func EmitTo(w *Writer, event, window string, payload map[string]any) {
	defer func() { _ = recover() }()
	_ = w.Emit(event, window, payload)
}
