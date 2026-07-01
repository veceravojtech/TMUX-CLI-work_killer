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
	if samePath(dir, cfg.ProjectDir) {
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
		if sessions, err := executor.ListSessions(); err == nil && len(sessions) == 1 {
			return restartClaude
		}
	}
	return ""
}

// singleSessionID resolves the one running tmux session, refusing when zero
// or several exist — no guessing across sessions.
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

// dispatchClaudeRestart interrupts the supervisor window's Claude process
// (window preserved, so window options survive) and relaunches Claude via
// the standard fallback chain, which includes `claude --resume`.
func dispatchClaudeRestart(executor tmux.TmuxExecutor) error {
	sessionID, err := singleSessionID(executor)
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
	if err := executor.InterruptWindow(windowID); err != nil {
		return fmt.Errorf("interrupt supervisor window: %w", err)
	}
	return session.ExecutePostCommandWithFallback(executor, sessionID, "supervisor", session.DefaultPostCommandConfig())
}

// dispatchSessionRestart stops the daemon process, kills the session, and
// self-execs `start-attach --resume-state` so the relaunched session picks
// up the interrupted work. All human-readable relaunch output goes to
// stderr — stdout stays the single JSON result line.
func dispatchSessionRestart(cfg selfUpdateConfig, executor tmux.TmuxExecutor, stderr io.Writer) error {
	sessionID, err := singleSessionID(executor)
	if err != nil {
		return err
	}
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
	relaunch := exec.Command(self, "start-attach", "--resume-state", cfg.ResumeState)
	relaunch.Dir = cfg.ProjectDir
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
		if err := dispatchClaudeRestart(executor); err != nil {
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
	selfUpdateCmd.Flags().StringVar(&selfUpdateBuildCmd, "build-cmd", "", "Override the build command (default: 'make install')")
	selfUpdateCmd.Flags().BoolVar(&selfUpdateDryRun, "dry-run", false, "Resolve the source directory and print the plan without building")

	rootCmd.AddCommand(selfUpdateCmd)
}
