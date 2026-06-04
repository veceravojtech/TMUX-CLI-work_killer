package taskvisor

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/tmux"
)

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

// goalRuntime holds the per-goal in-flight cycle state hoisted off Daemon so
// goal-level parallelism (execute-31) can track each goal independently. These
// are the 7 genuinely per-goal fields the phase machine reads/writes; the
// remaining Daemon fields are immutable config or daemon-global diagnostics and
// stay inline. With MaxGoals=1 (the default until execute-31) exactly one entry
// ever exists and behavior is byte-identical to the prior single-valued fields.
// The zero value (phase == phaseNone, zero timers, empty status) mirrors the old
// zero-valued Daemon fields exactly, so a never-dispatched goal reads idle.
type goalRuntime struct {
	phase                phase
	phaseStartedAt       time.Time
	bootConfirmedAt      time.Time
	dispatchTime         time.Time
	validateTime         time.Time
	lastSupervisorStatus string

	// WorktreeDir/Branch hold the per-goal git-worktree isolation state (E1-1a).
	// Set by ensureWorktree when MaxGoals>1 on a git repo; read by
	// mergeWorktreeBack/discardWorktree and by execute-35 (validate isolation).
	// Both stay EMPTY under MaxGoals=1 (and for a non-git repo), and every git
	// path short-circuits on the empty WorktreeDir, so single-goal operation makes
	// zero git calls and is byte-identical to the pre-worktree build.
	WorktreeDir string
	Branch      string
}

