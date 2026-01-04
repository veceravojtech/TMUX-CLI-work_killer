package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/console/tmux-cli/internal/recovery"
	"github.com/console/tmux-cli/internal/session"
	"github.com/console/tmux-cli/internal/store"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Create a new tmux session",
	Long: `Create a new detached tmux session with UUID identifier and project path.

The session will be created in tmux and persisted to the JSON store for recovery.
Use a valid UUID v4 for the session ID.`,
	RunE: runSessionStart,
}

var killCmd = &cobra.Command{
	Use:   "kill",
	Short: "Kill a tmux session (preserves file for recovery)",
	Long: `Kill a tmux session while preserving its JSON file in the active directory.
The session can be automatically recovered when accessed later.`,
	RunE: runSessionKill,
}

var endCmd = &cobra.Command{
	Use:   "end",
	Short: "End a session permanently (archives file to ended/)",
	Long: `Kill a tmux session and archive its JSON file to the ended/ directory.
This signals permanent completion - the session will NOT be recovered.`,
	RunE: runSessionEnd,
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all active sessions",
	Long:  `List all active sessions from the sessions directory (excludes ended sessions).`,
	RunE:  runSessionList,
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show detailed status of a specific session",
	Long:  `Display detailed information about a session including its running state.`,
	RunE:  runSessionStatus,
}

var windowsCreateCmd = &cobra.Command{
	Use:   "windows-create",
	Short: "Create a new window in the session",
	Long: `Create a new window in the session. The window will start with zsh and automatically
execute the configured postcommand (if enabled in .tmux-session file).

Example:
  tmux-cli windows-create --name editor`,
	RunE: runWindowsCreate,
}

var windowsListCmd = &cobra.Command{
	Use:   "windows-list",
	Short: "List all windows in the session",
	Long: `List all windows in the session with their IDs, names, and recovery commands.

Shows windows from both the session file (JSON) and live tmux state.
If session is killed, shows what windows WILL be recovered.

Example:
  tmux-cli windows-list --id abc-123`,
	RunE: runWindowsList,
}

var windowsGetCmd = &cobra.Command{
	Use:   "windows-get",
	Short: "Get details of a specific window",
	Long: `Get detailed information about a specific window by its tmux window ID.

Shows window ID, name, recovery command, and current status (running or not).

Example:
  tmux-cli windows-get --id abc-123 --window-id @0`,
	RunE: runWindowsGet,
}

var windowsKillCmd = &cobra.Command{
	Use:   "windows-kill",
	Short: "Kill a window in the session",
	Long: `Kill (remove) a window from the session by its tmux window ID.

The window is killed in the running tmux session and removed from the session file
to prevent recovery.

Example:
  tmux-cli windows-kill --id abc-123 --window-id @1`,
	RunE: runWindowsKill,
}

var windowsSendCmd = &cobra.Command{
	Use:   "windows-send",
	Short: "Send a text message to a specific window",
	Long: `Send a text message to a specific window in a session.

This command enables inter-window communication, allowing Claude instances
running in different windows to coordinate and share status updates.

The message is delivered to the first pane of the target window and
automatically presses Enter to execute it.

Window Identifier:
  - Use window ID (e.g., @0, @1) for direct window access
  - Use window name (e.g., "supervisor", "bmad-worker") for friendly access

Examples:
  # Send status update from worker to supervisor using window ID
  tmux-cli windows-send --id $SESSION --window-id @0 --message "Task complete"

  # Send command to worker window using window name
  tmux-cli windows-send --id $SESSION --window-id supervisor --message "restart"

  # Send command to worker window using window ID
  tmux-cli windows-send --id $SESSION --window-id @1 --message "python train.py"

If the session is killed, automatic recovery will be triggered transparently.`,
	RunE: runSessionSend,
}

var windowsUuidCmd = &cobra.Command{
	Use:   "windows-uuid",
	Short: "Get the persistent UUID of a window",
	Long: `Get the persistent UUID of a window by its tmux window ID.

Returns the UUID that was generated when the window was created.
The UUID persists across session recovery and restarts.

Example:
  tmux-cli windows-uuid --window-id @0
  export WINDOW_UUID=$(tmux-cli windows-uuid --window-id @0)`,
	RunE: runWindowsUuid,
}

var windowsCaptureCmd = &cobra.Command{
	Use:   "windows-capture",
	Short: "Capture the current pane output from a window",
	Long: `Capture the current pane content from a specific window.

This command captures all visible text and scrollback history from the
window's pane, useful for debugging, logging, or checking command execution results.

Example:
  tmux-cli windows-capture --window-id @0
  tmux-cli windows-capture --window-id @1 > output.txt`,
	RunE: runWindowsCapture,
}

