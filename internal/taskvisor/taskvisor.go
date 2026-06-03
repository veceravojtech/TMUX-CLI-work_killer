package taskvisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/tmux"
	"gopkg.in/yaml.v3"
)

type ScriptRunnerFunc func(ctx context.Context, scriptPath, dir string, env []string) (stdout, stderr string, exitCode int, err error)

const validateScriptTimeout = 30 * time.Second

// stallWatchdogTicks is the number of consecutive idle-but-runnable ticks the
// stall watchdog tolerates before logging a single STUCK: line (~15-30s at the
// 5-10s poll cadence). A package constant by design — this is a diagnostics
// signal that should never fire in healthy operation, so it gets no setting.yaml
// surface (a config key would only invite tuning a never-fire alarm).
const stallWatchdogTicks = 3

type mode int

const (
	modeIdle mode = iota
	modeActive
)

type phase int

const (
	phaseNone phase = iota
	phaseSupervising
	phaseValidating
)

type CreatedWindow struct {
	TmuxWindowID string
	Name         string
}

type WindowCreateFunc func(name, command string) (*CreatedWindow, error)

type Daemon struct {
	workDir                 string
	executor                tmux.TmuxExecutor
	createWindowFn          WindowCreateFunc
	mode                    mode
	session                 string
	pollInterval            time.Duration
	dispatchTimeout         time.Duration
	validateTimeout         time.Duration
	currentGoalDispatchTime time.Time
	currentGoalValidateTime time.Time
	lastSupervisorStatus    string
	phase                   phase
	phaseStartedAt          time.Time
	bootConfirmedAt         time.Time
	validatorSendDelay      time.Duration
	promptSettleDelay       time.Duration
	promptPollInterval      time.Duration
	ctx                     context.Context
	cancel                  context.CancelFunc
	currentGoal             string
	exitFunc                func(int)
	signalCh                chan os.Signal
	scriptRunnerFn          ScriptRunnerFunc
	scriptTimeout           time.Duration
	// autoResumeInterval paces resumeDownstreamLoop, the §5 background poll that
	// re-evaluates precondition-blocked goals. Independent of pollInterval; seeded
	// from taskvisor.auto_resume_interval_sec (default 30s) at construction/Run.
	autoResumeInterval time.Duration

	// idleTicks / stallReported are the stall watchdog's only writable state
	// (diagnostics, see checkStall). idleTicks counts consecutive ticks that
	// failed to dispatch despite a runnable candidate; stallReported gates the
	// STUCK: line to once per episode. Both zero-valued by default (no New change)
	// and reset on dispatch/dispatchRetry/deactivate.
	idleTicks     int
	stallReported bool
}

func New(workDir string, executor tmux.TmuxExecutor) *Daemon {
	return &Daemon{
		workDir:         workDir,
		executor:        executor,
		mode:            modeIdle,
		pollInterval:    10 * time.Second,
		dispatchTimeout: time.Hour,
		// validateTimeout is intentionally left zero-valued here. It is the
		// single authoritative finalization point clampValidateTimeout() in
		// Run() that sets the effective deadline (derived from the worker
		// budget). The zero value is never observed as a live deadline: the
		// only read (the watchdog around taskvisor.go:1044) runs inside the
		// poll loop, which Run() reaches only after the clamp has executed.
		validatorSendDelay: 2 * time.Second,
		// promptSettleDelay/promptPollInterval pace waitForPrompt against a real
		// tmux pane. Injectable so tests can zero them (the mock returns the
		// prompt synchronously); production keeps the settle + poll cadence.
		promptSettleDelay:  3 * time.Second,
		promptPollInterval: 2 * time.Second,
		scriptRunnerFn:     defaultScriptRunner,
		scriptTimeout:      validateScriptTimeout,
		autoResumeInterval: 30 * time.Second,
	}
}

func defaultScriptRunner(ctx context.Context, scriptPath, dir string, env []string) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, scriptPath)
	cmd.Env = env
	cmd.Dir = dir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
		}
		return outBuf.String(), errBuf.String(), -1, runErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
}

func goalDuration(goal *Goal) string {
	if goal.StartedAt == "" || goal.FinishedAt == "" {
		return ""
	}
	start, err := time.Parse(time.RFC3339, goal.StartedAt)
	if err != nil {
		return ""
	}
	end, err := time.Parse(time.RFC3339, goal.FinishedAt)
	if err != nil {
		return ""
	}
	return end.Sub(start).Round(time.Second).String()
}

func phaseName(p phase) string {
	switch p {
	case phaseSupervising:
		return "supervising"
	case phaseValidating:
		return "validating"
	default:
		return "idle"
	}
}

func (d *Daemon) SetWindowCreateFunc(fn WindowCreateFunc) {
	d.createWindowFn = fn
}

func (d *Daemon) SetScriptRunnerFunc(fn ScriptRunnerFunc) {
	d.scriptRunnerFn = fn
}

func (d *Daemon) runValidateScript(goal *Goal) (passed bool, stderr string, err error) {
	goalDir := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID)
	scriptPath := filepath.Join(goalDir, "validate.sh")

	info, statErr := os.Stat(scriptPath)
	if statErr != nil {
		return false, "", nil
	}
	if info.Mode().Perm()&0o111 == 0 {
		log.Printf("warning: validate.sh exists but is not executable for goal %s", goal.ID)
		return false, "", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.scriptTimeout)
	defer cancel()

	env := append(os.Environ(), "GOAL_ID="+goal.ID)
	_, stderrOut, exitCode, runErr := d.scriptRunnerFn(ctx, scriptPath, d.workDir, env)
	if runErr != nil {
		log.Printf("error: validate.sh exec error for goal %s: %v", goal.ID, runErr)
		return false, "", nil
	}

	if exitCode == 0 {
		return true, "", nil
	}

	if len(stderrOut) > 500 {
		stderrOut = stderrOut[:500]
	}
	return false, stderrOut, nil
}

func (d *Daemon) hasValidateMd(goalID string) bool {
	mdPath := filepath.Join(d.workDir, ".tmux-cli", "goals", goalID, "validate.md")
	_, err := os.Stat(mdPath)
	return err == nil
}

