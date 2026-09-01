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
- Business ownership: product masters, categories, brands, couriers and packing
  rules belong to each tenant's mall business schema. Among the 51 non-identity
  compatibility resources, tenant-platform retains only `payments`; see
  DEC-0009.
- Development execution: resource-heavy development uses the versioned remote
  checkout on `167.17.68.242`, but every original `r1shop-dev` resource is
  immutable. The only write target is a new `mss-shop-dev` namespace with
  dedicated PostgreSQL 17.6, Redis 8.6.3, PVCs, TLS, credentials and network
  policy. The exact old TimescaleDB is a bounded read-only import source; old
  Redis is not shared. See DEC-0010.
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
- A create-only isolated infrastructure operator and manifest for exactly 24
  `mss-shop-dev` objects, with nine NetworkPolicies ordered before both
  StatefulSets; a create-only foundation operator for six immutable
  namespace-local Secrets; dedicated PostgreSQL 17.6/Redis 8.6.3 PVC/TLS
  definitions; and exact old-source/production rejection contracts.
- A one-time legacy importer image and Job template with a compiled 51-table
  inventory, structure-only `orders`/`order_goods`, source read-only controls,
  strict target TLS, deterministic per-table receipt evidence and a
  receipt-bound database marker.
- Fixed create-only readiness and post-import verifier Jobs in the same fourth
  delivery image. They use separate minimum network roles, mount only the new
  datastore credentials they need, emit one strict Pod/revision/digest-bound
  JSON record, and never receive a legacy-source credential in verifier mode.
- A metadata-only original-development fingerprint helper. Its typed client
  exposes only the fixed Namespace/Deployment/StatefulSet/Service/Ingress/Pod/
  PVC/PV GET/LIST inventory; it has no Secret, Pod exec, database or mutation
  path and emits only reviewed safe fields plus a canonical SHA-256.
- A fixed isolated PostgreSQL reconciler, no-ServiceAccount Job template and
  two Admin runtime manifests. The reconciler is receipt-bound and owns only
  the reviewed isolated roles, schemas, compatibility owners, snapshots,
  views and grants. These repository artifacts are not deployment, import or
  acceptance evidence.
- MSS-generated project contracts, seven MSS workflow Skills and two R1Shop
  project Skills.
- Versioned target architecture, migration plan, i18n policy and project Skill.
- A project-scoped `mss-mcp` connection whose wrapper rejects any version other
  than 1.3.7.
- A versioned 54-table legacy data contract and 31-scenario business acceptance
  matrix, with a target owner split of four tenant-platform migration/payment
  tables and 50 mall-platform tables. The 51 compatibility resources allocate
  one (`payments`) to tenant-platform and 50 to mall-platform.
- A mall Admin compatibility UI for all 50 mall-owned legacy resources. It
  includes list/search/paging, detail, sensitive-field handling, stable error
  states and complete `zh-CN`/`en-US` catalogs. Every resource is currently
  read-only. This is a compatibility surface, not completion of the historical
  order, inventory, payment, wallet, promotion or import workflows.
- A fixed-binding mall compatibility backend for the same 50 resources. The
  seven source-global product/logistics rows are schema-scoped tenant snapshots
  in the fixed business schema; the runtime has no shared-catalog selector. It
  enforces the immutable MSS/legacy tenant scopes, fully qualified allow-listed
  tables, inherited tenant guards, explicit detail capabilities, sensitive
  field redaction and both component/API policies. All 50 resources reject
  generic create, update and delete at the authorization and repository layers
  until each resource has
  recovered legacy validation, relationship/tenant constraints, model-hook
  effects and deletion semantics through a dedicated qualified workflow.
  `system_configs.metadata` is recursively redacted and excluded from search,
  filtering and sorting because its nested secrets must not become a
  result-count oracle.
- A tenant-platform payment-catalog backend and Admin UI containing only
  `payments`. The seven product/logistics resources have forward tenant policy
  revocations and mall grants. All 51 resources remain read-only, and no
  environment application or deployment of those migrations is claimed.
- A flat bilingual compatibility-error contract shared by both platforms. It
  includes stable code/key/fallback fields and reduces old or malformed nested
  responses to React-safe strings.
