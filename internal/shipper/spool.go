// Package shipper implements the CLI side of the P2 structured-telemetry
// pipeline (design docs/architecture/session-log-streaming-design.md §4/§8,
// frozen contract .tmux-cli/research/2026-07-22-16/p2-events-contract.md).
//
// It reads append-only spool segments produced by internal/telemetry's writer
// (worker 1), batches complete NDJSON lines, gzip-compresses each batch (≤ 5 MiB
// compressed), and POSTs them to the backend ingest with a per-batch idempotency
// key. A shipper-owned cursor.json ({segment, offset}) is advanced atomically
// after each acked batch; a segment is deleted only once it is fully shipped AND
// a newer (rotated) segment proves it is closed. Auth is Bearer-only with a
// producer-style single-flight refresh on 401 (internal/auth is the sole token
// authority). The shipper degrades gracefully: transport failures back off and
// retry, and nothing here ever crashes the tmux session.
//
// Dependency direction is shipper -> {auth, identity}; both leaf packages are
// reused verbatim and never import shipper in reverse.
package shipper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	spoolSubdir    = "spool"
	cursorFileName = "cursor.json"
	// segmentPrefix / segmentSuffix bound the append-only segment file names the
	// writer emits: events-<UTCstamp>-<pid>.jsonl (contract §Spool). The stamp is
	// fixed-width yyyymmddThhmmss so lexical name order equals chronological order.
	segmentPrefix = "events-"
	segmentSuffix = ".jsonl"
)

// SpoolDir returns the spool directory (.tmux-cli/logs/spool) under projectRoot.
func SpoolDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-cli", "logs", spoolSubdir)
}

// ListSegments returns the spool's segment file names (not full paths) sorted
// ascending — which, given the fixed-width UTC stamp, is chronological order.
// A missing spool directory yields an empty slice with no error (nothing spooled
// yet is not a failure).
func ListSegments(spoolDir string) ([]string, error) {
	entries, err := os.ReadDir(spoolDir)
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
		if strings.HasPrefix(n, segmentPrefix) && strings.HasSuffix(n, segmentSuffix) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

// Cursor is the shipper-owned resume position: the segment currently being
// shipped and the byte offset within it that has been shipped AND acked. JSON
// keys are byte-exact with the contract (cursor.json {"segment","offset"}).
type Cursor struct {
	Segment string `json:"segment"`
	Offset  int64  `json:"offset"`
}

// LoadCursor reads cursor.json from spoolDir. A missing or corrupt cursor
// degrades to the zero Cursor{} ("start from the first segment") with no error —
// the cursor is a resumption hint, never a source of truth that can wedge the
// shipper.
func LoadCursor(spoolDir string) (Cursor, error) {
	data, err := os.ReadFile(filepath.Join(spoolDir, cursorFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return Cursor{}, nil
		}
		return Cursor{}, err
	}
	var c Cursor
	if err := json.Unmarshal(data, &c); err != nil {
		// Corrupt cursor → restart from the beginning rather than erroring; the
		// backend dedupes by X-Batch-Id so a replay is harmless.
		return Cursor{}, nil
	}
	return c, nil
}

// SaveCursor writes cursor.json atomically: a temp file in the same directory is
// written then renamed over the target, so a crash mid-write can never leave a
// partial cursor (contract: "atomic temp+rename").
func SaveCursor(spoolDir string, c Cursor) error {
	if err := os.MkdirAll(spoolDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(spoolDir, cursorFileName+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(spoolDir, cursorFileName)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// segLine is one scanned spool line: raw carries the on-disk bytes INCLUDING the
// terminating newline (so offset accounting is exact), json carries the trimmed
// content when the line is valid JSON, and ship reports whether the line should
// be POSTed (a complete-but-non-JSON line is consumed for offset purposes yet
// skipped from the batch — contract "shipper skips a non-JSON last line").
type segLine struct {
	raw  []byte
	json []byte
	ship bool
}

// scanSegment splits data (the unshipped tail of a segment, read from the
// cursor's offset) into complete lines. Only bytes up to and including the LAST
// newline are "consumed"; a trailing fragment with no terminating newline is a
// torn tail left mid-write by the producer and is held back (not returned, not
// counted in consumed) so it ships once the writer completes it. Complete lines
// that fail to parse as JSON are returned with ship=false but still counted in
// consumed, so a genuinely corrupt line advances the cursor instead of wedging.
func scanSegment(data []byte) (lines []segLine, consumed int64) {
	for {
		nl := indexByte(data[consumed:], '\n')
		if nl < 0 {
			// No further newline — the remainder is an incomplete trailing line.
			return lines, consumed
		}
		lineLen := nl + 1 // include the newline
		raw := data[consumed : consumed+int64(lineLen)]
		content := trimLineEnd(raw)
		ln := segLine{raw: raw}
		if len(content) > 0 && json.Valid(content) {
			ln.json = content
			ln.ship = true
		}
		lines = append(lines, ln)
		consumed += int64(lineLen)
	}
}

// indexByte returns the index of the first b in data, or -1. (Local helper to
// keep scanSegment allocation-free and dependency-light.)
func indexByte(data []byte, b byte) int {
	for i := 0; i < len(data); i++ {
		if data[i] == b {
			return i
		}
	}
	return -1
}

// trimLineEnd returns raw without its trailing \n (and a preceding \r if the
// writer ever emitted CRLF).
func trimLineEnd(raw []byte) []byte {
	end := len(raw)
	if end > 0 && raw[end-1] == '\n' {
		end--
	}
	if end > 0 && raw[end-1] == '\r' {
		end--
	}
	return raw[:end]
}
