# Project status

Last verified: 2026-09-02

First verified tag-only development refresh: PR-head revision
`bf07098cb8a7c5f2c52993e28c69afc7712c4d98` passed GitHub Actions run
`33574863356`, published all four images, and completed the first automatic
DEC-0012 CD. The tenant and mall Admin Deployments are each 1/1 updated, ready
and available; both `migrate` and `admin` use the matching full-SHA tag, with
zero restarts. The latest evidence-bearing browser smoke remains revision
`f202b094fd5b2839a9020ff38db833fec40be704` from run `33565434916`, which cut
both Admin runtimes over to DNS-only trusted HTTPS hosts and reached their
visible workspaces after login as `admin`. The canonical legacy import was not
rerun: it remains the single 51-table snapshot bound to receipt
`fa666688d8df975344030f31266072605031da1cd22cfcc341326f909071ef76`.
The fixed-tenant Member Levels projection still contains four matching rows and
both order tables remain empty. This fast smoke cutover does not close any of
the 31 business acceptance scenarios. All earlier environment claims in this
document apply only to their named revision.

The repository now defines a DEC-0012 development CD contract for a
same-repository `codex/**` pull request targeting `main`: after the existing CI
gates publish all four images for the exact PR-head SHA, a local reusable
workflow may update only the `migrate` and `admin` images in the two existing
`mss-shop-dev` Admin Deployments. This is now
`verified-first-success-run-33574863356`. The `mss-shop-dev` GitHub
Environment retains `MSS_SHOP_DEV_KUBECONFIG` outside Git. That run proves the
bounded image refresh and observed rollout health only; it does not close a
system, browser or business acceptance scenario.

The namespace-side CD access bootstrap is complete: ServiceAccount, Role,
RoleBinding and service-account token Secret are all named
`mss-shop-dev-image-updater` in `mss-shop-dev`. The Role was verified to allow
only `get`/`patch` on the two named Admin Deployments and to deny other
Deployments, Secrets and Pods. These four objects are tracked separately from
the historical 24 infrastructure objects and six foundation Secrets. Run
`33574863356` proves that this bounded identity can perform the intended image
refresh; it grants no broader authority.

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
- Development image delivery: a successful same-repository `codex/**` pull
  request to `main` may call the local reusable dev CD only after all four
  same-head-SHA images are published. That CD changes only the two isolated
  Admin Deployments' `migrate` and `admin` image fields and has no production,
  old-development, database, configuration or acceptance authority. See
  DEC-0012.
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
  revocations and mall grants. All 51 generic compatibility resources remain
  read-only; the separate Mall Settings source workflow does not alter that
  generic capability. The isolated runtime init containers completed the fixed
  core migrations, but no generic business mutation is enabled or accepted.
- A flat bilingual compatibility-error contract shared by both platforms. It
  includes stable code/key/fallback fields and reduces old or malformed nested
  responses to React-safe strings.
- A guarded, forward-only local SQLite UI fixture for mall-platform. It creates
  the 50 reviewed table shapes and non-sensitive demo rows without
  `AutoMigrate`, rejects remote/escaped/incompatible targets, is idempotent and
  is explicitly excluded from migration or system-acceptance evidence.
- An MSS-generated migration-checkpoint module used only as a review ledger,
  plus a handwritten Feature contract for fixed legacy schema compatibility.
- A dedicated Mall Settings source slice with a closed four-field DTO,
  fixed-schema/legacy-tenant repository, PostgreSQL first-create serialization,
  old `system_configs.appConfig` merge preservation, independent MSS
  read/update permissions, an MSS-style focused page and complete Chinese and
  English messages. It deliberately excludes raw metadata, secrets, payment,
  logistics, storefront and native-App settings. Its source tests, production
  build, MSS v1.3.7 verification, isolated reconciliation and read-only runtime
  deployment passed. Browser review and a qualified PostgreSQL write path
  remain open. The cluster manifest keeps writes disabled; see
  `docs/project/mall-settings-development.md`.