- A guarded, forward-only local SQLite UI fixture for mall-platform. It creates
  the 50 reviewed table shapes and non-sensitive demo rows without
  `AutoMigrate`, rejects remote/escaped/incompatible targets, is idempotent and
  is explicitly excluded from migration or system-acceptance evidence.
- An MSS-generated migration-checkpoint module used only as a review ledger,
  plus a handwritten Feature contract for fixed legacy schema compatibility.
- A repository Skill and contract tests that keep the target table ownership,
  50/one source catalogs, acceptance matrix, MCP registration and
  project-memory source paths in sync with code.
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
- A single, intentionally small CI workflow covering backend and Admin Web unit
  tests for all three modules, exact MSS/project-memory and architecture
  contracts, followed by Buildx verification. Push builds publish four
  full-SHA images—tenant-platform, mall-platform, reconciler and
  legacy-importer—with digest-bound receipts; pull requests build without
  pushing and no workflow deploys them. Run `33494258866` supplied the
  successful import revision-A image; run `33497583981` supplied the revision-B
  verifier image; and run `33500133380` validates the later stage-annotation
  compatibility fix. Run `33503127917` validates revision-C evidence and memory.
  None was deployed. The older 12c6 publication remains historical
  pre-source-catalog-fix evidence. See `docs/runbooks/ci-images.md`.
- A versioned gap assessment that keeps “complete legacy restoration” distinct
  from the current read-only compatibility surface. It records the P0/P1/P2
  work and dependency order in
  `docs/project/legacy-restoration-gap.md`; accepted business scenarios remain
  0/31.
- An accepted remote-development/stage boundary and runbook. They pin the
  server checkout/toolchain, immutable original development environment,
  isolated `mss-shop-dev` write target, four-image flow, create-only operators,
  receipt-bound A-to-B-to-C import/verification/reconciliation evidence chain,
  disposable-Pod verification and in-app-browser acceptance requirements. The
  isolated infrastructure, foundation Secrets, revision-bound readiness, one
  successful legacy import and its independent revision-B verifier have
  executed. The receipt and verifier output are persisted under the canonical
  receipt SHA; reconciliation, runtime rollout and cluster/browser acceptance
  are still open.
- A catalog/logistics redesign review covering CL-01 through CL-12. Its
  recommendations for product/SKU identity, inventory, packing rules, courier
  adapters, credentials, outbox and replay remain **awaiting project-owner
  review**; DEC-0009 ownership is accepted, but the review does not authorize
  business writes or those redesigns.

## Not implemented yet

- A persistent control-plane repository, leases or observed-status integration
  between the tenant Admin module and reconciler.
- The authoritative normalized Host/AppID binding repository between raw Admin
  desired state and storefront serving. The current static serving directory
  already normalizes exact bindings and fails closed on duplicates.
- A generalized or production PostgreSQL/Kubernetes reconciler, persistent
  worker queue/inbox, or persistent desired/observed control-plane integration.
  The fixed first-tenant development driver and operator resources still need
  their immutable-image cluster rehearsal and browser acceptance.
- Executing the reconciliation-secret operator and reconciler Job against the
  isolated cluster. The successful importer, immutable receipt and clean
  revision-B verifier now form the A-to-B-to-C evidence chain, but no
  reconciliation Secret or reconciler workload has been created yet. These
  gates may not be replaced by ad-hoc deployment commands.
- The reconciliation-secret operator now defaults to a non-persistent
  API-server dry-run and requires explicit `--create`; this safety change is
  not itself execution evidence. Its clean-revision dry-run and later exact
  three-Secret write declaration remain pending.
- Dedicated order, inventory, payment, wallet, promotion, import/export and
  other historical side-effect workflows. Generic resource access does not
  satisfy their business acceptance scenarios.
- Applying and verifying the product/logistics ownership migrations in an
  isolated development environment, including per-tenant data conversion,
  menu/policy results, row counts, hashes and relationship checks. The source
  migrations exist but have not been executed or deployed.
- Per-resource mutation qualification for the 50 mall and one tenant
  payment compatibility resources. Writes remain disabled until old
  validation, relationships, tenant scope, hooks, authorization and deletion
  semantics are restored and accepted for that resource.
