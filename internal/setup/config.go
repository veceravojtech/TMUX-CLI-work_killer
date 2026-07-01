package setup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type CustomHook struct {
	Event   string `yaml:"event"`
	Matcher string `yaml:"matcher,omitempty"`
	Command string `yaml:"command"`
	Timeout int    `yaml:"timeout"`
}

type HooksSettings struct {
	SessionNotify    bool         `yaml:"session_notify"`
	BlockInteractive bool         `yaml:"block_interactive"`
	Custom           []CustomHook `yaml:"custom,omitempty"`
}

type CommandsSettings struct {
	Enabled bool `yaml:"enabled"`
}

// APISettings configures the producer task-reporting side channel. It is read
// here so the api: block PERSISTS across the lossy SaveSettings round-trip
// (without a typed field, LoadSettings re-marshals and silently drops it). The
// producer package reads the SAME api.{enabled,url} keys independently via its
// own producer.LoadConfig (it never imports setup), so the two stay in sync by
// the shared yaml shape, not a Go dependency.
type APISettings struct {
	Enabled bool   `yaml:"enabled"`
	URL     string `yaml:"url"`
}

type SupervisorSettings struct {
	MaxCycles  int `yaml:"max_cycles"`
	MaxWorkers int `yaml:"max_workers"`
	// MaxGoals bounds how many goals the daemon may have in flight concurrently.
	// A value <=0 (or absent from a legacy setting.yaml) means "1" at runtime —
	// the daemon's maxGoals() accessor coerces it. At 1 every tmux window keeps
	// its bare singleton name (supervisor/validator/execute-/investigator-); >1 namespaces
	// each goal's windows so concurrent goals never collide (execute-31 wiring).
	MaxGoals        int  `yaml:"max_goals"`
	CycleDelay      int  `yaml:"cycle_delay"`
	UnplannedAudit  bool `yaml:"unplanned_audit"`
	MaxStuckRetries int  `yaml:"max_stuck_retries"`
}

type PlanSettings struct {
	AutoApprove bool `yaml:"auto_approve"`
	AutoExecute bool `yaml:"auto_execute"`
	// Audit gates the blind plan audit (plan.xml step 11a), decoupled from
	// supervisor.unplanned_audit which keeps gating only the unplanned-work
	// Stop hook. A *bool (not plain bool) so a legacy setting.yaml that
	// predates the key (nil) is distinguishable from an explicit
	// `audit: false` opt-out: nil is backfilled to true by LoadSettings
	// (mirroring the AutoCommit idiom). Read via AuditEnabled(), never directly.
	Audit *bool `yaml:"audit"`
}

// AuditEnabled reports whether the blind plan audit is on. Nil (a
// hand-constructed Settings{} or a pre-backfill legacy decode) defaults ON;
// only an explicit false opts out.
func (p PlanSettings) AuditEnabled() bool {
	return p.Audit == nil || *p.Audit
}

type SudoSettings struct {
	Timeout int `yaml:"timeout"`
}

