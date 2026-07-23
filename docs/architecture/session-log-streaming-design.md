# Design — Cloud session streaming: account login, all-window capture, full-session review

**Status:** Design (not yet implemented). Drafted 2026-07-22.
**Author:** drafted with Vojta.
**Scope:** Three coupled features across the `cli` and `web` lanes: (A) replace the baked-in
Ed25519 producer key with **account login** shared with tmux-web (tmux.vojta.ai); (B)
**automatic streaming of all session logs** to the backend; (C) a **full-session review**
surface covering the supervisor and every spawned agent window. `task-report`/artifact
flows keep working throughout (auth migrates under them).

---

## 1. Problem

1. **Uploads depend on a build-baked key.** The producer client signs every request with an
   Ed25519 private key embedded in the binary (`internal/producer/client.go:58-92,199`,
   `internal/producer/keys/private.pem` via `//go:embed`). A deployed tmux-cli can only
   upload if its binary was built with the key — there is no per-user identity, no
   rotation/revocation story, and fleet deployment means shipping the secret inside every
   binary.
2. **Session logs are local-only.** `.tmux-cli/logs/` already holds `notifications.log`,
   `taskvisor.log`, `postcommand.log`, `sudo.log`, and per-worker pane logs
   (`logs/panes/execute-*.log`, piped by the MCP spawn path) — but nothing leaves the
   machine, so cross-session/cross-machine flow analysis exists only inside the e2e
   evaluator's own ledger.
3. **No whole-session review.** There is no way to look at one tmux-cli session as a unit —
   supervisor turns, every spawned `execute-N`/`prereq-*` agent, daemon activity, hook
   firings — to understand what happened and improve the flow.

## 2. Goals / non-goals

**Goals**
1. `tmux-cli login` — authenticate once per user+machine against the tmux-web account
   system on `api.url` (tmux.vojta.ai); every deployed, logged-in tmux-cli can upload.
2. Automatic, resilient streaming of session telemetry (structured events always;
   transcripts opt-in) attributed to the logged-in account.
3. A full-session review: browse one session end-to-end (all windows/agents) in tmux-web,
   plus a generated review report.

**Non-goals**
- Live remote control of sessions (view/analyze only).
- Replacing `task-report`/`task-artifact-*` semantics — only their auth changes.
- Streaming secrets: transcripts never ship without the redaction pass (§6).
- Removing Ed25519 signing in the first release (deprecation window, §3).

## 3. A — Account login (shared with tmux-web)

**Decision: device-code flow against the tmux-web account system.**
`tmux-cli login` requests a device code from the backend, prints
`https://tmux.vojta.ai/device` + short code (and attempts `xdg-open`), polls until the user
approves in their (possibly already-logged-in) tmux-web browser session, then stores the
issued token. Works headless (SSH boxes — the fleet case) and never handles the password in
the CLI. Email+password prompt is the fallback for fully offline-browser setups.

- **Token storage:** `~/.config/tmux-cli/auth.json` (user-global, `0600`) —
  `{account, access_token, refresh_token, expires_at, api_url}`. NOT per-project: one login
  serves every project on the machine. `tmux-cli logout` deletes it; `tmux-cli whoami`
  prints account + token scopes.
- **Token shape:** opaque bearer tokens issued by tmux-web (same user table/session
  infrastructure as the web UI — "share acc from tmux-web"), scoped
  (`tasks:write artifacts:write telemetry:write`), refreshable, revocable per-device from
  the tmux-web account page (each token records machine fingerprint + hostname as label).
- **Client change:** `internal/producer/client.go` gains a Bearer-auth mode: when
  `auth.json` exists and is valid, requests carry `Authorization: Bearer <token>` (with
  transparent refresh); otherwise the legacy Ed25519 signing path is used unchanged.
  Backend accepts both during the migration window; signing is removed one release after
  login ships. Machine fingerprint stays in payloads as metadata (it becomes attribution,
  not identity).
- **Gating:** upload features degrade gracefully when logged out — task-report falls back
  to legacy signing (until removed), telemetry shipping simply stays local-spooled and
  `tmux-cli start` prints a one-line "not logged in — session streaming disabled" notice.

## 4. B — Session log streaming

**Two data classes, different defaults:**

1. **Structured flow events** (default ON once logged in, `telemetry.enabled: true`):
   JSONL `{ts, account, fingerprint, project, session_id, window, event_type, payload}`.
   Emitters: taskvisor daemon (goal/phase transitions, retries, bounces), hook scripts
   (each notifications.log line doubles as an event), MCP server (windows-spawn-worker,
   task status flips, windows-kill), supervisor/worker lifecycle markers. Payloads carry
   ids/durations/verdicts — never file contents or command output.
2. **Pane transcripts** (opt-in, `telemetry.transcripts: true`): extend the existing
   worker pipe-pane to ALL windows (supervisor, taskvisor, every spawned agent).
   Before upload each segment is ANSI-stripped (OSC+CSI; see the position-tolerant-matching
   lesson from the fresh-handoff e2e) and passed through the **redaction hook**
   (`.tmux-cli/hooks/redact-transcript.sh`, installed default: mask `(?i)(api[_-]?key|token|
   secret|password|bearer)\s*[=:]\s*\S+`, AWS/GCP key shapes, and `.env`-style lines;
   project-extensible). This aligns with the pane-log redaction work already queued in the
   fleet backlog.

