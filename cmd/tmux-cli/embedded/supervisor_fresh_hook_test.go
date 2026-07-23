package embedded

// Bash-shim tests for the two supervisor Stop hooks
// (tmux-supervisor-cycle.sh + tmux-unplanned-audit.sh). They exec the REAL
// committed hook scripts against a fake `tmux` binary so exactly one hook may
// send into the supervisor pane per Stop event and the audit-done ownership
// race is eliminated. See goal-001 / backend task 533.
//
// These run under `make test` (go test -short -race ./...): they must NOT skip
// on -short, so the fake `sleep` shim keeps them fast (no real sleep 2/0.1) and
// setting.yaml uses cycle_delay: 0 to skip the cancellable countdown.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const fakeUUID = "test-window-uuid-0001"

// scriptDir resolves this test file's directory so the sibling .sh hooks are
// located by path (they live next to this file under cmd/tmux-cli/embedded/).
func scriptDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return filepath.Dir(thisFile)
}

// newFakeBinDir writes a fake `tmux` and a no-op `sleep` into a temp bin dir and
// returns it for PATH-prepend. The fake tmux dispatches on the subcommand and
// always exits 0 (a non-zero exit would trip the hooks' `set -euo pipefail`).
func newFakeBinDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	fakeTmux := `#!/usr/bin/env bash
sub="$1"
case "$sub" in
  list-sessions)
    echo "testsess"
    ;;
  show-environment)
    echo "TMUX_CLI_PROJECT_PATH=${FAKE_TMUX_PROJECT_DIR}"
    ;;
  list-windows)
    fmt=""
    prev=""
    for a in "$@"; do
      if [[ "$prev" == "-F" ]]; then fmt="$a"; fi
      prev="$a"
    done
    if [[ "$fmt" == *window_id* ]]; then
      echo "@1|supervisor"
      i=2
      for w in ${FAKE_TMUX_EXTRA_WINDOWS:-}; do echo "@${i}|${w}"; i=$((i+1)); done
    else
      echo "supervisor"
      for w in ${FAKE_TMUX_EXTRA_WINDOWS:-}; do echo "$w"; done
    fi
    ;;
  show-options)
    echo "${FAKE_TMUX_UUID}"
    ;;
  send-keys)
    printf 'send-keys %s\n' "$*" >> "${FAKE_TMUX_LOG}"
    ;;
  display-message)
    printf 'display-message %s\n' "$*" >> "${FAKE_TMUX_LOG}"
    ;;
esac
exit 0
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tmux"), []byte(fakeTmux), 0o755))
	// no-op sleep so the hooks' `sleep 2` / `sleep 0.1` do not slow the suite
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sleep"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755))
	return dir
}

// seedProject creates a temp project dir with a .tmux-cli containing a
// setting.yaml (cycle_delay: 0) and a tasks.yaml with the given status/tasks
// block. tasksStatus is the top-level status: value; tasksBody is appended
// verbatim (the `tasks:` list).
func seedProject(t *testing.T, tasksStatus, tasksBody string) string {
	t.Helper()
	projectDir := t.TempDir()
	tmuxDir := filepath.Join(projectDir, ".tmux-cli")
	require.NoError(t, os.MkdirAll(filepath.Join(tmuxDir, "logs"), 0o755))

	setting := "supervisor:\n  cycle_delay: 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "setting.yaml"), []byte(setting), 0o644))

	tasks := "status: " + tasksStatus + "\ncycle: 1\n" + tasksBody
	require.NoError(t, os.WriteFile(filepath.Join(tmuxDir, "tasks.yaml"), []byte(tasks), 0o644))

	return projectDir
}

// runHook execs one hook script with a fake tmux/sleep on PATH and returns the
// captured fake-tmux log contents (send-keys / display-message lines).
func runHook(t *testing.T, scriptName, projectDir, fakeBinDir string) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "tmux.log")
	scriptPath := filepath.Join(scriptDir(t), scriptName)

	cmd := exec.Command("bash", scriptPath)
	cmd.Stdin = strings.NewReader("{}") // hooks block on `HOOK_INPUT=$(cat)`
	cmd.Env = append(os.Environ(),
		"TMUX_WINDOW_UUID="+fakeUUID,
		"CLAUDE_PROJECT_DIR="+projectDir,
		"PATH="+fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_TMUX_PROJECT_DIR="+projectDir,
		"FAKE_TMUX_UUID="+fakeUUID,
		"FAKE_TMUX_LOG="+logPath,
	)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "hook %s exited non-zero: %s", scriptName, string(out))

	data, readErr := os.ReadFile(logPath)
	if readErr != nil {
		return "" // no log written = the hook sent nothing
	}
	return string(data)
}

const pendingTasks = "tasks:\n  - name: \"do a thing\"\n    wid: \"execute-1\"\n    status: pending\n"
const doneTasks = "tasks:\n  - name: \"did a thing\"\n    wid: \"execute-1\"\n    status: done\n"

func sentinelPath(projectDir string) string {
	return filepath.Join(projectDir, ".tmux-cli", "cycle-restart-queued")
}
func auditDonePath(projectDir string) string {
	return filepath.Join(projectDir, ".tmux-cli", "audit-done")
}
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Case 1: cycle hook writes the sentinel and no longer removes audit-done.
func TestSupervisorCycleHook_WritesSentinelAndDropsAuditDoneRm(t *testing.T) {
	fakeBin := newFakeBinDir(t)
	projectDir := seedProject(t, "ready", pendingTasks)
	// pre-existing audit-done that must NOT be clobbered by the cycle hook
	require.NoError(t, os.WriteFile(auditDonePath(projectDir), []byte(""), 0o644))

	log := runHook(t, "tmux-supervisor-cycle.sh", projectDir, fakeBin)

	require.True(t, fileExists(sentinelPath(projectDir)), "cycle hook must touch cycle-restart-queued")
	require.True(t, fileExists(auditDonePath(projectDir)), "cycle hook must NOT remove pre-existing audit-done")
	require.Contains(t, log, "/clear")
	require.Contains(t, log, "/tmux:supervisor .tmux-cli/tasks.yaml")
}

// Case 2: audit hook yields to a queued restart — consumes the sentinel and
// exits without injecting or touching audit-done.
func TestUnplannedAuditHook_YieldsToQueuedRestart(t *testing.T) {
	fakeBin := newFakeBinDir(t)
	projectDir := seedProject(t, "ready", doneTasks)
	require.NoError(t, os.WriteFile(sentinelPath(projectDir), []byte(""), 0o644))

	log := runHook(t, "tmux-unplanned-audit.sh", projectDir, fakeBin)

	require.NotContains(t, log, "Unplanned work audit", "audit must not inject when a restart is queued")
	require.False(t, fileExists(sentinelPath(projectDir)), "audit hook must consume the sentinel")
	require.False(t, fileExists(auditDonePath(projectDir)), "audit hook must not create audit-done when yielding")
}

// Case 3: audit fires once then is guarded by audit-done on the second run.
func TestUnplannedAuditHook_FiresOnceThenGuarded(t *testing.T) {
	fakeBin := newFakeBinDir(t)
	projectDir := seedProject(t, "ready", doneTasks)

	log1 := runHook(t, "tmux-unplanned-audit.sh", projectDir, fakeBin)
	require.Contains(t, log1, "Unplanned work audit", "first run must inject the audit prompt")
	require.True(t, fileExists(auditDonePath(projectDir)), "first run must create the audit-done guard")

	log2 := runHook(t, "tmux-unplanned-audit.sh", projectDir, fakeBin)
	require.NotContains(t, log2, "Unplanned work audit", "second run must be guarded (no re-inject)")
}

// Case 4: both hooks on one Stop (unfinished work) — exactly one restart stream,
// zero audit injections.
func TestBothHooksOneStop_SingleSendStream(t *testing.T) {
	fakeBin := newFakeBinDir(t)
	projectDir := seedProject(t, "ready", pendingTasks)

	cycleLog := runHook(t, "tmux-supervisor-cycle.sh", projectDir, fakeBin)
	require.Contains(t, cycleLog, "/clear")
	require.Contains(t, cycleLog, "/tmux:supervisor .tmux-cli/tasks.yaml")

	auditLog := runHook(t, "tmux-unplanned-audit.sh", projectDir, fakeBin)
	require.NotContains(t, auditLog, "Unplanned work audit",
		"audit must not inject in the same Stop as a cycle restart (UNFINISHED>0 gate)")
}

// Case 6: an open supervisor-task-* delegation window defers BOTH hooks even
// with zero execute-* windows — the child sub-supervisor may still be planning
// its own fan-out, and that gap must not read as "no workers running".
func TestHooksDefer_WhileDelegationOpen(t *testing.T) {
	t.Setenv("FAKE_TMUX_EXTRA_WINDOWS", "supervisor-task-1")
	fakeBin := newFakeBinDir(t)

	cycleDir := seedProject(t, "ready", pendingTasks)
	cycleLog := runHook(t, "tmux-supervisor-cycle.sh", cycleDir, fakeBin)
	require.NotContains(t, cycleLog, "/clear", "cycle hook must defer while a delegation window is open")
	require.False(t, fileExists(sentinelPath(cycleDir)), "no restart may be queued over a live delegation")

	auditDir := seedProject(t, "ready", doneTasks)
	auditLog := runHook(t, "tmux-unplanned-audit.sh", auditDir, fakeBin)
	require.NotContains(t, auditLog, "Unplanned work audit", "audit must defer while a delegation window is open")
	require.False(t, fileExists(auditDonePath(auditDir)), "audit must not burn its one-shot guard while deferring")
}

// Case 5: baseline — with no sentinel and no guard, the audit injects normally
// (the yield guard did not over-suppress).
func TestUnplannedAuditHook_NoSentinelInjectsNormally(t *testing.T) {
	fakeBin := newFakeBinDir(t)
	projectDir := seedProject(t, "ready", doneTasks)

	log := runHook(t, "tmux-unplanned-audit.sh", projectDir, fakeBin)

	require.Contains(t, log, "Unplanned work audit", "audit must inject when neither sentinel nor guard present")
	require.True(t, fileExists(auditDonePath(projectDir)), "normal audit path must create the guard")
}
