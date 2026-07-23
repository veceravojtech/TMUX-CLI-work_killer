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

// The P2 telemetry pipeline (design §4/§8) instruments the embedded hook scripts
// with fire-and-forget `tmux-cli telemetry emit` calls. These tests pin the
// contract event types and — critically — that a failing/absent emit NEVER
// changes the hook's exit status (contract "hooks must never fail on telemetry").

// --- content assertions on the embedded scripts ----------------------------

func TestSupervisorCycleHook_EmitsContractEvents(t *testing.T) {
	s := hookSupervisorCycle
	// The shared fire-and-forget helper and its guards.
	assert.Contains(t, s, "emit_telemetry()", "cycle hook must define the emit helper")
	assert.Contains(t, s, "command -v tmux-cli", "emit must be guarded on the binary existing")
	assert.Contains(t, s, "|| true", "emit must never abort the hook under set -e")
	assert.Contains(t, s, "tmux-cli telemetry emit --event", "helper must call the contract CLI")

	// marker.consumed on the fresh-handoff restart.
	assert.Contains(t, s, "emit_telemetry marker.consumed", "fresh-handoff consume must emit marker.consumed")
	// supervisor.cycle with all three contract actions.
	assert.Contains(t, s, `"action\":\"restart\"`)
	assert.Contains(t, s, `"action\":\"exhausted\"`)
	assert.Contains(t, s, `"action\":\"stop\"`)
}

func TestSessionNotifyHook_EmitsHookFired(t *testing.T) {
	s := hookSessionNotify
	assert.Contains(t, s, "tmux-cli telemetry emit --event hook.fired", "session-notify must emit hook.fired")
	assert.Contains(t, s, "command -v tmux-cli", "emit must be guarded on the binary existing")
	assert.Contains(t, s, "|| true", "emit must never abort the hook")
	assert.Contains(t, s, `\"hook\":\"session-notify\"`, "payload must name the hook")
	assert.Contains(t, s, `\"action\":\"${EVENT_TYPE}\"`, "payload must carry the lifecycle action")
}

// --- bash-level test with a fake tmux + tmux-cli shim -----------------------

// telemetryHookEnv materialises the supervisor-cycle hook plus fake `tmux`,
// `sleep`, and `tmux-cli` on PATH. The tmux-cli shim appends its argv to a log so
// the test can assert which telemetry emits fired.
type telemetryHookEnv struct {
	projectDir   string
	scriptPath   string
	shimDir      string
	telemetryLog string
}

func newTelemetryHookEnv(t *testing.T) *telemetryHookEnv {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	tmuxCLIDir := filepath.Join(projectDir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(tmuxCLIDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmuxCLIDir, "setting.yaml"),
		[]byte("supervisor:\n    max_cycles: 0\n    cycle_delay: 0\n"), 0o644))

	scriptPath := filepath.Join(root, "tmux-supervisor-cycle.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte(hookSupervisorCycle), 0o755))

	shimDir := filepath.Join(root, "shim")
	require.NoError(t, os.MkdirAll(shimDir, 0o755))

	tmuxShim := `#!/usr/bin/env bash
case "$1" in
    list-sessions)   echo "sess" ;;
    show-environment) echo "TMUX_CLI_PROJECT_PATH=` + projectDir + `" ;;
    show-options)    echo "uuid-supervisor" ;;
    list-windows)
        if [[ "$*" == *window_id* ]]; then echo "@1|supervisor"; else echo "supervisor"; fi ;;
esac
exit 0
`
	require.NoError(t, os.WriteFile(filepath.Join(shimDir, "tmux"), []byte(tmuxShim), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(shimDir, "sleep"),
		[]byte("#!/usr/bin/env bash\nexit 0\n"), 0o755))

	telemetryLog := filepath.Join(root, "telemetry-calls.log")
	tmuxCLIShim := "#!/usr/bin/env bash\n" +
		"if [[ \"$1\" == telemetry && \"$2\" == emit ]]; then printf '%s\\n' \"$*\" >> \"" + telemetryLog + "\"; exit 0; fi\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(shimDir, "tmux-cli"), []byte(tmuxCLIShim), 0o755))

	return &telemetryHookEnv{
		projectDir:   projectDir,
		scriptPath:   scriptPath,
		shimDir:      shimDir,
		telemetryLog: telemetryLog,
	}
}

func (e *telemetryHookEnv) armMarker(t *testing.T) {
	t.Helper()
	plan := filepath.Join(e.projectDir, ".tmux-cli", "next.md")
	require.NoError(t, os.WriteFile(plan, []byte("# next\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(e.projectDir, ".tmux-cli", "fresh-handoff"),
		[]byte("plan: .tmux-cli/next.md\nself_wave: 1\ncycle: 2\n"), 0o644))
}

func (e *telemetryHookEnv) run(t *testing.T) {
	t.Helper()
	cmd := exec.Command("bash", e.scriptPath, "stop")
	cmd.Dir = e.projectDir
	cmd.Stdin = strings.NewReader("{}")
	cmd.Env = append(os.Environ(),
		"TMUX_WINDOW_UUID=uuid-supervisor",
		"CLAUDE_PROJECT_DIR="+e.projectDir,
		"PATH="+e.shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "hook must exit 0 regardless of telemetry; output: %s", string(out))
}

func (e *telemetryHookEnv) telemetryCalls(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(e.telemetryLog)
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(b)
}

func TestSupervisorCycleHook_Bash_FreshHandoffEmitsMarkerConsumed(t *testing.T) {
	env := newTelemetryHookEnv(t)
	env.armMarker(t)

	env.run(t)

	calls := env.telemetryCalls(t)
	assert.Contains(t, calls, "--event marker.consumed", "the consumed one-shot marker must emit marker.consumed")
	assert.Contains(t, calls, "--event supervisor.cycle", "the restart must emit a supervisor.cycle event")
	assert.Contains(t, calls, `"action":"restart"`, "the fresh-handoff restart action is restart")
}

func TestSupervisorCycleHook_Bash_EmitFailureDoesNotFailHook(t *testing.T) {
	env := newTelemetryHookEnv(t)
	// A tmux-cli shim that FAILS the telemetry emit (exit 1).
	tmuxCLIShim := "#!/usr/bin/env bash\n" +
		"if [[ \"$1\" == telemetry && \"$2\" == emit ]]; then exit 1; fi\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(env.shimDir, "tmux-cli"), []byte(tmuxCLIShim), 0o755))
	env.armMarker(t)

	// run() already require.NoError on exit status → proves the failing emit never
	// takes the hook down (set -e + `|| true`).
	env.run(t)
}
