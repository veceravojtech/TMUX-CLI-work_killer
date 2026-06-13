package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/console/tmux-cli/internal/setup"
)

type settingItem struct {
	key    string
	label  string
	kind   string // "bool", "int", or "string"
	value  bool
	intVal int
	strVal string
}

type Model struct {
	items        []settingItem
	cursor       int
	projectRoot  string
	baseSettings *setup.Settings
}

func NewModel(projectRoot string, settings *setup.Settings) Model {
	return Model{
		projectRoot:  projectRoot,
		baseSettings: settings,
		items: []settingItem{
			{key: "hooks.session_notify", label: "Session Notify", kind: "bool", value: settings.Hooks.SessionNotify},
			{key: "hooks.block_interactive", label: "Block Interactive", kind: "bool", value: settings.Hooks.BlockInteractive},
			{key: "commands.enabled", label: "Commands Enabled", kind: "bool", value: settings.Commands.Enabled},
			{key: "supervisor.max_workers", label: "Max Workers", kind: "int", intVal: settings.Supervisor.MaxWorkers},
			{key: "supervisor.cycle_delay", label: "Cycle Timeout (s)", kind: "int", intVal: settings.Supervisor.CycleDelay},
			{key: "supervisor.max_cycles", label: "Max Cycles (0=∞)", kind: "int", intVal: settings.Supervisor.MaxCycles},
			{key: "supervisor.unplanned_audit", label: "Unplanned Audit", kind: "bool", value: settings.Supervisor.UnplannedAudit},
			{key: "plan.auto_approve", label: "Plan Auto-Approve", kind: "bool", value: settings.Plan.AutoApprove},
			{key: "plan.auto_execute", label: "Plan Auto-Execute", kind: "bool", value: settings.Plan.AutoExecute},
			{key: "sudo.timeout", label: "Sudo Timeout (0=∞)", kind: "int", intVal: settings.Sudo.Timeout},
			{key: "taskvisor.dispatch_timeout", label: "Taskvisor Dispatch Timeout", kind: "int", intVal: settings.Taskvisor.DispatchTimeout},
			{key: "taskvisor.validate_timeout", label: "Taskvisor Validate Timeout", kind: "int", intVal: settings.Taskvisor.ValidateTimeout},
			{key: "taskvisor.poll_interval", label: "Taskvisor Poll Interval", kind: "int", intVal: settings.Taskvisor.PollInterval},
			{key: "taskvisor.circuit_breaker_k", label: "Circuit Breaker K", kind: "int", intVal: settings.Taskvisor.CircuitBreakerK},
			{key: "taskvisor.auto_resume_interval_sec", label: "Taskvisor Auto-Resume Interval (s)", kind: "int", intVal: settings.Taskvisor.AutoResumeIntervalSec},
			{key: "taskvisor.transient_retry_max_attempts", label: "Taskvisor Transient Retry Max Attempts", kind: "int", intVal: settings.Taskvisor.TransientRetryMaxAttempts},
			{key: "taskvisor.transient_retry_backoff_ms", label: "Taskvisor Transient Retry Backoff (ms)", kind: "int", intVal: settings.Taskvisor.TransientRetryBackoffMs},
			{key: "supervisor.max_goals", label: "Max Goals (concurrent)", kind: "int", intVal: settings.Supervisor.MaxGoals},
			{key: "supervisor.max_stuck_retries", label: "Max Stuck Retries", kind: "int", intVal: settings.Supervisor.MaxStuckRetries},
			{key: "taskvisor.progress_timeout_sec", label: "Taskvisor Progress Timeout (s)", kind: "int", intVal: settings.Taskvisor.ProgressTimeoutSec},
			{key: "taskvisor.validate_script_timeout_sec", label: "Taskvisor Validate Script Timeout (s)", kind: "int", intVal: settings.Taskvisor.ValidateScriptTimeoutSec},
			{key: "taskvisor.max_wall_clock_sec", label: "Taskvisor Max Wall Clock (sec)", kind: "int", intVal: settings.Taskvisor.MaxWallClockSec},
			{key: "taskvisor.integration_cmd", label: "Taskvisor Integration Cmd", kind: "string", strVal: settings.Taskvisor.IntegrationCmd},
			{key: "taskvisor.require_plan_approval", label: "Require Plan Approval", kind: "bool", value: settings.Taskvisor.RequirePlanApproval},
			{key: "taskvisor.halt_on_stale_binary", label: "Halt On Stale Binary", kind: "bool", value: settings.Taskvisor.HaltOnStaleBinary},
			{key: "taskvisor.restart_on_stale_binary", label: "Restart On Stale Binary", kind: "bool", value: settings.Taskvisor.RestartOnStaleBinary},
			{key: "api.enabled", label: "API Reporting Enabled", kind: "bool", value: settings.API.Enabled},
			{key: "api.url", label: "API URL", kind: "string", strVal: settings.API.URL},
			{key: "taskvisor.auto_commit", label: "Taskvisor Auto-Commit", kind: "bool", value: settings.Taskvisor.AutoCommitEnabled()},
			{key: "plan.audit", label: "Plan Audit", kind: "bool", value: settings.Plan.AuditEnabled()},
			{key: "taskvisor.auto_push", label: "Taskvisor Auto-Push", kind: "bool", value: settings.Taskvisor.AutoPush},
		},
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		cur := &m.items[m.cursor]

		// String items capture printable runes / space / backspace as literal text
		// input — only Esc and the arrow keys escape the field. (Letters like "q" are
		// literal characters here, NOT a quit; bool/int items keep the q/esc quit.)
		if cur.kind == "string" {
			switch msg.Type {
			case tea.KeyEsc:
				return m, tea.Quit
			case tea.KeyUp:
				if m.cursor > 0 {
					m.cursor--
				}
			case tea.KeyDown:
				if m.cursor < len(m.items)-1 {
					m.cursor++
				}
			case tea.KeyBackspace, tea.KeyDelete:
				if r := []rune(cur.strVal); len(r) > 0 {
					cur.strVal = string(r[:len(r)-1])
				}
			case tea.KeyRunes, tea.KeySpace:
				// KeySpace carries Runes==[' '] in bubbletea, so appending msg.Runes
				// types a literal space rather than toggling.
				cur.strVal += string(msg.Runes)
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "esc":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case " ", "enter":
			if cur.kind == "bool" {
				cur.value = !cur.value
			}
		case "right", "l":
			if cur.kind == "int" {
				cur.intVal++
			}
		case "left", "h":
			if cur.kind == "int" && cur.intVal > 0 {
				cur.intVal--
			}
		}
	}
	return m, nil
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).MarginBottom(1)
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)
	checkStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	uncheckStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).MarginTop(1)
)

