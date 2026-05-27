package mcp

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"path/filepath"

	"github.com/console/tmux-cli/internal/session"
	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/tasks"
	"github.com/console/tmux-cli/internal/tmux"
)

// WindowListItem represents a simplified window entry with only the name.
type WindowListItem struct {
	Name string `json:"name"`
}

// WindowsList returns all windows in the current tmux session.
func (s *Server) WindowsList() ([]WindowListItem, error) {
	sessionID, err := s.discoverSession()
	if err != nil {
		return nil, err
	}

	windows, err := s.executor.ListWindows(sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTmuxCommandFailed, err)
	}

	result := make([]WindowListItem, len(windows))
	for i, w := range windows {
		result[i] = WindowListItem{Name: w.Name}
	}

	return result, nil
}

// resolveWindowIdentifier resolves a window identifier (ID or name) to a window ID.
// If the identifier starts with "@", it's treated as a window ID and returned as-is.
// Otherwise, it's treated as a window name and resolved by searching the window list.
func resolveWindowIdentifier(windows []tmux.WindowInfo, identifier string) (string, error) {
	if identifier == "" {
		return "", fmt.Errorf("%w: identifier cannot be empty", ErrInvalidWindowID)
	}

	// If identifier starts with "@", treat as window ID
	if strings.HasPrefix(identifier, "@") {
		return identifier, nil
	}

	// Otherwise, treat as window name - search for exact case-sensitive match
	for i := range windows {
		if windows[i].Name == identifier {
			return windows[i].TmuxWindowID, nil
		}
	}

	// Name not found - build helpful error message
	availableNames := make([]string, len(windows))
	for i := range windows {
		availableNames[i] = windows[i].Name
	}
	return "", fmt.Errorf("%w: window name %q not found (available: %v)",
		ErrWindowNotFound, identifier, availableNames)
}

// WindowsSend sends a text command to a specific window for execution.
func (s *Server) WindowsSend(windowIdentifier, command string) (bool, error) {
	if windowIdentifier == "" {
		return false, fmt.Errorf("%w: windowIdentifier cannot be empty", ErrInvalidWindowID)
	}
	if command == "" {
		return false, fmt.Errorf("%w: command cannot be empty", ErrInvalidInput)
	}

	sessionID, err := s.discoverSession()
	if err != nil {
		return false, err
	}

	windows, err := s.executor.ListWindows(sessionID)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrTmuxCommandFailed, err)
	}

	windowID, err := resolveWindowIdentifier(windows, windowIdentifier)
	if err != nil {
		return false, err
	}

	// Verify window exists
	var windowExists bool
	for i := range windows {
		if windows[i].TmuxWindowID == windowID {
			windowExists = true
			break
		}
	}
	if !windowExists {
		return false, fmt.Errorf("%w: windowID=%s session=%s",
			ErrWindowNotFound, windowID, sessionID)
	}

	err = s.executor.SendMessageWithDelay(sessionID, windowID, command)
	if err != nil {
		return false, fmt.Errorf("%w: session=%s window=%s command=%q: %w",
			ErrTmuxCommandFailed, sessionID, windowID, command, err)
	}

	return true, nil
}

// WindowsMessage sends a formatted message to another window with auto-detected sender.
func (s *Server) WindowsMessage(receiver, message string) (bool, string, error) {
	if receiver == "" {
		return false, "", fmt.Errorf("%w: receiver cannot be empty", ErrInvalidInput)
	}
	if message == "" {
		return false, "", fmt.Errorf("%w: message cannot be empty", ErrInvalidInput)
	}

	sessionID, err := s.discoverSession()
	if err != nil {
		return false, "", err
	}

	windows, err := s.executor.ListWindows(sessionID)
	if err != nil {
		return false, "", fmt.Errorf("%w: %w", ErrTmuxCommandFailed, err)
	}

	// Detect sender window by UUID from environment variable
	senderUUID := os.Getenv("TMUX_WINDOW_UUID")
	sender := sessionID // Default to session ID

	if senderUUID != "" {
		for i := range windows {
			uuid, err := s.executor.GetWindowOption(sessionID, windows[i].TmuxWindowID, tmux.WindowUUIDOption)
			if err == nil && uuid == senderUUID {
				sender = windows[i].Name
				break
			}
		}
	}

	// Resolve receiver window
	receiverWindowID, err := resolveWindowIdentifier(windows, receiver)
	if err != nil {
		return false, "", err
	}

	// Build formatted message
	formattedMessage := fmt.Sprintf("New message from: %s\n\n%s\n",
		sender, message)

	err = s.executor.SendMessageWithDelay(sessionID, receiverWindowID, formattedMessage)
	if err != nil {
		return false, "", fmt.Errorf("%w: session=%s window=%s: %w",
			ErrTmuxCommandFailed, sessionID, receiverWindowID, err)
	}

	return true, sender, nil
}

