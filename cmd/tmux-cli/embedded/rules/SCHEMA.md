# Rule catalogue — schema & maintenance

`.tmux-cli/rules/` is materialized from the tmux-cli binary at setup and
resolved per project by `tmux-cli rules resolve`. Two species of rules live
here — keep them separate:

| kind          | audience                          | format                          |
|---------------|-----------------------------------|---------------------------------|
| `conventions` | the planning agent (BINDING)      | `.md` carrying `<rule>` elements |
| `code-rules`  | spec / implementation / review    | `.yaml` catalogue + `.md` docs   |

## Resolving

```
tmux-cli rules resolve [--kind=convention|code-rules] [--lang=php] [--framework=symfony] [--json]
```

Prints one project-relative file path per line (stderr carries warnings).
`--lang`/`--framework` pass the discovery session state (LANG/FRAMEWORK) —
they beat filesystem detection and are how greenfield projects (no manifest
file yet) resolve their stack packs.

Unknown capability signals (missing/unparseable discovery docs) load their
packs conservatively with a warning. Unknown stack signals load nothing
stack-specific: wrong-stack rules misdirect rather than protect.

## Frontend modes & stack packs

Two axes select packs beyond the always-on `_base`: the **frontend mode** and
the **language/framework stack**. Both are *stack-style* signals — they must be
KNOWN to match (an unknown signal loads nothing rather than guessing wrong; see
the matching-asymmetry note in `manifest.yaml`).

**`frontend_mode: vue | twig | none`** is the per-frontend-mode signal
(goal-034). It gates the frontend convention packs and the framework-specific
front-end code rules:

- **`vue`** — a Vue SPA front-end. Loads the `frontend` / `frontend-auth`
  convention packs (node tooling, e2e artifacts, auth-state reuse) **and** the
  `vue` pack, which carries both `conventions` *and* an automated `code_rules`
  catalogue — e.g. VUE-PROP-001 requires boolean props to be `is`/`has`/`can`-prefixed
  and default to `false` (an `automated` signal with `examples.{bad,good}`).
- **`twig`** — a server-rendered Twig front-end. Loads the `twig` pack, which is
  **conventions-only** (no `code_rules`): there is no SPA build/test surface to
  lint, so it ships binding planner conventions and nothing more.
- **`none`** — no front-end packs load.

**`lang: php` + `framework: symfony`** selects the `php-symfony` pack, which
targets a **P2 multi-package DDD monorepo** (not a flat `src/<BC>` skeleton). Its
conventions — `monorepo-layout`, `context-layers`, `shared-kernel`, `app-layer`
— describe the layout the planner emits:

- `contexts/<bc>/src/{Domain,Application}` — the bounded-context library
  (framework-free), with `contexts/<bc>/src/Bundle/{Domain,Application}` as the
  **infrastructure layer** (the only layer touching Doctrine/MySQL — there is no
  `Infrastructure/` directory) and `contexts/<bc>/app/` as the framework
  entry-point (controllers/CLI/processors).
- `contexts/previo/src` — the **shared kernel** context (published language +
  contracts), the only legal cross-context touchpoint.
- `projects/<app>/` — deployables composing one or more contexts; `packages/<pkg>`
  — shared libraries with no domain ownership.

The `php-symfony` code rules glob onto `contexts/**`, `projects/**`, and
`packages/**` (e.g. PHP-TYPE-003's automated `Assert\Uuid|Assert::uuid` UUID
check over `contexts/*/src/**` and `contexts/*/app/src/**`), so an illegal
cross-context import or a layering breach is an analysis-time failure, not a
review guess.

## code-rules YAML schema (per rule)

Required: `id`, `category`, `scope` (generic | project), `severity`
(must | should — `must` cannot be waived), `title`, `rule`, `why`,
`applies_to` (path globs), `acceptance` (Given/When/Then list), `validate`
(list), `validate_kind` (automated | review | mixed), `phase`.

`applies_to` globs may carry two **discovery tokens** that decouple the rule
from a hardcoded source layout, resolved once at load time before matching:

- `{src}` — the project's source-root globs. Expands to one glob per resolved
  source root, so a rule routes to wherever code actually lives (top-level
  `src/` greenfield, or `contexts/*/src`, `projects/*/src`, … in a monorepo).
- `{infra}` — the infra-layer directory name (greenfield `Infrastructure`; a P2
  monorepo's `Bundle`).

The Layout is resolved (read-only, never interactive) in precedence order:
`docs/architecture/layout.md` `## Layers` (authoritative) → `composer.json`
`autoload.psr-4` directory values → the greenfield default
`{["src"],"Infrastructure"}` (with a warning that discovery should ASK). A glob
carrying NEITHER token passes through byte-unchanged, so a flat-`src` project
behaves exactly as before tokenization.

Optional: `adapted_from` (source rule ID when adapted from another
catalogue), `origin` (a non-identifying one-line description of where the rule
came from — record durable provenance via `adapted_from` (a rule id) plus an
inline `examples.bad` fixture, never a live MR/note URL or author-named
pointer), `detect`, `fix`, `autofix` (safe | structural | none), `refs`,
`depends_on_rules`, `signal`, `examples` ({bad, good} snippets).

Falsifiability contract (enforced by the embedded-catalogue selftest):

- `automated` rules MUST carry `signal` + `examples`; the signal regex must
  match `examples.bad` and must NOT match `examples.good`. A check that
  cannot go red on its own bad example is rejected.
- `review` rules: every validate line starts with `review:` — they never
  borrow a green `phpstan`/`eslint` run they don't earn.
- `mixed` rules combine both: command lines that genuinely check the rule
  (e.g. `deptrac` for a layering rule) plus `review:` lines for the rest.

## Extending per project (the growth path)

Embedded packs are CLEAN-SLATED on every setup — never edit them in place.
Project-local rules go under:

```
.tmux-cli/rules/local/conventions/*.md     # extra binding planner conventions
.tmux-cli/rules/local/code-rules/*.yaml    # extra code rules (same schema)
```

`local/` is always included by resolve and never touched by setup. When a
local rule reuses an embedded rule's `id`, the local definition wins —
that's how a project tightens or overrides a default.

Ingesting review feedback (GitLab MRs etc.) into rules is a per-project
workflow: distill the feedback into the schema above (the MR comment is a
ready-made `examples.bad` fixture; set `adapted_from` to a stable rule id and
keep `origin` a non-identifying one-line note — never embed the live MR/note
URL or iid, mirroring the packs' "provenance identifiers are intentionally not
embedded" stance), drop the file in `local/code-rules/`, and it resolves from
the next plan on.

## Editing embedded packs (this repo)

- Convention bodies under `_base/`, `docker/`, `database/`, `frontend/`,
  `frontend-auth/` were extracted VERBATIM from task-plan-generate.xml's
  `<conventions>` block; the catalogue golden test pins every rule id.
- Keep `manifest.yaml` in sync — the manifest-integrity test fails on
  missing or orphaned files.
- New language/framework packs: add a pack dir + manifest entry keyed on
  `lang`/`framework`; generic rules go in the language pack, framework
  specifics in `<lang>-<framework>`.
