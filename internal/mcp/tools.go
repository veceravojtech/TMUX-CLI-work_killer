package mcp

import (
	"fmt"
	"os"
	"strings"

	"github.com/console/tmux-cli/internal/recovery"
	"github.com/console/tmux-cli/internal/session"
	"github.com/console/tmux-cli/internal/store"
	"github.com/console/tmux-cli/internal/tmux"
)

// WindowListItem represents a simplified window entry with only the name.
type WindowListItem struct {
	Name string `json:"name"`
}

// WindowsList returns all windows in the current tmux session.
// Implements FR1: AI assistants can list all windows in the current tmux session
// Implements FR3: AI assistants can identify which window is currently active
// Implements FR4: AI assistants can determine the total number of windows
//
// This method uses direct internal package calls (Decision 3) rather than
// subprocess execution, and returns Go types directly (Decision 4) allowing
// the MCP SDK to handle JSON serialization.
//
// Returns:
//   - []WindowListItem: Array of window names (without IDs or UUIDs)
//   - error: ErrSessionNotFound if session file cannot be loaded
//
// Performance: Expected to complete in <10ms, well under NFR-P1 requirement of 2s.
func (s *Server) WindowsList() ([]WindowListItem, error) {
	// Load session from working directory using SessionStore (direct internal call per Decision 3)
	// Load() expects project path, not full session file path
	session, err := s.store.Load(s.workingDir)
	if err != nil {
		// Use categorized error with context (STRICT Rule 9)
		return nil, fmt.Errorf("%w in directory %s: expected %s",
			ErrSessionNotFound, s.workingDir, s.sessionFile)
	}

	// Map to WindowListItem array with only names (no IDs or UUIDs)
	result := make([]WindowListItem, len(session.Windows))
	for i := range session.Windows {
		result[i] = WindowListItem{
			Name: session.Windows[i].Name,
		}
	}

	// Return window names array - MCP SDK handles JSON serialization (Decision 4)
	return result, nil
}

// resolveWindowIdentifier resolves a window identifier (ID or name) to a window ID.
// If the identifier starts with "@", it's treated as a window ID and returned as-is.
// Otherwise, it's treated as a window name and resolved by searching session.Windows.
//
// Parameters:
//   - session: The session containing windows to search
//   - identifier: Either a window ID (starts with "@") or window name (exact match)
//
// Returns:
//   - string: The resolved window ID
//   - error: ErrWindowNotFound if name doesn't match any window (includes available names)
//     ErrInvalidWindowID if identifier is empty
func resolveWindowIdentifier(session *store.Session, identifier string) (string, error) {
	// Validate identifier is non-empty
	if identifier == "" {
		return "", fmt.Errorf("%w: identifier cannot be empty", ErrInvalidWindowID)
	}

	// If identifier starts with "@", treat as window ID (backward compatible)
	if strings.HasPrefix(identifier, "@") {
		return identifier, nil
	}

	// Otherwise, treat as window name - search for exact case-sensitive match
	for i := range session.Windows {
		if session.Windows[i].Name == identifier {
			return session.Windows[i].TmuxWindowID, nil
		}
	}

	// Name not found - build helpful error message with available window names
	availableNames := make([]string, len(session.Windows))
	for i := range session.Windows {
		availableNames[i] = session.Windows[i].Name
	}
	return "", fmt.Errorf("%w: window name %q not found (available: %v)",
		ErrWindowNotFound, identifier, availableNames)
}

