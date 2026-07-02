// Package e2e holds the deterministic, side-effect-free logic for the
// /tmux:e2e-evaluator conductor's PROVISION/BOOTSTRAP/TEARDOWN prologue:
// scenario→dir resolution, ~/.claude.json trust seeding, stale test-session
// selection, and the durable run-state ledger. The CLI command layer
// (cmd/tmux-cli/e2e.go) wires these to tmux/claude/docker side effects.
//
// Everything here is pure (inputs in, values out, no exec/clock) so it can be
// unit-tested without a tmux server; callers inject the UTC stamp.
package e2e

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// StatusInProgress and friends are the run-state lifecycle values (design §6).
const (
	StatusInProgress = "in-progress"
	StatusPassed     = "passed"
	StatusExhausted  = "exhausted"
	StatusEscalated  = "escalated"
)

// DefaultMaxCycles is the self-heal budget — the loop never silently spins past it.
const DefaultMaxCycles = 10

// StaleSessionPrefix is the auto-name prefix `tmux-cli start` gives a session
// whose working dir is under /tmp (slugified path). The conductor's disposable
// targets ALL carry it; real-project sessions (slugging a non-/tmp path) never
// do — so reaping by this prefix can never touch a real session (design §10).
const StaleSessionPrefix = "tmux-cli-tmp-"

// State is the durable per-scenario run ledger persisted at StateFilePath.
// History entries are kept as raw JSON so the prologue can read/rewrite the
// ledger without owning the (execute-4-authored) per-cycle entry schema.
type State struct {
	Scenario  string            `json:"scenario"`
	Cycle     int               `json:"cycle"`
	MaxCycles int               `json:"max_cycles"`
	Status    string            `json:"status"`
	History   []json.RawMessage `json:"history"`
	// Verify, when set, means "the NEXT cycle is a fix-verification cycle for
	// this signature/task" (the step-7b self-update handoff). A record without
	// verify flags clears it (fix confirmed or superseded).
	Verify *VerifyState `json:"verify,omitempty"`
	// LastSelfUpdate stamps the one managed session restart performed for a
	// resolved task — the restart-loop guard (one restart per resolved task).
	LastSelfUpdate *SelfUpdateStamp `json:"last_self_update,omitempty"`
}

// VerifyState is the pending fix-verification marker: the defect signature the
// next pristine cycle must prove cleared, and the resolved task that fixed it.
type VerifyState struct {
	Signature string `json:"signature"`
	TaskID    string `json:"task_id"`
}

// SelfUpdateStamp records the resolved task a managed session restart was
// performed for. At is RFC3339, injected by the CLI layer — this package
// stays clock-free.
type SelfUpdateStamp struct {
	TaskID string `json:"task_id"`
	At     string `json:"at"`
}

// BootstrapResult is the single JSON object `e2e-bootstrap` prints to stdout.
// On success Ok=true and every handle the DRIVE loop needs is populated; on a
// gate failure Ok=false with Stage/Error set (and the partial handles known so
// far, so a caller can still teardown).
type BootstrapResult struct {
	Ok               bool     `json:"ok"`
	Stage            string   `json:"stage,omitempty"`
	Error            string   `json:"error,omitempty"`
	Scenario         string   `json:"scenario"`
	Cycle            int      `json:"cycle"`
	MaxCycles        int      `json:"max_cycles"`
	Session          string   `json:"session,omitempty"`
	TargetPane       string   `json:"target_pane,omitempty"`
	OrchestratorPane string   `json:"orchestrator_pane,omitempty"`
	TargetDir        string   `json:"target_dir,omitempty"`
	LogPath          string   `json:"log_path,omitempty"`
	ReceiptPath      string   `json:"receipt_path,omitempty"`
	StateFile        string   `json:"state_file,omitempty"`
	HumanView        string   `json:"human_view,omitempty"`
	Handshake        string   `json:"handshake,omitempty"`
	ReapedSessions   []string `json:"reaped_sessions,omitempty"`
	// VerifySignature/VerifyTaskID surface the ledger's pending verification
	// on --resume so the conductor knows it is entering a confirm-fix cycle.
	VerifySignature string `json:"verify_signature,omitempty"`
	VerifyTaskID    string `json:"verify_task_id,omitempty"`
}

