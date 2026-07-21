package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Stop hook grows a fresh-handoff marker branch (design §5b,
// docs/architecture/supervisor-fresh-design.md). These tests pin the contract
// tokens shared with the /tmux:supervisor:fresh command and supervisor.xml, the
// branch ordering relative to the existing guards and the tasks.yaml branch, and
// the one-shot consume-before-send lifecycle.

// --- content assertions on the embedded script -----------------------------

const (
	freshMarkerToken   = ".tmux-cli/fresh-handoff"
	tasksBranchAnchor  = "--- Check tasks.yaml for unfinished tasks ---"
	freshBranchAnchor  = "--- Fresh-context handoff marker"
	freshConsumeToken  = `rm -f "$FRESH_MARKER"`
	freshRestartTarget = "/tmux:supervisor ${FRESH_PLAN}"
)

// freshBranch returns the slice of the hook script covering the marker branch,
// i.e. from its anchor comment up to the start of the tasks.yaml branch.
func freshBranch(t *testing.T) string {
	t.Helper()
	start := strings.Index(hookSupervisorCycle, freshBranchAnchor)
	require.GreaterOrEqual(t, start, 0, "hook must contain the fresh-handoff marker branch anchor")
	end := strings.Index(hookSupervisorCycle, tasksBranchAnchor)
	require.Greater(t, end, start, "tasks.yaml branch must follow the fresh-handoff branch")
	return hookSupervisorCycle[start:end]
}

func TestSupervisorCycleHook_FreshMarkerBranchPresent(t *testing.T) {
	branch := freshBranch(t)

	assert.Contains(t, branch, freshMarkerToken, "marker path must be the byte-exact contract token")
	assert.Contains(t, branch, `if [[ -f "$FRESH_MARKER" ]]`, "branch must be gated on marker existence")
	assert.Contains(t, branch, "plan:", "branch must parse the required plan field")
	assert.Contains(t, branch, "self_wave:", "branch must parse the optional self_wave field")
	assert.Contains(t, branch, "cycle:", "branch must parse the optional cycle field")
	assert.Contains(t, branch, freshRestartTarget, "branch must relaunch onto the marker's plan path")
	assert.Contains(t, branch, `"/clear" Enter`, "branch must clear context before relaunching")
}

func TestSupervisorCycleHook_FreshBranchOrderedBeforeTasksBranch(t *testing.T) {
	fresh := strings.Index(hookSupervisorCycle, freshBranchAnchor)
	tasks := strings.Index(hookSupervisorCycle, tasksBranchAnchor)

	require.GreaterOrEqual(t, fresh, 0)
	require.GreaterOrEqual(t, tasks, 0)
	assert.Less(t, fresh, tasks,
		"an armed handoff must take precedence over leftover unfinished tasks (design §5b)")
}

func TestSupervisorCycleHook_FreshBranchOrderedAfterGuards(t *testing.T) {
	fresh := strings.Index(hookSupervisorCycle, freshBranchAnchor)
	require.GreaterOrEqual(t, fresh, 0)

	// Every pre-existing guard must still fire BEFORE the marker branch is reached.
	guards := []string{
		".tmux-cli/taskvisor-active",
		".tmux-cli/recurring-active",
		"GOALS_FILE=",                    // goals-all-terminal
		`grep -c '^execute-'`,            // open worker windows
		"GUARD_FILE=",                    // auto-execute-guard
		`"$WINDOW_NAME" != "supervisor"`, // window match stays exactly "supervisor"
	}
	for _, guard := range guards {
		idx := strings.Index(hookSupervisorCycle, guard)
		require.GreaterOrEqual(t, idx, 0, "guard %q must exist in the hook", guard)
		assert.Less(t, idx, fresh, "guard %q must precede the fresh-handoff branch", guard)
	}
}

func TestSupervisorCycleHook_ConsumesMarkerBeforeSend(t *testing.T) {
	branch := freshBranch(t)

	consume := strings.Index(branch, freshConsumeToken)
	require.GreaterOrEqual(t, consume, 0, "branch must rm the marker")

	send := strings.Index(branch, "send-keys")
	require.GreaterOrEqual(t, send, 0, "branch must send the restart keys")

	assert.Less(t, consume, send,
		"the marker is one-shot: it must be consumed BEFORE anything is sent (design §4)")
}

func TestSupervisorCycleHook_FreshBranchPlanMissingPath(t *testing.T) {
	branch := freshBranch(t)

	assert.Contains(t, branch, "notifications.log",
		"a missing plan file must be logged, never silently dropped")
	assert.Contains(t, branch, "fresh handoff aborted",
		"the plan-missing log line must be greppable")
	// The abort path consumes the marker before the guarded send path is reached.
	abort := strings.Index(branch, "fresh handoff aborted")
	send := strings.Index(branch, "send-keys")
	require.GreaterOrEqual(t, send, 0)
	assert.Less(t, abort, send, "plan-missing abort must precede any send")
}

func TestSupervisorCycleHook_FreshBranchEnforcesMaxCycles(t *testing.T) {
	branch := freshBranch(t)

	assert.Contains(t, branch, "max_cycles:", "branch must read max_cycles from setting.yaml")
	assert.Contains(t, branch, "cycle_delay:", "branch must reuse cycle_delay for the countdown")
	assert.Contains(t, branch, "cancel-cycle", "branch must reuse the cancel-cycle mechanism")
	assert.Contains(t, branch, "cycle limit reached", "cap rejection must be logged")
}

func TestSupervisorCycleHook_FreshBranchUsesNoJq(t *testing.T) {
	// jq is not available on the host — yaml reads stay grep/sed based (design §4).
	// Comments are stripped so the rationale comment itself is not a false positive.
	var code []string
	for _, line := range strings.Split(freshBranch(t), "\n") {
		if trimmed := strings.TrimSpace(line); !strings.HasPrefix(trimmed, "#") {
			code = append(code, line)
		}
	}
	assert.NotContains(t, strings.Join(code, "\n"), "jq", "marker parsing must not shell out to jq")
	assert.Contains(t, freshBranch(t), "grep -E", "marker parsing uses the script's existing grep/sed style")
}

// --- bash-level tests with a fake tmux shim --------------------------------

type freshHookEnv struct {
	projectDir string
	scriptPath string
	shimDir    string
	tmuxLog    string
	planPath   string
	// cancelOnSleep makes the fake `sleep` create .tmux-cli/cancel-cycle, which
	// deterministically simulates a user aborting mid-countdown.
	cancelOnSleep bool
}

// newFreshHookEnv materialises the embedded hook plus a fake `tmux` (and no-op
// `sleep`) on PATH, so the branch can be exercised with no tmux server running.
func newFreshHookEnv(t *testing.T, cycleDelay string) *freshHookEnv {
	t.Helper()

	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	tmuxCLIDir := filepath.Join(projectDir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxCLIDir, 0o755))

	scriptPath := filepath.Join(root, "tmux-supervisor-cycle.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte(hookSupervisorCycle), 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(tmuxCLIDir, "setting.yaml"),
		[]byte("supervisor:\n    max_cycles: 0\n    cycle_delay: "+cycleDelay+"\n"), 0o644))

	shimDir := filepath.Join(root, "shim")
	require.NoError(t, os.MkdirAll(shimDir, 0o755))
	tmuxLog := filepath.Join(root, "tmux-calls.log")

	tmuxShim := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "` + tmuxLog + `"
