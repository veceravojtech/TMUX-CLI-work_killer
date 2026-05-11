package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/console/tmux-cli/internal/setup"
)

type settingItem struct {
	key   string
	label string
	value bool
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
			{key: "hooks.session_notify", label: "Session Notify", value: settings.Hooks.SessionNotify},
			{key: "hooks.block_interactive", label: "Block Interactive", value: settings.Hooks.BlockInteractive},
			{key: "commands.enabled", label: "Commands Enabled", value: settings.Commands.Enabled},
			{key: "supervisor.unplanned_audit", label: "Unplanned Audit", value: settings.Supervisor.UnplannedAudit},
			{key: "plan.auto_approve", label: "Plan Auto-Approve", value: settings.Plan.AutoApprove},
			{key: "plan.auto_execute", label: "Plan Auto-Execute", value: settings.Plan.AutoExecute},
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
			m.items[m.cursor].value = !m.items[m.cursor].value
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
		var checkbox string
		if item.value {
			checkbox = checkStyle.Render("[x]")
		} else {
			checkbox = uncheckStyle.Render("[ ]")
		}

		line := fmt.Sprintf("%s %s", checkbox, item.label)
		if i == m.cursor {
			line = selectedStyle.Render("> "+line) + "  " + lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(item.key)
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
		case "supervisor.unplanned_audit":
			s.Supervisor.UnplannedAudit = item.value
		case "plan.auto_approve":
			s.Plan.AutoApprove = item.value
		case "plan.auto_execute":
			s.Plan.AutoExecute = item.value
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
