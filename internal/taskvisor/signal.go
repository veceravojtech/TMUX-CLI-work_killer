package taskvisor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Verdict enum — the closed set of validation verdicts. Every verdict string
// produced or consumed anywhere MUST reference one of these consts (never a
// bare literal) so the MCP guard, the daemon router and the classifier agree.
//   - pass:    all rules satisfied; advance the goal.
//   - fail:    a code defect; the implementer must fix it (charges code retries).
//   - blocked: a precondition is missing (env/spec/infra); ops or planner owns it.
//   - error:   the validator itself could not run / never reported; re-validate
//     only (charges validation retries, never code/spec retries).
const (
	VerdictPass    = "pass"
	VerdictFail    = "fail"
	VerdictBlocked = "blocked"
	VerdictError   = "error"
)

// ValidationFinding is one rule's outcome reported by the validator.
//
// SYNC: this struct is mirrored field-for-field (same json tags, same
// semantics) by mcp.ValidationFinding in internal/mcp/server.go. The two are
// kept in lock-step by TestValidationFindingStructsInSync (internal/mcp).
// Never add, rename or retag a field here without doing the same there.
type ValidationFinding struct {
	Rule           string `json:"rule" yaml:"rule"`
	Status         string `json:"status" yaml:"status"`
	Detail         string `json:"detail" yaml:"detail"`
	Correction     string `json:"correction,omitempty" yaml:"correction,omitempty"`
	FailingCommand string `json:"failing_command,omitempty" yaml:"failing_command,omitempty"`
	OutputExcerpt  string `json:"output_excerpt,omitempty" yaml:"output_excerpt,omitempty"`
	ExpectedState  string `json:"expected_state,omitempty" yaml:"expected_state,omitempty"`
	FailureClass   string `json:"failure_class,omitempty" yaml:"failure_class,omitempty"`
	Owner          string `json:"owner,omitempty" yaml:"owner,omitempty"`

	// C10 incremental re-validation fields. Scope and Preconditions are inputs to
	// ComputeInputFingerprint (the latter denormalized from Goal.Preconditions by
	// the orchestrator so the anchored 2-arg fingerprint signature is preserved);
	// InputFingerprint/ReusedFromCycle/ReusedFingerprint are reuse-decision
	// outputs carried for dispatch rendering. All omitempty so signal.json shape
	// is unchanged for findings that do not use them.
	Scope             []string `json:"scope,omitempty" yaml:"scope,omitempty"`
	Preconditions     []string `json:"preconditions,omitempty" yaml:"preconditions,omitempty"`
	InputFingerprint  string   `json:"input_fingerprint,omitempty" yaml:"input_fingerprint,omitempty"`
	ReusedFromCycle   int      `json:"reused_from_cycle,omitempty" yaml:"reused_from_cycle,omitempty"`
	ReusedFingerprint string   `json:"reused_fingerprint,omitempty" yaml:"reused_fingerprint,omitempty"`

	// B5a structured correction. CorrectionEdits is an OPTIONAL, machine-applicable
	// remedy a validator MAY emit alongside the REQUIRED free-text Correction: a
	// list of {File,Line,Old,New} edits a downstream mechanical applier consumes
	// before the generation bounce. Advisory only — never auto-applied here, never
	// folded into failureSignature/ComputeSignatures (it is a remedy, not failure
	// identity) nor HasSubstantiveSpecDefect. Appended LAST so existing field order
	// is preserved; omitempty so prose-only findings are byte-identical on the wire.
	// SYNC: mirrored by mcp.ValidationFinding.CorrectionEdits + mcp.CorrectionEdit
	// (TestValidationFindingStructsInSync + TestCorrectionEditStructsInSync_TagsMatch).
	CorrectionEdits []CorrectionEdit `json:"correction_edit,omitempty" yaml:"correction_edit,omitempty"`
}

// CorrectionEdit is one machine-applicable edit within a finding's structured
// remedy. SYNC: mirrored field-for-field (same json tags) by mcp.CorrectionEdit
// in internal/mcp/server.go (taskvisor cannot import mcp — import cycle). Keep
// the two in lock-step; TestCorrectionEditStructsInSync_TagsMatch pins this.
type CorrectionEdit struct {
	File string `json:"file" yaml:"file"`                     // repo-relative path (REQUIRED, non-empty)
	Line int    `json:"line,omitempty" yaml:"line,omitempty"` // 1-based anchor hint; 0 = unknown
	Old  string `json:"old,omitempty" yaml:"old,omitempty"`   // exact text to replace ("" = insert)
	New  string `json:"new,omitempty" yaml:"new,omitempty"`   // replacement ("" = delete)
}