- A dedicated Member Levels source slice with fixed tenant/schema binding,
  strict DTOs, action-level permissions, optimistic revisions, default-level
  integrity reporting and preservation of unowned legacy policy columns. Its
  source tests and production build pass remotely. The cluster release remains
  read-only; its digest-bound projection verifier proved the fixed tenant's
  four-row projection, one enabled default, no cross-tenant rows and zero rows
  in both order tables; see
  `docs/project/member-levels-development.md`.
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
  legacy-importer—with digest-bound receipts. A same-repository `codex/**` PR
  to `main` may publish the same four-image set after all gates and then call a
  reusable CD that only updates both existing Admin Deployments' `migrate` and
  `admin` image tags in `mss-shop-dev`. Forks and other PR shapes receive no
  package or deployment credential. Run `33574863356` is the first successful
  automatic execution: it deployed PR-head revision `bf07098...` and both
  Admin Deployments were observed 1/1 updated, ready and available with zero
  restarts. This tag-only refresh is not acceptance evidence. Run
  `33494258866` supplied the
  successful import revision-A image; run `33497583981` supplied the revision-B
  verifier image; run `33500133380` validates the later stage-annotation
  compatibility fix; and run `33503127917` validates revision-C evidence and
  memory. Run `33532383550` published the four images used by the original
  isolated deployment at source revision `3e64a57d...`; run `33565434916`
  published the four images used by the DNS-only HTTPS Admin cutover at
  revision `f202b094...`. Older publications remain historical evidence and
  were not substituted into either evidence-bearing rollout. See
  `docs/runbooks/ci-images.md`.
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
  successful legacy import, independent receipt verification, reconciliation,
  fixed projection verification, runtime rollout and disposable cluster
  acceptance have executed. Confirmed-login browser smoke review now passes on
  both trusted HTTPS hosts; detailed business-route and locale review remains
  open. The receipt and verifier output are persisted under the canonical
  receipt SHA.
- A catalog/logistics redesign review covering CL-01 through CL-12. Its
  recommendations for product/SKU identity, inventory, packing rules, courier
  adapters, credentials, outbox and replay remain **awaiting project-owner
  review**; DEC-0009 ownership is accepted, but the review does not authorize
  business writes or those redesigns.

## Not implemented yet

- The first operational DEC-0012 dev CD execution. Its repository workflow is
  configured, its namespace access identity is present and the
  `mss-shop-dev` GitHub Environment kubeconfig is provisioned outside Git, but
  a qualifying PR run must complete before the status can change to deployed.
  This pending execution is not a system or browser acceptance failure.

- A persistent control-plane repository, leases or observed-status integration
  between the tenant Admin module and reconciler.
- The authoritative normalized Host/AppID binding repository between raw Admin
  desired state and storefront serving. The current static serving directory
  already normalizes exact bindings and fails closed on duplicates.
- A generalized or production PostgreSQL/Kubernetes reconciler, persistent
  worker queue/inbox, or persistent desired/observed control-plane integration.
  The fixed first-tenant development driver has now passed its immutable-image
  cluster rehearsal and confirmed-login browser smoke; detailed business
  acceptance remains open.
- Generalizing the fixed isolated reconciliation operator into a persistent
  control-plane lifecycle. The exact first-tenant Secrets and reconciler Job
  have executed in `mss-shop-dev`; that bounded evidence does not authorize
  another tenant, environment or production rollout.
- Dedicated order, inventory, payment, wallet, promotion, import/export and
  other historical side-effect workflows. Generic resource access does not
  satisfy their business acceptance scenarios.
- Completing the detailed browser review of the deployed Mall Settings and
  Member Levels read-only slices. Confirmed-login workspace smoke now passes;
  PostgreSQL projection evidence and
  cluster system verification pass, while writable cutover semantics, business
  switches and credential rotation remain open; `CONFIG-001` and `MEMBER-001`
  are not closed.
- Qualifying the reconciled product/logistics snapshots as writable tenant
  workflows, including relationship checks, menu/policy behavior and mutation
  lifecycle evidence. The current deployment proves only isolated read access
  and least privilege.
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
- Legacy identity conversion (`tenants`, `users`, `roles`) and warehouse data
  scopes. The current fixed-tenant reconciliation deliberately does not claim
  generalized identity conversion.
- Detailed isolated UI business acceptance, production migration or cutover.
  The two Admin runtimes and confirmed-login workspace smoke are deployed in
  `mss-shop-dev`, but the successful bounded import and read-only system checks
  must not be confused with complete business acceptance.
- A storefront API image or production reconciler/worker image. Those
  components do not yet own complete production entrypoints and Dockerfiles.