var windowsMessageCmd = &cobra.Command{
	Use:   "windows-message",
	Short: "Send a formatted message to another window",
	Long: `Send a formatted message to another window with auto-detected sender.

This command enables inter-window AI agent communication with a consistent
message format that includes sender identification, message body, and reply instructions.

The message is delivered with a 1-second delay to ensure complete delivery of formatted
multi-line messages.

Window Identifier:
  - Use window ID (e.g., @0, @1) for direct window access
  - Use window name (e.g., "supervisor", "bmad-worker") for friendly access

Examples:
  # Send message from current window to supervisor window
  tmux-cli windows-message --receiver supervisor --message "Task completed successfully"

  # Send message to worker window using window ID
  tmux-cli windows-message --receiver @1 --message "Starting new task"

The message will be formatted as:
  New message from: {sender}

  {message}

  Respond available using: windows-message {sender}`,
	RunE: runWindowsMessage,
}

var (
	projectPath     string
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

	// Add flags to windows-get command
	windowsGetCmd.Flags().StringVar(&windowIDFlag, "window-id", "", "Tmux window ID (e.g., @0, @1)")
	windowsGetCmd.MarkFlagRequired("window-id")

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

	// Add flags to windows-capture command
	windowsCaptureCmd.Flags().StringVar(&windowIDFlag, "window-id", "", "Tmux window ID (e.g., @0, @1)")
	windowsCaptureCmd.MarkFlagRequired("window-id")

	// Add flags to windows-message command
	windowsMessageCmd.Flags().StringVar(&messageReceiver, "receiver", "", "Target window ID or name (e.g., @0, supervisor)")
	windowsMessageCmd.Flags().StringVar(&messageText, "message", "", "Message text to send")
	windowsMessageCmd.MarkFlagRequired("receiver")
	windowsMessageCmd.MarkFlagRequired("message")

	// Add all commands directly to root
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(killCmd)
	rootCmd.AddCommand(endCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(windowsCreateCmd)
	rootCmd.AddCommand(windowsListCmd)
	rootCmd.AddCommand(windowsGetCmd)
	rootCmd.AddCommand(windowsKillCmd)
	rootCmd.AddCommand(windowsSendCmd)
	rootCmd.AddCommand(windowsUuidCmd)
	rootCmd.AddCommand(windowsCaptureCmd)
	rootCmd.AddCommand(windowsMessageCmd)
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

func runSessionStart(cmd *cobra.Command, args []string) error {
	// Use current directory as project path
	var err error
	projectPath, err = os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	// Create dependencies
	executor := tmux.NewTmuxExecutor()
	fileStore, err := store.NewFileSessionStore()
	if err != nil {
		return fmt.Errorf("initialize session store: %w", err)
	}
	manager := session.NewSessionManager(executor, fileStore)
	recoveryManager := recovery.NewSessionRecoveryManager(fileStore, executor)

	// 1. Check if session already exists for this path
	existingSession, err := fileStore.Load(projectPath)
	if err != nil && !errors.Is(err, store.ErrSessionNotFound) {
		return fmt.Errorf("load existing session: %w", err)
	}

	if existingSession != nil {
		// Found existing session for this path
		finalSessionID := existingSession.SessionID

		// Recover if needed (session killed but file exists)
		err := MaybeRecoverSession(existingSession, recoveryManager)
		if err != nil {
			return fmt.Errorf("recovery failed: %w", err)
		}

		fmt.Printf("Using existing session '%s' for %s\n", finalSessionID, projectPath)
		return nil
	}

	// 2. No existing session - create new one with auto-generated UUID
	finalSessionID := session.GenerateUUID()

	// Create new session
	if err := manager.CreateSession(finalSessionID, projectPath); err != nil {
		return err
	}

	fmt.Printf("Created session '%s' for %s\n", finalSessionID, projectPath)
	return nil
}

func runSessionKill(cmd *cobra.Command, args []string) error {
	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	// Create dependencies
	executor := tmux.NewTmuxExecutor()
	fileStore, err := store.NewFileSessionStore()
	if err != nil {
		return fmt.Errorf("initialize session store: %w", err)
	}
	manager := session.NewSessionManager(executor, fileStore)

	// Get session ID from .tmux-session file
	sessionID, err := GetSessionIDFromFile(fileStore)
	if err != nil {
		return err
	}

	// Kill session using project path
	if err := manager.KillSession(cwd); err != nil {
		return err
	}

	fmt.Printf("Session %s killed (file preserved for recovery)\n", sessionID)
	return nil
}

func runSessionEnd(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("end command has been removed - sessions are never archived\n.tmux-session files persist forever for recovery\nuse 'kill' to stop the tmux session or manually delete .tmux-session if needed")
}

// Update determineExitCode to handle new error types
func init() {
	// Override the basic determineExitCode with our enhanced version
	// This is called after the basic version in root.go
}

func runSessionList(cmd *cobra.Command, args []string) error {
	// Create executor to query tmux directly
	executor := tmux.NewTmuxExecutor()

	// List all sessions from tmux server
	sessionIDs, err := executor.ListSessions()
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	// Handle empty list
	if len(sessionIDs) == 0 {
		fmt.Println("No active sessions")
		return nil
	}

	// Display sessions
	fmt.Println("Active Sessions (from tmux server):")
	fmt.Println()

	for _, id := range sessionIDs {
		fmt.Printf("ID: %s\n", id)
		// Note: With new storage model, we can't get path without scanning filesystem
		// Session files are stored in project directories, not centrally
		fmt.Println()
	}

	fmt.Printf("Total: %d active sessions\n", len(sessionIDs))
	fmt.Println("\nNote: Session files are stored in project directories (.tmux-session)")
	return nil
}

// WindowVerificationResult contains verification status for windows
type WindowVerificationResult struct {
	FileWindows     []FileWindowStatus
	OrphanedWindows []OrphanedWindowInfo
	InSync          bool
}

// FileWindowStatus represents status of a window from the session file
type FileWindowStatus struct {
	WindowID string
	Name     string
	Status   string // "Running", "Not Found", "Not Running (session killed)"
}

// OrphanedWindowInfo represents a window in tmux not in the file
type OrphanedWindowInfo struct {
	WindowID string
	Name     string
}

// verifyWindows compares session file windows against live tmux state
func verifyWindows(executor tmux.TmuxExecutor, session *store.Session) WindowVerificationResult {
	result := WindowVerificationResult{
		FileWindows:     make([]FileWindowStatus, 0, len(session.Windows)),
		OrphanedWindows: make([]OrphanedWindowInfo, 0),
		InSync:          true,
	}

	// Get live windows from tmux
	liveWindows, err := executor.ListWindows(session.SessionID)
	if err != nil {
		// Session not running - all file windows are "Not Running"
		for _, window := range session.Windows {
			result.FileWindows = append(result.FileWindows, FileWindowStatus{
				WindowID: window.TmuxWindowID,
				Name:     window.Name,
				Status:   "Not Running (session killed)",
			})
		}
		result.InSync = false
		return result
	}

	// Create lookup map for live windows
	liveWindowMap := make(map[string]tmux.WindowInfo)
	for _, w := range liveWindows {
		liveWindowMap[w.TmuxWindowID] = w
	}

	// Check each file window against live state
	for _, fileWindow := range session.Windows {
		status := FileWindowStatus{
			WindowID: fileWindow.TmuxWindowID,
			Name:     fileWindow.Name,
		}

		if _, exists := liveWindowMap[fileWindow.TmuxWindowID]; exists {
			status.Status = "Running"
			delete(liveWindowMap, fileWindow.TmuxWindowID) // Remove from map
		} else {
			status.Status = "Not Found"
			result.InSync = false
		}

		result.FileWindows = append(result.FileWindows, status)
	}

	// Remaining windows in map are orphaned (in tmux but not in file)
	for _, liveWindow := range liveWindowMap {
		result.OrphanedWindows = append(result.OrphanedWindows, OrphanedWindowInfo{
			WindowID: liveWindow.TmuxWindowID,
			Name:     liveWindow.Name,
		})
		result.InSync = false
	}

	return result
}

func runSessionStatus(cmd *cobra.Command, args []string) error {
	// Create dependencies
	executor := tmux.NewTmuxExecutor()
	fileStore, err := store.NewFileSessionStore()
	if err != nil {
		return fmt.Errorf("initialize session store: %w", err)
	}

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}

	// Load session
	sess, err := fileStore.Load(cwd)
	if err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			return fmt.Errorf("session not found in current directory")
		}
		return fmt.Errorf("load session: %w", err)
	}

	sessionID := sess.SessionID

	// Check for recovery and trigger if needed (Story 3.3)
	recoveryManager := recovery.NewSessionRecoveryManager(fileStore, executor)
	err = MaybeRecoverSession(sess, recoveryManager)
	if err != nil {
		// Non-fatal error - recovery failure shouldn't block status display
		// Just log it and continue
		fmt.Fprintf(os.Stderr, "Warning: Recovery check failed: %v\n", err)
	}

	// Check if tmux session is running
	running, err := executor.HasSession(sessionID)
	if err != nil {
		// Non-fatal - we can still show file-based status
		running = false
	}

	// Determine status string
	var statusStr string
	if running {
		statusStr = "Active (tmux session running)"
	} else {
		statusStr = "Killed (file exists, tmux session not running)"
	}

	// Verify windows against live tmux state
	windowVerification := verifyWindows(executor, sess)

	// Display status (following existing style)
	fmt.Println("Session Status:")
	fmt.Println()
	fmt.Printf("ID: %s\n", sess.SessionID)
	fmt.Printf("Path: %s\n", sess.ProjectPath)
	fmt.Printf("Status: %s\n", statusStr)

	// Display timestamps
	fmt.Println()
	fmt.Println("Timeline:")
	fmt.Printf("  Created:        %s\n", formatTimestamp(sess.CreatedAt))
	if sess.LastRecoveryAt != "" {
		fmt.Printf("  Last Recovery:  %s\n", formatTimestamp(sess.LastRecoveryAt))
	} else {
		fmt.Printf("  Last Recovery:  Never recovered\n")
	}

	// Display window verification
	fmt.Println()
	fmt.Println("Window Verification:")
	if windowVerification.InSync {
		fmt.Println("  ✓ All windows in sync with tmux")
	} else {
		fmt.Println("  ⚠ Warning: Session file is out of sync with tmux")
	}
	fmt.Println()

	// Display file windows
	fmt.Printf("Windows in .tmux-session file (%d):\n", len(windowVerification.FileWindows))
	for _, w := range windowVerification.FileWindows {
		var statusIcon string
		switch w.Status {
		case "Running":
			statusIcon = "✓"
		case "Not Found":
			statusIcon = "✗"
		default:
			statusIcon = "⚠"
		}
		fmt.Printf("  %s %s (%s) - %s\n", statusIcon, w.WindowID, w.Name, w.Status)
	}

	// Display orphaned windows if any
	if len(windowVerification.OrphanedWindows) > 0 {
		fmt.Println()
		fmt.Printf("Orphaned windows in tmux (%d):\n", len(windowVerification.OrphanedWindows))
		for _, w := range windowVerification.OrphanedWindows {
			fmt.Printf("  ⚠ %s (%s) - Not in session file\n", w.WindowID, w.Name)
		}
	}

	// Display JSON preview
	fmt.Println()
	fmt.Println("JSON File Preview:")
	jsonData, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		// Fallback if JSON formatting fails
		fmt.Println("(Unable to format JSON)")
	} else {
		fmt.Println(string(jsonData))
	}

	return nil
}