func (m Model) View() string {
	s := titleStyle.Render("tmux-cli Settings") + "\n"

	for i, item := range m.items {
		var indicator string
		if item.kind == "int" {
			val := fmt.Sprintf("%d", item.intVal)
			if item.intVal == 0 {
				indicator = uncheckStyle.Render("[" + val + "]")
			} else {
				indicator = checkStyle.Render("[" + val + "]")
			}
		} else if item.kind == "string" {
			if item.strVal == "" {
				indicator = uncheckStyle.Render("[ ]")
			} else {
				indicator = checkStyle.Render("[" + item.strVal + "]")
			}
		} else {
			if item.value {
				indicator = checkStyle.Render("[x]")
			} else {
				indicator = uncheckStyle.Render("[ ]")
			}
		}

		line := fmt.Sprintf("%s %s", indicator, item.label)
		if i == m.cursor {
			hint := item.key
			if item.kind == "int" {
				hint += "  ←/→ adjust"
			} else if item.kind == "string" {
				hint += "  type to edit • ⌫ delete"
			}
			line = selectedStyle.Render("> "+line) + "  " + lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(hint)
		} else {
			line = "  " + line
		}
		s += line + "\n"
	}

	s += helpStyle.Render("↑/↓ navigate • space/enter toggle • q/esc save & quit")
	return s
}

