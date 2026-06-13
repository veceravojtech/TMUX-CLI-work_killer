package taskvisor

import "strings"

// ParseInvestigators is the in-package, byte-faithful inverse of
// renderInvestigationConfig (goalmd.go): given goal.md markdown it returns the
// `## Investigation Config` investigators — name/type/commands/Pass/Fail/paths —
// the fields IsPureCommand consumes to classify a check as pure-command. It is the
// single parser shared by the CLI (cmd/tmux-cli/session.go's parseGoalInvestigators
// delegates here, reading the file first) and the in-package parity test, so the
// renderer and reader can never drift. Robust by construction: no
// `## Investigation Config` section → empty slice; malformed/extra lines are
// skipped; it never panics.
func ParseInvestigators(md string) []Investigator {
	lines := strings.Split(md, "\n")

	var invs []Investigator
	section := ""
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "## ") {
			section = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			continue
		}
		if section != "Investigation Config" || !strings.HasPrefix(line, "### ") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "### "))
		if colon := strings.IndexByte(name, ':'); colon >= 0 && strings.HasPrefix(name, "Investigator") {
			name = strings.TrimSpace(name[colon+1:])
		}
		inv := Investigator{Name: name}
		// Scan this investigator's body until the next heading.
		for j := i + 1; j < len(lines); j++ {
			b := strings.TrimSpace(lines[j])
			if strings.HasPrefix(b, "### ") || strings.HasPrefix(b, "## ") {
				break
			}
			stripped := strings.TrimLeft(b, "-* ")
			low := strings.ToLower(stripped)
			switch {
			case strings.HasPrefix(low, "type:"):
				inv.Type = strings.TrimSpace(stripped[strings.IndexByte(stripped, ':')+1:])
			case strings.HasPrefix(low, "command:"):
				if c := strings.TrimSpace(stripped[strings.IndexByte(stripped, ':')+1:]); c != "" {
					inv.Commands = append(inv.Commands, c)
				}
			case strings.HasPrefix(low, "pass:"):
				inv.Pass = strings.TrimSpace(stripped[strings.IndexByte(stripped, ':')+1:])
			case strings.HasPrefix(low, "fail:"):
				inv.Fail = strings.TrimSpace(stripped[strings.IndexByte(stripped, ':')+1:])
			case strings.HasPrefix(low, "paths:") || strings.HasPrefix(low, "path:"):
				val := stripped[strings.IndexByte(stripped, ':')+1:]
				for _, p := range strings.FieldsFunc(val, func(r rune) bool { return r == ',' || r == ' ' }) {
					if p = strings.TrimSpace(p); p != "" {
						inv.Paths = append(inv.Paths, p)
					}
				}
			}
		}
		invs = append(invs, inv)
	}
	return invs
}