// formatTimestamp converts RFC3339 timestamp to human-readable format
func formatTimestamp(rfc3339 string) string {
	if rfc3339 == "" {
		return "N/A"
	}

	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return "Invalid timestamp"
	}

	return t.Format("2006-01-02 15:04:05 MST")
}

// determineExitCodeEnhanced maps errors to appropriate exit codes following AR8
func determineExitCodeEnhanced(err error) int {
	if err == nil {
		return ExitSuccess
	}

	// Check for specific error types
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
	// 1. Create dependencies
	executor := tmux.NewTmuxExecutor()
	fileStore, err := store.NewFileSessionStore()
	if err != nil {
		return fmt.Errorf("initialize session store: %w", err)
	}

	// 2. Validate window name
	if windowName == "" {
		return NewUsageError("--name flag is required")
	}

	// 4. Load session to verify it exists and get session object
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	sess, err := fileStore.Load(cwd)
	if err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			return fmt.Errorf("session not found in current directory")
		}
		return fmt.Errorf("load session: %w", err)
	}

	// 5. Check for recovery and trigger if needed (Story 3.3)
	recoveryManager := recovery.NewSessionRecoveryManager(fileStore, executor)
	err = MaybeRecoverSession(sess, recoveryManager)
	if err != nil {
		return err
	}

	// 6. Generate UUID before creating window
	windowUUID := session.GenerateUUID()

	// 7. Create window in tmux
	windowID, err := executor.CreateWindow(sess.SessionID, windowName, "zsh")
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}

	// 8. Set window UUID immediately after creation
	err = executor.SetWindowOption(sess.SessionID, windowID, tmux.WindowUUIDOption, windowUUID)
	if err != nil {
		// Cleanup: kill the window since UUID setup failed
		_ = executor.KillWindow(sess.SessionID, windowID)
		return fmt.Errorf("set window UUID: %w", err)
	}

	// Export the UUID in the running shell
	// The init command tried to export it when the shell started, but the UUID wasn't set yet
	// Validate UUID before using in shell command (security)
	if err := session.ValidateUUID(windowUUID); err != nil {
		_ = executor.KillWindow(sess.SessionID, windowID)
		return fmt.Errorf("invalid window UUID: %w", err)
	}
	exportCmd := fmt.Sprintf("export TMUX_WINDOW_UUID=\"%s\"", windowUUID)
	err = executor.SendMessage(sess.SessionID, windowID, exportCmd)
	if err != nil {
		// Cleanup: kill the window since UUID export failed
		_ = executor.KillWindow(sess.SessionID, windowID)
		return fmt.Errorf("export TMUX_WINDOW_UUID in shell: %w", err)
	}

	// 9. Create Window struct with UUID (needed for GetEffectivePostCommand)
	newWindow := store.Window{
		TmuxWindowID: windowID,
		Name:         windowName,
		UUID:         windowUUID,
		// RecoveryCommand removed - always uses zsh
	}

	// 10. Execute postcommand if configured (non-fatal)
	postCmdConfig := sess.GetEffectivePostCommand(&newWindow)
	err = session.ExecutePostCommandWithFallback(executor, sess.SessionID, windowID, postCmdConfig)
	if err != nil {
		// Post-command failure is not fatal - window is created and UUID is exported
		// The user can manually run commands in the window
		// TODO: Add logging when logging infrastructure is available
		_ = err // Suppress unused variable warning
	}

	// 11. Append window to session
	sess.Windows = append(sess.Windows, newWindow)

	// 8. Save updated session with atomic write
	err = fileStore.Save(sess)
	if err != nil {
		// Window was created in tmux but persistence failed
		// This is a problem - window won't survive recovery
		return fmt.Errorf("save session: %w (window created in tmux but not persisted!)", err)
	}

	// 9. Success message
	fmt.Printf("Window created: %s (name: %s)\n", windowID, windowName)
	return nil
}

