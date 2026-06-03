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
	kind   string // "bool" or "int"
	value  bool
	intVal int
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
		},
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
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
			if m.items[m.cursor].kind == "bool" {
				m.items[m.cursor].value = !m.items[m.cursor].value
			}
		case "right", "l":
			if m.items[m.cursor].kind == "int" {
				m.items[m.cursor].intVal++
			}
		case "left", "h":
			if m.items[m.cursor].kind == "int" && m.items[m.cursor].intVal > 0 {
				m.items[m.cursor].intVal--
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
		case "taskvisor.transient_retry_max_attempts":
			s.Taskvisor.TransientRetryMaxAttempts = item.intVal
		case "taskvisor.transient_retry_backoff_ms":
			s.Taskvisor.TransientRetryBackoffMs = item.intVal
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
