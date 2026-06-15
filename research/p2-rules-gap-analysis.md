# tmux-cli rule packs vs Previo2 — gap analysis

**Question:** where do the tmux-cli rule packs **miss Previo2 (P2) logic**, and where do
they **need to be better** — focused on architecture, new code generation, namespaces,
naming, and code comments.

## Scope / method

Compared:

- `.tmux-cli/rules/php/code-rules.yaml` (generic PHP pack)
- `.tmux-cli/rules/php-symfony/code-rules.yaml` + `php-symfony/ddd-conventions.md`
- `.tmux-cli/rules/SCHEMA.md`

against P2's canonical sources:

- `previo2/contexts/AGENTS.md` (the DDD contract)
- `previo2/docs/ai/PREVIO2.md` (monorepo model + writing-code rules)
- `previo2/docs/docs/security.md` (ACL / module access)
- the real `contexts/*` tree (layer = `Domain/Application/Bundle`, ns = `Previo2\…`)

**Important framing.** The tmux packs are deliberately *generic* — the header of both
YAML packs says *"provenance identifiers are intentionally not embedded … adapted from a
production house-rules catalogue."* That production catalogue **is** P2. So a "gap" here is
P2 logic that **did not survive the generalization** into the packs — either (a) a P2
convention with no generic analogue in the packs, or (b) a generic rule whose *shape or
path globs* no longer fit P2's real topology.

---

## 0. Cross-cutting defect — the packs address a layout P2 doesn't have (HIGH)

`compileGlob` (internal/rules/coderules.go:104) anchors every `applies_to` pattern at `^`.
So `src/**/*.php` compiles to `^src/(?:.*/)?[^/]*\.php$` — it only matches paths that
**start with** `src/`.

P2 has **no top-level `src/`**. Code lives under:

| P2 location | what it is |
|---|---|
| `contexts/<name>/src/{Domain,Application,Bundle}` | DDD context library |
| `contexts/<name>/app/src` | per-context Symfony entry app |
| `projects/<name>/src` | user-facing project |
| `integrations/<name>/src` | third-party integration app |
| `packages/<name>/src` | reusable library |

Consequences if a pack is resolved against a P2-shaped tree:

1. **Every `src/**` glob matches zero files** — the rule is silently inert (check.go:87
   omits a rule when nothing matches). That's `PHP-TYPE-*`, `PHP-CTRL-*`, `PHP-DATE-*`,
   `PHP-NAME-*`, `PHP-TEST-001`, `PHP-ARCH-*`, `PHP-PERS-*`, `PHP-I18N-001`.
2. **The `Infrastructure` layer never exists.** P2's infra layer is **`Bundle`**
   (`contexts/*/src/Bundle`, split into `Bundle/Domain` + `Bundle/Application`). Every
   `src/**/Infrastructure/**` glob (`PHP-PERS-001..004`) is doubly dead: wrong prefix *and*
   a layer name P2 doesn't use.

This is the single biggest miss — not wrong *content*, wrong *addressing*. The packs
encode a single-app `src/<BC>/<Layer>` mental model; P2's logic lives in a monorepo with a
differently-named infra layer.

**Fix direction:** globs need a monorepo root alternation
(`{contexts,projects,integrations,packages}/*/{src,app/src}/…`) and `Infrastructure` →
`Bundle`. As a generic pack that can't hardcode P2 paths, the realistic fix is to make the
layer/root tokens *parameters of the discovery step* rather than literals.

---

## 1. Architecture

P2 conventions with no enforceable analogue in the packs:

- **Infra layer is `Bundle`, sub-split into `Bundle/Domain` (domain repo impls) and
  `Bundle/Application` (query repo impls).** The packs only know a flat `Infrastructure/`.
- **The monorepo / component model is entirely absent.** Component types
  (`package`, `fe-package`, `context`, `context-application`, `project`, `integration`,
  `monorepo`), `monorepo-component.json`, `composer p2:components`, and cross-component
  dependency direction have no rule. The packs assume one app.
- **The `app/` entry-point layer is unmodeled.** P2 puts controllers, `*CliCommand`
  (`#[AsCommand]`), message processors (`#[AsMessageProcessor]`), event listeners,
  external-system adapters, `Application/Security/` (`User` + `UserBuilder`), the `Previo1/`
  legacy bridge, and `DataChanges/` processors in `contexts/*/app/src` (mirrors
  `projects/*/src`). The packs fold controllers into `src/<BC>/` and have no notion of this
  layer — so any rule about *where framework entry points live* is missing.