- Project-owner decisions for CL-01 through CL-12 in
  `docs/reviews/legacy-catalog-logistics-redesign.md`; until then the seven
  migrated resources remain compatibility reads and unsafe legacy behaviors
  are not reproduced as generic CRUD.
- Customer authentication, storefront catalog/cart/checkout and payment
  execution beyond the existing contract-first bootstrap slice.
- Legacy identity conversion (`tenants`, `users`, `roles`), warehouse data
  scopes, or reconciliation of the imported compatibility snapshot into the
  final tenant core/business schemas.
- A development-cluster Admin runtime rollout, isolated UI acceptance,
  production migration or cutover. The successful bounded import into
  `mss_shop_dev` must not be confused with a completed application deployment.
- A storefront API image or production reconciler/worker image. Those
  components do not yet own complete production entrypoints and Dockerfiles.

## Next milestone and acceptance criteria

Publish and verify the reconciliation-secret safety revision's four image
receipts, then run its default API-server dry-run against the committed receipt
and verifier evidence. Only after that gate passes and the exact write is
declared may the three receipt-bound application/bootstrap Secrets be created
in `mss-shop-dev`; the reconciler Job remains a separate later gate. Acceptance
still requires isolated disposable-Pod system tests and in-app-browser review
with URLs left for owner verification. The original development environment
and production remain unchanged.

## Verification evidence

Verification evidence recorded on 2026-09-01:

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
- Before DEC-0009, the 43-resource mall compatibility backend and the
  eight-resource tenant compatibility backend passed their recorded package
  tests, `go vet` and focused race checks. Those results are historical and do
  not by themselves verify the new 50/one source allocation.
- Before DEC-0009, the mall Admin Web compatibility surface passed 9 test files
  / 33 tests, Biome/Umi lint, and a production build containing 43 mall legacy
  routes. The tenant Admin Web passed 7 test files / 26 tests, lint and a
  production build containing eight tenant routes. Each lint reported one
  non-blocking unused-hook warning in an MSS-generated page; the qualified
  specification workarounds and warning are recorded in
  `docs/tooling/mss-1.3.7-generation-notes.md`.
- The updated `go test ./contracts` passed against a one-time source snapshot
  on the DEC-0010 development host with Go 1.26.6. It proves the 54-table target
  owner counts of four/50, the 51-resource allocation of one/50, exact source
  catalogs, the 31-row acceptance baseline, safety flags and required memory
  paths.
- The current complete source tree passed `GOWORK=off GOMAXPROCS=2 go test
  -p=1 ./...` and `go vet ./...` at the platform root and independently in
  both final Thin Hosts. This includes the isolated infrastructure, Secret,
  Job, reconciliation-evidence, runtime and original-development fingerprint
  operators plus the 51-table importer/readiness/verifier packages.
- The current tenant Admin Web passed 7 files / 26 tests and the mall Admin Web
  passed 9 files / 33 tests using Node 24.19.0 and pnpm 10.34.5 with the exact
  1.3.7 package. The root proof Web had no matching test files and exited
  successfully. Final production bundles remain gated by the four-image CI
  build for the committed revision.
- `tools/check-project-memory.sh` passed in the same temporary checkout with the
  official MSS 1.3.7 tool. It reran the contract suite, validated all nine
  repository Skills and completed the whitespace checks;
  `scripts/check-platform-boundaries.sh` also passed. No database or Kubernetes
  resource was changed by this source validation.
