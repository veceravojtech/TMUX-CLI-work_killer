package taskvisor

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// concurrency.go is the single source of truth for the runtime concurrency
// override — a plain-text integer in `.tmux-cli/taskvisor-concurrency` that lets
// an operator change the daemon's in-flight goal cap WITHOUT a restart (which
// would orphan in-flight goals). It is read by the daemon's (d *Daemon)
// maxGoals() every tick and written by the `tmux-cli taskvisor concurrency`
// subcommand. The helpers are package-level (not methods) and import NOTHING
// from daemon/tmux, so cmd/tmux-cli can call them without an import cycle.
//
// Precedence: a valid override (integer ≥ 1) wins over setting.yaml's
// supervisor.max_goals; an absent / unparsable / < 1 override falls back to the
// configured value (defaulting to 1). The read side is deliberately tolerant —
// a transient bad/partial read returns (0,false) so the cap falls back to the
// configured value rather than collapsing to 1; the write side is atomic
// (temp+rename) so the daemon never observes a half-written file.

// ConcurrencyOverridePath returns the path to the runtime concurrency override
// file for the given project root. It mirrors the existing bare marker-file
// convention under .tmux-cli/.
func ConcurrencyOverridePath(workDir string) string {
	return filepath.Join(workDir, ".tmux-cli", "taskvisor-concurrency")
}

// ReadConcurrencyOverride reads the runtime concurrency override. It returns
// (n, true) ONLY when the file exists, parses as an integer, AND that integer
// is ≥ 1. Any other condition — missing file, read error, non-integer content,
// or a value < 1 — returns (0, false) so the caller falls back to the
// configured cap (never a hard floor on a transient bad read).
func ReadConcurrencyOverride(workDir string) (int, bool) {
	b, err := os.ReadFile(ConcurrencyOverridePath(workDir))
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// WriteConcurrencyOverride atomically writes the runtime concurrency override,
// flooring n at 1. It creates .tmux-cli/ if needed, writes to a temp file in the
// same directory, then os.Rename-s it into place so the daemon never reads a
// partial value.
func WriteConcurrencyOverride(workDir string, n int) error {
	if n < 1 {
		n = 1
	}
	path := ConcurrencyOverridePath(workDir)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "taskvisor-concurrency-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if anything below fails before the rename succeeds.
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(strconv.Itoa(n) + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
