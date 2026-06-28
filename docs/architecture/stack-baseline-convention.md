# Stack Baseline convention — per-worktree migrate command

**Audience:** operators of a MANAGED project that taskvisor drives in **docker**
run-target mode (`docs/architecture/test-environment.md` declares
`Run Target: docker`). This reference does NOT apply to tmux-cli's own checkout,
which is a local/Go project — its per-worktree compose stack is never engaged
(`worktreeStackEnabled()` is false in local mode).

This is operator-copy reference material: declare the field in **your managed
project's** `docs/architecture/test-environment.md`. It is intentionally NOT
added to tmux-cli's own `test-environment.md` (that file is a credentials ADR for
this repo, not a docker-runtime contract).

## Why the field exists

When taskvisor runs a goal in an isolated git worktree under `MaxGoals > 1` (or a
git project), it brings up the goal's OWN compose stack — project name
`taskvisor-<goalID>` — with `cwd = the worktree`, so the validator's commands
run against the goal's uncommitted edits instead of the
operator's MAIN stack (the task-275 fix). That per-worktree stack gets a **fresh,
empty** `db-data` volume on every `up`.

An empty database red-lines a DB-touching goal's validation for an **infra**
reason (no schema), not a code reason — exactly the class of false-fail the
worktree-compose work eliminates. The **Stack Baseline** command is the opt-in
hook that migrates that fresh per-worktree DB to a usable baseline **before**
the validator's commands ever touch it.

## Where to declare it

In your managed project's `docs/architecture/test-environment.md`, add a line
whose key (case-insensitively) contains **`Stack Baseline`** or
**`Baseline Command`**, followed by `:` and the command:

```markdown
## Stack Baseline

**Stack Baseline:** bin/console doctrine:migrations:migrate -n
```

The `Baseline Command:` spelling is an accepted alias for the same field:

```markdown
**Baseline Command:** bin/console doctrine:migrations:migrate -n
```

## Exact parse contract

The daemon reads this field via `stackBaselineCmd`
(`internal/taskvisor/dispatch.go`), which opens the managed project's
`docs/architecture/test-environment.md` and delegates the parse to
`resolveBaselineCmd` (`internal/taskvisor/composestack.go:126`). The documented
rule below is **byte-consistent** with that parser — keep it in sync if the
parser changes:

1. **Key match — case-insensitive, substring.** The first line whose
   lowercased text contains `stack baseline` **or** `baseline command` is the
   candidate. Casing of your heading (`Stack Baseline`, `STACK BASELINE`, …) does
   not matter.
2. **Value — whole remainder after the FIRST `:`.** The value is everything
   after the first colon on that line. The split is on the *first* colon only, so
   a colon **inside** the command — e.g. `doctrine:migrations:migrate` — is
   preserved verbatim. The value is the WHOLE remainder (multi-word commands with
   flags are kept intact), not just the first token.
3. **Trim set.** Leading/trailing `*`, `_`, backtick (`` ` ``), space, and tab
   are stripped from the value — so Markdown emphasis around the key
   (`**Stack Baseline:** …`) and code-fencing the value
   (`` Stack Baseline: `bin/console …` ``) both parse cleanly.
4. **First non-empty match wins.** If a matching line's value is empty after
   trimming, the parser keeps scanning for the next matching line; the first
   matching line with a non-empty value is returned.
5. **Empty / absent ⇒ SKIPPED.** If no matching line exists (or every match is
   empty), `resolveBaselineCmd` returns `""` and the migrate step is **silently
   skipped**. The mechanism is opt-in and project-agnostic by design — no
   command is ever assumed.

### Worked example

The line

```markdown
**Stack Baseline:** bin/console doctrine:migrations:migrate -n
```

parses as follows: the lowercased line contains `stack baseline` (match); the
first `:` is the one in `**Stack Baseline:**`; the remainder is
`** bin/console doctrine:migrations:migrate -n`; trimming the `*`/space cutset
from the ends yields the resolved command:

```
bin/console doctrine:migrations:migrate -n
```

The two internal colons in `doctrine:migrations:migrate` survive because only the
first colon splits.

## Behavior: set vs unset

- **Set** (non-empty value): after `docker compose -p taskvisor-<goalID>
  -f <base> -f <override> up -d` succeeds, the daemon runs the command against
  the per-worktree app service via:

  ```
  docker compose -p taskvisor-<goalID> -f <base> -f <override> \
    exec -T <appSvc> sh -c '<your Stack Baseline command>'
  ```

  `<appSvc>` is the app/PHP service resolved from your `test-environment.md`
  (`App Service:` / `Runtime Container:`), never hardcoded. This runs **after**
  `up -d` and **before** the validator's commands touch the fresh `db-data` volume. A
  non-zero exit (or exec error) **halts the dispatch** as an infra/ops fault — it
  never falls back to validating against the operator's main stack and never
  charges a code-retry cycle (`ComposeStack.Up`, `composestack.go:164`;
  `Daemon.bringUpWorktreeStack`, `dispatch.go`).

- **Unset** (field absent or empty): `BaselineCmd == ""`, so `ComposeStack.Up`
  returns right after `up -d` with **zero** exec calls — the stack comes up
  un-migrated. Use this only for projects whose validate path needs no migrated
  schema.

## Caveats

- **Docker mode only.** The field is read on every dispatch but only acted on
  when the per-worktree stack engages, i.e. `Run Target: docker`. In local mode
  it is inert.
- **Put the field on its own line, above any prose.** Because the key match is a
  substring on the whole line and the **first** matching line wins, a sentence
  elsewhere in `test-environment.md` that merely mentions "baseline command" can
  shadow the real field if it appears first. Keep the declaration on a dedicated
  line and avoid the trigger phrases in surrounding prose.
- **The value is `sh -c`-evaluated** inside the app container, so it runs through
  a shell — keep it a single self-contained command (chain with `&&` if needed).

## Source of truth

- `internal/taskvisor/composestack.go:126` — `resolveBaselineCmd`, the parser
  this document mirrors. **This doc must stay byte-consistent with it.**
- `internal/taskvisor/dispatch.go` — `stackBaselineCmd` (reads the managed
  project's `docs/architecture/test-environment.md`) and
  `bringUpWorktreeStack` (gates on docker run-target, halts dispatch on Up error).
- `internal/taskvisor/composestack.go:164` — `ComposeStack.Up`, which runs the
  baseline via `exec -T <appSvc> sh -c <cmd>` after `up -d`.
- `docs/plans/worktree-compose-validation.md` — the per-worktree compose feature
  this convention completes.
