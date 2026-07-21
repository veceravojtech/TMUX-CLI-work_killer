package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Hardening coverage for two PRE-EXISTING defects in the tasks.yaml branch of
// tmux-supervisor-cycle.sh (the fresh-handoff marker branch already carries the
// correct patterns and is pinned by supervisor_fresh_hook_test.go):
//
//  1. OPEN_WORKERS used `grep -c ... || echo "0"`. `grep -c` prints its count AND
//     exits 1 on no match, so the fallback appended a SECOND line — "0\n0" — and
//     `[[ "$OPEN_WORKERS" -gt 0 ]]` emitted a bash arithmetic-syntax error to
//     stderr on every Stop with no workers open.
//  2. CURRENT_CYCLE / MAX_CYCLES / CYCLE_DELAY were only `-z`-checked. A corrupted
//     non-numeric value reached `[[ -ge ]]` / `-gt` / `$(( ))` / `seq`; under the
//     script's `set -u` a bare word like `abc` is an unbound-variable *fatal*, so
//     one bad byte in tasks.yaml killed the hook and silently stopped cycling.
//  3. OPEN_WORKERS piped tmux straight into `grep -c`, discarding tmux's exit
//     status: a FAILED `list-windows` was indistinguishable from a successful read
//     of zero workers, so an unverifiable liveness check licensed a restart on top
//     of workers that may well still be alive.
//
// The harness mirrors the fake-tmux + no-op-sleep shim pattern from
// supervisor_fresh_hook_test.go, but is self-contained here: these tests need a
// parameterisable window list and separated stderr, neither of which that helper
// exposes, and the fresh feature's test files are not ours to edit.

type cycleHookEnv struct {
	projectDir string
	scriptPath string
	shimDir    string
	tmuxLog    string
}

// cycleHookOpts parameterises the fake tmux.
//
// failListWindowsCall, when > 0, makes the shim exit non-zero on the Nth
// `list-windows` invocation (1-based) and only that one. The count matters: call 1
// is the window-uuid discovery walk, which MUST succeed for the script to reach the
// supervisor-window guard and then the OPEN_WORKERS read at call 2. Failing every
// list-windows would strand the script at the identity guard and the test would
// pass without ever exercising the read under test.
type cycleHookOpts struct {
	windowNames         []string
	failListWindowsCall int
}

// newCycleHookEnv materialises the embedded hook plus a fake `tmux` (and a no-op
// `sleep`) on PATH, so the tasks.yaml branch runs with no tmux server present.
// windowNames is what the fake `tmux list-windows -F '#{window_name}'` reports —
// i.e. the input to the OPEN_WORKERS guard.
func newCycleHookEnv(t *testing.T, windowNames ...string) *cycleHookEnv {
	t.Helper()
	return newCycleHookEnvOpts(t, cycleHookOpts{windowNames: windowNames})
}

// newCycleHookEnvOpts is newCycleHookEnv with the failure-injection knobs exposed.
func newCycleHookEnvOpts(t *testing.T, opts cycleHookOpts) *cycleHookEnv {
	t.Helper()

	windowNames := opts.windowNames

	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	tmuxCLIDir := filepath.Join(projectDir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxCLIDir, 0o755))

	scriptPath := filepath.Join(root, "tmux-supervisor-cycle.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte(hookSupervisorCycle), 0o755))

	shimDir := filepath.Join(root, "shim")
	require.NoError(t, os.MkdirAll(shimDir, 0o755))
	tmuxLog := filepath.Join(root, "tmux-calls.log")

	if len(windowNames) == 0 {
		windowNames = []string{"supervisor"}
	}
	var nameEchoes strings.Builder
	for _, n := range windowNames {
		nameEchoes.WriteString(`            echo "` + n + `"` + "\n")
	}

	// Injected only when asked, so the healthy-path shim stays byte-identical.
	failBlock := ""
	if opts.failListWindowsCall > 0 {
		counter := filepath.Join(root, "list-windows-calls")
		failBlock = `if [[ "$1" == "list-windows" ]]; then
    n=$(( $(cat "` + counter + `" 2>/dev/null || echo 0) + 1 ))
    printf '%s' "$n" > "` + counter + `"
    if [[ "$n" -eq ` + strconv.Itoa(opts.failListWindowsCall) + ` ]]; then
        exit 1
    fi
fi
`
	}

	tmuxShim := `#!/usr/bin/env bash
printf '%s\n' "$*" >> "` + tmuxLog + `"
` + failBlock + `case "$1" in
    list-sessions)   echo "sess" ;;
    show-environment) echo "TMUX_CLI_PROJECT_PATH=` + projectDir + `" ;;
    show-options)    echo "uuid-supervisor" ;;
    list-windows)
        if [[ "$*" == *window_id* ]]; then
            echo "@1|supervisor"
        else
` + nameEchoes.String() + `        fi
        ;;
esac
exit 0
`
	require.NoError(t, os.WriteFile(filepath.Join(shimDir, "tmux"), []byte(tmuxShim), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(shimDir, "sleep"),
		[]byte("#!/usr/bin/env bash\nexit 0\n"), 0o755))

	return &cycleHookEnv{
		projectDir: projectDir,
		scriptPath: scriptPath,
		shimDir:    shimDir,
		tmuxLog:    tmuxLog,
	}
}

