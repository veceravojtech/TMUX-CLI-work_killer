package taskvisor

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/console/tmux-cli/internal/producer"
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

	// lastProgressHash/lastProgressAt back the per-tick pane-output progress
	// heartbeat (P2). lastProgressHash is the FNV-1a digest of the supervisor/
	// validator pane at the last observation; lastProgressAt is the clock stamp
	// (via d.now()) of the last time the digest CHANGED. checkProgressHeartbeat
	// refreshes both on change and fires a stuck-recovery when the digest is
	// static for >= d.progressTimeout while the window is still the agent. The
	// zero value (empty hash, zero time) is "never observed" — the heartbeat
	// seeds it on first observation and never fires the same tick. clearRuntime's
	// map delete zeros these on goal exit; there is no in-place reset path.
	lastProgressHash string
	lastProgressAt   time.Time

	// scriptPassed records the validate.sh exit-0 result from checkSupervisingPhase,
	// threaded to checkValidatingPhase for GateTerminalPass. Zero-value false is the
	// correct default (no validate.sh ran, or runtime was cleared). NOT final within
	// a cycle: checkValidatingPhase's gate-time re-run (P7-fresh) refreshes it when
	// an LLM pass would otherwise be vetoed by a stale false.
	scriptPassed bool
	// scriptReason names why the LAST validate.sh observation did not pass
	// (runValidateScript's reason contract; empty when scriptPassed). Surfaced in
	// the P7 downgrade log so a gated pass is diagnosable without re-running.
	scriptReason string

	// WorktreeDir/Branch hold the per-goal git-worktree isolation state (E1-1a).
	// Set by ensureWorktree when MaxGoals>1 on a git repo; read by
	// mergeWorktreeBack/discardWorktree and by execute-35 (validate isolation).
	// Both stay EMPTY under MaxGoals=1 (and for a non-git repo), and every git
	// path short-circuits on the empty WorktreeDir, so single-goal operation makes
	// zero git calls and is byte-identical to the pre-worktree build.
	WorktreeDir string
	Branch      string

	// activatedAt is the PER-GOAL wall-clock budget epoch (P3). It is stamped once
	// per in-flight episode at the goal's first dispatch (both dispatch sites, under
	// an IsZero() guard so a redispatch within the same episode PRESERVES it and does
	// NOT extend the budget), and the tick() gate halts the goal when
	// d.now().Sub(activatedAt) >= d.maxWallClock. Each goal thus gets its own budget
	// window from ITS dispatch — goals running sequentially no longer share one
	// daemon timer. INVARIANT: the budget caps a goal's TOTAL in-flight wall time
	// across redispatch retries; clearRuntime's map delete zeros it on terminal exit,
	// so a fully re-pended goal correctly gets a fresh budget. ZERO VALUE = never
	// dispatched = NEVER halts (the gate skips IsZero epochs). Distinct from
	// dispatchTime (the dispatch-timeout epoch) so the two semantics can diverge.
	activatedAt time.Time
}

