# Problem summary — MCP (tmux-cli) + Web

All problems identified this session, with backend task ids. Two surfaces: **MCP** (tmux-cli
rules/commands/tooling) and **Web** (frontend/quality gates). `*` = filed this session.

## MCP — rule-pack gaps vs a real DDD monorepo (P2)

| id | sev | problem |
|----|-----|---------|
| 76 | crit | quality-gate template emits skeleton `src/` commands, unusable on a monorepo |
| 77 | crit | PHP code-rule validates omit PHPStan (the primary static gate) |
| 78 | warn | no gate validates `monorepo-component.json` on component scaffold |
| 79 | warn | PERS-001..004 dropped `db-validate` → review-only |
| 80 | warn | CFG-001 dropped `env-merge` / root `.env` check → review-only |
| 81 | warn | `taskvisor.integration_cmd` empty — no repo-wide gate at completion |
| 83 | info | command-execution never models component-scoped (`--component`) execution |
| 85 | warn | no rule forbids committed debug calls (`var_dump`/`dd`/`console.log`) |
| 86 | warn | no security convention in any pack (ACL / module access) |
| 87*| warn | `applies_to` globs anchored at `^src/` + `Infrastructure` layer name — no resolution of real roots/layer (`Bundle`) |
| 88*| warn | `App\` namespace prefix hardcoded — no path-mirror rule, no resolution |
| 89*| info | framework entry-point (`app/`) layer placement unmodeled |
| 90*| info | aggregate persistence-DAO vs readonly-DTO split unmodeled |
| 91*| info | published-language events (additive-only, snapshot not aggregate) — no rule |
| 92*| warn | cross-bounded-context access via published contracts — no rule |
| 93*| info | tech-stating repo / handler / query naming vocabulary unpinned |
| 94*| info | test-first ordering not enforced |
| 95*| info | test-layout conventions (marker, mothers, mirror tree) unpinned |
| 96*| info | SCHEMA + `rules/add` tell authors to embed live MR pointers (contradicts packs) |
| 112*| warn | no index-on-relation-column rule for no-FK schemas (gate `db.foreign_keys:false`) |
| 113*| warn | monolithic `code-rules.yaml` (20KB+) too big — split per category |

## MCP — new brownfield feature-dev command `/tmux:feature` *

Self-contained set (full design in #105 `payload.design_doc`). Chain F1→F7.

| id | stage | id | stage |
|----|-------|----|-------|
| 105 | F1 orchestrator skeleton | 109 | F5 test-strategy decision (TDD/Playwright) |
| 106 | F2 context ingestion | 110 | F6 phased goals + handoff |
| 107 | F3 arch recommendation | 111 | F7 e2e test + docs |
| 108 | F4 feature docs | | |

## MCP — process / tooling limitations *

- **Backend has no task retire path:** no `deny`/`archive`/`delete` by id, `task-claim` is
  id-blind, no un-claim. Blocks fail-and-recreate.
- **Outstanding cleanup:** old feature tasks **97–103** are superseded by 105–111 but can't be
  retired via tools — **deny/archive 97–103 on the backend.**

## Web / frontend

| id | sev | problem |
|----|-----|---------|
| 82 | warn | frontend quality-gate commands (jsf/eslint/stylelint/vitest/tsc) absent; vue pack rules-only |
| 84 | warn | `api` reporting block customer-configurable in settings TUI; make internal-only |
| 109* | warn | (FE half of test-strategy) Playwright E2E selection only when `has_frontend` + runner resolve, else HTTP fallback |

## Notes

- Several MCP gaps (#89/#92 app-layer/shared-kernel/monorepo) may be **partly addressed** —
  the committed php-symfony pack was refactored into `monorepo-layout.md` / `context-layers.md`
  / `app-layer.md` / `shared-kernel.md`, and `PHP-PERS-005` (typed-Id + `#[PersistentReference]`)
  already encodes the no-FK reference seam. Re-check against committed source before acting.
- P2 schema fact: **no DB foreign keys** (policy in `database/AGENTS.md`); relations are
  indexed id columns. tmux rules agree on the reference style (PERS-005) but miss the
  index-every-relation-column corollary (→ #112).