func (e *cycleHookEnv) writeTasks(t *testing.T, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(e.projectDir, ".tmux-cli", "tasks.yaml"),
		[]byte(body), 0o644))
}

func (e *cycleHookEnv) writeSettings(t *testing.T, maxCycles, cycleDelay string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(e.projectDir, ".tmux-cli", "setting.yaml"),
		[]byte("supervisor:\n    max_cycles: "+maxCycles+"\n    cycle_delay: "+cycleDelay+"\n"), 0o644))
}

// run executes the hook and returns its stderr separately, which is the whole
// point of this harness: defect 1 is invisible in the exit code (the malformed
// comparison evaluates false, so the behaviour was accidentally correct) and
// shows up ONLY as noise on stderr.
func (e *cycleHookEnv) run(t *testing.T) (stderr string, exitCode int) {
	t.Helper()

	cmd := exec.Command("bash", e.scriptPath, "stop")
	cmd.Dir = e.projectDir
	cmd.Stdin = strings.NewReader("{}")
	cmd.Env = append(os.Environ(),
		"TMUX_WINDOW_UUID=uuid-supervisor",
		"CLAUDE_PROJECT_DIR="+e.projectDir,
		"PATH="+e.shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		// Locale-independent diagnostics: bash arithmetic errors are translated,
		// so assertions stay on "stderr is empty", never on message text.
		"LC_ALL=C",
		"LANG=C",
	)

	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	cmd.Stdout = nil

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr, "hook must be runnable")
		return errBuf.String(), exitErr.ExitCode()
	}
	return errBuf.String(), 0
}

func (e *cycleHookEnv) tmuxCalls(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(e.tmuxLog)
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(b)
}

func (e *cycleHookEnv) notifications(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.projectDir, ".tmux-cli", "logs", "notifications.log"))
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(b)
}

func (e *cycleHookEnv) tasksYAML(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.projectDir, ".tmux-cli", "tasks.yaml"))
	require.NoError(t, err)
	return string(b)
}

const pendingTasks = "cycle: 0\nstatus: active\ntasks:\n  - id: 1\n    status: pending\n"

// --- defect 1: OPEN_WORKERS -------------------------------------------------

func TestSupervisorCycleHook_Bash_NoOpenWorkersEmitsNoStderrNoise(t *testing.T) {
	env := newCycleHookEnv(t, "supervisor")
	env.writeSettings(t, "0", "0")
	env.writeTasks(t, pendingTasks)

	stderr, code := env.run(t)

	assert.Equal(t, 0, code, "hook must exit 0")
	assert.Empty(t, stderr,
		"a Stop with zero execute-* workers must produce NO stderr: `grep -c || echo 0` "+
			"yielded a two-line \"0\\n0\" and tripped a bash arithmetic-syntax error")
	assert.Contains(t, env.tmuxCalls(t), "/tmux:supervisor .tmux-cli/tasks.yaml",
		"silencing the noise must not cost the restart — no workers means cycle on")
	assert.Contains(t, env.tasksYAML(t), "cycle: 1",
		"a healthy read of zero workers is the success path: it must still burn a cycle "+
			"(regression guard for the fail-safe read — see the tmux-failure test below)")
}