type Daemon struct {
	workDir        string
	executor       tmux.TmuxExecutor
	createWindowFn WindowCreateFunc
	// producer is the fire-and-forget backend reporter (goal-008). Nil = reporting
	// disabled (no API config / no signing key), in which case reportFailure is a
	// silent no-op. Initialized once in Run() after settings load via
	// producer.New(producer.LoadConfig(d.workDir)); reused verbatim, never an
	// interface. See reporting.go for the submission helpers.
	producer *producer.Client
	// reportedFailures is the in-memory dedup set for terminal goal-failure
	// reports (completion.go: reportFailedGoals). A goal ID is EAGERLY marked
	// when its GoalFailed report submission starts (deduping both repeated
	// deactivateOnCompletion passes and sweeps racing an in-flight async
	// submission) and cleared by the submit callback on error so the next sweep
	// retries. Lazily initialized on first use (nil-safe; no New() change).
	// Intentionally NOT persisted to goals.yaml — it resets on process restart,
	// where re-reporting from goals.yaml is acceptable and rare, and a schema
	// change is avoided.
	reportedFailures map[string]bool
	// reportedFailuresMu guards ALL reportedFailures access, including the lazy
	// map init: the clear-on-error callback runs on submitReport's goroutine,
	// concurrent with the tick loop's sweeps.
	reportedFailuresMu sync.Mutex
	mode               mode
	session            string
	pollInterval       time.Duration
	dispatchTimeout    time.Duration
	validateTimeout    time.Duration
	// progressTimeout bounds how long a dispatched supervisor/validator window may
	// emit NO new pane output before the per-tick heartbeat (checkProgressHeartbeat)
	// declares it wedged and recovers early — closing the silent-timeout hole where
	// a stuck LLM was invisible until the 1h hard dispatch/validate timeout. Seeded
	// to 5m by New() and overridable via taskvisor.progress_timeout_sec. A value
	// <=0 DISABLES the heartbeat entirely (the literal-Daemon legacy test harness is
	// then byte-identical to the pre-P2 build — no CaptureWindowOutput call).
	progressTimeout time.Duration
	// clock is the injectable time source for all deadline/interval MATH (the now()
	// accessor). Seeded to time.Now by New(); tests inject a controllable clock to
	// advance past progressTimeout deterministically. Nil ⇒ time.Now via now(), so
	// a literal Daemon never panics. Timestamp FORMATTING (.UTC().Format) stays on
	// time.Now() — only deadline math routes through the clock. P5/P3 reuse this seam.
	clock              func() time.Time
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
	// autoCommit gates the completion-time auto-commit step (autoCommitGoal):
	// on a goal's done transition the daemon commits the goal's scope-matched
	// changeset to the current branch. Seeded true by New() and overridden from
	// taskvisor.auto_commit (AutoCommitEnabled) in Run(); warn-only by contract —
	// a commit failure never alters goal status or daemon flow.
	autoCommit bool
	// autoPush gates the completion-time auto-push step (autoPushOnCompletion):
	// when a run finishes, the daemon runs one plain `git push` once to publish
	// the run's local commits. Default-OFF — the zero value is correct, so New()
	// does NOT seed it; Run() overrides it from taskvisor.auto_push. Warn-only by
	// contract — a push failure never alters goal status or daemon flow.
	autoPush bool
	// gitFreshness gates the pre-dispatch git-freshness preflight
	// (gitFreshnessGate): before a goal is dispatched the daemon fetches origin
	// and refuses to dispatch a diverged checkout. Zero-value false so a
	// direct-construct Daemon (dispatch unit tests) never touches the git runner;
	// seeded from taskvisor.git_freshness (GitFreshnessEnabled) ONLY in Run().
	gitFreshness bool
	// skipValidation disables the post-execution validation step: when true a goal
	// is marked done DIRECTLY out of the supervising phase (no validate.sh, no
	// validator windows) instead of being handed to the validator. Zero-value
	// false (validate as normal) so a direct-construct Daemon and every literal-
	// Daemon unit test keep the validating transition unchanged; seeded from the
	// INVERSE of taskvisor.validation (ValidationEnabled) ONLY in Run().
	skipValidation bool
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

	// invariantReported mirrors stallReported for the Bug-A invariant check
	// (checkInvariant): it gates the failure report to once per violation episode
	// so an every-tick check can never flood the backend. Set true when a
	// violation is reported; cleared in checkInvariant at the same len(ids)==0
	// early return that ends an episode. Zero-valued by default (no New change).
	invariantReported bool

	// activatedAt stamps when the daemon last entered modeActive (set in activate()
	// through the P2 clock seam d.now()). As of the per-goal-budget move (P3) it is
	// NO LONGER the wall-clock budget epoch — that now lives per-goal on
	// goalRuntime.activatedAt. This daemon-global field's SOLE remaining consumer is
	// the ALL-COMPLETE `wall=` run-total diagnostic (deactivateOnCompletion computes
	// d.now().Sub(d.activatedAt)); the notification tests assert that contract. Still
	// re-stamped on every activate() so the diagnostic measures the current run.
	activatedAt time.Time
	// maxWallClock is the daemon's wall-clock cost ceiling (P3). New() seeds it to
	// 4h (the DefaultSettings() value) so the ceiling is active even when a legacy
	// setting.yaml omits max_wall_clock_sec; a positive taskvisor.max_wall_clock_sec
	// overrides it in Run(). Zero ⇒ DISABLED (no halt ever fires): set explicitly in
	// tests, or by an operator writing max_wall_clock_sec: 0. Wall-clock is the chosen
	// proxy because token/$ spend is not observable by the daemon.
	maxWallClock time.Duration
	// haltReason, when non-empty, is the loud dashboard banner explaining a
	// daemon-level halt (currently only the wall-clock ceiling). Set BEFORE
	// deactivate() (whose tail render surfaces it) and cleared in activate() so a
	// (re)start shows a clean IDLE/ACTIVE surface.
	haltReason           string
	haltOnStaleBinary    bool
	restartOnStaleBinary bool
	restartAttempted     bool
	execReplaceFn        func(string, []string, []string) error
	// commandRefreshFn rewrites the installed .claude/commands/tmux/ templates from
	// the (new) binary's embedded FS when checkStaleBinary fires. Injected from
	// cmd/tmux-cli (where the embedded FS lives) so internal/taskvisor need not
	// import package main; nil ⇒ refreshCommands() is a no-op (literal-Daemon tests).
	commandRefreshFn   func() error
	vcsRevision        string
	lastStaleCheck     time.Time
	staleBanner        string
	specRepairs        int
	depWarningCount    int
	stackGateSkips     int
	execReplaceRestart bool
}

func readVCSRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return "dev"
}

func New(workDir string, executor tmux.TmuxExecutor) *Daemon {
	return &Daemon{
		workDir:         workDir,
		executor:        executor,
		mode:            modeIdle,
		vcsRevision:     readVCSRevision(),
		pollInterval:    10 * time.Second,
		dispatchTimeout: time.Hour,
		// clock/progressTimeout seed the P2 heartbeat. clock defaults to the real
		// wall clock; progressTimeout to 5m — 12× faster than the 1h hard timeout,
		// the smallest window that won't false-positive on a normal `go test` run.
		clock:           time.Now,
		progressTimeout: 5 * time.Minute,
		// maxWallClock seeds the P3 cost ceiling to 4h (mirroring progressTimeout's
		// 5m seed). Seeding here — not relying on Run()'s settings-load — is what
		// makes the ceiling reach a legacy setting.yaml that omits max_wall_clock_sec:
		// such a file loads MaxWallClockSec==0, so Run()'s `if >0` override is skipped
		// and this 4h seed stands. An explicit positive setting still overrides it in
		// Run(); tests that need it DISABLED set d.maxWallClock=0 after New().
		maxWallClock: 4 * time.Hour,
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
		execReplaceFn:      syscall.Exec,
		// autoCommit seeds ON (matching DefaultSettings) so the per-goal commit
		// boundary reaches a literal-Daemon run that never loads settings; Run()
		// overrides it from taskvisor.auto_commit unconditionally.
		autoCommit: true,
	}
}

func (d *Daemon) SetWindowCreateFunc(fn WindowCreateFunc) {
	d.createWindowFn = fn
}

func (d *Daemon) SetScriptRunnerFunc(fn ScriptRunnerFunc) {
	d.scriptRunnerFn = fn
}

func (d *Daemon) SetExecReplaceFnForTest(fn func(string, []string, []string) error) {
	d.execReplaceFn = fn
}

func (d *Daemon) SetCommandRefreshFn(fn func() error) {
	d.commandRefreshFn = fn
}

