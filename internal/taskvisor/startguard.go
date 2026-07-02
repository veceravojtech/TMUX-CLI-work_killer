package taskvisor

import "github.com/console/tmux-cli/internal/setup"

// StartRefusal classifies the shared taskvisor start-permission decision made
// by EvaluateStartGuard for both start entry points (the MCP taskvisor-start
// tool and the `tmux-cli taskvisor start` CLI command).
type StartRefusal int

const (
	// StartAllowed — write the taskvisor-start signal file.
	StartAllowed StartRefusal = iota
	// StartRefusedNoLedger — goals.yaml is missing and the planning mode does
	// not permit an empty ledger.
	StartRefusedNoLedger
	// StartRefusedNoStartable — goals.yaml exists but holds no startable work
	// and the planning mode does not permit an empty ledger.
	StartRefusedNoStartable
)

// EvaluateStartGuard is the single start-permission decision shared by the MCP
// taskvisor-start tool and the CLI `taskvisor start` command, so the two
// guards can never drift again. Callers keep their own startability semantics
// (ledgerMissing / hasStartable) and their own refusal error strings; only the
// DECISION lives here. In incremental planning mode an empty ledger (missing
// goals.yaml or zero startable goals) is a valid start state — the daemon's
// idle poll synthesizes an empty in-memory GoalsFile and the incremental loop
// authors goal-001 itself (daemon.go poll / plannext.go) — so both refusals
// are bypassed. Roadmap mode keeps them; settings are only consulted when a
// refusal would otherwise fire.
func EvaluateStartGuard(projectRoot string, ledgerMissing, hasStartable bool) StartRefusal {
	if !ledgerMissing && hasStartable {
		return StartAllowed
	}
	if startPlanningModeIncremental(projectRoot) {
		return StartAllowed
	}
	if ledgerMissing {
		return StartRefusedNoLedger
	}
	return StartRefusedNoStartable
}

// startPlanningModeIncremental reports whether the project runs the daemon's
// incremental planning loop (taskvisor.planning_mode == "incremental").
// LoadSettings already coerces empty/unknown values to roadmap, so no
// re-validation happens here; a settings load error conservatively reads as
// roadmap, preserving the empty-ledger refusals.
func startPlanningModeIncremental(projectRoot string) bool {
	settings, err := setup.LoadSettings(projectRoot)
	return err == nil && settings != nil &&
		settings.Taskvisor.PlanningMode == setup.PlanningModeIncremental
}