// WindowsCreate creates a new window in the current tmux session.
func (s *Server) WindowsCreate(name, command string) (*WindowInfo, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: name cannot be empty", ErrInvalidInput)
	}

	sessionID, err := s.discoverSession()
	if err != nil {
		return nil, err
	}

	windows, err := s.executor.ListWindows(sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTmuxCommandFailed, err)
	}

	// Validate window name uniqueness (case-insensitive)
	for _, w := range windows {
		if strings.EqualFold(w.Name, name) {
			return nil, fmt.Errorf("%w: window name %q already exists (case-insensitive match with %q)",
				ErrWindowCreateFailed, name, w.Name)
		}
	}

	// Generate UUID
	windowUUID := session.GenerateUUID()

	windowID, err := s.executor.CreateWindow(sessionID, name, "")
	if err != nil {
		return nil, fmt.Errorf("%w: session=%s name=%q: %w",
			ErrWindowCreateFailed, sessionID, name, err)
	}

	// Set window UUID
	err = s.executor.SetWindowOption(sessionID, windowID, tmux.WindowUUIDOption, windowUUID)
	if err != nil {
		_ = s.executor.KillWindow(sessionID, windowID)
		return nil, fmt.Errorf("%w: set window UUID: %w", ErrTmuxCommandFailed, err)
	}

	// Export TMUX_WINDOW_UUID in the running shell
	if err := session.ValidateUUID(windowUUID); err != nil {
		_ = s.executor.KillWindow(sessionID, windowID)
		return nil, fmt.Errorf("%w: invalid window UUID: %w", ErrTmuxCommandFailed, err)
	}
	exportCmd := fmt.Sprintf("export TMUX_WINDOW_UUID=\"%s\"", windowUUID)
	err = s.executor.SendMessage(sessionID, windowID, exportCmd)
	if err != nil {
		_ = s.executor.KillWindow(sessionID, windowID)
		return nil, fmt.Errorf("%w: export TMUX_WINDOW_UUID in shell: %w", ErrTmuxCommandFailed, err)
	}

	// Execute postcommand if configured - NON-FATAL
	postCmdConfig := session.DefaultPostCommandConfig()
	err = session.ExecutePostCommandWithFallback(s.executor, sessionID, windowID, postCmdConfig)
	if err != nil {
		_ = err // Post-command failure is not fatal
	}

	return &WindowInfo{
		TmuxWindowID: windowID,
		Name:         name,
		UUID:         windowUUID,
	}, nil
}