func (d *Daemon) Run(ctx context.Context) error {
	logDir := filepath.Join(d.workDir, ".tmux-cli", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logFile, err := os.OpenFile(filepath.Join(logDir, "taskvisor.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	pidPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0o644); err != nil {
		log.Printf("warning: write PID file: %v", err)
	}
	defer os.Remove(pidPath)

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
		if settings.Taskvisor.ProgressTimeoutSec > 0 {
			d.progressTimeout = time.Duration(settings.Taskvisor.ProgressTimeoutSec) * time.Second
		}
		// P7-fresh: per-execution validate.sh ceiling. <=0 keeps the New() seed
		// (validateScriptTimeout), mirroring the ProgressTimeoutSec convention.
		if settings.Taskvisor.ValidateScriptTimeoutSec > 0 {
			d.scriptTimeout = time.Duration(settings.Taskvisor.ValidateScriptTimeoutSec) * time.Second
		}
		// P3 wall-clock cost ceiling. Left zero (disabled) by New(); a positive
		// setting enables it. A <=0 value keeps it disabled (byte-identical no-op).
		if settings.Taskvisor.MaxWallClockSec > 0 {
			d.maxWallClock = time.Duration(settings.Taskvisor.MaxWallClockSec) * time.Second
		}
		d.haltOnStaleBinary = settings.Taskvisor.HaltOnStaleBinary
		d.restartOnStaleBinary = settings.Taskvisor.RestartOnStaleBinary
		d.autoCommit = settings.Taskvisor.AutoCommitEnabled()
		d.autoPush = settings.Taskvisor.AutoPush
		d.gitFreshness = settings.Taskvisor.GitFreshnessEnabled()
		// validation OFF ⇒ goals are marked done directly out of supervising. The
		// daemon field is the inverse so its zero value (false) means "validate".
		d.skipValidation = !settings.Taskvisor.ValidationEnabled()
	}

	// Backend failure reporting (goal-008/009). Config is read independently of
	// setup.Settings; a missing/disabled config yields a nil *producer.Client and
	// reportFailure degrades to a silent no-op. The goroutine in reportFailure reads
	// d.ctx at run time, so initializing here (before setupSignalHandler wires the
	// context) is safe — no submission can fire until the poll loop is live.
	cfg, _ := producer.LoadConfig(d.workDir)
	d.producer = producer.New(cfg)

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

	// The restart marker is read BEFORE crash recovery so a deliberate
	// exec-replace deploy is resumed (and announced) as a planned restart, not a
	// crash. It is consumed unconditionally afterwards; the auto-activate branch
	// still keys on the post-recovery mode (idle means nothing was in flight).
	restartMarker := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-restart")
	_, markerErr := os.Stat(restartMarker)
	plannedRestart := markerErr == nil

	if err := d.crashRecovery(plannedRestart); err != nil {
		log.Printf("crash recovery error: %v", err)
	}

	if plannedRestart {
		_ = os.Remove(restartMarker)
		if d.mode == modeIdle {
			log.Printf("exec-replace restart detected — auto-activating")
			d.execReplaceRestart = true
			startPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-start")
			_ = os.WriteFile(startPath, nil, 0o644)
		}
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
			d.renderBoard()
		}
	}
}

// clampValidateTimeout is the single authoritative finalization seam for
// d.validateTimeout. It raises (never lowers) the effective timeout to the
// minimum derived from the per-worker budget, logging when it does so.
//
// It floors at one full parallel wave (workerCount = maxWorkers ⇒ ceil(max/max)=1
// ⇒ 1260s for the default budget, incl. ValidatorOverheadSec): the daemon cannot know how many workers a
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

