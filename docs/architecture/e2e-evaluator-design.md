# Design — `/tmux:e2e-evaluator` — self-healing end-to-end evaluation loop

**Status:** Design (not yet implemented). Drafted 2026-06-28 from the live two-session PoC (drove `/tmux:project-discovery` in a throwaway Symfony project from the orchestrator session via `tmux send-keys` + `capture-pane`, with a working reply channel back to the orchestrator pane).
**Author:** drafted with Vojta.
**Scope:** A new **embedded** command `/tmux:e2e-evaluator` (shipped from `cmd/tmux-cli/embedded/commands/tmux/`, installed like every other `/tmux:` command). It is a *meta* tool: it tests tmux-cli's full flow against itself in disposable sessions, files defects as backend tasks, waits for the fix to land, rebuilds, and re-runs — up to 10 cycles — producing a per-cycle report and resuming across a cleared context.

---

## 1. Goal

Drive and verify the **entire** tmux-cli flow end-to-end, autonomously, and **self-heal** the tooling when the flow breaks:

```
discovery → roadmap/goal-generation → taskvisor implementation → running app
```

The canonical scenario (the first thing it must get green):

> **A running Symfony app that serves a dashboard reachable only after login.**
> The evaluator leads it from discovery, through goal generation, starts taskvisor, monitors the
> build, self-heals on failure, and repeats until the whole build succeeds and the app is
> verifiably up (login page serves, dashboard route is auth-protected, authenticated request reaches it).

It also **measures per-phase/per-goal running times** and tunes acceptable-duration thresholds from observed data (§9), so "slow" becomes a first-class, reportable signal — not just "failed".

---

## 2. The self-healing loop (one cycle)

```
┌── CYCLE n (max 10) ─────────────────────────────────────────────────────────┐
│ 1. PROVISION   fresh /tmp/<scenario>-<ts> dir + `tmux-cli start` (detached)   │
│                + attach the one native terminal found (Konsole) for the human │
│ 2. BOOTSTRAP   launch claude (trust pre-accepted, bypass perms), pipe-pane    │
│                the target transcript to a per-session log                     │
│ 3. DRIVE       send the scenario instruction; the target runs discovery →     │
│                roadmap → taskvisor                                            │
│ 4. WATCH       PRIMARY: target reports progress over the channel              │
│                FALLBACK: capture-pane / Monitor only when the channel is silent│
│ 5. MEASURE     record start/end of every goal + phase (elaborate/impl/inv/val)│
│ 6. JUDGE       against the scenario's intended end-state (§8) + timing (§9)   │
│ ├─ PASS  → build succeeded + app verified up → record green, stop the loop    │
│ └─ FAIL  → TRIAGE (§7):                                                       │
│       defect → /tmux:task-report → poll via /tmux:task-list to resolved →     │
│               WAIT for fix commit + auto-install → TEARDOWN (§10) → CYCLE n+1  │
│       variance/bad-input → retry once clean; if it passes, not a defect       │
│ 7. REPORT      write e2e-report-cycle-<n>.md + update run state (§6)          │
└──────────────────────────────────────────────────────────────────────────────┘
```

Pristine every cycle. A failed cycle's session **and** all its `/tmp` content are deleted before the next (§10).

---

## 3. Two-process architecture

| Role | Where | Talks via |
|---|---|---|
| **Orchestrator** | this repo's session (the running `/tmux:e2e-evaluator`) | drives target with `tmux send-keys`/`paste-buffer`; observes the target's **pipe-pane log**; files tasks with `/tmux:task-report`; polls task state with `/tmux:task-list`; **waits for** the fix commit + external auto-install (does not build/pull itself) |
| **Target** | a fresh `/tmp/<scenario>-<ts>` session created with `tmux-cli start` (detached) | runs `claude`, executes the real flow, reports progress back to the orchestrator pane |

**Key property (learned in the PoC):** `send-keys`/`capture-pane`/`pipe-pane` all work on a **detached** session — no terminal required to *drive*. A native terminal is attached only so the **human can watch** (you asked for this); driving is headless and works in CI/SSH. The flatpak Konsole is a trap (sandboxed: no host binary/TTY); use the native terminal only.

The orchestrator drives via **CLI + send-keys**, never via the tmux-cli **MCP tools** — MCP tools are loaded into a Claude at startup, so a mid-loop `make install` would not refresh them. CLI/send-keys are re-read from disk every call, so the orchestrator survives rebuilds without restarting. (`/tmux:task-report` is a skill that wraps the `task-report` MCP tool; if that path proves stale-prone post-rebuild, fall back to the `tmux-cli` CLI equivalent.)