## Current development sequence and later acceptance

The HTTPS Admin runtime and confirmed-login smoke gate is complete for revision
`f202b094...`; both isolated URLs remain available for owner inspection.
The configured DEC-0012 workflow may refresh those two Admin images from a
qualifying PR head after its CI publication, but it performs no rollout wait or
acceptance. The Environment secret is configured, but until the first
qualifying run succeeds, the deployed revision remains the historical
`f202b094...` evidence above.
Continue the detailed route/locale review and later domain work without
treating these two read-only slices as full business restoration. Payment
writes wait for
DEC-0008 approval, and product/logistics writes wait for the open CL review
items. The original development environment and production remain unchanged.

## Verification evidence

Verification evidence recorded on 2026-09-02:

- The DEC-0012 namespace access bootstrap created exactly
  `ServiceAccount/mss-shop-dev-image-updater`,
  `Role/mss-shop-dev-image-updater`,
  `RoleBinding/mss-shop-dev-image-updater` and service-account token
  `Secret/mss-shop-dev-image-updater` in `mss-shop-dev`. Authorization checks
  allowed only `get`/`patch` on the tenant and mall Admin Deployments and
  denied other Deployments, Secrets and Pods. No Secret value is recorded in
  the repository. The GitHub Environment and named secret remain configured;
  run `33574863356` subsequently proved the bounded CD execution. The RBAC
  checks remain access-boundary evidence rather than business acceptance.