// ClassifyVerdict rolls a set of findings up into a single (verdict, owner)
// pair. Findings are sorted by Rule before roll-up so the result is
// deterministic regardless of input order. Precedence, highest first:
//
//  1. fail — any finding that is a code defect: Status==fail with
//     FailureClass=="code-defect", OR (leaf-4 catch-all) any non-pass finding
//     whose FailureClass matches no recognised class. Owner: "implementer".
//  2. blocked — any blocked finding (missing precondition). Owner: "planner"
//     if any blocked finding is owned by the planner, else "ops"
//     (planner > ops).
//  3. error — the validator could not run. Owner: "ops".
//  4. pass — every finding passed. Owner: "".
//
// The catch-all in tier 1 guarantees a non-pass finding never silently rolls
// up to pass. ClassifyVerdict performs only the cross-finding roll-up; it
// never invents or mutates an individual finding's Owner (emitters set that).
//
// Environment/connectivity/auth/missing-secret failures must arrive here
// PRE-CLASSIFIED as env-config (or infra-flake) by the validator prompt
// (investigate-worker.xml step 3's deterministic decision rule + decision table). This
// function intentionally treats an unrecognised/empty FailureClass as a code
// defect (tier-1 leaf-4 catch-all), so any misclassification of an env failure
// is fixed UPSTREAM in the validator prompt, never by changing logic here.
func ClassifyVerdict(findings []ValidationFinding) (verdict, owner string) {
	sorted := make([]ValidationFinding, len(findings))
	copy(sorted, findings)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Rule < sorted[j].Rule })

	knownClass := map[string]bool{
		"code-defect":     true,
		"env-config":      true,
		"infra-flake":     true,
		"spec-defect":     true,
		"validator-error": true,
	}

	var hasCodeDefect, hasBlocked, hasValidatorError, blockedPlanner bool
	for _, f := range sorted {
		if f.Status == VerdictPass {
			continue
		}
		switch {
		case f.Status == VerdictFail && f.FailureClass == "code-defect":
			hasCodeDefect = true
		case !knownClass[f.FailureClass]:
			// Leaf-4 catch-all: a non-pass finding with an empty or unrecognised
			// class is treated as a code defect so it never becomes pass.
			hasCodeDefect = true
		case f.FailureClass == "env-config", f.FailureClass == "infra-flake", f.FailureClass == "spec-defect", f.Status == VerdictBlocked:
			hasBlocked = true
			if f.Owner == "planner" {
				blockedPlanner = true
			}
		case f.FailureClass == "validator-error" || f.Status == VerdictError:
			hasValidatorError = true
		default:
			// Recognised class on a non-pass finding that fits no tier above —
			// never silently pass.
			hasCodeDefect = true
		}
	}

	switch {
	case hasCodeDefect:
		return VerdictFail, "implementer"
	case hasBlocked:
		if blockedPlanner {
			return VerdictBlocked, "planner"
		}
		return VerdictBlocked, "ops"
	case hasValidatorError:
		return VerdictError, "ops"
	default:
		return VerdictPass, ""
	}
}

// PassGate carries the deterministic-backing inputs GateTerminalPass needs to
// decide whether a terminal LLM `pass` is permitted.
//   - RequireValidate: the goal DECLARES validate steps (len(goal.Validate) > 0),
//     so a deterministic `validate.sh` is expected to be the independent anchor.
//   - ScriptPassed: the deterministic `validate.sh` exited 0 (runValidateScript's
//     `passed` contract). Threaded from checkSupervisingPhase via
//     goalRuntime.scriptPassed — true when validate.sh passed, false otherwise
//     (including when no validate.sh exists or the runtime was cleared). The
//     salvageLateVerdicts path always passes false (runtime cleared, conservative).
type PassGate struct {
	RequireValidate bool
	ScriptPassed    bool
}

