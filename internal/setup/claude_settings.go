package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type ClaudeSettings struct {
	Hooks HooksConfig `json:"hooks"`
}

type HooksConfig struct {
	SessionStart []HookGroup           `json:"SessionStart,omitempty"`
	SessionEnd   []HookGroup           `json:"SessionEnd,omitempty"`
	Stop         []HookGroup           `json:"Stop,omitempty"`
	PreToolUse   []PreToolUseHookGroup `json:"PreToolUse,omitempty"`
}

type HookGroup struct {
	Hooks []Hook `json:"hooks"`
}

type PreToolUseHookGroup struct {
	Matcher string `json:"matcher"`
	Hooks   []Hook `json:"hooks"`
}

type Hook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

const notifyScript = `"$CLAUDE_PROJECT_DIR"/.tmux-cli/hooks/tmux-session-notify.sh`
const noInteractiveScript = `"$CLAUDE_PROJECT_DIR"/.tmux-cli/hooks/no-interactive-questions.sh`
const supervisorCycleScript = `"$CLAUDE_PROJECT_DIR"/.tmux-cli/hooks/tmux-supervisor-cycle.sh`
const unplannedAuditScript = `"$CLAUDE_PROJECT_DIR"/.tmux-cli/hooks/tmux-unplanned-audit.sh`

func GenerateClaudeSettings(s *Settings) *ClaudeSettings {
	cs := &ClaudeSettings{}

	if s.Hooks.SessionNotify {
		cs.Hooks.SessionStart = []HookGroup{
			{Hooks: []Hook{{Type: "command", Command: notifyScript + " start", Timeout: 10}}},
		}
		cs.Hooks.SessionEnd = []HookGroup{
			{Hooks: []Hook{{Type: "command", Command: notifyScript + " end", Timeout: 10}}},
		}
		cs.Hooks.Stop = []HookGroup{
			{Hooks: []Hook{{Type: "command", Command: notifyScript + " stop", Timeout: 10}}},
		}
	}

	if s.Supervisor.UnplannedAudit {
		cs.Hooks.Stop = append(cs.Hooks.Stop, HookGroup{
			Hooks: []Hook{{Type: "command", Command: unplannedAuditScript + " stop", Timeout: 15}},
		})
	}

	cs.Hooks.Stop = append(cs.Hooks.Stop, HookGroup{
		Hooks: []Hook{{Type: "command", Command: supervisorCycleScript + " stop", Timeout: 15}},
	})

	if s.Hooks.BlockInteractive {
		cs.Hooks.PreToolUse = append(cs.Hooks.PreToolUse, PreToolUseHookGroup{
			Matcher: "AskUserQuestion",
			Hooks:   []Hook{{Type: "command", Command: noInteractiveScript, Timeout: 5}},
		})
	}

	for _, ch := range s.Hooks.Custom {
		hook := Hook{Type: "command", Command: ch.Command, Timeout: ch.Timeout}
		switch ch.Event {
		case "SessionStart":
			cs.Hooks.SessionStart = append(cs.Hooks.SessionStart, HookGroup{Hooks: []Hook{hook}})
		case "SessionEnd":
			cs.Hooks.SessionEnd = append(cs.Hooks.SessionEnd, HookGroup{Hooks: []Hook{hook}})
		case "Stop":
			cs.Hooks.Stop = append(cs.Hooks.Stop, HookGroup{Hooks: []Hook{hook}})
		case "PreToolUse":
			cs.Hooks.PreToolUse = append(cs.Hooks.PreToolUse, PreToolUseHookGroup{
				Matcher: ch.Matcher,
				Hooks:   []Hook{hook},
			})
		}
	}

	return cs
}

func WriteClaudeSettings(projectRoot string, s *Settings) error {
	claudeDir := filepath.Join(projectRoot, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return err
	}

	cs := GenerateClaudeSettings(s)
	data, err := json.MarshalIndent(cs, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o644)
}
