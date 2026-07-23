package transcript

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

// Spool constants — pinned by the frozen P3 transcripts contract, mirroring
// the P2 events writer (internal/telemetry/writer.go).
const (
	// SegmentMaxBytes rotates the active segment once it reaches 1 MiB.
	SegmentMaxBytes = 1 << 20
	// SegmentMaxAge rotates the active segment once it is 5 minutes old.
	SegmentMaxAge = 5 * time.Minute
	// TreeCapBytes is the local ceiling across the WHOLE transcripts tree
	// (all window subdirs); oldest segments are evicted first.
	TreeCapBytes = 200 << 20

	segmentTimeLayout = "20060102T150405" // <UTCstamp yyyymmddThhmmss>
)

// Segment is one captured chunk — the contract's NDJSON object. Field order is
// the contract's: ts, session_id, window, kind, seq, text. Text is the
// ANSI-stripped RAW pre-redaction chunk; redaction happens at ship time.
type Segment struct {
	Ts        string `json:"ts"`
	SessionID string `json:"session_id"`
	Window    string `json:"window"`
	Kind      string `json:"kind"`
	Seq       int64  `json:"seq"`
	Text      string `json:"text"`
}

// Options configures a Writer. Root is the transcripts tree
// (.tmux-cli/logs/transcripts); segments land in Root/<window>/. Clock and PID
// are injectable for tests.
type Options struct {
	Root      string
	SessionID string
	Window    string
	PID       int
	Clock     func() time.Time
}

// Writer is a concurrency-safe, append-only transcript segment writer for ONE
// window. It rotates segments at 1 MiB OR 5 minutes (whichever first) and
// evicts oldest segments across the whole transcripts tree once it exceeds the
// local cap. Append never blocks on the network and swallows IO errors
// (returned for tests/diagnostics only).
type Writer struct {
	mu        sync.Mutex
	root      string
	dir       string
	sessionID string
	window    string
	kind      string
	pid       int
	clock     func() time.Time

	file     *os.File
	name     string
	size     int64
	openedAt time.Time
	seq      int64

	// capBytesOverride, when non-zero, replaces TreeCapBytes (tests only).
	capBytesOverride int64
}

// NewWriter builds a Writer for one window, filling in defaults for Clock
// (time.Now) and PID (os.Getpid). Kind is derived from the window name.
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
		root:      opts.Root,
		dir:       filepath.Join(opts.Root, opts.Window),
		sessionID: opts.SessionID,
		window:    opts.Window,
		kind:      Kind(opts.Window),
		pid:       pid,
		clock:     clock,
	}
}

// Append writes one ANSI-stripped chunk to the active segment. Seq is
// monotonic per segment file starting at 1. Fire-and-forget: the returned
// error is for tests/diagnostics only, and Append never panics.
func (w *Writer) Append(text string) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	now := w.clock().UTC()
	if err := w.ensureSegmentLocked(now); err != nil {
		return err
	}
	w.seq++
	seg := Segment{
		Ts:        now.Format(time.RFC3339Nano),
		SessionID: w.sessionID,
		Window:    w.window,
		Kind:      w.kind,
		Seq:       w.seq,
		Text:      strings.ToValidUTF8(text, ""),
	}
	line, err := json.Marshal(seg)
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

// ensureSegmentLocked opens a fresh segment when none is active or the active
// one hit the size/age threshold. Rotation only happens when the new segment
// name would differ (per-second granularity); a would-be sub-second rotation
// is deferred so the current segment may slightly exceed 1 MiB rather than
// collide on a name / break per-segment seq monotonicity.
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
	return fmt.Sprintf("seg-%s-%d.ndjson", now.UTC().Format(segmentTimeLayout), w.pid)
}

func (w *Writer) capBytes() int64 {
	if w.capBytesOverride > 0 {
		return w.capBytesOverride
	}
	return TreeCapBytes
}

// enforceCapLocked deletes oldest segments across the WHOLE transcripts tree
// (every window subdir) until it is at/under the cap. Oldest is by segment
// base name (the fixed-width timestamp prefix makes lexicographic ≈ chrono
// order across windows). The active segment is never evicted. Eviction emits
// nothing.
func (w *Writer) enforceCapLocked() {
	windows, err := os.ReadDir(w.root)
	if err != nil {
		return
	}
	type seg struct {
		path string
		base string
		size int64
	}
	var segs []seg
	var total int64
	for _, d := range windows {
		if !d.IsDir() {
			continue
		}
		entries, derr := os.ReadDir(filepath.Join(w.root, d.Name()))
		if derr != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			n := e.Name()
			if !strings.HasPrefix(n, "seg-") || !strings.HasSuffix(n, ".ndjson") {
				continue
			}
			info, ierr := e.Info()
			if ierr != nil {
				continue
			}
			segs = append(segs, seg{filepath.Join(w.root, d.Name(), n), n, info.Size()})
			total += info.Size()
		}
	}
	cap := w.capBytes()
	if total <= cap {
		return
	}
	active := filepath.Join(w.dir, w.name)
	sort.Slice(segs, func(i, j int) bool { return segs[i].base < segs[j].base })
	for _, s := range segs {
		if total <= cap {
			break
		}
		if s.path == active {
			continue // never evict the active segment
		}
		if os.Remove(s.path) == nil {
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