- GitHub Actions run
  [`33451906040`](https://github.com/shop-r1/mss-shop/actions/runs/33451906040)
  passed the contract, three Go-unit and three Admin-Web-unit jobs, then
  published both delivery images for revision
  `ac74347cbbd6cd24f731dadd239c1044ff132e38`. The tenant image digest is
  `sha256:c4d0e651553263f8cf8127351ee3d14d13076c0b23052f64ecb017d8cd2dbef0`;
  the mall image digest is
  `sha256:2fa16ec9cf3854662726f64de978940653b3133a63b2e1951dd189656f34bc1e`.
  This run predates the reconciler/importer matrix and per-image receipts; it
  is historical two-image evidence and does not pass the current four-image
  deployment gate.
- GitHub Actions run
  [`33487529898`](https://github.com/shop-r1/mss-shop/actions/runs/33487529898)
  completed the current contract/unit gates and published all four immutable
  image receipts for revision
  `12c6a682e38bfef165e09d108e0bd77c53ee73ca`. The legacy-importer digest was
  `sha256:9eb9efcb01ff5ac115b6df772f68139cba9ce53f691a310756f81cceb161fb05`;
  this is historical pre-source-catalog-fix evidence and is not reusable by a
  later revision.
- GitHub Actions run
  [`33494258866`](https://github.com/shop-r1/mss-shop/actions/runs/33494258866)
  passed the current contract/unit gates and published all four immutable
  image receipts for revision
  `6fed45f354e93efe104045c6dde86ac33c368d6d`. Its legacy-importer digest is
  `sha256:881f105ea00dfac3bf4381e0177ad1349998d51059beeb155e1a96c64bbe3ba3`;
  this is the exact image used by the successful readiness and import Jobs.
- GitHub Actions run
  [`33497583981`](https://github.com/shop-r1/mss-shop/actions/runs/33497583981)
  passed every current gate and published all four revision-B
  `3eb4c72b485066e7b189446fab5b66a1047e66a2` image receipts. The
  legacy-importer digest
  `sha256:a3e1609e75164187557c9207f3565efe7bf8fb413b0adc7f6cceb71c1d531799`
  is the exact verifier workload image.
- GitHub Actions run
  [`33500133380`](https://github.com/shop-r1/mss-shop/actions/runs/33500133380)
  passed every current gate for stage-tool fix revision
  `ebefd1c20bf51f3c43e4a2bb90085fb60ea21442`. Its four images were not
  deployed. The fix permits exact KubeSphere v3.1.1 controller-owned Job
  status annotations without accepting stale, foreign or extra metadata.
- GitHub Actions run
  [`33503127917`](https://github.com/shop-r1/mss-shop/actions/runs/33503127917)
  passed every current gate for evidence revision C
  `fc6d1bf357ca7291a0fc2fe4391ca15628f8e9b9` and published four immutable
  receipts. Its tenant, mall, reconciler and legacy-importer digests are
  respectively `sha256:87a2ba402b9dc5f82769b4fbf4c1b1220368483dde6a4c6fc580507328f05750`,
  `sha256:22a02242cc815ec7e2bf29fc5f9ec86789a245074754f49b59bc2b7def66c92e`,
  `sha256:0beece2f39be8892649981db69bb20ebef6c6b27a6ab8741d4cf129b5d6a3af5`
  and `sha256:385e5161b5a2133a482cb34330092d4e94e9e3c48d7b1a7ca01dc5f4b7e3bb38`.
  None was deployed.
- The exact 24 infrastructure objects and six immutable foundation Secrets
  were created only in `mss-shop-dev`. Its PostgreSQL 17.6 and Redis 8.6.3 are
  ready. The original-development metadata fingerprint was captured from the
  fixed read-only helper at revision `12c6a682...`; its selected safe-field
  SHA-256 remained
  `7ddbc7f22749a29a7c019a5fa9f6c5d933cdfdd5fa5cb0e5fb9bc2bab54d8854`.
  The complete non-secret output is versioned at
  `docs/evidence/original-dev/2026-09-01-before-12c6a682.json`.
- Disposable readiness for revision `12c6a682...` passed with exact Pod,
  revision and importer-image digest binding. The subsequent importer failed
  before target transaction creation because the initial policy rejected 91
  legitimate TimescaleDB extension-owned `public` routines. A prior importer
  revision had also failed before the target transaction on a reserved SQL
  alias. Both failed Jobs were still present in the cluster when evidence was
  captured; their complete safe outputs, workload identities and digests are
  recorded at
  `docs/evidence/mss-shop-dev/2026-09-01-import-attempts.yaml`. Neither emitted
  a receipt or could change the isolated-empty marker.
- A catalog-only read-only repeatable-read inspection then proved exact
  extensions `plpgsql 1.0` and `timescaledb 2.20.2`, 91 exact TimescaleDB
  routine members, no other `public` routines, no standalone `public` types and
  the complete ordered `pg_proc` SHA-256
  `32c0b88f3178e4a15647eef85da4a718b4e490070bd7fa2c77876101f386d81e`.
  The importer now enforces that instance-bound fingerprint in the same source
  snapshot used by COPY.
- Revision `6fed45f...` passed a new isolated readiness Job and a single-attempt
  importer Job only in `mss-shop-dev`. The importer copied 49 eligible tables,
  created the two order tables as structure-only, and emitted a deterministic
  51-table receipt. `orders` retained source count 14 with target count 0;
  `order_goods` retained source count 1170 with target count 0. The canonical
  receipt SHA is
  `fa666688d8df975344030f31266072605031da1cd22cfcc341326f909071ef76`;
  the byte-exact receipt and safe workload provenance are versioned under
  `docs/evidence/legacy-import/` and
  `docs/evidence/mss-shop-dev/2026-09-01-import-success.yaml`.
- Revision-B Job
  `mss-shop-legacy-verify-3eb4c72b485066e7b189446fab5b66a1047e66a2`
  completed once in `mss-shop-dev` with one succeeded Pod, zero failures and
  zero restarts. Its digest-bound in-cluster verifier independently matched the
  marker, all 51 receipt tables and target schema, and proved both `orders` and
  `order_goods` contain zero rows. The original one-line stdout is committed as
  `docs/evidence/legacy-import/fa666688d8df975344030f31266072605031da1cd22cfcc341326f909071ef76/verification.json`;
  its file SHA-256 is
  `47878f1f7da8164438604751a89f45775695a1794603296a93d6d5a81499824c`.
- The initial stage command reported a post-create failure only because
  KubeSphere added its `revisions` annotation between Create and GET; the
  already-created verifier itself succeeded. Revision `ebefd1c...` corrected
  that bounded annotation check and then accepted the existing B resources as
  a read-only exact retry (`created=false`). A new B2 verifier preflight was
  separately rejected because the immutable receipt ConfigMap remains bound to
  B; it performed no write and created no second Job or Pod. Full safe
  provenance is recorded in
  `docs/evidence/mss-shop-dev/2026-09-01-verifier-success.yaml`.
- The fixed metadata-only original-development fingerprint was captured both
  before and after this import. The complete documents were byte-identical
  (`70b29137f5c499c8819effb4313838a5fd73f0d229205ed92160dda43663683d`),
  the selected safe-fields digest remained
  `7ddbc7f22749a29a7c019a5fa9f6c5d933cdfdd5fa5cb0e5fb9bc2bab54d8854`,
  and the helper recorded no Secret access, database connection or write.
- The same helper was run again after verifier acceptance at revision
  `ebefd1c...`. Its selected safe-fields digest is still
  `7ddbc7f22749a29a7c019a5fa9f6c5d933cdfdd5fa5cb0e5fb9bc2bab54d8854`;
  the complete new file is
  `docs/evidence/original-dev/2026-09-01-post-verifier-ebefd1c.json`, and again
  records no Secret access, database connection or write.
- Before DEC-0009, the local mall UI fixture passed full Host backend tests/vet
  and focused race checks; its tests proved 43-table completeness, ten-row
  idempotent seeding, readiness and rejection of DSNs, escaped paths, symlinks,
  non-SQLite files and incompatible existing relations. The expanded 50-table
  fixture requires post-change validation.
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
- The current tenant-platform and mall-platform Thin Hosts both passed their
  complete MSS 1.3.7 `verify --all` suites, including backend build/test,
  frontend build/lint/test, generated-drift and diff checks. The root
  verification's known nested-Host discovery limitation remains documented in
  `docs/runbooks/local-development.md`; root strict doctor and explicit CI
  jobs are the project-level proof.

A successful bounded legacy snapshot import and its independent disposable
verifier have been performed only against the dedicated `mss-shop-dev`
PostgreSQL database. No reconciliation, Admin runtime rollout, isolated browser
acceptance, production migration or production write has been performed. The
import/verifier evidence is not business or complete system acceptance. The
original `r1shop-dev` environment remains ready with the same selected safe
metadata fingerprint. The target is now gated on the ordered
reconciliation/runtime/disposable-Pod controls above. Accepted business
scenarios remain 0/31.