- GitHub Actions run
  [`33574863356`](https://github.com/shop-r1/mss-shop/actions/runs/33574863356)
  passed for PR-head revision
  `bf07098cb8a7c5f2c52993e28c69afc7712c4d98`, published all four images and
  completed the first automatic DEC-0012 CD. The tenant image ID was
  `sha256:deec51fe4fa4a65729f55b727591635f70a4f9bb27bf2a834ec1b7bd2bf52f17`
  and the mall image ID was
  `sha256:6ad41feed5f348f2f71fdf4f12b87c1e64a649e0367b8f55a7425c0d696662be`.
  Both Deployments were observed generation 3, 1/1 updated, ready and
  available; both `migrate` and `admin` had zero restarts. No system or browser
  acceptance was executed by CD.

- GitHub Actions run
  [`33565434916`](https://github.com/shop-r1/mss-shop/actions/runs/33565434916)
  passed for source revision
  `f202b094fd5b2839a9020ff38db833fec40be704` and published all four immutable
  receipts. The deployed tenant and mall digests are
  `sha256:75ed6e8e2b42aad4a88e618f6cd9b2d0197ad12f15392c47ea458b2f3433f39d`
  and
  `sha256:32e9497279393e7cd5bc0896e594f52697cb6092a939f61e1939cb5c86208b50`.
- The create-only TLS stage created one solver NetworkPolicy, one namespaced
  production ACME Issuer and two exact-host Certificates only in
  `mss-shop-dev`. Both Certificates reached `Ready=True`, expire on
  2026-11-30 and validate for their exact Admin hostnames.
- The trusted runtime stage updated the fixed eight Admin objects. Both
  Deployments rolled out 1/1 Ready, both Service cluster IPs stayed unchanged,
  HTTP redirects to HTTPS, and both trusted HTTPS login pages resolve to 200.
  In-app-browser login as `admin` reached `/workplace` on both hosts; password
  values were neither logged nor committed. Detailed business-route review is
  still open, so accepted business scenarios remain 0/31.

- GitHub Actions run
  [`33532383550`](https://github.com/shop-r1/mss-shop/actions/runs/33532383550)
  passed for source revision
  `3e64a57dae8bb3dd4d337a423015baae6c352b32`. The exact tenant, mall,
  reconciler and legacy-importer image digests are respectively
  `sha256:c65f5e8b19033afcdae25e0ec046efc958190a0abf38ab1d2bf379d0475b742d`,
  `sha256:a58868c78bc3e62f40b6988ec43eb4923f00d15ecc8540eb06b6b863016e1c1a`,
  `sha256:fba8a63938eef780e8eeb68e2c391bd91ad01c4214dcfa6a7089cf75cc1ab4fd`
  and
  `sha256:0d2d6077798328227e2b19a14d8075e25de0cdccdee5100a118ec3a888fa0bb0`.
  The remote release checkout was full-history, clean and exactly bound to the
  source revision. CI still had no deployment permission.
- The legacy importer was **not** rerun. Reconciliation consumed the existing
  canonical receipt and completed in one successful Pod with zero restarts.
  It applied one locked transaction containing 252 reviewed SQL statements,
  46 views and seven snapshots. The final safe Job log SHA-256 is
  `c3fbf359e366b7795369366915cc6fd0a0e175a19290151590aef145b51aeb9a`.
  Two earlier same-day reconciler revisions failed closed on PostgreSQL 17
  query-qualification defects and rolled their transactions back; their Jobs
  remain as immutable diagnostic evidence.
- The digest-bound Member Levels projection Job succeeded once. It proved four
  fixed-tenant source rows, four business rows, zero differences, no
  cross-tenant rows, exactly one enabled default, no runtime privilege on the
  imported public source and zero rows in both `orders` and `order_goods`.
- The trusted runtime stage created exactly two ConfigMaps, two Deployments,
  two Services and two Ingresses in `mss-shop-dev`. Both Deployments are 1/1
  Ready and Available; their Pods have zero restarts and exact CI image IDs.
  The isolated review URLs are
  `http://tenant-admin.167.17.68.242.nip.io` and
  `http://mall-admin.167.17.68.242.nip.io`.
- Two v3 disposable verification Jobs completed successfully in the cluster.
  They proved both health/readiness/UI endpoints return 200 while unauthenticated
  APIs are denied; fixed PostgreSQL roles cannot cross schema boundaries or
  perform forbidden DDL/DML; target TLS validates CA and hostname; and the two
  authenticated Redis TLS connections are isolated to DB 1 and DB 2. No
  temporary verification NetworkPolicy remained after collection.
- The post-acceptance metadata-only fingerprint of the original development
  environment is still
  `7ddbc7f22749a29a7c019a5fa9f6c5d933cdfdd5fa5cb0e5fb9bc2bab54d8854`.
  The helper again recorded no Secret access, database connection or write.
  Production was neither read nor changed.
- The earlier revision's confirmed-login browser step was pending and is now
  superseded by the bounded `f202b094...` smoke evidence above. That smoke
  still closes no business scenario.

Historical verification evidence recorded on 2026-09-01:

- The current release candidate passed `GOWORK=off GOMAXPROCS=2 go test -p=1
  ./...` and `go vet -p=1 ./...` independently at the root and both final
  Hosts. Root, Tenant and Mall Admin Web tests/lint/production builds passed on
  Node 24.19.0 and pnpm 10.34.5. Tenant and Mall each passed all ten MSS v1.3.7
  `verify --all` checks. Mall ran 16 files / 50 tests and built at 913.92 KiB
  against its reviewed 930 KiB total compressed-JavaScript budget while the
  default entry and largest-async-chunk limits remained unchanged. Project
  memory, boundaries and contract checks also passed. This is source evidence,
  not immutable-image, database, Kubernetes or browser evidence.

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
- Revision C passed `GOWORK=off GOMAXPROCS=2 go test
  -p=1 ./...` and `go vet ./...` at the platform root and independently in
  both final Thin Hosts. This includes the isolated infrastructure, Secret,
  Job, reconciliation-evidence, runtime and original-development fingerprint
  operators plus the 51-table importer/readiness/verifier packages.
- At Revision C, the tenant Admin Web passed 7 files / 26 tests and the mall Admin Web
  passed 9 files / 33 tests using Node 24.19.0 and pnpm 10.34.5 with the exact
  1.3.7 package. The root proof Web had no matching test files and exited
  successfully. Final production bundles remain gated by the four-image CI
  build for the committed revision.
- `tools/check-project-memory.sh` passed for Revision C in the same temporary checkout with the
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
  fixture is included in the current passing Mall Host suite.
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

A successful bounded legacy snapshot import, independent receipt verification,
receipt-bound reconciliation, Member Levels projection check, Admin runtime
rollout and disposable cluster system verification have been performed only
against `mss-shop-dev`. Confirmed-login browser review, production migration
and production write have not been performed. These read-only migration and
runtime results are not complete business acceptance. The original
`r1shop-dev` environment remains ready with the same selected safe metadata
fingerprint. Accepted business scenarios remain 0/31.