case "$1" in
    list-sessions)   echo "sess" ;;
    show-environment) echo "TMUX_CLI_PROJECT_PATH=` + projectDir + `" ;;
    show-options)    echo "uuid-supervisor" ;;
    list-windows)
        if [[ "$*" == *window_id* ]]; then
            echo "@1|supervisor"
        else
            echo "supervisor"
        fi
        ;;
esac
exit 0
`
	require.NoError(t, os.WriteFile(filepath.Join(shimDir, "tmux"), []byte(tmuxShim), 0o755))
	// Keep the countdown and the post-/clear settle sleep from costing real wall
	// time; optionally arm the cancel file from inside the countdown.
	require.NoError(t, os.WriteFile(filepath.Join(shimDir, "sleep"),
		[]byte("#!/usr/bin/env bash\nif [[ -n \"${SHIM_CANCEL_FILE:-}\" ]]; then : > \"$SHIM_CANCEL_FILE\"; fi\nexit 0\n"), 0o755))

	return &freshHookEnv{
		projectDir: projectDir,
		scriptPath: scriptPath,
		shimDir:    shimDir,
		tmuxLog:    tmuxLog,
	}
}

func (e *freshHookEnv) writePlan(t *testing.T, rel string) {
	t.Helper()
	abs := filepath.Join(e.projectDir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte("# next wave\n"), 0o644))
	e.planPath = rel
}

func (e *freshHookEnv) writeMarker(t *testing.T, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(e.projectDir, ".tmux-cli", "fresh-handoff"),
		[]byte(body), 0o644))
}

func (e *freshHookEnv) touch(t *testing.T, rel string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(e.projectDir, rel), []byte(""), 0o644))
}

func (e *freshHookEnv) run(t *testing.T) {
	t.Helper()
	// Registered as `tmux-supervisor-cycle.sh stop` (internal/setup/claude_settings.go).
	cmd := exec.Command("bash", e.scriptPath, "stop")
	cmd.Dir = e.projectDir
	cmd.Stdin = strings.NewReader("{}")
	cmd.Env = append(os.Environ(),
		"TMUX_WINDOW_UUID=uuid-supervisor",
		"CLAUDE_PROJECT_DIR="+e.projectDir,
		"PATH="+e.shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if e.cancelOnSleep {
		cmd.Env = append(cmd.Env,
			"SHIM_CANCEL_FILE="+filepath.Join(e.projectDir, ".tmux-cli", "cancel-cycle"))
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "hook must exit 0; output: %s", string(out))
}

func (e *freshHookEnv) tmuxCalls(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(e.tmuxLog)
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(b)
}

func (e *freshHookEnv) notifications(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.projectDir, ".tmux-cli", "logs", "notifications.log"))
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(b)
}

func (e *freshHookEnv) markerExists() bool {
	_, err := os.Stat(filepath.Join(e.projectDir, ".tmux-cli", "fresh-handoff"))
	return err == nil
}

func TestSupervisorCycleHook_Bash_ArmedMarkerRestartsOntoPlan(t *testing.T) {
	env := newFreshHookEnv(t, "0")
	env.writePlan(t, ".tmux-cli/research/2026-07-21-17/next-wave-2.md")
	env.writeMarker(t, "plan: "+env.planPath+"\nself_wave: 1\ncycle: 3\nrequested_by: supervisor\ncreated: 2026-07-21T12:34:56Z\n")

	env.run(t)

	calls := env.tmuxCalls(t)
	assert.Contains(t, calls, `send-keys -t sess:@1 /clear Enter`)
	assert.Contains(t, calls, `send-keys -t sess:@1 /tmux:supervisor `+env.planPath+` Enter`)
	assert.False(t, env.markerExists(), "marker must be consumed (one-shot)")

	clearIdx := strings.Index(calls, "/clear")
	restartIdx := strings.Index(calls, "/tmux:supervisor")
	assert.Less(t, clearIdx, restartIdx, "/clear must be sent before the relaunch")
}

func TestSupervisorCycleHook_Bash_MissingPlanConsumesAndSkips(t *testing.T) {
	env := newFreshHookEnv(t, "0")
	env.writeMarker(t, "plan: .tmux-cli/research/gone/next-wave-9.md\ncycle: 1\n")

	env.run(t)

	assert.NotContains(t, env.tmuxCalls(t), "send-keys", "must never restart onto a missing plan")
	assert.False(t, env.markerExists(), "marker must be consumed even on the abort path")
	assert.Contains(t, env.notifications(t), "fresh handoff aborted")
}

func TestSupervisorCycleHook_Bash_TaskvisorActiveStillDefers(t *testing.T) {
	env := newFreshHookEnv(t, "0")
	env.writePlan(t, ".tmux-cli/research/next-wave-2.md")
	env.writeMarker(t, "plan: "+env.planPath+"\n")
	env.touch(t, ".tmux-cli/taskvisor-active")

	env.run(t)

	assert.NotContains(t, env.tmuxCalls(t), "send-keys", "daemon owns dispatch while taskvisor-active")
	assert.True(t, env.markerExists(), "a deferred hook must leave the marker armed")
}

func TestSupervisorCycleHook_Bash_MaxCyclesCapBlocksRestart(t *testing.T) {
	env := newFreshHookEnv(t, "0")
	env.writePlan(t, ".tmux-cli/research/next-wave-2.md")
	env.writeMarker(t, "plan: "+env.planPath+"\ncycle: 4\n")
	require.NoError(t, os.WriteFile(filepath.Join(env.projectDir, ".tmux-cli", "setting.yaml"),
		[]byte("supervisor:\n    max_cycles: 4\n    cycle_delay: 0\n"), 0o644))

	env.run(t)

	assert.NotContains(t, env.tmuxCalls(t), "send-keys", "cap must block the restart")
	assert.False(t, env.markerExists(), "a capped marker must still be consumed, never left armed")
	assert.Contains(t, env.notifications(t), "cycle limit reached")
}

func TestSupervisorCycleHook_Bash_CancelCycleSkipsRestart(t *testing.T) {
	env := newFreshHookEnv(t, "3")
	env.writePlan(t, ".tmux-cli/research/next-wave-2.md")
	env.writeMarker(t, "plan: "+env.planPath+"\n")
	// A cancel file pre-dating the countdown is cleared (stale-cancel guard, mirroring
	// the tasks.yaml branch); the abort must come from INSIDE the countdown window.
	env.touch(t, ".tmux-cli/cancel-cycle")
	env.cancelOnSleep = true

	env.run(t)

	assert.NotContains(t, env.tmuxCalls(t), "send-keys", "a cancelled countdown must not restart")
	assert.False(t, env.markerExists(), "cancel leaves the marker consumed — no re-arm")
	assert.Contains(t, env.notifications(t), "cancelled")
}

func TestSupervisorCycleHook_Bash_NoMarkerLeavesTasksBranchIntact(t *testing.T) {
	env := newFreshHookEnv(t, "0")
	require.NoError(t, os.WriteFile(filepath.Join(env.projectDir, ".tmux-cli", "tasks.yaml"),
		[]byte("cycle: 0\nstatus: active\ntasks:\n  - id: 1\n    status: pending\n"), 0o644))

	env.run(t)

	calls := env.tmuxCalls(t)
	assert.Contains(t, calls, "/tmux:supervisor .tmux-cli/tasks.yaml",
		"the untouched tasks.yaml branch must still restart on unfinished tasks")
}