- **Shared kernel `contexts/previo/src` (published language) is missing.** P2 rules:
  domain Events are additive-only (never remove/rename a field); each module defines an
  `XxxEventRecord` (lightweight readonly snapshot embedded in events — never the full
  aggregate) and an `XxxEventInterface`; shared aggregate IDs (`ReservationId`, `PropertyId`,
  extending `AbstractIntId`) live here and must never be redefined per-context. The packs'
  ddd-conventions mention `Share/` and cross-BC contracts in passing but encode none of the
  event/published-language rules.
- **Cross-context contracts are unmodeled.** P2: a context consumes another **only** via
  `XxxQueryContract` → returning `XxxContract` DTO interfaces, both living in
  `contexts/previo/src/Application/`; the consumer injects the contract, never the foreign
  concrete class. The closest pack rule is `PHP-ARCH-003` (domain query returns an aggregate)
  — related but not the contract system.
- **Aggregate anatomy is half-captured.** Packs say "aggregate has a `create()` factory and
  fires events" (good). They miss P2's concrete shape: ctor takes
  `(AggregateId, PropertyId, AggregateData)`; `AggregateData` is the Doctrine **DAO**
  annotated `#[AggregateDAO]`; `create(Id, PropertyId, Command)`; events via
  `EventPublisher::publish()`; child entities exposed **only as DTOs**. The **`PropertyId`**
  in the aggregate is central to P2's multi-tenant model and is entirely absent.

---

## 2. New code generation

("Builder" in recent commits is **Builder.io**, a CMS/content integration — *not* a code
scaffolder. So the gap is the **tmux planner's own generated skeleton**, not a P2 tool to
mirror.)

The planner generates `src/<BC>/{Domain,Application,Infrastructure}` with an `App\`
namespace. A module generated that way is wrong for P2 in five ways:

1. infra layer named `Infrastructure` instead of **`Bundle`** (+ its `Domain`/`Application`
   sub-split);
2. root namespace `App\` instead of **`Previo2\<Context>\`** (and the separate
   `Previo2\<Context>App\` for the entry layer);
3. no **DAO/DTO pair** on the aggregate (`AggregateData` + `AggregateDTO`);
4. no **`app/` entry layer** (controllers/CLI/processors land in the wrong place);
5. no **ACL line** in handlers and no `EventPublisher::publish()` in business methods.

Also: PREVIO2.md mandates **test-first** ("Write tests before business logic"). `PHP-TEST-001`
requires tests but says nothing about *ordering*, so generated code can satisfy the rule
while violating the P2 workflow.

---

## 3. Namespaces

P2 namespace contract (mirrors path, prefix `Previo2\`):

- context library: `Previo2\<Context>\<Layer>\<Module>\…`
- context app: `Previo2\<Context>App\<Module>\…`
- shared kernel: `Previo2\Previo\<Layer>\<Module>\…`

The packs' ddd-conventions example is `App\<BC>\<Layer>\<Module>`. No rule enforces
"namespace == path mirror," and the `App\` example will actively mislead generation for P2.
The layer token **`Bundle`** is not represented anywhere. This is the namespace counterpart
of §0.

---

## 4. Naming

`PHP-NAME-001..004` are good *generic* rules (concept names, no `Manager/Helper/Util`, no
opaque `$data/$tmp`, accurate names). But P2's **concrete naming vocabulary** is enforced
nowhere — it lives only as prose in ddd-conventions:

| P2 name shape | enforced? |
|---|---|
| `*Data` (Doctrine DAO) + `*DTO` (readonly input) aggregate pair | ✗ none |
| `Doctrine*Repository` (domain) / `Mysql*QueryRepository` (app query) | prose only |
| `*CliCommand`, `*Handler`, `*QueryService` + `*QueryRepositoryInterface` | prose only |
| `*QueryContract` / `*Contract`, `*EventRecord` / `*EventInterface` | prose only |
| controller's logic in a sibling folder named after it **minus** `Controller` | partial — `PHP-ARCH-006` is close but states a different rule |

None of these are pinned as a falsifiable code-rule, so a review agent has to *know* the
vocabulary rather than have the gate assert it.

---

## 5. Code comments  ← zero coverage

P2 has one explicit, trivially-automatable hygiene rule (`PREVIO2.md:48`):

> Do not commit debug calls: `var_dump`, `dd`, `dump`, `print_r`, `console.log`, etc.

**No pack has any rule for this**, despite it being the *ideal* `automated` rule (a single
`signal` regex, a ready bad/good fixture pair). There is also no rule on comment language,
no "prefer self-explanatory names over explanatory comments," and no "no commented-out
code." This is the most clear-cut, lowest-effort gap to close.

---

## 6. Security / ACL  (the most important "and so on")

P2 is multi-tenant (per-hotel `PropertyId`); access control is concrete and security-critical:

- **Projects:** `#[ModuleGranted(Module::X)]` on controller actions (P2SecurityBundle).
- **Context Application layer:** every command handler runs
  `$this->ACL->ensureAccessToProperty($actor, $command->propertyId);`
  (`ACLAssertionInterface`) before mutating.

