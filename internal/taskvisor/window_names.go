package taskvisor

import (
	"strings"

	"github.com/console/tmux-cli/internal/setup"
)

// window_names.go is the single source of truth mapping a goal ID -> its tmux
// window names / worker prefixes. The four naming helpers are pure (no tmux, no
// I/O) so they are exhaustively table-testable; the lone impurity is the
// (d *Daemon) maxGoals() accessor, which reads setting.yaml.
//
// Naming contract (P1): goal windows are ALWAYS namespaced — every helper returns
// the per-goal form (supervisor-<ns> / validator-<ns> / execute-<ns>- / investigator-<ns>-)
// regardless of MaxGoals. The bare singleton names are retired for goal windows;
// the only surviving bare name is window-0 "supervisor", the human's interactive /
// standalone window, which the daemon never spawns, kills, or reuses for goal
// execution ([[never-kill-tmux-server-pid]]). Namespacing at MaxGoals=1 means the
// daemon spawns a fresh supervisor-<ns> per goal and leaves window-0 untouched,
// while at MaxGoals>1 the same per-goal names keep two in-flight goals from
// colliding. The maxGoals param is retained on the unexported helpers (it no longer
// branches) so ~15 call sites need no re-threading and ValidatorWindowNames's
// two-arg calls stay meaningful as fallback documentation.

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

// supervisorWindow returns the supervisor window name for goalID — always the
// namespaced "supervisor-<ns>". The maxGoals param is retained for call-site
// compatibility (it no longer branches). The bare "supervisor" name belongs to
// window-0 (the human's interactive window) and is never produced here.
func supervisorWindow(goalID string, maxGoals int) string {
	_ = maxGoals
	return "supervisor-" + goalNamespace(goalID)
}

// validatorWindow returns the validator window name for goalID — always the
// namespaced "validator-<ns>". The maxGoals param is retained for call-site
// compatibility (it no longer branches).
func validatorWindow(goalID string, maxGoals int) string {
	_ = maxGoals
	return "validator-" + goalNamespace(goalID)
}

// ValidatorWindowNames returns every validator window name that may legitimately
// belong to goalID, most-specific first: the per-goal "validator-<ns>" the daemon
// now always emits, then bare "validator" kept as a ONE-RELEASE fallback so a
// pre-upgrade live validator window (spawned by the prior bare-name build) still
// resolves. The MCP goal-validation-done lookup matches a live window against this
// set WITHOUT re-reading max_goals; UUID authorization remains the real gate. The
// namespaced name comes from validatorWindow, so it can never drift from the name
// the daemon spawns.
func ValidatorWindowNames(goalID string) []string {
	return []string{
		validatorWindow(goalID, 2), // "validator-<ns>" (current, always emitted)
		"validator",                // bare fallback for a pre-upgrade live window
	}
}

// executePrefix returns the implementer-worker window prefix for goalID — always
// the namespaced "execute-<ns>-". The maxGoals param is retained for call-site
// compatibility (it no longer branches). nextExecuteN and the MaxWorkers cap
// (internal/mcp/tools.go) are already prefix-parametric, so the namespaced prefix
// makes allocation and the cap per-goal automatically.
func executePrefix(goalID string, maxGoals int) string {
	_ = maxGoals
	return "execute-" + goalNamespace(goalID) + "-"
}

// ExecutePrefixForGoal returns the namespaced implementer-worker window prefix for
// goalID — "execute-<ns>-". The MCP windows-recover-workers tool uses it to scope
// batch recovery to ONE goal's worker pool when the caller window is
// goal-namespaced, so a supervisor recovering its stuck workers never injects
// messages into other goals' healthy workers. Mirrors the ValidatorWindowNames
// export pattern: derived from the same unexported helper the daemon spawns with,
// so it can never drift.
func ExecutePrefixForGoal(goalID string) string {
	return executePrefix(goalID, 2)
}

// investigatorPrefix returns the investigator-worker window prefix for goalID — always the
// namespaced "investigator-<ns>-". The maxGoals param is retained for call-site
// compatibility (it no longer branches).
func investigatorPrefix(goalID string, maxGoals int) string {
	_ = maxGoals
	return "investigator-" + goalNamespace(goalID) + "-"
}

// InvestigatorPrefixForGoal returns the namespaced investigator-worker window prefix for
// goalID — "investigator-<ns>-". Mirrors ExecutePrefixForGoal: the package-main goal-skip
// sweep uses it to kill a goal's investigator pool by prefix without ad-hoc string
// surgery, and it can never drift from the name the daemon spawns.
func InvestigatorPrefixForGoal(goalID string) string {
	return investigatorPrefix(goalID, 2)
}

// planAuditWindow returns the plan-audit window name for goalID — always the
// namespaced "plan-audit-<ns>". The maxGoals param is retained for call-site
// compatibility (it no longer branches).
func planAuditWindow(goalID string, maxGoals int) string {
	_ = maxGoals
	return "plan-audit-" + goalNamespace(goalID)
}

// SupervisorWindowForGoal returns the namespaced supervisor window name for goalID
// — "supervisor-<ns>". Exposed for the package-main goal-skip sweep so it targets
// the goal's supervisor window via the real helper (never bare window-0
// "supervisor"). Mirrors ExecutePrefixForGoal; derived from supervisorWindow so it
// can never drift.
func SupervisorWindowForGoal(goalID string) string {
	return supervisorWindow(goalID, 2)
}

// maxGoals resolves the in-flight goal cap. It consults the runtime concurrency
// override file FIRST — a valid override (integer ≥ 1) wins so an operator can
// raise/lower concurrency on a running daemon without a restart, and the change
// applies on the very next tick (tick() calls maxGoals() for both the free
// budget and every DisjointReadySet argument). When no valid override exists it
// falls back, byte-identically, to Supervisor.MaxGoals from setting.yaml,
// defaulting to 1 when the setting is unset, <=0, or unreadable. It is the only
// impurity behind the naming helpers; the daemon resolves it once per lifecycle
// operation and threads the int into the pure helpers above.
func (d *Daemon) maxGoals() int {
	if n, ok := ReadConcurrencyOverride(d.workDir); ok {
		return n
	}
	s, err := setup.LoadSettings(d.workDir)
	if err != nil || s == nil || s.Supervisor.MaxGoals <= 0 {
		return 1
	}
	return s.Supervisor.MaxGoals
}
