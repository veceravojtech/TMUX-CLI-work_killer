# Per-Product Rule Resolution — Research & Implementation Plan

**Date:** 2026-06-09 · **Implemented:** 2026-06-12 (phase 1 + code-rules catalogue)
**Decisions locked:** Q1=C (hybrid: Go detects signals + emits pack list, agent loads markdown), Q2=A (extract inline rules into catalogue), Q3=A (planner only)
**Open questions resolved (2026-06-12):** resolver surface = CLI `tmux-cli rules resolve`; E2E-SIDEFX-CONV kept intact in database/ (symfony split deferred); unknown CAPABILITY signals include packs conservatively with stderr warning, unknown STACK signals load no stack pack (wrong-stack rules misdirect rather than protect) — `--lang`/`--framework` flags pass discovery session state for greenfield.
**Scope extension (2026-06-12):** the catalogue also carries previo2-style CODE RULES (kind=code-rules, YAML schema per `embedded/rules/SCHEMA.md`) — generic PHP rules (`php/`) + universal Symfony DDD rules (`php-symfony/`, incl. ddd-conventions.md adapted from the previo2 layer contract, provenance stripped, `adapted_from` retained). Project-local growth path: `.tmux-cli/rules/local/{conventions,code-rules}/` — always resolved, never clean-slated by setup. Falsifiability selftest ported as `cmd/tmux-cli/rules_catalogue_test.go`.
**Goal:** Resolve rules per product/project — when the project uses Docker, load docker rules; when PHP/Symfony, load PHP rules; etc. Clean up and separate concerns out of the monolithic planner XML.

---

## 1. How templating works in this codebase (context)

There is **no `text/template` engine**. The system has two layers:

- **Layer 1 — XML files are agent programs.** `cmd/tmux-cli/embedded/commands/tmux/*.xml` are procedural specs executed *by a Claude agent*, not rendered by Go. `execute.xml` is a worker's full behavioral contract (`<objective>`, `<flow>`/`<step>`, `<rule critical="true">`, `<template>` reply formats).
- **Layer 2 — variables are injected as a message.** Placeholders like `<SELF_WID>`, `SUBTASK`, `SCOPE` are *documentation of what the agent receives*. Values are assembled in Go with `fmt.Fprintf` in `buildTaskMessage()` (`internal/mcp/tools.go:600`) and delivered to the tmux pane via two sends: `/tmux:execute` (boot the agent into the XML) then the task payload (`WindowsSpawnWorker`, `internal/mcp/tools.go:648`).

Embedding:
```go
// cmd/tmux-cli/session.go:47
//go:embed embedded/commands/tmux
var embeddedCommands embed.FS
//go:embed all:embedded/templates
var embeddedTemplates embed.FS
```

Materialization at setup: `runAutoSetup` (`session.go:1652`) WalkDirs `embedded/templates` into a map → `setup.Run` → `WriteTemplates` writes to `.tmux-cli/templates/` in the project (`internal/setup/templates.go:9`).

---

## 2. What already exists for stack/product resolution

### 2.1 Stack-keyed template lookup (the "look here for PHP" part — already works)
`task-plan-generate.xml:20` defines `TEMPLATE_DIR` as a **two-tier lookup**:
```
templates/<LANG>-<FRAMEWORK>/  →  templates/<LANG>/  →  templates/_base/
```
PHP rules live as markdown overlays: `embedded/templates/php-symfony/*.md` (quality-gates.md, fixtures.md, error-handling.md, …) overriding `_base/`.

### 2.2 Capability-conditioned rules (the "when docker" part — exists but baked inline)
The `<conventions>` block of `task-plan-generate.xml:36-57` holds 11 cross-cutting rules, each already tagged with a `condition=` that maps 1:1 to a capability:

