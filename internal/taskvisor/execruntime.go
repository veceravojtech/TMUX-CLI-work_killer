package taskvisor

import (
	"os"
	"path/filepath"
	"strings"
)

// ExecRuntime captures HOW a project's validate/investigator commands must be
// executed: directly on the host (local) or inside per-service containers
// (docker). It is resolved once from docs/architecture/test-environment.md and
// threaded into goal.md rendering so the daemon — not the generating LLM — is the
// single, deterministic source of truth for command runtime. Distinct from
// goalRuntime, which is per-goal in-flight cycle state.
type ExecRuntime struct {
	RunTarget string // "docker" | "local"
	AppSvc    string // PHP/app compose service (docker mode), default "app"
	NodeSvc   string // Node/Playwright compose service (docker + frontend), else ""
}

// LocalExecRuntime is the no-op default: commands run unchanged on the host.
func LocalExecRuntime() ExecRuntime { return ExecRuntime{RunTarget: "local"} }

// ResolveExecRuntime derives the execution runtime from
// docs/architecture/test-environment.md under projectRoot. An absent/unreadable
// file or a non-docker "Run Target" yields LocalExecRuntime — the byte-identical
// no-op path that keeps non-DDD / local-mode projects unaffected.
func ResolveExecRuntime(projectRoot string) ExecRuntime {
	data, err := os.ReadFile(filepath.Join(projectRoot, "docs", "architecture", "test-environment.md"))
	if err != nil {
		return LocalExecRuntime()
	}
	body := string(data)
	if !runTargetIsDocker(body) {
		return LocalExecRuntime()
	}
	er := ExecRuntime{RunTarget: "docker", AppSvc: "app"}
	if svc := appServiceFromPublishedPorts(body); svc != "" {
		er.AppSvc = svc
	}
	if playwrightApplicable(body) {
		er.NodeSvc = "e2e"
	}
	return er
}

// runTargetIsDocker is true when test-environment.md declares "Run Target: docker"
// (case-insensitive, tolerant of the **bold** markdown the discovery skill emits).
func runTargetIsDocker(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		low := strings.ToLower(line)
		if !strings.Contains(low, "run target") {
			continue
		}
		if idx := strings.Index(low, ":"); idx >= 0 {
			return strings.Contains(low[idx:], "docker")
		}
	}
	return false
}

// appServiceFromPublishedPorts returns the first Published-Ports service row whose
// name is "app" or "php" (the conventional PHP/app container), else "" so the
// caller keeps the default "app". The runtime front-load (task-R) creates a
// service by this name.
func appServiceFromPublishedPorts(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cells := splitTableRow(line)
		if len(cells) == 0 {
			continue
		}
		switch strings.ToLower(cells[0]) {
		case "app", "php":
			return strings.ToLower(cells[0])
		}
	}
	return ""
}

// playwrightApplicable is true unless test-environment.md explicitly marks
// Playwright "not applicable" / "not installed". Mirrors the generation template's
// HAS_FRONTEND / Playwright-availability gate that decides whether Node tooling is
// ever emitted.
func playwrightApplicable(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		low := strings.ToLower(line)
		if !strings.Contains(low, "playwright") {
			continue
		}
		if strings.Contains(low, "not applicable") || strings.Contains(low, "not installed") || strings.Contains(low, "n/a") {
			return false
		}
		if strings.Contains(low, "installed") || strings.Contains(low, "configured") {
			return true
		}
	}
	return false
}

// splitTableRow splits a markdown table row on "|", trimming surrounding
// whitespace and dropping the empty leading/trailing cells from the bordering "|".
func splitTableRow(line string) []string {
	parts := strings.Split(strings.TrimSpace(line), "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
