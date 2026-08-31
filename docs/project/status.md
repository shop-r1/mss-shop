# Project status

Last verified: 2026-09-01

## Confirmed decisions

- Admin foundation: `mss-boot-admin` Distribution `v1.3.7`, consumed as an
  exact backend/frontend dependency without forking MSS core.
- Release qualification: both Host manifests consume the published stable
  1.3.7 channel coordinated at commit
  `77b53d41092741eac62fa6418c0bdbf87413c7cd`.
- Admin topology: one tenant control plane and one mall management runtime per
  tenant. The current first milestone has one tenant, so it needs one of each.
- Tenant isolation: every mall runtime is configured with one immutable tenant
  identity and one core/business schema pair. There is no request-time schema
  switch.
- Storefront: a separate `/app/v1` API serves H5 and WeChat Mini Program.
- Mobile: classic uni-app with Vue 3, Vite, TypeScript and Pinia. H5 and
  `mp-weixin` are phase-one targets; App is deferred.
- Languages: all new surfaces are internationalization-ready. The initial
  complete locale set is Simplified Chinese and English.
- Engineering memory: constraints, architecture, ADRs, runbooks, Skills and
  verified MCP configuration live in the repositories and are reviewed with
  code.

## Present in this repository

- A runnable MSS 1.3.7 Thin Host proof of concept at the repository root.
- Final MSS 1.3.7 Thin Hosts under `apps/tenant-platform` and
  `apps/mall-platform`, each with its backend and Admin Web in one directory.
- A generated tenant desired-state Admin module with bilingual UI. Its delete
  action is a soft archive and never destroys tenant resources.
- A handwritten forward constraint migration that permits repeated empty
  optional WeChat AppIDs but rejects duplicate configured AppIDs before the
  tenant migration is marked ready.
- The authoritative `/app/v1` OpenAPI, JSON schemas and bilingual bootstrap
  examples.
- A runnable storefront API vertical slice with strict static configuration,
  exact Host/AppID bindings, weighted locale negotiation, stable errors and
  public-only response DTOs.
- In-memory lifecycle/reconciler and worker inbox implementations covering
  idempotent convergence, retry, suspend/resume, CAS and tenant-scoped message
  deduplication. They are simulation/test code, not production drivers.
- MSS-generated project contracts, seven MSS workflow Skills and two R1Shop
  project Skills.
- Versioned target architecture, migration plan, i18n policy and project Skill.
- A project-scoped `mss-mcp` connection whose wrapper rejects any version other
  than 1.3.7.
- A versioned 54-table legacy data contract and 31-scenario business acceptance
  matrix, with an executable 11-table control/shared and 43-table mall split.
- A mall Admin compatibility UI for all 43 tenant-owned legacy resources. It
  includes list/search/paging, detail, sensitive-field handling, stable error
  states and complete `zh-CN`/`en-US` catalogs. Every resource is currently
  read-only. This is a compatibility surface, not completion of the historical
  order, inventory, payment, wallet, promotion or import workflows.
- A fixed-binding mall compatibility backend for the same 43 resources. It
  enforces the immutable MSS/legacy tenant scopes, fully qualified allow-listed
  tables, inherited tenant guards, explicit detail capabilities, sensitive
  field redaction and both component/API policies. All 43 resources reject
  generic create, update and delete at the authorization and repository layers
  until each resource has
  recovered legacy validation, relationship/tenant constraints, model-hook
  effects and deletion semantics through a dedicated qualified workflow.
  `system_configs.metadata` is recursively redacted and excluded from search,
  filtering and sorting because its nested secrets must not become a
  result-count oracle.
- A tenant-platform shared-catalog backend and Admin UI for the eight global
  legacy resources. All eight are read-only for the same qualification reason;
  no shared-catalog resource currently advertises generic mutation capability.
- A flat bilingual compatibility-error contract shared by both platforms. It
  includes stable code/key/fallback fields and reduces old or malformed nested
  responses to React-safe strings.
- A guarded, forward-only local SQLite UI fixture for mall-platform. It creates
  the 43 reviewed table shapes and ten non-sensitive demo rows without
  `AutoMigrate`, rejects remote/escaped/incompatible targets, is idempotent and
  is explicitly excluded from migration or system-acceptance evidence.
- An MSS-generated migration-checkpoint module used only as a review ledger,
  plus a handwritten Feature contract for fixed legacy schema compatibility.
- A repository Skill and contract tests that keep the table manifest, owner
  counts, acceptance matrix, implementation status, both frontend catalogs,
  MCP registration and project-memory source paths in sync with code.
- Forward-only core migrations and a minimal Host runtime facade that make the
  authorized business menus bilingual without changing MSS core. Stable,
  dot-free root tokens survive MSS 1.3.7 name normalization; source-contract
  tests ensure both Host facades continue enabling dynamic-menu locale support.