func (m Model) ToSettings() *setup.Settings {
	var s setup.Settings
	if m.baseSettings != nil {
		s = *m.baseSettings
	} else {
		s = *setup.DefaultSettings()
	}
	for _, item := range m.items {
		switch item.key {
		case "hooks.session_notify":
			s.Hooks.SessionNotify = item.value
		case "hooks.block_interactive":
			s.Hooks.BlockInteractive = item.value
		case "commands.enabled":
			s.Commands.Enabled = item.value
		case "supervisor.max_workers":
			s.Supervisor.MaxWorkers = item.intVal
		case "supervisor.max_goals":
			s.Supervisor.MaxGoals = item.intVal
		case "supervisor.max_stuck_retries":
			s.Supervisor.MaxStuckRetries = item.intVal
		case "supervisor.cycle_delay":
			s.Supervisor.CycleDelay = item.intVal
		case "supervisor.max_cycles":
			s.Supervisor.MaxCycles = item.intVal
		case "supervisor.unplanned_audit":
			s.Supervisor.UnplannedAudit = item.value
		case "plan.auto_approve":
			s.Plan.AutoApprove = item.value
		case "plan.auto_execute":
			s.Plan.AutoExecute = item.value
		case "sudo.timeout":
			s.Sudo.Timeout = item.intVal
		case "taskvisor.dispatch_timeout":
			s.Taskvisor.DispatchTimeout = item.intVal
		case "taskvisor.validate_timeout":
			s.Taskvisor.ValidateTimeout = item.intVal
		case "taskvisor.poll_interval":
			s.Taskvisor.PollInterval = item.intVal
		case "taskvisor.circuit_breaker_k":
			s.Taskvisor.CircuitBreakerK = item.intVal
		case "taskvisor.auto_resume_interval_sec":
			s.Taskvisor.AutoResumeIntervalSec = item.intVal
		case "taskvisor.progress_timeout_sec":
			s.Taskvisor.ProgressTimeoutSec = item.intVal
		case "taskvisor.validate_script_timeout_sec":
			s.Taskvisor.ValidateScriptTimeoutSec = item.intVal
		case "taskvisor.max_wall_clock_sec":
			s.Taskvisor.MaxWallClockSec = item.intVal
		case "taskvisor.transient_retry_max_attempts":
			s.Taskvisor.TransientRetryMaxAttempts = item.intVal
		case "taskvisor.transient_retry_backoff_ms":
			s.Taskvisor.TransientRetryBackoffMs = item.intVal
		case "taskvisor.integration_cmd":
			s.Taskvisor.IntegrationCmd = item.strVal
		case "taskvisor.require_plan_approval":
			s.Taskvisor.RequirePlanApproval = item.value
		case "taskvisor.halt_on_stale_binary":
			s.Taskvisor.HaltOnStaleBinary = item.value
		case "taskvisor.restart_on_stale_binary":
			s.Taskvisor.RestartOnStaleBinary = item.value
		case "taskvisor.auto_commit":
			v := item.value
			s.Taskvisor.AutoCommit = &v
		case "plan.audit":
			v := item.value
			s.Plan.Audit = &v
		case "taskvisor.auto_push":
			s.Taskvisor.AutoPush = item.value
		case "api.enabled":
			s.API.Enabled = item.value
		case "api.url":
			s.API.URL = item.strVal
		}
	}
	return &s
}

func Run(projectRoot string) error {
	settings, err := setup.LoadSettings(projectRoot)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}

	model := NewModel(projectRoot, settings)
	p := tea.NewProgram(model)
	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}

	if fm, ok := finalModel.(Model); ok {
		if saveErr := setup.SaveSettings(projectRoot, fm.ToSettings()); saveErr != nil {
			return fmt.Errorf("save settings: %w", saveErr)
		}
	}
	return nil
}
