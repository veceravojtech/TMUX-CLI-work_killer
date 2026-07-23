// Transcript ship path (P3 — frozen contract
// .tmux-cli/research/2026-07-23-00/p3-transcripts-contract.md §Ship). ADDITIVE
// to the P2 events path in shipper.go/spool.go/client.go, which it reuses but
// never alters: transcripts have their OWN tree (.tmux-cli/logs/transcripts/,
// one subdirectory per window), their OWN per-window cursor.json (same atomic
// temp+rename Cursor as events, different directories), and their OWN ingest
// endpoint. Every line is redacted (internal/redact, built-in masker + optional
// fail-closed user hook) BEFORE any byte leaves the machine, and the whole path
// runs only while armed (telemetry.enabled AND telemetry.transcripts AND a
// logged-in auth store).
package shipper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/console/tmux-cli/internal/auth"
	"github.com/console/tmux-cli/internal/redact"
	"gopkg.in/yaml.v3"
)

const (
	transcriptsSubdir = "transcripts"
	// transcriptSegPrefix / transcriptSegSuffix bound the capture writer's
	// append-only segment names: seg-<UTCstamp yyyymmddThhmmss>-<pid>.ndjson
	// (contract §Capture). Fixed-width stamp → lexical order is chronological.
	transcriptSegPrefix = "seg-"
	transcriptSegSuffix = ".ndjson"
)

// TranscriptsDir returns the transcript capture tree
// (.tmux-cli/logs/transcripts) under projectRoot — separate from the events
// spool (contract: "SEPARATE spool tree from P2 events").
func TranscriptsDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "logs", transcriptsSubdir)
}

