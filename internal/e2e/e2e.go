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
	StateFile        string   `json:"state_file,omitempty"`
	HumanView        string   `json:"human_view,omitempty"`
	Handshake        string   `json:"handshake,omitempty"`
	ReapedSessions   []string `json:"reaped_sessions,omitempty"`
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

// ParseState decodes a State ledger from disk bytes.
func ParseState(raw []byte) (State, error) {
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return State{}, fmt.Errorf("parse run-state: %w", err)
	}
	return s, nil
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

// HandshakeToken is the comms-test string the target echoes back through
// notify-orchestrator. The conductor proves the channel live by finding it
// (alongside the notify success line) in the target's pipe-pane log (step 3b).
func HandshakeToken(session string) string {
	return "E2E-HANDSHAKE-OK " + session
}