// WindowsSend sends a text command to a specific window for execution.
// Implements FR8: AI assistants can send text commands to specific windows
// Implements FR9: AI assistants can execute commands without manual user switching
// Implements FR10: AI assistants can send commands to multiple windows in sequence
//
// This method validates both windowIdentifier and command parameters, verifies the window
// exists in the session, then uses tmux.Executor.SendMessage() to send the command
// directly to the window (Decision 3: Direct internal calls). It returns a boolean
// success indicator (Decision 4: Go types directly).
//
// The command is sent exactly as provided - no interpretation or modification.
// The tmux.Executor automatically appends Enter to execute the command.
//
// Window Identifier Resolution:
// - If identifier starts with "@" (e.g., "@0", "@1"), it's treated as a window ID
// - Otherwise, it's treated as a window name and resolved via case-sensitive exact match
//
// Examples:
//
//	WindowsSend("@0", "echo test")        // Send to window ID @0
//	WindowsSend("supervisor", "echo test") // Send to window named "supervisor"
//
// Parameters:
//   - windowIdentifier: Either a window ID (starts with "@") or window name (must be non-empty)
//   - command: The command text to execute (must be non-empty)
//
// Returns:
//   - bool: true if command was sent successfully
//   - error: ErrInvalidWindowID if windowIdentifier is empty
//     ErrInvalidInput if command is empty
//     ErrSessionNotFound if session file cannot be loaded
//     ErrWindowNotFound if window ID or name doesn't exist in session
//     ErrTmuxCommandFailed if tmux send-keys command fails
//
// Performance: Expected to complete in <100ms, well under NFR-P1 requirement of 2s.
func (s *Server) WindowsSend(windowIdentifier, command string) (bool, error) {
	// Validate window identifier format (STRICT Rule 9)
	if windowIdentifier == "" {
		return false, fmt.Errorf("%w: windowIdentifier cannot be empty", ErrInvalidWindowID)
	}

	// Validate command is non-empty (STRICT Rule 9)
	if command == "" {
		return false, fmt.Errorf("%w: command cannot be empty", ErrInvalidInput)
	}

	// Load session from working directory using SessionStore (direct internal call per Decision 3)
	session, err := s.store.Load(s.workingDir)
	if err != nil {
		// Use categorized error with context (STRICT Rule 9)
		return false, fmt.Errorf("%w in directory %s: expected %s",
			ErrSessionNotFound, s.workingDir, s.sessionFile)
	}

	// Resolve window identifier (ID or name) to window ID
	windowID, err := resolveWindowIdentifier(session, windowIdentifier)
	if err != nil {
		// Error already includes proper categorization and context
		return false, err
	}

	// Verify window exists in session before attempting send
	var windowExists bool
	for i := range session.Windows {
		if session.Windows[i].TmuxWindowID == windowID {
			windowExists = true
			break
		}
	}

	if !windowExists {
		// Window not found - return categorized error with context (STRICT Rule 9)
		return false, fmt.Errorf("%w: windowID=%s session=%s",
			ErrWindowNotFound, windowID, session.SessionID)
	}

	// Send command using tmux.Executor (direct internal call per Decision 3)
	// Command is sent exactly as provided - no modification (per AC requirement)
	err = s.executor.SendMessage(session.SessionID, windowID, command)
	if err != nil {
		// Wrap tmux execution error with categorized error and context (STRICT Rule 9)
		return false, fmt.Errorf("%w: session=%s window=%s command=%q: %w",
			ErrTmuxCommandFailed, session.SessionID, windowID, command, err)
	}

	// Return bool success indicator directly - MCP SDK handles JSON serialization (Decision 4)
	return true, nil
}

// WindowsMessage sends a formatted message to another window with auto-detected sender.
// Implements inter-window AI agent communication with consistent message format.
//
// This method automatically detects the sender from session.SessionID and formats the message
// with clear sections: sender identification, message body, and reply instructions.
//
// Message Format:
//
//	New message from: {session.SessionID}
//
//	{message}
//
// Returns:
//   - success: true if message was delivered
//   - sender: auto-detected sender name (session.SessionID)
//   - error: categorized error with context if operation fails
func (s *Server) WindowsMessage(receiver, message string) (bool, string, error) {
	// Validate receiver is non-empty (STRICT Rule 9)
	if receiver == "" {
		return false, "", fmt.Errorf("%w: receiver cannot be empty", ErrInvalidInput)
	}

	// Validate message is non-empty (STRICT Rule 9)
	if message == "" {
		return false, "", fmt.Errorf("%w: message cannot be empty", ErrInvalidInput)
	}

	// Load session from working directory using SessionStore (direct internal call)
	session, err := s.store.Load(s.workingDir)
	if err != nil {
		// Use categorized error with context (STRICT Rule 9)
		return false, "", fmt.Errorf("%w in directory %s: expected %s",
			ErrSessionNotFound, s.workingDir, s.sessionFile)
	}

	// Detect sender window by UUID from environment variable
	// Each window exports TMUX_WINDOW_UUID - we use it to identify which window is sending
	senderUUID := os.Getenv("TMUX_WINDOW_UUID")
	sender := session.SessionID // Default fallback to session ID if UUID not found

	// Find window with matching UUID to get its name
	if senderUUID != "" {
		for i := range session.Windows {
			if session.Windows[i].UUID == senderUUID {
				sender = session.Windows[i].Name
				break
			}
		}
	}

	// Resolve receiver window identifier (ID or name) to window ID
	receiverWindowID, err := resolveWindowIdentifier(session, receiver)
	if err != nil {
		// Error already includes proper categorization and context (helpful error with available windows)
		return false, "", err
	}

	// Build formatted message with sender, message body, and reply instructions (Decision 2)
	formattedMessage := fmt.Sprintf("New message from: %s\n\n%s\n",
		sender, message)

	// Send message with 1-second delay to ensure complete delivery of formatted multi-line message
	// Using SendMessageWithDelay instead of SendMessage for windows-message MCP action
	err = s.executor.SendMessageWithDelay(session.SessionID, receiverWindowID, formattedMessage)
	if err != nil {
		// Wrap tmux execution error with categorized error and context (STRICT Rule 9)
		return false, "", fmt.Errorf("%w: session=%s window=%s: %w",
			ErrTmuxCommandFailed, session.SessionID, receiverWindowID, err)
	}

	// Return success and sender name (Decision 5)
	return true, sender, nil
}

