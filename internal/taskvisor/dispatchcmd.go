package taskvisor

import (
	"fmt"
	"log"
)

// DispatchKind enumerates EVERY worker command taskvisor dispatches. It is the
// single control surface for "what taskvisor dispatches": each call site names a
// kind, and dispatchCommand renders the exact slash command. No call site
// hand-formats a slash command, so every command taskvisor can emit is listed
// (and tested) in this one file.
//
// Which kind a goal's first dispatch uses is decided by resolveDispatchKind
// (the phase matrix + overrides). Elaborate / Investigate / RecurringSupervisor
// are dispatched by their own lifecycle sites, which pass the kind explicitly.
type DispatchKind int

const (
	// DispatchPlan is the full plan+implement path for a freshly-pending goal:
	// the per-goal planner expands the goal's fan-out into tasks.yaml, then hands
	// off to the supervisor. Sent by dispatch().
	DispatchPlan DispatchKind = iota
	// DispatchImplement skips planning and runs the supervisor directly — on a
	// retry/resume against an existing tasks.yaml (the supervisor then self-specs
	// a single-task fan-out). Sent by dispatch() and dispatchRetry().
	DispatchImplement
	// DispatchElaborate is Tier-2 elaboration: it authors a roadmap goal's
	// concrete fields against the live tree and flips it roadmap→pending. Sent by
	// dispatchElaborate().
	DispatchElaborate
	// DispatchInvestigate runs the validation-phase investigate orchestrator
	// against a goal's goal.md. Sent by createValidatorAndSendPayload().
	DispatchInvestigate
	// DispatchRecurringSupervisor runs a standalone supervisor against a free-form
	// recurring-task prompt (the recurring driver, not the goal state machine).
	// Sent by the recurring driver's dispatching phase.
	DispatchRecurringSupervisor
	// DispatchGate runs the dedicated, self-contained /tmux:gate executor for the
	// atomic gate phase: it probes the environment, applies corrections in order,
	// writes AGENTS.md, and signals completion — WITHOUT the supervisor→worker
	// fan-out the gate never needs. The supervisor is an implementation
	// orchestrator; a verify-and-bootstrap gate is not implementation, so it gets
	// its own quick command instead of a degenerate single-task supervisor run.
	// Sent by dispatch() for a first dispatch of phase=gate. Because /tmux:gate
	// writes NO tasks.yaml, every re-dispatch routes back through dispatch() (not
	// dispatchRetry, which gates on tasksYamlExists) and resolves here again.
	DispatchGate
)

// String returns a stable, human-readable kind name for logs and test output.
func (k DispatchKind) String() string {
	switch k {
	case DispatchPlan:
		return "plan"
	case DispatchImplement:
		return "implement"
	case DispatchElaborate:
		return "elaborate"
	case DispatchInvestigate:
		return "investigate"
	case DispatchRecurringSupervisor:
		return "recurring-supervisor"
	case DispatchGate:
		return "gate"
	default:
		return fmt.Sprintf("DispatchKind(%d)", int(k))
	}
}

// DispatchArgs carries every value any dispatch command might need. Each kind
// reads only the fields relevant to it (documented per-case in dispatchCommand);
// the rest are ignored. A struct (not positional args) keeps the differently
// shaped commands in one resolver without overloading the meaning of a slot.
type DispatchArgs struct {
	// DispatchPath is the absolute path to the goal's dispatch.md (Plan, Elaborate).
	DispatchPath string
	// GoalMdPath is the absolute path to the goal's goal.md (Investigate).
	GoalMdPath string
	// GoalID is the goal id (Plan, Implement, Elaborate).
	GoalID string
	// Prompt is the free-form recurring-task prompt (RecurringSupervisor).
	Prompt string
}

// dispatchCommand renders the EXACT slash command taskvisor sends to a worker
// window for the given kind. This is the single source of truth for the command
// strings — to change what taskvisor dispatches for a kind, change it HERE (and
// dispatchcmd_test.go), never at a call site.
func dispatchCommand(kind DispatchKind, a DispatchArgs) string {
	switch kind {
	case DispatchPlan:
		return fmt.Sprintf("/tmux:plan %s %s", a.DispatchPath, a.GoalID)
	case DispatchImplement:
		// No dispatch path: the supervisor reloads context from the goal dir and
		// only the authoritative goal id is shipped (dispatchRetry rationale).
		return fmt.Sprintf("/tmux:supervisor %s", a.GoalID)
	case DispatchElaborate:
		return fmt.Sprintf("/tmux:elaborate %s %s", a.DispatchPath, a.GoalID)
	case DispatchInvestigate:
		return fmt.Sprintf("/tmux:investigate %s", a.GoalMdPath)
	case DispatchRecurringSupervisor:
		return fmt.Sprintf("/tmux:supervisor %s", a.Prompt)
	case DispatchGate:
		// Only the authoritative goal id is shipped (same rationale as
		// DispatchImplement): the gate executor reloads its spec from the goal
		// dir's goal.md, so no dispatch path is needed.
		return fmt.Sprintf("/tmux:gate %s", a.GoalID)
	default:
		panic(fmt.Sprintf("dispatchCommand: unknown DispatchKind %d", int(kind)))
	}
}

// Goal lifecycle phases. These mirror the allowedPhases enum the goal-create
// MCP validates against (internal/mcp/tools_taskvisor.go) — kept in sync by hand
// because taskvisor cannot import mcp without an import cycle. The roadmap
// generator (task-plan-generate) stamps exactly one of these on every goal, and
// the dispatch matrix below keys on it.
const (
	PhaseGate           = "gate"
	PhaseScaffold       = "scaffold"
	PhaseFixtures       = "fixtures"
	PhaseDomain         = "domain"
	PhaseApplication    = "application"
	PhaseInfrastructure = "infrastructure"
	PhaseAction         = "action"
	PhaseAuth           = "auth"
	PhaseEvent          = "event"
	PhaseCrossCutting   = "cross-cutting"
	PhaseDeployment     = "deployment"
	PhaseCI             = "ci"
	PhaseFinal          = "final"
)

