package taskvisor

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/tmux"
)

type mode int

const (
	modeIdle mode = iota
	modeActive
)

type phase int

const (
	phaseNone phase = iota
	phaseSupervising
	phaseValidating
)

type CreatedWindow struct {
	TmuxWindowID string
	Name         string
}

type WindowCreateFunc func(name, command string) (*CreatedWindow, error)

type Daemon struct {
	workDir                 string
	executor                tmux.TmuxExecutor
	createWindowFn          WindowCreateFunc
	mode                    mode
	session                 string
	pollInterval            time.Duration
	dispatchTimeout         time.Duration
	validateTimeout         time.Duration
	currentGoalDispatchTime time.Time
	currentGoalValidateTime time.Time
	lastSupervisorStatus    string
	phase                   phase
	phaseStartedAt          time.Time
	bootConfirmedAt         time.Time
	validatorSendDelay      time.Duration
	ctx                     context.Context
	cancel                  context.CancelFunc
	currentGoal             string
	exitFunc                func(int)
	signalCh                chan os.Signal
}

func New(workDir string, executor tmux.TmuxExecutor) *Daemon {
	return &Daemon{
		workDir:            workDir,
		executor:           executor,
		mode:               modeIdle,
		pollInterval:       10 * time.Second,
		dispatchTimeout:    time.Hour,
		validateTimeout:    5 * time.Minute,
		validatorSendDelay: 2 * time.Second,
	}
}

func (d *Daemon) SetWindowCreateFunc(fn WindowCreateFunc) {
	d.createWindowFn = fn
}

func (d *Daemon) Run(ctx context.Context) error {
	settings, err := setup.LoadSettings(d.workDir)
	if err != nil {
		log.Printf("warning: failed to load settings: %v", err)
	} else {
		if settings.Taskvisor.PollInterval > 0 {
			d.pollInterval = time.Duration(settings.Taskvisor.PollInterval) * time.Second
		}
		if settings.Taskvisor.DispatchTimeout > 0 {
			d.dispatchTimeout = time.Duration(settings.Taskvisor.DispatchTimeout) * time.Second
		}
		if settings.Taskvisor.ValidateTimeout > 0 {
			d.validateTimeout = time.Duration(settings.Taskvisor.ValidateTimeout) * time.Second
		}
	}

	d.setupSignalHandler(ctx)
	defer d.cancel()

	if err := d.crashRecovery(); err != nil {
		log.Printf("crash recovery error: %v", err)
	}

	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return nil
		case <-ticker.C:
			if err := d.poll(d.ctx); err != nil {
				log.Printf("poll error: %v", err)
			}
		}
	}
}

func (d *Daemon) poll(ctx context.Context) error {
	switch d.mode {
	case modeIdle:
		startPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-start")
		if _, err := os.Stat(startPath); err != nil {
			return nil
		}
		if err := os.Remove(startPath); err != nil {
			return fmt.Errorf("remove start signal: %w", err)
		}

		goals, err := LoadGoals(d.workDir)
		if err != nil {
			return fmt.Errorf("load goals: %w", err)
		}
		if goals == nil {
			return fmt.Errorf("no goals.yaml found")
		}

		return d.activate(goals)

	case modeActive:
		goals, err := LoadGoals(d.workDir)
		if err != nil {
			return fmt.Errorf("load goals: %w", err)
		}
		if goals == nil {
			return nil
		}
		return d.tick(ctx, goals)
	}
	return nil
}

func (d *Daemon) tick(ctx context.Context, goals *GoalsFile) error {
	current, ok := goals.GoalByID(goals.CurrentGoal)
	if !ok {
		return d.deactivate()
	}

	switch current.Status {
	case GoalPending:
		return d.dispatch(current, goals)
	case GoalRunning:
		return d.checkProgress(current, goals)
	case GoalDone, GoalFailed:
		next, hasNext := goals.NextPendingGoal()
		if !hasNext {
			return d.deactivate()
		}
		goals.CurrentGoal = next.ID
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.dispatch(next, goals)
	}
	return nil
}

