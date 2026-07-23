# `redact-transcript.sh` — optional user redaction hook (P3 transcripts, Layer 2)

Contract for the optional per-project transcript redaction hook, per the frozen
P3 contract (`§Redaction`) and design `session-log-streaming-design.md §6`. This
document plus the sample below IS the deliverable — tmux-cli does **not**
install this hook; a project opts in by creating the file itself.

## When it runs

- **Location:** `.tmux-cli/hooks/redact-transcript.sh` (project-local).
- **Trigger:** the transcript shipper consults it for every segment tail about
  to ship, **only if the file is present AND executable** (`chmod +x`). A
  missing or non-executable file is normal — Layer 1 (the built-in secret
  masker, always on, cannot be disabled) alone applies.
- **Order:** always AFTER the built-in masker (defense in depth). The hook
  never sees pre-masked secrets that Layer 1 caught.

## I/O contract

- **stdin:** the built-in-masked segment tail as NDJSON — one JSON object per
  line, contract shape:
  `{"ts":"<RFC3339Nano UTC>","session_id":"…","window":"…","kind":"…","seq":<n>,"text":"<masked text>"}`
- **stdout:** the redacted NDJSON. Every emitted line MUST remain a valid JSON
  object; the hook MAY drop lines entirely (redaction by omission) but MUST NOT
  emit non-JSON output. Simple text substitutions (`sed`/`awk` over the stream)
  are safe as long as replacement strings contain no unescaped `"` or `\`.
- **exit code:** `0` on success. Anything else is a failure.
- **timeout:** one invocation is bounded at **10 seconds** (enforced by the
  shipper; a hung hook is killed).

## Fail-closed semantics

If the hook is present and it errors, exits non-zero, times out, or emits
non-NDJSON output, the segment is **dropped from the ship batch**: it stays
local, the shipper logs `redaction hook failed — segment N held local`, and the
pass retries after backoff — so a fixed hook lets held segments ship later.
Nothing unredacted ever leaves the machine. Events (P2) are unaffected.

## Sample

```sh
#!/bin/sh
# .tmux-cli/hooks/redact-transcript.sh — sample Layer-2 transcript redaction.
# stdin:  built-in-masked transcript NDJSON (one JSON object per line)
# stdout: redacted NDJSON (same shape; lines may be dropped, never mangled)
# exit 0 on success; any other exit / timeout holds the segment local.
#
# Install: copy into .tmux-cli/hooks/, adapt the patterns, chmod +x.

sed \
  -e 's/codename-[a-z0-9-]*/«REDACTED:codename»/g' \
  -e 's/[A-Za-z0-9._%+-]*@internal\.example\.com/«REDACTED:email»/g'
```

Notes for hook authors:

- Keep replacements free of `"` and `\` so the JSON lines stay valid.
- To drop an entire line, filter it out (e.g. `grep -v`) — an empty stdout with
  exit 0 is valid and ships nothing.
- The hook is invoked per segment tail, not per line — one process per ship
  pass per segment, so a `sed`/`awk` pipeline is cheap.
