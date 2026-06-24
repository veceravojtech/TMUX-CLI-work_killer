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
	RunTarget      string // "docker" | "local"
	AppSvc         string // PHP/app compose service (docker mode), default "app"
	NodeSvc        string // Node/Playwright compose service (docker + frontend), else ""
	ComposeProject string // main stack's docker compose project name (docker mode); "" on local/legacy no-project path
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
	if svc := appServiceFromDocumentedField(body); svc != "" {
		er.AppSvc = svc
	} else if svc := appServiceFromPublishedPorts(body); svc != "" {
		er.AppSvc = svc
	}
	if playwrightApplicable(body) {
		er.NodeSvc = "e2e"
	}
	er.ComposeProject = resolveComposeProject(projectRoot, body)
	return er
}

// resolveComposeProject derives the main stack's docker compose project name from
// the BASE checkout (never the worktree cwd): projectRoot is routed through
// NormalizeProjectDir first so a per-goal worktree path maps back to base and the
// name stays STABLE across worktrees. Precedence mirrors AppSvc resolution —
// documented field in test-environment.md > COMPOSE_PROJECT_NAME in the main .env
// > normalized base-dir basename — so explicit operator intent overrides the
// basename heuristic. Each candidate is normalized; an all-invalid basename yields
// "" (falls back to the bare legacy wrap, no regression).
func resolveComposeProject(projectRoot, body string) string {
	base := NormalizeProjectDir(projectRoot)
	if name := normalizeComposeName(composeProjectFromDocumentedField(body)); name != "" {
		return name
	}
	if name := normalizeComposeName(composeProjectFromEnvFile(base)); name != "" {
		return name
	}
	return normalizeComposeName(filepath.Base(base))
}

// composeProjectFromDocumentedField returns the compose project name an operator
// explicitly declares via a "Compose Project:" field in test-environment.md, else
// "". Mirrors appServiceFromDocumentedField: split on the FIRST ':' (the colon may
// sit inside markdown bold), strip the `*_\`` wrappers, take the first field.
func composeProjectFromDocumentedField(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(strings.ToLower(line), "compose project") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		val := strings.Trim(line[idx+1:], "*_` \t")
		fields := strings.Fields(val)
		if len(fields) == 0 {
			continue
		}
		return fields[0]
	}
	return ""
}

// composeProjectFromEnvFile reads <base>/.env and returns the value of a
// COMPOSE_PROJECT_NAME assignment (tolerant of a leading `export ` and surrounding
// single/double quotes), else "" when the file is absent/unreadable or the key is
// not set. Line-based parsing — no dotenv dependency.
func composeProjectFromEnvFile(base string) string {
	data, err := os.ReadFile(filepath.Join(base, ".env"))
	if err != nil {
		return ""
	}
	const key = "COMPOSE_PROJECT_NAME="
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		t = strings.TrimSpace(strings.TrimPrefix(t, "export "))
		if !strings.HasPrefix(t, key) {
			continue
		}
		return strings.Trim(strings.TrimSpace(t[len(key):]), `"'`)
	}
	return ""
}

// normalizeComposeName lowercases s and keeps only [a-z0-9_-], then trims any
// leading '_'/'-' — the docker compose project-name normalization (compose-go).
func normalizeComposeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return strings.TrimLeft(b.String(), "_-")
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

// appServiceFromDocumentedField returns the app/PHP compose service name an
// operator explicitly declares via the documented "Runtime Container:" / "APP
// service:" field in test-environment.md, honored VERBATIM (casing preserved),
// else "" so the caller falls through to the published-ports heuristic and then
// the "app" default. This explicit declaration takes precedence over the
// name-matching published-ports row. The colon may sit inside markdown bold
// (`**Runtime Container:** php`), so we split on the FIRST ':' in the line — the
// bold-close `**` lands in the value and is stripped along with the other
// wrapper runes; the first whitespace-delimited token then drops any trailing
// parenthetical (e.g. `php (php-fpm)` -> `php`).
func appServiceFromDocumentedField(body string) string {
	for _, line := range strings.Split(body, "\n") {
		low := strings.ToLower(line)
		if !strings.Contains(low, "runtime container") && !strings.Contains(low, "app service") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		val := strings.Trim(line[idx+1:], "*_` \t")
		fields := strings.Fields(val)
		if len(fields) == 0 {
			continue
		}
		return fields[0]
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
