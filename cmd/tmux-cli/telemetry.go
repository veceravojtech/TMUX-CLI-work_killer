package main

import (
	"encoding/json"
	"os"

	"github.com/console/tmux-cli/internal/telemetry"
	"github.com/spf13/cobra"
)

var (
	telemetryEmitEvent       string
	telemetryEmitWindow      string
	telemetryEmitPayloadJSON string
)

var telemetryCmd = &cobra.Command{
	Use:   "telemetry",
	Short: "Structured session telemetry (P2 events)",
	Long: `Structured flow-event telemetry for the session-streaming pipeline.
Events spool locally under .tmux-cli/logs/spool/ and are shipped by
` + "`tmux-cli logs ship`" + `. Emit is fire-and-forget and gated on telemetry.enabled.`,
}

var telemetryEmitCmd = &cobra.Command{
	Use:   "emit",
	Short: "Emit one structured telemetry event (fire-and-forget; always exits 0)",
	Long: `Append one structured event to the local spool. This is the emit path for
hook scripts (Go code uses the in-process writer). It is fire-and-forget:
it ALWAYS exits 0 — even when telemetry is disabled, the payload is
malformed, or the spool is unwritable — so hooks never fail on telemetry.`,
	// Run (not RunE) so a nil/degraded outcome still yields exit 0.
	Run: func(cmd *cobra.Command, args []string) {
		runTelemetryEmit()
	},
}

func init() {
	telemetryEmitCmd.Flags().StringVar(&telemetryEmitEvent, "event", "", "event type (e.g. hook.fired, supervisor.cycle)")
	telemetryEmitCmd.Flags().StringVar(&telemetryEmitWindow, "window", "", "window name the event belongs to (optional)")
	telemetryEmitCmd.Flags().StringVar(&telemetryEmitPayloadJSON, "payload-json", "", "payload as a JSON object of ids/enums/numbers/short labels")
	telemetryCmd.AddCommand(telemetryEmitCmd)
	rootCmd.AddCommand(telemetryCmd)
}

// runTelemetryEmit performs a best-effort emit. Every failure mode is swallowed:
// no event, bad JSON, disabled telemetry, unwritable spool — all exit 0.
func runTelemetryEmit() {
	if telemetryEmitEvent == "" {
		return // nothing to emit; still exit 0
	}
	var payload map[string]any
	if telemetryEmitPayloadJSON != "" {
		// Ignore parse errors: a malformed payload must not fail the hook. A failed
		// unmarshal leaves payload nil → serialized as an empty object.
		_ = json.Unmarshal([]byte(telemetryEmitPayloadJSON), &payload)
	}
	dir, err := os.Getwd()
	if err != nil || dir == "" {
		dir = "."
	}
	w := telemetry.NewProjectWriter(dir)
	telemetry.EmitTo(w, telemetryEmitEvent, telemetryEmitWindow, payload)
}

// emitSessionStart records a session.start event with the freshly-minted session
// id and the {hostname, binary_version} payload from the frozen contract. Uses a
// project-scoped writer (identity resolved from the environment, session id
// overridden with the authoritative value). Fire-and-forget.
func emitSessionStart(sessionID, projectPath string) {
	id := telemetry.ResolveIdentity(projectPath)
	id.SessionID = sessionID
	w := telemetry.NewWriter(telemetry.Options{
		Dir:      telemetry.SpoolDir(projectPath),
		Identity: id,
		Enabled:  telemetry.Enabled(projectPath),
	})
	host, _ := os.Hostname()
	telemetry.EmitTo(w, telemetry.EventSessionStart, "supervisor", map[string]any{
		"hostname":       host,
		"binary_version": version,
	})
}

// emitSessionEnd records a session.end event ({} payload) at session teardown.
// The spool dir is resolved from the current working directory (best-effort — at
// kill time only the session id is authoritative). Fire-and-forget.
func emitSessionEnd(sessionID string) {
	dir, err := os.Getwd()
	if err != nil || dir == "" {
		dir = "."
	}
	id := telemetry.ResolveIdentity(dir)
	id.SessionID = sessionID
	w := telemetry.NewWriter(telemetry.Options{
		Dir:      telemetry.SpoolDir(dir),
		Identity: id,
		Enabled:  telemetry.Enabled(dir),
	})
	telemetry.EmitTo(w, telemetry.EventSessionEnd, "", nil)
}