// WindowsCreate creates a new window in the current tmux session for expanded workflows.
// Implements FR11: AI assistants can create new windows in the current session
// Implements FR13: AI assistants can manage window lifecycle for workflow cleanup and expansion
//
// This method replicates the exact CLI behavior from cmd/tmux-cli/session.go:573-670
// including UUID generation, environment export, postcommand execution, and session persistence.
// Uses direct internal calls (Decision 3) and returns Go types (Decision 4).
//
// Parameters:
//   - name: The name for the new window (must be non-empty)
//   - command: IGNORED - always uses "zsh" shell (CLI hardcodes this)
//
// Returns:
//   - *store.Window: Pointer to the created window with ID, name, UUID, and metadata
//   - error: ErrInvalidInput if name is empty
//     ErrSessionNotFound if session file cannot be loaded
//     ErrWindowCreateFailed if tmux window creation fails
//     ErrTmuxCommandFailed if UUID setup or export fails
//
// Performance: Expected to complete in <500ms (excluding Claude startup), well under NFR-P1 requirement of 2s.
func (s *Server) WindowsCreate(name, command string) (*store.Window, error) {
	// Step 1: Validate name is non-empty (STRICT Rule 9)
	if name == "" {
		return nil, fmt.Errorf("%w: name cannot be empty", ErrInvalidInput)
	}

	// Step 2: Load session from working directory using SessionStore (direct internal call per Decision 3)
	sess, err := s.store.Load(s.workingDir)
	if err != nil {
		// Use categorized error with context (STRICT Rule 9)
		return nil, fmt.Errorf("%w in directory %s: expected %s",
			ErrSessionNotFound, s.workingDir, s.sessionFile)
	}

	// Step 2.5: Validate window name uniqueness (case-insensitive)
	for _, w := range sess.Windows {
		if strings.EqualFold(w.Name, name) {
			return nil, fmt.Errorf("%w: window name %q already exists (case-insensitive match with %q)", ErrWindowCreateFailed, name, w.Name)
		}
	}

	// Step 3: Check for recovery and trigger if needed (matches CLI line 600-604)
	recoveryManager := recovery.NewSessionRecoveryManager(s.store, s.executor)
	err = s.maybeRecoverSession(sess, recoveryManager)
	if err != nil {
		return nil, fmt.Errorf("session recovery failed: %w", err)
	}

	// Step 4: Generate UUID before creating window (matches CLI line 607)
	windowUUID := session.GenerateUUID()

	// Step 5: Create window with "zsh" shell - IGNORING command parameter (matches CLI line 610)
	// CRITICAL: CLI hardcodes "zsh", MCP must match exactly
	windowID, err := s.executor.CreateWindow(sess.SessionID, name, "zsh")
	if err != nil {
		// Wrap tmux execution error with categorized error and context (STRICT Rule 9)
		return nil, fmt.Errorf("%w: session=%s name=%q: %w",
			ErrWindowCreateFailed, sess.SessionID, name, err)
	}

	// Step 6: Set window UUID immediately after creation (matches CLI line 616-621)
	err = s.executor.SetWindowOption(sess.SessionID, windowID, tmux.WindowUUIDOption, windowUUID)
	if err != nil {
		// Cleanup: kill the window since UUID setup failed (matches CLI line 619)
		_ = s.executor.KillWindow(sess.SessionID, windowID)
		return nil, fmt.Errorf("%w: set window UUID: %w", ErrTmuxCommandFailed, err)
	}

	// Step 7: Export TMUX_WINDOW_UUID in the running shell (matches CLI line 623-636)
	// Validate UUID before using in shell command (security - matches CLI line 626)
	if err := session.ValidateUUID(windowUUID); err != nil {
		// Cleanup: kill the window since UUID is invalid (matches CLI line 627)
		_ = s.executor.KillWindow(sess.SessionID, windowID)
		return nil, fmt.Errorf("%w: invalid window UUID: %w", ErrTmuxCommandFailed, err)
	}
	exportCmd := fmt.Sprintf("export TMUX_WINDOW_UUID=\"%s\"", windowUUID)
	err = s.executor.SendMessage(sess.SessionID, windowID, exportCmd)
	if err != nil {
		// Cleanup: kill the window since UUID export failed (matches CLI line 633-634)
		_ = s.executor.KillWindow(sess.SessionID, windowID)
		return nil, fmt.Errorf("%w: export TMUX_WINDOW_UUID in shell: %w", ErrTmuxCommandFailed, err)
	}

	// Step 8: Create Window struct with UUID (matches CLI line 639-644)
	newWindow := store.Window{
		TmuxWindowID: windowID,
		Name:         name,
		UUID:         windowUUID,
		// PostCommand not set - uses session-level config via GetEffectivePostCommand
	}

	// Step 9: Execute postcommand if configured - NON-FATAL (matches CLI line 647-654)
	postCmdConfig := sess.GetEffectivePostCommand(&newWindow)
	err = session.ExecutePostCommandWithFallback(s.executor, sess.SessionID, windowID, postCmdConfig)
	if err != nil {
		// Post-command failure is not fatal - window is created and UUID is exported
		// The user can manually run commands in the window
		// Matches CLI pattern (line 649-654)
		_ = err // Suppress unused variable warning
	}

	// Step 10: Append window to session (matches CLI line 657)
	sess.Windows = append(sess.Windows, newWindow)

	// Step 11: Save updated session with atomic write (matches CLI line 660-665)
	err = s.store.Save(sess)
	if err != nil {
		// Window was created in tmux but persistence failed
		// This is a problem - window won't survive recovery
		// Don't cleanup window - it exists in tmux, just warn via error
		return &newWindow, fmt.Errorf("save session: %w (window created in tmux but not persisted!)", err)
	}

	// Return *Window type with UUID populated - MCP SDK handles JSON serialization (Decision 4)
	return &newWindow, nil
}