func (d *Daemon) activate(goals *GoalsFile) error {
	guardPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-active")
	if err := os.MkdirAll(filepath.Dir(guardPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(guardPath, nil, 0o644); err != nil {
		return err
	}

	settings, err := setup.LoadSettings(d.workDir)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	if !settings.Plan.AutoApprove || !settings.Plan.AutoExecute {
		settings.Plan.AutoApprove = true
		settings.Plan.AutoExecute = true
		if err := setup.SaveSettings(d.workDir, settings); err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
	}

	if g, ok := goals.NextPendingGoal(); ok {
		goals.CurrentGoal = g.ID
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
	}

	sessionID, err := d.discoverSession()
	if err != nil {
		return err
	}
	d.session = sessionID

	if err := d.killWindowByName("supervisor"); err != nil {
		return err
	}
	if err := d.killWindowsByPrefix("execute-"); err != nil {
		return err
	}
	if err := d.killWindowByName("validator"); err != nil {
		return err
	}

	d.mode = modeActive
	return nil
}

func (d *Daemon) deactivate() error {
	if err := d.killWindowByName("supervisor"); err != nil {
		return err
	}
	if err := d.killWindowsByPrefix("execute-"); err != nil {
		return err
	}
	if err := d.killWindowByName("validator"); err != nil {
		return err
	}

	allNames := d.collectManagedNames()
	if err := d.waitWindowsGone(allNames, 5*time.Second); err != nil {
		log.Printf("warning: waitWindowsGone: %v", err)
	}

	if _, err := d.createWindow("supervisor", ""); err != nil {
		return fmt.Errorf("create supervisor: %w", err)
	}

	if err := d.waitClaudeBoot("supervisor", 30*time.Second); err != nil {
		log.Printf("warning: waitClaudeBoot: %v", err)
	}

	guardPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-active")
	_ = os.Remove(guardPath)

	d.mode = modeIdle
	return nil
}

func (d *Daemon) dispatch(goal *Goal, goals *GoalsFile) error {
	if err := d.writeDispatchMd(goal); err != nil {
		return fmt.Errorf("write dispatch.md: %w", err)
	}

	currentGoalPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-current-goal")
	if err := os.MkdirAll(filepath.Dir(currentGoalPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(currentGoalPath, []byte(goal.ID), 0o644); err != nil {
		return err
	}

	if err := d.killWindowByName("supervisor"); err != nil {
		return err
	}
	if err := d.killWindowsByPrefix("execute-"); err != nil {
		return err
	}
	if err := d.killWindowByName("validator"); err != nil {
		return err
	}

	allNames := d.collectManagedNames()
	if err := d.waitWindowsGone(allNames, 5*time.Second); err != nil {
		return fmt.Errorf("waitWindowsGone: %w", err)
	}

	winInfo, err := d.createWindow("supervisor", "")
	if err != nil {
		return fmt.Errorf("create supervisor: %w", err)
	}

	if err := d.waitClaudeBoot("supervisor", 30*time.Second); err != nil {
		return fmt.Errorf("waitClaudeBoot: %w", err)
	}

	if err := d.waitForPrompt("supervisor", 30*time.Second); err != nil {
		log.Printf("warning: waitForPrompt: %v (proceeding anyway)", err)
	}

	d.bootConfirmedAt = time.Now()
	d.phase = phaseSupervising

	dispatchPath := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID, "dispatch.md")
	planCmd := fmt.Sprintf("/tmux:plan %s", dispatchPath)
	if err := d.executor.SendMessage(d.session, winInfo.TmuxWindowID, planCmd); err != nil {
		return fmt.Errorf("send plan command: %w", err)
	}

	goal.Status = GoalRunning
	if err := SaveGoals(d.workDir, goals); err != nil {
		return err
	}

	d.currentGoalDispatchTime = time.Now()
	d.lastSupervisorStatus = "dispatched"
	return nil
}

func (d *Daemon) createWindow(name, command string) (*CreatedWindow, error) {
	if d.createWindowFn != nil {
		return d.createWindowFn(name, command)
	}
	return nil, fmt.Errorf("no window create function configured")
}

func (d *Daemon) collectManagedNames() []string {
	allNames := []string{"supervisor", "validator"}
	windows, err := d.listWindows()
	if err == nil {
		for _, w := range windows {
			if strings.HasPrefix(w.Name, "execute-") {
				allNames = append(allNames, w.Name)
			}
		}
	}
	return allNames
}

func (d *Daemon) killWindowByName(name string) error {
	windows, err := d.listWindows()
	if err != nil {
		return err
	}
	for _, w := range windows {
		if w.Name == name {
			return d.executor.KillWindow(d.session, w.TmuxWindowID)
		}
	}
	return nil
}

func (d *Daemon) killWindowsByPrefix(prefix string) error {
	windows, err := d.listWindows()
	if err != nil {
		return err
	}
	for _, w := range windows {
		if strings.HasPrefix(w.Name, prefix) {
			if err := d.executor.KillWindow(d.session, w.TmuxWindowID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Daemon) waitWindowsGone(names []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}

	for {
		windows, err := d.listWindows()
		if err != nil {
			return err
		}
		found := false
		for _, w := range windows {
			if nameSet[w.Name] {
				found = true
				break
			}
		}
		if !found {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for windows to disappear: %v", names)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (d *Daemon) waitForPrompt(windowName string, timeout time.Duration) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = nil
		}
	}()
	deadline := time.Now().Add(timeout)
	winInfo, err := d.findWindowByName(windowName)
	if err != nil {
		return nil
	}
	for {
		output, err := d.executor.CaptureWindowOutput(d.session, winInfo.TmuxWindowID)
		if err != nil {
			return nil
		}
		if strings.Contains(output, "❯") {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for prompt in %q", windowName)
		}
		time.Sleep(2 * time.Second)
	}
}

func (d *Daemon) findWindowByName(name string) (*tmux.WindowInfo, error) {
	windows, err := d.listWindows()
	if err != nil {
		return nil, err
	}
	for i := range windows {
		if windows[i].Name == name {
			return &windows[i], nil
		}
	}
	return nil, fmt.Errorf("window %q not found", name)
}

func (d *Daemon) waitClaudeBoot(windowName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		windows, err := d.listWindows()
		if err != nil {
			return err
		}
		found := false
		for _, w := range windows {
			if w.Name == windowName {
				found = true
				if w.CurrentCommand != "zsh" && w.CurrentCommand != "" {
					return nil
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("window %q not found", windowName)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for Claude boot in %q", windowName)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (d *Daemon) writeDispatchMd(goal *Goal) error {
	goalDir := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID)
	if err := os.MkdirAll(filepath.Join(goalDir, "corrections"), 0o755); err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString("# Dispatch: " + goal.Description + "\n\n")

	sb.WriteString("## Acceptance Criteria\n\n")
	if len(goal.Acceptance) > 0 {
		for _, a := range goal.Acceptance {
			sb.WriteString("- " + a + "\n")
		}
	} else {
		sb.WriteString("(none specified)\n")
	}

	sb.WriteString("\n## Prior Corrections\n\n")
	correctionsDir := filepath.Join(goalDir, "corrections")
	entries, err := os.ReadDir(correctionsDir)
	if err != nil || len(entries) == 0 {
		sb.WriteString("None (first attempt)\n")
	} else {
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		hasCorrections := false
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(correctionsDir, e.Name()))
			if err != nil {
				continue
			}
			sb.WriteString("### " + e.Name() + "\n\n")
			sb.WriteString(string(data) + "\n\n")
			hasCorrections = true
		}
		if !hasCorrections {
			sb.WriteString("None (first attempt)\n")
		}
	}

	dispatchPath := filepath.Join(goalDir, "dispatch.md")
	return os.WriteFile(dispatchPath, []byte(sb.String()), 0o644)
}

func (d *Daemon) discoverSession() (string, error) {
	sessionID, err := d.executor.FindSessionByEnvironment("TMUX_CLI_PROJECT_PATH", d.workDir)
	if err != nil {
		return "", fmt.Errorf("find session: %w", err)
	}
	if sessionID == "" {
		return "", fmt.Errorf("no tmux-cli session found for %s", d.workDir)
	}
	return sessionID, nil
}

func (d *Daemon) listWindows() ([]tmux.WindowInfo, error) {
	if d.session == "" {
		return nil, nil
	}
	return d.executor.ListWindows(d.session)
}

func (d *Daemon) setupSignalHandler(parentCtx context.Context) {
	d.ctx, d.cancel = context.WithCancel(parentCtx)
	d.signalCh = make(chan os.Signal, 1)
	signal.Notify(d.signalCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		defer signal.Stop(d.signalCh)
		select {
		case <-d.signalCh:
			d.cancel()
			exists, err := d.executor.HasSession(d.session)
			if err == nil && exists {
				d.deactivate()
			} else {
				guardPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-active")
				_ = os.Remove(guardPath)
			}
			if d.exitFunc != nil {
				d.exitFunc(0)
			} else {
				os.Exit(0)
			}
		case <-d.ctx.Done():
			return
		}
	}()
}

func (d *Daemon) crashRecovery() error {
	guardPath := filepath.Join(d.workDir, ".tmux-cli", "taskvisor-active")
	if _, err := os.Stat(guardPath); os.IsNotExist(err) {
		return nil
	}

	sessionID, err := d.discoverSession()
	if err != nil {
		log.Printf("crash recovery: no session found: %v", err)
		_ = os.Remove(guardPath)
		return nil
	}
	d.session = sessionID

	goals, err := LoadGoals(d.workDir)
	if err != nil || goals == nil {
		log.Printf("crash recovery: invalid goals.yaml: %v", err)
		return d.deactivate()
	}

	var runningGoal *Goal
	for i := range goals.Goals {
		if goals.Goals[i].Status == GoalRunning {
			runningGoal = &goals.Goals[i]
			break
		}
	}

	if runningGoal == nil {
		return d.deactivate()
	}

	d.mode = modeActive
	d.currentGoal = runningGoal.ID
	goals.CurrentGoal = runningGoal.ID

	sig, err := LoadSignal(d.workDir, runningGoal.ID)
	if err != nil {
		log.Printf("crash recovery: failed to read signal for %s: %v", runningGoal.ID, err)
	}
	if sig != nil {
		d.phaseStartedAt = time.Now()
		return nil
	}

	windows, err := d.executor.ListWindows(d.session)
	if err != nil {
		return err
	}

	hasValidator := false
	hasSupervisor := false
	for _, w := range windows {
		if w.Name == "validator" {
			hasValidator = true
		}
		if w.Name == "supervisor" {
			hasSupervisor = true
		}
	}
	if hasValidator {
		d.phase = phaseValidating
		d.phaseStartedAt = time.Now()
		return nil
	}
	if hasSupervisor {
		d.phase = phaseSupervising
		d.phaseStartedAt = time.Now()
		return nil
	}

	if runningGoal.Retries < runningGoal.MaxRetries {
		runningGoal.Status = GoalPending
	} else {
		runningGoal.Status = GoalFailed
	}
	return SaveGoals(d.workDir, goals)
}

func (d *Daemon) checkProgress(goal *Goal, goals *GoalsFile) error {
	switch d.phase {
	case phaseSupervising:
		return d.checkSupervisingPhase(goal, goals)
	case phaseValidating:
		return d.checkValidatingPhase(goal, goals)
	}
	return nil
}

func (d *Daemon) checkSupervisingPhase(goal *Goal, goals *GoalsFile) error {
	sig, err := LoadSignal(d.workDir, goal.ID)
	if err != nil {
		return fmt.Errorf("load signal: %w", err)
	}

	if sig == nil {
		if !d.currentGoalDispatchTime.IsZero() && time.Since(d.currentGoalDispatchTime) >= d.dispatchTimeout {
			return d.handleFailedCycle(goal, goals, "Cycle timed out — no completion signal received.")
		}
		if !d.bootConfirmedAt.IsZero() && time.Since(d.bootConfirmedAt) > 5*time.Second {
			windows, err := d.listWindows()
			if err != nil {
				return err
			}
			for _, w := range windows {
				if w.Name == "supervisor" && w.CurrentCommand == "zsh" {
					return d.handleFailedCycle(goal, goals, "Crash detected — supervisor returned to shell.")
				}
			}
		}
		return nil
	}

	supSig, ok := sig.(*SupervisorSignal)
	if !ok {
		_ = DeleteSignal(d.workDir, goal.ID)
		return d.handleFailedCycle(goal, goals, "Unexpected signal type during supervising phase.")
	}

	d.lastSupervisorStatus = supSig.Status
	if err := DeleteSignal(d.workDir, goal.ID); err != nil {
		return fmt.Errorf("delete signal: %w", err)
	}

	if err := d.killWindowsByPrefix("execute-"); err != nil {
		return err
	}
	if err := d.killWindowByName("supervisor"); err != nil {
		return err
	}

	if err := d.createValidatorAndSendPayload(goal); err != nil {
		return err
	}

	d.phase = phaseValidating
	d.currentGoalValidateTime = time.Now()
	return nil
}

func (d *Daemon) checkValidatingPhase(goal *Goal, goals *GoalsFile) error {
	sig, err := LoadSignal(d.workDir, goal.ID)
	if err != nil {
		return fmt.Errorf("load signal: %w", err)
	}

	if sig == nil {
		if !d.currentGoalValidateTime.IsZero() && time.Since(d.currentGoalValidateTime) >= d.validateTimeout {
			return d.handleFailedCycle(goal, goals, "Validation timed out — no verdict received.")
		}
		if !d.bootConfirmedAt.IsZero() && time.Since(d.bootConfirmedAt) > 5*time.Second {
			windows, err := d.listWindows()
			if err != nil {
				return err
			}
			for _, w := range windows {
				if w.Name == "validator" && w.CurrentCommand == "zsh" {
					return d.handleFailedCycle(goal, goals, "Crash detected — validator returned to shell.")
				}
			}
		}
		return nil
	}

	valSig, ok := sig.(*ValidatorSignal)
	if !ok {
		_ = DeleteSignal(d.workDir, goal.ID)
		return d.handleFailedCycle(goal, goals, "Unexpected signal type during validating phase.")
	}

	if err := DeleteSignal(d.workDir, goal.ID); err != nil {
		return fmt.Errorf("delete signal: %w", err)
	}

	if err := d.killWindowByName("validator"); err != nil {
		return err
	}

	if valSig.Verdict == "pass" {
		goal.Status = GoalDone
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.advanceToNextGoal(goals)
	}

	return d.handleFailedCycle(goal, goals, valSig.NextAction)
}

func (d *Daemon) writeCorrectionFile(goalDir string, cycleNum int, header, nextAction string) error {
	correctionsDir := filepath.Join(goalDir, "corrections")
	if err := os.MkdirAll(correctionsDir, 0o755); err != nil {
		return err
	}
	filename := fmt.Sprintf("cycle-%d.md", cycleNum)
	content := header + "\n\n" + nextAction
	return os.WriteFile(filepath.Join(correctionsDir, filename), []byte(content), 0o644)
}

func (d *Daemon) handleFailedCycle(goal *Goal, goals *GoalsFile, reason string) error {
	goalDir := filepath.Join(d.workDir, ".tmux-cli", "goals", goal.ID)
	cycleNum := goal.Retries + 1

	var header string
	if d.lastSupervisorStatus == "stopped" {
		header = "Previous cycle hit the supervisor cycle limit — work is incomplete. Prioritize the unmet criteria below over polish or cleanup."
	} else {
		header = "Implementation completed but failed acceptance criteria."
	}

	if err := d.writeCorrectionFile(goalDir, cycleNum, header, reason); err != nil {
		return err
	}

	goal.Retries++
	if goal.Retries >= goal.MaxRetries {
		goal.Status = GoalFailed
		if err := SaveGoals(d.workDir, goals); err != nil {
			return err
		}
		return d.advanceToNextGoal(goals)
	}

	goal.Status = GoalPending
	d.phase = phaseSupervising
	return SaveGoals(d.workDir, goals)
}

func (d *Daemon) advanceToNextGoal(goals *GoalsFile) error {
	next, hasNext := goals.NextPendingGoal()
	if !hasNext {
		return d.deactivate()
	}
	goals.CurrentGoal = next.ID
	return SaveGoals(d.workDir, goals)
}

func (d *Daemon) createValidatorAndSendPayload(goal *Goal) error {
	winInfo, err := d.createWindow("validator", "")
	if err != nil {
		return fmt.Errorf("create validator: %w", err)
	}

	if err := d.waitClaudeBoot("validator", 30*time.Second); err != nil {
		return fmt.Errorf("waitClaudeBoot validator: %w", err)
	}

	if err := d.waitForPrompt("validator", 30*time.Second); err != nil {
		log.Printf("warning: waitForPrompt validator: %v (proceeding anyway)", err)
	}

	d.bootConfirmedAt = time.Now()

	if err := d.executor.SendMessage(d.session, winInfo.TmuxWindowID, "/tmux:validate"); err != nil {
		return fmt.Errorf("send validate command: %w", err)
	}

	if d.validatorSendDelay > 0 {
		time.Sleep(d.validatorSendDelay)
	}

	var sb strings.Builder
	sb.WriteString("GOAL_ID: " + goal.ID + "\n")
	sb.WriteString("GOAL: " + goal.Description + "\n\n")
	sb.WriteString("ACCEPTANCE CRITERIA:\n")
	for _, a := range goal.Acceptance {
		sb.WriteString("- " + a + "\n")
	}
	if len(goal.Validate) > 0 {
		sb.WriteString("\nVALIDATE RULES:\n")
		for _, v := range goal.Validate {
			sb.WriteString("- " + v + "\n")
		}
	}

	if err := d.executor.SendMessageWithDelay(d.session, winInfo.TmuxWindowID, sb.String()); err != nil {
		return fmt.Errorf("send validate payload: %w", err)
	}

	return nil
}