The packs cover authorization only indirectly via `PHP-TEST-001` ("test ownership/authz
deny paths") plus a prose line in ddd-conventions. **No rule asserts the ACL call is present
in a handler, or the `ModuleGranted` attribute on a controller action.** For a security
invariant this should be a `must` rule with a `signal`, not left to test coverage.

---

## 7. Tests  (and so on)

ddd-conventions mirrors P2's test layout well (Unit/Domain vs Unit/Application, Integration,
test mothers). But only `PHP-TEST-001` (coverage + authz) is an *enforceable* rule. Not
pinned: `#[Test]` attribute (not `test*` prefix), test mothers `Test*` under `Resources/`,
mirror-the-source-structure, the Unit vs Integration split. These are conventions an agent
must remember, not gate-checked.

---

## Where existing rules need to be *better* (not just missing)

1. **Fix the globs (§0).** `Infrastructure` → `Bundle`; add the monorepo roots
   (`contexts/*/src`, `contexts/*/app/src`, `projects/*/src`, …). Until then `PHP-PERS-001..004`
   and the `Domain/`-scoped arch rules are inert on a P2-shaped tree.
2. **Upgrade `review` → `automated`/`mixed` where P2 hands you a fixture.** Almost every
   rule is `validate_kind: review` (leans on agent judgment). These have a mechanical `signal`:
   - debug calls — `\b(var_dump|dd|dump|print_r|var_export)\s*\(` / `console\.log`
   - union-return getter — `: *[A-Z][A-Za-z0-9_]*\|`
   - hand-formatted date in DTO — `->format\(['"]`
   - entity creation in controller — `new +[A-Z][A-Za-z0-9_]*\(` inside `*Controller.php`
   - direct persistence — `->(persist|flush)\(` outside `Bundle/`
3. **Drop the "origin → live MR" guidance.** `SCHEMA.md:35` and `:63-66`, plus
   `rules/add.xml` lines 19/33/51/64, tell authors to record the MR/note in `origin:`. That
   **contradicts the packs' own stance** ("provenance identifiers are intentionally not
   embedded") and couples a durable rule to an ephemeral, author-named MR note. The lint gate
   (`internal/rules/lint.go`) never checks `origin`/`adapted_from`, so this is pure convention
   — replace it with `adapted_from: <stable-id>` + an inline `examples.bad` fixture.

---

## Suggested new rules (stubs)

| id | category | sev | gist | kind |
|---|---|---|---|---|
| `PHP-CMT-001` | comments | must | no committed debug calls | automated (signal) |
| `PHP-SEC-001` | security | must | handler runs `ensureAccessToProperty`; project controller action carries `#[ModuleGranted]` | mixed |
| `PHP-ARCH-007` | architecture | should | infra layer is `Bundle` (`Bundle/Domain`+`Bundle/Application`), repos `Doctrine*`/`Mysql*QueryRepository` | review |
| `PHP-ARCH-008` | architecture | should | framework entry points (controllers/CLI/processors) live in the `app`/project entry layer, never in the context `src/` | review |
| `PHP-PERS-005` | persistence | should | aggregate has a `*Data` DAO + readonly `*DTO`; child entities exposed only as DTO | review |
| `PHP-EVT-001` | architecture | must | domain events are additive-only and carry an `*EventRecord` snapshot, never the full aggregate | review |
| `PHP-CONTRACT-001` | architecture | must | cross-context access only via `*QueryContract`/`*Contract` from the shared kernel; never inject a foreign concrete class | review |

---

## TL;DR

- The packs are a faithful but **lossy** generalization of P2. The losses cluster exactly
  where you asked: **architecture** (no `Bundle` layer, no monorepo/component model, no `app`
  entry layer, no published-language/contract rules, half an aggregate), **namespaces**
  (`App\` vs `Previo2\…`, no path-mirror rule), **naming** (P2's `*Data/*DTO/*Contract/
  *EventRecord/Doctrine*/Mysql*` vocabulary unpinned), and **comments** (zero coverage of the
  one debug-call rule P2 actually has).
- The highest-severity finding is structural (§0): the packs' `applies_to` globs are anchored
  at a top-level `src/` with an `Infrastructure` layer — **neither exists in P2** — so on a
  P2-shaped tree the rules don't fire at all.
- Cheapest wins: add `PHP-CMT-001` (debug calls) and `PHP-SEC-001` (ACL), and fix the
  `Infrastructure`→`Bundle` / monorepo-root globs.
