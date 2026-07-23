package main

import (
	"io"

	"github.com/console/tmux-cli/internal/transcript"
	"github.com/spf13/cobra"
)

var (
	logsCaptureDir     string
	logsCaptureSession string
	logsCaptureWindow  string
)

// logsCaptureCmd is the pipe-pane sink for P3 transcript capture: tmux runs it
// as `... | tmux-cli logs capture --dir D --session S --window W` (wired by
// transcript.CapturePipeCommand), feeding raw pane bytes on stdin. It
// ANSI-strips line-by-line and appends NDJSON segments under
// .tmux-cli/logs/transcripts/<window>/. Hidden: never invoked by humans.
var logsCaptureCmd = &cobra.Command{
	Use:    "capture",
	Short:  "Consume a pipe-pane stream into transcript segments (internal)",
	Hidden: true,
	// Run (not RunE): this process must never exit non-zero or early — when it
	// sits behind a tee, dying kills the tee'd pane log via SIGPIPE.
	Run: func(cmd *cobra.Command, args []string) {
		runLogsCapture(cmd.InOrStdin())
	},
}

func init() {
	logsCaptureCmd.Flags().StringVar(&logsCaptureDir, "dir", "", "project directory (transcripts land under its .tmux-cli/logs/transcripts)")
	logsCaptureCmd.Flags().StringVar(&logsCaptureSession, "session", "", "tmux session id recorded in each segment")
	logsCaptureCmd.Flags().StringVar(&logsCaptureWindow, "window", "", "window name this capture serves")
	logsCmd.AddCommand(logsCaptureCmd)
}

// runLogsCapture drains stdin into the transcript writer. Any degraded state —
// missing flags, or the privacy gate having flipped off since the pipe was
// wired (defense in depth: NO segment may land on disk when unarmed) — still
// DRAINS stdin to EOF instead of exiting, keeping an upstream tee alive.
func runLogsCapture(in io.Reader) {
	if logsCaptureDir == "" || logsCaptureWindow == "" || !transcript.Armed(logsCaptureDir) {
		_, _ = io.Copy(io.Discard, in)
		return
	}
	w := transcript.NewWriter(transcript.Options{
		Root:      transcript.Root(logsCaptureDir),
		SessionID: logsCaptureSession,
		Window:    logsCaptureWindow,
	})
	defer w.Close()
	transcript.CaptureStream(in, w)
}
