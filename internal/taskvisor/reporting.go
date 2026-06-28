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

// fallbackProposedFix derives a non-blank remediation hint from the request's
// own title/category when no call site supplied one. The backend rejects a
// blank proposedFix with a 422 (NotBlank), so this is the choke-point backstop
// for any report path that forgets the field. Non-blank by construction even
// for an empty title (the %q renders as "" inside non-blank text).
func fallbackProposedFix(req producer.TaskRequest) string {
	return fmt.Sprintf("Triage the failure report %q: inspect the payload diagnostics and .tmux-cli/logs/taskvisor.log, then remediate the underlying %s-category failure.", req.Title, req.Category)
}

// fallbackExpectedGreenState derives a non-blank, checkable green-state
// statement from the request's own title — the NotBlank backstop counterpart to
// fallbackProposedFix.
func fallbackExpectedGreenState(req producer.TaskRequest) string {
	return fmt.Sprintf("The condition that produced %q no longer occurs: a subsequent daemon pass over the same state emits no such failure report.", req.Title)
}

// buildRequest assembles a producer.TaskRequest deterministically — no network,
// no goroutine — so the request-building contract is unit-testable even though
// producer.Client is a concrete (non-interface) type. Category/Severity are
// normalized; SystemInfo is collected via identity using d.vcsRevision as the
// CLI version (the only build-identity value importable inside this package).
// After the options are applied, a blank ProposedFix/ExpectedGreenState is
// backfilled from the request's own title/category so no request ever violates
// the backend's NotBlank contract on the wire.
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
	if strings.TrimSpace(req.ProposedFix) == "" {
		req.ProposedFix = fallbackProposedFix(req)
	}
	if strings.TrimSpace(req.ExpectedGreenState) == "" {
		req.ExpectedGreenState = fallbackExpectedGreenState(req)
	}
	return req
}

// submitReport is the single submission path for daemon-built failure reports.
// When reporting is disabled (d.producer == nil) it spawns NO goroutine and
// invokes onResult(nil) synchronously — delivered-equivalent, so dedup marks
// are kept. Otherwise it submits on a goroutine so the tick loop never blocks;
// d.ctx is read at goroutine RUN time, so submitting before the context is
// wired (e.g. during early Run setup) is safe. onResult (nil-safe) receives
// SubmitTask's error so callers can mark delivery truthfully.
func (d *Daemon) submitReport(req producer.TaskRequest, onResult func(error)) {
	if d.producer == nil {
		if onResult != nil {
			onResult(nil)
		}
		return
	}
	go func() {
		_, err := d.producer.SubmitTask(d.ctx, req)
		if onResult != nil {
			onResult(err)
		}
	}()
}

// submitReportFn is the indirection every report path routes through. It
// defaults to (*Daemon).submitReport and is a package var ONLY so tests can
// observe submitted requests deterministically: producer.Client is a concrete
// type with an unexported constructor and no daemon-level injection seam, so a
// swappable function is the only way to observe a submission without a live
// backend (the reportWorkerCrashFn pattern). Production never reassigns it.
var submitReportFn = (*Daemon).submitReport

// reportFailure submits a failure TaskRequest to the backend as fire-and-forget.
//
// The nil-producer contract lives in submitReport: reporting disabled spawns no
// goroutine and never panics. The request (including identity.CollectSystemInfo)
// is built even when reporting is disabled — an accepted trade-off, since
// failure events are rare and the build is cheap and network-free.
//
// reportFailure is side-effect-free on the Daemon (it writes no fields), so any
// phase or goroutine may call it. It never forces reporting on success or
// user-initiated kills — callers decide when a failure is worth reporting.
func (d *Daemon) reportFailure(category, severity, title, description string, payload map[string]any, opts ...reportOption) {
	req := d.buildRequest(category, severity, title, description, payload, opts...)
	submitReportFn(d, req, nil)
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
// a supervisor/critical, fire-and-forget report. The nil-producer contract lives
// in submitReport (via reportFailure): reporting disabled spawns no goroutine and
// never panics. The breaker halt is a supervisor-level convergence event
// regardless of route (code/spec), so the category is always "supervisor" (the
// route is carried in the title/payload instead). Both backend NotBlank contract
// fields are explicit: the fix names the goal-reset remediation; the green state
// derives from the goal's acceptance, falling back to a no-recurrence statement.
// Called at the trip edge (after BlockedBy is set) in handleFailedCycle and
// bounceToGeneration.
func (d *Daemon) reportBreakerTrip(goal *Goal, route string, signatures []string, streak, k int) {
	desc := fmt.Sprintf(
		"Convergence circuit-breaker tripped: %d consecutive %s-route cycles produced an identical failure signature set (k=%d). The goal was halted to blocked/owner=human WITHOUT consuming further budget — a human must inspect the recurring failure and unblock.",
		streak, route, k)
	fix := fmt.Sprintf(
		"Inspect the recurring failure signatures for %s in the payload, fix the underlying cause, then run `taskvisor goal reset %s` to unblock and re-pend the goal.",
		goal.ID, goal.ID)
	expected := expectedGreenState(*goal)
	if strings.TrimSpace(expected) == "" {
		expected = fmt.Sprintf("Goal %s completes a %s cycle without re-emitting an identical failure-signature set.", goal.ID, route)
	}
	d.reportFailure("supervisor", "critical",
		breakerTripTitle(goal.ID, route, streak, k), desc,
		breakerTripPayload(goal, route, signatures, streak, k),
		withProposedFix(fix), withExpectedGreenState(expected))
}

// reportPollWedge submits a daemon poll/bring-up wedge to the backend as a
// supervisor/critical, fire-and-forget report — the same reportFailure path the
// convergence breaker uses, with a distinct title/payload (NOT a parallel failure
// channel). Called at the poll-error fail-fast edge in handlePollError, AFTER the
// goal's Status/FailedBy are set so the YAML snapshot reflects the failed goal.
// streak is the observed consecutive identical-error count; k = circuitBreakerK().
func (d *Daemon) reportPollWedge(goal *Goal, streak int, pollErr error) {
	k := d.circuitBreakerK()
	msg := ""
	if pollErr != nil {
		msg = pollErr.Error()
	}
	desc := fmt.Sprintf(
		"Daemon poll/bring-up wedge: %d consecutive IDENTICAL poll errors (k=%d) on goal %s. The goal could not be brought up, so the poll loop failed fast — marking the goal failed via the existing failure path and deactivating — instead of looping forever and leaking the goal/session. Last error: %s",
		streak, k, goal.ID, msg)
	fix := fmt.Sprintf(
		"Inspect the repeating bring-up/poll error for %s (worktree / compose stack / window creation). Fix the underlying cause, then run `taskvisor goal reset %s` to re-pend the goal.",
		goal.ID, goal.ID)
	expected := expectedGreenState(*goal)
	if strings.TrimSpace(expected) == "" {
		expected = fmt.Sprintf("Goal %s dispatches and brings up its worker without a repeating poll error.", goal.ID)
	}
	d.reportFailure("supervisor", "critical",
		fmt.Sprintf("Poll-wedge fail-fast: %s (streak=%d/%d)", goal.ID, streak, k),
		desc,
		map[string]any{
			"goal_id":    goal.ID,
			"streak":     streak,
			"k":          k,
			"poll_error": msg,
			"goal_yaml":  goalToYAML(*goal),
		},
		withProposedFix(fix), withExpectedGreenState(expected))
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