// initialDispatchByPhase is the PHASE → first-dispatch-command matrix: the
// single, explicit control point for which worker command taskvisor runs when a
// goal is FIRST dispatched (before any retry), absent a per-phase override.
//
//   - DispatchPlan — run the /tmux:plan pre-planner (spec workers + blind audit)
//     to expand the goal into a parallel tasks.yaml fan-out, then the supervisor
//     implements it. The default for any phase whose goal decomposes into
//     multiple parallel deliverables.
//   - DispatchImplement — SKIP planning: dispatch the supervisor directly. With
//     no pre-planned tasks.yaml the supervisor self-specs a single-task fan-out
//     (supervisor.xml step 3). The right choice for an ATOMIC goal, where the
//     plan step is pure overhead.
//
// gate is the lone phase that neither plans NOR runs the supervisor: its goal is
// a single environment-check + AGENTS.md bootstrap (docker engine/compose/daemon
// probes, port preflight) — nothing to fan out, and verifying an environment is
// not implementation, so it routes to the dedicated /tmux:gate executor
// (DispatchGate) rather than a degenerate single-task supervisor run. Every
// other phase plans. Listed exhaustively (not just the exceptions) so this table
// reads as the whole policy and a drift test can assert full coverage.
var initialDispatchByPhase = map[string]DispatchKind{
	PhaseGate:           DispatchGate, // dedicated quick gate executor — no plan, no supervisor
	PhaseScaffold:       DispatchPlan,
	PhaseFixtures:       DispatchPlan,
	PhaseDomain:         DispatchPlan,
	PhaseApplication:    DispatchPlan,
	PhaseInfrastructure: DispatchPlan,
	PhaseAction:         DispatchPlan,
	PhaseAuth:           DispatchPlan,
	PhaseEvent:          DispatchPlan,
	PhaseCrossCutting:   DispatchPlan,
	PhaseDeployment:     DispatchPlan,
	PhaseCI:             DispatchPlan,
	PhaseFinal:          DispatchPlan,
}

// initialDispatchKind returns the matrix default first-dispatch kind for a
// phase. An unknown or empty phase defaults to DispatchPlan — the safe, full
// path (never silently skip planning for a phase we don't recognize).
func initialDispatchKind(phase string) DispatchKind {
	if k, ok := initialDispatchByPhase[phase]; ok {
		return k
	}
	return DispatchPlan
}

// resolveDispatchKind is the pure first-dispatch decision the daemon acts on. In
// precedence order:
//
//  1. A generation bounce (NextDispatch==dispatchGeneration) ALWAYS forces
//     DispatchPlan — that marker exists to route the goal through the planner to
//     create/wire a missing prerequisite, a correctness requirement an override
//     must not defeat.
//  2. A per-phase override (from setting.yaml dispatch_overrides; may be nil)
//     replaces the matrix default for that phase.
//  3. The phase matrix default (initialDispatchKind).
//
// override may be nil — a nil-map read returns the zero value with ok=false, so
// the lookup safely falls through.
func resolveDispatchKind(goal *Goal, override map[string]DispatchKind) DispatchKind {
	if goal.NextDispatch == dispatchGeneration {
		return DispatchPlan
	}
	if k, ok := override[goal.Phase]; ok {
		return k
	}
	return initialDispatchKind(goal.Phase)
}

// dispatchKindForGoal is the daemon's first-dispatch decision: resolveDispatchKind
// seeded with this daemon's parsed setting.yaml overrides (d.dispatchPhaseOverride,
// may be nil). dispatch() calls it.
func (d *Daemon) dispatchKindForGoal(goal *Goal) DispatchKind {
	return resolveDispatchKind(goal, d.dispatchPhaseOverride)
}

// parseDispatchKindName maps a setting.yaml dispatch_overrides value to a kind.
// Only the two phase-selectable initial kinds are valid overrides; elaborate /
// investigate / recurring are lifecycle commands, not phase choices. Returns
// ok=false for anything else (the caller logs and ignores it).
func parseDispatchKindName(name string) (DispatchKind, bool) {
	switch name {
	case "plan":
		return DispatchPlan, true
	case "implement", "supervisor":
		return DispatchImplement, true
	default:
		return 0, false
	}
}

// parseDispatchOverrides validates the raw setting.yaml dispatch_overrides map
// into a phase → kind map. An unknown phase or an unparseable kind is logged and
// skipped (fail-soft: one bad row never discards the valid ones). Returns nil for
// an empty/nil input or when nothing valid survived, so resolveDispatchKind cleanly
// falls back to the built-in matrix.
func parseDispatchOverrides(raw map[string]string) map[string]DispatchKind {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]DispatchKind, len(raw))
	for phase, kindName := range raw {
		if !knownPhase(phase) {
			log.Printf("[dispatch-override] ignoring unknown phase %q", phase)
			continue
		}
		kind, ok := parseDispatchKindName(kindName)
		if !ok {
			log.Printf("[dispatch-override] phase %q: ignoring invalid kind %q (want \"plan\" or \"implement\")", phase, kindName)
			continue
		}
		out[phase] = kind
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// knownPhase reports whether a phase appears in the built-in dispatch matrix
// (the canonical set of phases the generator emits).
func knownPhase(phase string) bool {
	_, ok := initialDispatchByPhase[phase]
	return ok
}
