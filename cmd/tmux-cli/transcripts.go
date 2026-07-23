package main

import (
	"path/filepath"

	"github.com/console/tmux-cli/internal/tmux"
	"github.com/console/tmux-cli/internal/transcript"
)

// transcriptPaneLogPath returns the existing pane-log destination for a
// managed window, or "" for windows that have none. Worker windows are piped
// to .tmux-cli/logs/panes/<name>.log at spawn (windows-spawn-worker / the
// daemon's WindowCreateFunc); supervisor and taskvisor are not pane-logged.
func transcriptPaneLogPath(projectPath, window string) string {
	if transcript.Kind(window) != transcript.KindWorker {
		return ""
	}
	return filepath.Join(projectPath, ".tmux-cli", "logs", "panes", window+".log")
}

// maybeArmTranscripts wires P3 transcript capture pipes on `tmux-cli start` /
// `start-attach` for every managed window already in the session (contract:
// "at creation AND for existing windows on start" — supervisor and taskvisor
// are created by start itself moments earlier, so this one sweep covers both
// halves for them; later worker windows are armed at their own creation
// sites). It is a no-op unless the privacy gate passes (telemetry.enabled AND
// telemetry.transcripts AND logged-in): when unarmed, NO pipe is wired here
// and nothing lands on disk. Best-effort — capture must never fail or block
// session start.
func maybeArmTranscripts(projectPath, sessionID string, executor tmux.TmuxExecutor) {
	if !transcript.Armed(projectPath) {
		return
	}
	windows, err := executor.ListWindows(sessionID)
	if err != nil {
		return
	}
	for _, w := range windows {
		if !transcript.IsManaged(w.Name) {
			continue
		}
		cmd := transcript.CapturePipeCommand(projectPath, sessionID, w.Name, transcriptPaneLogPath(projectPath, w.Name))
		if cmd == "" {
			continue
		}
		// A worker window may already hold a plain pane-log pipe (one pipe per
		// pane): close it first so the tee'd capture command takes over the same
		// pane-log destination without losing either stream.
		_ = executor.ClosePipePane(sessionID, w.TmuxWindowID)
		_ = executor.PipePaneCommand(sessionID, w.TmuxWindowID, cmd)
	}
}
