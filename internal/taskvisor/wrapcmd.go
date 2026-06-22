package taskvisor

import "strings"

type cmdClass int

const (
	classHost cmdClass = iota // host tools: curl, grep, test, python3, docker, ...
	classPHP                  // PHP toolchain: php, composer, bin/console, vendor/bin/*, ...
	classNode                 // Node tools: node, npm, npx, playwright
)

var phpToolPrefixes = []string{"php", "composer", "bin/console", "vendor/bin/", "phpunit", "phpstan", "ecs", "deptrac"}
var nodeToolPrefixes = []string{"node", "npm", "npx", "playwright"}

// wrapCommand rewrites cmd to execute in the correct container for er. Local mode
// and host-only commands pass through unchanged. It is IDEMPOTENT: a command
// already beginning with "docker " is never re-wrapped, so a prefix the generation
// template happens to emit coexists cleanly with this daemon-side normalisation.
func wrapCommand(cmd string, er ExecRuntime) string {
	if er.RunTarget != "docker" {
		return cmd
	}
	t := strings.TrimSpace(cmd)
	if t == "" || strings.HasPrefix(t, "docker ") {
		return cmd
	}
	switch classify(t) {
	case classPHP:
		return dockerExec(er.ComposeProject, er.AppSvc, t)
	case classNode:
		if er.NodeSvc == "" {
			// A Node tool with no Node service should not have been emitted
			// (NODE-TOOL-CONV); leave it for the validation gate to surface
			// rather than silently route it into the PHP container.
			return cmd
		}
		return dockerExec(er.ComposeProject, er.NodeSvc, t)
	default:
		return cmd
	}
}

// dockerExec wraps cmd to run inside service svc. sh -c handles &&, pipes and
// argument quoting uniformly inside the container. When project is non-empty it
// pins the run to that compose project (`-p <project>`) so a worktree-cwd validate
// targets the MAIN running stack instead of a worktree-named project with no
// services; an empty project keeps the output byte-identical to the legacy form.
func dockerExec(project, svc, cmd string) string {
	pin := ""
	if project != "" {
		pin = "-p " + project + " "
	}
	return "docker compose " + pin + "exec -T " + svc + " sh -c " + shSingleQuote(cmd)
}

// classify keys off the first real command token, skipping leading VAR=val
// assignments (e.g. `APP_ENV=test php ...` classifies on `php`).
func classify(cmd string) cmdClass {
	tok := firstCommandToken(cmd)
	for _, p := range nodeToolPrefixes {
		if tok == p {
			return classNode
		}
	}
	for _, p := range phpToolPrefixes {
		if tok == p || (strings.HasSuffix(p, "/") && strings.HasPrefix(tok, p)) {
			return classPHP
		}
	}
	return classHost
}

// firstCommandToken returns the first whitespace-delimited token that is not a
// leading `VAR=value` environment assignment.
func firstCommandToken(cmd string) string {
	for _, tok := range strings.Fields(cmd) {
		if isEnvAssignment(tok) {
			continue
		}
		return tok
	}
	return ""
}

// isEnvAssignment reports whether tok looks like NAME=value (an env prefix).
func isEnvAssignment(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := tok[i]
		if !(c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// shSingleQuote wraps s in single quotes for safe use as one `sh -c` argument,
// escaping embedded single quotes via the standard '\” idiom.
func shSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