// ListTranscriptSegments returns one window directory's segment file names
// sorted ascending (chronological). A missing directory yields an empty slice
// with no error.
func ListTranscriptSegments(windowDir string) ([]string, error) {
	entries, err := os.ReadDir(windowDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, transcriptSegPrefix) && strings.HasSuffix(n, transcriptSegSuffix) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

// transcriptLine is the contract-pinned segment object (contract §Capture
// "Segment format"). Field order mirrors the contract so re-marshaled
// (redacted) lines keep the documented shape.
type transcriptLine struct {
	Ts        string `json:"ts"`
	SessionID string `json:"session_id"`
	Window    string `json:"window"`
	Kind      string `json:"kind"`
	Seq       int64  `json:"seq"`
	Text      string `json:"text"`
}

// TranscriptShipper drains the transcript tree for one session: per window
// directory it redacts every complete line and POSTs gzip NDJSON batches to the
// transcript ingest, advancing a per-window cursor only after the batch is
// acked. Fail-closed: any redaction-hook failure holds that segment local and
// ships nothing from it.
type TranscriptShipper struct {
	rootDir   string
	sessionID string
	client    *Client
	hook      redact.Hook
	// armed gates every pass (nil = always armed, for tests). Evaluated per
	// ShipOnce so a mid-session opt-out stops shipping on the next pass.
	armed func() bool
}

// NewTranscriptShipper builds a TranscriptShipper over projectRoot's transcript
// tree. hook is the optional Layer-2 user hook (zero value = no hook path —
// Layer 1 alone). armed may be nil (always armed).
func NewTranscriptShipper(projectRoot, sessionID string, client *Client, hook redact.Hook, armed func() bool) *TranscriptShipper {
	return &TranscriptShipper{
		rootDir:   TranscriptsDir(projectRoot),
		sessionID: sessionID,
		client:    client,
		hook:      hook,
		armed:     armed,
	}
}

// ShipOnce runs one pass over every window directory. A failure in one window
// (e.g. its segment is held by a failing redaction hook) is collected and does
// not block the other windows; the joined error surfaces so the caller backs
// off and retries the held work on a later pass. Not-armed is a silent no-op.
func (t *TranscriptShipper) ShipOnce(ctx context.Context) error {
	if t.armed != nil && !t.armed() {
		return nil
	}
	entries, err := os.ReadDir(t.rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var errs []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := t.shipWindow(ctx, e.Name()); err != nil {
			errs = append(errs, fmt.Errorf("transcript window %s: %w", e.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// shipWindow drains one window directory, mirroring the events segment walk:
// segments strictly older than the cursor are reaped, the cursor's segment
// resumes from its offset, a rotated (non-newest) segment is deleted once fully
// shipped, and the open (newest) segment is left in place with the cursor
// marking how far it is drained.
func (t *TranscriptShipper) shipWindow(ctx context.Context, window string) error {
	dir := filepath.Join(t.rootDir, window)
	segs, err := ListTranscriptSegments(dir)
	if err != nil {
		return err
	}
	if len(segs) == 0 {
		return nil
	}
	cur, err := LoadCursor(dir)
	if err != nil {
		return err
	}
	for i, seg := range segs {
		if cur.Segment != "" && seg < cur.Segment {
			_ = os.Remove(filepath.Join(dir, seg))
			continue
		}
		start := int64(0)
		if seg == cur.Segment {
			start = cur.Offset
		}
		end, err := t.shipSegment(ctx, dir, window, seg, start)
		if err != nil {
			return err
		}
		if i == len(segs)-1 {
			cur = Cursor{Segment: seg, Offset: end}
			continue
		}
		if err := os.Remove(filepath.Join(dir, seg)); err != nil && !os.IsNotExist(err) {
			return err
		}
		next := segs[i+1]
		if err := SaveCursor(dir, Cursor{Segment: next, Offset: 0}); err != nil {
			return err
		}
		cur = Cursor{Segment: next, Offset: 0}
	}
	return nil
}

// shipSegment redacts and ships the unshipped tail of one segment. BOTH
// redaction layers complete over the full tail BEFORE the first byte is POSTed
// (contract: "Ship only when: armed AND redaction ran successfully"), so the
// cursor is saved once, after every batch of the tail is acked — a mid-tail
// crash re-ships the whole tail, which the backend's X-Batch-Id/at-least-once
// semantics absorb. A torn trailing line is held back by scanSegment exactly as
// in the events path.
func (t *TranscriptShipper) shipSegment(ctx context.Context, dir, window, seg string, start int64) (int64, error) {
	data, err := os.ReadFile(filepath.Join(dir, seg))
	if err != nil {
		if os.IsNotExist(err) {
			return start, nil // raced with rotation/eviction — treat as drained
		}
		return 0, err
	}
	if start > int64(len(data)) {
		return int64(len(data)), nil // truncated behind the cursor — nothing to ship
	}
	lines, consumed := scanSegment(data[start:])
	end := start + consumed

	// Layer 1 — built-in masker over each line's text (always on).
	var masked [][]byte
	for _, ln := range lines {
		if !ln.ship {
			continue // complete-but-non-JSON line: consumed, never shipped
		}
		var tl transcriptLine
		if err := json.Unmarshal(ln.json, &tl); err != nil {
			continue // valid JSON of the wrong shape: consumed, never shipped
		}
		tl.Text = redact.Mask(tl.Text)
		out, merr := json.Marshal(tl)
		if merr != nil {
			return 0, merr
		}
		masked = append(masked, out)
	}

	// Layer 2 — optional user hook, per segment tail, over the masked NDJSON.
	// FAIL-CLOSED: any hook error (spawn/non-zero/timeout) or non-NDJSON output
	// holds the segment local — nothing ships, the cursor stays put, and the
	// pass retries after backoff (self-healing once the hook is fixed).
	shipLines := masked
	if len(masked) > 0 && t.hook.Runnable() {
		var sb strings.Builder
		for _, ln := range masked {
			sb.Write(ln)
			sb.WriteByte('\n')
		}
		out, herr := t.hook.Run(ctx, sb.String())
		if herr != nil {
			fmt.Fprintf(os.Stderr, "transcript shipper: redaction hook failed — segment %s held local: %v\n", seg, herr)
			return 0, fmt.Errorf("redaction hook failed — segment %s held local: %w", seg, herr)
		}
		shipLines = nil
		for _, raw := range strings.Split(out, "\n") {
			raw = strings.TrimRight(raw, "\r")
			if raw == "" {
				continue // the hook may drop lines (redaction by omission)
			}
			if !json.Valid([]byte(raw)) {
				fmt.Fprintf(os.Stderr, "transcript shipper: redaction hook produced non-NDJSON output — segment %s held local\n", seg)
				return 0, fmt.Errorf("redaction hook produced non-NDJSON output — segment %s held local", seg)
			}
			shipLines = append(shipLines, []byte(raw))
		}
	}

	// Batch + POST under the raw cap (≤4 MiB raw guarantees ≤5 MiB compressed).
	var pending [][]byte
	pendingRaw := 0
	post := func() error {
		if len(pending) == 0 {
			return nil
		}
		gz, gerr := gzipLines(pending)
		if gerr != nil {
			return gerr
		}
		if _, perr := t.client.PostTranscripts(ctx, t.sessionID, window, gz); perr != nil {
			return perr
		}
		pending = nil
		pendingRaw = 0
		return nil
	}
	for _, ln := range shipLines {
		if pendingRaw > 0 && pendingRaw+len(ln)+1 > softRawBatchCap {
			if err := post(); err != nil {
				return 0, err
			}
		}
		pending = append(pending, ln)
		pendingRaw += len(ln) + 1
	}
	if err := post(); err != nil {
		return 0, err
	}
	if end != start {
		if err := SaveCursor(dir, Cursor{Segment: seg, Offset: end}); err != nil {
			return 0, err
		}
	}
	return end, nil
}

// Run drives ShipOnce on the poll loop with the same cadence/backoff semantics
// as the events Shipper.Run (shared RunOptions and backoff). It returns only
// when ctx is cancelled.
func (t *TranscriptShipper) Run(ctx context.Context, opts RunOptions) error {
	b := backoff{min: opts.MinBackoff, max: opts.MaxBackoff}
	for {
		err := t.ShipOnce(ctx)
		var delay time.Duration
		switch {
		case err == nil:
			b.reset()
			delay = opts.PollInterval
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return err
		case errors.Is(err, ErrLoginRequired):
			fmt.Fprintln(os.Stderr, "transcript shipper: paused — run: tmux-cli login")
			delay = opts.ReauthBackoff
		default:
			delay = b.next()
			fmt.Fprintf(os.Stderr, "transcript shipper: transient error, retrying in %s: %v\n", delay, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// PostTranscripts ships one gzip NDJSON transcript batch to the contract
// endpoint POST /api/v1/telemetry/sessions/{session_id}/transcripts with
// headers Content-Encoding: gzip, Content-Type: application/x-ndjson,
// X-Batch-Id (minted once, REUSED across the 401-refresh retry so a replay is
// deduped) and X-Window. Response/auth semantics are identical to PostEvents.
func (c *Client) PostTranscripts(ctx context.Context, sessionID, window string, gzBody []byte) (EventsAck, error) {
	batchID := c.newBatchID()
	url := c.baseURL + "/api/v1/telemetry/sessions/" + sessionID + "/transcripts"
	resp, err := c.doWithRefresh(ctx, func(token string) (*http.Request, error) {
		req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(gzBody))
		if rerr != nil {
			return nil, rerr
		}
		req.Header.Set("Content-Encoding", "gzip")
		req.Header.Set("Content-Type", "application/x-ndjson")
		req.Header.Set("X-Batch-Id", batchID)
		req.Header.Set("X-Window", window)
		return req, nil
	})
	if err != nil {
		return EventsAck{}, err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return EventsAck{}, fmt.Errorf("shipper: transcripts post returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return EventsAck{}, err
	}
	var ack EventsAck
	if err := json.Unmarshal(body, &ack); err != nil {
		return EventsAck{}, err
	}
	return ack, nil
}

// transcriptGateProbe decodes ONLY the telemetry keys straight from
// setting.yaml. Deliberately not setup.LoadSettings: (a) LoadSettings
// force-corrects and RE-SAVES the file on every call — unacceptable side
// effects for a probe evaluated every ship pass from a detached process; and
// (b) the `transcripts:` field of setup.TelemetrySettings is owned by the
// concurrent capture change set, so reading raw YAML keeps this path free of a
// compile-time coupling. Semantics mirror LoadSettings: enabled nil→true
// (backfill idiom), transcripts nil→false (contract: OPT-IN, default FALSE).
type transcriptGateProbe struct {
	Telemetry struct {
		Enabled     *bool `yaml:"enabled"`
		Transcripts *bool `yaml:"transcripts"`
	} `yaml:"telemetry"`
}

// TranscriptsArmed reports whether the transcript path may ship (contract
// §Opt-in: telemetry.enabled AND telemetry.transcripts AND a logged-in auth
// store). Any read/parse failure, a missing setting.yaml, or a logged-out
// store disarms (fail-closed toward privacy).
func TranscriptsArmed(projectRoot string, store *auth.Store) bool {
	data, err := os.ReadFile(filepath.Join(projectRoot, ".tmux-cli", "setting.yaml"))
	if err != nil {
		return false
	}
	var p transcriptGateProbe
	if err := yaml.Unmarshal(data, &p); err != nil {
		return false
	}
	enabled := p.Telemetry.Enabled == nil || *p.Telemetry.Enabled
	optIn := p.Telemetry.Transcripts != nil && *p.Telemetry.Transcripts
	return enabled && optIn && LoggedIn(store)
}