// maybeRecoverSession checks if session needs recovery and performs it transparently
// This is a direct port of cmd/tmux-cli/recovery_helper.go:MaybeRecoverSession
// to avoid importing cmd package into internal/mcp
func (s *Server) maybeRecoverSession(
	session *store.Session,
	recoveryManager recovery.RecoveryManager,
) error {
	if session == nil {
		return fmt.Errorf("session is required")
	}

	// 1. Check if recovery is needed
	recoveryNeeded, err := recoveryManager.IsRecoveryNeeded(session)
	if err != nil {
		return fmt.Errorf("check recovery needed: %w", err)
	}

	// 2. If no recovery needed, return immediately
	if !recoveryNeeded {
		return nil
	}

	// 3. Perform recovery (recreate session + windows)
	err = recoveryManager.RecoverSession(session)
	if err != nil {
		return fmt.Errorf("recovery failed: %w", err)
	}

	// 4. Verify recovery succeeded
	err = recoveryManager.VerifyRecovery(session)
	if err != nil {
		return fmt.Errorf("recovery verification failed: %w", err)
	}

	return nil
}

// WindowsKill terminates a specific window in the current tmux session by name.
// Implements FR12: AI assistants can terminate (kill) specific windows
// Implements FR13: AI assistants can manage window lifecycle for workflow cleanup and expansion
//
// IMPORTANT: This method ONLY accepts window names (no "@" prefix IDs).
// Using window IDs (e.g., "@0", "@1") will return an error.
//
// This method validates the window name, resolves it to a window ID, loads the session,
// prevents killing the last window (would terminate session), then uses tmux.Executor.KillWindow()
// to terminate the window directly (Decision 3: Direct internal calls). Returns a boolean
// success indicator (Decision 4: Go types directly).
//
// The method is STRICT - it returns an error if the window doesn't exist or kill fails.
//
// Examples:
//
//	WindowsKill("supervisor") // Kill window named "supervisor" ✓
//	WindowsKill("@0")         // ERROR: Window IDs not allowed ✗
//
// Parameters:
//   - windowIdentifier: Window name (must be non-empty, cannot start with "@")
//
// Returns:
//   - bool: true if window was killed successfully
//   - error: ErrInvalidWindowID if windowIdentifier is empty or starts with "@"
//     ErrSessionNotFound if session file cannot be loaded
//     ErrWindowNotFound if window name doesn't exist in the session
//     ErrWindowKillFailed if attempting to kill last window in session
//     ErrTmuxCommandFailed if tmux kill-window command fails
//
// Performance: Expected to complete in <100ms, well under NFR-P1 requirement of 2s.
func (s *Server) WindowsKill(windowIdentifier string) (bool, error) {
	// Validate window identifier is non-empty (STRICT Rule 9)
	if windowIdentifier == "" {
		return false, fmt.Errorf("%w: windowIdentifier cannot be empty", ErrInvalidWindowID)
	}

	// STRICT: Reject window IDs (@ prefix) - names only
	if strings.HasPrefix(windowIdentifier, "@") {
		return false, fmt.Errorf("%w: window IDs not allowed (use window name instead of %q)",
			ErrInvalidWindowID, windowIdentifier)
	}

	// Load session from working directory using SessionStore (direct internal call per Decision 3)
	session, err := s.store.Load(s.workingDir)
	if err != nil {
		// Use categorized error with context (STRICT Rule 9)
		return false, fmt.Errorf("%w in directory %s: expected %s",
			ErrSessionNotFound, s.workingDir, s.sessionFile)
	}

	// Check for recovery and trigger if needed (consistent with other MCP operations)
	recoveryManager := recovery.NewSessionRecoveryManager(s.store, s.executor)
	err = s.maybeRecoverSession(session, recoveryManager)
	if err != nil {
		return false, err
	}

	// Resolve window name to window ID (no @ prefix allowed anymore)
	windowID, err := resolveWindowIdentifier(session, windowIdentifier)
	if err != nil {
		// Error already includes proper categorization and context
		return false, err
	}

	// Try to query tmux to get current list of windows
	// This ensures we work with the actual tmux state when available
	actualWindows, tmuxErr := s.executor.ListWindows(session.SessionID)

	// STRICT: If tmux is not running, return error (cannot kill non-existent window)
	if tmuxErr != nil {
		return false, fmt.Errorf("%w: tmux session not running: %w",
			ErrTmuxCommandFailed, tmuxErr)
	}

	// Determine window count for last-window check using actual tmux state
	windowCount := len(actualWindows)
	var windowExistsInTmux bool
	for i := range actualWindows {
		if actualWindows[i].TmuxWindowID == windowID {
			windowExistsInTmux = true
			break
		}
	}

	// STRICT: Window doesn't exist - return error (not idempotent anymore)
	if !windowExistsInTmux {
		return false, fmt.Errorf("%w: window %q not found in tmux session",
			ErrWindowNotFound, windowIdentifier)
	}

	// CRITICAL: Check if this is the last window AND we're trying to kill it
	// Killing the last window kills the session in some tmux versions
	if windowCount <= 1 {
		return false, fmt.Errorf("%w: cannot kill last window in session (would terminate session)",
			ErrWindowKillFailed)
	}

	// Kill window using tmux.Executor
	err = s.executor.KillWindow(session.SessionID, windowID)
	if err != nil {
		// Wrap tmux execution error with categorized error and context (STRICT Rule 9)
		return false, fmt.Errorf("%w: session=%s window=%q (ID=%s): %w",
			ErrTmuxCommandFailed, session.SessionID, windowIdentifier, windowID, err)
	}

	// Remove killed window from session file
	updatedWindows := make([]store.Window, 0, len(session.Windows))
	for i := range session.Windows {
		if session.Windows[i].TmuxWindowID != windowID {
			updatedWindows = append(updatedWindows, session.Windows[i])
		}
	}
	session.Windows = updatedWindows

	// Save updated session file
	err = s.store.Save(session)
	if err != nil {
		// Window was killed but session file update failed - return error (strict mode)
		return false, fmt.Errorf("window killed but session file save failed: %w", err)
	}

	// Return bool success indicator directly - MCP SDK handles JSON serialization (Decision 4)
	return true, nil
}