func runWindowsList(cmd *cobra.Command, args []string) error {
	// 1. Create dependencies
	executor := tmux.NewTmuxExecutor()
	fileStore, err := store.NewFileSessionStore()
	if err != nil {
		return fmt.Errorf("initialize session store: %w", err)
	}

	// 2. Load session from store
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	sess, err := fileStore.Load(cwd)
	if err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			return fmt.Errorf("session not found in current directory")
		}
		return fmt.Errorf("load session: %w", err)
	}

	// 4. Check for recovery and trigger if needed (Story 3.3)
	recoveryManager := recovery.NewSessionRecoveryManager(fileStore, executor)
	err = MaybeRecoverSession(sess, recoveryManager)
	if err != nil {
		return err
	}

	// 5. Check if session is running in tmux (should always be true after recovery)
	running, err := executor.HasSession(sess.SessionID)
	if err != nil {
		return fmt.Errorf("check session exists: %w", err)
	}

	// 6. Display windows
	fmt.Printf("Windows in session %s:\n\n", sess.SessionID)

	if len(sess.Windows) == 0 {
		fmt.Println("No windows in session")
		return nil
	}

	// 7. If session was recovered, should be running (no warning needed anymore)
	if !running {
		// This should rarely happen after recovery integration
		fmt.Println("⚠️  Session is not running (killed). Showing persisted windows that will be recovered:")
		fmt.Println()
	}

	// 8. Display each window
	for _, window := range sess.Windows {
		fmt.Printf("ID: %s\n", window.TmuxWindowID)
		fmt.Printf("Name: %s\n", window.Name)
		fmt.Printf("Command: zsh\n") // Always uses zsh
		fmt.Println()
	}

	fmt.Printf("Total: %d windows\n", len(sess.Windows))

	return nil
}

