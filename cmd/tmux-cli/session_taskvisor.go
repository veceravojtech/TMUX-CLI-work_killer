package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/console/tmux-cli/internal/session"
	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/telemetry"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/console/tmux-cli/internal/transcript"
	"github.com/spf13/cobra"
)

var taskvisorCmd = &cobra.Command{
	Use:   "taskvisor",
	Short: "Manage the taskvisor daemon and goals",
	Run:   func(cmd *cobra.Command, args []string) { cmd.Help() },
}

var taskvisorStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Trigger taskvisor daemon from IDLE to ACTIVE",
	RunE:  runTaskvisorStart,
}

var taskvisorGoalCmd = &cobra.Command{
	Use:   "goal",
	Short: "Manage taskvisor goals",
	Run:   func(cmd *cobra.Command, args []string) { cmd.Help() },
}

var taskvisorGoalAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new goal",
	RunE:  runTaskvisorGoalAdd,
}

var taskvisorGoalEditCmd = &cobra.Command{
	Use:   "edit [goal-id]",
	Short: "Edit an existing goal's authoring fields (acceptance/validate/scope/status/deliverable-area/phase)",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskvisorGoalEdit,
}

var taskvisorGoalListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all goals with status",
	RunE:  runTaskvisorGoalList,
}

var taskvisorGoalDeleteCmd = &cobra.Command{
	Use:   "delete [goal-id]",
	Short: "Delete a goal from goals.yaml",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskvisorGoalDelete,
}

var taskvisorGoalResetCmd = &cobra.Command{
	Use:   "reset [goal-id]",
	Short: "Reset a failed or done goal back to pending (also recovers a window-less running goal, or any running goal with --force)",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskvisorGoalReset,
}

var taskvisorGoalPriorityCmd = &cobra.Command{
	Use:   "priority <goal-id> <value>",
	Short: "Set a goal's dispatch priority",
	Args:  cobra.ExactArgs(2),
	RunE:  runTaskvisorGoalPriority,
}

var taskvisorStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Return the taskvisor daemon to IDLE (graceful; process stays up — inverse of start)",
	Args:  cobra.NoArgs,
	RunE:  runTaskvisorStop,
}

var taskvisorGoalStopCmd = &cobra.Command{
	Use:        "stop",
	Short:      "Return the taskvisor daemon to IDLE (deprecated alias for `taskvisor stop`)",
	Args:       cobra.NoArgs,
	RunE:       runTaskvisorGoalStop,
	Deprecated: "use `tmux-cli taskvisor stop` instead",
}

var taskvisorRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the taskvisor daemon",
	Args:  cobra.NoArgs,
	RunE:  runTaskvisorRestart,
}

var taskvisorConcurrencyCmd = &cobra.Command{
	Use:   "concurrency",
	Short: "Get or set the live in-flight goal cap (runtime override; applies on the next tick, no restart)",
	Args:  cobra.NoArgs,
	RunE:  runTaskvisorConcurrency,
}

var taskvisorGoalSkipCmd = &cobra.Command{
	Use:   "skip [goal-id]",
	Short: "Skip a running goal (mark as done)",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskvisorGoalSkip,
}

var taskvisorGoalPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove all goals and daemon state for a fresh start",
	Args:  cobra.NoArgs,
	RunE:  runTaskvisorGoalPrune,
}

var taskvisorRevalidationPlanCmd = &cobra.Command{
	Use:   "revalidation-plan [goal-id]",
	Short: "Print the incremental re-validation plan (RERUN/REUSE per finding) as JSON — read-only",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskvisorRevalidationPlan,
}

var taskvisorInlinePlanCmd = &cobra.Command{
	Use:   "inline-plan [goal-id]",
	Short: "Partition the RERUN validators into inline (pure-command/static analysis) vs spawn (reasoning/advanced) — prints {inline,spawn,reason} JSON, read-only",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskvisorInlinePlan,
}

// dashboardDefaultWatchInterval is the bare `--watch` cadence; NoOptDefVal is
// derived from it (.String()) so the bare-flag value and the defensive helper
// default in resolveDashboardWatch never diverge.
const dashboardDefaultWatchInterval = 5 * time.Second

var taskvisorDashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Render the taskvisor status board (read-only); --watch to auto-refresh",
	RunE:  runTaskvisorDashboard,
}

// taskvisorDashboardWatch backs the dashboard `--watch[=Ns]` flag. Use
// cmd.Flags().Changed("watch") — not this value — to distinguish an omitted
// flag from a bare `--watch`.
var taskvisorDashboardWatch time.Duration

// taskvisorProjectRoot resolves the taskvisor control-plane root: cwd with any
// .tmux-cli/worktrees/<id> suffix stripped, so goal commands invoked from a
// per-goal worktree hit the BASE goals.yaml/locks (worktrees carry no .tmux-cli).
// Session start/windows-* commands intentionally keep raw cwd — their semantics
// differ and the MCP server already normalizes its own working dir.
func taskvisorProjectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return taskvisor.NormalizeProjectDir(cwd), nil
}

func runTaskvisorStart(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	gf, err := taskvisor.LoadGoals(cwd)
	if err != nil {
		return fmt.Errorf("load goals: %w", err)
	}
	hasStartable := false
	if gf != nil {
		_, hasPending := gf.NextPendingGoal()
		hasStartable = hasPending || gf.HasRecoverableBlock()
	}
	switch taskvisor.EvaluateStartGuard(cwd, gf == nil, hasStartable) {
	case taskvisor.StartRefusedNoLedger:
		return fmt.Errorf("no goals.yaml found — add goals first with 'taskvisor goal add'")
	case taskvisor.StartRefusedNoStartable:
		return fmt.Errorf("no pending or recoverable goals — all goals are done or failed")
	}

	signalPath := filepath.Join(cwd, ".tmux-cli", "taskvisor-start")
	if err := os.MkdirAll(filepath.Dir(signalPath), 0o755); err != nil {
		return fmt.Errorf("create .tmux-cli dir: %w", err)
	}
	if err := os.WriteFile(signalPath, nil, 0o644); err != nil {
		return fmt.Errorf("write signal file: %w", err)
	}

	fmt.Println("Taskvisor start signal written — daemon will activate on next poll")
	return nil
}

// effectiveConcurrency resolves the current effective in-flight goal cap for the
// given project root, mirroring the daemon's (d *Daemon) maxGoals() precedence:
// a valid runtime override (integer >= 1) wins, otherwise the setting.yaml
// supervisor.max_goals value, defaulting to 1 when unset/<=0/unreadable.
func effectiveConcurrency(root string) int {
	if n, ok := taskvisor.ReadConcurrencyOverride(root); ok {
		return n
	}
	s, err := setup.LoadSettings(root)
	if err != nil || s == nil || s.Supervisor.MaxGoals <= 0 {
		return 1
	}
	return s.Supervisor.MaxGoals
}

// runTaskvisorConcurrency gets or sets the live in-flight goal cap via the
// `.tmux-cli/taskvisor-concurrency` runtime override. The daemon's maxGoals()
// reads this file every tick, so a change applies on the next tick without a
// restart (raising it dispatches more ready disjoint goals; lowering it stops
// new dispatch while in-flight goals drain — never killed). With no flag it
// prints the current effective cap (read-only). --set/--inc/--dec are mutually
// exclusive; the override floors at 1 on the write path.
func runTaskvisorConcurrency(cmd *cobra.Command, args []string) error {
	root, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	current := effectiveConcurrency(root)

	switch {
	case cmd.Flags().Changed("set"):
		if concSet < 1 {
			return fmt.Errorf("--set must be >= 1 (got %d)", concSet)
		}
		if err := taskvisor.WriteConcurrencyOverride(root, concSet); err != nil {
			return fmt.Errorf("write concurrency override: %w", err)
		}
		fmt.Printf("concurrency cap set to %d — daemon applies it on the next tick\n", concSet)
	case concInc:
		n := current + 1
		if err := taskvisor.WriteConcurrencyOverride(root, n); err != nil {
			return fmt.Errorf("write concurrency override: %w", err)
		}
		fmt.Printf("concurrency cap set to %d — daemon applies it on the next tick\n", n)
	case concDec:
		n := current - 1
		if n < 1 {
			n = 1
		}
		if err := taskvisor.WriteConcurrencyOverride(root, n); err != nil {
			return fmt.Errorf("write concurrency override: %w", err)
		}
		fmt.Printf("concurrency cap set to %d — daemon applies it on the next tick\n", n)
	default:
		fmt.Printf("%d\n", current)
	}
	return nil
}

