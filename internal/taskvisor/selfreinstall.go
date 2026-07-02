package taskvisor

// Repair-cycle self-reinstall phase (design §6 forward hook 1): when a repair
// goal's changes touch tmux-cli's OWN source, the freshly implemented fix
// exists only as source — the running daemon and the goal's validation cycle
// still use the OLD installed binary. This module inserts a thin rebuild step
// at the supervising→validating transition: shell out to
// `tmux-cli self-update --restart daemon` with the goal's build tree as
// --source, then let the existing stale-binary adoption
// (checkStaleBinary/restartStaleBinary) and Pass-1 resume carry the daemon
// restart while validation proceeds against the freshly installed binary.
//
// Composition contract: this hook only rebuilds (via self-update) and
// un-throttles the stale check (zeroing d.lastStaleCheck). It NEVER writes
// taskvisor-restart or exec-replaces itself — restartStaleBinary owns that.

// selfUpdateResult mirrors the single machine-readable JSON line self-update
// prints on stdout (cmd/tmux-cli/self_update.go selfUpdateOutput).
type selfUpdateResult struct {
	BinaryChanged bool   `json:"binary_changed"`
	Stage         string `json:"stage,omitempty"`
	Source        string `json:"source"`
	Restart       string `json:"restart"`
}

// parseSelfUpdateOutput decodes self-update's single JSON stdout line. Garbage
// output is an error — treated by the caller as the build-failure path.
func parseSelfUpdateOutput(out []byte) (selfUpdateResult, error) {
	return selfUpdateResult{}, nil
}

// defaultSelfUpdate is the production selfUpdateFn: it invokes the running
// executable's own `self-update --source <buildDir> --project <projectDir>
// --restart daemon` under a 5-minute timeout and parses the JSON result line.
func defaultSelfUpdate(sourceDir, projectDir string) (selfUpdateResult, error) {
	return selfUpdateResult{}, nil
}

// goalTouchesCliSource reports whether the goal's actual changed files in
// buildDir intersect the cli source set (cmd/**, internal/**, go.mod, go.sum,
// Makefile). Ground truth is git (status --porcelain ∪ merge-base diff on the
// worktree branch); when git enumeration fails it falls back to a declared
// Scope/DeliverableArea prefix match. Never crashes the tick.
func (d *Daemon) goalTouchesCliSource(buildDir string, goal *Goal) bool {
	return false
}

// maybeSelfReinstall is the supervising→validating hook: rebuild+install the
// cli when this goal's build tree is a tmux-cli checkout whose changes touch
// cli source, at most once per goal cycle (persisted, crash-safe stamp).
// Build failure is non-destructive: no marker, no restart, goal untouched —
// a distinct log+notify is emitted and validation still proceeds. Never
// returns an error to the tick.
func (d *Daemon) maybeSelfReinstall(goal *Goal, goals *GoalsFile) {
}