---

## 4. Communication protocol

**Primary — target → orchestrator (proactive reports).** The target reports milestones ("discovery done", "roadmap: 14 goals", "goal-007 failed: <reason>") so the orchestrator is event-driven, not polling.

**Init-prompt handshake (first message, before any flow DRIVE).** The target does **not** report via `notify-orchestrator` unless it is told to — so the orchestrator's *very first* message into the target is an **init prompt**, not a flow `/command`. It (a) tells the target it runs under the e2e-evaluator orchestrator, (b) makes `tmux-cli notify-orchestrator "<msg>"` the mandatory milestone-reporting contract for the whole run (the milestone vocabulary WATCH expects: `discovery-done → roadmap-generated → preflight-passed → goals-dispatched → goals-done → app-up`, plus `goal-<id> failed: <reason>`), and (c) orders an immediate **comms-test** — the target echoes `E2E-HANDSHAKE-OK <session>` back, and the orchestrator verifies it landed in `TMUX_CLI_ORCHESTRATOR_PANE`. No flow DRIVE begins until the handshake passes; a dead channel aborts the cycle rather than silently degrading WATCH to blind log-polling. This is the e2e-evaluator.xml **step 3b** gate (BOOTSTRAP's handshake tail).

The PoC proved this works but exposed the failure mode: the target hand-rolled `tmux send-keys -t %5 "msg" Enter` and **twice got it wrong** (forgot the separate `Enter`; quoting). A primary channel cannot depend on hand-rolled keystrokes. So:

