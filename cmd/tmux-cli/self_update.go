package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/console/tmux-cli/internal/session"
	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/tmux"
	"github.com/spf13/cobra"
)

// restartMode selects what gets restarted after a successful self-update.
type restartMode string

const (
	restartDaemon  restartMode = "daemon"
	restartClaude  restartMode = "claude"
	restartSession restartMode = "session"
	restartAuto    restartMode = "auto"
)

// selfUpdateConfig carries every input doSelfUpdate needs, with all
// side-effecting collaborators (env lookup, build command, stderr sink)
// injectable so unit tests never touch the real environment or run
// `make install`.
type selfUpdateConfig struct {
	ProjectDir       string
	SourceFlag       string
	SettingSourceDir string
	ResumeState      string
	SupervisorUUID   string
	SessionFlag      string
	InstallPath      string
	Mode             restartMode
	Getenv           func(string) string
	BuildCmd         []string
	Stderr           io.Writer
}

// selfUpdateResult reports what the update did and, on failure, which
// stage it failed in (e.g. "build").
type selfUpdateResult struct {
	BinaryChanged bool
	Stage         string
	Source        string
	Restart       string
}

// resolveSourceDir resolves the tmux-cli source directory with precedence
// --source flag > TMUX_CLI_SRC env > setting.yaml source dir, refusing a
// source that equals the target project itself.
func resolveSourceDir(cfg selfUpdateConfig) (string, error) {
	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	dir := cfg.SourceFlag
	if dir == "" {
		dir = getenv("TMUX_CLI_SRC")
	}
	if dir == "" {
		dir = cfg.SettingSourceDir
	}
	if dir == "" {
		return "", fmt.Errorf("no source directory: pass --source, set TMUX_CLI_SRC, or set self_update.source_dir in setting.yaml")
	}
	// source == project is refused so an arbitrary target project can never be
	// "rebuilt as tmux-cli" — EXCEPT when the dir genuinely IS a tmux-cli source
	// checkout (module path + Makefile): the default max_goals=1 inline mode has
	// buildDir == workDir in the dogfood repo, and the daemon's repair-cycle
	// self-reinstall hook must be able to build it.
	if samePath(dir, cfg.ProjectDir) && !setup.IsCliSourceCheckout(dir) {
		return "", fmt.Errorf("source directory %s is the target project itself — refusing self-target update", dir)
	}
	return dir, nil
}

// samePath reports whether two paths name the same directory, resolving
// symlinks when possible and failing open to lexical Clean comparison.
func samePath(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ra, err := filepath.EvalSymlinks(ca); err == nil {
		ca = ra
	}
	if rb, err := filepath.EvalSymlinks(cb); err == nil {
		cb = rb
	}
	return ca == cb
}

// binaryChanged reports whether the file at path differs (size or mtime)
// from the pre-build stat captured in before.
func binaryChanged(path string, before os.FileInfo) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	// mtime via Equal, not == — the monotonic clock component breaks ==.
	return !fi.ModTime().Equal(before.ModTime()) || fi.Size() != before.Size(), nil
}

// rebuildAndInstall runs cfg.BuildCmd in sourceDir to rebuild and install
// the tmux-cli binary.
func rebuildAndInstall(cfg selfUpdateConfig, sourceDir string) error {
	buildCmd := cfg.BuildCmd
	if len(buildCmd) == 0 {
		buildCmd = []string{"make", "install"}
	}
	out := cfg.Stderr
	if out == nil {
		out = io.Discard
	}
	cmd := exec.Command(buildCmd[0], buildCmd[1:]...)
	cmd.Dir = sourceDir
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build command %q in %s: %w", strings.Join(buildCmd, " "), sourceDir, err)
	}
	return nil
}