// JSON renders the result as a single compact line (pure stdout contract).
func (r BootstrapResult) JSON() string {
	b, err := json.Marshal(r)
	if err != nil {
		// Marshal of this flat struct cannot realistically fail; degrade safely.
		return fmt.Sprintf(`{"ok":false,"stage":"marshal","error":%q}`, err.Error())
	}
	return string(b)
}

var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// SlugifyScenario normalizes a free-text scenario name/brief into a stable,
// filesystem- and session-safe slug. A blank or unusable input falls back to
// the canonical default scenario so a no-arg invocation is well-defined.
func SlugifyScenario(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = slugNonAlnum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	// Keep the slug bounded — long briefs make absurd dir/session names.
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	if s == "" {
		return "symfony-dashboard-login"
	}
	return s
}

// ResolveTargetDir picks the disposable target dir. An explicit --project path
// pins it verbatim; otherwise it is /tmp/<scenario>-<UTCstamp> (design §5). The
// stamp is injected by the caller so this stays deterministic/testable.
func ResolveTargetDir(scenario, projectFlag, utcStamp string) (string, error) {
	if strings.TrimSpace(projectFlag) != "" {
		abs, err := filepath.Abs(projectFlag)
		if err != nil {
			return "", fmt.Errorf("resolve --project path %q: %w", projectFlag, err)
		}
		return abs, nil
	}
	if scenario == "" {
		return "", fmt.Errorf("scenario must be non-empty to derive a target dir")
	}
	if utcStamp == "" {
		return "", fmt.Errorf("utcStamp must be provided to derive a target dir")
	}
	return filepath.Join("/tmp", scenario+"-"+utcStamp), nil
}

// StateFilePath is the durable run-state path under the conductor's owned dir,
// relative to the given repo root (the orchestrator's cwd).
func StateFilePath(repoRoot, scenario string) string {
	return filepath.Join(repoRoot, ".tmux-cli", "e2e-evaluator", scenario+".state.json")
}

// StateMDPath is the human/kickoff-readable markdown rendering written
// alongside the JSON ledger — the `--resume-state` handoff artifact a
// relaunched session's supervisor is pointed at.
func StateMDPath(repoRoot, scenario string) string {
	return filepath.Join(repoRoot, ".tmux-cli", "e2e-evaluator", scenario+".state.md")
}

// ReportFilePath is the single naming authority for per-cycle reports:
// `.tmux-cli/e2e-evaluator/e2e-report-<scenario>-cycle-<n>.md` under the
// conductor's owned dir. The name carries the scenario slug so one scenario's
// fresh sweep can never name another scenario's reports.
func ReportFilePath(repoRoot, scenario string, cycle int) string {
	return filepath.Join(repoRoot, ".tmux-cli", "e2e-evaluator",
		fmt.Sprintf("e2e-report-%s-cycle-%d.md", scenario, cycle))
}

// IsScenarioReport reports whether name (a bare file name) is one of the
// scenario's per-cycle reports. The slug is anchored by the following
// "-cycle-<digits>.md" — same discipline as the stamp-anchored run-artifact
// matcher — so a sibling scenario whose slug extends this one ("scn" vs
// "scn-two") is never cross-matched.
func IsScenarioReport(name, scenario string) bool {
	re := regexp.MustCompile(`^e2e-report-` + regexp.QuoteMeta(scenario) + `-cycle-[0-9]+\.md$`)
	return re.MatchString(name)
}

// legacyReportRe pins the pre-scenario report shape e2e-report-cycle-<n>.md.
// The digits anchor keeps a scenario slug that starts with "cycle-" (whose
// reports read e2e-report-cycle-…-cycle-<n>.md) out of the legacy match.
var legacyReportRe = regexp.MustCompile(`^e2e-report-cycle-[0-9]+\.md$`)

