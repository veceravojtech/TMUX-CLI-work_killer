package main

import (
	"io"
	"os"

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
}

// resolveSourceDir resolves the tmux-cli source directory with precedence
// --source flag > TMUX_CLI_SRC env > setting.yaml source dir, refusing a
// source that equals the target project itself.
func resolveSourceDir(cfg selfUpdateConfig) (string, error) {
	return "", nil
}

// binaryChanged reports whether the file at path differs (size or mtime)
// from the pre-build stat captured in before.
func binaryChanged(path string, before os.FileInfo) (bool, error) {
	return false, nil
}

// rebuildAndInstall runs cfg.BuildCmd in sourceDir to rebuild and install
// the tmux-cli binary.
func rebuildAndInstall(cfg selfUpdateConfig, sourceDir string) error {
	return nil
}

// doSelfUpdate is the testable core: resolve source, rebuild, detect a
// binary change, and dispatch the restart per cfg.Mode via the injected
// executor.
func doSelfUpdate(cfg selfUpdateConfig, executor tmux.TmuxExecutor) (selfUpdateResult, error) {
	return selfUpdateResult{}, nil
}

var (
	selfUpdateSource      string
	selfUpdateRestart     string
	selfUpdateResumeState string
)

func runSelfUpdate(cmd *cobra.Command, args []string) error {
	return nil
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

	rootCmd.AddCommand(selfUpdateCmd)
}