// WindowsKill terminates a specific window in the current tmux session by name.
func (s *Server) WindowsKill(windowIdentifier string) (bool, error) {
	if windowIdentifier == "" {
		return false, fmt.Errorf("%w: windowIdentifier cannot be empty", ErrInvalidWindowID)
	}

	// STRICT: Reject window IDs (@ prefix) - names only
	if strings.HasPrefix(windowIdentifier, "@") {
		return false, fmt.Errorf("%w: window IDs not allowed (use window name instead of %q)",
			ErrInvalidWindowID, windowIdentifier)
	}

	sessionID, err := s.discoverSession()
	if err != nil {
		return false, err
	}

	windows, err := s.executor.ListWindows(sessionID)
	if err != nil {
		return false, fmt.Errorf("%w: tmux session not running: %w",
			ErrTmuxCommandFailed, err)
	}

	// Resolve window name to ID
	windowID, err := resolveWindowIdentifier(windows, windowIdentifier)
	if err != nil {
		return false, err
	}

	// Verify window exists in tmux
	var windowExistsInTmux bool
	for i := range windows {
		if windows[i].TmuxWindowID == windowID {
			windowExistsInTmux = true
			break
		}
	}
	if !windowExistsInTmux {
		return false, fmt.Errorf("%w: window %q not found in tmux session",
			ErrWindowNotFound, windowIdentifier)
	}

	// Prevent killing kill-protected windows
	if windowIdentifier == "taskvisor" {
		return false, fmt.Errorf("%w: window %q is kill-protected",
			ErrWindowKillFailed, windowIdentifier)
	}

	// Prevent killing last window
	if len(windows) <= 1 {
		return false, fmt.Errorf("%w: cannot kill last window in session (would terminate session)",
			ErrWindowKillFailed)
	}

	err = s.executor.KillWindow(sessionID, windowID)
	if err != nil {
		return false, fmt.Errorf("%w: session=%s window=%q (ID=%s): %w",
			ErrTmuxCommandFailed, sessionID, windowIdentifier, windowID, err)
	}

	return true, nil
}

// HooksConfig views or toggles hook configuration in setting.yaml.
func (s *Server) HooksConfig(action, hook string) (*HooksConfigOutput, error) {
	switch action {
	case "list":
		settings, err := setup.LoadSettings(s.workingDir)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to load settings: %w", ErrInvalidInput, err)
		}
		return &HooksConfigOutput{Hooks: &settings.Hooks, Changed: false}, nil

	case "enable", "disable":
		if hook != "session_notify" && hook != "block_interactive" {
			return nil, fmt.Errorf("%w: invalid hook name %q (valid: session_notify, block_interactive)", ErrInvalidInput, hook)
		}
		settings, err := setup.LoadSettings(s.workingDir)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to load settings: %w", ErrInvalidInput, err)
		}
		value := action == "enable"
		switch hook {
		case "session_notify":
			settings.Hooks.SessionNotify = value
		case "block_interactive":
			settings.Hooks.BlockInteractive = value
		}
		if err := setup.SaveSettings(s.workingDir, settings); err != nil {
			return nil, fmt.Errorf("%w: failed to save settings: %w", ErrInvalidInput, err)
		}
		return &HooksConfigOutput{Hooks: &settings.Hooks, Changed: true}, nil

	default:
		return nil, fmt.Errorf("%w: invalid action %q (valid: list, enable, disable)", ErrInvalidInput, action)
	}
}

// WindowsRecoverWorkers batch-recovers stuck execute-N worker windows.
func (s *Server) WindowsRecoverWorkers(message string) (*WindowsRecoverWorkersOutput, error) {
	if message == "" {
		message = "continue"
	}

	sessionID, err := s.discoverSession()
	if err != nil {
		return nil, err
	}

	windows, err := s.executor.ListWindows(sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTmuxCommandFailed, err)
	}

	// Filter execute-* workers
	type workerInfo struct {
		name     string
		windowID string
	}
	var workers []workerInfo
	for _, w := range windows {
		if strings.HasPrefix(w.Name, "execute-") {
			workers = append(workers, workerInfo{name: w.Name, windowID: w.TmuxWindowID})
		}
	}

	if len(workers) == 0 {
		return &WindowsRecoverWorkersOutput{
			Recovered: []string{},
			Message:   message,
			Count:     0,
		}, nil
	}

	// Phase 1: Send Enter to all workers (dismiss dialogs)
	enterOK := make(map[string]bool)
	for _, w := range workers {
		if err := s.executor.SendEnter(sessionID, w.windowID); err == nil {
			enterOK[w.name] = true
		}
	}

	// Wait for dialogs to dismiss
	time.Sleep(1 * time.Second)

	// Phase 2: Send message to workers where Enter succeeded
	var recovered []string
	for _, w := range workers {
		if !enterOK[w.name] {
			continue
		}
		if err := s.executor.SendMessage(sessionID, w.windowID, message); err == nil {
			recovered = append(recovered, w.name)
		}
	}

	if recovered == nil {
		recovered = []string{}
	}

	return &WindowsRecoverWorkersOutput{
		Recovered: recovered,
		Message:   message,
		Count:     len(recovered),
	}, nil
}