func (d *Daemon) Run(ctx context.Context) error {
	logDir := filepath.Join(d.workDir, ".tmux-cli", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logFile, err := os.OpenFile(filepath.Join(logDir, "taskvisor.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	settings, err := setup.LoadSettings(d.workDir)
	if err != nil {
		log.Printf("warning: failed to load settings: %v", err)
	} else {
		if settings.Taskvisor.PollInterval > 0 {
			d.pollInterval = time.Duration(settings.Taskvisor.PollInterval) * time.Second
		}
		if settings.Taskvisor.DispatchTimeout > 0 {
			d.dispatchTimeout = time.Duration(settings.Taskvisor.DispatchTimeout) * time.Second
		}
		if settings.Taskvisor.ValidateTimeout > 0 {
			d.validateTimeout = time.Duration(settings.Taskvisor.ValidateTimeout) * time.Second
		}
		if settings.Taskvisor.AutoResumeIntervalSec > 0 {
			d.autoResumeInterval = time.Duration(settings.Taskvisor.AutoResumeIntervalSec) * time.Second
		}
	}

	// Single authoritative finalization of d.validateTimeout. Runs UNCONDITIONALLY
	// (even when LoadSettings failed above, in which case d.validateTimeout is the
	// zero value from New()) and only ever raises the value to the derived minimum.
	maxWorkers := setup.DefaultMaxWorkers
	if settings != nil && settings.Supervisor.MaxWorkers > 0 {
		maxWorkers = settings.Supervisor.MaxWorkers
	}
	d.clampValidateTimeout(maxWorkers)

	d.setupSignalHandler(ctx)
	defer d.cancel()

	if err := d.crashRecovery(); err != nil {
		log.Printf("crash recovery error: %v", err)
	}

	// §5 background auto-resume: re-evaluates precondition-blocked goals on its own
	// cadence. Reuses d.ctx (cancelled by setupSignalHandler) so it exits cleanly on
	// shutdown — no second cancel, no leaked goroutine.
	go d.resumeDownstreamLoop(d.ctx)

	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return nil
		case <-ticker.C:
			if err := d.poll(d.ctx); err != nil {
				log.Printf("poll error: %v", err)
			}
			if err := d.renderDashboard(os.Stdout); err != nil {
				log.Printf("dashboard render error: %v", err)
			}
		}
	}
}

// clampValidateTimeout is the single authoritative finalization seam for
// d.validateTimeout. It raises (never lowers) the effective timeout to the
// minimum derived from the per-worker budget, logging when it does so.
//
// It floors at one full parallel wave (workerCount = maxWorkers ⇒ ceil(max/max)=1
// ⇒ 660s for the default budget): the daemon cannot know how many workers a
// future validation spawns, so validations expecting more waves rely on a higher
// configured validate_timeout, which this clamp will not lower.
func (d *Daemon) clampValidateTimeout(maxWorkers int) {
	derivedMin := time.Duration(setup.DeriveValidateTimeout(setup.WorkerBudgetSec, maxWorkers, maxWorkers)) * time.Second
	if d.validateTimeout < derivedMin {
		old := d.validateTimeout
		d.validateTimeout = derivedMin
		log.Printf("validate_timeout %v below derived minimum %v; clamping up", old, derivedMin)
	}
}

func (d *Daemon) withGoalsLock(fn func() error) error {
	return WithGoalsLock(d.workDir, fn)
}

func (d *Daemon) poll(ctx context.Context) error {
	return d.withGoalsLock(func() error {
		switch d.mode {
		case modeIdle:
			startPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-start")
			if _, err := os.Stat(startPath); err != nil {
				return nil
			}
			if err := os.Remove(startPath); err != nil {
				return fmt.Errorf("remove start signal: %w", err)
			}

			goals, err := LoadGoals(d.workDir)
			if err != nil {
				return fmt.Errorf("load goals: %w", err)
			}
			if goals == nil {
				return fmt.Errorf("no goals.yaml found")
			}

			return d.activate(goals)

		case modeActive:
			goals, err := LoadGoals(d.workDir)
			if err != nil {
				return fmt.Errorf("load goals: %w", err)
			}
			if goals == nil {
				return nil
			}
			return d.tick(ctx, goals)
		}
		return nil
	})
}

func (d *Daemon) tick(ctx context.Context, goals *GoalsFile) error {
	current, ok := goals.GoalByID(goals.CurrentGoal)
	if !ok {
		return d.deactivate()
	}

	// Heal stale block-state before acting on it (Bug A + self-recovery on load).
	// This MUST self-persist here at the tick top: most ticks hit
	// GoalRunning->checkProgress->checkSupervising/ValidatingPhase, which return
	// nil WITHOUT a SaveGoals, so the reconcile mutation would be discarded when
	// the flock releases. Do NOT snapshot current.Status before this call —
	// reconcile mutates the same &goals.Goals[i] element GoalByID aliased, so a
	// re-pended current_goal self-dispatches in the switch below.
	if goals.ReconcileBlocks() {
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
	}

	// Diagnostics-only guardrails, run strictly AFTER reconcile (the only point
	// where the invariant should hold) and the current-goal fetch, BEFORE the
	// dispatch switch. Neither mutates goal state nor alters the dispatch decision
	// below — checkInvariant only logs, checkStall only advances its own counter.
	d.checkInvariant(goals)
	d.checkStall(goals)

	switch current.Status {
	case GoalPending:
		if goals.RetryCeilingReached() {
			return d.haltRetryCeiling(current, goals)
		}
		// Re-dispatch the implementer (reuse tasks.yaml, skip planning) only when a
		// prior cycle consumed code-defect budget. CodeRetries is decremented per
		// code-defect (handleFailedCycle); a spec-defect bounce leaves CodeRetries
		// untouched and only moves SpecRetries, so it falls through to the full
		// dispatch (planner re-generation) below — that is how "code-defect ->
		// implementer" and "spec-defect -> generation" stay distinct now that the
		// legacy goal.Retries is read-only. (goal.Retries > 0 kept for fixtures /
		// pre-migration goals that still carry a legacy count.)
		codeBudgetConsumed := current.CodeRetries < current.MaxCodeRetries
		if (current.Retries > 0 || codeBudgetConsumed) && d.tasksYamlExists() {
			return d.dispatchRetry(current, goals)
		}
		return d.dispatch(current, goals)
	case GoalRunning:
		return d.checkProgress(current, goals)
	case GoalBlocked:
		// A precondition-parked current_goal is NOT terminal work — advance to the
		// next runnable peer so independent goals don't starve while the park waits
		// for scanPreconditionBlocked to un-park it. Mirrors the GoalDone/GoalFailed
		// advance branch, EXCEPT: when nothing is dispatchable we idle (return nil),
		// never deactivateOnCompletion — a parked current_goal is outstanding work.
		next, hasNext := goals.NextPendingGoal()
		if !hasNext {
			return nil // parked current_goal still outstanding; idle, stay active
		}
		goals.CurrentGoal = next.ID
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		if goals.RetryCeilingReached() {
			return d.haltRetryCeiling(next, goals)
		}
		return d.dispatch(next, goals)
	case GoalDone, GoalFailed:
		next, hasNext := goals.NextPendingGoal()
		if !hasNext {
			return d.deactivateOnCompletion(goals)
		}
		goals.CurrentGoal = next.ID
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		if goals.RetryCeilingReached() {
			return d.haltRetryCeiling(next, goals)
		}
		return d.dispatch(next, goals)
	}
	return nil
}

// checkInvariant logs the literal Bug-A incident signature: a non-terminal goal
// still BlockedBy an id whose goal is GoalDone, post-reconcile. After M1's
// ReconcileBlocks runs this should be unreachable, so a hit is a reconcile
// regression. Diagnostics only — it NEVER mutates Status/BlockedBy/budgets and
// never touches dispatch. Excludes legitimate holds (precondition park, the
// convergence-circuit-breaker sentinel) and only flags BlockedBy values that
// name a real goal whose Status==GoalDone.
func (d *Daemon) checkInvariant(goals *GoalsFile) {
	var ids []string
	for i := range goals.Goals {
		g := &goals.Goals[i]
		switch g.Status {
		case GoalDone, GoalFailed, GoalRunning:
			continue
		}
		if g.BlockedByPrecondition {
			continue
		}
		if g.BlockedBy == "convergence-circuit-breaker" {
			continue
		}
		if g.BlockedBy == "" {
			continue
		}
		if goals.statusOf(g.BlockedBy) == GoalDone {
			ids = append(ids, g.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	const maxShown = 10
	n := len(ids)
	shown := ids
	suffix := ""
	if n > maxShown {
		shown = ids[:maxShown]
		suffix = fmt.Sprintf(" (+%d more)", n-maxShown)
	}
	log.Printf("INVARIANT VIOLATION: %d goal(s) blocked by a done goal post-reconcile: %s%s",
		n, strings.Join(shown, ", "), suffix)
}

// checkStall is the stall watchdog: it logs a single STUCK: line when the daemon
// stays idle for stallWatchdogTicks consecutive ticks while a runnable candidate
// exists — the silent-deadlock signature. A worker mid-flight (AnyRunning) or no
// runnable candidate at all is legitimate, so the counter resets and never fires
// in those cases. Its only writable state is d.idleTicks/d.stallReported;
// dispatch/dispatchRetry/deactivate also reset them, so a normally-dispatching
// tick increments then resets within the same tick (net 0). One STUCK: per
// episode (gated by stallReported); a later dispatch/deactivate clears the flag,
// allowing a fresh episode.
func (d *Daemon) checkStall(goals *GoalsFile) {
	candidates := goals.RunnableCandidates()
	if goals.AnyRunning() || len(candidates) == 0 {
		d.idleTicks = 0
		d.stallReported = false
		return
	}
	d.idleTicks++
	if d.idleTicks >= stallWatchdogTicks && !d.stallReported {
		ids := make([]string, len(candidates))
		for i, g := range candidates {
			ids[i] = g.ID
		}
		log.Printf("STUCK: daemon idle %d ticks with %d runnable goal(s): %s",
			d.idleTicks, len(candidates), strings.Join(ids, ", "))
		d.stallReported = true
	}
}

func (d *Daemon) activate(goals *GoalsFile) error {
	guardPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-active")
	if err := os.MkdirAll(filepath.Dir(guardPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(guardPath, nil, 0o644); err != nil {
		return err
	}

	settings, err := setup.LoadSettings(d.workDir)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	if !settings.Plan.AutoApprove || !settings.Plan.AutoExecute {
		settings.Plan.AutoApprove = true
		settings.Plan.AutoExecute = true
		if err := setup.SaveSettings(d.workDir, settings); err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
	}

	// Heal stale block-state on (re)activation too, so a daemon that comes up
	// against an already-stuck goals.yaml re-pends a recovered subtree before it
	// looks for the next pending goal. Persist a heal even when no pending goal
	// is selected (the NextPendingGoal block below only saves when one is found).
	reconciled := goals.ReconcileBlocks()

	if g, ok := goals.NextPendingGoal(); ok {
		goals.CurrentGoal = g.ID
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
	} else if reconciled {
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
	}

	sessionID, err := d.discoverSession()
	if err != nil {
		return err
	}
	d.session = sessionID

	if err := d.killWindowByName("supervisor"); err != nil {
		return err
	}
	if err := d.killWindowsByPrefix("execute-"); err != nil {
		return err
	}
	if err := d.killWindowByName("validator"); err != nil {
		return err
	}
	if err := d.killWindowsByPrefix("inv-"); err != nil {
		return err
	}

	d.mode = modeActive
	if err := d.renderDashboard(os.Stdout); err != nil {
		log.Printf("dashboard render error: %v", err)
	}
	return nil
}

func (d *Daemon) deactivate() error {
	if err := d.killWindowByName("supervisor"); err != nil {
		return err
	}
	if err := d.killWindowsByPrefix("execute-"); err != nil {
		return err
	}
	if err := d.killWindowByName("validator"); err != nil {
		return err
	}
	if err := d.killWindowsByPrefix("inv-"); err != nil {
		return err
	}

	allNames := d.collectManagedNames()
	if err := d.waitWindowsGone(allNames, 5*time.Second); err != nil {
		log.Printf("warning: waitWindowsGone: %v", err)
	}

	if _, err := d.createWindow("supervisor", ""); err != nil {
		return fmt.Errorf("create supervisor: %w", err)
	}

	if err := d.waitClaudeBoot("supervisor", 30*time.Second); err != nil {
		log.Printf("warning: waitClaudeBoot: %v", err)
	}

	guardPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-active")
	_ = os.Remove(guardPath)

	// Deactivation closes any open stall episode (watchdog reset).
	d.idleTicks = 0
	d.stallReported = false
	d.mode = modeIdle
	if err := d.renderDashboard(os.Stdout); err != nil {
		log.Printf("dashboard render error: %v", err)
	}
	return nil
}

// preconditionDialTimeout bounds the TCP reachability probe for service
// preconditions. Kept short so a real outage does not stall the poll loop;
// transient-retry/backoff is a separate concern (C4), not handled here.
const preconditionDialTimeout = 2 * time.Second

// evaluatePreconditions checks a goal's declared preconditions before dispatch.
// An empty slice is all-pass. It short-circuits on the first failure, returning
// the failure class (env-config|infra-flake|spec-defect) and the failing
// precondition's remedy. env: unset OR empty fails. service: a TCP dial to
// host:port that errors within the timeout fails. Any unknown kind fails as a
// spec defect.
func (d *Daemon) evaluatePreconditions(goal *Goal) (success bool, class, remedy string) {
	for _, p := range goal.Preconditions {
		switch p.Kind {
		case "env":
			if v, ok := os.LookupEnv(p.Spec); !ok || v == "" {
				return false, "env-config", p.Remedy
			}
		case "service":
			conn, err := net.DialTimeout("tcp", p.Spec, preconditionDialTimeout)
			if err != nil {
				return false, "infra-flake", p.Remedy
			}
			_ = conn.Close()
		default:
			return false, "spec-defect", p.Remedy
		}
	}
	return true, "", ""
}

// preconditionClass maps a precondition kind to its failure class.
func preconditionClass(kind string) string {
	switch kind {
	case "env":
		return "env-config"
	case "service":
		return "infra-flake"
	default:
		return "spec-defect"
	}
}

// ownerFor maps a failure class to the party responsible for remediation.
func ownerFor(class string) string {
	if class == "spec-defect" {
		return "planner"
	}
	return "ops"
}

// failingPreconditionSpec re-identifies the spec of the first precondition that
// produced the given (class, remedy) pair, so the block signal/log can name it.
// evaluatePreconditions short-circuits on the first failure, so this matches
// that same precondition without re-running the (possibly networked) check.
func failingPreconditionSpec(goal *Goal, class, remedy string) string {
	for _, p := range goal.Preconditions {
		if preconditionClass(p.Kind) == class && p.Remedy == remedy {
			return p.Spec
		}
	}
	return ""
}

// writeCycleMarker pre-creates the current cycle's goal-scoped research dir and
// writes the .tmux-cli/taskvisor-current-cycle marker (sibling of
// taskvisor-current-goal) so investigate.xml's step-0b resolution can locate
// research/cycle-<N>/ for the current dispatch attempt. Idempotent; called on
// every (re-)dispatch BEFORE any worker (supervisor or validator) is spawned.
func (d *Daemon) writeCycleMarker(goal *Goal) error {
	if _, err := EnsureCycleResearchDir(d.workDir, goal); err != nil {
		return fmt.Errorf("ensure cycle research dir: %w", err)
	}
	markerPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-current-cycle")
	return os.WriteFile(markerPath, []byte(fmt.Sprintf("%d", CurrentCycle(goal))), 0o644)
}

func (d *Daemon) dispatch(goal *Goal, goals *GoalsFile) error {
	if err := d.writeDispatchMd(goal); err != nil {
		return fmt.Errorf("write dispatch.md: %w", err)
	}

	currentGoalPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-current-goal")
	if err := os.MkdirAll(filepath.Dir(currentGoalPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(currentGoalPath, []byte(goal.ID), 0o644); err != nil {
		return err
	}
	// C7: allocate the per-cycle research dir + write the current-cycle marker
	// BEFORE spawning any worker, so reports land under research/cycle-<N>/.
	if err := d.writeCycleMarker(goal); err != nil {
		return err
	}

	if err := d.killWindowByName("supervisor"); err != nil {
		return err
	}
	if err := d.killWindowsByPrefix("execute-"); err != nil {
		return err
	}
	if err := d.killWindowByName("validator"); err != nil {
		return err
	}
	if err := d.killWindowsByPrefix("inv-"); err != nil {
		return err
	}

	allNames := d.collectManagedNames()
	if err := d.waitWindowsGone(allNames, 5*time.Second); err != nil {
		return fmt.Errorf("waitWindowsGone: %w", err)
	}

	// Preflight precondition gate: never spawn a worker for a goal whose
	// declared environment is unmet. On a block we emit a blocked signal with a
	// class + remedy runbook, log an owner-facing line, mark the goal blocked
	// (which excludes it from pending selection, halting re-dispatch), and
	// return without spawning or consuming a retry.
	if ok, class, remedy := d.evaluatePreconditions(goal); !ok {
		owner := ownerFor(class)
		failingSpec := failingPreconditionSpec(goal, class, remedy)
		prefix := "[BLOCKED - OPERATOR ACTION REQUIRED]"
		if owner == "planner" {
			prefix = "[SPEC-DEFECT - GENERATOR ACTION REQUIRED]"
		}
		sig := &ValidatorSignal{
			Verdict: "blocked",
			Class:   class,
			Owner:   owner,
			Remedy:  remedy,
			Findings: []ValidationFinding{{
				Rule:   failingSpec,
				Status: "blocked",
				Detail: remedy,
			}},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		if err := SaveValidatorSignal(d.workDir, goal.ID, sig); err != nil {
			return fmt.Errorf("save block signal: %w", err)
		}
		log.Printf("%s %s: precondition %q failed — %s", prefix, goal.ID, failingSpec, remedy)
		goal.Status = GoalBlocked
		// env/infra precondition blocks are auto-resumable: flag the goal so the
		// resume loop (scanPreconditionBlocked, §5) re-evaluates its preconditions
		// and resumes it once they clear. A spec-defect (planner) block needs a
		// re-plan, not a re-check, so it is deliberately left unflagged.
		if owner == "ops" {
			goal.BlockedByPrecondition = true
		}
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return nil
	}

	winInfo, err := d.createWindow("supervisor", "")
	if err != nil {
		return fmt.Errorf("create supervisor: %w", err)
	}

	if err := d.waitClaudeBoot("supervisor", 30*time.Second); err != nil {
		return fmt.Errorf("waitClaudeBoot: %w", err)
	}

	log.Printf("dispatch: waitClaudeBoot done, waiting for prompt...")
	if err := d.waitForPrompt("supervisor", 30*time.Second); err != nil {
		log.Printf("warning: waitForPrompt: %v (proceeding anyway)", err)
	}
	log.Printf("dispatch: prompt detected, sending command")

	d.bootConfirmedAt = time.Now()
	oldPhase := d.phase
	d.phase = phaseSupervising
	log.Printf("%s: phase %s -> supervising", goal.ID, phaseName(oldPhase))

	dispatchPath := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID, "dispatch.md")
	planCmd := fmt.Sprintf("/tmux:plan %s", dispatchPath)
	log.Printf("dispatch: sending to session=%s window=%s cmd=%s", d.session, winInfo.TmuxWindowID, planCmd)
	if err := d.executor.SendMessage(d.session, winInfo.TmuxWindowID, planCmd); err != nil {
		return fmt.Errorf("send plan command: %w", err)
	}
	log.Printf("dispatch: SendMessage returned successfully")

	goal.Status = GoalRunning
	log.Printf("%s: pending -> running", goal.ID)
	if goal.StartedAt == "" {
		goal.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := SaveGoals(d.workDir, goals); err != nil {
		return err
	}

	// Successful dispatch ends the stall episode (watchdog reset).
	d.idleTicks = 0
	d.stallReported = false
	d.currentGoalDispatchTime = time.Now()
	d.lastSupervisorStatus = "dispatched"
	return nil
}

func (d *Daemon) tasksYamlExists() bool {
	_, err := os.Stat(filepath.Join(d.workDir, ".tmux-cli", "tasks.yaml"))
	return err == nil
}

func (d *Daemon) dispatchRetry(goal *Goal, goals *GoalsFile) error {
	if err := d.resetTaskStatuses(); err != nil {
		log.Printf("dispatchRetry: resetTaskStatuses failed, falling back to full dispatch: %v", err)
		return d.dispatch(goal, goals)
	}

	if err := d.injectCorrections(goal); err != nil {
		log.Printf("dispatchRetry: injectCorrections failed: %v", err)
	}

	if err := d.writeDispatchMd(goal); err != nil {
		return fmt.Errorf("write dispatch.md: %w", err)
	}

	currentGoalPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-current-goal")
	if err := os.MkdirAll(filepath.Dir(currentGoalPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(currentGoalPath, []byte(goal.ID), 0o644); err != nil {
		return err
	}
	// C7: allocate the per-cycle research dir + write the current-cycle marker
	// BEFORE spawning any worker, so reports land under research/cycle-<N>/.
	if err := d.writeCycleMarker(goal); err != nil {
		return err
	}

	if err := d.killWindowByName("supervisor"); err != nil {
		return err
	}
	if err := d.killWindowsByPrefix("execute-"); err != nil {
		return err
	}
	if err := d.killWindowByName("validator"); err != nil {
		return err
	}
	if err := d.killWindowsByPrefix("inv-"); err != nil {
		return err
	}

	allNames := d.collectManagedNames()
	if err := d.waitWindowsGone(allNames, 5*time.Second); err != nil {
		return fmt.Errorf("waitWindowsGone: %w", err)
	}

	winInfo, err := d.createWindow("supervisor", "")
	if err != nil {
		return fmt.Errorf("create supervisor: %w", err)
	}

	if err := d.waitClaudeBoot("supervisor", 30*time.Second); err != nil {
		return fmt.Errorf("waitClaudeBoot: %w", err)
	}

	log.Printf("dispatchRetry: waitClaudeBoot done, waiting for prompt...")
	if err := d.waitForPrompt("supervisor", 30*time.Second); err != nil {
		log.Printf("warning: waitForPrompt: %v (proceeding anyway)", err)
	}
	log.Printf("dispatchRetry: prompt detected, sending /tmux:supervisor (skip planning)")

	d.bootConfirmedAt = time.Now()
	oldPhase := d.phase
	d.phase = phaseSupervising
	log.Printf("%s: phase %s -> supervising (retry, skip plan)", goal.ID, phaseName(oldPhase))

	supervisorCmd := "/tmux:supervisor"
	log.Printf("dispatchRetry: sending to session=%s window=%s cmd=%s", d.session, winInfo.TmuxWindowID, supervisorCmd)
	if err := d.executor.SendMessage(d.session, winInfo.TmuxWindowID, supervisorCmd); err != nil {
		return fmt.Errorf("send supervisor command: %w", err)
	}
	log.Printf("dispatchRetry: SendMessage returned successfully")

	goal.Status = GoalRunning
	log.Printf("%s: pending -> running (retry %d/%d, reusing tasks.yaml)", goal.ID, goal.Retries, goal.MaxRetries)
	if goal.StartedAt == "" {
		goal.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := SaveGoals(d.workDir, goals); err != nil {
		return err
	}

	// Successful re-dispatch ends the stall episode (watchdog reset).
	d.idleTicks = 0
	d.stallReported = false
	d.currentGoalDispatchTime = time.Now()
	d.lastSupervisorStatus = "dispatched"
	return nil
}

func (d *Daemon) resetTaskStatuses() error {
	p := filepath.Join(d.workDir, ".tmux-cli", "tasks.yaml")
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}

	tasksRaw, ok := raw["tasks"].([]interface{})
	if !ok {
		return fmt.Errorf("tasks field not found or not a list")
	}

	for _, t := range tasksRaw {
		taskMap, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		taskMap["status"] = "pending"
	}

	raw["status"] = "ready"

	out, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	return atomicWrite(p, out, 0o644)
}

func (d *Daemon) injectCorrections(goal *Goal) error {
	// Read the corrections file handleFailedCycle wrote one cycle earlier. That
	// write used cycleNum = CurrentCycle(goal) computed BEFORE the CodeRetries
	// decrement; this code runs AFTER the decrement (consumed budget is one
	// higher), so the just-written file is cycle-(CurrentCycle(goal)-1).md. This
	// stays in lockstep with handleFailedCycle's unified numbering (C7) — in the
	// code-only path it equals the legacy (MaxCodeRetries - CodeRetries) value,
	// and it stays correct when a prior spec/validation defect also consumed
	// budget (where the legacy formula would read the wrong file).
	cycle := CurrentCycle(goal) - 1
	cycleFile := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID,
		"corrections", fmt.Sprintf("cycle-%d.md", cycle))
	correction, err := os.ReadFile(cycleFile)
	if err != nil {
		return nil
	}

	p := filepath.Join(d.workDir, ".tmux-cli", "tasks.yaml")
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}

	tasksRaw, ok := raw["tasks"].([]interface{})
	if !ok {
		return nil
	}

	for _, t := range tasksRaw {
		taskMap, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		ctxRel, ok := taskMap["context"].(string)
		if !ok || ctxRel == "" {
			continue
		}
		ctxPath := filepath.Join(d.workDir, ctxRel)
		existing, err := os.ReadFile(ctxPath)
		if err != nil {
			continue
		}
		amended := fmt.Sprintf("%s\n\n## Prior Corrections (Cycle %d)\n\n%s\n",
			strings.TrimRight(string(existing), "\n"), cycle, string(correction))
		if err := os.WriteFile(ctxPath, []byte(amended), 0o644); err != nil {
			log.Printf("injectCorrections: failed to write %s: %v", ctxPath, err)
		}
	}
	return nil
}

func (d *Daemon) createWindow(name, command string) (*CreatedWindow, error) {
	if d.createWindowFn != nil {
		return d.createWindowFn(name, command)
	}
	return nil, fmt.Errorf("no window create function configured")
}

func (d *Daemon) collectManagedNames() []string {
	allNames := []string{"supervisor", "validator"}
	windows, err := d.listWindows()
	if err == nil {
		for _, w := range windows {
			if strings.HasPrefix(w.Name, "execute-") || strings.HasPrefix(w.Name, "inv-") {
				allNames = append(allNames, w.Name)
			}
		}
	}
	return allNames
}

func (d *Daemon) killWindowByName(name string) error {
	windows, err := d.listWindows()
	if err != nil {
		return err
	}
	for _, w := range windows {
		if w.Name == name {
			return d.executor.KillWindow(d.session, w.TmuxWindowID)
		}
	}
	return nil
}

func (d *Daemon) killWindowsByPrefix(prefix string) error {
	windows, err := d.listWindows()
	if err != nil {
		return err
	}
	for _, w := range windows {
		if strings.HasPrefix(w.Name, prefix) {
			if err := d.executor.KillWindow(d.session, w.TmuxWindowID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Daemon) waitWindowsGone(names []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}

	for {
		windows, err := d.listWindows()
		if err != nil {
			return err
		}
		found := false
		for _, w := range windows {
			if nameSet[w.Name] {
				found = true
				break
			}
		}
		if !found {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for windows to disappear: %v", names)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (d *Daemon) waitForPrompt(windowName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	winInfo, err := d.findWindowByName(windowName)
	if err != nil {
		return nil
	}
	for {
		output, err := d.executor.CaptureWindowOutput(d.session, winInfo.TmuxWindowID)
		if err != nil {
			return nil
		}
		if strings.Contains(output, "❯") {
			time.Sleep(d.promptSettleDelay)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for prompt in %q", windowName)
		}
		time.Sleep(d.promptPollInterval)
	}
}

func (d *Daemon) findWindowByName(name string) (*tmux.WindowInfo, error) {
	windows, err := d.listWindows()
	if err != nil {
		return nil, err
	}
	for i := range windows {
		if windows[i].Name == name {
			return &windows[i], nil
		}
	}
	return nil, fmt.Errorf("window %q not found", name)
}

func (d *Daemon) waitClaudeBoot(windowName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		windows, err := d.listWindows()
		if err != nil {
			return err
		}
		found := false
		for _, w := range windows {
			if w.Name == windowName {
				found = true
				if w.CurrentCommand != "zsh" && w.CurrentCommand != "" {
					return nil
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("window %q not found", windowName)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for Claude boot in %q", windowName)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (d *Daemon) writeDispatchMd(goal *Goal) error {
	goalDir := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID)
	if err := os.MkdirAll(filepath.Join(goalDir, "corrections"), 0o755); err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString("# Dispatch: " + goal.Description + "\n\n")

	sb.WriteString("## Acceptance Criteria\n\n")
	goalMdPath := filepath.Join(goalDir, "goal.md")
	goalMdData, goalMdErr := os.ReadFile(goalMdPath)
	if goalMdErr == nil && strings.TrimSpace(string(goalMdData)) != "" {
		sb.WriteString(string(goalMdData))
		if !strings.HasSuffix(string(goalMdData), "\n") {
			sb.WriteString("\n")
		}
	} else if len(goal.Acceptance) > 0 {
		for _, a := range goal.Acceptance {
			sb.WriteString("- " + a + "\n")
		}
	} else {
		sb.WriteString("(none specified)\n")
	}

	sb.WriteString("\n## Prior Corrections\n\n")
	correctionsDir := filepath.Join(goalDir, "corrections")
	entries, err := os.ReadDir(correctionsDir)
	if err != nil || len(entries) == 0 {
		sb.WriteString("None (first attempt)\n")
	} else {
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		hasCorrections := false
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(correctionsDir, e.Name()))
			if err != nil {
				continue
			}
			sb.WriteString("### " + e.Name() + "\n\n")
			sb.WriteString(string(data) + "\n\n")
			hasCorrections = true
		}
		if !hasCorrections {
			sb.WriteString("None (first attempt)\n")
		}
	}

	dispatchPath := filepath.Join(goalDir, "dispatch.md")
	return os.WriteFile(dispatchPath, []byte(sb.String()), 0o644)
}

func (d *Daemon) discoverSession() (string, error) {
	sessionID, err := d.executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", d.workDir)
	if err != nil {
		return "", fmt.Errorf("find session: %w", err)
	}
	if sessionID == "" {
		return "", fmt.Errorf("no tmux-cli session found for %s", d.workDir)
	}
	return sessionID, nil
}

func (d *Daemon) listWindows() ([]tmux.WindowInfo, error) {
	if d.session == "" {
		return nil, nil
	}
	return d.executor.ListWindows(d.session)
}

func (d *Daemon) setupSignalHandler(parentCtx context.Context) {
	d.ctx, d.cancel = context.WithCancel(parentCtx)
	d.signalCh = make(chan os.Signal, 1)
	signal.Notify(d.signalCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		defer signal.Stop(d.signalCh)
		select {
		case <-d.signalCh:
			d.cancel()
			exists, err := d.executor.HasSession(d.session)
			if err == nil && exists {
				d.deactivate()
			} else {
				guardPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-active")
				_ = os.Remove(guardPath)
			}
			if d.exitFunc != nil {
				d.exitFunc(0)
			} else {
				os.Exit(0)
			}
		case <-d.ctx.Done():
			return
		}
	}()
}

func (d *Daemon) crashRecovery() error {
	guardPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-active")
	if _, err := os.Stat(guardPath); os.IsNotExist(err) {
		return nil
	}

	sessionID, err := d.discoverSession()
	if err != nil {
		log.Printf("crash recovery: no session found: %v", err)
		_ = os.Remove(guardPath)
		return nil
	}
	d.session = sessionID

	goals, err := LoadGoals(d.workDir)
	if err != nil || goals == nil {
		log.Printf("crash recovery: invalid goals.yaml: %v", err)
		return d.deactivate()
	}

	var runningGoal *Goal
	for i := range goals.Goals {
		if goals.Goals[i].Status == GoalRunning {
			runningGoal = &goals.Goals[i]
			break
		}
	}

	if runningGoal == nil {
		return d.deactivate()
	}

	d.mode = modeActive
	d.currentGoal = runningGoal.ID
	goals.CurrentGoal = runningGoal.ID

	sig, err := LoadSignal(d.workDir, runningGoal.ID)
	if err != nil {
		log.Printf("crash recovery: failed to read signal for %s: %v", runningGoal.ID, err)
	}
	if sig != nil {
		switch sig.(type) {
		case *SupervisorSignal:
			d.phase = phaseSupervising
		case *ValidatorSignal:
			d.phase = phaseValidating
		}
		d.phaseStartedAt = time.Now()
		return nil
	}

	windows, err := d.executor.ListWindows(d.session)
	if err != nil {
		return err
	}

	hasValidator := false
	for _, w := range windows {
		if w.Name == "validator" || strings.HasPrefix(w.Name, "inv-") {
			hasValidator = true
		}
	}
	if hasValidator {
		d.phase = phaseValidating
		d.phaseStartedAt = time.Now()
		log.Printf("crash recovery: validator/investigator window found, resuming validating phase")
		return nil
	}

	log.Printf("crash recovery: re-dispatching %s (supervisor state unknown after crash)", runningGoal.ID)
	if runningGoal.Retries < runningGoal.MaxRetries {
		runningGoal.Status = GoalPending
	} else {
		runningGoal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		runningGoal.Status = GoalFailed
	}
	return SaveGoals(d.workDir, goals)
}

func (d *Daemon) checkProgress(goal *Goal, goals *GoalsFile) error {
	switch d.phase {
	case phaseSupervising:
		return d.checkSupervisingPhase(goal, goals)
	case phaseValidating:
		return d.checkValidatingPhase(goal, goals)
	}
	return nil
}

func (d *Daemon) checkSupervisingPhase(goal *Goal, goals *GoalsFile) error {
	sig, err := LoadSignal(d.workDir, goal.ID)
	if err != nil {
		return fmt.Errorf("load signal: %w", err)
	}

	if sig == nil {
		if !d.currentGoalDispatchTime.IsZero() && time.Since(d.currentGoalDispatchTime) >= d.dispatchTimeout {
			return d.handleFailedCycle(goal, goals, "Cycle timed out — no completion signal received.", "code-defect")
		}
		if !d.bootConfirmedAt.IsZero() && time.Since(d.bootConfirmedAt) > 5*time.Second {
			windows, err := d.listWindows()
			if err != nil {
				return err
			}
			for _, w := range windows {
				if w.Name == "supervisor" && w.CurrentCommand == "zsh" {
					return d.handleFailedCycle(goal, goals, "Crash detected — supervisor returned to shell.", "code-defect")
				}
			}
		}
		return nil
	}

	supSig, ok := sig.(*SupervisorSignal)
	if !ok {
		_ = DeleteSignal(d.workDir, goal.ID)
		return d.handleFailedCycle(goal, goals, "Unexpected signal type during supervising phase.", "code-defect")
	}

	d.lastSupervisorStatus = supSig.Status
	if err := DeleteSignal(d.workDir, goal.ID); err != nil {
		return fmt.Errorf("delete signal: %w", err)
	}

	if err := d.killWindowsByPrefix("execute-"); err != nil {
		return err
	}
	if err := d.killWindowByName("supervisor"); err != nil {
		return err
	}

	passed, stderr, err := d.runValidateScript(goal)
	if err != nil {
		return fmt.Errorf("validate script: %w", err)
	}
	if passed {
		if err := SaveValidatorSignal(d.workDir, goal.ID, &ValidatorSignal{
			Verdict:   "pass",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			return err
		}
		goal.Status = GoalDone
		goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		log.Printf("%s: running -> done (%s)", goal.ID, goalDuration(goal))
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.advanceToNextGoal(goals, goal.ID)
	}
	if stderr != "" && !d.hasValidateMd(goal.ID) {
		return d.handleFailedCycle(goal, goals, stderr, "code-defect")
	}

	if err := d.createValidatorAndSendPayload(goal); err != nil {
		return err
	}

	log.Printf("%s: phase supervising -> validating", goal.ID)
	d.phase = phaseValidating
	d.currentGoalValidateTime = time.Now()
	return nil
}

func (d *Daemon) checkValidatingPhase(goal *Goal, goals *GoalsFile) error {
	sig, err := LoadSignal(d.workDir, goal.ID)
	if err != nil {
		return fmt.Errorf("load signal: %w", err)
	}

	if sig == nil {
		if !d.currentGoalValidateTime.IsZero() && time.Since(d.currentGoalValidateTime) >= d.validateTimeout {
			// The validator never reported before the deadline. This is a
			// validator error (verdict=error / owner=ops), NOT a code defect —
			// synthesize it and re-run validation only. Do not route through
			// handleFailedCycle, which would charge a code-defect retry.
			log.Printf("%s: validation timed out — no verdict; synthesizing error/ops, re-running validation only", goal.ID)
			return d.rerunValidationOnly(goal, goals)
		}
		if !d.bootConfirmedAt.IsZero() && time.Since(d.bootConfirmedAt) > 5*time.Second {
			windows, err := d.listWindows()
			if err != nil {
				return err
			}
			for _, w := range windows {
				if w.Name == "validator" && w.CurrentCommand == "zsh" {
					return d.handleFailedCycle(goal, goals, "Crash detected — validator returned to shell.", "code-defect")
				}
			}
		}
		return nil
	}

	valSig, ok := sig.(*ValidatorSignal)
	if !ok {
		_ = DeleteSignal(d.workDir, goal.ID)
		return d.handleFailedCycle(goal, goals, "Unexpected signal type during validating phase.", "code-defect")
	}

	if err := DeleteSignal(d.workDir, goal.ID); err != nil {
		return fmt.Errorf("delete signal: %w", err)
	}

	if err := d.killWindowByName("validator"); err != nil {
		return err
	}

	// Route by the rolled-up verdict CLASS (C1's ClassifyVerdict), not a binary
	// pass/fail. ClassifyVerdict folds the per-finding classes into a single
	// (verdict, owner) pair; when the validator carried a top-level verdict but
	// no classifiable finding (e.g. a synthesized error/blocked), fall back to
	// that reported verdict — and its reported owner — so a non-pass verdict
	// never misclassifies as pass and the blocked owner-split still works.
	verdict, owner := ClassifyVerdict(valSig.Findings)
	if verdict == VerdictPass && valSig.Verdict != "" && valSig.Verdict != VerdictPass {
		verdict = valSig.Verdict
		if owner == "" {
			owner = valSig.Owner
		}
	}

	// An unsubstantiated spec-defect (blocked/planner with no concretely-cited
	// contradiction) is a validator failure, not a planner failure — re-validate
	// (bounded by ValidationRetries) rather than burning the single SpecRetries
	// and cascading. Mirrors the timeout (:1248) and empty-verdict-synth routes.
	// A genuine, concretely-cited spec defect still falls through to the
	// blocked/planner case below and charges SpecRetries.
	if verdict == VerdictBlocked && owner == "planner" && !HasSubstantiveSpecDefect(valSig.Findings) {
		log.Printf("%s: blocked/planner verdict carries no substantive spec contradiction — re-validating (not charging spec retry)", goal.ID)
		return d.rerunValidationOnly(goal, goals)
	}

	// Each non-pass branch moves exactly one per-class budget counter (or none):
	//   fail            -> implementer re-dispatch, dec CodeRetries
	//   blocked+planner -> bounce to generation (re-plan), dec SpecRetries
	//   blocked+ops     -> park + runbook (env/infra hold), NO budget (§5 resumes)
	//   error           -> re-run validation only, dec ValidationRetries
	switch {
	case verdict == VerdictPass:
		goal.Status = GoalDone
		goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		log.Printf("%s: running -> done (%s)", goal.ID, goalDuration(goal))
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.advanceToNextGoal(goals, goal.ID)
	case verdict == VerdictFail:
		// Code defect — the implementer must fix it. Charges CodeRetries only.
		return d.handleFailedCycle(goal, goals, valSig.NextAction, "code-defect")
	case verdict == VerdictBlocked && owner == "planner":
		// Spec defect — re-plan via the generation slot. Charges SpecRetries only.
		return d.bounceToGeneration(goal, goals, valSig)
	case verdict == VerdictBlocked:
		// Env/infra precondition (owner=ops) — park + runbook, NO budget. The
		// auto-resume loop is owned by §5; this branch never starts it.
		return d.haltBlockedEnv(goal, goals, valSig)
	case verdict == VerdictError:
		// The validator could not run. Re-validate only — never a code defect.
		return d.rerunValidationOnly(goal, goals)
	default:
		// Defensive: C1's enum is closed and its leaf-4 catch-all maps unknowns to
		// (fail, implementer), so this should not occur. Treat as a code defect.
		return d.handleFailedCycle(goal, goals, valSig.NextAction, "code-defect")
	}
}

// rerunValidationOnly handles a validator-error verdict: the validator itself
// could not run, or never reported before the deadline. It re-runs validation
// ONLY — it decrements ValidationRetries (the REMAINING validation budget) and
// re-creates the validator window without touching CodeRetries/SpecRetries and
// without re-dispatching the implementer (that would be the handleFailedCycle
// code-defect path). This is the single bounded error/ops route shared by both
// the caller-reported empty-verdict synthesis (MCP GoalValidationDone), the
// never-reported timeout watchdog, and the error branch of checkValidatingPhase
// (C1 introduced an unbounded reRunValidation; this consolidation makes it
// bounded so a wedged validator can no longer re-spawn forever). When the
// validation budget reaches 0 the goal hard-halts (verdict class "error" — kept
// available for §5.1's CascadeFailure wiring; CascadeFailure's signature change
// is owned by §5.1).
func (d *Daemon) rerunValidationOnly(goal *Goal, goals *GoalsFile) error {
	goal.ValidationRetries--
	if goal.ValidationRetries <= 0 {
		log.Printf("%s: validation budget exhausted (error/ops) — cascading failure to dependents", goal.ID)
		// Exhausted validation budget is a hard halt: the goal genuinely cannot
		// complete, so dependents are blocked (hard class "fail").
		goals.CascadeFailure(goal.ID, "fail")
		goal.Status = GoalFailed
		goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		log.Printf("%s: running -> failed (%s)", goal.ID, goalDuration(goal))
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.advanceToNextGoal(goals, "")
	}
	log.Printf("%s: validator error (error/ops) — re-running validation only (validation budget left %d)", goal.ID, goal.ValidationRetries)
	if err := SaveGoals(d.workDir, goals); err != nil {
		return err
	}
	// Tear down any stale validator before re-creating one. In the timeout path
	// the previous validator is still alive; in the verdict path it has already
	// been killed (killWindowByName is then a no-op).
	if err := d.killWindowByName("validator"); err != nil {
		return err
	}
	if err := d.createValidatorAndSendPayload(goal); err != nil {
		return err
	}
	d.phase = phaseValidating
	d.currentGoalValidateTime = time.Now()
	return nil
}

// writeCorrectionFile emits the per-cycle correction the implementer reads on
// re-dispatch. For every NON-pass finding it writes a structured block
// (### Finding / Command / Output / Expected / Correction) so the full failure
// detail flows VERBATIM through writeDispatchMd + injectCorrections — never
// collapsed to a one-liner. When the signal carries no non-pass findings it
// falls back to NextAction (which the call site primes with the daemon framing
// header) so the file is never empty.
func (d *Daemon) writeCorrectionFile(goalDir string, cycleNum int, signal *ValidatorSignal) error {
	correctionsDir := filepath.Join(goalDir, "corrections")
	if err := os.MkdirAll(correctionsDir, 0o755); err != nil {
		return err
	}

	var sb strings.Builder
	wrote := false
	if signal != nil {
		for _, f := range signal.Findings {
			if f.Status == VerdictPass {
				continue
			}
			fmt.Fprintf(&sb, "### Finding: %s\n", f.Rule)
			fmt.Fprintf(&sb, "Command: %s\n", f.FailingCommand)
			fmt.Fprintf(&sb, "Output: %s\n", f.OutputExcerpt)
			fmt.Fprintf(&sb, "Expected: %s\n", f.ExpectedState)
			fmt.Fprintf(&sb, "Correction: %s\n\n", f.Correction)
			wrote = true
		}
	}
	if !wrote {
		fallback := ""
		if signal != nil {
			fallback = strings.TrimSpace(signal.NextAction)
		}
		if fallback == "" {
			fallback = "Implementation failed acceptance criteria — re-check the goal acceptance and fix."
		}
		sb.WriteString(fallback)
	}

	filename := fmt.Sprintf("cycle-%d.md", cycleNum)
	return os.WriteFile(filepath.Join(correctionsDir, filename), []byte(sb.String()), 0o644)
}

// hasNonPassFindings reports whether the signal carries at least one non-pass
// finding (i.e. writeCorrectionFile will emit structured per-finding blocks
// rather than the NextAction fallback).
func hasNonPassFindings(sig *ValidatorSignal) bool {
	if sig == nil {
		return false
	}
	for _, f := range sig.Findings {
		if f.Status != VerdictPass {
			return true
		}
	}
	return false
}

// circuitBreakerK returns the configured convergence circuit-breaker threshold
// (consecutive identical-signature cycles before halting). It reads
// Taskvisor.CircuitBreakerK from setting.yaml; a missing/invalid (<1) value
// falls back to the documented default of 2.
func (d *Daemon) circuitBreakerK() int {
	s, err := setup.LoadSettings(d.workDir)
	if err != nil || s == nil || s.Taskvisor.CircuitBreakerK < 1 {
		return 2
	}
	return s.Taskvisor.CircuitBreakerK
}

// equalSorted reports whether two ALREADY-sorted string slices are element-wise
// equal. ComputeSignatures returns ascending-sorted slices and the stored
// Goal.ConvergenceSignatures is likewise the previously-computed sorted set, so
// a positional comparison is a correct set-equality test here.
func equalSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// handleFailedCycle is the code-defect route: the implementer must fix the
// reported defect. It writes the correction file, decrements CodeRetries (the
// REMAINING code-defect budget) toward zero, and either re-dispatches the
// implementer (budget remaining) or hard-halts (budget exhausted). verdictClass
// names the failure class ("code-defect") and is kept in scope at the
// CascadeFailure call site so §5.1 can wire CascadeFailure(goal.ID,
// verdictClass) — CascadeFailure's signature change is owned by §5.1, not here.
//
// reason is the correction text surfaced to the implementer on the next cycle;
// it is retained as a distinct parameter (not collapsed into verdictClass) so
// the correction file keeps the actionable remediation guidance.
func (d *Daemon) handleFailedCycle(goal *Goal, goals *GoalsFile, reason, verdictClass string) error {
	goalDir := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID)
	// Cycle number is the unified CurrentCycle(goal) (consumed per-class budget +
	// 1) so the corrections file (cycle-N.md) and the per-cycle research dir
	// (research/cycle-N/) share ONE source of truth — changing one without the
	// other reintroduces drift (C7). Computed BEFORE the CodeRetries decrement
	// below, so cycle K's corrections land in cycle-K.md and the subsequent retry
	// (after the decrement) allocates cycle-(K+1).
	cycleNum := CurrentCycle(goal)

	var header string
	if d.lastSupervisorStatus == "stopped" {
		header = "Previous cycle hit the supervisor cycle limit — work is incomplete. Prioritize the unmet criteria below over polish or cleanup."
	} else {
		header = "Implementation completed but failed acceptance criteria."
	}

	// Load the validator signal so the correction file carries full per-finding
	// detail (failing_command/output_excerpt/expected_state/correction) VERBATIM.
	// On the timeout/crash routes no signal exists yet — synthesize one from the
	// reason. When the signal has no structured findings, fold the daemon framing
	// header into NextAction so the fallback branch keeps the supervisor-context
	// line the implementer relies on; when findings ARE present, the structured
	// blocks carry the detail and take precedence.
	loaded, loadErr := LoadSignal(d.workDir, goal.ID)
	if loadErr != nil {
		log.Printf("%s: handleFailedCycle: load signal: %v (using reason fallback)", goal.ID, loadErr)
	}
	valSig, _ := loaded.(*ValidatorSignal)
	if valSig == nil {
		valSig = &ValidatorSignal{}
	}
	if !hasNonPassFindings(valSig) {
		detail := strings.TrimSpace(valSig.NextAction)
		if detail == "" {
			detail = strings.TrimSpace(reason)
		}
		valSig.NextAction = header + "\n\n" + detail
	}

	// C6 convergence circuit-breaker — checked BEFORE the CodeRetries decrement.
	// If this cycle's non-pass findings produce the SAME sorted signature set as
	// the prior failed cycle (persisted on the goal), the goal is not converging:
	// burning the rest of the code budget on an identical failure only wastes
	// cycles a human must eventually unblock. On K consecutive identical sets we
	// halt to blocked/owner=human REGARDLESS of remaining budget, WITHOUT
	// decrementing any counter and WITHOUT re-dispatching. The breaker fires only
	// on signature recurrence, never on budget exhaustion (that stays the
	// GoalFailed cascade below). An empty set (no non-pass findings, e.g. the
	// timeout/crash routes) never fires.
	cur := ComputeSignatures(valSig.Findings)
	k := d.circuitBreakerK()
	streak := 1
	if len(cur) > 0 && equalSorted(cur, goal.ConvergenceSignatures) {
		streak = goal.ConvergenceStreak + 1
	}
	goal.ConvergenceSignatures = cur
	goal.ConvergenceStreak = streak
	if len(cur) > 0 && streak >= k {
		goal.Status = GoalBlocked
		goal.BlockedBy = "convergence-circuit-breaker"
		if err := SaveValidatorSignal(d.workDir, goal.ID, &ValidatorSignal{
			Verdict:    VerdictBlocked,
			Owner:      "human",
			Findings:   valSig.Findings,
			Signatures: cur,
			NextAction: strings.TrimSpace(reason),
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			return err
		}
		log.Printf("%s: running -> blocked (circuit-breaker, streak=%d/%d)", goal.ID, streak, k)
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.advanceToNextGoal(goals, "")
	}

	if err := d.writeCorrectionFile(goalDir, cycleNum, valSig); err != nil {
		return err
	}

	// Decrement-toward-zero: CodeRetries is the REMAINING code-defect budget.
	// Legacy goal.Retries stays read-only post-migration (never incremented here).
	goal.CodeRetries--
	if goal.CodeRetries <= 0 {
		// Code budget exhausted — hard halt. verdictClass is in scope here (C2
		// threads it from ClassifyVerdict) and is a hard class ("fail"/"code-defect"),
		// so dependents are blocked.
		goals.CascadeFailure(goal.ID, verdictClass)
		log.Printf("%s: code budget exhausted (%s) — cascading failure to dependents", goal.ID, verdictClass)
		goal.Status = GoalFailed
		goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		log.Printf("%s: running -> failed (%s)", goal.ID, goalDuration(goal))
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.advanceToNextGoal(goals, "")
	}

	goal.Status = GoalPending
	log.Printf("%s: running -> pending (code budget left %d)", goal.ID, goal.CodeRetries)
	oldPhase := d.phase
	d.phase = phaseSupervising
	log.Printf("%s: phase %s -> supervising", goal.ID, phaseName(oldPhase))
	return SaveGoals(d.workDir, goals)
}

// bounceToGeneration is the spec-defect route (verdict=blocked, owner=planner):
// the goal/spec itself is contradictory or under-specified, so it must be
// re-planned via the generation/planner slot — NOT re-run against the unchanged
// spec by the implementer. It charges SpecRetries only (decrement-toward-zero)
// and leaves CodeRetries untouched; because tick() routes a pending dispatch to
// dispatchRetry only when code budget was consumed, an untouched CodeRetries
// makes the next dispatch a full planner re-generation. On exhausted spec budget
// it hard-halts (verdict class "spec-defect").
func (d *Daemon) bounceToGeneration(goal *Goal, goals *GoalsFile, valSig *ValidatorSignal) error {
	goalDir := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID)
	cycleNum := goal.MaxSpecRetries - goal.SpecRetries + 1
	if cycleNum < 1 {
		cycleNum = 1
	}
	// Spec-defect bounce. The framing header is the planner-facing context +
	// remediation; the live valSig.Findings carry the REAL per-finding detail
	// (which rule, what contradiction, the remedy) the re-generation must fix.
	const framing = "Validation reports a SPEC DEFECT (owner: PLANNER). The current spec is contradictory or under-specified — regenerate/repair the plan; do NOT re-run the implementer against the unchanged spec.\n\nBounce to generation: regenerate the goal plan to resolve the spec defect before re-implementing."
	// Prime the NextAction fallback so a findingless bounce still writes the
	// framing header; forward the validator's findings when present (a
	// synthesized/never-reported bounce may pass nil → header-only fallback).
	sig := &ValidatorSignal{NextAction: framing}
	if valSig != nil {
		sig.Findings = valSig.Findings
	}
	if err := d.writeCorrectionFile(goalDir, cycleNum, sig); err != nil {
		return err
	}
	// writeCorrectionFile's XOR suppresses the NextAction framing once structured
	// findings are rendered. Prepend the framing header ABOVE the rendered blocks
	// so the planner gets both the context and the concrete detail. (No non-pass
	// findings → the fallback already wrote the framing; skip the read-modify-write.)
	if hasNonPassFindings(sig) {
		correctionPath := filepath.Join(goalDir, "corrections", fmt.Sprintf("cycle-%d.md", cycleNum))
		body, err := os.ReadFile(correctionPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(correctionPath, []byte(framing+"\n\n"+string(body)), 0o644); err != nil {
			return err
		}
	}

	// Decrement-toward-zero: SpecRetries only. CodeRetries/ValidationRetries are
	// left untouched (one counter per verdict).
	goal.SpecRetries--
	if goal.SpecRetries <= 0 {
		// Spec budget exhausted — hard halt. An unrepairable spec leaves dependents
		// genuinely blocked, so cascade with the hard class "fail".
		goals.CascadeFailure(goal.ID, "fail")
		log.Printf("%s: spec budget exhausted (spec-defect) — cascading failure to dependents", goal.ID)
		goal.Status = GoalFailed
		goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		log.Printf("%s: running -> failed (%s)", goal.ID, goalDuration(goal))
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.advanceToNextGoal(goals, "")
	}

	goal.Status = GoalPending
	goal.Phase = "generation"
	log.Printf("%s: spec defect (planner) — bouncing to generation (spec budget left %d)", goal.ID, goal.SpecRetries)
	oldPhase := d.phase
	d.phase = phaseSupervising
	log.Printf("%s: phase %s -> supervising (generation re-dispatch)", goal.ID, phaseName(oldPhase))
	return SaveGoals(d.workDir, goals)
}

// haltBlockedEnv is the env/infra route (verdict=blocked, owner=ops): a
// precondition (missing secret, unreachable service) is unmet. It charges NO
// budget — re-running anything cannot clear an environmental block — and parks
// the goal after writing an operator runbook and emitting an operator-facing log
// line. The polling auto-resume loop (resumeDownstreamLoop / BlockedByPrecondition)
// is owned by §5 and is deliberately NOT started here; this branch only parks +
// notifies and lets §5 resume the goal once the precondition clears.
func (d *Daemon) haltBlockedEnv(goal *Goal, goals *GoalsFile, valSig *ValidatorSignal) error {
	goalDir := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID)

	remedy := valSig.Remedy
	if remedy == "" {
		remedy = valSig.NextAction
	}
	if remedy == "" {
		remedy = "Resolve the missing environment/infrastructure precondition, then resume the goal."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Runbook — %s blocked (env/infra)\n\n", goal.ID)
	b.WriteString("Owner: **ops**. This is an environment/infrastructure precondition, not a code or spec defect. No retry budget was charged; the goal is parked until the precondition clears (auto-resume is handled by §5).\n\n")
	fmt.Fprintf(&b, "## Remedy\n\n%s\n", remedy)
	if len(valSig.Findings) > 0 {
		b.WriteString("\n## Blocking findings\n\n")
		for _, f := range valSig.Findings {
			if f.Status == VerdictPass {
				continue
			}
			fmt.Fprintf(&b, "- [%s/%s] %s — %s\n", f.Status, f.FailureClass, f.Rule, f.Detail)
		}
	}
	if err := os.WriteFile(filepath.Join(goalDir, "runbook.md"), []byte(b.String()), 0o644); err != nil {
		return err
	}

	// notify(log): operator-facing, mirrors the preflight precondition-gate line.
	log.Printf("[BLOCKED - OPERATOR ACTION REQUIRED] %s: env/infra precondition unmet — %s (parked; no budget charged, §5 auto-resumes)", goal.ID, remedy)

	// SOFT cascade: an env/infra hold is transient, so dependents must NOT be
	// hard-blocked — they stay GoalPending with BlockedBy recorded and resume
	// automatically once this goal's precondition clears and it completes. Derive
	// the soft class from the validator signal (env-config / infra-flake); default
	// to env-config. This is the ONE soft CascadeFailure call site.
	softClass := valSig.Class
	if softClass != "env-config" && softClass != "infra-flake" {
		softClass = "env-config"
	}
	goals.CascadeFailure(goal.ID, softClass)

	goal.Status = GoalBlocked
	goal.BlockedBy = "env_precondition"
	// Flag for the §5 auto-resume loop: scanPreconditionBlocked re-evaluates this
	// goal's preconditions on each tick and resumes it when they pass.
	goal.BlockedByPrecondition = true
	// C-1: persist the precondition-classified signal so §5's resume gate recognizes
	// this park. Stamp the resolved soft class onto valSig first (it may be empty),
	// then persist BEFORE SaveGoals so the goal is never parked without its signal —
	// on a persist error, return early.
	valSig.Class = softClass
	if err := SaveValidatorSignal(d.workDir, goal.ID, valSig); err != nil {
		return err
	}
	return SaveGoals(d.workDir, goals)
}

func (d *Daemon) haltRetryCeiling(goal *Goal, goals *GoalsFile) error {
	log.Printf("%s: retry ceiling reached (total retries: %d), halting", goal.ID, goals.TotalRetries())
	goal.Status = GoalFailed
	goal.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	// Ceiling = exhausted retries → hard fail; downstream genuinely blocked.
	goals.CascadeFailure(goal.ID, "fail")
	if err := SaveGoals(d.workDir, goals); err != nil {
		return err
	}
	return d.advanceToNextGoal(goals, "")
}

// advanceToNextGoal selects the next dispatchable goal after the current one
// reaches a terminal state. resumeDoneID names the goal that just reached
// GoalDone (empty for failure/halt callers): when non-empty it first runs
// resumeDownstream to clear the BlockedBy hold on that goal's soft-held
// dependents, so a completed upstream immediately unblocks its pending subtree
// in the SAME locked critical section (the caller — poll — already holds the
// goals lock). Failure callers pass "" because CascadeFailure already settled
// their dependents (hard-block) and resuming would be wrong.
func (d *Daemon) advanceToNextGoal(goals *GoalsFile, resumeDoneID string) error {
	if resumeDoneID != "" {
		d.resumeDownstream(goals, resumeDoneID)
	}
	next, hasNext := goals.NextPendingGoal()
	if !hasNext {
		return d.deactivateOnCompletion(goals)
	}
	goals.CurrentGoal = next.ID
	return SaveGoals(d.workDir, goals)
}

// clearBlock lifts every hold flag from a goal: the BlockedBy upstream pointer
// and the BlockedByPrecondition env/infra flag. It NEVER touches status or any
// retry counter — the caller decides the resulting status (always GoalPending on
// the resume paths). Centralizing the clear keeps the two hold fields in lock-step.
func (d *Daemon) clearBlock(g *Goal) {
	g.BlockedBy = ""
	g.BlockedByPrecondition = false
}

// resumeDownstream is the SYNCHRONOUS resume path, called from advanceToNextGoal
// when an upstream goal reaches GoalDone. For every goal still GoalPending whose
// BlockedBy points at the just-completed doneGoalID, it clears the hold so the
// goal becomes dispatchable again (its dependency is now satisfied). It mutates
// the in-memory *GoalsFile in place; the caller (poll → advanceToNextGoal) already
// holds the goals lock and persists via SaveGoals, so this does NOT re-acquire the
// lock (doing so would deadlock the flock). It skips goals that are not pending or
// whose BlockedBy does not match doneGoalID — including the "deps_unsatisfied"
// sentinel, which is never a real goal ID — and it touches NO retry budget.
func (d *Daemon) resumeDownstream(goals *GoalsFile, doneGoalID string) {
	for i := range goals.Goals {
		g := &goals.Goals[i]
		if g.Status != GoalPending || g.BlockedBy != doneGoalID {
			continue
		}
		d.clearBlock(g)
		log.Printf("%s: upstream %s done — cleared block, staying pending for re-validation", g.ID, doneGoalID)
	}
}

// resumeDownstreamLoop is the §5 background auto-resume poll. On every
// autoResumeInterval tick it re-evaluates precondition-blocked goals; it exits
// cleanly when ctx is cancelled (the daemon's shared ctx from setupSignalHandler),
// leaking no goroutine. The interval is read from a Daemon field so tests can set a
// tiny cadence; a non-positive value falls back to 30s.
func (d *Daemon) resumeDownstreamLoop(ctx context.Context) {
	interval := d.autoResumeInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.scanPreconditionBlocked()
		}
	}
}

// scanPreconditionBlocked re-evaluates every goal flagged BlockedByPrecondition,
// cross-checked against its latest signal.json class (env-config / infra-flake) so
// it never blindly re-probes unrelated goals. For a matching goal it re-runs C3's
// evaluatePreconditions; when ALL preconditions pass it clears the block, sets the
// goal GoalPending and lets the dispatch loop re-validate it (no retry budget is
// consumed). A goal whose preconditions still fail is left flagged for the next
// tick. All mutations run under WithGoalsLock to serialize against the dispatch
// loop and operator edits; this loop is a SEPARATE goroutine from poll, so the
// flock provides mutual exclusion (no re-entrancy, no deadlock).
func (d *Daemon) scanPreconditionBlocked() {
	err := d.withGoalsLock(func() error {
		goals, err := LoadGoals(d.workDir)
		if err != nil {
			return fmt.Errorf("load goals: %w", err)
		}
		if goals == nil {
			return nil
		}
		changed := false
		for i := range goals.Goals {
			g := &goals.Goals[i]
			if !g.BlockedByPrecondition {
				continue
			}
			if !d.preconditionParkEligible(g) {
				continue
			}
			ok, _, _ := d.evaluatePreconditions(g)
			if !ok {
				// Still failing — leave flagged, re-poll next tick.
				continue
			}
			d.clearBlock(g)
			g.Status = GoalPending
			changed = true
			log.Printf("%s: precondition cleared — resuming (blocked -> pending) for re-validation", g.ID)
		}
		if !changed {
			return nil
		}
		return SaveGoals(d.workDir, goals)
	})
	if err != nil {
		log.Printf("scanPreconditionBlocked: %v", err)
	}
}

// latestSignalIsPreconditionClass reports whether the goal's latest signal.json
// classifies as an env/infra precondition hold (env-config / infra-flake) — the
// cross-check that gates auto-resume. It accepts either the signal's top-level
// Class (set by the preflight gate) or a non-pass finding's FailureClass (set by
// the validator), so both block sources are recognized. A missing/unreadable or
// non-validator signal returns false (never auto-resume on ambiguous state).
func (d *Daemon) latestSignalIsPreconditionClass(goalID string) bool {
	loaded, err := LoadSignal(d.workDir, goalID)
	if err != nil || loaded == nil {
		return false
	}
	valSig, ok := loaded.(*ValidatorSignal)
	if !ok {
		return false
	}
	isPrecond := func(c string) bool { return c == "env-config" || c == "infra-flake" }
	if isPrecond(valSig.Class) {
		return true
	}
	for _, f := range valSig.Findings {
		if f.Status != VerdictPass && isPrecond(f.FailureClass) {
			return true
		}
	}
	return false
}

// preconditionParkEligible reports whether a BlockedByPrecondition goal should be
// re-evaluated by the §5 resume loop. It resumes on EITHER (a) a readable
// precondition-class signal, OR (b) an absent/unreadable signal when the daemon's
// own BlockedBy=="env_precondition" discriminator is set (the validation-route
// park that pre-fix never wrote a signal — recovers already-stranded goals). A
// readable NON-precondition signal returns false. LoadSignal is called directly
// here because latestSignalIsPreconditionClass conflates absent/unreadable with
// non-precondition (both false), so it cannot be negated to tell them apart.
func (d *Daemon) preconditionParkEligible(g *Goal) bool {
	loaded, err := LoadSignal(d.workDir, g.ID)
	if err != nil || loaded == nil {
		// (b) absent/unreadable — trust the daemon's own flag.
		return g.BlockedBy == "env_precondition"
	}
	// (a) signal present — resume only on a precondition class.
	return d.latestSignalIsPreconditionClass(g.ID)
}

func (d *Daemon) deactivateOnCompletion(goals *GoalsFile) error {
	// Never tear down while a resumable precondition park is outstanding: AllResolved
	// counts GoalBlocked as resolved, but a BlockedByPrecondition park has pending
	// work that scanPreconditionBlocked will re-pend, so deactivating here would
	// deadlock it permanently (nothing would re-dispatch). Keys ONLY on the flag, so
	// manual/external holds (no flag) still allow deactivation.
	if goals.HasResumablePark() {
		log.Printf("deactivate skipped: resumable precondition park outstanding — staying active")
		return nil
	}
	// Never tear down while a recoverable cascade block is outstanding: AllResolved
	// counts GoalBlocked as resolved, but a goal blocked behind a now-Done goal with
	// satisfied deps is recoverable work that ReconcileBlocks re-pends. Deactivating
	// here would strand the whole cascade subtree permanently (the distinct sibling
	// of the precondition park above). The caller (poll → tick) already holds the
	// goals flock, so call the lock-free ReconcileBlocks/SaveGoals directly as the
	// tick top and precondition path do. The next tick re-pends + dispatches the
	// un-stuck frontier; deactivation proceeds only once no recoverable frontier
	// remains.
	if goals.HasRecoverableBlock() {
		if goals.ReconcileBlocks() {
			if err := SaveGoals(d.workDir, goals); err != nil {
				return err
			}
		}
		log.Printf("deactivate skipped: recoverable cascade block(s) outstanding — reconciling and staying active")
		return nil
	}
	if !goals.AllResolved() {
		for i := range goals.Goals {
			g := &goals.Goals[i]
			if g.Status == GoalPending && !g.DependsOnSatisfied(goals.Goals) {
				log.Printf("%s: pending -> blocked (deps unsatisfied)", g.ID)
				g.Status = GoalBlocked
				g.BlockedBy = "deps_unsatisfied"
			}
		}
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
	}
	if err := d.generateCompletionReport(goals); err != nil {
		log.Printf("warning: completion report: %v", err)
	}

	if err := d.killWindowByName("supervisor"); err != nil {
		return err
	}
	if err := d.killWindowsByPrefix("execute-"); err != nil {
		return err
	}
	if err := d.killWindowByName("validator"); err != nil {
		return err
	}
	if err := d.killWindowsByPrefix("inv-"); err != nil {
		return err
	}

	allNames := d.collectManagedNames()
	if err := d.waitWindowsGone(allNames, 5*time.Second); err != nil {
		log.Printf("warning: waitWindowsGone: %v", err)
	}

	guardPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-active")
	_ = os.Remove(guardPath)

	// Deactivation closes any open stall episode (watchdog reset).
	d.idleTicks = 0
	d.stallReported = false
	d.mode = modeIdle
	if err := d.renderDashboard(os.Stdout); err != nil {
		log.Printf("dashboard render error: %v", err)
	}
	return nil
}

func (d *Daemon) generateCompletionReport(goals *GoalsFile) error {
	var done, failed, blocked int
	for _, g := range goals.Goals {
		switch g.Status {
		case GoalDone:
			done++
		case GoalFailed:
			failed++
		case GoalBlocked:
			blocked++
		}
	}
	total := len(goals.Goals)

	var buf strings.Builder
	buf.WriteString("# Taskvisor Completion Report\n\n")
	buf.WriteString(fmt.Sprintf("Generated: %s\n\n", time.Now().UTC().Format(time.RFC3339)))
	buf.WriteString("## Summary\n\n")
	buf.WriteString("| Status | Count |\n")
	buf.WriteString("|--------|-------|\n")
	buf.WriteString(fmt.Sprintf("| Done   | %d     |\n", done))
	buf.WriteString(fmt.Sprintf("| Failed | %d     |\n", failed))
	buf.WriteString(fmt.Sprintf("| Blocked| %d     |\n", blocked))
	buf.WriteString(fmt.Sprintf("| Total  | %d     |\n", total))
	buf.WriteString("\n## Goals\n\n")

	for _, g := range goals.Goals {
		buf.WriteString(fmt.Sprintf("### %s: %s\n", g.ID, g.Description))
		buf.WriteString(fmt.Sprintf("- **Status:** %s\n", g.Status))
		dur := goalDuration(&g)
		if dur != "" {
			buf.WriteString(fmt.Sprintf("- **Duration:** %s\n", dur))
		}
		buf.WriteString(fmt.Sprintf("- **Retries:** %d/%d\n\n", g.Retries, g.MaxRetries))
	}

	reportDir := filepath.Join(d.workDir, ".tmux-cli", "goals")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(reportDir, "completion-report.md"), []byte(buf.String()), 0o644)
}

func (d *Daemon) createValidatorAndSendPayload(goal *Goal) error {
	winInfo, err := d.createWindow("validator", "")
	if err != nil {
		return fmt.Errorf("create validator: %w", err)
	}

	if err := d.waitClaudeBoot("validator", 30*time.Second); err != nil {
		return fmt.Errorf("waitClaudeBoot validator: %w", err)
	}

	if err := d.waitForPrompt("validator", 30*time.Second); err != nil {
		log.Printf("warning: waitForPrompt validator: %v (proceeding anyway)", err)
	}

	d.bootConfirmedAt = time.Now()

	goalMdPath := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID, "goal.md")
	investigateCmd := fmt.Sprintf("/tmux:investigate %s", goalMdPath)
	if err := d.executor.SendMessage(d.session, winInfo.TmuxWindowID, investigateCmd); err != nil {
		return fmt.Errorf("send investigate command: %w", err)
	}

	return nil
}