// now is the single chokepoint for deadline/interval MATH. It returns d.clock()
// when a clock is injected (tests advance it deterministically) and falls back to
// time.Now() when nil, so a literal Daemon (the legacy test harness) never panics.
// Timestamp FORMATTING (.UTC().Format) deliberately stays on time.Now() — only
// deadline arithmetic routes through here.
func (d *Daemon) now() time.Time {
	if d.clock != nil {
		return d.clock()
	}
	return time.Now()
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
// MaxGoals=1 this is cosmetic but keeps the map honest for execute-31. The map
// delete zeros ALL per-goal fields including the P2 progress-heartbeat state
// (lastProgressHash/lastProgressAt) — there is no in-place reset path, so a
// re-dispatched goal always re-seeds its heartbeat from scratch.
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
			if d.recurringPickup() {
				return nil
			}
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

	if settings.Taskvisor.RequirePlanApproval {
		approvalPath := filepath.Join(d.workDir, "docs", "architecture", "plan-approval.md")
		if _, err := os.Stat(approvalPath); os.IsNotExist(err) {
			d.haltReason = "HALTED: RequirePlanApproval is true but docs/architecture/plan-approval.md is absent — run /tmux:plan first (its blind audit gate writes the approval file)"
			return d.deactivate()
		}
	}

	// Prune worktrees orphaned by a crashed run BEFORE selecting/dispatching the
	// next goal: `git worktree prune` + remove .tmux-cli-worktrees/<id> dirs whose
	// goal is not GoalRunning. No-op with zero git when the worktrees dir is absent
	// (the MaxGoals=1 / never-parallel case), so activation stays byte-identical.
	d.pruneOrphanWorktrees(goals)

	// Heal stale block-state on (re)activation too, so a daemon that comes up
	// against an already-stuck goals.yaml re-pends a recovered subtree before it
	// looks for the next pending goal. Persist a heal even when no pending goal
	// is selected (the NextPendingGoal block below only saves when one is found).
	reconciled := goals.ReconcileBlocks()

	depFindings := InferMissingDeps(goals)
	d.depWarningCount = len(depFindings)
	for _, f := range depFindings {
		log.Printf("dep warning: %s references %s (produced by %s) without depends_on edge [evidence: %s]",
			f.Consumer, f.Stem, f.Producer, f.Evidence)
	}

	enforced := EnforceFileOverlapDeps(goals)
	for _, e := range enforced {
		log.Printf("dep enforce: %s now depends_on %s (file overlap: %s) — serialized pre-dispatch", e.From, e.To, e.Stem)
	}

	if g, ok := goals.NextPendingGoal(); ok {
		goals.CurrentGoal = g.ID
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
	} else if reconciled || len(enforced) > 0 {
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
	// the goal about to be dispatched; sweepGoalIDs returns [head] at MaxGoals<=1
	// and every in-flight goal namespace at MaxGoals>1. killGoalWindows targets the
	// per-goal namespaced names (supervisor-<ns> / execute-<ns>- / validator-<ns> /
	// inv-<ns>-), so the human's window-0 bare "supervisor" is NEVER swept.
	curGoal := goals.CurrentGoal
	if err := d.killGoalWindows(d.sweepGoalIDs(curGoal, allGoalIDs(goals))); err != nil {
		return err
	}

	// Stamp the daemon run-start epoch (P3) via the P2 clock seam and clear any
	// prior halt reason so a (re)start shows a clean surface. This stamp now feeds
	// ONLY the ALL-COMPLETE `wall=` run-total diagnostic — the wall-clock BUDGET
	// epoch moved per-goal onto goalRuntime.activatedAt (stamped at dispatch). Both
	// must precede the dashboard render below.
	d.activatedAt = d.now()
	d.haltReason = ""
	d.stackGateSkips = 0
	d.mode = modeActive
	if d.execReplaceRestart {
		d.execReplaceRestart = false
		d.notifySupervisor("[TASKVISOR:STATE exec-replace-restart]")
	}
	var pendingCount int
	for _, g := range goals.Goals {
		if g.Status == GoalPending {
			pendingCount++
		}
	}
	d.notifySupervisor(fmt.Sprintf("[TASKVISOR:STATE from=idle to=active goals=%d]", pendingCount))
	d.renderBoard()
	return nil
}

func (d *Daemon) cleanRuntimeMarkers() {
	tmuxDir := filepath.Join(d.workDir, ".tmux-cli")
	for _, name := range []string{"taskvisor-current-goal", "taskvisor-current-cycle", "taskvisor-current-worktree", "taskvisor-active"} {
		if err := os.Remove(filepath.Join(tmuxDir, name)); err != nil && !os.IsNotExist(err) {
			log.Printf("cleanRuntimeMarkers: remove %s: %v", name, err)
		}
	}
}

func (d *Daemon) deactivate() error {
	// currentGoal names the in-flight head (set on dispatch/crashRecovery; may be
	// empty on an idle-path deactivate). Tear down EVERY in-flight goal namespace
	// (the head plus every goal with a live runtime) so a sibling goal's windows
	// are never orphaned at MaxGoals>1. teardownGoalWindows targets only the
	// per-goal namespaced names, so the human's window-0 "supervisor" is never
	// touched by the sweep.
	curGoal := d.currentGoal
	var inflight []string
	for id := range d.runtimes {
		inflight = append(inflight, id)
	}
	if err := d.teardownGoalWindows(d.sweepGoalIDs(curGoal, inflight)); err != nil {
		return err
	}

	// Ensure the human's window-0 bare "supervisor" exists once the daemon goes
	// idle (supervisor.xml/standalone interaction lives here). We NEVER recreate a
	// namespaced supervisor-<ns> here — those are per-goal, spawned only by
	// dispatch — and we NEVER kill/recreate window-0 ([[never-kill-tmux-server-pid]]):
	// create bare "supervisor" only when no window by that name is live, else no-op.
	if err := d.ensureWindow0Supervisor(); err != nil {
		return err
	}

	d.cleanRuntimeMarkers()

	// Deactivation closes any open stall episode (watchdog reset) and drops every
	// per-goal runtime — no goal is in flight once the daemon is idle.
	d.idleTicks = 0
	d.stallReported = false
	d.runtimes = nil
	d.mode = modeIdle
	d.notifySupervisor("[TASKVISOR:STATE from=active to=idle]")
	d.renderBoard()
	return nil
}

// ensureWindow0Supervisor guarantees the human's bare "supervisor" window is live
// after the daemon goes idle, WITHOUT ever killing or recreating an existing one.
// In normal operation window-0 "supervisor" (the session's first window, UUID-
// stamped by session.Manager) is always present, so this is a no-op; it only
// creates a bare "supervisor" when none is live (e.g. a session started without
// it). It NEVER spawns a namespaced supervisor-<ns> — those are per-goal windows
// owned by dispatch. See [[never-kill-tmux-server-pid]].
func (d *Daemon) ensureWindow0Supervisor() error {
	windows, err := d.listWindows()
	if err != nil {
		return fmt.Errorf("list windows: %w", err)
	}
	for _, w := range windows {
		if w.Name == "supervisor" {
			return nil // window-0 already live — leave it untouched
		}
	}
	if _, err := d.createWindow("supervisor", "", ""); err != nil {
		return fmt.Errorf("create supervisor: %w", err)
	}
	if err := d.waitClaudeBoot("supervisor", 30*time.Second); err != nil {
		log.Printf("warning: waitClaudeBoot: %v", err)
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

// notifySupervisor targets the long-lived window-0 Claude pane, which can be
// mid-render or busy when the notification lands — an Enter fired immediately
// after the text gets grouped into the paste and swallowed, leaving the message
// sitting unsubmitted in the input box. SendMessageWithDelay's 1s gap before
// Enter defeats the paste grouping (same reason windows-message uses it).
func (d *Daemon) notifySupervisor(msg string) {
	win, err := d.findWindowByName("supervisor")
	if err != nil {
		log.Printf("notify: supervisor window not found, skipping: %v", err)
		return
	}
	if err := d.executor.SendMessageWithDelay(d.session, win.TmuxWindowID, msg); err != nil {
		log.Printf("notify: failed to send to supervisor: %v", err)
	}
}

func (d *Daemon) notifyCompletion(goals *GoalsFile) {
	win, err := d.findWindowByName("supervisor")
	if err != nil {
		log.Printf("notify: supervisor window not found, skipping completion notifications: %v", err)
		return
	}
	for _, g := range goals.Goals {
		if g.Status == GoalDone {
			dur := goalDuration(&g)
			if dur == "" {
				dur = "unknown"
			}
			if err := d.executor.SendMessageWithDelay(d.session, win.TmuxWindowID, fmt.Sprintf("[TASKVISOR:GOAL-DONE id=%s desc=%q duration=%s]", g.ID, g.Description, dur)); err != nil {
				log.Printf("notify: failed to send GOAL-DONE for %s: %v", g.ID, err)
			}
		}
	}
	var doneN, failedN, blockedN int
	for _, g := range goals.Goals {
		switch g.Status {
		case GoalDone:
			doneN++
		case GoalFailed:
			failedN++
		case GoalBlocked:
			blockedN++
		}
	}
	// d.activatedAt is the in-memory global run epoch; an exec-replace restart
	// resets it to zero (resume never re-stamps it), and now.Sub(zero) overflows
	// time.Duration to math.MaxInt64 — the ~292-year "wall=2562047h47m16.854775807s"
	// garbage. Guard it: recover the epoch from the persisted taskvisor-active
	// marker (still present here — cleanRuntimeMarkers runs after notifyCompletion),
	// else report "unknown" rather than the overflow value.
	wallStr := "unknown"
	if !d.activatedAt.IsZero() {
		wallStr = d.now().Sub(d.activatedAt).Round(time.Second).String()
	} else if info, statErr := os.Stat(filepath.Join(d.workDir, ".tmux-cli", "taskvisor-active")); statErr == nil {
		wallStr = d.now().Sub(info.ModTime()).Round(time.Second).String()
	}
	if err := d.executor.SendMessageWithDelay(d.session, win.TmuxWindowID, fmt.Sprintf("[TASKVISOR:ALL-COMPLETE done=%d failed=%d blocked=%d wall=%s]",
		doneN, failedN, blockedN, wallStr)); err != nil {
		log.Printf("notify: failed to send ALL-COMPLETE: %v", err)
	}
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
				d.cleanRuntimeMarkers()
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
