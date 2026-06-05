package mcp

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"path/filepath"

	"github.com/console/tmux-cli/internal/session"
	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/tasks"
	"github.com/console/tmux-cli/internal/taskvisor"
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

// resolveSelfWindowName returns the NAME of the tmux window this MCP server
// process is running in, resolved from the TMUX_WINDOW_UUID env var that the
// per-window launcher exports (see SessionManager / WindowsCreate). It matches
// that UUID against the per-window "window-uuid" option across the supplied
// window list. Returns "" when the env var is unset or no window matches — in
// those contexts (MaxGoals=1, non-tmux, unit tests) callers keep their prior
// behavior. This is the single source of truth for "which window am I?" so the
// LLM never has to guess it (which it gets wrong at MaxGoals>1, where multiple
// supervisor-NNN windows plus a bare "supervisor" coexist).
func (s *Server) resolveSelfWindowName(sessionID string, windows []tmux.WindowInfo) string {
	selfUUID := os.Getenv("TMUX_WINDOW_UUID")
	if selfUUID == "" {
		return ""
	}
	for i := range windows {
		uuid, err := s.executor.GetWindowOption(sessionID, windows[i].TmuxWindowID, tmux.WindowUUIDOption)
		if err == nil && uuid == selfUUID {
			return windows[i].Name
		}
	}
	return ""
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

	// Detect sender window by UUID from environment variable.
	sender := sessionID // Default to session ID
	if self := s.resolveSelfWindowName(sessionID, windows); self != "" {
		sender = self
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
// windowCreatorInDir is the optional cwd-aware extension implemented by the
// production executor (RealTmuxExecutor.CreateWindowInDir). It is deliberately NOT
// part of the TmuxExecutor interface (see internal/tmux/real_executor.go) so that
// threading a working directory stays additive — WindowsCreate type-asserts to it
// only when a non-empty cwd is requested and otherwise leaves every existing path
// (and its mocks) byte-identical.
type windowCreatorInDir interface {
	CreateWindowInDir(sessionID, name, command, cwd string) (string, error)
}

func (s *Server) WindowsCreate(name, command, cwd string) (*WindowInfo, error) {
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

	// A non-empty cwd (E1-1c per-goal worktree isolation) starts the window's shell
	// there via the executor's cwd-aware factory. cwd=="" keeps the session default,
	// routing through the original CreateWindow path so all existing callers are
	// unchanged. If the executor lacks CreateWindowInDir, degrade to the default cwd.
	var windowID string
	if cwd != "" {
		if dirExec, ok := s.executor.(windowCreatorInDir); ok {
			windowID, err = dirExec.CreateWindowInDir(sessionID, name, "", cwd)
		} else {
			log.Printf("warning: executor does not support working directory; creating %q at session default", name)
			windowID, err = s.executor.CreateWindow(sessionID, name, "")
		}
	} else {
		windowID, err = s.executor.CreateWindow(sessionID, name, "")
	}
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

// WindowsRecoverWorkers batch-recovers stuck execute-N worker windows. When
// callerWid is goal-namespaced (parseGoalBinding ok — e.g. "supervisor-020" or
// an explicit goal-<N> token), recovery is restricted to that goal's
// execute-<ns>-* worker pool, so at MaxGoals>1 a supervisor recovering ITS
// stuck workers never injects spurious messages into other goals' healthy
// workers mid-task. A bare ("supervisor") or absent callerWid keeps the global
// "execute-" prefix — byte-identical back-compat, and the tool remains usable
// as a manual catch-all recovery.
func (s *Server) WindowsRecoverWorkers(message, callerWid string) (*WindowsRecoverWorkersOutput, error) {
	if message == "" {
		message = "continue"
	}

	prefix := "execute-"
	if id, _, ok := parseGoalBinding(callerWid); ok {
		prefix = taskvisor.ExecutePrefixForGoal(id)
	}

	sessionID, err := s.discoverSession()
	if err != nil {
		return nil, err
	}

	windows, err := s.executor.ListWindows(sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTmuxCommandFailed, err)
	}

	// Filter the in-scope execute workers
	type workerInfo struct {
		name     string
		windowID string
	}
	var workers []workerInfo
	for _, w := range windows {
		if strings.HasPrefix(w.Name, prefix) {
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

func nextExecuteN(windows []WindowListItem, prefix string) string {
	maxN := 0
	for _, w := range windows {
		if strings.HasPrefix(w.Name, prefix) {
			suffix := w.Name[len(prefix):]
			if n, err := strconv.Atoi(suffix); err == nil && n > maxN {
				maxN = n
			}
		}
	}
	return fmt.Sprintf("%s%d", prefix, maxN+1)
}

// goalBindingRe matches the first goal-<N> token anywhere in a caller window name
// (e.g. the "goal-020" in "sup-goal-020-c3"). Compiled once at package scope.
var goalBindingRe = regexp.MustCompile(`goal-\d+`)

// goalCycleSuffixRe matches a trailing -c<N> cycle suffix on a caller window name
// (e.g. the "-c3" in "sup-goal-020-c3"). Compiled once at package scope.
var goalCycleSuffixRe = regexp.MustCompile(`-c(\d+)$`)

// namespacedSupRe / namespacedWorkerRe recover the goal namespace from the
// per-goal window names the taskvisor daemon actually emits at MaxGoals>1
// (internal/taskvisor/window_names.go): supervisor-<ns> / validator-<ns> and the
// execute-<ns>-<n> / inv-<ns>-<n> worker pools, where <ns> is the goal id with its
// "goal-" prefix stripped (e.g. goal-020 -> "020"). This reconciles the two halves
// of execute-31: window_names.go strips the "goal-" prefix to form <ns>, but this
// parser was written for an explicit "goal-<N>" token, so a worker spawned under a
// real namespaced supervisor window (e.g. "supervisor-020") would otherwise miss
// the goal binding and fall back to the SHARED global marker — routing two
// concurrent goals' workers to the same research root. The MaxGoals<=1 bare names
// ("supervisor"/"validator" carry no -<ns>; "execute-<n>"/"inv-<n>" carry a single
// numeric segment) are deliberately NOT matched here, so they still take the
// marker fallback (byte-identical single-goal routing).
var namespacedSupRe = regexp.MustCompile(`^(?:supervisor|validator)-(\d+)$`)
var namespacedWorkerRe = regexp.MustCompile(`^(?:execute|inv)-(\d+)-\d+`)

// parseGoalBinding derives a worker→goal binding from a caller window name. It is a
// pure function: no filesystem access, never panics. Resolution order: (1) an
// explicit goal-<N> token anywhere in the name (e.g. "sup-goal-020-c3"); (2) the
// daemon's namespaced window forms (supervisor-<ns> / execute-<ns>-<n>), from which
// the goal id is reconstructed as "goal-<ns>". A trailing -c<N> suffix with N>=1
// becomes cycle (otherwise cycle=0, the no-cycle research path; the namespaced
// forms carry no cycle suffix, so concurrent goals route to distinct goal-scoped
// roots). ok is true iff a goal id was resolved.
func parseGoalBinding(windowName string) (goalID string, cycle int, ok bool) {
	goalID = goalBindingRe.FindString(windowName)
	if goalID == "" {
		var ns string
		if m := namespacedSupRe.FindStringSubmatch(windowName); m != nil {
			ns = m[1]
		} else if m := namespacedWorkerRe.FindStringSubmatch(windowName); m != nil {
			ns = m[1]
		}
		if ns == "" {
			return "", 0, false
		}
		goalID = "goal-" + ns
	}
	if m := goalCycleSuffixRe.FindStringSubmatch(windowName); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 {
			cycle = n
		}
	}
	return goalID, cycle, true
}

// resolveResearchRoot returns the project-relative research-root path for worker
// reports, derived from the caller's window name first and process-global markers only
// as a fallback. Precedence:
//  1. If callerWid is goal-namespaced (parseGoalBinding ok), the binding is derived
//     from it — parallel-safe, never the global cycle marker: an explicit cycle>=1
//     suffix yields .tmux-cli/goals/<GOAL_ID>/research/cycle-<N>; otherwise the
//     PER-GOAL marker .tmux-cli/goals/<GOAL_ID>/current-cycle (readPerGoalCycleMarker)
//     supplies the cycle layer when valid, else .tmux-cli/goals/<GOAL_ID>/research.
//  2. Otherwise (today's plain "supervisor" caller, MaxGoals=1) it falls back to the
//     legacy global markers: a non-empty .tmux-cli/taskvisor-current-goal yields the
//     goal-scoped path, further namespaced per-cycle when a valid
//     .tmux-cli/taskvisor-current-cycle marker exists. This keeps MaxGoals=1 output
//     paths byte-identical to the prior marker-based resolution.
//  3. With no goal token and no marker, it is the standalone timestamped dir:
//     .tmux-cli/research/<YYYY-MM-DD-HH>.
//
// This mirrors supervisor.xml / plan.xml step 0b.
func (s *Server) resolveResearchRoot(callerWid string) string {
	if id, cyc, ok := parseGoalBinding(callerWid); ok {
		if cyc >= 1 {
			return filepath.Join(".tmux-cli", "goals", id, "research", fmt.Sprintf("cycle-%d", cyc))
		}
		if n, ok := s.readPerGoalCycleMarker(id); ok {
			return filepath.Join(".tmux-cli", "goals", id, "research", fmt.Sprintf("cycle-%d", n))
		}
		return filepath.Join(".tmux-cli", "goals", id, "research")
	}
	goalPath := filepath.Join(s.workingDir, ".tmux-cli", "taskvisor-current-goal")
	if data, err := os.ReadFile(goalPath); err == nil {
		if goalID := strings.TrimSpace(string(data)); goalID != "" {
			if n, ok := s.readCycleMarker(); ok {
				return filepath.Join(".tmux-cli", "goals", goalID, "research", fmt.Sprintf("cycle-%d", n))
			}
			return filepath.Join(".tmux-cli", "goals", goalID, "research")
		}
	}
	return filepath.Join(".tmux-cli", "research", time.Now().Format("2006-01-02-15"))
}

// readCycleMarker reads the .tmux-cli/taskvisor-current-cycle marker written by the
// daemon (a bare integer string) and returns (N, true) only when it trims to a
// positive integer (>=1). Absent, empty, or unparseable markers yield (0, false) so
// callers fall back to the no-cycle path. It never errors or panics.
func (s *Server) readCycleMarker() (int, bool) {
	cyclePath := filepath.Join(s.workingDir, ".tmux-cli", "taskvisor-current-cycle")
	data, err := os.ReadFile(cyclePath)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// readPerGoalCycleMarker reads the PER-GOAL .tmux-cli/goals/<goalID>/current-cycle
// marker written by the daemon's writeCycleMarker at mg>1 (a bare integer string)
// and returns (N, true) only when it trims to a positive integer (>=1). Absent,
// empty, or unparseable markers yield (0, false) so callers fall back to the
// no-cycle path. It is the race-free cycle source for namespaced callers — the
// global taskvisor-current-cycle marker is last-writer-wins under mg>1. It never
// errors or panics.
func (s *Server) readPerGoalCycleMarker(goalID string) (int, bool) {
	p := filepath.Join(s.workingDir, ".tmux-cli", "goals", goalID, "current-cycle")
	data, err := os.ReadFile(p)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

func buildTaskMessage(supervisorWid, workerName, subtask, contextFile, scope, context, researchRoot, deliverable string) string {
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
	fmt.Fprintf(&b, "1. Save the full report to %s/%s-<slug>.md\n", researchRoot, workerName)
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
func (s *Server) WindowsSpawnWorker(supervisorWid, subtask, contextFile, scope, context, deliverable, prefix, workingDirectory string) (*WindowInfo, string, string, error) {
	if prefix == "" {
		prefix = "execute-"
	}
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

	sessionID, err := s.discoverSession()
	if err != nil {
		return nil, "", "", err
	}

	// Authoritative supervisor identity: override the caller-supplied supervisorWid
	// with the spawning window's REAL name, resolved from TMUX_WINDOW_UUID. At
	// MaxGoals>1 the supervisor LLM cannot reliably name its own per-goal window
	// (supervisor-NNN vs the bare "supervisor"), and a wrong name silently misroutes
	// every worker's [EXECUTE:DONE] reply to the wrong window — stalling the goal.
	// Guarded by the env check inside resolveSelfWindowName, so MaxGoals=1 / non-tmux
	// / unit-test contexts (env unset) keep the passed value unchanged. Resolved
	// BEFORE the cap/allocation below so the worker prefix can be derived from it.
	if os.Getenv("TMUX_WINDOW_UUID") != "" {
		if full, lerr := s.executor.ListWindows(sessionID); lerr == nil {
			if self := s.resolveSelfWindowName(sessionID, full); self != "" {
				supervisorWid = self
			}
		}
	}

	// Per-supervisor worker namespacing: derive the worker-window prefix from the
	// resolved supervisor window so BOTH nextExecuteN allocation and the MaxWorkers
	// cap (each prefix-parametric) are scoped PER SUPERVISOR — a sibling goal's
	// workers never consume this goal's budget. "supervisor-<ns>" → "<base>-<ns>-"
	// (e.g. execute-<ns>- / inv-<ns>-), which also matches the daemon's per-goal
	// teardown (killWindowsByPrefix executePrefix/invPrefix), so workers are no
	// longer orphaned at goal completion. Post-P1 a goal supervisor is ALWAYS
	// "supervisor-<ns>" (even at MaxGoals=1), so this derivation fires for every
	// goal. The bare "supervisor" now denotes ONLY window-0 / a standalone
	// interactive supervisor, which never spawns goal workers, so the prefix is
	// left unchanged for it.
	if ns := strings.TrimPrefix(supervisorWid, "supervisor-"); ns != "" && ns != supervisorWid {
		prefix = strings.TrimSuffix(prefix, "-") + "-" + ns + "-"
	}

	windows, err := s.WindowsList()
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to list windows: %w", err)
	}

	settings, _ := setup.LoadSettings(s.workingDir)
	if settings != nil && settings.Supervisor.MaxWorkers > 0 {
		var count int
		for _, w := range windows {
			if strings.HasPrefix(w.Name, prefix) {
				count++
			}
		}
		if count >= settings.Supervisor.MaxWorkers {
			return nil, "", "", fmt.Errorf("%w: %d %sworkers already running (limit: %d) — wait for a worker to finish or increase supervisor.max_workers in setting.yaml",
				ErrMaxWorkersExceeded, count, prefix, settings.Supervisor.MaxWorkers)
		}
	}

	workerName := nextExecuteN(windows, prefix)

	// workingDirectory (E1-1c) starts the worker's shell in the goal's worktree so
	// validate-isolation investigators inherit the isolated tree; "" (the default
	// for every existing caller) keeps the session default cwd unchanged.
	window, err := s.WindowsCreate(workerName, "", workingDirectory)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to create worker window %s: %w", workerName, err)
	}

	err = s.executor.SendMessage(sessionID, window.TmuxWindowID, "/tmux:execute")
	if err != nil {
		_ = s.executor.KillWindow(sessionID, window.TmuxWindowID)
		return nil, "", "", fmt.Errorf("%w: failed to send /tmux:execute to %s: %w",
			ErrTmuxCommandFailed, workerName, err)
	}

	time.Sleep(2 * time.Second)

	researchRoot := s.resolveResearchRoot(supervisorWid)
	_ = os.MkdirAll(filepath.Join(s.workingDir, researchRoot), 0o755)
	taskMessage := buildTaskMessage(supervisorWid, workerName, subtask, contextFile, scope, context, researchRoot, deliverable)

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
	output := &SpecValidateOutput{
		Valid: result.Valid,
		Gaps:  gaps,
		Stats: SpecValidateStats{
			TestCases:          result.Stats.TestCases,
			AcceptanceCriteria: result.Stats.AcceptanceCriteria,
			CodeMapEntries:     result.Stats.CodeMapEntries,
		},
	}

	goalsFile, loadErr := taskvisor.LoadGoals(s.workingDir)
	if loadErr == nil && goalsFile != nil {
		depFindings := taskvisor.InferMissingDeps(goalsFile)
		if len(depFindings) > 0 {
			depWarnings := make([]DepWarning, len(depFindings))
			for i, f := range depFindings {
				depWarnings[i] = DepWarning{
					Consumer: f.Consumer,
					Producer: f.Producer,
					Stem:     f.Stem,
					Evidence: f.Evidence,
				}
			}
			output.DepWarnings = depWarnings
		}
	}

	return output, nil
}

func (s *Server) TasksValidate(in TasksValidateInput) (*TasksValidateOutput, error) {
	// Empty GoalID validates the top-level planning-queue (standalone /tmux:plan
	// flow, unchanged). A set GoalID validates the per-goal fan-out file so the
	// goal-mode step-3b gate checks the isolated path.
	path := tasks.TasksFilePath(s.workingDir)
	if in.GoalID != "" {
		path = tasks.GoalTasksFilePath(s.workingDir, in.GoalID)
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no tasks.yaml found at %s", path)
	}
	errs := tasks.ValidateTasksFile(path)
	return &TasksValidateOutput{
		Valid:  len(errs) == 0,
		Errors: errs,
	}, nil
}