// IsLegacyReport reports whether name is a pre-scenario-scoping report file —
// an orphan under the scoped scheme (it belongs to no scenario) that the
// fresh sweep removes as a one-time migration.
func IsLegacyReport(name string) bool {
	return legacyReportRe.MatchString(name)
}

// LogPath is the per-session pipe-pane transcript path. It lives under the
// conductor's owned dir (NOT the disposable target dir) so it survives teardown
// for post-cycle assertions/reporting.
func LogPath(repoRoot, session string) string {
	return filepath.Join(repoRoot, ".tmux-cli", "e2e-evaluator", "logs", session+".log")
}

// NewState builds a fresh cycle-1 in-progress ledger.
func NewState(scenario string, maxCycles int) State {
	if maxCycles <= 0 {
		maxCycles = DefaultMaxCycles
	}
	return State{
		Scenario:  scenario,
		Cycle:     1,
		MaxCycles: maxCycles,
		Status:    StatusInProgress,
		History:   []json.RawMessage{},
	}
}

// Marshal renders a State as pretty JSON for atomic write to disk.
func (s State) Marshal() ([]byte, error) {
	if s.History == nil {
		s.History = []json.RawMessage{}
	}
	return json.MarshalIndent(s, "", "  ")
}

// ParseState decodes a State ledger from disk bytes and rejects a ledger that
// fails ValidateState — every consumer (e2e-bootstrap resume, e2e-state record)
// gets the strict gate for free.
func ParseState(raw []byte) (State, error) {
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return State{}, fmt.Errorf("parse run-state: %w", err)
	}
	if err := ValidateState(s); err != nil {
		return State{}, fmt.Errorf("parse run-state: %w", err)
	}
	return s, nil
}

// ValidateState is the strict ledger gate: status must be one of the four
// lifecycle values, cycle and max_cycles at least 1, scenario non-empty.
func ValidateState(s State) error {
	switch s.Status {
	case StatusInProgress, StatusPassed, StatusExhausted, StatusEscalated:
	default:
		return fmt.Errorf("invalid status %q (want %s|%s|%s|%s)",
			s.Status, StatusInProgress, StatusPassed, StatusExhausted, StatusEscalated)
	}
	if s.Cycle < 1 {
		return fmt.Errorf("cycle must be >= 1, got %d", s.Cycle)
	}
	if s.MaxCycles < 1 {
		return fmt.Errorf("max_cycles must be >= 1, got %d", s.MaxCycles)
	}
	if strings.TrimSpace(s.Scenario) == "" {
		return fmt.Errorf("scenario must be non-empty")
	}
	return nil
}

// OutcomeFailed and OutcomePassed are the two cycle outcomes `e2e-state record`
// accepts (the step-8 STATE semantics of e2e-evaluator.xml).
const (
	OutcomeFailed = "failed"
	OutcomePassed = "passed"
)

// HistoryEntry is the typed per-cycle ledger entry `e2e-state record` WRITES.
// Reads stay json.RawMessage (tolerance); the json tags pin the XML schema
// names so a deterministic writer can never drift them.
type HistoryEntry struct {
	Cycle           int             `json:"cycle"`
	Outcome         string          `json:"outcome"`
	DefectSignature string          `json:"defect_signature"`
	TaskReported    string          `json:"task_reported"`
	TaskStatus      string          `json:"task_status"`
	GitAfter        string          `json:"git_after"`
	AppUp           bool            `json:"app_up"`
	Durations       json.RawMessage `json:"durations"`
}