func nextExecuteN(windows []WindowListItem) string {
	maxN := 0
	for _, w := range windows {
		if strings.HasPrefix(w.Name, "execute-") {
			suffix := w.Name[len("execute-"):]
			if n, err := strconv.Atoi(suffix); err == nil && n > maxN {
				maxN = n
			}
		}
	}
	return fmt.Sprintf("execute-%d", maxN+1)
}

func buildTaskMessage(supervisorWid, workerName, subtask, contextFile, scope, context, researchDir, deliverable string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "SUPERVISOR_WID=%s\n", supervisorWid)
	fmt.Fprintf(&b, "SELF_WID=%s\n", workerName)
	fmt.Fprintf(&b, "SUBTASK: %s\n", subtask)
	b.WriteString("\nSCOPE:\n")
	fmt.Fprintf(&b, "Full spec is in %s — read it and follow it.\n\n", contextFile)
	b.WriteString(scope)
	b.WriteString("\n")
	b.WriteString("\nCONTEXT:\n")
	if context != "" {
		b.WriteString(context)
	} else {
		b.WriteString("(none)")
	}
	b.WriteString("\n")
	b.WriteString("\nDELIVERABLE (write these sections INSIDE the report .md; do NOT paste them into tmux):\n")
	if deliverable != "" {
		b.WriteString(deliverable)
		b.WriteString("\n")
	} else {
		b.WriteString("- FINDINGS: >=3 bullets, each with file path or symbol reference\n")
		b.WriteString("- RISKS: bullets OR the literal word \"none\"\n")
		b.WriteString("- RECOMMENDATION: 1-3 sentences, must contain a verb of decision (use|avoid|rewrite|keep|split|merge)\n")
		b.WriteString("- FILES: absolute paths touched or cited, one per line\n")
	}
	b.WriteString("\nRESPONSE PROTOCOL (MANDATORY):\n")
	fmt.Fprintf(&b, "1. Save the full report to .tmux-cli/research/%s/%s-<slug>.md\n", researchDir, workerName)
	if deliverable != "" {
		b.WriteString("   using the sections specified in DELIVERABLE above.\n")
	} else {
		b.WriteString("   with headings ## FINDINGS / ## RISKS / ## RECOMMENDATION / ## FILES plus any supporting detail.\n")
	}
	b.WriteString("   Pick a descriptive <slug> (kebab-case, <=40 chars).\n")
	fmt.Fprintf(&b, "2. Reply via windows-message to %s with ONLY:\n", supervisorWid)
	fmt.Fprintf(&b, "   [EXECUTE:DONE wid=%s sup=%s file=<abs-path-to-your-md>]\n", workerName, supervisorWid)
	b.WriteString("   Do NOT inline report content in the tmux message. The file IS the report.\n")
	fmt.Fprintf(&b, "3. [EXECUTE:NEED_INPUT wid=%s sup=%s ...] and [EXECUTE:FAILED wid=%s sup=%s reason=...] may carry a short (<200 char) inline reason.\n", workerName, supervisorWid, workerName, supervisorWid)
	b.WriteString("4. If the supervisor sends [EXECUTE:PUSHBACK n=<N> gap=<SX>]:\n")
	b.WriteString("   a. Before amending: identify what currently works well in the cited section and adjacent sections (KEEP list).\n")
	b.WriteString("   b. Fix the cited gap.\n")
	b.WriteString("   c. Verify KEEP items survived the fix.\n")
	b.WriteString("   d. Append a Spec Change Log entry: gap cited, what changed, what was preserved, what bad state was avoided.\n")
	fmt.Fprintf(&b, "   e. Re-tag [EXECUTE:DONE wid=%s sup=%s file=<same-path>].\n", workerName, supervisorWid)

	return b.String()
}