func TestSupervisorCycleHook_Bash_OpenWorkerStillBlocksRestart(t *testing.T) {
	env := newCycleHookEnv(t, "supervisor", "execute-1")
	env.writeSettings(t, "0", "0")
	env.writeTasks(t, pendingTasks)

	stderr, code := env.run(t)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.NotContains(t, env.tmuxCalls(t), "send-keys",
		"a live execute-* worker must still suppress the cycle restart")
	assert.Contains(t, env.tasksYAML(t), "cycle: 0",
		"a suppressed cycle must not burn a cycle count")
}

// --- defect 3: OPEN_WORKERS must be fail-safe on a tmux failure -------------

// A failed `list-windows` means worker liveness is UNVERIFIABLE. Reading that as
// "zero workers" lets the hook /clear the supervisor pane and restart on top of
// workers that may still be alive. The convention this restores is supervisor.xml
// step 1c: a windows-list failure resets nothing.
//
// Pairs with the two tests above, which pin the healthy paths this must not break:
// zero workers still restarts (and burns a cycle), one execute-1 still suppresses.
func TestSupervisorCycleHook_Bash_TmuxFailureOnWorkerReadRestartsNothing(t *testing.T) {
	env := newCycleHookEnvOpts(t, cycleHookOpts{
		windowNames: []string{"supervisor"},
		// Call 1 is the uuid discovery walk and must succeed; call 2 is the read.
		failListWindowsCall: 2,
	})
	env.writeSettings(t, "0", "0")
	env.writeTasks(t, pendingTasks)

	stderr, code := env.run(t)

	calls := env.tmuxCalls(t)

	// Non-vacuity: prove the run actually reached the OPEN_WORKERS read rather
	// than exiting earlier at the supervisor-window identity guard.
	assert.Contains(t, calls, "#{window_name}",
		"the test must exercise the OPEN_WORKERS read itself, not fail before it")

	assert.Equal(t, 0, code, "an unverifiable liveness check must exit cleanly, not error out")
	assert.Empty(t, stderr)
	assert.NotContains(t, calls, "send-keys",
		"tmux failed, so open workers are UNKNOWN — a hook that cannot see the workers "+
			"must restart nothing (mirrors supervisor.xml step 1c)")
	assert.Contains(t, env.tasksYAML(t), "cycle: 0",
		"a hook that took no action must not burn a cycle")
	assert.NotContains(t, env.notifications(t), "auto-cycle restart",
		"no restart happened, so none may be logged")
}

func TestSupervisorCycleHook_OpenWorkersReadIsFailSafe(t *testing.T) {
	require.Contains(t, hookSupervisorCycle,
		`WINDOW_LIST=$(tmux list-windows -t "$SESSION_ID" -F '#{window_name}' 2>/dev/null) || exit 0`,
		"tmux's exit status must be captured SEPARATELY from the count, and a failed "+
			"read must exit the hook instead of falling through as zero workers")

	idx := strings.Index(hookSupervisorCycle, `grep -c '^execute-'`)
	require.GreaterOrEqual(t, idx, 0, "the open-worker guard must still exist")
	line := hookSupervisorCycle[idx:]
	if nl := strings.Index(line, "\n"); nl >= 0 {
		line = line[:nl]
	}
	assert.NotContains(t, line, "tmux",
		"the count must read the captured list, never a pipe straight from tmux — "+
			"a pipe discards the exit status this guard depends on")
}

// --- defect 2: non-numeric guards ------------------------------------------

func TestSupervisorCycleHook_Bash_CorruptCycleTakesDefault(t *testing.T) {
	env := newCycleHookEnv(t)
	env.writeSettings(t, "0", "0")
	env.writeTasks(t, "cycle: abc\nstatus: active\ntasks:\n  - id: 1\n    status: pending\n")

	stderr, code := env.run(t)

	assert.Equal(t, 0, code,
		"a corrupted cycle: must not kill the hook (`set -u` makes a bare word a fatal unbound variable)")
	assert.Empty(t, stderr)
	assert.Contains(t, env.tmuxCalls(t), "/tmux:supervisor .tmux-cli/tasks.yaml",
		"the corrupted value must fall back to the 0 default and cycle normally")
	assert.Contains(t, env.tasksYAML(t), "cycle: 1",
		"the counter must resume from the default, not from the garbage")
}

