package taskvisor

import (
	"fmt"
	"strings"

	"github.com/console/tmux-cli/internal/identity"
	"github.com/console/tmux-cli/internal/producer"
	"gopkg.in/yaml.v3"
)

// reportOption mutates a producer.TaskRequest before submission. Functional
// options keep the consumer call sites a one-liner (completion/diagnostics/
// recovery) while letting tests build requests without a live daemon.
type reportOption func(*producer.TaskRequest)

// withProposedFix sets the optional remediation hint on the request.
func withProposedFix(s string) reportOption {
	return func(r *producer.TaskRequest) { r.ProposedFix = s }
}

// withExpectedGreenState sets the optional "what passing looks like" hint.
func withExpectedGreenState(s string) reportOption {
	return func(r *producer.TaskRequest) { r.ExpectedGreenState = s }
}

// normalizeCategory returns c when it is one of the valid categories, else
// "general" (defense-in-depth). The closed enum set is single-sourced in
// producer.ValidCategories; this caller's policy is to coerce, not reject.
func normalizeCategory(c string) string {
	if producer.ValidCategories[c] {
		return c
	}
	return "general"
}

// normalizeSeverity returns s when it is one of the valid severities, else
// "info" (defense-in-depth). The closed enum set is single-sourced in
// producer.ValidSeverities; this caller's policy is to coerce, not reject.
func normalizeSeverity(s string) string {
	if producer.ValidSeverities[s] {
		return s
	}
	return "info"
}

// buildRequest assembles a producer.TaskRequest deterministically — no network,
// no goroutine — so the request-building contract is unit-testable even though
// producer.Client is a concrete (non-interface) type. Category/Severity are
// normalized; SystemInfo is collected via identity using d.vcsRevision as the
// CLI version (the only build-identity value importable inside this package).
func (d *Daemon) buildRequest(category, severity, title, description string, payload map[string]any, opts ...reportOption) producer.TaskRequest {
	req := producer.TaskRequest{
		Category:    normalizeCategory(category),
		Severity:    normalizeSeverity(severity),
		Title:       title,
		Description: description,
		Payload:     payload,
		SystemInfo:  identity.CollectSystemInfo(d.vcsRevision),
	}
	for _, opt := range opts {
		opt(&req)
	}
	return req
}

// reportFailure submits a failure TaskRequest to the backend as fire-and-forget.
//
// When reporting is disabled (d.producer == nil) it is a silent no-op and spawns
// NO goroutine — the nil-guard returns before any go func(). Otherwise it builds
// the request synchronously and submits on a goroutine so the tick loop never
// blocks; d.ctx is read at goroutine RUN time, so calling reportFailure before
// the context is wired (e.g. during early Run setup) is safe.
//
// reportFailure is side-effect-free on the Daemon (it writes no fields), so any
// phase or goroutine may call it. It never forces reporting on success or
// user-initiated kills — callers decide when a failure is worth reporting.
func (d *Daemon) reportFailure(category, severity, title, description string, payload map[string]any, opts ...reportOption) {
	if d.producer == nil {
		return
	}
	req := d.buildRequest(category, severity, title, description, payload, opts...)
	go func() {
		_, _ = d.producer.SubmitTask(d.ctx, req)
	}()
}

// goalToYAML marshals g to a YAML string for the report payload. Best-effort: a
// marshal error returns "" rather than failing the caller.
func goalToYAML(g Goal) string {
	data, err := yaml.Marshal(g)
	if err != nil {
		return ""
	}
	return string(data)
}

// proposedFixFromSignal derives a remediation hint from a validator signal. It
// is nil-safe. When the FIRST finding carries a non-empty Correction, that one
// wins alone; otherwise every non-empty correction is joined with newlines. A
// nil signal, an empty finding set, or no corrections all yield "".
func proposedFixFromSignal(sig *ValidatorSignal) string {
	if sig == nil || len(sig.Findings) == 0 {
		return ""
	}
	if first := strings.TrimSpace(sig.Findings[0].Correction); first != "" {
		return first
	}
	var all []string
	for _, f := range sig.Findings {
		if c := strings.TrimSpace(f.Correction); c != "" {
			all = append(all, c)
		}
	}
	return strings.Join(all, "\n")
}