**Transport — batch spool, not live streaming:** emitters append to
`.tmux-cli/logs/spool/<segment>.jsonl`; a detached shipper (`tmux-cli logs ship`, started
by `tmux-cli start` when logged in + enabled) gzips and POSTs batches every ~60s with a
persisted cursor — at-least-once, offline-tolerant (outbox semantics, same philosophy as
`.tmux-cli/task-reports/`). Session teardown flushes. No websockets: analysis is
retrospective; a live view can be added later without changing the ingest contract.

**Session manifest:** on `tmux-cli start`, POST `/api/v1/sessions` registers
`{session_id, project, fingerprint, started_at, binary_version}`; windows register lazily
on first event. This is the unit the review (§5) hangs off.

## 5. C — Full-session review (all spawned agents)

- **Backend model (web lane):** `session → windows[] → {events[], transcript segments[]}`
  plus links to goals/tasks/artifacts already known to the backend via task-report.
- **tmux-web session view:** timeline of the whole session — interleaved event stream with
  per-window (per-agent) tabs: supervisor turns, each `execute-N`/`prereq-*` transcript,
  daemon phases, hook firings; filters by window/event-type/time; deep-link to a goal or
  task. (Today's stranded-marker defect would be visible as "marker armed → no consume
  event" at a glance.)
- **Generated review:** `tmux-cli session review [session-id]` (and a button in tmux-web)
  produces a review report over the manifest: per-agent summaries, per-phase durations vs
  fleet p50/p90, retries/bounces/escalations, anomalies (events without expected
  successors, guard-blocked restarts, over-ceiling phases), and flow-improvement
  suggestions. Generation runs server-side in the web lane (it owns the data and the
  fleet baselines); the CLI triggers and fetches. Report is stored with the session and
  rendered in the session view.

## 6. Settings & privacy

```yaml
telemetry:
    enabled: true        # structured events; default true once logged in
    transcripts: false   # pane content upload; OPT-IN per project
```
- Transcripts ship only when: logged in AND `telemetry.transcripts: true` AND the
  redaction hook ran. Any redaction-hook failure = segment stays local (fail-closed).
- Events payloads are schema-bound (ids/enums/durations) — no free-text command output.
- Tokens: `0600`, per-device revocation in tmux-web, scopes as in §3.
- Per-project kill switch: `telemetry.enabled: false` stops the shipper entirely.

## 7. Failure modes

| Failure | Behavior |
|---|---|
| Backend unreachable | Spool grows locally (bounded, oldest-segment eviction at cap); shipper retries with backoff; nothing blocks the session. |
| Token expired/revoked | Shipper pauses + one notice line; `tmux-cli login` resumes; spool preserved. |
| Redaction hook missing/failing | Transcript segments NOT uploaded (fail-closed); events unaffected. |
| Mixed fleet (old binaries) | Backend accepts legacy Ed25519 signing during the deprecation window. |
| Spool corruption | Segment quarantined + skipped (logged), cursor advances — one bad segment never wedges shipping. |

## 8. Phasing & lane split

| Phase | cli lane | web lane |
|---|---|---|
| P1 login | `tmux-cli login/logout/whoami`, auth.json, producer Bearer mode | device-code endpoints, token issue/refresh/revoke UI on account page |
| P2 events | emitters + spool + shipper + session manifest | ingest endpoints + storage + retention |
| P3 transcripts | all-window pipe-pane, ANSI-strip, redaction hook, opt-in gate | transcript storage + per-window rendering |
| P4 review | `tmux-cli session review` trigger/fetch | session timeline UI + generated review |

P1 is strictly first — it unblocks "uploading on every deployed logged tmux-cli" and
everything else authenticates through it.

## 9. Testing

- cli: producer Bearer-vs-signing mode unit tests (httptest, both auth paths + refresh);
  spool/shipper round-trip with a fake ingest server; redaction-hook bash-shim tests
  (secrets masked, fail-closed on hook error); emitters' event-schema golden tests;
  content assertions that `tmux-cli start` wires pipe-pane for supervisor/taskvisor
  windows. Tests pass with no tmux server and no network (AGENTS.md).
- e2e: extend the e2e-evaluator catalogue with a `session-streaming` single-command
  scenario — logged-in target, run a trivial supervisor task, assert session manifest +
  events landed on a stub backend and transcripts stayed local with `transcripts: false`.
- web: contract tests for device-flow, ingest idempotency (at-least-once), review render.

## 10. Open questions

1. Spool cap + retention defaults (suggest: 200 MB local cap, 90-day backend retention).
2. Should the generated review (§5) auto-run on session end, or on-demand only?
   (Suggest: on-demand + auto for sessions that had a failed goal.)
3. Team/multi-user accounts on tmux-web — out of scope here, but token scopes are chosen
   so org accounts can slot in later.
4. Does `task-report`'s machine-fingerprint dedup key gain the account id after P1?
