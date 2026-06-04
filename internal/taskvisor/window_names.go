package taskvisor

import (
	"strings"

	"github.com/console/tmux-cli/internal/setup"
)

// window_names.go is the single source of truth mapping a goal ID -> its tmux
// window names / worker prefixes, gated on MaxGoals. The four naming helpers are
// pure (no tmux, no I/O) so they are exhaustively table-testable; the lone
// impurity is the (d *Daemon) maxGoals() accessor, which reads setting.yaml.
//
// Back-compat contract (execute-25): when MaxGoals<=1 every helper returns the
// bare singleton name used before namespacing existed, so a single-goal daemon's
// window names are byte-identical to the prior build (supervisor / validator /
// execute- / inv-). Namespacing kicks in ONLY when MaxGoals>1, where each goal
// owns distinct supervisor-<ns> / validator-<ns> windows and execute-<ns>- /
// inv-<ns>- worker pools so two in-flight goals never collide.

// goalNamespace maps a goal ID to the short, stable token embedded in per-goal
// window names. It strips a leading "goal-" prefix and returns the remainder
// verbatim (goal IDs are unique by construction, so the suffix is unique too).
// Leading zeros are PRESERVED — goal-020 -> "020" — matching the spec I/O matrix
// (supervisor-020) and keeping the token a faithful, reversible slice of the ID
// so execute-28 can derive the same prefix from the window name. When stripping
// leaves an empty string (an id of exactly "goal-"), it falls back to the raw id.
func goalNamespace(goalID string) string {
	ns := strings.TrimPrefix(goalID, "goal-")
	if ns == "" {
		return goalID
	}
	return ns
}

// supervisorWindow returns the supervisor window name for goalID: bare
// "supervisor" when maxGoals<=1, else "supervisor-<ns>".
func supervisorWindow(goalID string, maxGoals int) string {
	if maxGoals <= 1 {
		return "supervisor"
	}
	return "supervisor-" + goalNamespace(goalID)
}

// validatorWindow returns the validator window name for goalID: bare "validator"
// when maxGoals<=1, else "validator-<ns>".
func validatorWindow(goalID string, maxGoals int) string {
	if maxGoals <= 1 {
		return "validator"
	}
	return "validator-" + goalNamespace(goalID)
}

// ValidatorWindowNames returns every validator window name that may legitimately
// belong to goalID, most-specific first: the per-goal "validator-<ns>" form the
// daemon emits at MaxGoals>1, then the bare "validator" used at MaxGoals<=1. The
// MCP goal-validation-done lookup matches a live window against this set so it
// resolves the validator in both modes — and survives a max_goals config change
// while a validator window is still live — WITHOUT re-reading max_goals; UUID
// authorization remains the real gate. Both names come from validatorWindow, so
// they can never drift from the names the daemon actually spawns.
func ValidatorWindowNames(goalID string) []string {
	return []string{
		validatorWindow(goalID, 2), // "validator-<ns>" (MaxGoals>1)
		validatorWindow(goalID, 1), // "validator"      (MaxGoals<=1)
	}
}

// executePrefix returns the implementer-worker window prefix for goalID: bare
// "execute-" when maxGoals<=1, else "execute-<ns>-". nextExecuteN and the
// MaxWorkers cap (internal/mcp/tools.go) are already prefix-parametric, so a
// namespaced prefix makes allocation and the cap per-goal automatically.
func executePrefix(goalID string, maxGoals int) string {
	if maxGoals <= 1 {
		return "execute-"
	}
	return "execute-" + goalNamespace(goalID) + "-"
}

// ExecutePrefixForGoal returns the namespaced (MaxGoals>1) implementer-worker
// window prefix for goalID — "execute-<ns>-". The MCP windows-recover-workers
// tool uses it to scope batch recovery to ONE goal's worker pool when the
// caller window is goal-namespaced, so a supervisor recovering its stuck
// workers never injects messages into other goals' healthy workers. Mirrors
// the ValidatorWindowNames export pattern: derived from the same unexported
// helper the daemon spawns with, so it can never drift.
func ExecutePrefixForGoal(goalID string) string {
	return executePrefix(goalID, 2)
}

// invPrefix returns the investigator-worker window prefix for goalID: bare
// "inv-" when maxGoals<=1, else "inv-<ns>-".
func invPrefix(goalID string, maxGoals int) string {
	if maxGoals <= 1 {
		return "inv-"
	}
	return "inv-" + goalNamespace(goalID) + "-"
}

// maxGoals reads Supervisor.MaxGoals from setting.yaml, defaulting to 1 when the
// setting is unset, <=0, or unreadable. It is the only impurity behind the
// naming helpers; the daemon resolves it once per lifecycle operation and
// threads the int into the pure helpers above.
func (d *Daemon) maxGoals() int {
	s, err := setup.LoadSettings(d.workDir)
	if err != nil || s == nil || s.Supervisor.MaxGoals <= 0 {
		return 1
	}
	return s.Supervisor.MaxGoals
}
