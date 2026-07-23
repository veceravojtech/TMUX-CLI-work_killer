package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/console/tmux-cli/internal/auth"
	"github.com/console/tmux-cli/internal/identity"
	"github.com/console/tmux-cli/internal/producer"
	"github.com/console/tmux-cli/internal/redact"
	"github.com/console/tmux-cli/internal/setup"
	"github.com/console/tmux-cli/internal/shipper"
	"github.com/spf13/cobra"
)

// logsCmd groups the session-log telemetry subcommands.
var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Session-log telemetry pipeline (P2)",
}

// logsShipCmd is the detached shipper: it drains the spool to the backend ingest
// on a poll loop. It is launched by `tmux-cli start` when telemetry gating passes
// (see maybeStartShipper); run manually it behaves identically. A single-instance
// flock guarantees at most one shipper per project drains the cursor, so a repeat
// `start` never races two shippers on the same spool.
var logsShipCmd = &cobra.Command{
	Use:   "ship [PATH]",
	Short: "Ship spooled session-log events to the backend (detached loop)",
	RunE:  runLogsShip,
}

var logsShipSession string

func init() {
	logsShipCmd.Flags().StringVar(&logsShipSession, "session", "", "tmux session id this shipper serves (used for the manifest + events path)")
	logsCmd.AddCommand(logsShipCmd)
	rootCmd.AddCommand(logsCmd)
}

func runLogsShip(cmd *cobra.Command, args []string) error {
	projectPath, err := resolveProjectPath(args)
	if err != nil {
		return err
	}

	// Single-instance guard: acquire an exclusive, non-blocking lock. If another
	// shipper already owns it, exit 0 — never run two shippers on one cursor.
	lockFile, ok, err := acquireShipperLock(projectPath)
	if err != nil {
		return err
	}
	if !ok {
		return nil // another shipper is live
	}
	defer lockFile.Close()

	apiURL := auth.LoadAPIURL(projectPath)
	store, err := auth.NewStore()
	if err != nil {
		return err
	}
	client := shipper.NewClient(apiURL, identity.Fingerprint(), store)
	s := shipper.New(projectPath, logsShipSession, client)

	// Signal-aware context so a SIGTERM/SIGINT (or the tmux session teardown)
	// stops the loop cleanly instead of leaving an orphan.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Session-manifest registration (idempotent upsert). Best-effort: a failure
	// here (offline, transient) must not stop the ship loop — the backend
	// auto-registers a stub on the first events batch anyway (contract §Ingest).
	if logsShipSession != "" {
		if merr := client.RegisterManifest(ctx, buildManifest(projectPath, logsShipSession)); merr != nil {
			fmt.Fprintf(os.Stderr, "shipper: manifest register failed (continuing): %v\n", merr)
		}
	}

	// P3 transcript ship loop (additive to the events loop above). It shares
	// the process, context, and auth-wired client but drains its own tree
	// (.tmux-cli/logs/transcripts/) with its own per-window cursors. The armed
	// gate is re-evaluated every pass, so it is a silent no-op until the project
	// opts in (telemetry.transcripts: true) and stops shipping the moment the
	// user opts back out — launching it unconditionally here keeps the single
	// flock-guarded shipper process the sole drainer of both paths.
	tShipper := shipper.NewTranscriptShipper(projectPath, logsShipSession, client,
		redact.Hook{Path: filepath.Join(projectPath, ".tmux-cli", "hooks", "redact-transcript.sh")},
		func() bool { return shipper.TranscriptsArmed(projectPath, store) })
	go tShipper.Run(ctx, shipper.DefaultRunOptions())

	return s.Run(ctx, shipper.DefaultRunOptions())
}

// buildManifest assembles the session manifest from the local host/config.
func buildManifest(projectPath, sessionID string) shipper.Manifest {
	host, _ := os.Hostname()
	project := ""
	if cfg, cerr := producer.LoadConfig(projectPath); cerr == nil {
		project = cfg.Project
	}
	if project == "" {
		if abs, aerr := filepath.Abs(projectPath); aerr == nil {
			project = filepath.Base(abs)
		}
	}
	return shipper.Manifest{
		SessionID:     sessionID,
		Project:       project,
		Fingerprint:   identity.Fingerprint(),
		Hostname:      host,
		StartedAt:     time.Now().UTC().Format(time.RFC3339),
		BinaryVersion: version,
	}
}

// acquireShipperLock takes an exclusive non-blocking flock on
// .tmux-cli/logs/shipper.lock. The returned file must stay open for the lock to
// persist (the OS releases it when the fd closes / process dies). ok=false means
// another shipper already holds it.
func acquireShipperLock(projectPath string) (*os.File, bool, error) {
	dir := filepath.Join(projectPath, ".tmux-cli", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "shipper.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, false, nil
		}
		return nil, false, err
	}
	return f, true, nil
}

// telemetryGate decides shipping eligibility for the start path (contract
// §Gating). Shipping needs BOTH telemetry.enabled AND a logged-in auth store.
// It returns whether to launch the shipper and, when spool-only, the ONE notice
// line `tmux-cli start` prints. A disabled-entirely telemetry block ships
// nothing and prints nothing (emit is off too — worker 1's emitters honor the
// same flag).
func telemetryGate(projectPath string) (ship bool, notice string) {
	settings, err := setup.LoadSettings(projectPath)
	if err != nil || !settings.Telemetry.IsEnabled() {
		return false, ""
	}
	store, err := auth.NewStore()
	if err != nil || !shipper.LoggedIn(store) {
		return false, "telemetry: spooling session logs locally only — run `tmux-cli login` to ship them"
	}
	return true, ""
}

// maybeStartShipper is the `tmux-cli start` wiring: it evaluates the telemetry
// gate, prints the spool-only notice when shipping is gated off, and otherwise
// launches the detached `tmux-cli logs ship` process. Both are best-effort —
// telemetry must never fail or block session start.
func maybeStartShipper(projectPath, sessionID string, out io.Writer) {
	ship, notice := telemetryGate(projectPath)
	if notice != "" {
		fmt.Fprintln(out, notice)
	}
	if !ship {
		return
	}
	launchDetachedShipper(projectPath, sessionID)
}

// launchDetachedShipper spawns `tmux-cli logs ship <project> --session <id>` in
// its own process group, detached from the start command, with output routed to
// .tmux-cli/logs/shipper.log. Any failure to launch is swallowed (best-effort).
func launchDetachedShipper(projectPath, sessionID string) {
	self, err := os.Executable()
	if err != nil {
		self = "tmux-cli"
	}
	logDir := filepath.Join(projectPath, ".tmux-cli", "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logFile, _ := os.OpenFile(filepath.Join(logDir, "shipper.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)

	c := exec.Command(self, "logs", "ship", projectPath, "--session", sessionID)
	if logFile != nil {
		c.Stdout = logFile
		c.Stderr = logFile
	}
	// Detach into a new session/process group so it survives the start command.
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return
	}
	// Release the child; the parent does not wait.
	_ = c.Process.Release()
}
