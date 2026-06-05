package taskvisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func extractValidationRules(md string) []string {
	lines := strings.Split(md, "\n")
	start := indexOfHeading(lines, "Validation Rules")
	if start < 0 {
		return nil
	}

	var rules []string
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "## ") {
			break
		}
		if strings.HasPrefix(trimmed, "- ") {
			cmd := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			if cmd == "(none)" {
				continue
			}
			rules = append(rules, cmd)
		}
	}
	return rules
}

func goalMDDrift(goalDir string, g *Goal) (drifted []string, err error) {
	data, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read goal.md: %w", err)
	}

	mdRules := extractValidationRules(string(data))

	yamlSet := make(map[string]struct{}, len(g.Validate))
	for _, v := range g.Validate {
		yamlSet[strings.TrimSpace(v)] = struct{}{}
	}

	mdSet := make(map[string]struct{}, len(mdRules))
	for _, r := range mdRules {
		mdSet[strings.TrimSpace(r)] = struct{}{}
	}

	for _, v := range g.Validate {
		trimmed := strings.TrimSpace(v)
		if _, ok := mdSet[trimmed]; !ok {
			drifted = append(drifted, fmt.Sprintf("missing in goal.md: %s", trimmed))
		}
	}

	for _, r := range mdRules {
		trimmed := strings.TrimSpace(r)
		if _, ok := yamlSet[trimmed]; !ok {
			drifted = append(drifted, fmt.Sprintf("extra in goal.md: %s", trimmed))
		}
	}

	return drifted, nil
}

func repairValidationRules(goalDir string, g *Goal) error {
	mdPath := filepath.Join(goalDir, "goal.md")
	data, err := os.ReadFile(mdPath)
	if err != nil {
		return fmt.Errorf("read goal.md for repair: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	start := indexOfHeading(lines, "Validation Rules")
	if start < 0 {
		return fmt.Errorf("no Validation Rules heading in goal.md")
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			end = i
			break
		}
	}

	var section strings.Builder
	section.WriteString("## Validation Rules\n\n")
	if len(g.Validate) > 0 {
		for _, v := range g.Validate {
			fmt.Fprintf(&section, "- %s\n", v)
		}
	} else {
		section.WriteString("(none)\n")
	}

	return atomicWrite(mdPath, []byte(joinSections(lines[:start], section.String(), lines[end:])), 0o644)
}
