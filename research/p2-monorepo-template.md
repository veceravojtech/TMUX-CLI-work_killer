# P2 monorepo as the Symfony default + Vue/Twig frontend modes — design

**Date:** 2026-06-13
**Builds on:** the per-product rules catalogue (a38a4d2) and the rules-E2E
lifecycle (`research/rules-e2e-design.md`). Those generalized previo2's rules
into portable packs and deliberately kept previo-specific *topology* out of the
generator (rules-e2e §3: "tmux-cli never reads `docs/ai/code-rules/` directly;
embeds generalized adaptations").
**Decision (this doc):** for **php-symfony only**, intentionally REVERSE that
stance — generated Symfony projects should match previo2's **full DDD monorepo**,
which becomes the *only* php-symfony output (the flat single-package skeleton is
retired). Adds a generic Vue mode + an easy Twig-prototyping mode that previo2
has no portable equivalent of.

---

## 1. Current state and the gap

The Symfony generator emits a **flat, single-package** skeleton:
`src/<BC>/{Domain,Application,Infrastructure}` + `src/Share`, one `composer.json`,
deptrac between layers. Conventions live in
`cmd/tmux-cli/embedded/rules/php-symfony/ddd-conventions.md`; code-rules in the
same pack. The generated structure is a faithful *distillation* of P2's DDD
principles but diverges from P2's real structure on three axes (confirmed against
`previo2/contexts/AGENTS.md` + the live tree):

1. **Topology** — P2 is a multi-package monorepo (`contexts/` libs + `projects/`
   apps + `app/` entry + `packages/` shared), deptrac BETWEEN packages. The
   generator is single-package.
2. **Aggregate seam** — P2 keeps the domain aggregate Doctrine-free via a
   `Aggregate` + `AggregateData` (DAO, `#[AggregateDAO]`/`#[PersistentReference]`,
   `Collection<>`) + `AggregateDTO` triad. The generator collapses this to "entities
   in Infrastructure".
3. **Shared kernel** — P2's `contexts/previo/src` is a published-language context
   (`EventRecord`, `EventInterface`, `AbstractIntId`, `XxxQueryContract`/`XxxContract`).
   The generator's `src/Share` is just VOs/IDs.