// runTaskvisorDashboard is the thin CLI shell for the taskvisor status board. It
// resolves the project root + tmux executor and delegates ALL render/watch logic
// to internal/taskvisor (RenderBoard / WatchBoard). Session discovery is INTERNAL
// to the renderer, so — unlike runTaskvisorDaemon — this runner does NOT resolve
// or require a tmux session: a missing session degrades gracefully to a
// "no tmux session" census rather than erroring.
func runTaskvisorDashboard(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	executor := tmux.NewTmuxExecutor()

	watch, interval := resolveDashboardWatch(cmd.Flags().Changed("watch"), taskvisorDashboardWatch)
	if !watch {
		return taskvisor.RenderBoard(os.Stdout, cwd, executor)
	}

	// Watch mode: SIGINT (Ctrl-C) / SIGTERM cancels ctx → WatchBoard returns. Map
	// context.Canceled to nil so an interrupt is a clean exit 0, not a surfaced error.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := taskvisor.WatchBoard(ctx, os.Stdout, cwd, executor, interval); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// resolveDashboardWatch maps the parsed --watch flag state to (watch, interval).
// It keys off `changed` FIRST because pflag cannot distinguish an omitted flag
// from a bare `--watch` by value alone: absent ⇒ render once; bare/non-positive
// ⇒ the 5s default (defensive — NoOptDefVal already yields 5s for a bare flag);
// otherwise the explicit value.
func resolveDashboardWatch(changed bool, val time.Duration) (watch bool, interval time.Duration) {
	if !changed {
		return false, 0
	}
	if val <= 0 {
		return true, dashboardDefaultWatchInterval
	}
	return true, val
}

// runTaskvisorRevalidationPlan is the read-only read-side seam of C10
// incremental re-validation. It loads the orchestrator-owned results.json
// ledger, derives the current cycle's findings (rule + scope + preconditions)
// from goal.md, computes each finding's input fingerprint via the Go
// ComputeInputFingerprint, and prints the PlanRevalidation JSON the orchestrator
// consumes before spawning inv-* workers. It writes nothing.
func runTaskvisorRevalidationPlan(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	goalID := args[0]

	prev, err := taskvisor.LoadResults(cwd, goalID)
	if err != nil {
		return fmt.Errorf("load results.json: %w", err)
	}

	goalMD := filepath.Join(cwd, ".tmux-cli", "goals", goalID, "goal.md")
	findings, err := parseGoalFindings(goalMD)
	if err != nil {
		return fmt.Errorf("parse goal.md findings: %w", err)
	}
	// Degenerate fallback: if goal.md exposes no investigators (e.g. rule-based
	// goal), seed the finding set from the prior ledger so the plan still covers
	// known findings rather than emitting an empty plan.
	if len(findings) == 0 && prev != nil {
		for id := range prev.Results {
			findings = append(findings, taskvisor.ValidationFinding{Rule: id})
		}
	}

	changed := revalChangedFiles
	if len(changed) == 0 {
		changed = gitChangedFiles(cwd)
	}

	plan := taskvisor.PlanRevalidation(prev, findings, changed, revalForceFull, revalFinalCycle)
	out, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal plan: %w", err)
	}
	fmt.Println(string(out))
	return nil
}

// inlinePlanOutput is the JSON shape of the inline/spawn partition. investigate.xml
// applies the same type-based split per-investigator; this CLI is the
// deterministic, test-covered mirror of that decision.
type inlinePlanOutput struct {
	Inline []string `json:"inline"`
	Spawn  []string `json:"spawn"`
	Reason string   `json:"reason"`
}

