package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

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
	RunE: runSessionStart,
}

var startAttachCmd = &cobra.Command{
	Use:   "start-attach",
	Short: "Create a new tmux session and attach to it",
	Long: `Create a new detached tmux session for the current directory, then attach to it.

If a session is already running for the current directory, you will be prompted
to recreate it or keep the existing one. After session creation (or reuse),
tmux will attach to the session.`,
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
	Short: "Reset a failed or done goal back to pending",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskvisorGoalReset,
}

var taskvisorGoalPriorityCmd = &cobra.Command{
	Use:   "priority <goal-id> <value>",
	Short: "Set a goal's dispatch priority",
	Args:  cobra.ExactArgs(2),
	RunE:  runTaskvisorGoalPriority,
}

var taskvisorGoalStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Send stop signal to taskvisor daemon",
	Args:  cobra.NoArgs,
	RunE:  runTaskvisorGoalStop,
}

var taskvisorRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the taskvisor daemon",
	Args:  cobra.NoArgs,
	RunE:  runTaskvisorRestart,
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
	goalDescription string
	goalAcceptance  []string
	goalValidate    []string
	goalContext     string
	goalNotInScope  string
	goalPhase       string
	goalMaxRetries  int
	goalScope       []string
	goalPriority    int

	revalForceFull    bool
	revalFinalCycle   bool
	revalChangedFiles []string

	inlineCycleN int
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

	// Add --timeout flag to sudo command
	sudoCmd.Flags().Int("timeout", 0, "Timeout in seconds (0 = no timeout; omit for config default, which is 30s)")

	// Add --clean flag to start and start-attach commands
	startCmd.Flags().Bool("clean", false, "Delete and recreate .tmux-cli/ folder before session creation")
	startAttachCmd.Flags().Bool("clean", false, "Delete and recreate .tmux-cli/ folder before session creation")

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

	taskvisorCmd.AddCommand(taskvisorStartCmd)
	taskvisorCmd.AddCommand(taskvisorRestartCmd)
	taskvisorGoalCmd.AddCommand(taskvisorGoalAddCmd)
	taskvisorGoalCmd.AddCommand(taskvisorGoalListCmd)
	taskvisorGoalCmd.AddCommand(taskvisorGoalDeleteCmd)
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
// and session creation. Returns the session ID (existing or newly created).
func startOrReuseSession(executor tmux.TmuxExecutor, projectPath string) (string, error) {
	// Check if session already exists for this path
	existingSessionID, _ := executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", projectPath)

	if existingSessionID != "" {
		running, _ := executor.HasSession(existingSessionID)
		if running {
			fmt.Printf("Session '%s' is already running for %s\n", existingSessionID, projectPath)
			fmt.Println("What would you like to do?")
			fmt.Println("  [r] Recreate session (kill existing + create new)")
			fmt.Println("  [c] Cancel and keep existing session")
			fmt.Print("Choice (r/c): ")

			var response string
			if _, err := fmt.Scanln(&response); err != nil {
				// EOF or pipe input — treat as cancel
				fmt.Printf("Keeping existing session '%s'\n", existingSessionID)
				return existingSessionID, nil
			}

			if response == "r" || response == "R" {
				if err := executor.KillSession(existingSessionID); err != nil {
					return "", fmt.Errorf("kill existing session '%s': %w", existingSessionID, err)
				}
				// Fall through to create new session
			} else {
				fmt.Printf("Keeping existing session '%s'\n", existingSessionID)
				return existingSessionID, nil
			}
		}
	}

	// Create new session
	sessionID := session.GenerateSessionID(projectPath)
	manager := session.NewSessionManager(executor)

	if err := manager.CreateSession(sessionID, projectPath); err != nil {
		return "", err
	}

	fmt.Printf("Created session '%s' for %s\n", sessionID, projectPath)
	return sessionID, nil
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
	projectPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
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
	sessionID, err := startOrReuseSession(executor, projectPath)
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
	return nil
}

func runStartAttach(cmd *cobra.Command, args []string) error {
	projectPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
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
	sessionID, err := startOrReuseSession(executor, projectPath)
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
	fmt.Printf("Attaching to session '%s'...\n", sessionID)
	return executor.AttachSession(sessionID)
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

	// Execute postcommand (non-fatal)
	postCmdConfig := session.DefaultPostCommandConfig()
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
	if gf == nil {
		return fmt.Errorf("no goals.yaml found — add goals first with 'taskvisor goal add'")
	}

	_, hasPending := gf.NextPendingGoal()
	if !hasPending && !gf.HasRecoverableBlock() {
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

func stopDaemonProcess(cwd string) error {
	pidPath := filepath.Join(cwd, ".tmux-cli", "taskvisor.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read PID file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		_ = os.Remove(pidPath)
		return fmt.Errorf("invalid PID in %s: %w", pidPath, err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidPath)
		return nil
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		_ = os.Remove(pidPath)
		return nil
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			_ = proc // keep vet happy
			_ = os.Remove(pidPath)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Fprintf(os.Stderr, "warning: daemon PID %d did not exit within 10s after SIGTERM\n", pid)
	_ = os.Remove(pidPath)
	return nil
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
	daemon := taskvisor.New(cwd, executor)
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
		if ppErr := executor.PipePane(sessionID, windowID, filepath.Join(paneLogDir, name+".log")); ppErr != nil {
			log.Printf("WARNING: pipe-pane for %s failed (best-effort): %v", name, ppErr)
		}
		exportCmd := fmt.Sprintf("export TMUX_WINDOW_UUID=\"%s\"", windowUUID)
		_ = executor.SendMessage(sessionID, windowID, exportCmd)

		postCmdConfig := session.DefaultPostCommandConfig()
		_ = session.ExecutePostCommandWithFallback(executor, sessionID, windowID, postCmdConfig)

		return &taskvisor.CreatedWindow{TmuxWindowID: windowID, Name: name}, nil
	})

	return daemon.Run(cmd.Context())
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