// daemonPIDAlive reports whether .tmux-cli/taskvisor.pid under projectDir
// names a live process.
func daemonPIDAlive(projectDir string) bool {
	data, err := os.ReadFile(filepath.Join(projectDir, ".tmux-cli", "taskvisor.pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// resolveAutoMode picks the concrete restart target for restartAuto: the
// daemon when its PID file names a live process, else claude when exactly
// one tmux session exists, else no restart at all ("" — the update stays
// installed and the next manual restart adopts it).
func resolveAutoMode(cfg selfUpdateConfig, executor tmux.TmuxExecutor) restartMode {
	if daemonPIDAlive(cfg.ProjectDir) {
		return restartDaemon
	}
	if executor != nil {
		if _, err := resolveRestartSession(cfg, executor); err == nil {
			return restartClaude
		}
	}
	return ""
}

// sessionForWindowUUID returns the tmux session that owns the window whose
// WindowUUIDOption equals uuid, mirroring internal/mcp resolveSelfWindowName
// but scanning across ALL sessions (self-update runs outside the MCP server,
// so it cannot assume a single discovered session). Returns "" when uuid is
// empty or ListSessions errors; per-session ListWindows / per-window
// GetWindowOption failures are skipped (a dead or permission-denied session
// must not abort resolution), never fatal.
func sessionForWindowUUID(executor tmux.TmuxExecutor, uuid string) string {
	if uuid == "" {
		return ""
	}
	sessions, err := executor.ListSessions()
	if err != nil {
		return ""
	}
	for _, sessionID := range sessions {
		windows, err := executor.ListWindows(sessionID)
		if err != nil {
			continue
		}
		for _, w := range windows {
			got, err := executor.GetWindowOption(sessionID, w.TmuxWindowID, tmux.WindowUUIDOption)
			if err != nil {
				continue
			}
			if got == uuid {
				return sessionID
			}
		}
	}
	return ""
}

// resolveRestartSession derives the tmux session a restart should target from
// the invoking context, with precedence: (1) an explicit --session flag,
// validated against ListSessions and erroring when absent — silently
// restarting the wrong session is worse than refusing; (2) the caller's
// TMUX_WINDOW_UUID, resolved to the session owning that window; (3)
// singleSessionID as the last-resort fallback (preserving the exact
// "expected exactly 1" error on a multi-session host with no hint). getenv
// defaults to os.Getenv when cfg.Getenv is nil, matching resolveSourceDir, so
// zero-value-cfg callers never nil-panic.
func resolveRestartSession(cfg selfUpdateConfig, executor tmux.TmuxExecutor) (string, error) {
	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if cfg.SessionFlag != "" {
		sessions, err := executor.ListSessions()
		if err != nil {
			return "", fmt.Errorf("list tmux sessions: %w", err)
		}
		for _, s := range sessions {
			if s == cfg.SessionFlag {
				return s, nil
			}
		}
		return "", fmt.Errorf("--session %q not found among running tmux sessions", cfg.SessionFlag)
	}
	if sessionID := sessionForWindowUUID(executor, getenv("TMUX_WINDOW_UUID")); sessionID != "" {
		return sessionID, nil
	}
	return singleSessionID(executor)
}

// singleSessionID resolves the one running tmux session, refusing when zero
// or several exist — no guessing across sessions. It is the last-resort
// fallback inside resolveRestartSession.
func singleSessionID(executor tmux.TmuxExecutor) (string, error) {
	sessions, err := executor.ListSessions()
	if err != nil {
		return "", fmt.Errorf("list tmux sessions: %w", err)
	}
	if len(sessions) != 1 {
		return "", fmt.Errorf("cannot pick a restart target: found %d tmux sessions, expected exactly 1", len(sessions))
	}
	return sessions[0], nil
}

// dispatchClaudeRestart terminates the supervisor window's Claude process
// (window preserved, so window options survive) and relaunches Claude via
// the standard fallback chain, which includes `claude --resume`. A single
// C-c (InterruptWindow) does not exit Claude Code, so termination is
// deterministic — the pane's foreground child is killed and the pane is back
// at a shell before the relaunch chain types the launch commands.
func dispatchClaudeRestart(cfg selfUpdateConfig, executor tmux.TmuxExecutor) error {
	sessionID, err := resolveRestartSession(cfg, executor)
	if err != nil {
		return err
	}
	windows, err := executor.ListWindows(sessionID)
	if err != nil {
		return fmt.Errorf("list windows in %s: %w", sessionID, err)
	}
	windowID := ""
	for _, w := range windows {
		if w.Name == "supervisor" {
			windowID = w.TmuxWindowID
			break
		}
	}
	if windowID == "" {
		return fmt.Errorf("no supervisor window in session %s", sessionID)
	}
	if err := executor.TerminateWindowProcess(windowID); err != nil {
		return fmt.Errorf("terminate supervisor Claude before relaunch: %w", err)
	}
	return session.ExecutePostCommandWithFallback(executor, sessionID, "supervisor", session.DefaultPostCommandConfig())
}

// captureSupervisorUUID reads the supervisor window's persistent UUID
// (window-uuid option) for the given session. It is BEST-EFFORT: any failure
// (no session, no supervisor window, GetWindowOption error) returns "" so a
// caller can gracefully degrade to a freshly generated UUID and the restart
// never breaks.
func captureSupervisorUUID(executor tmux.TmuxExecutor, sessionID string) string {
	windows, err := executor.ListWindows(sessionID)
	if err != nil {
		return ""
	}
	for _, w := range windows {
		if w.Name == "supervisor" {
			uuid, err := executor.GetWindowOption(sessionID, w.TmuxWindowID, tmux.WindowUUIDOption)
			if err != nil {
				return ""
			}
			return uuid
		}
	}
	return ""
}

// buildSessionRestartArgs builds the argv (excluding the binary itself) for the
// session-restart relaunch: always `start-attach --resume-state <path>`, plus
// `--session-uuid <uuid>` ONLY when a UUID was captured. An empty
// SupervisorUUID omits the flag so start-attach mints a fresh UUID.
func buildSessionRestartArgs(cfg selfUpdateConfig) []string {
	args := []string{"start-attach", "--resume-state", cfg.ResumeState}
	if cfg.SupervisorUUID != "" {
		args = append(args, "--session-uuid", cfg.SupervisorUUID)
	}
	// --force is unconditional on the restart self-exec so start-attach recreates
	// the (now-empty) target dir non-interactively — never blocking on the
	// [r]/[c] prompt. Keyed on the restart path itself, not on --session-uuid, so
	// interactive start/start-attach keep their prompt.
	args = append(args, "--force")
	return args
}

// recreateDirForSession resolves the directory the restart self-exec should
// recreate: the KILLED session's own TMUX_CLI_PROJECT_PATH (so the recreate
// targets the session actually being restarted), falling back to fallback
// (cfg.ProjectDir — the legacy single-session behavior) when the session's
// environment is unreadable or empty. It MUST be called BEFORE KillSession — a
// dead session has no tmux environment to read.
func recreateDirForSession(executor tmux.TmuxExecutor, sessionID, fallback string) string {
	if dir, err := executor.GetSessionEnvironment(sessionID, "TMUX_CLI_PROJECT_PATH"); err == nil && dir != "" {
		return dir
	}
	return fallback
}

// dispatchSessionRestart stops the daemon process, kills the session, and
// self-execs `start-attach --resume-state` so the relaunched session picks
// up the interrupted work. All human-readable relaunch output goes to
// stderr — stdout stays the single JSON result line.
func dispatchSessionRestart(cfg selfUpdateConfig, executor tmux.TmuxExecutor, stderr io.Writer) error {
	sessionID, err := resolveRestartSession(cfg, executor)
	if err != nil {
		return err
	}
	// Capture the supervisor window's UUID BEFORE KillSession destroys it, so the
	// relaunch can reuse it and resume the same Claude conversation. Best-effort:
	// an empty result simply omits the flag and start-attach mints a fresh UUID.
	cfg.SupervisorUUID = captureSupervisorUUID(executor, sessionID)
	// Resolve the recreate dir from the KILLED session's own environment BEFORE
	// KillSession destroys it — the restart must recreate the session actually
	// being restarted, not cfg.ProjectDir (--project/CWD), which may host a
	// DIFFERENT running session (the cross-dir hang bug).
	recreateDir := recreateDirForSession(executor, sessionID, cfg.ProjectDir)
	if err := stopDaemonProcess(cfg.ProjectDir); err != nil {
		return fmt.Errorf("stop daemon: %w", err)
	}
	if err := executor.KillSession(sessionID); err != nil {
		return fmt.Errorf("kill session %s: %w", sessionID, err)
	}
	self, err := os.Executable()
	if err != nil {
		self = "tmux-cli"
	}
	relaunch := exec.Command(self, buildSessionRestartArgs(cfg)...)
	relaunch.Dir = recreateDir
	relaunch.Stdin = os.Stdin
	relaunch.Stdout = stderr
	relaunch.Stderr = stderr
	return relaunch.Run()
}

// doSelfUpdate is the testable core: resolve source, rebuild, detect a
// binary change, and dispatch the restart per cfg.Mode via the injected
// executor.
func doSelfUpdate(cfg selfUpdateConfig, executor tmux.TmuxExecutor) (selfUpdateResult, error) {
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	result := selfUpdateResult{Restart: "none"}

	// Session mode without a resume-state file must refuse BEFORE any side
	// effect — the build never runs.
	if cfg.Mode == restartSession && cfg.ResumeState == "" {
		return result, fmt.Errorf("session restart mode requires --resume-state")
	}

	sourceDir, err := resolveSourceDir(cfg)
	if err != nil {
		return result, err
	}
	result.Source = sourceDir

	before, err := os.Stat(cfg.InstallPath)
	if err != nil && !os.IsNotExist(err) {
		return result, fmt.Errorf("stat install path %s: %w", cfg.InstallPath, err)
	}

	// Stale-executable warning is mode-independent: daemon stale-binary
	// adoption watches InstallPath, so a process running from elsewhere
	// never sees the swap.
	if exe, exeErr := os.Executable(); exeErr == nil && !samePath(exe, cfg.InstallPath) {
		fmt.Fprintf(stderr, "warning: running executable %s is not the install path %s — daemon stale-binary adoption will not fire\n", exe, cfg.InstallPath)
	}

	if err := rebuildAndInstall(cfg, sourceDir); err != nil {
		result.Stage = "build"
		return result, err
	}

	var changed bool
	if before == nil {
		// No pre-existing binary: the install is a change iff the build
		// produced one.
		_, statErr := os.Stat(cfg.InstallPath)
		changed = statErr == nil
	} else {
		changed, err = binaryChanged(cfg.InstallPath, before)
		if err != nil {
			result.Stage = "verify"
			return result, err
		}
	}
	if !changed {
		return result, nil
	}
	result.BinaryChanged = true

	mode := cfg.Mode
	if mode == restartAuto {
		mode = resolveAutoMode(cfg, executor)
	}
	switch mode {
	case restartDaemon:
		marker := filepath.Join(cfg.ProjectDir, ".tmux-cli", "taskvisor-restart")
		if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
			result.Stage = "restart"
			return result, fmt.Errorf("create .tmux-cli dir: %w", err)
		}
		if err := os.WriteFile(marker, []byte("self-update\n"), 0o644); err != nil {
			result.Stage = "restart"
			return result, fmt.Errorf("write restart marker: %w", err)
		}
		result.Restart = string(restartDaemon)
	case restartClaude:
		if err := dispatchClaudeRestart(cfg, executor); err != nil {
			result.Stage = "restart"
			return result, err
		}
		result.Restart = string(restartClaude)
	case restartSession:
		if err := dispatchSessionRestart(cfg, executor, stderr); err != nil {
			result.Stage = "restart"
			return result, err
		}
		result.Restart = string(restartSession)
	}
	return result, nil
}

var (
	selfUpdateSource      string
	selfUpdateRestart     string
	selfUpdateResumeState string
	selfUpdateProject     string
	selfUpdateBuildCmd    string
	selfUpdateSession     string
	selfUpdateDryRun      bool
)

// selfUpdateOutput is the single machine-readable JSON line self-update
// prints on stdout; everything human-readable goes to stderr.
type selfUpdateOutput struct {
	BinaryChanged bool   `json:"binary_changed"`
	Stage         string `json:"stage,omitempty"`
	Source        string `json:"source"`
	Restart       string `json:"restart"`
	DryRun        bool   `json:"dry_run,omitempty"`
}

func runSelfUpdate(cmd *cobra.Command, args []string) error {
	mode := restartMode(selfUpdateRestart)
	switch mode {
	case restartDaemon, restartClaude, restartSession, restartAuto:
	default:
		return fmt.Errorf("invalid --restart %q: must be daemon|claude|session|auto", selfUpdateRestart)
	}

	projectDir := selfUpdateProject
	if projectDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get current directory: %w", err)
		}
		projectDir = cwd
	}
	projectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return fmt.Errorf("resolve project dir: %w", err)
	}

	settingSourceDir := ""
	if settings, err := setup.LoadSettings(projectDir); err == nil {
		settingSourceDir = settings.SelfUpdate.SourceDir
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: load settings: %v\n", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}

	var buildCmd []string
	if selfUpdateBuildCmd != "" {
		buildCmd = strings.Fields(selfUpdateBuildCmd)
	}

	cfg := selfUpdateConfig{
		ProjectDir:       projectDir,
		SourceFlag:       selfUpdateSource,
		SettingSourceDir: settingSourceDir,
		ResumeState:      selfUpdateResumeState,
		SessionFlag:      selfUpdateSession,
		InstallPath:      filepath.Join(home, ".local", "bin", "tmux-cli"),
		Mode:             mode,
		Getenv:           os.Getenv,
		BuildCmd:         buildCmd,
		Stderr:           cmd.ErrOrStderr(),
	}

	enc := json.NewEncoder(cmd.OutOrStdout())

	if selfUpdateDryRun {
		source, err := resolveSourceDir(cfg)
		if err != nil {
			return err
		}
		return enc.Encode(selfUpdateOutput{
			Source:  source,
			Restart: selfUpdateRestart,
			DryRun:  true,
		})
	}

	result, err := doSelfUpdate(cfg, tmux.NewTmuxExecutor())
	if err != nil {
		return err
	}
	return enc.Encode(selfUpdateOutput{
		BinaryChanged: result.BinaryChanged,
		Stage:         result.Stage,
		Source:        result.Source,
		Restart:       result.Restart,
	})
}

