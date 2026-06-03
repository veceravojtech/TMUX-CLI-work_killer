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

type SupervisorSettings struct {
	MaxCycles      int  `yaml:"max_cycles"`
	MaxWorkers     int  `yaml:"max_workers"`
	CycleDelay     int  `yaml:"cycle_delay"`
	UnplannedAudit bool `yaml:"unplanned_audit"`
}

type PlanSettings struct {
	AutoApprove bool `yaml:"auto_approve"`
	AutoExecute bool `yaml:"auto_execute"`
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
}

// WorkerBudgetSec is the per-worker time budget in seconds, mirroring the
// 10-min/worker budget documented in investigate.xml:158. It is an exported
// Go constant (NOT a setting.yaml field) so it does not trigger the AGENTS.md
// TUI invariant for surfaced Settings fields.
const WorkerBudgetSec = 600

// DefaultMaxWorkers mirrors SupervisorSettings.MaxWorkers default of 4 and is
// used as the floor basis for the daemon's validate-timeout clamp.
const DefaultMaxWorkers = 4

// DeriveValidateTimeout derives a validate timeout (seconds) from the per-worker
// budget rather than hardcoding it, so a legitimate multi-worker validation is
// never killed below the orchestrator's per-worker budget.
//
// It computes the number of sequential waves needed to run workerCount workers
// at maxWorkers parallelism (integer ceil), multiplies by the per-worker budget
// for the base, then adds a margin of max(60, base/10). The margin covers worker
// spawn/teardown plus result-aggregation overhead: the 60s floor dominates for
// small single-wave budgets while the 10% term scales for large/multi-wave runs.
//
// maxWorkers<=0 and workerCount<=0 are coerced to 1 (no div-by-zero, no zero result).
//
// Examples:
//
//	DeriveValidateTimeout(600,3,3) → 600*ceil(3/3)+60   = 660
//	DeriveValidateTimeout(600,2,3) → 600*ceil(3/2)+120  = 1320  (120 = 10% of 1200)
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
	return base + margin
}

type Settings struct {
	Hooks      HooksSettings      `yaml:"hooks"`
	Commands   CommandsSettings   `yaml:"commands"`
	Supervisor SupervisorSettings `yaml:"supervisor"`
	Plan       PlanSettings       `yaml:"plan"`
	Sudo       SudoSettings       `yaml:"sudo"`
	Taskvisor  TaskvisorSettings  `yaml:"taskvisor"`
}

func DefaultSettings() *Settings {
	return &Settings{
		Hooks: HooksSettings{
			SessionNotify:    false,
			BlockInteractive: true,
		},
		Commands: CommandsSettings{
			Enabled: true,
		},
		Supervisor: SupervisorSettings{
			MaxCycles:      0,
			MaxWorkers:     4,
			CycleDelay:     5,
			UnplannedAudit: true,
		},
		Plan: PlanSettings{
			AutoApprove: true,
			AutoExecute: true,
		},
		Sudo: SudoSettings{
			Timeout: 30,
		},
		Taskvisor: TaskvisorSettings{
			DispatchTimeout: 3600,
			// Seed the default from the worker budget (derived, not hardcoded)
			// so there is a single source of truth: DeriveValidateTimeout(600,4,4) = 660.
			ValidateTimeout:           DeriveValidateTimeout(WorkerBudgetSec, DefaultMaxWorkers, DefaultMaxWorkers),
			PollInterval:              5,
			CircuitBreakerK:           2,
			AutoResumeIntervalSec:     30,
			TransientRetryMaxAttempts: 3,
			TransientRetryBackoffMs:   500,
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