// expectedGreenState renders the goal's acceptance criteria as a single
// "; "-joined line. Empty/nil acceptance yields "".
func expectedGreenState(g Goal) string {
	return strings.Join(g.Acceptance, "; ")
}

// breakerTripTitle is the pure, deterministic title for a convergence
// circuit-breaker trip report. route is "code" or "spec"; streak/k are the
// observed consecutive-recurrence count and the configured threshold.
func breakerTripTitle(goalID, route string, streak, k int) string {
	return fmt.Sprintf("Circuit-breaker trip: %s (streak=%d/%d, %s)", goalID, streak, k, route)
}

// breakerTripPayload assembles the structured payload for a breaker-trip report.
// goal is marshalled to YAML here, so call it AFTER the trip has set
// goal.BlockedBy/Status so the snapshot reflects the just-tripped state. The map
// keys mirror the backend's expected breaker-trip schema.
func breakerTripPayload(goal *Goal, route string, signatures []string, streak, k int) map[string]any {
	return map[string]any{
		"goal_id":    goal.ID,
		"route":      route,
		"signatures": signatures,
		"streak":     streak,
		"k":          k,
		"goal_yaml":  goalToYAML(*goal),
	}
}

// reportBreakerTrip submits a convergence-circuit-breaker trip to the backend as
// a supervisor/critical, fire-and-forget report. It mirrors reportFailure's
// nil-producer contract: when reporting is disabled it is a silent no-op that
// builds nothing and spawns no goroutine. The breaker halt is a supervisor-level
// convergence event regardless of route (code/spec), so the category is always
// "supervisor" (the route is carried in the title/payload instead). Called at the
// trip edge (after BlockedBy is set) in handleFailedCycle and bounceToGeneration.
func (d *Daemon) reportBreakerTrip(goal *Goal, route string, signatures []string, streak, k int) {
	if d.producer == nil {
		return
	}
	desc := fmt.Sprintf(
		"Convergence circuit-breaker tripped: %d consecutive %s-route cycles produced an identical failure signature set (k=%d). The goal was halted to blocked/owner=human WITHOUT consuming further budget — a human must inspect the recurring failure and unblock.",
		streak, route, k)
	d.reportFailure("supervisor", "critical",
		breakerTripTitle(goal.ID, route, streak, k), desc,
		breakerTripPayload(goal, route, signatures, streak, k))
}

// inferCategory maps a failure to one of the daemon's actor categories. The
// validator signal is the most authoritative source, so its owner/FailureClass
// are consulted first (top-level fields, then findings); failing that, the
// durable NextDispatch routing marker; defaulting to "execute" because the
// code-defect re-pend is the dominant failure route. It NEVER infers
// "supervisor" (set explicitly by the diagnostics consumer). Always returns a
// valid category.
func inferCategory(sig *ValidatorSignal, g Goal) string {
	if sig != nil {
		owner, class := sig.Owner, sig.Class
		for _, f := range sig.Findings {
			if owner == "" && f.Owner != "" {
				owner = f.Owner
			}
			if class == "" && f.FailureClass != "" {
				class = f.FailureClass
			}
		}
		switch {
		case owner == "implementer" || class == "code-defect":
			return "execute"
		case owner == "planner" || class == "spec-defect":
			return "plan"
		case owner == "ops" || class == "env-config" || class == "infra-flake":
			return "general"
		case class == "validator-error":
			return "validator"
		}
	}
	switch g.NextDispatch {
	case dispatchGeneration:
		return "plan"
	case dispatchImplementer:
		return "execute"
	}
	return "execute"
}