| Rule id | line | condition | concern |
|---|---|---|---|
| CMD-CONV | 37 | (always) | command execution |
| HTTP-CONV | 38 | (always) | command execution |
| NODE-TOOL-CONV | 39 | (frontend implied) | frontend |
| ENSURE-STACK-CONV | 40 | HAS_DATABASE | database |
| HTTP-WAIT-CONV | 41 | (always) | command execution |
| E2E-ARTIFACT-CONV | 42 | HAS_FRONTEND | frontend |
| DOCKER-RUNTIME-FRONTLOAD | 43 | RUN_TARGET=docker | docker |
| E2E-ENV-CONV | 44 | HAS_DATABASE | database |
| E2E-SIDEFX-CONV | 45 | HAS_DATABASE | database (+ symfony specifics: mailer/messenger/http-client) |
| E2E-DATA-ISOLATION-CONV | 46 | HAS_DATABASE | database |
| E2E-AUTH-STATE-CONV | 47 | N_auth_flows>=1 AND HAS_FRONTEND | frontend-auth |

Plus two universal items: `scope-derivation` (line 49) and `validate-acceptance-mandate` (line 51).

### 2.3 Signal detection (reuse target for the Go resolver)
`internal/taskvisor/execruntime.go:29` already parses `docs/architecture/test-environment.md` for `RunTarget` ("docker"|"local"), `AppSvc`, `NodeSvc`, and `playwrightApplicable`. Signals (`LANG`, `FRAMEWORK`, `RUN_TARGET`, `HAS_DATABASE`, `HAS_FRONTEND`, `N_auth_flows`, symfony package flags) originate in discovery (`project-discovery.xml`) → written to `docs/architecture/test-environment.md` + project manifest (`composer.json`).

Other related (NOT in scope — leave as-is): `scope_gate.go` `stackMarkers` (runtime co-scheduling), `investigator.go` `projectSanityInvestigator` (marker-file detection go.mod/composer.json/package.json/Makefile), `classifyScope`/`scopeProfile`.

### The gap
PHP rules are modular resolvable files; docker/db/frontend rules are frozen in one XML with `condition=` attributes. No extensible per-capability **rule catalogue**. That is what this work adds.

---

## 3. Design (C / A / A)

### 3.1 Rule catalogue — `cmd/tmux-cli/embedded/rules/`
Concern-separated; each file preserves the **exact rule body byte-for-byte** (behavior unchanged):
```
rules/
  manifest.yaml
  _base/        command-execution.md   (CMD-CONV, HTTP-CONV, HTTP-WAIT-CONV)
                goal-structure.md       (scope-derivation, validate-acceptance-mandate)
  docker/       runtime-frontload.md    (DOCKER-RUNTIME-FRONTLOAD)
  database/     ensure-stack.md         (ENSURE-STACK-CONV)
                e2e-environment.md      (E2E-ENV-CONV)
                e2e-side-effects.md     (E2E-SIDEFX-CONV — generic part)
                e2e-data-isolation.md   (E2E-DATA-ISOLATION-CONV)
  frontend/     node-tooling.md         (NODE-TOOL-CONV)
                e2e-artifacts.md        (E2E-ARTIFACT-CONV)
                e2e-auth-state.md       (E2E-AUTH-STATE-CONV)
  php-symfony/  e2e-side-effects.md     (mailer/messenger/http-client symfony specifics, split from E2E-SIDEFX)
```
"Separate concerns" cleanup example: `E2E-SIDEFX-CONV` splits — generic "isolate side effects" → `database/`, symfony mailer/messenger/http-client specifics → `php-symfony/`.

### 3.2 `manifest.yaml` — capability → condition → files
```yaml
packs:
  - {id: _base,         when: always,                              files: [command-execution.md, goal-structure.md]}
  - {id: docker,        when: "RUN_TARGET == docker",              files: [runtime-frontload.md]}
  - {id: database,      when: "HAS_DATABASE",                      files: [ensure-stack.md, e2e-environment.md, e2e-side-effects.md, e2e-data-isolation.md]}
  - {id: frontend,      when: "HAS_FRONTEND",                      files: [node-tooling.md, e2e-artifacts.md]}
  - {id: frontend-auth, when: "HAS_FRONTEND && N_auth_flows >= 1", files: [e2e-auth-state.md]}
  - {id: php-symfony,   when: "FRAMEWORK == symfony",              files: [e2e-side-effects.md]}
```