// runTaskvisorInlinePlan is the read-only read-side seam of the inline/spawn
// validation split. It loads the same inputs as revalidation-plan (the
// results.json ledger + goal.md findings), additionally parses the full
// investigator configs (type/commands/pass) needed by IsPureCommand, and prints
// the taskvisor.InlinePlan partition as {"inline","spawn","reason"} JSON — the
// RERUN investigators that run in-window (pure-command / static analysis) vs.
// those that spawn a reasoning worker (code-review, e2e/Chrome, etc.). It writes
// nothing. When goal.md exposes no investigators, a RERUN finding cannot be
// proven pure-command and falls to the `spawn` set (the safe path), never inline.
func runTaskvisorInlinePlan(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	goalID := args[0]

	prev, err := taskvisor.LoadResults(cwd, goalID)
	if err != nil {
		return fmt.Errorf("load results.json: %w", err)
	}

	goalMD := filepath.Join(cwd, ".tmux-cli", "goals", goalID, "goal.md")
	findings, err := parseGoalFindings(goalMD)
	if err != nil {
		return fmt.Errorf("parse goal.md findings: %w", err)
	}
	investigators, err := parseGoalInvestigators(goalMD)
	if err != nil {
		return fmt.Errorf("parse goal.md investigators: %w", err)
	}
	// Degenerate fallback: if goal.md exposes no investigators (e.g. rule-based
	// goal), seed the finding set from the prior ledger. With no investigator
	// configs, IsPureCommand cannot be proven and InlinePlan returns fanout.
	if len(findings) == 0 && prev != nil {
		for id := range prev.Results {
			findings = append(findings, taskvisor.ValidationFinding{Rule: id})
		}
	}

	changed := revalChangedFiles
	if len(changed) == 0 {
		changed = gitChangedFiles(cwd)
	}

	inline, spawn, reason := taskvisor.InlinePlan(investigators, prev, findings, changed, inlineCycleN, revalForceFull, revalFinalCycle)
	if inline == nil {
		inline = []string{}
	}
	if spawn == nil {
		spawn = []string{}
	}
	out, err := json.MarshalIndent(inlinePlanOutput{Inline: inline, Spawn: spawn, Reason: reason}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal inline plan: %w", err)
	}
	fmt.Println(string(out))
	return nil
}