// RecordCycleOutcome applies the step-8 STATE transition for one finished
// cycle: append the entry (stamped with the just-finished cycle number), then
// on OutcomeFailed bump Cycle — or flip to exhausted when the bump would
// exceed MaxCycles (loop-guard parity with the resume read) — and on
// OutcomePassed set the terminal passed status without a bump. Only an
// in-progress ledger may record; value semantics, the input is untouched.
//
// verify carries the step-7b pending fix-verification: non-nil sets State.Verify
// (the cycle counter still bumps, so the verification runs as the next cycle);
// nil clears it (fix confirmed or superseded). A passed outcome cannot flag a
// pending verification — there is nothing left to verify.
func RecordCycleOutcome(st State, entry HistoryEntry, verify *VerifyState) (State, error) {
	if err := ValidateState(st); err != nil {
		return State{}, err
	}
	if st.Status != StatusInProgress {
		return State{}, fmt.Errorf("ledger is already terminal (status=%s); record applies only to an in-progress run", st.Status)
	}
	if verify != nil {
		if strings.TrimSpace(verify.Signature) == "" || strings.TrimSpace(verify.TaskID) == "" {
			return State{}, fmt.Errorf("verify requires BOTH a signature and a task id, got signature=%q task_id=%q", verify.Signature, verify.TaskID)
		}
		if entry.Outcome == OutcomePassed {
			return State{}, fmt.Errorf("a passed outcome cannot flag a pending verification (nothing left to verify)")
		}
	}
	st.Verify = verify
	entry.Cycle = st.Cycle
	switch entry.Outcome {
	case OutcomeFailed:
		if st.Cycle+1 > st.MaxCycles {
			st.Status = StatusExhausted
		} else {
			st.Cycle++
		}
	case OutcomePassed:
		st.Status = StatusPassed
	default:
		return State{}, fmt.Errorf("outcome must be %s|%s, got %q", OutcomeFailed, OutcomePassed, entry.Outcome)
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return State{}, fmt.Errorf("marshal history entry: %w", err)
	}
	st.History = append(append([]json.RawMessage{}, st.History...), json.RawMessage(b))
	return st, nil
}

// MarkSelfUpdate stamps the managed session restart performed for a resolved
// task — the restart-loop guard. It refuses a repeat task id (one restart per
// resolved task; the XML consults the refusal to skip a second restart) and a
// terminal ledger (restarting onto a finished run is meaningless). at is
// RFC3339, injected by the CLI layer. Value semantics, the input is untouched.
func MarkSelfUpdate(st State, taskID, at string) (State, error) {
	if err := ValidateState(st); err != nil {
		return State{}, err
	}
	if strings.TrimSpace(taskID) == "" {
		return State{}, fmt.Errorf("task id must be non-empty")
	}
	if strings.TrimSpace(at) == "" {
		return State{}, fmt.Errorf("timestamp must be non-empty (the CLI layer injects it)")
	}
	if st.Status != StatusInProgress {
		return State{}, fmt.Errorf("ledger is terminal (status=%s); a session restart onto a finished run is refused", st.Status)
	}
	if st.LastSelfUpdate != nil && st.LastSelfUpdate.TaskID == taskID {
		return State{}, fmt.Errorf("self-update already performed for %s at %s — one session restart per resolved task", taskID, st.LastSelfUpdate.At)
	}
	st.LastSelfUpdate = &SelfUpdateStamp{TaskID: taskID, At: at}
	return st, nil
}

// RenderStateMD renders the ledger as the human/kickoff-readable markdown
// handoff (`<scenario>.state.md`): a freshly-relaunched Claude reading it knows
// what cycle the run is on, whether a fix-verification is pending, and what to
// do next. Pure text transform — no clock, no fs.
func RenderStateMD(st State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# e2e-evaluator run state — %s\n\n", st.Scenario)
	fmt.Fprintf(&b, "- scenario: %s\n", st.Scenario)
	fmt.Fprintf(&b, "- cycle: %d (the NEXT cycle to run)\n", st.Cycle)
	fmt.Fprintf(&b, "- max_cycles: %d\n", st.MaxCycles)
	fmt.Fprintf(&b, "- status: %s\n", st.Status)
	if st.Verify != nil {
		fmt.Fprintf(&b, "- pending verification: signature `%s` (resolved task %s)\n", st.Verify.Signature, st.Verify.TaskID)
	} else {
		b.WriteString("- pending verification: none\n")
	}
	if st.LastSelfUpdate != nil {
		fmt.Fprintf(&b, "- last self-update: %s at %s\n", st.LastSelfUpdate.TaskID, st.LastSelfUpdate.At)
	}
	if cycle, outcome, ok := lastHistoryOutcome(st.History); ok {
		fmt.Fprintf(&b, "- last outcome: cycle %d %s\n", cycle, outcome)
	} else {
		b.WriteString("- last outcome: none recorded\n")
	}
	b.WriteString("\n## Next action\n\n")
	switch {
	case st.Status != StatusInProgress:
		fmt.Fprintf(&b, "Run is terminal (status: %s) — nothing to resume.\n", st.Status)
	case st.Verify != nil:
		fmt.Fprintf(&b, "Invoke `/tmux:e2e-evaluator resume` — this is a fix-verification cycle for signature `%s` (task %s): run ONE pristine cycle and have JUDGE confirm the signature cleared (or the run reaches app-up).\n",
			st.Verify.Signature, st.Verify.TaskID)
	default:
		fmt.Fprintf(&b, "Invoke `/tmux:e2e-evaluator resume` — continue the in-progress run at cycle %d.\n", st.Cycle)
	}
	return b.String()
}

