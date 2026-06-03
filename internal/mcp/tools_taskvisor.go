package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/tmux"
	"gopkg.in/yaml.v3"
)

type tvGoal struct {
	ID          string   `yaml:"id"`
	Description string   `yaml:"description"`
	Acceptance  []string `yaml:"acceptance,omitempty"`
	Validate    []string `yaml:"validate,omitempty"`

	Preconditions []taskvisor.Precondition `yaml:"preconditions,omitempty"`

	Status     string   `yaml:"status"`
	Retries    int      `yaml:"retries"`
	MaxRetries int      `yaml:"max_retries"`
	Phase      string   `yaml:"phase,omitempty"`
	DependsOn  []string `yaml:"depends_on,omitempty"`
	BlockedBy  string   `yaml:"blocked_by,omitempty"`
	StartedAt  string   `yaml:"started_at,omitempty"`
	FinishedAt string   `yaml:"finished_at,omitempty"`
}

type tvGoalsFile struct {
	CurrentGoal      string   `yaml:"current_goal"`
	GlobalMaxRetries int      `yaml:"global_max_retries,omitempty"`
	Goals            []tvGoal `yaml:"goals"`
}

func tvGoalsFilePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "goals.yaml")
}

func tvLoadGoals(projectRoot string) (*tvGoalsFile, error) {
	data, err := os.ReadFile(tvGoalsFilePath(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var gf tvGoalsFile
	if err := yaml.Unmarshal(data, &gf); err != nil {
		return nil, err
	}
	return &gf, nil
}

func tvSaveGoals(projectRoot string, gf *tvGoalsFile) error {
	p := tvGoalsFilePath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(gf)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func withGoalsFileLock(projectRoot string, fn func() error) error {
	return taskvisor.WithGoalsLock(projectRoot, fn)
}

func tvNextGoalID(goals []tvGoal) string {
	max := 0
	for _, g := range goals {
		if !strings.HasPrefix(g.ID, "goal-") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(g.ID, "goal-"))
		if err != nil {
			continue
		}
		if n > max {
			max = n
		}
	}
	return fmt.Sprintf("goal-%03d", max+1)
}

// TaskvisorStart checks for pending goals and writes the taskvisor-start signal file.
func (s *Server) TaskvisorStart() (*TaskvisorStartOutput, error) {
	gf, err := tvLoadGoals(s.workingDir)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load goals.yaml: %w", ErrInvalidInput, err)
	}
	if gf == nil {
		return nil, fmt.Errorf("%w: goals.yaml not found", ErrInvalidInput)
	}

	hasPending := false
	for _, g := range gf.Goals {
		if g.Status == "pending" {
			hasPending = true
			break
		}
	}
	if !hasPending {
		return nil, fmt.Errorf("%w: no pending goals in goals.yaml", ErrInvalidInput)
	}

	signalPath := filepath.Join(s.workingDir, ".tmux-cli", "taskvisor-start")
	if err := os.MkdirAll(filepath.Dir(signalPath), 0o755); err != nil {
		return nil, fmt.Errorf("%w: failed to create directory: %w", ErrInvalidInput, err)
	}
	if err := os.WriteFile(signalPath, []byte("start"), 0o644); err != nil {
		return nil, fmt.Errorf("%w: failed to write signal file: %w", ErrInvalidInput, err)
	}

	return &TaskvisorStartOutput{Started: true}, nil
}

const MaxGoalDescriptionLength = 120

var allowedPhases = map[string]bool{
	"gate": true, "scaffold": true, "fixtures": true, "domain": true,
	"application": true, "infrastructure": true, "action": true, "auth": true,
	"event": true, "cross-cutting": true, "deployment": true, "ci": true, "final": true,
}

// allowedInvestigatorTypes is the enum accepted in an explicit investigation_config.
// It mirrors allowedPhases in shape and MUST stay a superset of the types the
// planner (task-plan-generate.xml) emits — quality-gate, test-execution,
// convention-audit, static-analysis, architecture-check, command,
// environment-check, file-check, implementation-check, integration-check — plus
// code-review (design §3) and e2e-test/integration-test (investigate.xml) — so a
// valid planner config is never rejected (M2 design §88: superset of planner output).
var allowedInvestigatorTypes = map[string]bool{
	"static-analysis": true, "quality-gate": true, "test-execution": true,
	"architecture-check": true, "convention-audit": true, "code-review": true,
	"e2e-test": true, "integration-test": true,
	"command": true, "environment-check": true, "file-check": true,
	"implementation-check": true, "integration-check": true,
}

// validateInvestigators enforces the explicit investigation_config contract: 2–4
// entries, each with a Name, an enum Type, at least one Command, and a Pass.
// Index in messages is 1-based for operator readability. Callers must invoke it
// only when len(invs) > 0 — omission is valid and triggers M1's fallback.
func validateInvestigators(invs []taskvisor.Investigator) error {
	if len(invs) < 2 || len(invs) > 4 {
		return fmt.Errorf("%w: investigation_config requires 2–4 entries (got %d)", ErrInvalidInput, len(invs))
	}
	for i, inv := range invs {
		idx := i + 1
		if inv.Name == "" {
			return fmt.Errorf("%w: investigator[%d]: name is required", ErrInvalidInput, idx)
		}
		if !allowedInvestigatorTypes[inv.Type] {
			names := make([]string, 0, len(allowedInvestigatorTypes))
			for k := range allowedInvestigatorTypes {
				names = append(names, k)
			}
			sort.Strings(names)
			return fmt.Errorf("%w: investigator[%d]: invalid type %q; allowed: %s", ErrInvalidInput, idx, inv.Type, strings.Join(names, ","))
		}
		if len(inv.Commands) == 0 {
			return fmt.Errorf("%w: investigator[%d]: at least one command is required", ErrInvalidInput, idx)
		}
		if inv.Pass == "" {
			return fmt.Errorf("%w: investigator[%d]: pass is required", ErrInvalidInput, idx)
		}
	}
	return nil
}

// GoalCreate generates a sequential goal ID, appends the goal to goals.yaml, creates the goal directory, and writes goal.md.
func (s *Server) GoalCreate(description string, acceptance, validate []string, context, notInScope, phase string, maxRetries int, dependsOn []string, preconditions []taskvisor.Precondition, investigators []taskvisor.Investigator) (*GoalCreateOutput, error) {
	if description == "" {
		return nil, fmt.Errorf("%w: description cannot be empty", ErrInvalidInput)
	}
	if len(description) > MaxGoalDescriptionLength {
		return nil, fmt.Errorf("%w: description exceeds %d characters (got %d); use --acceptance for detailed criteria", ErrInvalidInput, MaxGoalDescriptionLength, len(description))
	}
	if len(validate) == 0 {
		return nil, fmt.Errorf("%w: at least one validation rule is required", ErrInvalidInput)
	}

	if phase != "" && !allowedPhases[phase] {
		names := make([]string, 0, len(allowedPhases))
		for k := range allowedPhases {
			names = append(names, k)
		}
		return nil, fmt.Errorf("%w: invalid phase %q; allowed: %s", ErrInvalidInput, phase, strings.Join(names, ","))
	}

	// Validate an explicit investigation_config before any filesystem side effect
	// (no goal ID burned, no dir created). Omission (nil/empty) is valid and lets
	// M1's deriveInvestigators fallback run in WriteGoalMD.
	if len(investigators) > 0 {
		if err := validateInvestigators(investigators); err != nil {
			return nil, err
		}
	}

	if maxRetries == 0 {
		maxRetries = 5
	}

	var goalID string
	if err := withGoalsFileLock(s.workingDir, func() error {
		gf, err := tvLoadGoals(s.workingDir)
		if err != nil {
			return fmt.Errorf("%w: failed to load goals.yaml: %w", ErrInvalidInput, err)
		}
		if gf == nil {
			gf = &tvGoalsFile{}
		}

		if len(dependsOn) > 0 {
			existingIDs := make(map[string]bool, len(gf.Goals))
			for _, g := range gf.Goals {
				existingIDs[g.ID] = true
			}
			for _, dep := range dependsOn {
				if !existingIDs[dep] {
					return fmt.Errorf("%w: depends_on references non-existent goal: %s", ErrInvalidInput, dep)
				}
			}
		}

		goalID = tvNextGoalID(gf.Goals)

		goal := tvGoal{
			ID:            goalID,
			Description:   description,
			Status:        "pending",
			Retries:       0,
			MaxRetries:    maxRetries,
			Phase:         phase,
			DependsOn:     dependsOn,
			Preconditions: preconditions,
		}
		gf.Goals = append(gf.Goals, goal)

		return tvSaveGoals(s.workingDir, gf)
	}); err != nil {
		return nil, err
	}

	goalDir := filepath.Join(s.workingDir, ".tmux-cli", "goals", goalID)
	if err := os.MkdirAll(filepath.Join(goalDir, "corrections"), 0o755); err != nil {
		return nil, fmt.Errorf("%w: failed to create goal directory: %w", ErrInvalidInput, err)
	}

	if err := taskvisor.WriteGoalMD(goalDir, description, phase, acceptance, validate, preconditions, context, notInScope, investigators); err != nil {
		return nil, fmt.Errorf("%w: failed to write goal.md: %w", ErrInvalidInput, err)
	}

	return &GoalCreateOutput{ID: goalID}, nil
}

// contentlessCorrections is a small, deliberately-short deny-list of stub
// "corrections" that carry no actionable instruction. It is compared after
// strings.TrimSpace + strings.ToLower and folds in the common abbreviated
// spellings of each filler phrase. Maintainers may extend it as new
// contentless stubs surface in the wild.
var contentlessCorrections = map[string]bool{
	"":                 true, // empty (also caught by the TrimSpace check)
	"fix it":           true,
	"none":             true,
	"n/a":              true,
	"na":               true,
	"not applicable":   true,
	"to be determined": true,
	"tbd":              true,
}

// validateFindings enforces the C5 non-lossy contract: every code-defect
// (status==fail) finding must carry concrete failing_command, output_excerpt,
// expected_state and correction detail (correction must also not be a
// contentless stub) so the re-dispatched implementer sees which command failed,
// what it produced and what state was expected. The four fields describe a
// failing COMMAND, which is only meaningful for a code defect — blocked
// (missing precondition) and error (validator could not run) findings, plus the
// daemon's own synthesized error finding, legitimately omit them and are NOT
// enforced here (C1 froze that shape). A pass finding may also leave all four
// empty. The error names all four required fields plus the offending ones; on
// error the caller writes NO signal.
func validateFindings(verdict string, findings []ValidationFinding) error {
	for _, f := range findings {
		if f.Status == taskvisor.VerdictBlocked {
			// C5/WS3: a parked goal's runbook must be actionable — require a
			// concrete remedy. COMMAND-shaped fields stay fail-only (blocked has
			// no failing command). Normalize exactly like the fail branch below.
			if c := strings.ToLower(strings.TrimSpace(f.Correction)); c == "" || contentlessCorrections[c] {
				return fmt.Errorf("%w: blocked finding %q requires a non-empty, non-stub correction (remedy)", ErrInvalidInput, f.Rule)
			}
			continue
		}
		if f.Status != taskvisor.VerdictFail {
			continue
		}
		var bad []string
		if strings.TrimSpace(f.FailingCommand) == "" {
			bad = append(bad, "failing_command")
		}
		if strings.TrimSpace(f.OutputExcerpt) == "" {
			bad = append(bad, "output_excerpt")
		}
		if strings.TrimSpace(f.ExpectedState) == "" {
			bad = append(bad, "expected_state")
		}
		if c := strings.ToLower(strings.TrimSpace(f.Correction)); c == "" || contentlessCorrections[c] {
			bad = append(bad, "correction")
		}
		if len(bad) > 0 {
			return fmt.Errorf("%w: non-pass finding %q requires non-empty failing_command, output_excerpt, expected_state, correction (missing/stub: %s)",
				ErrInvalidInput, f.Rule, strings.Join(bad, ", "))
		}
	}
	return nil
}

// GoalValidationDone validates caller authorization and writes signal.json for the goal.
func (s *Server) GoalValidationDone(goalID, verdict string, findings []ValidationFinding, nextAction string, results []FindingResult) (*GoalValidationDoneOutput, error) {
	// C1 verdict guard — validate the reported verdict/findings shape before any
	// I/O. Verdict strings are referenced via the taskvisor.Verdict* consts so
	// the enum stays single-sourced across packages (no literal duplication).
	//
	// Empty verdict: the validator reported back but produced no verdict. We
	// synthesize an error verdict owned by ops and carry that on a synthesized
	// finding so the daemon routes it as a validator error (re-validate only),
	// never as a code defect. This is the caller-reported synthesis site; the
	// daemon watchdog (taskvisor.go ~:1044) is the distinct never-reported site.
	if verdict == "" {
		verdict = taskvisor.VerdictError
		findings = append(findings, ValidationFinding{
			Rule:         "validator",
			Status:       taskvisor.VerdictError,
			FailureClass: "validator-error",
			Owner:        "ops",
			Detail:       "validator reported an empty verdict",
		})
	}
	allowed := map[string]bool{
		taskvisor.VerdictPass:    true,
		taskvisor.VerdictFail:    true,
		taskvisor.VerdictBlocked: true,
		taskvisor.VerdictError:   true,
	}
	if !allowed[verdict] {
		return nil, fmt.Errorf("%w: verdict must be one of %s, %s, %s, %s; got %q",
			ErrInvalidInput, taskvisor.VerdictPass, taskvisor.VerdictFail, taskvisor.VerdictBlocked, taskvisor.VerdictError, verdict)
	}
	for _, f := range findings {
		if f.Status != taskvisor.VerdictPass && strings.TrimSpace(f.FailureClass) == "" {
			return nil, fmt.Errorf("%w: finding %q has status %q but no failure_class (required when status is not pass)",
				ErrInvalidInput, f.Rule, f.Status)
		}
	}

	// C5 non-lossy guard — every code-defect (fail) finding must carry concrete
	// failing_command/output_excerpt/expected_state/correction detail so the
	// re-dispatched implementer never re-discovers the failure blind. Runs at the
	// top, before any I/O, so a malformed report writes NO signal.json.
	if err := validateFindings(verdict, findings); err != nil {
		return nil, err
	}

	gf, err := tvLoadGoals(s.workingDir)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load goals.yaml: %w", ErrInvalidInput, err)
	}
	if gf == nil {
		return nil, fmt.Errorf("%w: goal not found: %s", ErrInvalidInput, goalID)
	}

	found := false
	for _, g := range gf.Goals {
		if g.ID == goalID {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: goal not found: %s", ErrInvalidInput, goalID)
	}

	sessionID, err := s.discoverSession()
	if err != nil {
		return nil, err
	}

	windows, err := s.executor.ListWindows(sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTmuxCommandFailed, err)
	}

	var validatorWindow *tmux.WindowInfo
	for i := range windows {
		if windows[i].Name == "validator" {
			validatorWindow = &windows[i]
			break
		}
	}
	if validatorWindow == nil {
		return nil, fmt.Errorf("%w: no validator window found", ErrInvalidInput)
	}

	validatorUUID, err := s.executor.GetWindowOption(sessionID, validatorWindow.TmuxWindowID, tmux.WindowUUIDOption)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read validator window UUID: %w", ErrTmuxCommandFailed, err)
	}

	callerUUID := os.Getenv("TMUX_WINDOW_UUID")
	if callerUUID != validatorUUID {
		return nil, fmt.Errorf("%w: caller is not the validator window (caller=%s, validator=%s)", ErrInvalidInput, callerUUID, validatorUUID)
	}

	sig := validatorSignalJSON{
		Source:     "validator",
		Verdict:    verdict,
		Findings:   findings,
		NextAction: nextAction,
		Timestamp:  time.Now().Format(time.RFC3339),
	}

	signalData, err := json.Marshal(sig)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to marshal signal: %w", ErrInvalidInput, err)
	}

	signalDir := filepath.Join(s.workingDir, ".tmux-cli", "goals", goalID)
	if err := os.MkdirAll(signalDir, 0o755); err != nil {
		return nil, fmt.Errorf("%w: failed to create goal directory: %w", ErrInvalidInput, err)
	}

	signalPath := filepath.Join(signalDir, "signal.json")
	tmpPath := signalPath + ".tmp"
	if err := os.WriteFile(tmpPath, signalData, 0o644); err != nil {
		return nil, fmt.Errorf("%w: failed to write signal file: %w", ErrInvalidInput, err)
	}
	if err := os.Rename(tmpPath, signalPath); err != nil {
		return nil, fmt.Errorf("%w: failed to rename signal file: %w", ErrInvalidInput, err)
	}

	// C10 — orchestrator-owned results.json write path. When the caller supplies
	// per-finding re-validation inputs, compute each finding's input fingerprint
	// (rule + in-scope changed files + preconditions denormalized from the goal)
	// and persist the cycle's ledger. The daemon only READS results.json; this
	// MCP path (triggered by the validator orchestrator at cycle end) is the sole
	// writer. Absent results leave any prior ledger untouched.
	if len(results) > 0 {
		if err := s.writeRevalidationResults(goalID, results); err != nil {
			return nil, err
		}
	}

	return &GoalValidationDoneOutput{Written: true}, nil
}