// parseGoalInvestigators extracts the full ## Investigation Config investigator
// configs from goal.md — name, type, commands, and Pass — the fields
// taskvisor.IsPureCommand needs to classify a check as pure-command. It reads the
// file (an absent goal.md returns (nil,nil) so the caller degrades to fanout) and
// delegates the markdown scan to taskvisor.ParseInvestigators — the single,
// in-package inverse of renderInvestigationConfig, guarded by
// TestInvestigatorConfigParity so the renderer and reader can never drift.
func parseGoalInvestigators(goalMDPath string) ([]taskvisor.Investigator, error) {
	data, err := os.ReadFile(goalMDPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return taskvisor.ParseInvestigators(string(data)), nil
}

// parseGoalFindings extracts the current cycle's findings from goal.md: one
// finding per ## Investigation Config investigator (rule = its name, scope = its
// Paths line), each carrying the goal's stringified ## Preconditions for the
// fingerprint. An absent goal.md returns an empty slice (no error) so the caller
// can fall back to the prior ledger.
func parseGoalFindings(goalMDPath string) ([]taskvisor.ValidationFinding, error) {
	data, err := os.ReadFile(goalMDPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(string(data), "\n")

	var preconds []string
	var findings []taskvisor.ValidationFinding
	section := ""
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		switch {
		case strings.HasPrefix(line, "## "):
			section = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			continue
		case section == "Preconditions" && strings.HasPrefix(line, "- ["):
			// "- [kind] spec — remedy" → "kind:spec"
			rest := strings.TrimPrefix(line, "- [")
			if idx := strings.IndexByte(rest, ']'); idx >= 0 {
				kind := strings.TrimSpace(rest[:idx])
				spec := strings.TrimSpace(rest[idx+1:])
				if dash := strings.Index(spec, " — "); dash >= 0 {
					spec = strings.TrimSpace(spec[:dash])
				}
				preconds = append(preconds, kind+":"+spec)
			}
		case section == "Investigation Config" && strings.HasPrefix(line, "### "):
			name := strings.TrimSpace(strings.TrimPrefix(line, "### "))
			if colon := strings.IndexByte(name, ':'); colon >= 0 && strings.HasPrefix(name, "Investigator") {
				name = strings.TrimSpace(name[colon+1:])
			}
			f := taskvisor.ValidationFinding{Rule: name}
			// Scan this investigator's body for a Paths:/Path: line until the next heading.
			for j := i + 1; j < len(lines); j++ {
				b := strings.TrimSpace(lines[j])
				if strings.HasPrefix(b, "### ") || strings.HasPrefix(b, "## ") {
					break
				}
				// Strip a leading markdown bullet ("- "/"* ") so the canonical
				// rendered `- paths:` list item parses; bare `paths:` lines are
				// unaffected (TrimLeft is a no-op on them).
				stripped := strings.TrimLeft(b, "-* ")
				low := strings.ToLower(stripped)
				if strings.HasPrefix(low, "paths:") || strings.HasPrefix(low, "path:") {
					val := stripped[strings.IndexByte(stripped, ':')+1:]
					for _, p := range strings.FieldsFunc(val, func(r rune) bool { return r == ',' || r == ' ' }) {
						if p = strings.TrimSpace(p); p != "" {
							f.Scope = append(f.Scope, p)
						}
					}
				}
			}
			findings = append(findings, f)
		}
	}

	sort.Strings(preconds)
	for i := range findings {
		findings[i].Preconditions = preconds
	}
	return findings, nil
}

// gitChangedFiles returns repo-relative paths changed vs HEAD (staged, unstaged,
// and untracked), best-effort. Any git error yields an empty set (treated as no
// in-scope change), which the fingerprint handles as the baseline.
func gitChangedFiles(root string) []string {
	out, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		// porcelain format: "XY <path>" (path starts at column 3).
		path := strings.TrimSpace(line[3:])
		if arrow := strings.Index(path, " -> "); arrow >= 0 { // renames
			path = path[arrow+4:]
		}
		if path != "" {
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files
}

func runTaskvisorRestart(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	executor := tmux.NewTmuxExecutor()
	return doTaskvisorRestart(cwd, executor)
}

func doTaskvisorRestart(cwd string, executor tmux.TmuxExecutor) error {
	if err := stopDaemonProcess(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "warning: stop daemon process: %v\n", err)
	}

	sessionID, err := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", cwd)
	if err != nil || sessionID == "" {
		return fmt.Errorf("no tmux-cli session found for this directory — cannot restart daemon")
	}

	windows, err := executor.ListWindows(sessionID)
	if err != nil {
		return fmt.Errorf("list windows: %w", err)
	}

	var taskvisorWindowID string
	for _, w := range windows {
		if w.Name == "taskvisor" {
			taskvisorWindowID = w.TmuxWindowID
			break
		}
	}
	if taskvisorWindowID == "" {
		return fmt.Errorf("no 'taskvisor' window found in session — cannot restart daemon")
	}

	if err := executor.SendMessage(sessionID, taskvisorWindowID, "tmux-cli taskvisor --run"); err != nil {
		return fmt.Errorf("send relaunch command: %w", err)
	}

	pidPath := filepath.Join(cwd, ".tmux-cli", "taskvisor.pid")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pidPath); err == nil {
			fmt.Println("Taskvisor daemon restarted successfully")
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	fmt.Println("Taskvisor relaunch command sent (PID file not yet confirmed)")
	return nil
}

func runTaskvisorDaemon(cmd *cobra.Command, args []string) error {
	cwd, err := taskvisorProjectRoot()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	executor := tmux.NewTmuxExecutor()

	// Opt this (real daemon) process into structured telemetry so the in-process
	// goal.status / goal.phase emitters go live. Unit tests construct daemons
	// directly and never call this, so instrumented paths stay silent no-ops under
	// test. Gated on telemetry.enabled (default true).
	telemetry.InstallDefault(cwd)

	daemon := taskvisor.New(cwd, executor)
	// Inject the binary's compiled-in command templates so a per-goal worktree's
	// git-excluded .claude/commands/tmux mirror is regenerated byte-identical to the
	// embedded FS (not copied from a possibly-stale base mirror), keeping the
	// dual-write tests green under a daemon-run `make test`.
	daemon.SetCommandTemplates(buildCommandTemplates())
	// On stale-binary detection, rewrite the installed command templates in place
	// from the new binary's embedded FS (idempotent overwrite, no session restart).
	daemon.SetCommandRefreshFn(func() error {
		return setup.WriteCommands(cwd, buildCommandTemplates())
	})

	sessionID, _ := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", cwd)
	if sessionID == "" {
		return fmt.Errorf("no tmux-cli session found for this directory")
	}

	// Pane logs live at the BASE control plane (cwd here is the project root),
	// never inside a worktree — same destination windows-spawn-worker uses.
	// baseDir keeps the project root reachable inside the closure, whose own
	// cwd parameter is the per-goal worktree; the transcript gate/tree are
	// control-plane state and must never resolve inside a worktree.
	baseDir := cwd
	paneLogDir := filepath.Join(cwd, ".tmux-cli", "logs", "panes")
	daemon.SetWindowCreateFunc(func(name, command, cwd string) (*taskvisor.CreatedWindow, error) {
		windowUUID := session.GenerateUUID()
		// cwd is the per-goal worktree path (E1-1a) when MaxGoals>1, else "" or the
		// base workDir. CreateWindowInDir forwards it to `tmux new-window -c <dir>`;
		// an empty cwd leaves the session default (byte-identical to the prior build).
		windowID, err := executor.CreateWindowInDir(sessionID, name, "", cwd)
		if err != nil {
			return nil, fmt.Errorf("create window: %w", err)
		}
		if err := executor.SetWindowOption(sessionID, windowID, tmux.WindowUUIDOption, windowUUID); err != nil {
			_ = executor.KillWindow(sessionID, windowID)
			return nil, fmt.Errorf("set window UUID: %w", err)
		}
		// Best-effort pane persistence, mirroring windows-spawn-worker (tools.go):
		// without it a wedged supervisor/validator killed by stuck-recovery leaves
		// NO post-mortem trace ([[no-worker-pane-persistence]]). The daemon's kill
		// paths (killWindowByName/killWindowsByPrefix) already ClosePipePane first,
		// so the stream is flushed before the window dies. A pipe failure must
		// never block dispatch — log (lands in taskvisor.log) and continue.
		_ = os.MkdirAll(paneLogDir, 0o755)
		paneLog := filepath.Join(paneLogDir, name+".log")
		// P3 transcripts: same single-pipe split as windows-spawn-worker — armed
		// tees the pane log through the capture process, unarmed stays plain.
		var ppErr error
		if captureCmd := transcript.CapturePipeCommand(baseDir, sessionID, name, paneLog); captureCmd != "" {
			ppErr = executor.PipePaneCommand(sessionID, windowID, captureCmd)
		} else {
			ppErr = executor.PipePane(sessionID, windowID, paneLog)
		}
		if ppErr != nil {
			log.Printf("WARNING: pipe-pane for %s failed (best-effort): %v", name, ppErr)
		}
		exportCmd := fmt.Sprintf("export TMUX_WINDOW_UUID=\"%s\"", windowUUID)
		_ = executor.SendMessage(sessionID, windowID, exportCmd)

		// Inherit the session's --model and --flag tokens (set at start-attach)
		// when present.
		model, _ := executor.GetSessionEnvironment(sessionID, "TMUX_CLI_MODEL")
		rawFlags, _ := executor.GetSessionEnvironment(sessionID, "TMUX_CLI_FLAGS")
		postCmdConfig := session.PostCommandConfigWithModel(model, session.SplitFlags(rawFlags))
		_ = session.ExecutePostCommandWithFallback(executor, sessionID, windowID, postCmdConfig)

		return &taskvisor.CreatedWindow{TmuxWindowID: windowID, Name: name}, nil
	})

	return daemon.Run(cmd.Context())
}