// validateWindowID validates the format of a window ID
// Window IDs must start with @ followed by one or more digits (e.g., @0, @1, @123)
func validateWindowID(windowID string) error {
	if len(windowID) == 0 {
		return fmt.Errorf("window ID cannot be empty")
	}

	if windowID[0] != '@' {
		return fmt.Errorf("window ID must start with @ (e.g., @0, @1)")
	}

	// Extract numeric part
	numPart := windowID[1:]
	if len(numPart) == 0 {
		return fmt.Errorf("window ID must have a number after @ (e.g., @0, @1)")
	}

	// Verify numeric part is all digits
	for _, c := range numPart {
		if c < '0' || c > '9' {
			return fmt.Errorf("window ID must be @ followed by digits (e.g., @0, @1)")
		}
	}

	return nil
}

// findWindowByID searches for a window by its tmux window ID in the session
func findWindowByID(sess *store.Session, windowID string) (*store.Window, error) {
	for i := range sess.Windows {
		if sess.Windows[i].TmuxWindowID == windowID {
			return &sess.Windows[i], nil
		}
	}

	return nil, fmt.Errorf("window %s not found in session", windowID)
}

// getWindowStatus determines the status of a window in a session
func getWindowStatus(executor tmux.TmuxExecutor, sessionID string, windowID string) (string, error) {
	// 1. Check if session is running
	sessionRunning, err := executor.HasSession(sessionID)
	if err != nil {
		return "", fmt.Errorf("check session exists: %w", err)
	}

	if !sessionRunning {
		return "Not running (session killed)", nil
	}

	// 2. Check if window exists in tmux
	windows, err := executor.ListWindows(sessionID)
	if err != nil {
		// Session exists but can't list windows - unusual
		return "Unknown (error listing windows)", err
	}

	// 3. Search for window in live tmux list
	for _, w := range windows {
		if w.TmuxWindowID == windowID {
			return "Running", nil
		}
	}

	// Window not found in live tmux (may have died)
	return "Not found in tmux (may be dead)", nil
}