Plus: P2 uses `.phtml`/Vue; the generator assumes Twig and has no Vue conventions
(the `PHP-FE-001..005` frontend rules were never adopted — they are `scope: previo`,
tied to P2's `packages/design-system`).

This design closes all of that for php-symfony.

## 2. Target structure (port of P2, generalized)

Source of truth to generalize from: `previo2/contexts/AGENTS.md` (strip
previo-proprietary identifiers, keep the patterns). Namespace root becomes the
generated project's vendor namespace (e.g. `App\` or a discovered vendor).

```
contexts/<bc>/
  src/Domain/<Module>/        Aggregate, AggregateData (DAO #[PersistentReference]),
                              AggregateDTO, <Module>RepositoryInterface, Command/
  src/Application/<Module>/   Query/ (read-only, own repo iface) · Command/ (handle()
                              atomic + actor ACL) · Exception/
  src/Bundle/{Domain,Application}/   infra impls: DoctrineXxxRepository, MysqlXxxQueryRepository
  app/<Module>/               controllers, CLI, message processors, adapters (framework-only layer)
  tests/{Unit,Integration,Resources}/
contexts/previo/src/          shared kernel: published language (EventRecord, EventInterface,
                              AbstractIntId), contracts (XxxQueryContract/XxxContract),
                              Share/ (Money, Email, DateRange, AbstractQuery/Paging/Ordering,
                              Command/Result)
projects/<app>/src/<Module>/  deployable apps composing contexts (controllers/wiring)
packages/<pkg>/               shared libraries (incl. packages/ui for Vue components)
composer.json (root, path repositories) · per-package composer.json · deptrac.yaml
(layer direction + package boundaries) · per-package CI
```

**Frontend modes** (Q4), chosen once at discovery:
- `vue` — SPA + `packages/ui` shared component lib + the generic Vue rules (§4).
- `twig` — server-rendered Twig, light scaffold for quick prototyping; only the
  "no business logic in views" rule applies.
- `none` — API-only.

## 3. The E2E lifecycle — surface by surface

### 3.1 Discovery + signals (Phase 1)
`internal/rules/rules.go`: add `FrontendMode string` to `Signals` (`""`=unknown);
`parseFrontendMode(testEnv)` reads an explicit `**Frontend:** vue|twig|none` line,
else derives from `has_frontend`. `HasFrontend` stays derived (`vue|twig`⇒true).
Add `FrontendMode *string` to the manifest `Condition` (rules.go:73) + a case in
`matches()` (rules.go:170) with **known-to-match** (stack-style) semantics. Dump in
`resolve --signals`. `task-plan-discover.xml` captures the mode and a **deployables/
packages inventory** (which BCs are libraries vs which `projects/<app>` compose
them) — P2's contexts-vs-projects split is a new discovery dimension. Default: one
`projects/api` wiring all contexts unless discovery finds more.

### 3.2 Manifest + packs (Phase 2)
`manifest.yaml`: repoint `php-symfony` at the rewritten monorepo conventions; add
`vue` pack (`when: {frontend_mode: vue}`) and `twig` pack (`when: {frontend_mode:
twig}`); re-gate the E2E `frontend`/`frontend-auth` packs on `frontend_mode: vue`.

### 3.3 Conventions rewrite (Phase 3 — the heart)
Replace `php-symfony/ddd-conventions.md` with: `monorepo-layout.md`,
`context-layers.md`, `shared-kernel.md`, `app-layer.md` (content per §2). Re-target
`php-symfony/code-rules.yaml` `applies_to` globs (`src/**` → `contexts/*/src/**`,
`contexts/*/app/**`, `projects/*/src/**`); restore automated signals (`PHP-TYPE-001`
back to `automated`; deptrac-backed package-boundary rules; new rules for the
DAO/`#[PersistentReference]` seam, shared-kernel contract usage, no-foreign-context
concrete import).

### 3.4 Generation shards (Phase 4 — largest surface)
`task-plan-generate/step-*.xml`: scaffold lays the monorepo skeleton (root
composer.json + path repos, per-package composer.json, deptrac package edges);
domain→`contexts/<bc>/src/Domain`, application→`.../Application`, infra→`.../Bundle`,
controllers→`contexts/<bc>/app` or `projects/<app>/src/<Module>`; auth/listeners/
messenger/error/middleware/api-docs/health→app layer; docker/cicd/dx/final-gates→
monorepo-aware. FE deliverables branch on `FrontendMode`.

### 3.5 Vue + Twig (Phase 5)
`rules/vue/code-rules.yaml`: generalized FE rules — **reuse-existing-shared-component**
(Q3 reuse intent, scoped to `packages/ui/**`), shared date helper, `is`-prefixed
default-false bool props (**automated** signal `:[\w-]+="(?:true|false)"`), SFC block
order (Props→Emits→logic), design tokens over ad-hoc CSS. `rules/vue/conventions.md`
connective doc. `rules/twig/conventions.md`: server-render layout + no-logic-in-views.
Scaffold emits `packages/ui` stub (vue) or a Twig templates tree (twig).

### 3.6 Tests, selftest, docs (Phase 6)
`internal/rules/*_test.go`, `rules_catalogue_test.go` golden ids, manifest-integrity
test; rewrite generation goal-content tests pinning `src/<BC>/` → monorepo paths
(largest churn); selftest the new automated signals; update `rules/SCHEMA.md`.

## 4. What does NOT change
- The **convention-not-cookiecutter** invariant: the planner lays down no files;
  worker agents build the monorepo from the rewritten conventions + the scaffold
  shard's acceptance criteria. Go owns matching/signals; agents own code.
- Daemon goal lifecycle, lanes, retry budgets — rules ride existing goal fields.
- previo2's own repo — untouched; generalize from `contexts/AGENTS.md`, never read it
  at runtime.

## 5. Risks
- **Test churn dominates** — replacing the flat skeleton breaks every golden test
  asserting `src/<BC>/` deliverable paths.
- Monorepo scaffolding (path repos, N composer.json, deptrac package edges) is a new
  generation capability and lengthens the scaffold/plan phase.
- Discovery gains a real new dimension (contexts vs projects vs packages).
- MCP staleness: `make install` does not refresh the running server — reinstall +
  restart; `.claude/commands/tmux/` is clean-slated on install.

## 6. Implementation phasing

| phase | content | touches |
|-------|---------|---------|
| 1 | FrontendMode signal + discovery (contexts/projects/packages split) | internal/rules/rules.go, task-plan-discover.xml |
| 2 | manifest repoint + vue/twig packs + E2E pack re-gate | rules/manifest.yaml |
| 3 | conventions rewrite (port P2) + code-rules globs/signals | rules/php-symfony/* |
| 4 | ~21 generation shards → monorepo paths + FrontendMode branches | task-plan-generate/step-*.xml |
| 5 | vue + twig packs/templates + packages/ui stub | rules/vue/, rules/twig/, templates/ |
| 6 | tests + selftest + docs (goal-content path rewrite) | internal/rules/*_test.go, generation tests, SCHEMA.md |

Spine: 1→2→3→4; 5 ∥ 4; 6 last. Phases 1–3 are the dependency core; 4 is the bulk;
6 carries the test churn.

## 7. Verification
- `go test ./...` green (rules, mcp, generation).
- `tmux-cli rules resolve --framework symfony --signals` shows FrontendMode;
  `rules match` returns monorepo-path payloads; `rules lint` passes incl. new
  automated fixtures.
- End-to-end: a small greenfield `/tmux:plan` per frontend mode (vue/twig/none);
  confirm the scaffold goal lays the monorepo skeleton, `deptrac analyse` passes
  across package edges, FE deliverables match the mode.