// lastHistoryOutcome extracts {cycle, outcome} from the newest history entry,
// tolerating any entry shape (raw JSON reads stay schema-loose).
func lastHistoryOutcome(history []json.RawMessage) (int, string, bool) {
	if len(history) == 0 {
		return 0, "", false
	}
	var e struct {
		Cycle   int    `json:"cycle"`
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(history[len(history)-1], &e); err != nil || e.Outcome == "" {
		return 0, "", false
	}
	return e.Cycle, e.Outcome, true
}

// SelectStaleSessions filters a tmux session-name list down to the conductor's
// disposable /tmp targets (StaleSessionPrefix) — the ONLY names safe to reap.
// Real-project sessions are never matched (design §10).
func SelectStaleSessions(names []string) []string {
	var out []string
	for _, n := range names {
		if strings.HasPrefix(n, StaleSessionPrefix) {
			out = append(out, n)
		}
	}
	return out
}

// SeedTrustConfig returns a new ~/.claude.json body with the target dir marked
// trusted + onboarded and bypass-permissions accepted globally, so the
// auto-launched claude lands straight at the idle prompt (config-then-run,
// step 2). It is a pure transform over the raw config bytes; the caller writes
// the result atomically. A nil/empty input starts from an empty object.
func SeedTrustConfig(raw []byte, targetDir string) ([]byte, error) {
	if strings.TrimSpace(targetDir) == "" {
		return nil, fmt.Errorf("targetDir must be non-empty to seed trust")
	}
	root := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, fmt.Errorf("parse ~/.claude.json: %w", err)
		}
	}

	// Top-level bypassPermissionsModeAccepted = true.
	root["bypassPermissionsModeAccepted"] = json.RawMessage("true")

	// projects[targetDir].{hasTrustDialogAccepted,hasCompletedProjectOnboarding}=true.
	projects := map[string]map[string]json.RawMessage{}
	if cur, ok := root["projects"]; ok && len(cur) > 0 {
		if err := json.Unmarshal(cur, &projects); err != nil {
			return nil, fmt.Errorf("parse ~/.claude.json projects: %w", err)
		}
	}
	proj := projects[targetDir]
	if proj == nil {
		proj = map[string]json.RawMessage{}
	}
	proj["hasTrustDialogAccepted"] = json.RawMessage("true")
	proj["hasCompletedProjectOnboarding"] = json.RawMessage("true")
	projects[targetDir] = proj

	projBytes, err := json.Marshal(projects)
	if err != nil {
		return nil, fmt.Errorf("re-marshal projects: %w", err)
	}
	root["projects"] = projBytes

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal ~/.claude.json: %w", err)
	}
	return out, nil
}

// DisposableMarkerName is the marker file `e2e-bootstrap` writes into every
// disposable target dir it provisions. Teardown/reaping selects dirs by this
// marker (never by name pattern), so a /tmp dir without it is never touched.
const DisposableMarkerName = ".tmux-cli-e2e-disposable"

// Marker is the parsed content of a DisposableMarkerName file. Session is
// empty until the bootstrap records it post-start (a marker without a session
// line means the run crashed between provision and start).
type Marker struct {
	Scenario string
	Stamp    string
	Session  string
}