func runWindowsGet(cmd *cobra.Command, args []string) error {
	// 1. Validate window ID format
	if err := validateWindowID(windowIDFlag); err != nil {
		return NewUsageError(err.Error())
	}

	// 2. Create dependencies
	executor := tmux.NewTmuxExecutor()
	fileStore, err := store.NewFileSessionStore()
	if err != nil {
		return fmt.Errorf("initialize session store: %w", err)
	}

	// 3. Get session ID from .tmux-session file
	sessionID, err := GetSessionIDFromFile(fileStore)
	if err != nil {
		return err
	}

	// 4. Load session from store
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	sess, err := fileStore.Load(cwd)
	if err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			return fmt.Errorf("session not found in current directory")
		}
		return fmt.Errorf("load session: %w", err)
	}

	// 5. Check for recovery and trigger if needed (Story 3.3)
	recoveryManager := recovery.NewSessionRecoveryManager(fileStore, executor)
	err = MaybeRecoverSession(sess, recoveryManager)
	if err != nil {
		return err
	}

	// 6. Find window in session
	window, err := findWindowByID(sess, windowIDFlag)
	if err != nil {
		return fmt.Errorf("window %s not found in session %s", windowIDFlag, sessionID)
	}

	// 7. Get window status
	status, err := getWindowStatus(executor, sess.SessionID, windowIDFlag)
	if err != nil {
		// Non-fatal error, show "Unknown" status
		status = "Unknown"
	}

	// 8. Display window details
	fmt.Println("Window Details:")
	fmt.Println()
	fmt.Printf("Session ID: %s\n", sess.SessionID)
	fmt.Printf("Window ID: %s\n", window.TmuxWindowID)
	fmt.Printf("Name: %s\n", window.Name)
	fmt.Printf("Recovery Command: zsh\n") // Always uses zsh
	fmt.Printf("Status: %s\n", status)

	return nil
}