// writeRevalidationResults computes input fingerprints for the supplied
// per-finding results and writes the orchestrator-owned results.json ledger.
func (s *Server) writeRevalidationResults(goalID string, results []FindingResult) error {
	// Load the full goal to denormalize preconditions onto each finding and to
	// derive the current cycle number from consumed code-defect budget.
	preconds := []string{}
	cycle := 1
	if gf, err := taskvisor.LoadGoals(s.workingDir); err == nil && gf != nil {
		if g, ok := gf.GoalByID(goalID); ok {
			preconds = stringifyPreconditions(g.Preconditions)
			if c := g.MaxCodeRetries - g.CodeRetries + 1; c > cycle {
				cycle = c
			}
		}
	}

	ledger := &taskvisor.Results{Results: make(map[string]taskvisor.ResultEntry, len(results))}
	for _, r := range results {
		f := taskvisor.ValidationFinding{
			Rule:          r.ID,
			Status:        r.Status,
			Scope:         r.ScopeFiles,
			Preconditions: preconds,
		}
		ledger.Results[r.ID] = taskvisor.ResultEntry{
			FindingID:        r.ID,
			Status:           r.Status,
			InputFingerprint: taskvisor.ComputeInputFingerprint(f, r.ChangedFiles),
			CycleNumber:      cycle,
		}
	}

	if err := taskvisor.SaveResults(s.workingDir, goalID, ledger); err != nil {
		return fmt.Errorf("%w: failed to write results.json: %w", ErrInvalidInput, err)
	}
	return nil
}