var selfUpdateCmd = &cobra.Command{
	Use:   "self-update",
	Short: "Rebuild tmux-cli from source and restart the updated components",
	RunE:  runSelfUpdate,
}

func init() {
	selfUpdateCmd.Flags().StringVar(&selfUpdateSource, "source", "", "tmux-cli source directory (overrides TMUX_CLI_SRC and setting.yaml)")
	selfUpdateCmd.Flags().StringVar(&selfUpdateRestart, "restart", string(restartAuto), "What to restart after update: daemon|claude|session|auto")
	selfUpdateCmd.Flags().StringVar(&selfUpdateResumeState, "resume-state", "", "Resume-state path required for session restart mode")
	selfUpdateCmd.Flags().StringVar(&selfUpdateProject, "project", "", "Target project directory (default: current directory)")
	selfUpdateCmd.Flags().StringVar(&selfUpdateSession, "session", "", "Restart target tmux session (default: resolve from TMUX_WINDOW_UUID, else the sole session)")
	selfUpdateCmd.Flags().StringVar(&selfUpdateBuildCmd, "build-cmd", "", "Override the build command (default: 'make install')")
	selfUpdateCmd.Flags().BoolVar(&selfUpdateDryRun, "dry-run", false, "Resolve the source directory and print the plan without building")

	rootCmd.AddCommand(selfUpdateCmd)
}
