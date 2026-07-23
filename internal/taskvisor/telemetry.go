package taskvisor

import "github.com/console/tmux-cli/internal/telemetry"

// contractPhase maps the daemon's internal phase enum onto the frozen P2 events
// contract's closed phase set {elaborate|implement|investigate|validate}. An
// unmapped phase (phaseNone/idle) returns "" and suppresses the emit — the
// contract has no idle phase, so no event is better than an off-contract value.
func contractPhase(p phase) string {
	switch p {
	case phaseElaborating:
		return "elaborate"
	case phaseSupervising:
		return "implement"
	case phaseValidating:
		return "validate"
	default:
		return ""
	}
}

// emitGoalPhase fires a goal.phase event for a phase edge (start|end). It is
// fire-and-forget and a no-op unless the daemon process opted into telemetry via
// telemetry.InstallDefault (never under unit test). Off-contract phases are
// dropped. durationMs is emitted only when > 0 (an end edge with a known span).
func emitGoalPhase(goalID string, p phase, edge string, durationMs int64) {
	cp := contractPhase(p)
	if cp == "" {
		return
	}
	payload := map[string]any{
		"goal_id": goalID,
		"phase":   cp,
		"edge":    edge,
	}
	if durationMs > 0 {
		payload["duration_ms"] = durationMs
	}
	telemetry.Emit(telemetry.EventGoalPhase, "", payload)
}
