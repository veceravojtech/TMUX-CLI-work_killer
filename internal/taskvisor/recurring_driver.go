package taskvisor

import "time"

// recurring_driver.go — the daemon-side cycle machine for the recurring
// supervisor task (dispatch → settle → advance → finish). This file currently
// holds ONLY the runtime enum, the per-cycle runtime struct, and NO-OP stubs:
// the real driver behavior is the GREEN half of a red→green pair and lands in a
// later goal. The call SEAMS into tick() (statemachine.go) and poll() (daemon.go)
// route through these stubs so the package compiles and the TestRecurring* suite
// fails on ASSERTIONS (not compile errors).
//
// cyclePhase is a daemon-RUNTIME enum, deliberately distinct from the PERSISTED
// RecurringCycle.Phase string (recurring.go): cyclePhaseName is the int→string
// bridge, mirroring the type phase int / phaseName(p) pair (daemon.go:27-33,
// diagnostics.go:34).

// cyclePhase is the in-flight phase of one recurring cycle, mirroring `type phase
// int`. The zero value is cyclePhaseDispatching: a freshly-seeded cycle starts by
// dispatching its prompt to the supervisor.
type cyclePhase int

const (
	cyclePhaseDispatching cyclePhase = iota
	cyclePhaseSettling
	cyclePhaseSettled
)

// cyclePhaseName maps a cyclePhase to its persisted RecurringCycle.Phase string,
// mirroring phaseName(p phase) string (diagnostics.go:34).
func cyclePhaseName(p cyclePhase) string {
	switch p {
	case cyclePhaseDispatching:
		return "dispatching"
	case cyclePhaseSettling:
		return "settling"
	case cyclePhaseSettled:
		return "settled"
	default:
		return "dispatching"
	}
}

// recurringRuntime holds the in-flight cycle runtime hoisted off Daemon, mirroring
// goalRuntime (daemon.go:43-95). The zero value (phase == cyclePhaseDispatching,
// zero timers, empty hash) is "never dispatched". Timers are read against d.now()
// so tests advance a fakeClock deterministically.
type recurringRuntime struct {
	phase            cyclePhase
	dispatchedAt     time.Time
	lastProgressHash string
	lastActivityAt   time.Time
}

// driveRecurring is the per-tick recurring cycle machine. STUB: returns
// immediately with no dispatch and no mutation — the dispatch/settle/advance/
// finish bodies are the green goal. Wired into tick() right after the modeActive
// guard (statemachine.go) so the seam exists today.
func (d *Daemon) driveRecurring(goals *GoalsFile) {
	// no-op stub (green goal implements the cycle machine)
}

// recurringSettled reports whether the in-flight cycle has drained and aged past
// its idle-grace / boot-min floors (or its wall-clock cap). STUB: returns false —
// the green goal evaluates the drain clauses against rt and task.
func (d *Daemon) recurringSettled(rt *recurringRuntime, task *RecurringTask) bool {
	return false
}

// recurringActive reports whether a recurring task is mid-run, gating the tick
// step-5 teardown predicate (statemachine.go: `&& !d.recurringActive()`). STUB:
// returns false, so the extended predicate is byte-identical to today's behavior
// for every non-recurring test.
func (d *Daemon) recurringActive() bool {
	return false
}

// recurringPickup is the poll() modeIdle activation seam: when an active
// recurring.yaml is present (no goals run required), it activates the daemon into
// modeActive and returns true. STUB: returns false, so poll() falls through to the
// existing taskvisor-start pickup unchanged.
func (d *Daemon) recurringPickup() bool {
	return false
}