// GateTerminalPass is the deterministic terminal-pass gate (P7). A terminal LLM
// `pass` rests on judgment alone — the validator (a Claude window) grades against
// acceptance criteria the planner LLM authored, so the two share blind spots; the
// deterministic `validate.sh` is the only independent anchor. When a goal DECLARES
// validate steps but that script did not pass, an LLM `pass` has zero deterministic
// backing — a missing anchor is indistinguishable from "unverified" — so the pass
// is downgraded to (error, ops): re-validate (charging ValidationRetries via the
// existing rerunValidationOnly route) and, on exhaustion, an ops hold.
//
// It is intentionally SEPARATE from ClassifyVerdict (left 100% untouched) so the
// proven cross-finding roll-up and its test suites stay byte-identical: this gate
// composes AFTER classification at each seam that finalizes a validating-phase
// verdict. Non-pass verdicts and goals with no validate steps pass through
// unchanged. Pure, no I/O — fully unit-testable.
func GateTerminalPass(verdict, owner string, gate PassGate) (string, string) {
	if verdict == VerdictPass && gate.RequireValidate && !gate.ScriptPassed {
		return VerdictError, "ops"
	}
	return verdict, owner
}

// contentlessDetail is the taskvisor-local MIRROR of mcp.contentlessCorrections
// (internal/mcp/tools_taskvisor.go:220-229). taskvisor cannot import internal/mcp
// (that would create an import cycle: mcp already imports taskvisor), so the
// stub deny-list is duplicated here VERBATIM. The two maps must stay key-for-key
// identical; TestContentlessDetail_ParityWithMCP pins this frozen key set so any
// taskvisor-side drift is caught. A maintainer adding a stub to one map MUST add
// it to the other. Compared after strings.TrimSpace + strings.ToLower.
var contentlessDetail = map[string]bool{
	"":                 true, // empty (also caught by the TrimSpace check)
	"fix it":           true,
	"none":             true,
	"n/a":              true,
	"na":               true,
	"not applicable":   true,
	"to be determined": true,
	"tbd":              true,
}

// isContentlessDetail reports whether s is an empty/stub filler that carries no
// actionable contradiction. Normalized (trim + lower) exactly like the mcp-side
// contentlessCorrections check so the two agree.
func isContentlessDetail(s string) bool {
	return contentlessDetail[strings.ToLower(strings.TrimSpace(s))]
}

// HasSubstantiveSpecDefect reports whether findings contain at least one
// planner-owned blocked/spec-defect finding that cites a CONCRETE (non-stub)
// contradiction — i.e. a genuine spec defect worth burning the scarce single
// SpecRetries on. It implements predicate A (the zero-regression reading): a
// finding qualifies iff it is non-pass, planner-owned AND (Status==blocked OR
// FailureClass=="spec-defect"), AND at least one of Detail/Correction is
// non-stub.
//
// Rationale: validateFindings (internal/mcp) already forces every MCP-emitted
// Status==blocked finding to carry a non-stub Correction, so every legitimately
// emitted blocked/planner finding passes A automatically — the guard never
// re-routes a genuine spec defect. It fires only on (a) a top-level fallback
// blocked/planner verdict with no classifiable finding, and (b) a
// FailureClass=="spec-defect" finding with Status!=blocked (which bypasses
// validateFindings) whose Detail AND Correction are both stub. In both cases
// there is no concrete contradiction to act on, so the daemon re-validates
// (charging ValidationRetries) instead of bouncing to generation.
func HasSubstantiveSpecDefect(findings []ValidationFinding) bool {
	for _, f := range findings {
		if f.Status == VerdictPass {
			continue
		}
		plannerBlocked := f.Owner == "planner" &&
			(f.Status == VerdictBlocked || f.FailureClass == "spec-defect")
		if !plannerBlocked {
			continue
		}
		if !isContentlessDetail(f.Detail) || !isContentlessDetail(f.Correction) {
			return true // has a concrete Detail or Correction
		}
	}
	return false
}

