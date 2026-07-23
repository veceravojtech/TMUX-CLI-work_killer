package shipper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/console/tmux-cli/internal/auth"
)

// softRawBatchCap bounds a batch's RAW (uncompressed) NDJSON size. gzip never
// expands text by more than a negligible header/overhead margin, so a raw batch
// ≤ 4 MiB is guaranteed to compress to well under the contract's 5 MiB compressed
// cap — letting the batcher size batches with a single cheap byte count instead
// of gzip-probing after every line. In practice a whole segment (writer rotates
// at 1 MiB) is one batch and this cap never triggers.
const softRawBatchCap = 4 << 20

// Shipper ships one session's spool to the backend ingest. It is constructed per
// `tmux-cli logs ship` invocation with the resolved session id and an auth-wired
// Client.
type Shipper struct {
	spoolDir  string
	sessionID string
	client    *Client
}

// New builds a Shipper for sessionID against the spool under projectRoot.
func New(projectRoot, sessionID string, client *Client) *Shipper {
	return &Shipper{
		spoolDir:  SpoolDir(projectRoot),
		sessionID: sessionID,
		client:    client,
	}
}

// ShipOnce ships every currently-available complete spool line once, advancing
// (and persisting) the cursor after each acked batch and deleting rotated
// segments once fully shipped. It is safe to call repeatedly: "nothing new to
// ship" is a no-op success. Any transport/auth error stops the pass and is
// returned so the caller can back off and resume from the persisted cursor — no
// data is lost because a segment is deleted only after its bytes are acked.
func (s *Shipper) ShipOnce(ctx context.Context) error {
	segs, err := ListSegments(s.spoolDir)
	if err != nil {
		return err
	}
	if len(segs) == 0 {
		return nil
	}
	cur, err := LoadCursor(s.spoolDir)
	if err != nil {
		return err
	}

	for i, seg := range segs {
		// A segment strictly older than the cursor's segment was fully shipped in a
		// prior run (the cursor only advances off a segment once it is drained);
		// reap any such leftover and move on.
		if cur.Segment != "" && seg < cur.Segment {
			_ = os.Remove(filepath.Join(s.spoolDir, seg))
			continue
		}
		start := int64(0)
		if seg == cur.Segment {
			start = cur.Offset
		}
		end, err := s.shipSegment(ctx, seg, start)
		if err != nil {
			return err
		}
		isLast := i == len(segs)-1
		if isLast {
			// The newest segment is still open — the writer may append more. Leave
			// it in place; the cursor now marks how far it is drained.
			cur = Cursor{Segment: seg, Offset: end}
			continue
		}
		// A newer segment exists, proving this one is rotated (closed) and fully
		// drained: delete it and advance the cursor to the next segment's start.
		if err := os.Remove(filepath.Join(s.spoolDir, seg)); err != nil && !os.IsNotExist(err) {
			return err
		}
		next := segs[i+1]
		if err := SaveCursor(s.spoolDir, Cursor{Segment: next, Offset: 0}); err != nil {
			return err
		}
		cur = Cursor{Segment: next, Offset: 0}
	}
	return nil
}

// shipSegment ships the unshipped tail of one segment (from byte offset start),
// batching complete lines under the raw-size cap, POSTing each batch, and
// persisting the cursor after every acked batch. It returns the byte offset up to
// which the segment is now drained (start + bytes of all complete lines). A torn
// trailing line (no terminating newline) is held back and NOT counted, so it
// ships on a later pass once the writer completes it.
func (s *Shipper) shipSegment(ctx context.Context, seg string, start int64) (int64, error) {
	path := filepath.Join(s.spoolDir, seg)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Raced with a delete/rotation — treat as drained.
			return start, nil
		}
		return 0, err
	}
	if start > int64(len(data)) {
		// Defensive: a truncated/rotated file behind the cursor — nothing to ship.
		return int64(len(data)), nil
	}
	lines, _ := scanSegment(data[start:])

	cum := start
	saved := start
	var pending [][]byte
	pendingRaw := 0

	flush := func(end int64) error {
		if len(pending) == 0 && end == saved {
			return nil // nothing to post and cursor unchanged
		}
		if len(pending) > 0 {
			gz, gerr := gzipLines(pending)
			if gerr != nil {
				return gerr
			}
			if _, perr := s.client.PostEvents(ctx, s.sessionID, gz); perr != nil {
				return perr
			}
		}
		pending = nil
		pendingRaw = 0
		if err := SaveCursor(s.spoolDir, Cursor{Segment: seg, Offset: end}); err != nil {
			return err
		}
		saved = end
		return nil
	}

	for _, ln := range lines {
		if ln.ship {
			if pendingRaw > 0 && pendingRaw+len(ln.json)+1 > softRawBatchCap {
				// Adding this line would exceed the raw cap — flush the batch at the
				// boundary BEFORE this line (cum has not yet counted it).
				if err := flush(cum); err != nil {
					return 0, err
				}
			}
			pending = append(pending, ln.json)
			pendingRaw += len(ln.json) + 1
		}
		cum += int64(len(ln.raw))
	}
	if err := flush(cum); err != nil {
		return 0, err
	}
	return cum, nil
}

// RunOptions tunes the detached ship loop.
type RunOptions struct {
	// PollInterval is the idle cadence between successful passes.
	PollInterval time.Duration
	// MinBackoff / MaxBackoff bound the exponential backoff applied after a
	// transient error.
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// ReauthBackoff is the (longer) pause used when shipping is blocked on
	// re-authentication (ErrLoginRequired) — retrying fast would just 401 again.
	ReauthBackoff time.Duration
}

// DefaultRunOptions returns production cadences: a 5s idle poll, 1s→60s
// exponential backoff on transient errors, and a 5m pause when login is required.
func DefaultRunOptions() RunOptions {
	return RunOptions{
		PollInterval:  5 * time.Second,
		MinBackoff:    1 * time.Second,
		MaxBackoff:    60 * time.Second,
		ReauthBackoff: 5 * time.Minute,
	}
}

// Run drives ShipOnce forever on a poll interval, applying exponential backoff on
// transient errors and a longer pause when login is required. It returns only
// when ctx is cancelled (the detached process is killed). Errors are logged to
// stderr but never propagate as a crash — telemetry must never take the session
// down.
func (s *Shipper) Run(ctx context.Context, opts RunOptions) error {
	b := backoff{min: opts.MinBackoff, max: opts.MaxBackoff}
	for {
		err := s.ShipOnce(ctx)
		var delay time.Duration
		switch {
		case err == nil:
			b.reset()
			delay = opts.PollInterval
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return err
		case errors.Is(err, ErrLoginRequired):
			fmt.Fprintln(os.Stderr, "shipper: paused — run: tmux-cli login")
			delay = opts.ReauthBackoff
		default:
			delay = b.next()
			fmt.Fprintf(os.Stderr, "shipper: transient error, retrying in %s: %v\n", delay, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// backoff is a simple capped exponential backoff.
type backoff struct {
	min, max, cur time.Duration
}

func (b *backoff) next() time.Duration {
	if b.cur == 0 {
		b.cur = b.min
	} else {
		b.cur *= 2
		if b.cur > b.max {
			b.cur = b.max
		}
	}
	return b.cur
}

func (b *backoff) reset() { b.cur = 0 }

// LoggedIn reports whether the auth store currently holds a login (a stored,
// possibly-refreshable token). A stale-but-refreshable token still counts —
// refresh is transparent at ship time. Used by the start-path gating decision.
func LoggedIn(store *auth.Store) bool {
	a, err := store.Load()
	return err == nil && a != nil
}
