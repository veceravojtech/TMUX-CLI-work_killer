# Monorepo layout (pack: php-symfony)

This pack targets a **multi-package DDD monorepo**, not a flat single-package
skeleton. The repository is a set of composer path-packages wired by a root
`composer.json`, with deptrac enforcing dependency direction **between packages**
as well as between layers. Namespace root is the project's vendor namespace
(shown here as `App\`); substitute the discovered vendor.

## Top-level topology

```
contexts/<bc>/        a bounded-context LIBRARY (no HTTP kernel of its own)
  src/Domain/<Module>/        pure business model — no Doctrine, no framework
  src/Application/<Module>/   use cases — Query/ (read) + Command/ (write) + Exception/
  src/Bundle/{Domain,Application}/  infrastructure impls (the ONLY layer touching Doctrine/MySQL)
  app/<Module>/               framework layer — controllers/CLI/processors (see app-layer.md)
  tests/{Unit,Integration,Resources}/
contexts/previo/src/  the SHARED KERNEL context — published language + contracts (see shared-kernel.md)
projects/<app>/src/<Module>/  a DEPLOYABLE app composing one or more contexts (wiring + controllers)
packages/<pkg>/       shared libraries reused across contexts/projects (e.g. packages/ui)
composer.json         root: path repositories pointing at every package
deptrac.yaml          layer direction + package boundary edges
```

A **context** is a library: it owns a slice of the domain and exposes its
capabilities through the shared kernel's contracts. A **project** is a deployable
that composes contexts into a running app (its module roots hold only thin
controllers — see context-layers.md and app-layer.md). A **package** is a plain
shared library with no domain ownership.

## The Bundle layer is the infrastructure layer

There is no `Infrastructure/` folder. A context's concrete, technology-bound code
lives under `src/Bundle/`:

- `Bundle/Domain/<Module>/` — Doctrine/MySQL implementations of the domain
  `RepositoryInterface` (e.g. `DoctrineOrderRepository`, `MysqlOrderRepository`).
- `Bundle/Application/<Module>/` — read-side implementations of the Application
  query repository interfaces (e.g. `MysqlOrdersQueryRepository`).

`Bundle/Domain` and `Bundle/Application` are the **only** layers permitted to
import Doctrine, the DBAL, or any storage SDK. Domain and Application stay
technology-free so they remain unit-testable with in-memory doubles.

## Dependency direction (deptrac enforces this)

Within a context the layers form a strict chain — a lower layer never imports a
higher one:

```
Domain  ←  Application  ←  Bundle  ←  app
```

Across packages, deptrac adds **package edges**: a context may depend on the
shared kernel (`contexts/previo/src`) and on `packages/*`, but never on another
context's internal `Domain`/`Application`. Cross-context collaboration is allowed
only through a published contract from the shared kernel. A `projects/<app>`
deployable may depend on the contexts it composes plus the shared kernel; a
context never depends on a project.

```yaml
# deptrac.yaml (sketch) — package + layer edges
ruleset:
  Domain: []                      # depends on nothing
  Application: [Domain]
  Bundle: [Domain, Application]
  App: [Domain, Application, Bundle, SharedKernel]
  Context: [SharedKernel, Packages]   # never another Context's internals
```

## Why a monorepo, not a single package

- Each context is **independently buildable and testable** — its own
  `composer.json`, its own CI, its own deptrac slice.
- The package graph *is* the architecture: an illegal cross-context import is a
  compile-/analysis-time failure (`vendor/bin/deptrac analyse`), not a code-review
  guess.
- Shared concepts live in exactly one place (`contexts/previo/src` for domain
  language, `packages/*` for cross-cutting libraries), so duplication can't drift.
- **Place a module in the context that owns its concept.** Author a module's read
  models, query services and repositories in the bounded context that ALREADY holds
  the `Application/<Concept>` module for its dominant aggregate / `*Id` / DTO (e.g.
  `Guest` under customer, `Invoice` under invoicing, `Partner` under crm) — never in
  whatever context is convenient to build under. deptrac green does not excuse a
  misplaced concept: it checks dependency direction only (see PHP-ARCH-017).

The code rules in this pack glob onto these paths via the discovery-resolved
`{src}`/`{infra}` tokens (see `../SCHEMA.md`), NOT hardcoded prefixes: `{src}`
resolves to the context library + framework-layer + deployable + package source
roots (`contexts/*/src`, `contexts/*/app/src`, `projects/*/src`, `packages/*/src`)
and `{infra}` to the infrastructure-layer dir (`Bundle`). The `## Layers` section
of `docs/architecture/layout.md` is authoritative for this monorepo topology (a
path-package monorepo's root `composer.json` cannot enumerate per-context source
roots). Project-root `templates/**` / `config/**` / `.env` stay literal — they
carry no source-root semantics.
