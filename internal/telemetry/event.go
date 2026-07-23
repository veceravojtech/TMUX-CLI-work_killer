// Package telemetry is the CLI-side producer for the P2 structured-events
// pipeline (docs/architecture/session-log-streaming-design.md §4/§8). It defines
// the frozen JSONL event schema, a local append-only spool writer (segment
// rotation + bounded cap), and a fire-and-forget emit surface. It is the WRITER
// side of the byte-exact contract in
// .tmux-cli/research/2026-07-22-16/p2-events-contract.md; the `tmux-cli logs
// ship` shipper (a separate lane owner) is the reader/cursor side.
//
// Every emit is best-effort: it swallows IO errors internally and NEVER fails
// the caller. Gating is via telemetry.enabled (default true).
package telemetry

// Event is one JSONL record. The struct field order is SIGNIFICANT: encoding/json
// marshals in declaration order, and the frozen contract pins the key order as
// {ts, event, session_id, project, fingerprint, window, seq, payload}. Do not
// reorder these fields.
type Event struct {
	Ts          string         `json:"ts"`          // RFC3339Nano UTC
	Event       string         `json:"event"`       // closed event-type set (see contract)
	SessionID   string         `json:"session_id"`  // tmux session name
	Project     string         `json:"project"`     // lane name
	Fingerprint string         `json:"fingerprint"` // 64-hex machine identity
	Window      string         `json:"window"`      // window name or ""
	Seq         int64          `json:"seq"`         // monotonic per segment, starts at 1
	Payload     map[string]any `json:"payload"`     // ids/enums/numbers/short labels ONLY
}

// Event-type constants for the emitters owned by this worker. The set is the
// closed initial catalogue from the frozen contract; unknown types are still
// accepted by the ingest side (forward compat) but Go emitters use these.
const (
	EventSessionStart = "session.start"
	EventSessionEnd   = "session.end"
	EventWindowSpawn  = "window.spawn"
	EventWindowKill   = "window.kill"
	EventTaskStatus   = "task.status"
	EventGoalStatus   = "goal.status"
	EventGoalPhase    = "goal.phase"
	EventWorkerReport = "worker.report"
)
