package setup

import "fmt"

type SetupConfig struct {
	ProjectRoot      string
	HookScripts      map[string]string
	CommandTemplates map[string]string
	Templates        map[string]string
	Rules            map[string]string
}

func Run(cfg *SetupConfig) error {
	settings, err := LoadSettings(cfg.ProjectRoot)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}

	if err := WriteHookScripts(cfg.ProjectRoot, cfg.HookScripts); err != nil {
		return fmt.Errorf("write hook scripts: %w", err)
	}

	if err := WriteClaudeSettings(cfg.ProjectRoot, settings); err != nil {
		return fmt.Errorf("write claude settings: %w", err)
	}

	if settings.Commands.Enabled && len(cfg.CommandTemplates) > 0 {
		if err := WriteCommands(cfg.ProjectRoot, cfg.CommandTemplates); err != nil {
			return fmt.Errorf("write commands: %w", err)
		}
	}

	if len(cfg.Templates) > 0 {
		if err := WriteTemplates(cfg.ProjectRoot, cfg.Templates); err != nil {
			return fmt.Errorf("write templates: %w", err)
		}
	}

	if len(cfg.Rules) > 0 {
		if err := WriteRules(cfg.ProjectRoot, cfg.Rules); err != nil {
			return fmt.Errorf("write rules: %w", err)
		}
	}

	if err := EnsureGitExclude(cfg.ProjectRoot); err != nil {
		return fmt.Errorf("ensure git exclude: %w", err)
	}

	return nil
}
