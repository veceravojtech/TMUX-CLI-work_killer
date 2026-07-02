package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"os/exec"

	"github.com/console/tmux-cli/internal/producer"
	"github.com/console/tmux-cli/internal/session"
	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/sudo"
	"github.com/console/tmux-cli/internal/taskvisor"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/console/tmux-cli/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Embedded hook scripts
//
//go:embed embedded/tmux-session-notify.sh
var hookSessionNotify string

//go:embed embedded/tmux-validate-session.sh
var hookValidateSession string

//go:embed embedded/no-interactive-questions.sh
var hookNoInteractiveQuestions string

//go:embed embedded/tmux-supervisor-cycle.sh
var hookSupervisorCycle string

//go:embed embedded/tmux-unplanned-audit.sh
var hookUnplannedAudit string

//go:embed embedded/tmux-window-watchdog.sh
var hookWindowWatchdog string

//go:embed embedded/commands/tmux
var embeddedCommands embed.FS

//go:embed all:embedded/templates
var embeddedTemplates embed.FS

//go:embed all:embedded/rules
var embeddedRules embed.FS

var readPassword = func() (string, error) {
	fmt.Print("Enter sudo password: ")
	bytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func promptAndStoreSudoPassword(executor tmux.TmuxExecutor, sessionID string) error {
	password, err := readPassword()
	if err != nil {
		return fmt.Errorf("read sudo password: %w", err)
	}
	return executor.SetSessionEnvironment(sessionID, "TMUX_CLI_SUDO_PASS", password)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Create a new tmux session",
	Long: `Create a new detached tmux session for the current directory.

If a session is already running for this directory, you will be prompted
to recreate it or keep the existing one.

The session is identified by project path and timestamp.
Session state is stored in tmux itself — no session file needed.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSessionStart,
}

var startAttachCmd = &cobra.Command{
	Use:   "start-attach",
	Short: "Create a new tmux session and attach to it",
	Long: `Create a new detached tmux session for the current directory, then attach to it.

If a session is already running for the current directory, you will be prompted
to recreate it or keep the existing one. After session creation (or reuse),
tmux will attach to the session.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runStartAttach,
}

var killCmd = &cobra.Command{
	Use:   "kill [session-id]",
	Short: "Kill a tmux session",
	Long:  `Kill a tmux session by its session ID.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runSessionKill,
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all active sessions",
	Long:  `List all active sessions from the tmux server.`,
	RunE:  runSessionList,
}

// statusJSON toggles machine-readable JSON output for `tmux-cli status`.
var statusJSON bool

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show detailed status of the session for this directory",
	Long:  `Display detailed information about the tmux-cli session for the current directory.`,
	RunE:  runSessionStatus,
}

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Project management",
}

var projectInitCmd = &cobra.Command{
	Use:   "init [PATH]",
	Short: "Initialize a new tmux-cli project",
	Long: `Scaffold a .tmux-cli/ project structure, run auto-setup, and create or reuse
a tmux session non-interactively. Prints Session: <id> on stdout for agent parsing.

If PATH is omitted, the current directory is used. If a session is already running
for the resolved path, it is reused silently (no interactive prompt).`,
	Args: cobra.MaximumNArgs(1),
	RunE: runProjectInit,
}

var windowsCreateCmd = &cobra.Command{
	Use:   "windows-create",
	Short: "Create a new window in the session",
	Long: `Create a new window in the session. The window will start with zsh and automatically
execute the configured postcommand (if enabled).

Example:
  tmux-cli windows-create --name editor`,
	RunE: runWindowsCreate,
}

var windowsListCmd = &cobra.Command{
	Use:   "windows-list",
	Short: "List all windows in the session",
	Long: `List all windows in the session with their IDs and names.

Example:
  tmux-cli windows-list`,
	RunE: runWindowsList,
}

var windowsKillCmd = &cobra.Command{
	Use:   "windows-kill",
	Short: "Kill a window in the session",
	Long: `Kill (remove) a window from the session by its tmux window ID.

Example:
  tmux-cli windows-kill --window-id @1`,
	RunE: runWindowsKill,
}

var windowsSendCmd = &cobra.Command{
	Use:   "windows-send",
	Short: "Send a text message to a specific window",
	Long: `Send a text message to a specific window in a session.

Window Identifier:
  - Use window ID (e.g., @0, @1) for direct window access
  - Use window name (e.g., "supervisor", "bmad-worker") for friendly access

Examples:
  tmux-cli windows-send --window-id @0 --message "Task complete"
  tmux-cli windows-send --window-id supervisor --message "restart"`,
	RunE: runSessionSend,
}

var windowsUuidCmd = &cobra.Command{
	Use:   "windows-uuid",
	Short: "Get the persistent UUID of a window",
	Long: `Get the persistent UUID of a window by its tmux window ID.

Example:
  tmux-cli windows-uuid --window-id @0
  export WINDOW_UUID=$(tmux-cli windows-uuid --window-id @0)`,
	RunE: runWindowsUuid,
}

var settingCmd = &cobra.Command{
	Use:   "setting",
	Short: "Open TUI to configure tmux-cli settings",
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get current directory: %w", err)
		}
		return tui.Run(cwd)
	},
}

var sudoCmd = &cobra.Command{
	Use:   "sudo",
	Short: "Run a command with root privileges",
	Long: `Run a shell command with root privileges using the cached sudo password.
The command streams stdout/stderr in real-time. Requires a session started with --sudo.

Example:
  tmux-cli sudo "apt update"
  tmux-cli sudo --timeout 120 "apt upgrade -y"`,
	Args: cobra.ExactArgs(1),
	RunE: runSudo,
}

var windowsMessageCmd = &cobra.Command{
	Use:   "windows-message",
	Short: "Send a formatted message to another window",
	Long: `Send a formatted message to another window with auto-detected sender.

Window Identifier:
  - Use window ID (e.g., @0, @1) for direct window access
  - Use window name (e.g., "supervisor", "bmad-worker") for friendly access

Examples:
  tmux-cli windows-message --receiver supervisor --message "Task completed successfully"`,
	RunE: runWindowsMessage,
}

var notifyOrchestratorCmd = &cobra.Command{
	Use:   "notify-orchestrator <message>",
	Short: "Send a reply message to the orchestrator pane",
	Long: `Deliver a message (followed by a separate Enter) directly to the orchestrator
pane identified by the TMUX_CLI_ORCHESTRATOR_PANE environment variable.

The pane id (e.g. %3) is targeted directly — pane ids are session-global, so no
session/window resolution occurs. This is the target→orchestrator reply channel
used by the e2e-evaluator conductor tasks.

An empty message delivers a bare Enter (a valid heartbeat/ack ping).
If TMUX_CLI_ORCHESTRATOR_PANE is unset or empty, the command fails loudly without
sending anything.

Examples:
  TMUX_CLI_ORCHESTRATOR_PANE=%3 tmux-cli notify-orchestrator "evaluation complete"`,
	Args: cobra.ExactArgs(1),
	RunE: runNotifyOrchestrator,
}

var (
	windowName      string
	windowIDFlag    string
	sendWindowID    string
	sendMessage     string
	messageReceiver string
	messageText     string
)

var (
	taskvisorRun    bool
	skipReason      string
	resetForce      bool
	goalDescription string
	goalAcceptance  []string
	goalValidate    []string
	goalContext     string
	goalNotInScope  string
	goalPhase       string
	goalMaxRetries  int
	goalScope       []string
	goalPriority    int

	// goal edit flag-backing vars. The handler reads them only when the
	// corresponding flag was Changed(), so an absent flag maps to a nil GoalEdit
	// pointer (leave untouched) and a present flag to a set/clear.
	goalEditAcceptance      []string
	goalEditValidate        []string
	goalEditScope           []string
	goalEditStatus          string
	goalEditDeliverableArea string
	goalEditPhase           string

	revalForceFull    bool
	revalFinalCycle   bool
	revalChangedFiles []string

	inlineCycleN int

	concSet int
	concInc bool
	concDec bool
)

func init() {
	// Add flags to windows-create command
	windowsCreateCmd.Flags().StringVar(&windowName, "name", "", "Window name (required)")
	windowsCreateCmd.MarkFlagRequired("name")

	// Add flags to windows-kill command
	windowsKillCmd.Flags().StringVar(&windowIDFlag, "window-id", "", "Tmux window ID to kill (e.g., @0, @1)")
	windowsKillCmd.MarkFlagRequired("window-id")

	// Add flags to windows-send command
	windowsSendCmd.Flags().StringVar(&sendWindowID, "window-id", "", "Target window ID (format: @N, e.g., @0, @1)")
	windowsSendCmd.Flags().StringVar(&sendMessage, "message", "", "Text message to send to the window")
	windowsSendCmd.MarkFlagRequired("window-id")
	windowsSendCmd.MarkFlagRequired("message")

	// Add flags to windows-uuid command
	windowsUuidCmd.Flags().StringVar(&windowIDFlag, "window-id", "", "Tmux window ID (e.g., @0, @1)")
	windowsUuidCmd.MarkFlagRequired("window-id")

	// Add flags to windows-message command
	windowsMessageCmd.Flags().StringVar(&messageReceiver, "receiver", "", "Target window ID or name (e.g., @0, supervisor)")
	windowsMessageCmd.Flags().StringVar(&messageText, "message", "", "Message text to send")
	windowsMessageCmd.MarkFlagRequired("receiver")
	windowsMessageCmd.MarkFlagRequired("message")

	// Add --sudo flag to start and start-attach commands
	startCmd.Flags().Bool("sudo", false, "Prompt for sudo password and cache it for the session")
	startAttachCmd.Flags().Bool("sudo", false, "Prompt for sudo password and cache it for the session")

	// Add --model flag to start and start-attach commands. Recorded in the session
	// environment as TMUX_CLI_MODEL and injected as --model into every
	// window/worker's claude launch.
	startCmd.Flags().String("model", "", "Claude model for this session (e.g. 'claude-opus-4-6[1m]'); applies to all windows and workers")
	startAttachCmd.Flags().String("model", "", "Claude model for this session (e.g. 'claude-opus-4-6[1m]'); applies to all windows and workers")

	// Add --timeout flag to sudo command
	sudoCmd.Flags().Int("timeout", 0, "Timeout in seconds (0 = no timeout; omit for config default, which is 30s)")

	// Add --clean flag to start and start-attach commands
	startCmd.Flags().Bool("clean", false, "Delete and recreate .tmux-cli/ folder before session creation")
	startCmd.Flags().Bool("print-json", false, "Emit exactly one JSON line {\"session\":\"<id>\",\"created\":true|false} on stdout (human progress moves to stderr)")
	startAttachCmd.Flags().Bool("clean", false, "Delete and recreate .tmux-cli/ folder before session creation")

	// Add --resume-state flag to start and start-attach commands. When set, a
	// kickoff message pointing the supervisor window at the resume-state file is
	// sent before attaching, so a resumed session picks up the interrupted work.
	startCmd.Flags().String("resume-state", "", "Path to a resume-state file; the supervisor window is pointed at it on startup")
	startAttachCmd.Flags().String("resume-state", "", "Path to a resume-state file; the supervisor window is pointed at it on startup")

	// Taskvisor commands
	taskvisorCmd.Flags().BoolVar(&taskvisorRun, "run", false, "Start daemon loop (internal)")
	taskvisorCmd.Flags().MarkHidden("run")
	taskvisorCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if taskvisorRun {
			if err := runTaskvisorDaemon(cmd, args); err != nil {
				return err
			}
			os.Exit(0)
		}
		return nil
	}

	taskvisorGoalAddCmd.Flags().StringVar(&goalDescription, "description", "", "Goal description (required)")
	taskvisorGoalAddCmd.Flags().StringArrayVar(&goalAcceptance, "acceptance", nil, "Acceptance criteria (repeatable)")
	taskvisorGoalAddCmd.Flags().StringArrayVar(&goalValidate, "validate", nil, "Validation commands (repeatable)")
	taskvisorGoalAddCmd.Flags().StringVar(&goalContext, "context", "", "Background context for the goal")
	taskvisorGoalAddCmd.Flags().StringVar(&goalNotInScope, "not-in-scope", "", "What is explicitly out of scope")
	taskvisorGoalAddCmd.Flags().StringVar(&goalPhase, "phase", "", "Development phase (gate,scaffold,fixtures,domain,application,infrastructure,action,auth,event,cross-cutting,deployment,ci,final)")
	taskvisorGoalAddCmd.Flags().IntVar(&goalMaxRetries, "max-retries", 5, "Max retry attempts")
	taskvisorGoalAddCmd.Flags().StringArrayVar(&goalScope, "scope", nil, "Goal file/namespace footprint for co-scheduling, e.g. 'internal/x/**' (repeatable; empty = derived from --acceptance, else unknown ⇒ serialized)")
	taskvisorGoalAddCmd.Flags().IntVar(&goalPriority, "priority", 0, "Dispatch priority (higher = first; default 0)")
	taskvisorGoalAddCmd.MarkFlagRequired("description")

	// goal edit: each flag maps to a tri-state GoalEdit pointer — set only when
	// the flag was Changed() (an empty value then CLEARS the field).
	taskvisorGoalEditCmd.Flags().StringArrayVar(&goalEditAcceptance, "acceptance", nil, "Replace acceptance criteria (repeatable; pass once with no value to clear)")
	taskvisorGoalEditCmd.Flags().StringArrayVar(&goalEditValidate, "validate", nil, "Replace validation commands (repeatable; pass once with no value to clear)")
	taskvisorGoalEditCmd.Flags().StringArrayVar(&goalEditScope, "scope", nil, "Replace file/namespace footprint globs (repeatable; pass once with no value to clear)")
	taskvisorGoalEditCmd.Flags().StringVar(&goalEditStatus, "status", "", "Set status — only roadmap/pending/blocked (running/done/failed are daemon-owned and rejected)")
	taskvisorGoalEditCmd.Flags().StringVar(&goalEditDeliverableArea, "deliverable-area", "", "Replace the coarse deliverable footprint (empty clears)")
	taskvisorGoalEditCmd.Flags().StringVar(&goalEditPhase, "phase", "", "Refine the development phase (gate,scaffold,fixtures,domain,application,infrastructure,action,auth,event,cross-cutting,deployment,ci,final)")

	taskvisorCmd.AddCommand(taskvisorStartCmd)
	taskvisorCmd.AddCommand(taskvisorStopCmd)
	taskvisorCmd.AddCommand(taskvisorRestartCmd)

	taskvisorConcurrencyCmd.Flags().IntVar(&concSet, "set", 0, "Set the in-flight goal cap to N (N>=1)")
	taskvisorConcurrencyCmd.Flags().BoolVar(&concInc, "inc", false, "Increment the current cap by 1")
	taskvisorConcurrencyCmd.Flags().BoolVar(&concDec, "dec", false, "Decrement the current cap by 1 (floored at 1)")
	taskvisorConcurrencyCmd.MarkFlagsMutuallyExclusive("set", "inc", "dec")
	taskvisorCmd.AddCommand(taskvisorConcurrencyCmd)
	taskvisorGoalCmd.AddCommand(taskvisorGoalAddCmd)
	taskvisorGoalCmd.AddCommand(taskvisorGoalEditCmd)
	taskvisorGoalCmd.AddCommand(taskvisorGoalListCmd)
	taskvisorGoalCmd.AddCommand(taskvisorGoalDeleteCmd)
	taskvisorGoalResetCmd.Flags().BoolVar(&resetForce, "force", false, "Force reset a running goal even if it still owns a live worker window")
	taskvisorGoalCmd.AddCommand(taskvisorGoalResetCmd)
	taskvisorGoalCmd.AddCommand(taskvisorGoalPriorityCmd)
	taskvisorGoalSkipCmd.Flags().StringVar(&skipReason, "reason", "manually skipped", "Reason for skipping")
	taskvisorGoalCmd.AddCommand(taskvisorGoalSkipCmd)
	taskvisorGoalCmd.AddCommand(taskvisorGoalStopCmd)
	taskvisorGoalCmd.AddCommand(taskvisorGoalPruneCmd)
	taskvisorCmd.AddCommand(taskvisorGoalCmd)

	taskvisorRevalidationPlanCmd.Flags().BoolVar(&revalForceFull, "full", false, "Force full re-validation — every check RERUN regardless of fingerprint")
	taskvisorRevalidationPlanCmd.Flags().BoolVar(&revalFinalCycle, "final", false, "Final cycle before overall pass — re-run all checks for end-to-end verification")
	taskvisorRevalidationPlanCmd.Flags().StringArrayVar(&revalChangedFiles, "changed-file", nil, "A file changed this cycle (repeatable); defaults to git diff --name-only HEAD")
	taskvisorCmd.AddCommand(taskvisorRevalidationPlanCmd)

	taskvisorInlinePlanCmd.Flags().IntVar(&inlineCycleN, "cycle", 0, "Current validation cycle (informational; the inline/spawn split is per-investigator pure-command, any cycle)")
	taskvisorInlinePlanCmd.Flags().BoolVar(&revalForceFull, "full", false, "Force full re-validation — every check RERUN regardless of fingerprint")
	taskvisorInlinePlanCmd.Flags().BoolVar(&revalFinalCycle, "final", false, "Final cycle before overall pass — re-run all checks for end-to-end verification")
	taskvisorInlinePlanCmd.Flags().StringArrayVar(&revalChangedFiles, "changed-file", nil, "A file changed this cycle (repeatable); defaults to git diff --name-only HEAD")
	taskvisorCmd.AddCommand(taskvisorInlinePlanCmd)

	taskvisorDashboardCmd.Flags().DurationVar(&taskvisorDashboardWatch, "watch", 0, "Auto-refresh the board on an interval (e.g. --watch=10s); bare --watch = 5s; omit for a single static snapshot")
	// NoOptDefVal makes a bare `--watch` (no =value) resolve to 5s; it MUST be set
	// after DurationVar and derived from the const so the two never drift.
	taskvisorDashboardCmd.Flags().Lookup("watch").NoOptDefVal = dashboardDefaultWatchInterval.String()
	taskvisorCmd.AddCommand(taskvisorDashboardCmd)

	// Project init flags
	projectInitCmd.Flags().Bool("no-attach", false, "Don't attach to the session after creation")
	projectCmd.AddCommand(projectInitCmd)

	// Add all commands directly to root
	rootCmd.AddCommand(projectCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(startAttachCmd)
	rootCmd.AddCommand(killCmd)
	rootCmd.AddCommand(listCmd)
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "emit machine-readable JSON worker state instead of human output")
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(windowsCreateCmd)
	rootCmd.AddCommand(windowsListCmd)
	rootCmd.AddCommand(windowsKillCmd)
	rootCmd.AddCommand(windowsSendCmd)
	rootCmd.AddCommand(windowsUuidCmd)
	rootCmd.AddCommand(windowsMessageCmd)
	rootCmd.AddCommand(notifyOrchestratorCmd)
	rootCmd.AddCommand(settingCmd)
	rootCmd.AddCommand(sudoCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(taskvisorCmd)
}

// UsageError represents an error in command usage or arguments
type UsageError struct {
	msg string
}

func (e UsageError) Error() string {
	return e.msg
}

// NewUsageError creates a new usage error
func NewUsageError(msg string) error {
	return UsageError{msg: msg}
}

// startOrReuseSession handles session discovery, interactive prompts for existing sessions,
// and session creation. Returns the session ID (existing or newly created) and whether a
// new session was created (false when an existing session was kept). Human progress and
// prompt lines go to out — os.Stdout normally, os.Stderr under `start --print-json` so
// stdout stays pure for the JSON contract line.
func startOrReuseSession(executor tmux.TmuxExecutor, projectPath, model string, out io.Writer) (string, bool, error) {
	// Check if session already exists for this path
	existingSessionID, _ := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", projectPath)

	if existingSessionID != "" {
		running, _ := executor.HasSession(existingSessionID)
		if running {
			fmt.Fprintf(out, "Session '%s' is already running for %s\n", existingSessionID, projectPath)
			fmt.Fprintln(out, "What would you like to do?")
			fmt.Fprintln(out, "  [r] Recreate session (kill existing + create new)")
			fmt.Fprintln(out, "  [c] Cancel and keep existing session")
			fmt.Fprint(out, "Choice (r/c): ")

			var response string
			if _, err := fmt.Scanln(&response); err != nil {
				// EOF or pipe input — treat as cancel
				fmt.Fprintf(out, "Keeping existing session '%s'\n", existingSessionID)
				applyModelToExistingSession(executor, existingSessionID, model)
				return existingSessionID, false, nil
			}

			if response == "r" || response == "R" {
				if err := executor.KillSession(existingSessionID); err != nil {
					return "", false, fmt.Errorf("kill existing session '%s': %w", existingSessionID, err)
				}
				// Fall through to create new session
			} else {
				fmt.Fprintf(out, "Keeping existing session '%s'\n", existingSessionID)
				applyModelToExistingSession(executor, existingSessionID, model)
				return existingSessionID, false, nil
			}
		}
	}

	// Create new session
	sessionID := session.GenerateSessionID(projectPath)
	manager := session.NewSessionManager(executor).WithModel(model)

	if err := manager.CreateSession(sessionID, projectPath); err != nil {
		return "", false, err
	}

	fmt.Fprintf(out, "Created session '%s' for %s\n", sessionID, projectPath)
	return sessionID, true, nil
}

// startJSONResult is the machine-readable line emitted by `start --print-json`.
// The JSON tags are the stable contract e2e-bootstrap's startTarget consumes —
// exactly one compact line on stdout: {"session":"<id>","created":true|false}.
type startJSONResult struct {
	Session string `json:"session"`
	Created bool   `json:"created"`
}

// startSessionJSON renders the single compact --print-json stdout line.
func startSessionJSON(sessionID string, created bool) string {
	b, _ := json.Marshal(startJSONResult{Session: sessionID, Created: created})
	return string(b)
}

// applyModelToExistingSession records TMUX_CLI_MODEL on a reused session so
// windows/workers spawned AFTER this point inject the requested model into their
// claude launch command. The already-running supervisor window keeps its current
// model (its launch command already ran). Best-effort, no-op for an empty model.
func applyModelToExistingSession(executor tmux.TmuxExecutor, sessionID, model string) {
	if model == "" {
		return
	}
	_ = executor.SetSessionEnvironment(sessionID, "TMUX_CLI_MODEL", model)
}

func cleanProjectDir(projectPath string) error {
	if err := os.RemoveAll(filepath.Join(projectPath, ".tmux-cli")); err != nil {
		return err
	}
	// Per-goal worktrees now live in the in-repo sibling .tmux-cli-worktrees/
	// (no longer nested under .tmux-cli), so a clean must remove it too. Git's
	// .git/worktrees/<id> admin stubs left behind are healed by the next
	// pruneOrphanWorktrees `git worktree prune`.
	return os.RemoveAll(filepath.Join(projectPath, ".tmux-cli-worktrees"))
}

func runSessionStart(cmd *cobra.Command, args []string) error {
	projectPath, err := resolveProjectPath(args)
	if err != nil {
		return err
	}
	if clean, _ := cmd.Flags().GetBool("clean"); clean {
		if err := cleanProjectDir(projectPath); err != nil {
			return fmt.Errorf("clean project dir: %w", err)
		}
	}
	if err := runAutoSetup(projectPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: auto-setup: %v\n", err)
	}
	// Under --print-json stdout carries EXACTLY one JSON contract line; all
	// human progress moves to stderr. Without the flag output is unchanged.
	printJSON, _ := cmd.Flags().GetBool("print-json")
	progressOut := io.Writer(os.Stdout)
	if printJSON {
		progressOut = os.Stderr
	}
	executor := tmux.NewTmuxExecutor()
	model, _ := cmd.Flags().GetString("model")
	sessionID, created, err := startOrReuseSession(executor, projectPath, model, progressOut)
	if err != nil {
		return err
	}
	mgr := session.NewSessionManager(executor)
	if err := mgr.EnsureTaskvisorWindow(sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: taskvisor setup: %v\n", err)
	}
	sudoFlag, _ := cmd.Flags().GetBool("sudo")
	if sudoFlag {
		if err := promptAndStoreSudoPassword(executor, sessionID); err != nil {
			return fmt.Errorf("sudo setup failed: %w", err)
		}
	}
	if printJSON {
		fmt.Fprintln(cmd.OutOrStdout(), startSessionJSON(sessionID, created))
	}
	return nil
}

func runStartAttach(cmd *cobra.Command, args []string) error {
	projectPath, err := resolveProjectPath(args)
	if err != nil {
		return err
	}
	if clean, _ := cmd.Flags().GetBool("clean"); clean {
		if err := cleanProjectDir(projectPath); err != nil {
			return fmt.Errorf("clean project dir: %w", err)
		}
	}
	if err := runAutoSetup(projectPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: auto-setup: %v\n", err)
	}
	executor := tmux.NewTmuxExecutor()
	model, _ := cmd.Flags().GetString("model")
	sessionID, _, err := startOrReuseSession(executor, projectPath, model, os.Stdout)
	if err != nil {
		return err
	}
	mgr := session.NewSessionManager(executor)
	if err := mgr.EnsureTaskvisorWindow(sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: taskvisor setup: %v\n", err)
	}
	sudoFlag, _ := cmd.Flags().GetBool("sudo")
	if sudoFlag {
		if err := promptAndStoreSudoPassword(executor, sessionID); err != nil {
			return fmt.Errorf("sudo setup failed: %w", err)
		}
	}
	resumeState, _ := cmd.Flags().GetString("resume-state")
	if resumeState != "" {
		if err := sendResumeKickoff(executor, sessionID, "supervisor", resumeState); err != nil {
			return fmt.Errorf("send resume kickoff: %w", err)
		}
	}
	fmt.Printf("Attaching to session '%s'...\n", sessionID)
	return executor.AttachSession(sessionID)
}

// resolveProjectPath resolves the start-attach project directory from an optional
// positional argument (falling back to the current working directory) and returns
// its canonicalized absolute path (filepath.Abs then filepath.EvalSymlinks),
// erroring when the resolved path does not exist or is not a directory.
func resolveProjectPath(args []string) (string, error) {
	var path string
	if len(args) > 0 {
		path = args[0]
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get current directory: %w", err)
		}
		path = wd
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path: %w", err)
	}
	canonicalPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks: %w", err)
	}
	info, err := os.Stat(canonicalPath)
	if err != nil {
		return "", fmt.Errorf("stat project path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", canonicalPath)
	}
	return canonicalPath, nil
}

// sendResumeKickoff sends the resume kickoff message (threading resumeStateFile)
// to the supervisor window BEFORE the blocking AttachSession, so a resumed session
// hands the supervisor its resume-state pointer. It is a separate injectable helper
// because runStartAttach builds its executor internally and then blocks on attach.
func sendResumeKickoff(executor tmux.TmuxExecutor, sessionID, supervisorWindowID, resumeStateFile string) error {
	msg := fmt.Sprintf("Resume state: read %s and continue the interrupted work.", resumeStateFile)
	return executor.SendMessage(sessionID, supervisorWindowID, msg)
}

func runProjectInit(cmd *cobra.Command, args []string) error {
	var projectDir string
	if len(args) > 0 {
		projectDir = args[0]
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get current directory: %w", err)
		}
		projectDir = wd
	}
	noAttach, _ := cmd.Flags().GetBool("no-attach")
	executor := tmux.NewTmuxExecutor()
	return runProjectInitWithExecutor(executor, projectDir, noAttach)
}

func runProjectInitWithExecutor(executor tmux.TmuxExecutor, projectDir string, noAttach bool) error {
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return fmt.Errorf("create project directory: %w", err)
	}

	if _, err := os.Stat(filepath.Join(projectDir, ".git")); os.IsNotExist(err) {
		if err := exec.Command("git", "init", "--quiet", projectDir).Run(); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
	}

	settingPath := filepath.Join(projectDir, ".tmux-cli", "setting.yaml")
	if _, err := os.Stat(settingPath); os.IsNotExist(err) {
		if err := setup.SaveSettings(projectDir, setup.DefaultSettings()); err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
	}

	if err := os.MkdirAll(filepath.Join(projectDir, ".tmux-cli", "goals"), 0o755); err != nil {
		return fmt.Errorf("create goals directory: %w", err)
	}

	claudeMDPath := filepath.Join(projectDir, "CLAUDE.md")
	if _, err := os.Stat(claudeMDPath); os.IsNotExist(err) {
		content := fmt.Sprintf("# %s\n", filepath.Base(projectDir))
		if err := os.WriteFile(claudeMDPath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write CLAUDE.md: %w", err)
		}
	}

	if err := runAutoSetup(projectDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: auto-setup: %v\n", err)
	}

	absPath, err := filepath.Abs(projectDir)
	if err != nil {
		return fmt.Errorf("resolve absolute path: %w", err)
	}
	canonicalPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	var sessionID string
	existingID, _ := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", canonicalPath)
	if existingID != "" {
		if running, _ := executor.HasSession(existingID); running {
			sessionID = existingID
		}
	}

	if sessionID == "" {
		sessionID = session.GenerateSessionID(canonicalPath)
		mgr := session.NewSessionManager(executor)
		if err := mgr.CreateSession(sessionID, canonicalPath); err != nil {
			return fmt.Errorf("create session: %w", err)
		}
	}

	mgr := session.NewSessionManager(executor)
	if err := mgr.EnsureTaskvisorWindow(sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: taskvisor setup: %v\n", err)
	}

	fmt.Printf("Session: %s\n", sessionID)

	if !noAttach {
		return executor.AttachSession(sessionID)
	}

	return nil
}

func runSessionKill(cmd *cobra.Command, args []string) error {
	sessionID := args[0]

	executor := tmux.NewTmuxExecutor()
	_ = executor.KillSession(sessionID)

	fmt.Printf("Session %s killed\n", sessionID)
	return nil
}

// Update determineExitCode to handle new error types
func init() {
	// Override the basic determineExitCode with our enhanced version
}

func runSessionList(cmd *cobra.Command, args []string) error {
	executor := tmux.NewTmuxExecutor()

	sessionIDs, err := executor.ListSessions()
	if err != nil {
		// No tmux server running = no sessions
		fmt.Println("No active sessions")
		return nil
	}

	if len(sessionIDs) == 0 {
		fmt.Println("No active sessions")
		return nil
	}

	fmt.Println("Active Sessions (from tmux server):")
	fmt.Println()

	for _, id := range sessionIDs {
		// Try to get project path from tmux environment
		path, err := executor.GetSessionEnvironment(id, "TMUX_CLI_PROJECT_PATH")
		if err != nil {
			fmt.Printf("ID: %s\n\n", id)
		} else {
			fmt.Printf("ID: %s\n  Path: %s\n\n", id, path)
		}
	}

	fmt.Printf("Total: %d active sessions\n", len(sessionIDs))
	return nil
}

// statusJSONReport is the machine-readable snapshot emitted by
// `tmux-cli status --json`. Field order matches the documented shape; the
// JSON tags are the stable contract the dispatcher host-side reporter consumes.
type statusJSONReport struct {
	Project         string `json:"project"`
	SessionUp       bool   `json:"sessionUp"`
	TaskvisorActive bool   `json:"taskvisorActive"`
	RuntimeState    string `json:"runtimeState"`
	Activity        string `json:"activity"`
	LaneNew         *int   `json:"laneNew"`
}

// buildStatusReport gathers this directory's worker state into a statusJSONReport
// using the same helpers the human status path relies on. It never returns an
// error: a missing session yields sessionUp=false / runtimeState="down".
func buildStatusReport(cwd string) statusJSONReport {
	executor := tmux.NewTmuxExecutor()

	rep := statusJSONReport{}

	// project: reuse the api-project lane resolution (empty on error).
	if cfg, err := producer.LoadConfig(cwd); err == nil {
		rep.Project = cfg.Project
	}

	// sessionUp: a discoverable, live tmux session for this directory.
	sessionID, _ := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", cwd)
	if sessionID != "" {
		if up, err := executor.HasSession(sessionID); err == nil {
			rep.SessionUp = up
		}
	}

	// taskvisorActive: the daemon's active marker.
	if _, err := os.Stat(filepath.Join(cwd, ".tmux-cli", "taskvisor-active")); err == nil {
		rep.TaskvisorActive = true
	}

	// goalWindowsOpen: any supervisor/validator/execute/investigator window.
	goalWindowsOpen := false
	if rep.SessionUp {
		if windows, err := executor.ListWindows(sessionID); err == nil {
			for _, w := range windows {
				if strings.HasPrefix(w.Name, "supervisor-") ||
					strings.HasPrefix(w.Name, "validator-") ||
					strings.HasPrefix(w.Name, "execute-") ||
					strings.HasPrefix(w.Name, "investigator-") {
					goalWindowsOpen = true
					break
				}
			}
		}
	}

	// runtimeState: paused (marker) > consuming > idle > down.
	switch {
	case fileExists(filepath.Join(cwd, ".tmux-cli", "PAUSED")):
		rep.RuntimeState = "paused"
	case rep.TaskvisorActive || goalWindowsOpen:
		rep.RuntimeState = "consuming"
	case rep.SessionUp:
		rep.RuntimeState = "idle"
	default:
		rep.RuntimeState = "down"
	}

	// activity: the current (or first running) goal as "<id>: <description>".
	if gf, err := taskvisor.LoadGoals(cwd); err == nil && gf != nil {
		id := gf.CurrentGoal
		if id == "" {
			if running, ok := gf.FirstRunningGoalID(); ok {
				id = running
			}
		}
		if id != "" {
			rep.Activity = id
			for i := range gf.Goals {
				if gf.Goals[i].ID == id {
					if gf.Goals[i].Description != "" {
						rep.Activity = id + ": " + gf.Goals[i].Description
					}
					break
				}
			}
		}
	}

	// laneNew: the host reporter already has be-queue-count; leave null.
	return rep
}

// fileExists reports whether path exists (any stat success).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runSessionStatus(cmd *cobra.Command, args []string) error {
	executor := tmux.NewTmuxExecutor()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	// Machine-readable path: emit one JSON object and return (exit 0 even when
	// no session exists). Placed before the human session-discovery error return.
	if statusJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(buildStatusReport(cwd))
	}

	// Discover session for this directory
	sessionID, _ := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", cwd)
	if sessionID == "" {
		return fmt.Errorf("no tmux-cli session found for this directory")
	}

	// Check if running
	running, err := executor.HasSession(sessionID)
	if err != nil {
		running = false
	}

	var statusStr string
	if running {
		statusStr = "Active (tmux session running)"
	} else {
		statusStr = "Not running"
	}

	// Get project path from environment
	path, _ := executor.GetSessionEnvironment(sessionID, "TMUX_CLI_PROJECT_PATH")

	fmt.Println("Session Status:")
	fmt.Println()
	fmt.Printf("ID: %s\n", sessionID)
	fmt.Printf("Path: %s\n", path)
	fmt.Printf("Status: %s\n", statusStr)

	// List windows if running
	if running {
		windows, err := executor.ListWindows(sessionID)
		if err == nil {
			fmt.Println()
			fmt.Printf("Windows (%d):\n", len(windows))
			for _, w := range windows {
				fmt.Printf("  %s (%s)\n", w.TmuxWindowID, w.Name)
			}
		}
	}

	return nil
}

// determineExitCodeEnhanced maps errors to appropriate exit codes following AR8
func determineExitCodeEnhanced(err error) int {
	if err == nil {
		return ExitSuccess
	}

	switch {
	case errors.Is(err, tmux.ErrTmuxNotFound):
		return ExitCommandNotFound // 126
	case errors.Is(err, session.ErrInvalidUUID):
		return ExitUsageError // 2
	case errors.As(err, &UsageError{}):
		return ExitUsageError // 2
	default:
		return ExitGeneralError // 1
	}
}

func runWindowsCreate(cmd *cobra.Command, args []string) error {
	executor := tmux.NewTmuxExecutor()

	if windowName == "" {
		return NewUsageError("--name flag is required")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	// Discover session
	sessionID, _ := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", cwd)
	if sessionID == "" {
		return fmt.Errorf("no tmux-cli session found for this directory")
	}

	// Validate window name uniqueness (case-insensitive)
	windows, err := executor.ListWindows(sessionID)
	if err != nil {
		return fmt.Errorf("list windows: %w", err)
	}
	for _, w := range windows {
		if strings.EqualFold(w.Name, windowName) {
			return NewUsageError(fmt.Sprintf("window name %q already exists (found as %q in window %s)", windowName, w.Name, w.TmuxWindowID))
		}
	}

	// Generate UUID
	windowUUID := session.GenerateUUID()

	// Create window
	windowID, err := executor.CreateWindow(sessionID, windowName, "")
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}

	// Set window UUID
	err = executor.SetWindowOption(sessionID, windowID, tmux.WindowUUIDOption, windowUUID)
	if err != nil {
		_ = executor.KillWindow(sessionID, windowID)
		return fmt.Errorf("set window UUID: %w", err)
	}

	// Export UUID in running shell
	if err := session.ValidateUUID(windowUUID); err != nil {
		_ = executor.KillWindow(sessionID, windowID)
		return fmt.Errorf("invalid window UUID: %w", err)
	}
	exportCmd := fmt.Sprintf("export TMUX_WINDOW_UUID=\"%s\"", windowUUID)
	err = executor.SendMessage(sessionID, windowID, exportCmd)
	if err != nil {
		_ = executor.KillWindow(sessionID, windowID)
		return fmt.Errorf("export TMUX_WINDOW_UUID in shell: %w", err)
	}

	// Execute postcommand (non-fatal) — inherit the session's --model when set.
	model, _ := executor.GetSessionEnvironment(sessionID, "TMUX_CLI_MODEL")
	postCmdConfig := session.PostCommandConfigWithModel(model)
	err = session.ExecutePostCommandWithFallback(executor, sessionID, windowID, postCmdConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: post-command execution failed for window %s: %v\n", windowID, err)
	}

	fmt.Printf("Window created: %s (name: %s)\n", windowID, windowName)
	return nil
}

func runWindowsList(cmd *cobra.Command, args []string) error {
	executor := tmux.NewTmuxExecutor()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	// Discover session
	sessionID, _ := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", cwd)
	if sessionID == "" {
		return fmt.Errorf("no tmux-cli session found for this directory")
	}

	// List windows from tmux
	windows, err := executor.ListWindows(sessionID)
	if err != nil {
		return fmt.Errorf("list windows: %w", err)
	}

	fmt.Printf("Windows in session %s:\n\n", sessionID)

	if len(windows) == 0 {
		fmt.Println("No windows in session")
		return nil
	}

	for _, w := range windows {
		fmt.Printf("ID: %s\n", w.TmuxWindowID)
		fmt.Printf("Name: %s\n", w.Name)
		fmt.Printf("Command: %s\n", w.CurrentCommand)
		fmt.Println()
	}

	fmt.Printf("Total: %d windows\n", len(windows))
	return nil
}

// validateWindowID validates the format of a window ID
func validateWindowID(windowID string) error {
	if len(windowID) == 0 {
		return fmt.Errorf("window ID cannot be empty")
	}

	if windowID[0] != '@' {
		return fmt.Errorf("window ID must start with @ (e.g., @0, @1)")
	}

	numPart := windowID[1:]
	if len(numPart) == 0 {
		return fmt.Errorf("window ID must have a number after @ (e.g., @0, @1)")
	}

	for _, c := range numPart {
		if c < '0' || c > '9' {
			return fmt.Errorf("window ID must be @ followed by digits (e.g., @0, @1)")
		}
	}

	return nil
}

func runWindowsKill(cmd *cobra.Command, args []string) error {
	if err := validateWindowID(windowIDFlag); err != nil {
		return NewUsageError(err.Error())
	}

	executor := tmux.NewTmuxExecutor()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	// Discover session
	sessionID, _ := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", cwd)
	if sessionID == "" {
		return fmt.Errorf("no tmux-cli session found for this directory")
	}

	// Validate window exists in tmux
	windows, err := executor.ListWindows(sessionID)
	if err != nil {
		return fmt.Errorf("list windows: %w", err)
	}

	var windowName string
	windowFoundInTmux := false
	for _, w := range windows {
		if w.TmuxWindowID == windowIDFlag {
			windowFoundInTmux = true
			windowName = w.Name
			break
		}
	}
	if !windowFoundInTmux {
		return fmt.Errorf("window %s not found in tmux session %s", windowIDFlag, sessionID)
	}

	// Check not last window
	if len(windows) <= 1 {
		return fmt.Errorf("cannot kill last window in session (would terminate session)")
	}

	// Kill window
	err = executor.KillWindow(sessionID, windowIDFlag)
	if err != nil {
		return fmt.Errorf("kill window: %w", err)
	}

	fmt.Printf("Window %s (%s) killed\n", windowIDFlag, windowName)
	return nil
}

func runSessionSend(cmd *cobra.Command, args []string) error {
	if sendWindowID == "" {
		return NewUsageError("window identifier is required (use --window-id flag)")
	}
	if sendMessage == "" {
		return NewUsageError("message is required (use --message flag)")
	}

	executor := tmux.NewTmuxExecutor()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	// Discover session
	sessionID, _ := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", cwd)
	if sessionID == "" {
		return fmt.Errorf("no tmux-cli session found for this directory")
	}

	// List windows for resolution
	windows, err := executor.ListWindows(sessionID)
	if err != nil {
		return fmt.Errorf("list windows: %w", err)
	}

	// Resolve window identifier
	resolvedWindowID, err := ResolveWindowIdentifier(windows, sendWindowID)
	if err != nil {
		return fmt.Errorf("resolve window identifier: %w", err)
	}

	// Get window name for feedback
	var resolvedWindowName string
	for _, w := range windows {
		if w.TmuxWindowID == resolvedWindowID {
			resolvedWindowName = w.Name
			break
		}
	}

	// Send message
	err = executor.SendMessage(sessionID, resolvedWindowID, sendMessage)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}

	fmt.Printf("Message sent to window %s (%s) in session %s\n",
		resolvedWindowID, resolvedWindowName, sessionID)
	return nil
}

func runWindowsMessage(cmd *cobra.Command, args []string) error {
	if messageReceiver == "" {
		return NewUsageError("receiver window identifier is required (use --receiver flag)")
	}
	if messageText == "" {
		return NewUsageError("message is required (use --message flag)")
	}

	executor := tmux.NewTmuxExecutor()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	// Discover session
	sessionID, _ := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", cwd)
	if sessionID == "" {
		return fmt.Errorf("no tmux-cli session found for this directory")
	}

	// List windows
	windows, err := executor.ListWindows(sessionID)
	if err != nil {
		return fmt.Errorf("list windows: %w", err)
	}

	// Auto-detect sender from TMUX_WINDOW_UUID
	senderUUID := os.Getenv("TMUX_WINDOW_UUID")
	sender := sessionID

	if senderUUID != "" {
		for _, w := range windows {
			uuid, err := executor.GetWindowOption(sessionID, w.TmuxWindowID, tmux.WindowUUIDOption)
			if err == nil && uuid == senderUUID {
				sender = w.Name
				break
			}
		}
	}

	// Resolve receiver
	receiverWindowID, err := ResolveWindowIdentifier(windows, messageReceiver)
	if err != nil {
		return fmt.Errorf("resolve receiver window identifier: %w", err)
	}

	var receiverWindowName string
	for _, w := range windows {
		if w.TmuxWindowID == receiverWindowID {
			receiverWindowName = w.Name
			break
		}
	}

	// Build formatted message
	formattedMessage := fmt.Sprintf("New message from: %s\n\n%s\n",
		sender, messageText)

	// Send with delay
	err = executor.SendMessageWithDelay(sessionID, receiverWindowID, formattedMessage)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}

	fmt.Printf("Message sent from %s to window %s (%s) in session %s\n",
		sender, receiverWindowID, receiverWindowName, sessionID)
	return nil
}

// runNotifyOrchestrator delivers args[0] to the orchestrator pane named by
// TMUX_CLI_ORCHESTRATOR_PANE. Env reading lives here (the wrapper); the testable
// core notifyOrchestrator stays env-free so it can be exercised with a mock.
// TMUX_CLI_NOTIFY_RECEIPT (optional) names a receipt file that records each
// successfully delivered message — a deterministic proof surface for the
// e2e-evaluator handshake.
func runNotifyOrchestrator(cmd *cobra.Command, args []string) error {
	pane := strings.TrimSpace(os.Getenv("TMUX_CLI_ORCHESTRATOR_PANE"))
	receipt := strings.TrimSpace(os.Getenv("TMUX_CLI_NOTIFY_RECEIPT"))
	return notifyOrchestrator(tmux.NewTmuxExecutor(), pane, args[0], receipt)
}

// notifyOrchestrator is the testable core of the notify-orchestrator command.
// A missing/empty pane id is a loud usage error (exit 2) with NO send attempted.
// An empty message is intentionally allowed — it delivers a bare Enter ping.
// After a successful pane delivery, a non-empty receiptPath gets the delivered
// message appended (plus newline). Receipt writes FAIL OPEN: the pane delivery
// already succeeded, so a write error only warns on stderr and returns nil.
func notifyOrchestrator(executor tmux.TmuxExecutor, pane, msg, receiptPath string) error {
	if pane == "" {
		return NewUsageError("TMUX_CLI_ORCHESTRATOR_PANE is not set; cannot notify orchestrator")
	}
	if err := executor.NotifyPane(pane, msg); err != nil {
		return fmt.Errorf("notify orchestrator pane %s: %w", pane, err)
	}
	fmt.Printf("Notified orchestrator pane %s\n", pane)
	if receiptPath != "" {
		if err := appendNotifyReceipt(receiptPath, msg); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: notify receipt %s: %v\n", receiptPath, err)
		}
	}
	return nil
}

// appendNotifyReceipt appends msg plus a trailing newline to receiptPath,
// creating the parent directory if missing.
func appendNotifyReceipt(receiptPath, msg string) error {
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(receiptPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(msg + "\n")
	return err
}

func runWindowsUuid(cmd *cobra.Command, args []string) error {
	if err := validateWindowID(windowIDFlag); err != nil {
		return NewUsageError(err.Error())
	}

	executor := tmux.NewTmuxExecutor()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	// Discover session
	sessionID, _ := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", cwd)
	if sessionID == "" {
		return fmt.Errorf("no tmux-cli session found for this directory")
	}

	// Get UUID from tmux user-option
	uuid, err := executor.GetWindowOption(sessionID, windowIDFlag, tmux.WindowUUIDOption)
	if err != nil {
		return fmt.Errorf("get window UUID: %w", err)
	}

	fmt.Println(uuid)
	return nil
}

func runSudo(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	executor := tmux.NewTmuxExecutor()
	sessionID, _ := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", cwd)
	if sessionID == "" {
		return fmt.Errorf("no tmux-cli session found for this directory")
	}

	password, err := executor.GetSessionEnvironment(sessionID, "TMUX_CLI_SUDO_PASS")
	if err != nil || password == "" {
		return fmt.Errorf("sudo not configured — start session with --sudo flag")
	}

	var timeoutInput *int
	if cmd.Flags().Changed("timeout") {
		v, _ := cmd.Flags().GetInt("timeout")
		timeoutInput = &v
	}
	timeout := sudo.ResolveTimeout(timeoutInput, cwd)

	sudoExec := sudo.NewExecutor(password, time.Duration(timeout)*time.Second)

	start := time.Now()
	execErr := sudoExec.ExecuteStream(context.Background(), args[0], os.Stdout, os.Stderr)
	logSudoResult(cwd, args[0], execErr, time.Since(start))
	return execErr
}

func logSudoResult(workDir, command string, execErr error, elapsed time.Duration) {
	entry := sudo.LogEntry{
		Command:    command,
		DurationMs: elapsed.Milliseconds(),
	}
	if execErr != nil {
		entry.Error = execErr.Error()
		entry.ExitCode = 1
		var exitErr *exec.ExitError
		if errors.As(execErr, &exitErr) {
			entry.ExitCode = exitErr.ExitCode()
		}
	}
	sudo.LogExecution(workDir, entry)
}

// buildCommandTemplates walks the embedded command FS into a relPath→content map.
// Shared by runAutoSetup and the daemon's stale-binary command-refresh hook so the
// installed .claude/commands/tmux/ tree matches what runAutoSetup writes.
func buildCommandTemplates() map[string]string {
	cmdTemplates := make(map[string]string)
	fs.WalkDir(embeddedCommands, "embedded/commands/tmux", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, readErr := embeddedCommands.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		relPath := strings.TrimPrefix(path, "embedded/commands/tmux/")
		cmdTemplates[relPath] = string(content)
		return nil
	})
	return cmdTemplates
}

func runAutoSetup(projectPath string) error {
	hookScripts := map[string]string{
		"tmux-session-notify.sh":      hookSessionNotify,
		"tmux-validate-session.sh":    hookValidateSession,
		"no-interactive-questions.sh": hookNoInteractiveQuestions,
		"tmux-supervisor-cycle.sh":    hookSupervisorCycle,
		"tmux-unplanned-audit.sh":     hookUnplannedAudit,
		"tmux-window-watchdog.sh":     hookWindowWatchdog,
	}

	cmdTemplates := buildCommandTemplates()

	tplMap := make(map[string]string)
	fs.WalkDir(embeddedTemplates, "embedded/templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, readErr := embeddedTemplates.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		relPath := strings.TrimPrefix(path, "embedded/templates/")
		tplMap[relPath] = string(content)
		return nil
	})

	rulesMap := make(map[string]string)
	fs.WalkDir(embeddedRules, "embedded/rules", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, readErr := embeddedRules.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		relPath := strings.TrimPrefix(path, "embedded/rules/")
		rulesMap[relPath] = string(content)
		return nil
	})

	return setup.Run(&setup.SetupConfig{
		ProjectRoot:      projectPath,
		HookScripts:      hookScripts,
		CommandTemplates: cmdTemplates,
		Templates:        tplMap,
		Rules:            rulesMap,
	})
}