func TestSupervisorCycleHook_Bash_CorruptCycleUnderCapStillRestarts(t *testing.T) {
	// max_cycles > 0 takes the `[[ "$CURRENT_CYCLE" -ge "$MAX_CYCLES" ]]` path,
	// which is where an unguarded non-numeric value aborted the hook outright.
	env := newCycleHookEnv(t)
	env.writeSettings(t, "5", "0")
	env.writeTasks(t, "cycle: 1.5\nstatus: active\ntasks:\n  - id: 1\n    status: pending\n")

	stderr, code := env.run(t)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, env.tmuxCalls(t), "/tmux:supervisor .tmux-cli/tasks.yaml")
}

func TestSupervisorCycleHook_Bash_CorruptMaxCyclesTakesDefault(t *testing.T) {
	env := newCycleHookEnv(t)
	env.writeSettings(t, "lots", "0")
	env.writeTasks(t, pendingTasks)

	stderr, code := env.run(t)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, env.tmuxCalls(t), "/tmux:supervisor .tmux-cli/tasks.yaml",
		"an unparseable max_cycles must fall back to 0 = unlimited")
}

func TestSupervisorCycleHook_Bash_CorruptCycleDelayTakesDefault(t *testing.T) {
	env := newCycleHookEnv(t)
	env.writeSettings(t, "0", "soon")
	env.writeTasks(t, pendingTasks)

	stderr, code := env.run(t)

	assert.Equal(t, 0, code, "an unparseable cycle_delay must not reach `seq` as a bad operand")
	assert.Empty(t, stderr)
	calls := env.tmuxCalls(t)
	assert.Contains(t, calls, "Supervisor restarting in",
		"the 5s default must be applied, so the countdown still runs")
	assert.Contains(t, calls, "/tmux:supervisor .tmux-cli/tasks.yaml")
}

// --- regression: the guards must not defeat the real cap -------------------

func TestSupervisorCycleHook_Bash_ValidCycleStillHitsCap(t *testing.T) {
	env := newCycleHookEnv(t)
	env.writeSettings(t, "4", "0")
	env.writeTasks(t, "cycle: 4\nstatus: active\ntasks:\n  - id: 1\n    status: pending\n")

	stderr, code := env.run(t)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.NotContains(t, env.tmuxCalls(t), "send-keys", "a reached cap must still block the restart")
	assert.Contains(t, env.notifications(t), "cycle limit reached")
}

// --- content assertions on the tasks.yaml branch ---------------------------

// tasksBranch returns the slice of the hook script from the tasks.yaml branch
// anchor to end of file — the only region these hardening changes may touch.
func tasksBranch(t *testing.T) string {
	t.Helper()
	start := strings.Index(hookSupervisorCycle, tasksBranchAnchor)
	require.GreaterOrEqual(t, start, 0, "hook must contain the tasks.yaml branch anchor")
	return hookSupervisorCycle[start:]
}

func TestSupervisorCycleHook_TasksBranchGuardsNumericReads(t *testing.T) {
	branch := tasksBranch(t)

	for _, guard := range []string{
		`[[ "$MAX_CYCLES" =~ ^[0-9]+$ ]] || MAX_CYCLES=0`,
		`[[ "$CYCLE_DELAY" =~ ^[0-9]+$ ]] || CYCLE_DELAY=5`,
		`[[ "$CURRENT_CYCLE" =~ ^[0-9]+$ ]] || CURRENT_CYCLE=0`,
	} {
		assert.Contains(t, branch, guard,
			"tasks.yaml branch must mirror the marker branch's numeric-shape guards")
	}
}

func TestSupervisorCycleHook_OpenWorkersCountHasNoEchoFallback(t *testing.T) {
	idx := strings.Index(hookSupervisorCycle, `grep -c '^execute-'`)
	require.GreaterOrEqual(t, idx, 0, "the open-worker guard must still exist")

	line := hookSupervisorCycle[idx:]
	if nl := strings.Index(line, "\n"); nl >= 0 {
		line = line[:nl]
	}
	assert.NotContains(t, line, `echo "0"`,
		`grep -c already prints 0 on no match; the || echo "0" fallback appended a second line`)
	assert.Contains(t, hookSupervisorCycle, `[[ "$OPEN_WORKERS" =~ ^[0-9]+$ ]] || OPEN_WORKERS=0`,
		"the tmux-failure case still needs a numeric-shape guard")
}