type Daemon struct {
	workDir            string
	executor           tmux.TmuxExecutor
	createWindowFn     WindowCreateFunc
	mode               mode
	session            string
	pollInterval       time.Duration
	dispatchTimeout    time.Duration
	validateTimeout    time.Duration
	validatorSendDelay time.Duration
	promptSettleDelay  time.Duration
	promptPollInterval time.Duration
	ctx                context.Context
	cancel             context.CancelFunc
	// currentGoal is the compat pointer to the single active runtime key. It
	// mirrors goals.CurrentGoal (the persisted scalar) for the dashboard's
	// active-phase lookup and is the one source of "which goal is in flight" for
	// single-goal operation. Set on dispatch/redispatch and crashRecovery.
	currentGoal string
	// runtimes maps goal ID -> per-goal cycle state. Lazily populated via
	// runtime(); cleared via clearRuntime() when a goal leaves the in-flight set.
	runtimes       map[string]*goalRuntime
	exitFunc       func(int)
	signalCh       chan os.Signal
	scriptRunnerFn ScriptRunnerFunc
	scriptTimeout  time.Duration
	// gitRunnerFn is the injectable seam for every git invocation behind the
	// per-goal worktree lifecycle (E1-1a). Nil ⇒ defaultGitRunner (real git). With
	// MaxGoals=1 no git path is ever reached, so this is never invoked.
	gitRunnerFn GitRunnerFunc
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

	// finalGateStuckReported debounces the terminal final-gate STUCK: line to one
	// emission per episode (mirrors stallReported). Unlike the idle-tick path, the
	// final-gate branch in checkStall is AnyRunning-agnostic and self-clears in
	// checkStall: when FinalGateBlockedByFailed no longer matches (a candidate
	// appears, or the blocker leaves GoalFailed after `taskvisor goal reset`) the
	// flag resets — so it needs no entry at the dispatch/deactivate reset sites.
	finalGateStuckReported bool
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

func (d *Daemon) SetWindowCreateFunc(fn WindowCreateFunc) {
	d.createWindowFn = fn
}

func (d *Daemon) SetScriptRunnerFunc(fn ScriptRunnerFunc) {
	d.scriptRunnerFn = fn
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

// withDBLock runs fn while holding the shared-schema db lock, mirroring
// withGoalsLock. Lock order is goals→db: the daemon already holds the goals flock
// (via poll→tick→checkProgress) when it reaches the validate step this wraps, so
// db is always the inner lock and never acquired before goals.
func (d *Daemon) withDBLock(fn func() error) error {
	return WithDBLock(d.workDir, fn)
}

// runtime returns the per-goal runtime for goalID, lazily creating a zero-valued
// entry (phase == phaseNone, zero timers) on first access so callers never
// nil-check — the old zero-valued Daemon fields had identical semantics. The poll
// loop is single-threaded today, so no mutex is taken.
// execute-31: add sync when >1 goal runs concurrently.
func (d *Daemon) runtime(goalID string) *goalRuntime {
	if d.runtimes == nil {
		d.runtimes = make(map[string]*goalRuntime)
	}
	rt, ok := d.runtimes[goalID]
	if !ok {
		rt = &goalRuntime{}
		d.runtimes[goalID] = rt
	}
	return rt
}

// clearRuntime drops the per-goal runtime for goalID. Called when a goal leaves
// the in-flight set (advanceToNextGoal/deactivate) to bound map growth; with
// MaxGoals=1 this is cosmetic but keeps the map honest for execute-31.
func (d *Daemon) clearRuntime(goalID string) {
	delete(d.runtimes, goalID)
}

// goalWorkDir resolves the working directory a goal's VALIDATION must run in so
// the verdict observes ONLY that goal's edits (E1-1c). It is the single chokepoint
// for validate-cwd routing: both runValidateScript and createValidatorAndSendPayload
// (and, via the WORKTREE_DIR marker, the inv-* investigators) derive their cwd here,
// so there is no duplicated worktree-path logic.
//
//   - empty WorktreeDir (MaxGoals=1, or a non-git repo) ⇒ base d.workDir — the cwd
//     is byte-identical to the pre-worktree build and zero new behavior is added.
//   - WorktreeDir set and the directory exists ⇒ that worktree path.
//   - WorktreeDir set but the directory is missing (stale/raced cleanup) ⇒ degrade
//     to base d.workDir and log a warning. NEVER crash and NEVER run a goal's
//     validation against a tree that is gone.
//
// The script path and every .tmux-cli/ control-plane read/write stay rooted at base
// d.workDir; only the cwd of the executed validation commands moves here.
func (d *Daemon) goalWorkDir(goalID string) string {
	wt := d.runtime(goalID).WorktreeDir
	if wt == "" {
		return d.workDir
	}
	if _, err := os.Stat(wt); err != nil {
		log.Printf("warning: stale worktree %s for goal %s, using base", wt, goalID)
		return d.workDir
	}
	return wt
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

	// Prune worktrees orphaned by a crashed run BEFORE selecting/dispatching the
	// next goal: `git worktree prune` + remove .tmux-cli/worktrees/<id> dirs whose
	// goal is not GoalRunning. No-op with zero git when the worktrees dir is absent
	// (the MaxGoals=1 / never-parallel case), so activation stays byte-identical.
	d.pruneOrphanWorktrees(goals)

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

	// Startup cleanup of any leftover windows from a prior run. CurrentGoal names
	// the goal about to be dispatched; at MaxGoals<=1 sweepGoalIDs returns [head]
	// and the helpers return bare names (byte-identical to the pre-namespacing
	// sweep). At MaxGoals>1 we sweep EVERY goal namespace so leftovers from any
	// goal that was in flight in a prior run are cleared, not just the head's.
	curGoal := goals.CurrentGoal
	if err := d.killGoalWindows(d.sweepGoalIDs(curGoal, allGoalIDs(goals))); err != nil {
		return err
	}

	d.mode = modeActive
	if err := d.renderDashboard(os.Stdout); err != nil {
		log.Printf("dashboard render error: %v", err)
	}
	return nil
}

func (d *Daemon) deactivate() error {
	// currentGoal names the in-flight head (set on dispatch/crashRecovery; may be
	// empty on an idle-path deactivate). Tear down EVERY in-flight goal namespace
	// (the head plus every goal with a live runtime) so a sibling goal's windows
	// are never orphaned at MaxGoals>1. At MaxGoals<=1 sweepGoalIDs collapses to
	// [head] and the helpers return bare names, so this is byte-identical to the
	// pre-namespacing teardown + the idle supervisor window supervisor.xml expects.
	mg := d.maxGoals()
	curGoal := d.currentGoal
	var inflight []string
	for id := range d.runtimes {
		inflight = append(inflight, id)
	}
	if err := d.teardownGoalWindows(d.sweepGoalIDs(curGoal, inflight)); err != nil {
		return err
	}

	supWin := supervisorWindow(curGoal, mg)
	if _, err := d.createWindow(supWin, "", ""); err != nil {
		return fmt.Errorf("create supervisor: %w", err)
	}

	if err := d.waitClaudeBoot(supWin, 30*time.Second); err != nil {
		log.Printf("warning: waitClaudeBoot: %v", err)
	}

	guardPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-active")
	_ = os.Remove(guardPath)

	// Deactivation closes any open stall episode (watchdog reset) and drops every
	// per-goal runtime — no goal is in flight once the daemon is idle.
	d.idleTicks = 0
	d.stallReported = false
	d.runtimes = nil
	d.mode = modeIdle
	if err := d.renderDashboard(os.Stdout); err != nil {
		log.Printf("dashboard render error: %v", err)
	}
	return nil
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