type TaskvisorSettings struct {
	DispatchTimeout int `yaml:"dispatch_timeout"`
	ValidateTimeout int `yaml:"validate_timeout"`
	PollInterval    int `yaml:"poll_interval"`
	// CircuitBreakerK is the number of consecutive cycles with an identical
	// failure-signature set that trips the C6 convergence circuit-breaker
	// (halt to blocked/owner=human). Values <1 fall back to the default of 2.
	CircuitBreakerK int `yaml:"circuit_breaker_k"`
	// AutoResumeIntervalSec is the cadence (seconds) of the §5 auto-resume loop
	// that re-evaluates precondition-blocked goals. Values <1 fall back to 30s.
	AutoResumeIntervalSec int `yaml:"auto_resume_interval_sec"`
	// ProgressTimeoutSec bounds how long a dispatched supervisor/validator window
	// may emit NO new pane output before the daemon's per-tick progress heartbeat
	// (P2) declares it wedged and recovers early — closing the silent-timeout hole
	// where a stuck LLM was invisible until the 1h hard timeout. Default 300 (5m);
	// a value <=0 disables the heartbeat. Values >0 override the daemon default.
	ProgressTimeoutSec int `yaml:"progress_timeout_sec"`
	// ValidateScriptTimeoutSec bounds ONE execution of the worktree integration
	// gate script (runIntegrationGate), which shares the daemon's script-runner
	// seam. Default 600; a value <=0 keeps the daemon seed. The 600s ceiling
	// covers a Symfony stack with margin.
	ValidateScriptTimeoutSec int `yaml:"validate_script_timeout_sec"`
	// TransientRetryMaxAttempts bounds the C4-cont transient-failure retry loop in
	// investigate.xml: a preflight/probe failing for a transient infra reason
	// (service not ready, timeout, DNS hiccup) is retried up to this many TOTAL
	// attempts before escalating to a blocked/infra-flake finding. Backfilled to 3
	// when absent from a legacy setting.yaml.
	TransientRetryMaxAttempts int `yaml:"transient_retry_max_attempts"`
	// TransientRetryBackoffMs is the per-retry backoff (milliseconds) between
	// transient-failure attempts (N attempts ⇒ N-1 sleeps). Backfilled to 500 when
	// absent from a legacy setting.yaml.
	TransientRetryBackoffMs int `yaml:"transient_retry_backoff_ms"`
	// MaxWallClockSec is the daemon's wall-clock cost ceiling (P3): once the daemon
	// has been continuously active for this many seconds, tick() halts loudly and
	// deactivates, leaving goal statuses UNTOUCHED so a human can raise the budget
	// and resume. Wall-clock is the chosen proxy because token/$ spend is not
	// observable by the daemon. Default 14400 (4h); a value <=0 DISABLES the ceiling
	// (no halt ever fires — byte-identical to the pre-P3 build).
	MaxWallClockSec int `yaml:"max_wall_clock_sec"`
	// IntegrationCmd is an optional shell command run against the merged base
	// INSIDE the worktree-merge lock immediately after a goal's worktree
	// fast-forwards base (MaxGoals>1 only). It catches a semantically-broken
	// merge that scope-disjointness alone misses: two goals editing different
	// files in the same package can each pass in isolation yet break the combined
	// suite. A non-zero exit fails the goal and cascades to its dependents. Empty
	// (the default) disables the gate — byte-identical to the pre-gate build.
	IntegrationCmd       string `yaml:"integration_cmd"`
	RequirePlanApproval  bool   `yaml:"require_plan_approval"`
	HaltOnStaleBinary    bool   `yaml:"halt_on_stale_binary"`
	RestartOnStaleBinary bool   `yaml:"restart_on_stale_binary"`
	// AutoPush gates the completion-time auto-push step (autoPushOnCompletion):
	// when a run finishes, the daemon runs one plain `git push` once to publish
	// the whole run's local commits. A plain bool (not the *bool+accessor idiom
	// of AutoCommit) because the default is OFF — pushing is outward-facing — so
	// the Go zero value (false) is exactly right and a legacy setting.yaml that
	// predates the key reads false. Mirrors HaltOnStaleBinary.
	AutoPush bool `yaml:"auto_push"`
	// AutoCommit gates the completion-time auto-commit step: when a goal
	// transitions to done, the daemon commits the goal's scope-matched changeset
	// to the currently checked-out branch (one commit boundary per goal). A
	// *bool (not plain bool) so a legacy setting.yaml that predates the key
	// (nil) is distinguishable from an explicit `auto_commit: false` opt-out:
	// nil is backfilled to true by LoadSettings (mirroring the TransientRetry
	// idiom). Read via AutoCommitEnabled(), never directly.
	AutoCommit *bool `yaml:"auto_commit"`
	// GitFreshness gates the pre-dispatch / pre-claim git-freshness preflight
	// (goal-005): before a goal is dispatched or a backend task is claimed, the
	// daemon fetches origin and refuses to start work on a diverged checkout. A
	// *bool (not plain bool), byte-for-byte mirroring AutoCommit, so a legacy
	// setting.yaml predating the key (nil) is distinguishable from an explicit
	// `git_freshness: false` opt-out: nil is backfilled to true by LoadSettings.
	// Read via GitFreshnessEnabled(), never directly.
	GitFreshness *bool `yaml:"git_freshness"`
	// DispatchOverrides overrides the per-phase first-dispatch command the daemon
	// runs (taskvisor dispatchcmd.go matrix). Keys are goal phases (gate, scaffold,
	// domain, …); values are "plan" (run the /tmux:plan pre-planner) or "implement"
	// (skip planning, dispatch the supervisor directly). Unlisted phases keep the
	// built-in matrix default; an unknown phase or value is logged and ignored. A
	// generation bounce always forces planning regardless of an override.
	DispatchOverrides map[string]string `yaml:"dispatch_overrides"`
	// Validation gates the post-execution goal validation step: when ON (the
	// default), a goal that finishes execution is handed to the reasoning
	// validator/investigator workers before it can reach done. When OFF, the
	// daemon marks the goal done DIRECTLY out of the supervising phase — no
	// validator windows. A *bool (not plain bool),
	// byte-for-byte mirroring AutoCommit, so a legacy setting.yaml predating the
	// key (nil) is distinguishable from an explicit `validation: false` opt-out:
	// nil is backfilled to true by LoadSettings. Read via ValidationEnabled(),
	// never directly.
	Validation *bool `yaml:"validation"`
}

