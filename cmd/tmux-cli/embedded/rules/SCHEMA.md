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

## code-rules YAML schema (per rule)

Required: `id`, `category`, `scope` (generic | project), `severity`
(must | should — `must` cannot be waived), `title`, `rule`, `why`,
`applies_to` (path globs), `acceptance` (Given/When/Then list), `validate`
(list), `validate_kind` (automated | review | mixed), `phase`.

Optional: `adapted_from` (source rule ID when adapted from another
catalogue), `origin` (provenance for project-local rules, e.g. an MR note),
`detect`, `fix`, `autofix` (safe | structural | none), `refs`,
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
ready-made `examples.bad` fixture; record the MR/note in `origin`), drop the
file in `local/code-rules/`, and it resolves from the next plan on.

## Editing embedded packs (this repo)

- Convention bodies under `_base/`, `docker/`, `database/`, `frontend/`,
  `frontend-auth/` were extracted VERBATIM from task-plan-generate.xml's
  `<conventions>` block; the catalogue golden test pins every rule id.
- Keep `manifest.yaml` in sync — the manifest-integrity test fails on
  missing or orphaned files.
- New language/framework packs: add a pack dir + manifest entry keyed on
  `lang`/`framework`; generic rules go in the language pack, framework
  specifics in `<lang>-<framework>`.