// runWindowsKill kills a window and removes it from the session file
//
// Invariants and Recovery Behavior (Fixed - Tech Spec):
// - Window must exist in session JSON file before kill (fail-fast if not found)
// - Window must exist in tmux and belong to correct session (F4 validation)
// - File is saved FIRST with window removed (save-before-kill pattern - F3)
// - If tmux kill fails after save, file is rolled back to restore window (F9 atomic)
// - This ensures file is always accurate or behind tmux (safe for recovery)
// - If window exists in tmux but not in file, it's left alone (orphaned)
// - Killing the last window may auto-kill the session (tmux version dependent)
func runWindowsKill(cmd *cobra.Command, args []string) error {
	// 1. Validate window ID format
	if err := validateWindowID(windowIDFlag); err != nil {
		return NewUsageError(err.Error())
	}

	// 2. Create dependencies
	executor := tmux.NewTmuxExecutor()
	fileStore, err := store.NewFileSessionStore()
	if err != nil {
		return fmt.Errorf("initialize session store: %w", err)
	}

	// 3. Get session ID from .tmux-session file
	sessionID, err := GetSessionIDFromFile(fileStore)
	if err != nil {
		return err
	}

	// 4. Load session from store
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	sess, err := fileStore.Load(cwd)
	if err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			return fmt.Errorf("session not found in current directory")
		}
		return fmt.Errorf("load session: %w", err)
	}

	// 5. Check for recovery and trigger if needed
	recoveryManager := recovery.NewSessionRecoveryManager(fileStore, executor)
	err = MaybeRecoverSession(sess, recoveryManager)
	if err != nil {
		return err
	}

	// 6. Find window in session file
	window, err := findWindowByID(sess, windowIDFlag)
	if err != nil {
		return fmt.Errorf("window %s not found in session %s", windowIDFlag, sessionID)
	}

	// 7. Validate window exists in tmux and belongs to this session (F4)
	windows, err := executor.ListWindows(sess.SessionID)
	if err != nil {
		return fmt.Errorf("validate window in tmux: %w", err)
	}
	windowFoundInTmux := false
	for _, w := range windows {
		if w.TmuxWindowID == windowIDFlag {
			windowFoundInTmux = true
			break
		}
	}
	if !windowFoundInTmux {
		return fmt.Errorf("window %s not found in tmux session %s (may belong to different session or already dead)", windowIDFlag, sess.SessionID)
	}

	// 8. Create snapshot of original session for rollback (F9)
	originalWindows := make([]store.Window, len(sess.Windows))
	copy(originalWindows, sess.Windows)

	// 9. Remove window from session slice (in-memory)
	updatedWindows := make([]store.Window, 0, len(sess.Windows))
	for _, w := range sess.Windows {
		if w.TmuxWindowID != windowIDFlag {
			updatedWindows = append(updatedWindows, w)
		}
	}
	sess.Windows = updatedWindows

	// 10. Save updated session FIRST (save-before-kill pattern - F3)
	err = fileStore.Save(sess)
	if err != nil {
		// Save failed, window still exists in tmux - safe state
		return fmt.Errorf("save session: %w", err)
	}

	// 11. Kill window in tmux SECOND
	err = executor.KillWindow(sess.SessionID, windowIDFlag)
	if err != nil {
		// Kill failed after save succeeded - ROLLBACK (F9)
		sess.Windows = originalWindows
		rollbackErr := fileStore.Save(sess)
		if rollbackErr != nil {
			return fmt.Errorf("kill window failed: %w; rollback also failed: %v (manual recovery may be needed)", err, rollbackErr)
		}
		return fmt.Errorf("kill window %s failed: %w (session file rolled back, window restored)", windowIDFlag, err)
	}

	// 12. Success message
	fmt.Printf("Window %s (%s) killed and removed from session\n", windowIDFlag, window.Name)
	return nil
}

func runSessionSend(cmd *cobra.Command, args []string) error {
	// Validate window identifier (ID or name) is provided
	if sendWindowID == "" {
		return NewUsageError("window identifier is required (use --window-id flag with window ID like @0 or window name like 'supervisor')")
	}

	// Validate message is not empty
	if sendMessage == "" {
		return NewUsageError("message is required (use --message flag)")
	}

	// Create dependencies
	executor := tmux.NewTmuxExecutor()
	fileStore, err := store.NewFileSessionStore()
	if err != nil {
		return fmt.Errorf("initialize session store: %w", err)
	}
	recoveryManager := recovery.NewSessionRecoveryManager(fileStore, executor)

	// 1. Load session to validate window exists (FR33)
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	sess, err := fileStore.Load(cwd)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	// 3. Check for recovery and trigger if needed (FR34)
	err = MaybeRecoverSession(sess, recoveryManager)
	if err != nil {
		return err
	}

	// 4. Resolve window identifier (ID or name) to window ID
	resolvedWindowID, err := ResolveWindowIdentifier(sess, sendWindowID)
	if err != nil {
		return fmt.Errorf("resolve window identifier: %w", err)
	}

	// 5. Get window name for feedback
	var windowName string
	for _, window := range sess.Windows {
		if window.TmuxWindowID == resolvedWindowID {
			windowName = window.Name
			break
		}
	}

	// 6. Send message via tmux executor (FR35)
	err = executor.SendMessage(sess.SessionID, resolvedWindowID, sendMessage)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}

	// 7. Success feedback (FR38)
	fmt.Printf("Message sent to window %s (%s) in session %s\n",
		resolvedWindowID, windowName, sess.SessionID)

	return nil
}