// AutoCommitEnabled reports whether completion-time auto-commit is on. Nil
// (a hand-constructed Settings{} or a pre-backfill legacy decode) defaults ON;
// only an explicit false opts out.
func (t TaskvisorSettings) AutoCommitEnabled() bool {
	return t.AutoCommit == nil || *t.AutoCommit
}

// GitFreshnessEnabled reports whether the pre-dispatch/pre-claim git-freshness
// preflight is on. Nil (a hand-constructed Settings{} or a pre-backfill legacy
// decode) defaults ON; only an explicit false opts out.
func (t TaskvisorSettings) GitFreshnessEnabled() bool {
	return t.GitFreshness == nil || *t.GitFreshness
}

// ValidationEnabled reports whether the post-execution goal validation step is on.
// Nil (a hand-constructed Settings{} or a pre-backfill legacy decode) defaults ON;
// only an explicit false opts out (goals are marked done directly).
func (t TaskvisorSettings) ValidationEnabled() bool {
	return t.Validation == nil || *t.Validation
}

// WorkerBudgetSec is the per-worker time budget in seconds, mirroring the
// 10-min/worker budget documented in investigate.xml:158. It is an exported
// Go constant (NOT a setting.yaml field) so it does not trigger the AGENTS.md
// TUI invariant for surfaced Settings fields.
const WorkerBudgetSec = 600

// DefaultMaxWorkers mirrors SupervisorSettings.MaxWorkers default of 4 and is
// used as the floor basis for the daemon's validate-timeout clamp.
const DefaultMaxWorkers = 4

// ValidatorOverheadSec covers the validator window itself: Claude boot,
// step-2 preflights (revalidation-plan, C8, inline-plan), report collection
// and the goal-validation-done call — wall time spent OUTSIDE the per-worker
// budget. Observed real-world overhead is 5–8 min; 600s gives headroom.
const ValidatorOverheadSec = 600

// DeriveValidateTimeout derives a validate timeout (seconds) from the per-worker
// budget rather than hardcoding it, so a legitimate multi-worker validation is
// never killed below the orchestrator's per-worker budget.
//
// It computes the number of sequential waves needed to run workerCount workers
// at maxWorkers parallelism (integer ceil), multiplies by the per-worker budget
// for the base, then adds a margin of max(60, base/10) plus ValidatorOverheadSec.
// The margin covers worker spawn/teardown plus result-aggregation overhead: the
// 60s floor dominates for small single-wave budgets while the 10% term scales
// for large/multi-wave runs. The overhead constant covers the validator window's
// own orchestration (boot, preflights, collection) — the rt.validateTime timer
// starts at validator WINDOW CREATION, well before any worker spawns.
//
// maxWorkers<=0 and workerCount<=0 are coerced to 1 (no div-by-zero, no zero result).
//
// Examples:
//
//	DeriveValidateTimeout(600,3,3) → 600+600*ceil(3/3)+60   = 1260
//	DeriveValidateTimeout(600,2,3) → 600+600*ceil(3/2)+120  = 1920  (120 = 10% of 1200)
func DeriveValidateTimeout(workerBudgetSec, maxWorkers, workerCount int) int {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	if workerCount <= 0 {
		workerCount = 1
	}
	waves := (workerCount + maxWorkers - 1) / maxWorkers
	base := workerBudgetSec * waves
	margin := base / 10
	if margin < 60 {
		margin = 60
	}
	return ValidatorOverheadSec + base + margin
}

type Settings struct {
	Hooks      HooksSettings      `yaml:"hooks"`
	Commands   CommandsSettings   `yaml:"commands"`
	API        APISettings        `yaml:"api"`
	Supervisor SupervisorSettings `yaml:"supervisor"`
	Plan       PlanSettings       `yaml:"plan"`
	Sudo       SudoSettings       `yaml:"sudo"`
	Taskvisor  TaskvisorSettings  `yaml:"taskvisor"`
	SelfUpdate SelfUpdateSettings `yaml:"self_update"`
}

// SelfUpdateSettings configures the `tmux-cli self-update` command. Like the
// api: block, it is deliberately NOT surfaced in the TUI (machine-specific,
// not customer-configurable per project) — no items entry, no ToSettings()
// arm; the overlay onto loaded settings preserves it through TUI round-trips.
type SelfUpdateSettings struct {
	// SourceDir is the tmux-cli source checkout used as the last-resort
	// source resolution (after --source and TMUX_CLI_SRC).
	SourceDir string `yaml:"source_dir"`
}

