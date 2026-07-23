package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Spool constants — pinned by the frozen P2 contract.
const (
	// SegmentMaxBytes rotates the active segment once it reaches 1 MiB.
	SegmentMaxBytes = 1 << 20
	// SegmentMaxAge rotates the active segment once it is 5 minutes old.
	SegmentMaxAge = 5 * time.Minute
	// SpoolCapBytes is the local spool ceiling; oldest segments are evicted first.
	SpoolCapBytes = 200 << 20
	// PayloadMaxStringLen bounds any single payload string value (no free text).
	PayloadMaxStringLen = 200

	segmentTimeLayout = "20060102T150405" // <UTCstamp yyyymmddThhmmss>
)

// Options configures a Writer. Dir is the spool directory
// (.tmux-cli/logs/spool). Clock and PID are injectable for tests.
type Options struct {
	Dir      string
	Identity Identity
	Enabled  bool
	PID      int
	Clock    func() time.Time
}

// Writer is a concurrency-safe, append-only spool writer. It rotates segments at
// 1 MiB OR 5 minutes (whichever first) and evicts oldest segments once the spool
// exceeds the local cap. Emit never blocks on the network and swallows IO errors.
type Writer struct {
	mu      sync.Mutex
	dir     string
	id      Identity
	enabled bool
	pid     int
	clock   func() time.Time

	file     *os.File
	name     string
	size     int64
	openedAt time.Time
	seq      int64

	// capBytesOverride, when non-zero, replaces SpoolCapBytes (tests only).
	capBytesOverride int64
}

// NewWriter builds a Writer, filling in defaults for Clock (time.Now) and PID
// (os.Getpid). A Writer with Enabled=false makes every Emit a no-op.
func NewWriter(opts Options) *Writer {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	pid := opts.PID
	if pid == 0 {
		pid = os.Getpid()
	}
	return &Writer{
		dir:     opts.Dir,
		id:      opts.Identity,
		enabled: opts.Enabled,
		pid:     pid,
		clock:   clock,
	}
}

// Enabled reports whether this writer emits.
func (w *Writer) Enabled() bool { return w != nil && w.enabled }

// Emit appends one event to the active spool segment. It is fire-and-forget: the
// returned error is for tests/diagnostics only — production callers ignore it,
// and Emit never panics. A nil or disabled writer is a silent no-op.
func (w *Writer) Emit(event, window string, payload map[string]any) error {
	if w == nil || !w.enabled {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	now := w.clock().UTC()
	if err := w.ensureSegmentLocked(now); err != nil {
		return err
	}
	w.seq++
	ev := Event{
		Ts:          now.Format(time.RFC3339Nano),
		Event:       event,
		SessionID:   w.id.SessionID,
		Project:     w.id.Project,
		Fingerprint: w.id.Fingerprint,
		Window:      window,
		Seq:         w.seq,
		Payload:     sanitizePayload(payload),
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	n, werr := w.file.Write(line)
	w.size += int64(n)
	if werr != nil {
		return werr
	}
	w.enforceCapLocked()
	return nil
}

// currentSegmentName returns the active segment file name (for tests).
func (w *Writer) currentSegmentName() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.name
}

// ensureSegmentLocked opens a fresh segment when none is active or the active one
// hit the size/age threshold. Rotation only happens when the new segment name
// would differ (per-second granularity); a would-be sub-second rotation is
// deferred so the current segment may slightly exceed 1 MiB rather than collide
// on a name / break per-segment seq monotonicity.
func (w *Writer) ensureSegmentLocked(now time.Time) error {
	if w.file != nil {
		needRotate := w.size >= SegmentMaxBytes || now.Sub(w.openedAt) >= SegmentMaxAge
		if !needRotate {
			return nil
		}
		if w.segmentName(now) == w.name {
			return nil // defer: same-second rotation would collide
		}
		_ = w.file.Close()
		w.file = nil
	}
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return err
	}
	name := w.segmentName(now)
	f, err := os.OpenFile(filepath.Join(w.dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	w.file = f
	w.name = name
	w.openedAt = now
	w.seq = 0
	w.size = 0
	return nil
}

func (w *Writer) segmentName(now time.Time) string {
	return fmt.Sprintf("events-%s-%d.jsonl", now.UTC().Format(segmentTimeLayout), w.pid)
}

func (w *Writer) capBytes() int64 {
	if w.capBytesOverride > 0 {
		return w.capBytesOverride
	}
	return SpoolCapBytes
}

// enforceCapLocked deletes oldest segments (lexicographic name order ≈ chrono
// order, since the timestamp prefix is fixed-width) until the spool is at/under
// the cap. The active segment is never evicted. Eviction emits nothing.
func (w *Writer) enforceCapLocked() {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}
	type seg struct {
		name string
		size int64
	}
	var segs []seg
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasPrefix(n, "events-") || !strings.HasSuffix(n, ".jsonl") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		segs = append(segs, seg{n, info.Size()})
		total += info.Size()
	}
	cap := w.capBytes()
	if total <= cap {
		return
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].name < segs[j].name })
	for _, s := range segs {
		if total <= cap {
			break
		}
		if s.name == w.name {
			continue // never evict the active segment
		}
		if os.Remove(filepath.Join(w.dir, s.name)) == nil {
			total -= s.size
		}
	}
}

// Close flushes and closes the active segment.
func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

// sanitizePayload enforces the "ids/enums/numbers/short labels only" boundary:
// string values longer than PayloadMaxStringLen are truncated (never command
// output/file contents beyond the cap). A nil payload becomes an empty object so
// the record always serializes "payload":{}, never null.
func sanitizePayload(p map[string]any) map[string]any {
	out := make(map[string]any, len(p))
	for k, v := range p {
		out[k] = sanitizeValue(v)
	}
	return out
}

func sanitizeValue(v any) any {
	switch t := v.(type) {
	case string:
		if len(t) > PayloadMaxStringLen {
			return strings.ToValidUTF8(t[:PayloadMaxStringLen], "")
		}
		return t
	case map[string]any:
		return sanitizePayload(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = sanitizeValue(e)
		}
		return out
	default:
		return v
	}
}
