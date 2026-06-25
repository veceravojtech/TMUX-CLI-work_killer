package taskvisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// composeOverrideFileName is the port-strip override the runner writes into the
// worktree on Up. It is threaded EXPLICITLY via `-f <base> -f <override>` (never
// COMPOSE_FILE) so merge order is deterministic and independent of env-var
// precedence. The `taskvisor.` infix keeps it visually owned by the daemon and
// unlikely to collide with any operator-authored override.
const composeOverrideFileName = "docker-compose.taskvisor.override.yml"

// composeBaseFileCandidates is the discovery order for the base compose file in a
// worktree. Covers both the legacy `docker-compose.*` and the modern `compose.*`
// names so the mechanism stays project-agnostic (no hardcoded file name).
var composeBaseFileCandidates = []string{
	"docker-compose.yml",
	"docker-compose.yaml",
	"compose.yaml",
	"compose.yml",
}

// ComposeRunnerFunc is the injectable command seam mirroring ScriptRunnerFunc
// (dispatch.go): it shells `docker <args...>` with cwd=dir and the given env,
// returning captured stdout/stderr, the process exit code, and a non-nil err ONLY
// for an exec-layer failure (a non-zero exit is reported via exitCode, err==nil).
// Unit tests inject a fake to assert argv + cwd without a Docker daemon present.
type ComposeRunnerFunc func(ctx context.Context, dir string, env []string, args ...string) (stdout, stderr string, exitCode int, err error)

// defaultComposeRunner is the production ComposeRunnerFunc: it runs the real
// `docker` binary with cmd.Dir=dir so the relative `.:/app` bind-mount and
// `build: context: .` in the base compose resolve to the WORKTREE, not the
// operator's master checkout. Mirrors defaultScriptRunner's exec/ExitError
// handling exactly.
func defaultComposeRunner(ctx context.Context, dir string, env []string, args ...string) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	cmd.Env = env
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
		}
		return outBuf.String(), errBuf.String(), -1, runErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
}

// ComposeStack is the daemon-owned, per-worktree compose mechanism. It brings a
// goal's OWN stack (project name taskvisor-<goalID>) up with cwd=worktree so the
// stack sees the goal's uncommitted edits, and tears it down with volumes so the
// fresh per-worktree db-data leaves no leak. It NEVER touches the operator's main
// stack — Down hard-refuses any project name that is not taskvisor-prefixed.
//
// T1 supplies this API only; wiring it into the goal lifecycle (goals.go /
// state-machine) is T2's job.
type ComposeStack struct {
	Project      string // taskvisor-<goalID>, normalized — the per-worktree compose project
	Worktree     string // cwd for every compose invocation (the goal's checkout)
	AppSvc       string // app/PHP compose service (from ExecRuntime) — exec target + port-strip key
	BaselineCmd  string // migration/baseline command run via exec -T after up -d; "" ⇒ skipped
	BaseFile     string // resolved base compose file in the worktree; "" ⇒ Up errors
	OverrideFile string // absolute path of the port-strip override written on Up

	run ComposeRunnerFunc
}

// WorktreeComposeProject is the per-worktree compose project name for a goal:
// normalizeComposeName("taskvisor-" + goalID). The taskvisor- prefix is added
// BEFORE normalization so the guard token survives (normalizeComposeName trims
// leading separators and drops invalid chars) — Down keys its base-project
// safety guard off that surviving prefix. Pure function, no *Daemon receiver:
// trivially unit-testable and what T2 consumes.
func WorktreeComposeProject(goalID string) string {
	return normalizeComposeName("taskvisor-" + goalID)
}

// NewComposeStack constructs a ComposeStack for goalID rooted at worktree. The
// app service is taken from the resolved ExecRuntime (never hardcoded). run
// defaults to defaultComposeRunner when nil. BaseFile is resolved by stat-ing the
// candidate compose file names under worktree, in order; an absent base file
// leaves BaseFile=="" so Up fails fast rather than starting a half-configured
// stack.
func NewComposeStack(er ExecRuntime, goalID, worktree, baselineCmd string, run ComposeRunnerFunc) *ComposeStack {
	if run == nil {
		run = defaultComposeRunner
	}
	s := &ComposeStack{
		Project:      WorktreeComposeProject(goalID),
		Worktree:     worktree,
		AppSvc:       er.AppSvc,
		BaselineCmd:  baselineCmd,
		OverrideFile: filepath.Join(worktree, composeOverrideFileName),
		run:          run,
	}
	for _, name := range composeBaseFileCandidates {
		p := filepath.Join(worktree, name)
		if _, err := os.Stat(p); err == nil {
			s.BaseFile = p
			break
		}
	}
	return s
}