func DefaultSettings() *Settings {
	autoCommit := true
	gitFreshness := true
	validation := true
	planAudit := true
	return &Settings{
		Hooks: HooksSettings{
			SessionNotify:    false,
			BlockInteractive: true,
		},
		Commands: CommandsSettings{
			Enabled: true,
		},
		// Reporting is on by default for new projects, pointing at the production
		// backend. The URL mirrors producer.defaultAPIURL; producer.LoadConfig falls
		// back to that same value when url is empty, so the two never drift.
		API: APISettings{
			Enabled: true,
			URL:     "https://tmux.vojta.ai",
		},
		Supervisor: SupervisorSettings{
			MaxCycles:       0,
			MaxWorkers:      4,
			MaxGoals:        1,
			CycleDelay:      5,
			UnplannedAudit:  true,
			MaxStuckRetries: 3,
		},
		Plan: PlanSettings{
			AutoApprove: true,
			AutoExecute: true,
			Audit:       &planAudit,
		},
		Sudo: SudoSettings{
			Timeout: 30,
		},
		Taskvisor: TaskvisorSettings{
			RestartOnStaleBinary: true,
			DispatchTimeout:      3600,
			// Seed the default from the worker budget (derived, not hardcoded)
			// so there is a single source of truth: DeriveValidateTimeout(600,4,4) = 1260.
			ValidateTimeout:           DeriveValidateTimeout(WorkerBudgetSec, DefaultMaxWorkers, DefaultMaxWorkers),
			PollInterval:              5,
			CircuitBreakerK:           2,
			AutoResumeIntervalSec:     30,
			ProgressTimeoutSec:        300,
			ValidateScriptTimeoutSec:  600,
			TransientRetryMaxAttempts: 3,
			TransientRetryBackoffMs:   500,
			MaxWallClockSec:           14400,
			AutoCommit:                &autoCommit,
			GitFreshness:              &gitFreshness,
			Validation:                &validation,
		},
	}
}

func settingPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "setting.yaml")
}

func LoadSettings(projectRoot string) (*Settings, error) {
	p := settingPath(projectRoot)

	data, err := os.ReadFile(p)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		s := DefaultSettings()
		if err := SaveSettings(projectRoot, s); err != nil {
			return nil, err
		}
		return s, nil
	}

	var s Settings
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	// Backfill the C4-cont transient-retry knobs when a legacy setting.yaml omits
	// them, so the file (read directly by investigate.xml) always carries usable
	// retry defaults instead of a zero that disables retries. Scoped to the two
	// new fields only — the older taskvisor ints keep their zero-means-runtime-
	// default convention untouched.
	if s.Taskvisor.TransientRetryMaxAttempts == 0 {
		s.Taskvisor.TransientRetryMaxAttempts = 3
	}
	if s.Taskvisor.TransientRetryBackoffMs == 0 {
		s.Taskvisor.TransientRetryBackoffMs = 500
	}
	// Backfill auto_commit for a legacy setting.yaml predating the key: nil
	// means pre-feature (default ON), while an explicit false survives untouched.
	if s.Taskvisor.AutoCommit == nil {
		autoCommit := true
		s.Taskvisor.AutoCommit = &autoCommit
	}
	// Backfill git_freshness for a legacy setting.yaml predating the key: nil
	// means pre-feature (default ON), while an explicit false survives untouched.
	if s.Taskvisor.GitFreshness == nil {
		gitFreshness := true
		s.Taskvisor.GitFreshness = &gitFreshness
	}
	// Backfill validation for a legacy setting.yaml predating the key: nil means
	// pre-feature (default ON), while an explicit false survives untouched.
	if s.Taskvisor.Validation == nil {
		validation := true
		s.Taskvisor.Validation = &validation
	}
	// Backfill plan.audit for a legacy setting.yaml predating the key: nil
	// means pre-feature (default ON), while an explicit false survives untouched.
	if s.Plan.Audit == nil {
		planAudit := true
		s.Plan.Audit = &planAudit
	}
	// The api: reporting block is internal-only telemetry — never customer-configurable.
	// Force it enabled and pointed at the canonical backend so a hand-edited setting.yaml
	// (api.enabled: false or a repointed url) cannot disable or exfiltrate reporting. It is
	// absent from the TUI by design (see AGENTS.md api-block exception; WorkerBudgetSec precedent).
	s.API.Enabled = true
	s.API.URL = "https://tmux.vojta.ai"
	if err := SaveSettings(projectRoot, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func SaveSettings(projectRoot string, s *Settings) error {
	p := settingPath(projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}