- A bounded in-app-browser report for the mall local compatibility surface. It
  records the executed login, list, detail, empty-search, composite-key and
  bilingual checks, plus the post-correction absence of mutation controls on
  all four formerly generic-writable mall resources, while keeping all 31
  business acceptance scenarios open.
  Its create/update step was exploratory evidence against the superseded
  writable implementation and is not evidence of a current write capability.

## Not implemented yet

- A persistent control-plane repository, leases or observed-status integration
  between the tenant Admin module and reconciler.
- The authoritative normalized Host/AppID binding repository between raw Admin
  desired state and storefront serving. The current static serving directory
  already normalizes exact bindings and fails closed on duplicates.
- Real PostgreSQL role/schema drivers, secret storage, Kubernetes resources or
  a persistent worker queue/inbox.
- Dedicated order, inventory, payment, wallet, promotion, import/export and
  other historical side-effect workflows. Generic resource access does not
  satisfy their business acceptance scenarios.
- Per-resource mutation qualification for the 43 mall and eight shared-catalog
  compatibility resources. Writes remain disabled until old validation,
  relationships, tenant scope, hooks, authorization and deletion semantics are
  restored and accepted for that resource.
- Customer authentication, storefront catalog/cart/checkout and payment
  execution beyond the existing contract-first bootstrap slice.
- Legacy identity conversion (`tenants`, `users`, `roles`), warehouse data
  scopes, or any legacy business data migration.
- Any development-cluster rollout, production migration or cutover.

## Next milestone and acceptance criteria

Rehearse one tenant in a dedicated development database: persist desired and
observed state, implement the reconciler's PostgreSQL schema/role steps, and
bind one mall runtime to its fixed core/business schema pair. Acceptance needs
idempotent retry and isolation tests in disposable Kubernetes Pods. No
production write is part of that milestone.

## Verification evidence

Verified locally on 2026-09-01:

- `GOTOOLCHAIN=go1.26.6 mss doctor --strict` reported ready with exact MSS,
  Go, Node and pnpm versions.
- The root proof and both final Thin Hosts completed independent strict doctor
  and Skill validation with the official 1.3.7 tools. Both final Hosts passed
  full verification after the compatibility backends, UIs and local fixture
  were added. Root full verification remains intentionally split into
  explicit backend/frontend jobs because 1.3.7 still discovers nested Host
  modules against the root specification path.
- Root platform tests, race checks, vet, storefront build and architecture
  boundary checks passed.
- The tenant module passed deterministic 1.3.7 generation check, including its
  generated presentation registry.
- The mall compatibility backend passed its full package tests, `go vet` and
  focused race checks for fixed binding, qualified legacy access and per-action
  authorization. The tenant shared-catalog backend passed the same three
  validation levels.
- The mall Admin Web compatibility surface passed 9 test files / 33 tests,
  Biome/Umi lint, and a production build containing all 43 mall legacy routes.
  The tenant Admin Web passed 7 test files / 26 tests, lint and a production
  build containing all eight shared-catalog routes. Each lint reports one
  non-blocking unused-hook warning in an MSS-generated page; the qualified
  specification workarounds and warning are recorded in
  `docs/tooling/mss-1.3.7-generation-notes.md`.
- `go test ./contracts` proved the 54-table and 11/43 owner counts, the 31-row
  acceptance baseline, safety flags, required memory/MCP source paths and exact
  43-resource mall plus eight-resource tenant frontend projections.
- `tools/check-project-memory.sh` passed the executable memory contracts,
  repository Skill validation and whitespace check.
- The local mall UI fixture passed full Host backend tests/vet and focused race
  checks; its tests prove 43-table completeness, ten-row idempotent seeding,
  readiness and rejection of DSNs, escaped paths, symlinks, non-SQLite files
  and incompatible existing relations.
- The mall compatibility UI passed the bounded in-app-browser smoke review in
  `docs/acceptance/mall-local-browser-acceptance.md`. A fresh review-time tab
  rendered the bilingual authorized menu and two-row display-category list
  with zero browser warning/error entries. The earlier local
  display-category create/update was executed before the all-read-only safety
  correction; it exposed an incomplete generic-write contract and is retained
  only as superseded exploration evidence. It does not pass a current write
  capability. Delete, a browser-level denied role, tenant-platform browser
  review and all dedicated workflow scenarios remain open; the accepted
  business-scenario count is still zero.
- The mobile repository's locked contract check, TypeScript check and both H5
  and `mp-weixin` builds passed against this implementation.
- The repository-scoped MCP wrapper completed a stdio EOF smoke test against
  the official `mss-mcp v1.3.7` binary.

No development or production PostgreSQL legacy-data migration, Kubernetes
operation, deployment or production write was performed. Verification used
only forward MSS core metadata migrations and an ignored local SQLite fixture;
neither is system-acceptance or data-migration evidence. Existing shared
databases remain blocked on the documented `20260830193000`
permission-collision review.