### 3.3 Go resolver — `internal/rules/` (deterministic half of option C)
- `Signals{Lang, Framework, RunTarget string; HasDatabase, HasFrontend bool; NAuthFlows int}`
- `Detect(projectRoot) Signals` — reuse `taskvisor.ResolveExecRuntime` for RUN_TARGET/frontend; parse `composer.json` for framework + symfony packages; parse `test-environment.md` for DB/auth signals.
- `Resolve(sig) []Pack` — evaluate each pack's `when` (tiny boolean-expr evaluator), return ordered `[]Pack` with resolved `.tmux-cli/rules/<pack>/<file>.md` paths.
- Exposed as CLI subcommand **`tmux-cli rules resolve`** → prints the resolved file-path list (agent reads these). *(Open question: CLI vs MCP tool — leaning CLI.)*

### 3.4 Materialization + wiring
- `//go:embed embedded/rules` in `session.go`; new `setup.WriteRules` (mirror `WriteTemplates`) → `.tmux-cli/rules/`.
- `task-plan-generate.xml`: **replace** the inline `<conventions>` block with **Step 0a**: "run `tmux-cli rules resolve`; Read each returned `.tmux-cli/rules/...md` and treat as BINDING conventions for all later steps." (agent-loaded half of option C).

### 3.5 Tests
- Resolver unit tests: signal combos → expected pack set (php-symfony+docker+db+frontend+auth; php API-only; go project; unknown stack).
- **No-rule-lost golden test**: every original `*-CONV` id + the two mandates appears in exactly one catalogue file.
- Manifest integrity test: every referenced file exists; no orphan files.

### 3.6 Scope boundary
- **Phase 1** = the `<conventions>` cross-cutting block (clean, 1:1 condition mapping, high value). **Shipped a38a4d2.**
- **Phase 2** = ADAPTED and redesigned in `rules-e2e-design.md` (2026-06-12): the per-step `condition=` branches are NOT extracted wholesale — survey showed three classes (capability signals / discovery-content predicates / control flow). What extracts is the condition *evaluation* (Signals + `rules resolve --signals` as single authority) and the stack-instructional *content* (dedup against ddd-conventions.md + code rules), while control flow stays in XML. Full E2E lifecycle (rule→goal injection, spec S9, execute/investigate/audit consumption, `rules lint`/`check`, ingestion skill) is designed there with phasing 2a–2e.

---

## 4. Open questions (awaiting decision before build)
1. **Resolver surface:** CLI subcommand `tmux-cli rules resolve` (preferred — planner can `Bash` it, no MCP churn) vs. new MCP tool `rules-resolve`.
2. **php-symfony split timing:** split mailer/messenger specifics out of `E2E-SIDEFX-CONV` now (cleanest separation, but the one place a battle-tested rule is *reworded* not moved verbatim), or keep `E2E-SIDEFX-CONV` intact in `database/` for phase 1 and split later.

---

## 5. Key file references
- `cmd/tmux-cli/embedded/commands/tmux/task-plan-generate.xml:36-57` — `<conventions>` block (extraction source)
- `cmd/tmux-cli/embedded/commands/tmux/task-plan-generate.xml:20` — `TEMPLATE_DIR` two-tier lookup
- `cmd/tmux-cli/session.go:47-51` — `go:embed` directives; `:1652` `runAutoSetup`
- `internal/setup/templates.go:9` — `WriteTemplates` → `.tmux-cli/templates/` (mirror for `WriteRules`)
- `internal/setup/setup.go:32` — Templates wiring in `setup.Run`
- `internal/taskvisor/execruntime.go:29` — `ResolveExecRuntime` parses `test-environment.md` (reuse for `Detect`)
- `internal/mcp/tools.go:600,648` — `buildTaskMessage` / `WindowsSpawnWorker` (delivery mechanism, context only)