> **New primitive — `tmux-cli notify-orchestrator "<msg>"`** (decision #1, approved).
> Resolves the orchestrator's pane from an env var injected at bootstrap (`TMUX_CLI_ORCHESTRATOR_PANE`),
> sends the text **and** the submit key **atomically** (text via a tmux buffer + `paste-buffer`, then `Enter`
> as a separate key), with escaping handled in Go. The target calls one command; no keystroke choreography.
> Mirrors the existing `windows-message` design but targets a pane id across sessions instead of a
> cwd-resolved in-session window.

**Fallback — capture-pane / Monitor.** Used **only when the channel goes silent** past a heartbeat window (§9 timeouts): the orchestrator reads the pipe-pane log and/or `capture-pane` to diagnose a hang, then decides retry vs. defect.

**Orchestrator → target (drive).** Always two steps, never combined: `paste-buffer`/`send-keys -l "<text>"` **then** a separate `Enter`. Large/complex inputs use `tmux load-buffer` + `paste-buffer` (no shell-escaping). Readiness before sending: poll the log/pane until the idle `❯` prompt is stable and no spinner ("Cogitating/Germinating…").

**Transcript of record.** `tmux pipe-pane -o -t <target-pane> 'cat >> <session>.log'` captures the *entire* target transcript to a file. Assertions grep the log, not the screen (screen scraping is timing-fragile and only shows the visible region).

---

## 5. Provisioning & bootstrap

> **Automated.** The entire prologue below is performed by a single command — `tmux-cli e2e-bootstrap <scenario> [--project <path>] [--resume]` — so the conductor LLM does not hand-drive it tool-call-by-tool-call (the original PoC wasted most of its time there). The command runs each item as a hard gate, self-tears-down on any failure, and prints one JSON line (`{ok, session, target_pane, orchestrator_pane, target_dir, log_path, state_file, cycle, human_view, handshake}`) the conductor reads once before DRIVE. The handshake is the real proof the orchestrator-pane env propagated (notify-orchestrator fails loudly without it), so the LLM takes over only for judgment after `ok:true`.

- **Dir:** `--project <path>` or default `/tmp/<scenario>-<UTCstamp>` (kept under `/tmp/`). `git init` if the flow needs a repo.
- **Session:** `tmux-cli start` (detached). Optionally also attach the discovered native terminal for the human view.
- **Terminal discovery (decision #4):** probe native terminals in order and use the first found; on this host that's **Konsole**. If none / no `$DISPLAY`: run fully headless (no attach) — driving is unaffected.
- **claude bootstrap:** pre-accept the trust prompt and start in bypass-permissions mode so respawns are non-interactive; inject `TMUX_CLI_ORCHESTRATOR_PANE`. The "N MCP servers need authentication" warning is **ignored** — only the `tmux-cli` MCP server matters (decision #5).
- **Logging:** start `pipe-pane` immediately so nothing is missed.

---

## 6. Cross-context continuation & reporting (decision #6)

The loop must survive a **cleared orchestrator context**: re-invoking `/tmux:e2e-evaluator` with no args resumes the next cycle automatically.

- **Run state** — `.tmux-cli/e2e-evaluator/<scenario>.state.json`:
  ```jsonc
  {
    "scenario": "symfony-dashboard-login",
    "cycle": 4,                    // next cycle to run
    "max_cycles": 10,
    "status": "in-progress",       // in-progress | passed | exhausted | escalated
    "history": [
      { "cycle": 3, "outcome": "failed", "defect_signature": "elaboration timeout goal-009",
        "task_reported": "task-281", "task_status": "resolved",
        "git_after": "<sha>", "durations": { /* §9 */ } }
    ]
  }
  ```
- **Per-cycle report** — `.tmux-cli/e2e-evaluator/e2e-report-cycle-<n>.md`: what was driven, where it failed, the defect signature, the task filed, timing table, and the verdict.
- **Resume rule:** on invocation, read the state file; if `status == in-progress` and `cycle <= max_cycles`, continue at `cycle`. **Max 10 cycles**, then `status: exhausted` and escalate to human.

---

## 7. Failure triage (before any task-report) — decision (recommended)

Only a genuine **tmux-cli defect** earns a `task-report`. Three failure classes:

| Class | Signal | Action |
|---|---|---|
| **tmux-cli defect** | reproduces on a clean retry; failure is in tooling (dispatch/worktree/compose/elaboration/daemon), not in the worker's code choices | `/tmux:task-report` → self-heal |
| **agent/LLM variance** | a one-off bad choice by the worker; does **not** reproduce on a fresh clean run | retry; do not file |
| **bad test input / wrong expectation** | the scenario prompt or success criteria was wrong | fix the evaluator's own inputs; do not file |

**Triage authority:** the orchestrator Claude judges, gated by the **reproduce-on-clean-retry rule** — a failure is only a defect if a fresh pristine run hits the *same signature*. This prevents filing bogus tasks against LLM noise. (Open for your veto.)

**Defect signature** = normalized {phase, failure reason, goal/area}. Used to dedupe across cycles and to enforce the loop guard: the *same* signature failing twice after a fix → escalate to human (don't thrash).

---

## 8. Success-criteria derivation

- **Full-flow scenario (primary):** end-state is concrete and verifiable, not just "goals done":
  1. discovery produced `docs/architecture/*`;
  2. a roadmap `goals.yaml` was generated (skeleton nodes, then elaborated);
  3. taskvisor drove all goals to `done`;
  4. **the app is actually up** — `docker compose up`, then: `GET /login` → 200; dashboard route unauthenticated → 302/401; authenticated request → 200. This is the deliverable-pinning principle from the two-tier design applied to the *whole build* (a green daemon with a dead app is a false pass).
- **Single-command mode (secondary):** derive the intended end-state from the **command-under-test's own skill/XML description** (the `description`/contract), then assert the artifacts that command claims to produce.

**Registered single-command scenarios:**

| Slug | Command under test | Definition |
|---|---|---|
| `supervisor-fresh-handoff` | `/tmux:supervisor:fresh` + the step-9b standalone handoff | [`supervisor-fresh-design.md` §8.1](supervisor-fresh-design.md) — two-wave plan document through a standalone supervisor; asserts marker armed/consumed/gone, `/clear`-before-relaunch ordering, step-0c counter adoption, and that the SELF_WAVE cap holds across `/clear` (no Docker, no `goals.yaml`) |

---

## 9. Timing metrics & tuning (decision #3, second half)

Record start/end timestamps for **every goal** and **every phase** (`elaborate`, `implement`, `investigate`, `validate`) — taskvisor already drives these transitions; the evaluator reads them from the dashboard/goal state and the log.

**Initial baseline ceilings** (to be *tuned from observed data*, not treated as gospel):

| Phase | Target (warn over) | Hard ceiling / fail-safe |
|---|---|---|
| Elaboration (per goal) | 8 min | 20 min (matches existing elaboration fail-safe) |
| Implementation (per goal) | 20 min | 30 min |
| Investigation (per goal) | 10 min | 15 min |
| Validation (per goal) | 10 min | 15 min |
| **Whole build (scenario)** | 75 min | 120 min |

**Tuning method (recommended metrics):** across cycles, collect per-phase durations; compute **p50 / p90 / p95**; set the "warn" threshold at observed **p90** and the hard ceiling at **max(absolute ceiling, p95 × 1.5)**. Each report flags goals/phases exceeding warn (investigate) or ceiling (treated as a hang → fallback diagnosis → triage). Over-ceiling with no progress on the channel = the silence trigger for the capture-pane/Monitor fallback (§4).

The point: the evaluator doesn't just answer "did it build?" — it answers "did it build **within acceptable time**, and which phase is the bottleneck?", feeding both defect reports and perf tuning.

---

## 10. Teardown / leak control

A full implementation run spawns artifacts **outside** `/tmp`: docker compose stacks, git worktrees, a taskvisor daemon — these collide on host ports across cycles (the exact failure class in the director design). Teardown must reap all of it, in order:

1. `tmux-cli`/taskvisor: stop the daemon for the target.
2. `docker compose down` every stack the run created (network-internal by default per the stack convention; still must be downed).
3. Remove git worktrees + their branches.
4. `tmux kill-session -t <target-session>` (by **exact name**).
5. `rm -rf /tmp/<scenario>-<ts>`.

**Never `pkill -f <projectname>`** — it matches the killer's own command line and SIGTERMs the teardown mid-run (we hit exactly this: exit 144, `rm` skipped). Target by **session name + tracked PIDs** only.

---

## 11. New/changed surface

| Item | Kind | Notes |
|---|---|---|
| `embedded/commands/tmux/e2e-evaluator.xml` | new embedded command | the conductor; installed like the rest |
| `tmux-cli e2e-bootstrap <scenario>` | new CLI subcommand | **automates the whole deterministic prologue** (preconditions → reap stale `tmux-cli-tmp-*` → resolve/`git init` dir → seed `~/.claude.json` trust → `tmux-cli start` → attach human view → `pipe-pane` → init-prompt HANDSHAKE) and emits one JSON line the conductor reads before DRIVE. Every step is a hard gate; on failure it self-tears-down and exits non-zero. Pure logic lives in `internal/e2e/` (unit-tested); the conductor LLM only takes over for judgment after `ok:true`. |
| `tmux-cli e2e-teardown <session>` | new CLI subcommand | the ordered §10 reap (daemon → `compose down` → worktrees → kill-session → `rm -rf`), best-effort, never `pkill -f`. |
| `tmux-cli notify-orchestrator "<msg>"` | new CLI subcommand (decision #1) | reliable atomic reply channel; reads `TMUX_CLI_ORCHESTRATOR_PANE` |
| `TMUX_CLI_ORCHESTRATOR_PANE` | new env, injected at target bootstrap | the orchestrator pane the target reports to |
| `.tmux-cli/e2e-evaluator/` | runtime state + reports | `state.json`, `e2e-report-cycle-<n>.md` |
| pipe-pane logging | bootstrap behavior | full target transcript of record |

---

## 12. Resolved decisions

1. **Fix actor — RESOLVED.** `/tmux:task-report` has its **own consumer** (an always-on pipeline on this repo); the evaluator does **not** spawn or drive the fix. It only **files** the defect, then **waits**: polls task state via `/tmux:task-list` until the task is `resolved`, and waits for the fix commit to land on **this cli repo** + the **external auto-install** to rebuild the binary. It never runs `git pull`/`make install` itself.
2. **Triage authority — RESOLVED (§7):** orchestrator-judged, gated by the reproduce-on-clean-retry rule.
3. **Fix location — RESOLVED:** fixes land on **this cli repo** (local), picked up by auto-install on new commit. The evaluator checks resolution via `/tmux:task-list` and detects the rebuilt binary (version/commit/mtime bump) before starting the next cycle.

**Dependency note:** the loop's autonomy relies on (a) the task-report consumer being live, and (b) an auto-install watcher that rebuilds `~/.local/bin/tmux-cli` on new commits. tmux-cli ships no such watcher today (`make install` is manual) — it is an environment prerequisite the evaluator should **assert at startup** (consumer reachable? auto-install present?) and otherwise degrade to "pause and tell the human to `make install`".

---

## 13. Parallelism & the 20-minute-per-goal target

A core objective: goals should run **~20 min each, in parallel**, not hour-long serial chains. The system already has every lever — they are just **off by default**. The evaluator must **set parallel work up *before* goal consumption begins** and then measure against it.

### 14.1 What the code actually does (audit)

| Lever | Where | Default | Effect |
|---|---|---|---|
| `supervisor.max_goals` | `config.go` (`SupervisorSettings.MaxGoals`) | **1** | Master concurrency cap. **=1 ⇒ fully serial** (no worktree, base tree, byte-identical legacy). **>1 ⇒** per-goal worktree isolation + per-worktree compose stacks + namespaced windows (`execute-<id>` etc.). |
| `.tmux-cli/taskvisor-concurrency` | `concurrency.go` | absent | Runtime override (atomic temp+rename), changeable **without restart**; wins over `max_goals` when ≥1. |
| `DisjointReadySet(maxGoals)` | `statemachine.go` | — | Slot-fill is **scope-gated**: co-schedules only goals whose declared file `Scope` is **disjoint**. Unknown/overlapping scope ⇒ forced serialize even at `max_goals>1`. |
| `depinfer` + plan-audit | `depinfer.go` | — | `depends_on` from produce/consume overlap (not ordinal/phase); plan-audit warns on **over-serialized** (near-linear / single-runnable) DAGs. |
| `max_wall_clock_sec` (P3) | `config.go` / `daemon.go` | **14400 (4h)**, applied **per-goal** | Hard wall-clock ceiling per in-flight goal; on exceed the daemon halts loudly, leaving status untouched. |
| `progress_timeout_sec` (P2) | `config.go` | **300 (5m)** | Kills a window emitting **no pane output** for this long (wedged-LLM heartbeat) — keeps stuck goals from eating the full ceiling. |
| `supervisor.max_workers` | `config.go` | 4 | Separate fan-out cap for `/supervisor`→`/execute` workers (not the daemon's goal cap). |

**Conclusion:** four things must *all* be true to get real parallelism — flip the cap, **and** give the scheduler a parallelizable DAG, disjoint scopes, and host capacity. Flipping `max_goals` alone on a linear DAG (or goals with unknown scope) changes nothing.

### 14.2 Preflight — set up parallel work BEFORE consuming goals

The evaluator runs this as a gate between **roadmap generated** and **taskvisor start**:

1. **Set the cap.** Choose `max_goals = N` (start N=3–4) and write it (setting.yaml or the `.tmux-cli/taskvisor-concurrency` override). Pick N from **host capacity**, not optimism (see step 5).
2. **Prove the DAG is parallelizable.** Run plan-audit on the roadmap; **fail the preflight if it reports near-linear / single-runnable** (the depinfer over-serialization signal). A linear DAG ⇒ no concurrency possible; fix the roadmap (produce/consume `depends_on`) before starting.
3. **Verify disjoint scopes.** Every roadmap node about to run concurrently must declare a `Scope` that's disjoint from its siblings — otherwise `DisjointReadySet` serializes them silently. Unknown scope = serialized; flag it.
4. **Right-size goals for ~20 min.** Smaller goals = more concurrency *and* shorter per-goal wall time. Elaboration (Tier-2) should split a node whose estimated footprint won't fit the target. Oversized goals are the root cause of hour-long runs — parallelism can't fix a single 60-min goal.
5. **Confirm host capacity for N parallel stacks.** `max_goals=N` ⇒ up to N concurrent docker compose stacks + worktrees. Verify CPU/RAM and **no host-port collisions** (network-internal stacks by default; port-alloc when a host port is truly needed — director design §9). If the host can't run N stacks, N is too high regardless of the DAG.
6. **Set the ceilings as guardrails, not targets.** Keep `max_wall_clock_sec` a **generous safety net** (e.g. 1800–2700s / 30–45 min) — *not* 1200s, which would kill legitimately-borderline goals; the 20-min figure is a **target achieved by sizing + parallelism**, enforced softly. Keep `progress_timeout_sec` at ~300s so wedged goals die in 5 min instead of eating the ceiling.

### 14.3 How the evaluator drives the 20-min target

- **Measure** per-goal wall time (§9) and the **degree of parallelism actually achieved** (goals in-flight per tick vs `max_goals`). Low achieved-parallelism at `max_goals>1` is itself a defect signal — it means the DAG/scopes serialized.
- **Tune across cycles:** if p90 per-goal > 20 min, the fix is usually *smaller goals* or *more disjoint scopes*, not a bigger ceiling. If achieved-parallelism ≪ N, the fix is the DAG (over-serialized `depends_on`) — file it as a defect against the **planner/elaborator**, not the worker.
- The report (§6) gains two headline numbers per cycle: **p90 per-goal minutes** and **mean in-flight goals** (achieved parallelism). "Green" requires both the build passing *and* p90 within target.

> Net: the e2e-evaluator doesn't just test that the flow *works* — it drives the system toward *fast, parallel* flow, and turns "a goal took an hour" and "everything ran serially" into first-class, reportable, self-healable defects.

## 14. Non-goals

- Not a replacement for unit/integration tests (`make test-all`) — this is black-box, real-instance E2E.
- Not shipped to drive end-user projects — it is a tmux-cli **self-test** tool (though embedded/installable like the rest, per decision #2).
- Not changing the daemon's implement/investigate/validate cycle — it *observes and times* it.
