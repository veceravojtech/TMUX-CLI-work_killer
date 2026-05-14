package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"os/exec"

	"github.com/console/tmux-cli/internal/session"
	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/sudo"
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

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show detailed status of the session for this directory",
	Long:  `Display detailed information about the tmux-cli session for the current directory.`,
	RunE:  runSessionStatus,
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

var (
	windowName      string
	windowIDFlag    string
	sendWindowID    string
	sendMessage     string
	messageReceiver string
	messageText     string
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

	// Add all commands directly to root
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(startAttachCmd)
	rootCmd.AddCommand(killCmd)
	rootCmd.AddCommand(listCmd)
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
	return os.RemoveAll(filepath.Join(projectPath, ".tmux-cli"))
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
	sudoFlag, _ := cmd.Flags().GetBool("sudo")
	if sudoFlag {
		if err := promptAndStoreSudoPassword(executor, sessionID); err != nil {
			return fmt.Errorf("sudo setup failed: %w", err)
		}
	}
	fmt.Printf("Attaching to session '%s'...\n", sessionID)
	return executor.AttachSession(sessionID)
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

func runSessionStatus(cmd *cobra.Command, args []string) error {
	executor := tmux.NewTmuxExecutor()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
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
	windowID, err := executor.CreateWindow(sessionID, windowName, "zsh")
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

func runAutoSetup(projectPath string) error {
	hookScripts := map[string]string{
		"tmux-session-notify.sh":      hookSessionNotify,
		"tmux-validate-session.sh":    hookValidateSession,
		"no-interactive-questions.sh": hookNoInteractiveQuestions,
		"tmux-supervisor-cycle.sh":    hookSupervisorCycle,
		"tmux-unplanned-audit.sh":     hookUnplannedAudit,
	}

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

	return setup.Run(&setup.SetupConfig{
		ProjectRoot:      projectPath,
		HookScripts:      hookScripts,
		CommandTemplates: cmdTemplates,
	})
}