type SupervisorSignal struct {
	Source    string `json:"source"`
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

type ValidatorSignal struct {
	Source     string              `json:"source"`
	Verdict    string              `json:"verdict"`
	Class      string              `json:"class,omitempty"`
	Owner      string              `json:"owner,omitempty"`
	Remedy     string              `json:"remedy,omitempty"`
	Findings   []ValidationFinding `json:"findings"`
	NextAction string              `json:"next_action"`
	Timestamp  string              `json:"timestamp"`
	// Signatures is the current-cycle serialization of this signal's non-pass
	// findings (one stable hash per finding, sorted ascending). It is the
	// per-cycle snapshot used by the C6 convergence circuit-breaker and mirrored
	// for downstream result reporting. The DURABLE cross-cycle comparison
	// baseline lives on Goal.ConvergenceSignatures (goals.yaml), not here, because
	// signal.json is rewritten every cycle. omitempty preserves back-compat with
	// signals written before C6.
	Signatures []string `json:"signatures,omitempty"`
}

// Failure-cause normalization regexes, compiled once at package load.
//
// They are applied by NormalizeFailureCause in a FIXED, documented order so the
// output is deterministic across runs/processes (identical input → identical
// string → identical hash). Each variable kind maps to a DISTINCT substitution
// token; kinds are never collapsed into one generic placeholder.
var (
	// Step 1 — error-code extractors (run BEFORE any stripping so a code embedded
	// in a path is captured, not erased). Tried in this priority order; the first
	// pattern that matches wins.
	reCodeExit  = regexp.MustCompile(`(?i)exit[\s_-]*(?:code|status)?[\s_:=-]*(\d+)`)
	reCodeSig   = regexp.MustCompile(`SIG[A-Z]+`)
	reCodeErrno = regexp.MustCompile(`E[A-Z]{2,}`)
	reCodeHTTP  = regexp.MustCompile(`(?i)(?:http|status)[\s_]*(?:code)?[\s:=]*([1-5][0-9]{2})`)

	// Step 2 — variable-part strippers, applied in this sub-order. TS first (it
	// embeds ':' and digits the LINE rule would otherwise grab); HEX before PATH
	// (long hashes); PATH before PID/LINE; LINE last.
	reTS   = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`)
	reHex  = regexp.MustCompile(`0x[0-9a-fA-F]+|\b[0-9a-fA-F]{8,}\b`)
	rePath = regexp.MustCompile(`(?:[A-Za-z]:\\[^\s]+|/[^\s:]+(?:/[^\s:]+)*)`)
	rePID  = regexp.MustCompile(`(?i)\b(?:pid[\s=:]*\d+|process\s+\d+)`)
	reLine = regexp.MustCompile(`:\d+:\d+|(?i)\bline\s+\d+|:\d+\b`)

	reWhitespace = regexp.MustCompile(`\s+`)
)

// NormalizeFailureCause reduces a raw failure-cause string to a canonical form
// so that two failures that differ ONLY in volatile detail (timestamps, paths,
// PIDs, hex addresses, line numbers) normalize to the same string — while
// genuinely distinct failures stay distinct. The transformation order is FIXED
// and documented (changing it changes every hash), as follows:
//
//  1. EXTRACT the first error code (exit/status N → EXIT_<N>; SIG... ; errno
//     E[A-Z]{2,}; HTTP status near "http"/"status" → HTTP_<N>) and prefix it as
//     "CODE=<code>; ". This runs BEFORE stripping so a code sitting inside a
//     path (e.g. /tmp/exit-137.log) is captured, not erased by the PATH rule.
//  2. STRIP variable parts to DISTINCT tokens, in order: <TS>, <HEX>, <PATH>,
//     <PID>, <LINE>. Distinct kinds are never merged into one token.
//  3. TRIM and COLLAPSE all whitespace runs to a single space.
func NormalizeFailureCause(cause string) string {
	prefix := ""
	switch {
	case reCodeExit.MatchString(cause):
		m := reCodeExit.FindStringSubmatch(cause)
		prefix = "CODE=EXIT_" + m[1] + "; "
	case reCodeSig.MatchString(cause):
		prefix = "CODE=" + reCodeSig.FindString(cause) + "; "
	case reCodeErrno.MatchString(cause):
		prefix = "CODE=" + reCodeErrno.FindString(cause) + "; "
	case reCodeHTTP.MatchString(cause):
		m := reCodeHTTP.FindStringSubmatch(cause)
		prefix = "CODE=HTTP_" + m[1] + "; "
	}

	s := cause
	s = reTS.ReplaceAllString(s, "<TS>")
	s = reHex.ReplaceAllString(s, "<HEX>")
	s = rePath.ReplaceAllString(s, "<PATH>")
	s = rePID.ReplaceAllString(s, "<PID>")
	s = reLine.ReplaceAllString(s, "<LINE>")

	s = reWhitespace.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	return prefix + s
}

// failureSignature hashes one finding into a stable hex digest. It binds to the
// REAL ValidationFinding fields (the spec's hypothetical Investigator/Cause
// fields never landed): the hash is taken over
//
//	Rule + "\x00" + FailureClass + "\x00" + NormalizeFailureCause(cause)
//
// where cause is the finding's failing detail (FailingCommand, OutputExcerpt and
// Detail joined). The NUL separators prevent field-boundary collisions (so e.g.
// swapping Rule and FailureClass yields a different digest). Deterministic:
// identical findings → identical digest across runs/processes.
func failureSignature(f ValidationFinding) string {
	parts := make([]string, 0, 3)
	for _, p := range []string{f.FailingCommand, f.OutputExcerpt, f.Detail} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	cause := strings.Join(parts, "\n")
	payload := f.Rule + "\x00" + f.FailureClass + "\x00" + NormalizeFailureCause(cause)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// ComputeSignatures maps every NON-pass finding to its failureSignature, sorts
// the result ascending and returns it. Sorting makes the output independent of
// the input finding order, so two cycles that produce the same failures in any
// order yield an identical (comparable) signature set. Pass findings are
// excluded; an all-pass set returns an empty slice (the breaker never fires on
// an empty set).
func ComputeSignatures(findings []ValidationFinding) []string {
	sigs := make([]string, 0, len(findings))
	for _, f := range findings {
		if f.Status == VerdictPass {
			continue
		}
		sigs = append(sigs, failureSignature(f))
	}
	sort.Strings(sigs)
	return sigs
}

func SignalPath(projectRoot, goalID string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "goals", goalID, "signal.json")
}

func LoadSignal(projectRoot, goalID string) (any, error) {
	data, err := os.ReadFile(SignalPath(projectRoot, goalID))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	source, _ := raw["source"].(string)
	switch source {
	case "supervisor":
		var sig SupervisorSignal
		if err := json.Unmarshal(data, &sig); err != nil {
			return nil, err
		}
		return &sig, nil
	case "validator":
		var sig ValidatorSignal
		if err := json.Unmarshal(data, &sig); err != nil {
			return nil, err
		}
		return &sig, nil
	default:
		return nil, fmt.Errorf("unknown signal source: %q", source)
	}
}

func SaveValidatorSignal(projectRoot, goalID string, sig *ValidatorSignal) error {
	sig.Source = "validator"
	return saveSignal(projectRoot, goalID, sig)
}

func DeleteSignal(projectRoot, goalID string) error {
	p := SignalPath(projectRoot, goalID)
	err := os.Remove(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func saveSignal(projectRoot, goalID string, sig any) error {
	p := SignalPath(projectRoot, goalID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(sig)
	if err != nil {
		return err
	}
	return atomicWrite(p, data, 0o644)
}

// --- C10 incremental re-validation ---------------------------------------

// Revalidation actions returned by PlanRevalidation.
const (
	ActionRerun = "RERUN"
	ActionReuse = "REUSE"
)

// ResultEntry is one finding's persisted outcome for a single validation cycle.
// It is the unit of the orchestrator-owned results.json ledger; the daemon only
// reads it. ReusedFromCycle/ReusedFingerprint are set only when a prior cycle's
// pass was reused (they echo the cycle it came from and the unchanged hash).
type ResultEntry struct {
	FindingID         string `json:"finding_id"`
	Status            string `json:"status"`
	InputFingerprint  string `json:"input_fingerprint"`
	CycleNumber       int    `json:"cycle_number"`
	ReusedFromCycle   int    `json:"reused_from_cycle,omitempty"`
	ReusedFingerprint string `json:"reused_fingerprint,omitempty"`
}

// Results is the per-goal validation result ledger persisted at results.json.
// It is keyed by finding id (the finding's Rule — C1 added no explicit
// Finding.ID). Go marshals map keys in sorted order, so the JSON is byte-stable.
type Results struct {
	Results map[string]ResultEntry `json:"results"`
}

// ResultsPath is the sibling of SignalPath holding the re-validation ledger.
func ResultsPath(projectRoot, goalID string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "goals", goalID, "results.json")
}

// LoadResults reads the results.json ledger. An ABSENT file returns (nil, nil)
// — the first cycle / fresh goal is the safe degenerate full-run case, not an
// error. A CORRUPT/unparseable file also degrades to (nil, nil) after a logged
// warning: a partial write must trigger a full re-run, never crash a cycle.
func LoadResults(projectRoot, goalID string) (*Results, error) {
	data, err := os.ReadFile(ResultsPath(projectRoot, goalID))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var r Results
	if err := json.Unmarshal(data, &r); err != nil {
		log.Printf("LoadResults: corrupt results.json for %s (%v) — treating as absent (full re-validation)", goalID, err)
		return nil, nil
	}
	return &r, nil
}

// SaveResults atomically writes the results.json ledger, reusing atomicWrite so
// a partial file is never observable. ORCHESTRATOR-OWNED: only the
// goal-validation-done MCP path calls this; the daemon never writes it.
func SaveResults(projectRoot, goalID string, r *Results) error {
	p := ResultsPath(projectRoot, goalID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return atomicWrite(p, data, 0o644)
}

// fingerprintInput is the marshaled, sorted shape that ComputeInputFingerprint
// hashes. Field order + sorted slices make the JSON (and thus the hash) stable.
type fingerprintInput struct {
	RuleDef        string   `json:"rule_def"`
	Scope          []string `json:"scope"`
	ChangedInScope []string `json:"changed_in_scope"`
	Preconditions  []string `json:"preconditions"`
}

// ComputeInputFingerprint hashes a finding's re-validation INPUT SET into a
// stable sha256 hex digest. The input set is the rule/check definition
// (finding.Rule), the finding's sorted Scope, the sorted intersection of Scope
// with changedFiles (so a touched in-scope file flips the hash ⇒ RE-RUN, while
// an out-of-scope change does not), and the finding's sorted Preconditions
// (denormalized from Goal.Preconditions by the orchestrator). Every collection
// is sorted before marshaling so a shuffled changedFiles yields an identical
// digest. The anchored 2-arg signature is intentional: preconditions ride on the
// finding, not a third argument.
func ComputeInputFingerprint(finding ValidationFinding, changedFiles []string) string {
	scope := append([]string(nil), finding.Scope...)
	sort.Strings(scope)

	inScope := make(map[string]bool, len(scope))
	for _, s := range scope {
		inScope[s] = true
	}
	var changedInScope []string
	for _, c := range changedFiles {
		if inScope[c] {
			changedInScope = append(changedInScope, c)
		}
	}
	sort.Strings(changedInScope)

	preconds := append([]string(nil), finding.Preconditions...)
	sort.Strings(preconds)

	in := fingerprintInput{
		RuleDef:        finding.Rule,
		Scope:          scope,
		ChangedInScope: changedInScope,
		Preconditions:  preconds,
	}
	// json.Marshal of a struct of strings/[]string never errors.
	data, _ := json.Marshal(in)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// FindingPlan is the per-finding spawn decision produced by PlanRevalidation.
type FindingPlan struct {
	FindingID       string `json:"finding_id"`
	Action          string `json:"action"`
	ReusedFromCycle int    `json:"reused_from_cycle,omitempty"`
	Fingerprint     string `json:"fingerprint"`
}

// PlanRevalidation decides, per finding, whether to RE-RUN an investigation
// worker or REUSE a prior pass. The result is sorted by finding id for a
// deterministic plan. Full-sweep gates take precedence: forceFull (--full) or
// finalCycle (all pass, no further cycles) ⇒ every finding RERUN; a nil prev
// ledger (first cycle / fresh goal) ⇒ every finding RERUN. Otherwise per finding:
// a missing prior entry ⇒ RERUN; a non-pass prior status ⇒ RERUN; a changed
// fingerprint ⇒ RERUN (regression); else REUSE carrying the prior CycleNumber.
func PlanRevalidation(prev *Results, findings []ValidationFinding, changedFiles []string, forceFull, finalCycle bool) []FindingPlan {
	sorted := append([]ValidationFinding(nil), findings...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Rule < sorted[j].Rule })

	plans := make([]FindingPlan, 0, len(sorted))
	for _, f := range sorted {
		fp := ComputeInputFingerprint(f, changedFiles)
		plan := FindingPlan{FindingID: f.Rule, Action: ActionRerun, Fingerprint: fp}

		if forceFull || finalCycle || prev == nil {
			plans = append(plans, plan)
			continue
		}
		entry, ok := prev.Results[f.Rule]
		switch {
		case !ok, entry.Status != VerdictPass, entry.InputFingerprint != fp:
			// unseen, prior non-pass, or input changed → re-run.
		default:
			plan.Action = ActionReuse
			plan.ReusedFromCycle = entry.CycleNumber
		}
		plans = append(plans, plan)
	}
	return plans
}