// stringifyPreconditions renders a goal's preconditions into a deterministic,
// sorted "<kind>:<spec>" slice so they can ride on a finding for fingerprinting.
func stringifyPreconditions(pcs []taskvisor.Precondition) []string {
	out := make([]string, 0, len(pcs))
	for _, p := range pcs {
		out = append(out, p.Kind+":"+p.Spec)
	}
	sort.Strings(out)
	return out
}

// GoalPrune atomically removes all taskvisor goal state.
func (s *Server) GoalPrune() (*GoalPruneOutput, error) {
	activePath := filepath.Join(s.workingDir, ".tmux-cli", "taskvisor-active")
	if _, err := os.Stat(activePath); err == nil {
		return nil, fmt.Errorf("%w: taskvisor daemon is active — stop it first", ErrInvalidInput)
	}

	gf, err := tvLoadGoals(s.workingDir)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load goals.yaml: %w", ErrInvalidInput, err)
	}
	count := 0
	if gf != nil {
		count = len(gf.Goals)
	}

	goalsFile := tvGoalsFilePath(s.workingDir)
	if err := os.Remove(goalsFile); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: failed to remove goals.yaml: %w", ErrInvalidInput, err)
	}

	goalsDir := filepath.Join(s.workingDir, ".tmux-cli", "goals")
	if err := os.RemoveAll(goalsDir); err != nil {
		return nil, fmt.Errorf("%w: failed to remove goals directory: %w", ErrInvalidInput, err)
	}

	for _, name := range []string{"taskvisor-current-goal", "taskvisor-start"} {
		p := filepath.Join(s.workingDir, ".tmux-cli", name)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: failed to remove %s: %w", ErrInvalidInput, name, err)
		}
	}

	return &GoalPruneOutput{Pruned: true, GoalsRemoved: count}, nil
}

type validatorSignalJSON struct {
	Source     string              `json:"source"`
	Verdict    string              `json:"verdict"`
	Findings   []ValidationFinding `json:"findings"`
	NextAction string              `json:"next_action"`
	Timestamp  string              `json:"timestamp"`
}
