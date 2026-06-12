# Goal structure mandates (_base — always loaded)

Binding planning conventions. Each element below is BINDING on every
goal-creation call, exactly as if inlined in the planner's `<conventions>`
block (it was extracted from there verbatim).

<scope-derivation>
          The `scope` param is the goal's declared file footprint (array of path globs / namespace prefixes) that the daemon's disjoint-scope co-scheduling gate reads to decide whether two goals may run CONCURRENTLY under MaxGoals&gt;1. DERIVE it from the goal's Deliverables file paths: emit the distinct directory globs that cover every deliverable (e.g. deliverables under src/Booking/Domain/ ⇒ scope ["src/Booking/Domain/**"]; a single file ⇒ that file path). CONSERVATIVE CONTRACT: overlap OR an omitted/unknown scope ⇒ the daemon SERIALIZES the goals (never co-schedules), so it is always safe to omit when the footprint is unclear. An explicit author-provided `scope:` overrides derivation. The runtime reads ONLY this persisted field — it never parses goal.md — so scope MUST be passed here to enable safe co-scheduling.
</scope-derivation>

<validate-acceptance-mandate>
          STRUCTURED-FIELDS MANDATE (applies to EVERY goal-creation call in this flow — MCP `goal-create` tool or `tmux-cli taskvisor goal add` CLI alike; see the matching execution-rule). Every call MUST pass:
          1. `--validate` / `validate` param: >=1 concrete, PROJECT-RUNNABLE command — resolved against the detected LANG/FRAMEWORK stack (e.g. `vendor/bin/phpstan analyse src/Booking/`, `go test ./internal/booking/`, `npx playwright test tests/e2e/booking.spec.ts`), NOT prose. A sentence describing what to check is not a validate entry; only a command the daemon can execute verbatim in the project is.
          2. `--acceptance` / `acceptance` param: >=1 criterion.
          3. `--scope` / `scope` param whenever the goal's file footprint is known (path globs per &lt;scope-derivation&gt; above). An empty/omitted scope ⇒ the goal SERIALIZES against ALL concurrent goals (the daemon's DisjointReadySet gate co-schedules only provably disjoint scopes) — scope is the price of parallelism. Omit only when the footprint is genuinely unknown (safe, but serial).
</validate-acceptance-mandate>
