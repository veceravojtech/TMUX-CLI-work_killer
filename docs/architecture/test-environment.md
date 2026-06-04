# ADR: Test-environment credentials for the unattended acceptance gate

Status: Accepted

## Context

The acceptance gate (`goal-001` / `gate-001`) validates the project by running
the test suite against a PostgreSQL test database. The suite reads its
connection parameters from `DB_*` environment variables (`DB_HOST`, `DB_PORT`,
`DB_NAME`, `DB_USER`, `DB_PASSWORD`).

Today those variables come from a human's exported shell environment. That makes
the gate **non-deterministic and impossible to run unattended**: on a fresh
checkout with no `DB_*` exported (e.g. CI, or the taskvisor daemon spawning a
supervisor), the gate either blocks waiting for credentials or risks hanging on
human input. There is no recorded decision for how the test environment obtains
its credentials, and neither a decision record nor a committed credentials file
exists.

The test database is an **ephemeral throwaway container** — the compose default
`stock_test` / `stock_test` user/password against `127.0.0.1:5432`. It holds no
production data and is recreated on demand. Its "password" therefore protects
nothing of value.

This ADR records how the gate obtains test credentials so it can run
deterministically and without human input.

## (a) Decision

Two mechanisms were considered. Both are documented here; the **preferred**
option is the recommended and accepted choice.

- **PREFERRED (accepted):** Commit a disposable, non-secret `.env.test` file at
  the repository root containing the compose-default throwaway credentials
  (`stock_test` / `stock_test`). The gate loads `.env.test` from the checkout, so
  it runs unattended on a fresh checkout with **no** `DB_*` exported and never
  prompts for input.

- **ALT (documented, not adopted):** Declare the test DB password an **external
  CI secret** — e.g. `STOCK_TEST_DB_PASSWORD`, owned by **ops / the repository
  maintainer** — injected into the environment by the CI runner. The C3
  env-config preflight (`evaluatePreconditions` in
  `internal/taskvisor/taskvisor.go`) detects its absence and emits
  `verdict=blocked, class=env-config, owner=ops` rather than hanging.

**Accepted choice: PREFERRED.** Commit `.env.test` with throwaway
`stock_test` / `stock_test` credentials.

## (b) Rationale

**Why preferred is accepted:**

- The test DB is a disposable throwaway container, so its password
  (`stock_test`) is **not a real secret** — committing it leaks nothing. The
  value is already the public compose default.
- It makes the gate fully **self-contained and deterministic**: a fresh checkout
  has everything it needs, with zero external configuration, zero CI-secret
  plumbing, and zero dependency on a human's exported shell.
- It removes a class of "works on my machine" failures where the gate passes
  locally (creds exported) but blocks in CI/daemon contexts (creds absent).

**Why the alt was not adopted (but remains valid for shared/real infra):**

- The alt keeps the credential **external** so it is never written to the repo.
  This matters only when the test DB is shared or backed by real infrastructure
  whose password is genuinely sensitive — which is **not** the case here.
- It adds operational coupling: the secret must be provisioned, owned, rotated,
  and kept in sync by ops, and any context lacking the injected secret
  (local runs, the taskvisor daemon) is blocked until it is supplied.
- For a throwaway container the alt buys no security and costs determinism, so it
  is documented for completeness and as the fallback should the test DB ever
  graduate to shared/real infrastructure.

## (c) Implementation

**Preferred (implemented here):**

- File: `.env.test` at the repository root.
- Contents (throwaway, safe to commit):

  ```dotenv
  DB_HOST=127.0.0.1
  DB_PORT=5432
  DB_NAME=stock_test
  DB_USER=stock_test
  DB_PASSWORD=stock_test
  ```

- The file carries a leading comment stating it is safe to commit and is not a
  real secret.
- `.env.test` must be **tracked**, not ignored. The repository's `.gitignore`
  contains a broad `*.test` pattern (intended for Go `go test -c` binaries) that
  would otherwise catch `.env.test`; a `!.env.test` negation was added so the
  file is trackable (`git check-ignore .env.test` exits non-zero). tmux-cli also
  manages `.git/info/exclude`; no exclude entry matches `.env.test`.

**Alt (documented only — no artifact created):**

- External CI secret name: **`STOCK_TEST_DB_PASSWORD`** (and, if needed,
  companion `STOCK_TEST_DB_USER` / host / port / name).
- Owner: **ops / repository maintainer**, responsible for provisioning and
  rotation.
- The CI runner injects the secret into the job environment; the C3 preflight
  (`evaluatePreconditions`, attached at the supervisor-spawn path
  `internal/taskvisor/taskvisor.go` `func (d *Daemon) dispatch`) classifies a
  missing secret as `blocked / env-config` with `owner=ops` and surfaces the
  remediation runbook rather than blocking on input. **Note:** C3 owns the
  authoritative `signal.json` / remedy emission; this ADR only points at that
  path and does not duplicate it.

## (d) Verification

In-repo artifact checks (this repository):

- `test -f docs/architecture/test-environment.md`
- `grep -Eq 'Decision|Rationale|Implementation|Verification' docs/architecture/test-environment.md`
- `grep -q stock_test .env.test` and the safe-to-commit comment is present
- `git check-ignore .env.test` exits non-zero (the file is tracked, not ignored)
- `.env.test` contains only throwaway creds (`127.0.0.1` + `stock_test`) — no
  production hostname or real secret

CI gate-determinism (out-of-repo — runs in `test-project` CI via
`test-project/.github/workflows/ci.yml`, **NOT** in `tmux-package/cli`; listed
here for traceability only). The step checks out fresh, scrubs all `DB_*` from
the environment, and runs the gate validation entrypoint non-interactively
(stdin closed, `</dev/null`). Named cases:

- **TestGateDeterminism_PreferredEnvTest** — fresh checkout, no `DB_*` exported,
  committed `.env.test` present, stdin closed → `verdict=pass`, no hang.
- **TestGateDeterminism_AltExternalSecret** — fresh checkout, external secret
  unset, no `DB_*` exported → `verdict=blocked, class=env-config` via the C3
  preflight.
- **TestGateDeterminism_NonInteractiveNoHang** — either option, gate run with
  stdin closed exits within the CI timeout, never prompts for or reads a
  human-exported shell var, and yields the same verdict on every fresh run.

These tests are **not** implemented in `tmux-package/cli`; no Go test is created
here for them.

## Adjacent risk (recorded, not acted on)

The out-of-repo CI verification above invokes a gate validation entrypoint
(conceptually `tmux-cli goal validate goal-001`). **This repository's CLI
currently exposes only `goal add / list / delete / reset / stop / skip / prune`
— there is no `validate` subcommand.** The exact validation entrypoint must be
confirmed by the test-project owner before wiring the CI gate step; this is out
of C9's edit scope but blocks the out-of-repo verification if unaddressed.