// NewMarker renders the provision-time marker content (no session yet).
func NewMarker(scenario, stamp string) string {
	return fmt.Sprintf("scenario: %s\nstamp: %s\n", scenario, stamp)
}

// MarkerWithSession records (or replaces) the session line in marker content.
func MarkerWithSession(content, session string) string {
	var lines []string
	for _, l := range strings.Split(content, "\n") {
		if strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "session:") {
			continue
		}
		lines = append(lines, l)
	}
	lines = append(lines, "session: "+session)
	return strings.Join(lines, "\n") + "\n"
}

// ParseMarker decodes marker content; unknown/garbage lines are ignored.
func ParseMarker(content string) Marker {
	var m Marker
	for _, l := range strings.Split(content, "\n") {
		key, val, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "scenario":
			m.Scenario = val
		case "stamp":
			m.Stamp = val
		case "session":
			m.Session = val
		}
	}
	return m
}

// ShouldReapDisposable decides whether a marked dir is an orphan to remove:
// no recorded session (crashed between provision and start) or a recorded
// session that is no longer live. The marker file itself is the disposable
// claim — liveness is the only thing keeping the dir alive.
func ShouldReapDisposable(m Marker, liveSessions []string) bool {
	if m.Session == "" {
		return true
	}
	for _, s := range liveSessions {
		if strings.TrimSpace(s) == m.Session {
			return false
		}
	}
	return true
}

// StartOutput is the single JSON line `tmux-cli start --print-json` prints.
type StartOutput struct {
	Session string `json:"session"`
	Created bool   `json:"created"`
}

// ParseStartOutput decodes the `start --print-json` stdout contract: exactly
// one compact JSON line {"session":...,"created":true|false} with all human
// output on stderr. Anything else is an error — there is deliberately NO
// fallback parse path (a second path would hide contract drift).
func ParseStartOutput(stdout string) (StartOutput, error) {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return StartOutput{}, fmt.Errorf("start --print-json printed nothing on stdout")
	}
	if strings.ContainsAny(trimmed, "\n\r") {
		return StartOutput{}, fmt.Errorf("start --print-json stdout must be exactly one line, got: %q", trimmed)
	}
	var out StartOutput
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return StartOutput{}, fmt.Errorf("parse start --print-json output %q: %w", trimmed, err)
	}
	if out.Session == "" {
		return StartOutput{}, fmt.Errorf("start --print-json output missing session: %q", trimmed)
	}
	return out, nil
}

// ReceiptPath is the notify-orchestrator receipt file path for a run identity,
// next to the pipe-pane log under the conductor's owned dir. The name is the
// bootstrap's unique run identity (<scenario>-<stamp>, the target-dir
// basename) rather than the tmux session name: the receipt env must be
// injected BEFORE `start`, and the session name embeds a start-time timestamp
// that does not exist yet at injection time.
func ReceiptPath(repoRoot, name string) string {
	return filepath.Join(repoRoot, ".tmux-cli", "e2e-evaluator", "logs", name+".receipt")
}

// MarkExhausted returns a copy of the state with a terminal exhausted status.
func (s State) MarkExhausted() State {
	s.Status = StatusExhausted
	return s
}

// TrustSeeded reports whether raw ~/.claude.json bytes carry the three keys
// SeedTrustConfig guarantees for targetDir — the post-write verify half of
// the seed-then-verify cycle.
func TrustSeeded(raw []byte, targetDir string) bool {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return false
	}
	if string(root["bypassPermissionsModeAccepted"]) != "true" {
		return false
	}
	var projects map[string]map[string]json.RawMessage
	if err := json.Unmarshal(root["projects"], &projects); err != nil {
		return false
	}
	proj := projects[targetDir]
	return string(proj["hasTrustDialogAccepted"]) == "true" &&
		string(proj["hasCompletedProjectOnboarding"]) == "true"
}

// HandshakeToken is the comms-test string the target echoes back through
// notify-orchestrator. The conductor proves the channel live by finding it
// (alongside the notify success line) in the target's pipe-pane log (step 3b).
func HandshakeToken(session string) string {
	return "E2E-HANDSHAKE-OK " + session
}