// resolveBaselineCmd returns the migration/baseline command an operator declares
// via a "Stack Baseline:" / "Baseline Command:" field in test-environment.md,
// else "". Mirrors composeProjectFromDocumentedField (split on the FIRST ':' so a
// colon inside the command — e.g. doctrine:migrations — is preserved) but returns
// the WHOLE trimmed remainder rather than the first whitespace-delimited token,
// since a baseline command is multi-word. Empty ⇒ the Up migration step is
// skipped. Adds NO field to ExecRuntime, so existing full-struct equality tests
// stay byte-identical.
func resolveBaselineCmd(body string) string {
	for _, line := range strings.Split(body, "\n") {
		low := strings.ToLower(line)
		if !strings.Contains(low, "stack baseline") && !strings.Contains(low, "baseline command") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		val := strings.Trim(line[idx+1:], "*_` \t")
		if val == "" {
			continue
		}
		return val
	}
	return ""
}

// portStripOverride is the override body that empties the inherited published
// ports of the app and db services. `!reset []` (compose v2.24+) REPLACES the
// inherited sequence — a plain `ports: []` would append-merge and leave the host
// bindings intact. Stripping host ports lets the per-worktree stack coexist with
// the operator's main stack: validate execs over the internal compose network
// (exec -T <app>, db:5432), so no host binding is needed. The app service name is
// taken from the resolved ExecRuntime (never hardcoded); db is the conventional
// database service key.
func portStripOverride(appSvc string) string {
	return fmt.Sprintf("services:\n  %s:\n    ports: !reset []\n  db:\n    ports: !reset []\n", appSvc)
}

// Up brings the per-worktree stack to a migrated baseline. It (1) fails fast if no
// base compose file was located, (2) writes the port-strip override into the
// worktree, (3) runs `docker compose -p <project> -f <base> -f <override> up -d`
// with cwd=worktree, and (4) — when BaselineCmd is set — runs it via `exec -T
// <appSvc> sh -c <cmd>` so the fresh per-worktree DB is migrated before the
// deterministic validate.sh ever touches it. Any non-zero exit or exec error is
// wrapped and returned; Up never auto-fires Down on failure (the caller decides).
func (s *ComposeStack) Up(ctx context.Context) error {
	if s.BaseFile == "" {
		return fmt.Errorf("composestack: cannot locate base compose file under %s", s.Worktree)
	}
	if err := os.WriteFile(s.OverrideFile, []byte(portStripOverride(s.AppSvc)), 0o644); err != nil {
		return fmt.Errorf("composestack: write override %s: %w", s.OverrideFile, err)
	}

	env := os.Environ()
	_, stderr, code, err := s.run(ctx, s.Worktree, env,
		"compose", "-p", s.Project, "-f", s.BaseFile, "-f", s.OverrideFile, "up", "-d")
	if err != nil {
		return fmt.Errorf("composestack: up exec error: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("composestack: up failed (exit %d): %s", code, strings.TrimSpace(stderr))
	}

	if s.BaselineCmd == "" {
		return nil
	}
	_, stderr, code, err = s.run(ctx, s.Worktree, env,
		"compose", "-p", s.Project, "-f", s.BaseFile, "-f", s.OverrideFile,
		"exec", "-T", s.AppSvc, "sh", "-c", s.BaselineCmd)
	if err != nil {
		return fmt.Errorf("composestack: baseline migrate exec error: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("composestack: baseline migrate failed (exit %d): %s", code, strings.TrimSpace(stderr))
	}
	return nil
}

// Down tears the per-worktree stack down WITH volumes (`down -v`) so the fresh
// db-data named volume is removed and nothing leaks between cycles. It first
// hard-guards that Project is taskvisor-prefixed: a misresolved project name
// nuking the operator's BASE volume is the worst-case failure, so a non-taskvisor
// project returns an error and issues NO compose command at all.
func (s *ComposeStack) Down(ctx context.Context) error {
	if !strings.HasPrefix(s.Project, "taskvisor-") {
		return fmt.Errorf("composestack: refusing 'down -v' on non-taskvisor project %q (base-project safety guard)", s.Project)
	}
	_, stderr, code, err := s.run(ctx, s.Worktree, os.Environ(),
		"compose", "-p", s.Project, "down", "-v")
	if err != nil {
		return fmt.Errorf("composestack: down exec error: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("composestack: down failed (exit %d): %s", code, strings.TrimSpace(stderr))
	}
	return nil
}