// WindowsSpawnWorker atomically spawns a worker: creates window, sends /tmux:execute,
// and sends the structured task message.
func (s *Server) WindowsSpawnWorker(supervisorWid, subtask, contextFile, scope, context, deliverable string) (*WindowInfo, string, string, error) {
	if supervisorWid == "" {
		return nil, "", "", fmt.Errorf("%w: supervisorWid cannot be empty", ErrInvalidInput)
	}
	if subtask == "" {
		return nil, "", "", fmt.Errorf("%w: subtask cannot be empty", ErrInvalidInput)
	}
	if contextFile == "" {
		return nil, "", "", fmt.Errorf("%w: contextFile cannot be empty", ErrInvalidInput)
	}
	if scope == "" {
		return nil, "", "", fmt.Errorf("%w: scope cannot be empty", ErrInvalidInput)
	}

	windows, err := s.WindowsList()
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to list windows: %w", err)
	}

	settings, _ := setup.LoadSettings(s.workingDir)
	if settings != nil && settings.Supervisor.MaxWorkers > 0 {
		var count int
		for _, w := range windows {
			if strings.HasPrefix(w.Name, "execute-") {
				count++
			}
		}
		if count >= settings.Supervisor.MaxWorkers {
			return nil, "", "", fmt.Errorf("%w: %d execute workers already running (limit: %d) — wait for a worker to finish or increase supervisor.max_workers in setting.yaml",
				ErrMaxWorkersExceeded, count, settings.Supervisor.MaxWorkers)
		}
	}

	workerName := nextExecuteN(windows)

	window, err := s.WindowsCreate(workerName, "")
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to create worker window %s: %w", workerName, err)
	}

	sessionID, err := s.discoverSession()
	if err != nil {
		return nil, "", "", err
	}

	err = s.executor.SendMessage(sessionID, window.TmuxWindowID, "/tmux:execute")
	if err != nil {
		_ = s.executor.KillWindow(sessionID, window.TmuxWindowID)
		return nil, "", "", fmt.Errorf("%w: failed to send /tmux:execute to %s: %w",
			ErrTmuxCommandFailed, workerName, err)
	}

	time.Sleep(2 * time.Second)

	researchDir := time.Now().Format("2006-01-02-15")
	taskMessage := buildTaskMessage(supervisorWid, workerName, subtask, contextFile, scope, context, researchDir, deliverable)

	err = s.executor.SendMessageWithDelay(sessionID, window.TmuxWindowID, taskMessage)
	if err != nil {
		_ = s.executor.KillWindow(sessionID, window.TmuxWindowID)
		return nil, "", "", fmt.Errorf("%w: failed to send task message to %s: %w",
			ErrTmuxCommandFailed, workerName, err)
	}

	return window, workerName, taskMessage, nil
}

func (s *Server) SpecValidate(file string) (*SpecValidateOutput, error) {
	result, err := tasks.ValidateSpecFile(file)
	if err != nil {
		return nil, err
	}
	gaps := make([]SpecValidateGap, len(result.Gaps))
	for i, g := range result.Gaps {
		gaps[i] = SpecValidateGap{ID: g.ID, Message: g.Message}
	}
	return &SpecValidateOutput{
		Valid: result.Valid,
		Gaps:  gaps,
		Stats: SpecValidateStats{
			TestCases:          result.Stats.TestCases,
			AcceptanceCriteria: result.Stats.AcceptanceCriteria,
			CodeMapEntries:     result.Stats.CodeMapEntries,
		},
	}, nil
}

func (s *Server) TasksValidate() (*TasksValidateOutput, error) {
	path := filepath.Join(s.workingDir, ".tmux-cli", "tasks.yaml")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no tasks.yaml found at %s", path)
	}
	errs := tasks.ValidateTasksFile(path)
	return &TasksValidateOutput{
		Valid:  len(errs) == 0,
		Errors: errs,
	}, nil
}