func runWindowsMessage(cmd *cobra.Command, args []string) error {
	// Validate receiver (window identifier) is provided
	if messageReceiver == "" {
		return NewUsageError("receiver window identifier is required (use --receiver flag with window ID like @0 or window name like 'supervisor')")
	}

	// Validate message is not empty
	if messageText == "" {
		return NewUsageError("message is required (use --message flag)")
	}

	// Create dependencies
	executor := tmux.NewTmuxExecutor()
	fileStore, err := store.NewFileSessionStore()
	if err != nil {
		return fmt.Errorf("initialize session store: %w", err)
	}
	recoveryManager := recovery.NewSessionRecoveryManager(fileStore, executor)

	// 1. Load session from current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	sess, err := fileStore.Load(cwd)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	// 2. Check for recovery and trigger if needed
	err = MaybeRecoverSession(sess, recoveryManager)
	if err != nil {
		return err
	}

	// 3. Auto-detect sender from TMUX_WINDOW_UUID environment variable
	senderUUID := os.Getenv("TMUX_WINDOW_UUID")
	sender := sess.SessionID // Default fallback to session ID if UUID not found

	// Find window with matching UUID to get its name
	if senderUUID != "" {
		for _, window := range sess.Windows {
			if window.UUID == senderUUID {
				sender = window.Name
				break
			}
		}
	}

	// 4. Resolve receiver window identifier (ID or name) to window ID
	receiverWindowID, err := ResolveWindowIdentifier(sess, messageReceiver)
	if err != nil {
		return fmt.Errorf("resolve receiver window identifier: %w", err)
	}

	// 5. Get receiver window name for feedback
	var receiverWindowName string
	for _, window := range sess.Windows {
		if window.TmuxWindowID == receiverWindowID {
			receiverWindowName = window.Name
			break
		}
	}

	// 6. Build formatted message with sender, message body, and reply instructions
	formattedMessage := fmt.Sprintf("New message from: %s\n\n%s\n\nRespond available using: windows-message %s",
		sender, messageText, sender)

	// 7. Send message with delay via tmux executor
	err = executor.SendMessageWithDelay(sess.SessionID, receiverWindowID, formattedMessage)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}

	// 8. Success feedback
	fmt.Printf("Message sent from %s to window %s (%s) in session %s\n",
		sender, receiverWindowID, receiverWindowName, sess.SessionID)

	return nil
}

func runWindowsUuid(cmd *cobra.Command, args []string) error {
	// 1. Validate window ID format
	if err := validateWindowID(windowIDFlag); err != nil {
		return NewUsageError(err.Error())
	}

	// 2. Create dependencies
	executor := tmux.NewTmuxExecutor()
	fileStore, err := store.NewFileSessionStore()
	if err != nil {
		return fmt.Errorf("initialize session store: %w", err)
	}

	// 3. Get session ID from .tmux-session file
	sessionID, err := GetSessionIDFromFile(fileStore)
	if err != nil {
		return err
	}

	// 4. Load session and maybe recover
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	sess, err := fileStore.Load(cwd)
	if err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			return fmt.Errorf("session not found in current directory")
		}
		return fmt.Errorf("load session: %w", err)
	}

	recoveryManager := recovery.NewSessionRecoveryManager(fileStore, executor)
	err = MaybeRecoverSession(sess, recoveryManager)
	if err != nil {
		return err
	}

	// 5. Get UUID from tmux user-option (primary source)
	uuid, err := executor.GetWindowOption(sess.SessionID, windowIDFlag, tmux.WindowUUIDOption)
	if err != nil {
		// If tmux option fails, try session file backup
		var window *store.Window
		for i := range sess.Windows {
			if sess.Windows[i].TmuxWindowID == windowIDFlag {
				window = &sess.Windows[i]
				break
			}
		}

		if window == nil {
			return fmt.Errorf("window %s not found in session %s", windowIDFlag, sessionID)
		}

		if window.UUID == "" {
			return fmt.Errorf("window %s does not have a UUID (was it created with windows-create?)", windowIDFlag)
		}

		uuid = window.UUID
	}

	// 6. Output just the UUID (for easy shell capture)
	fmt.Println(uuid)
	return nil
}

func runWindowsCapture(cmd *cobra.Command, args []string) error {
	// 1. Validate window ID format
	if err := validateWindowID(windowIDFlag); err != nil {
		return NewUsageError(err.Error())
	}

	// 2. Create dependencies
	executor := tmux.NewTmuxExecutor()
	fileStore, err := store.NewFileSessionStore()
	if err != nil {
		return fmt.Errorf("initialize session store: %w", err)
	}

	// 3. Load session from current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	sess, err := fileStore.Load(cwd)
	if err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			return fmt.Errorf("session not found in current directory")
		}
		return fmt.Errorf("load session: %w", err)
	}

	recoveryManager := recovery.NewSessionRecoveryManager(fileStore, executor)
	err = MaybeRecoverSession(sess, recoveryManager)
	if err != nil {
		return err
	}

	// 5. Capture the pane output
	output, err := executor.CaptureWindowOutput(sess.SessionID, windowIDFlag)
	if err != nil {
		return fmt.Errorf("capture window output: %w", err)
	}

	// 6. Output the captured content
	fmt.Print(output)
	return nil
}
