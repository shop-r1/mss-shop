# Overall solution

## Outcome

R1Shop becomes a small control plane plus isolated tenant runtimes. MSS remains
an unmodified Admin foundation inside two separate Thin Hosts. The storefront
API and mobile application are independent of the Admin API.

```text
Platform operator
    |
    v
Tenant platform (one control-plane deployment)
    | desired state
    v
Reconciler -----------------------> tenant database roles/schemas/runtime
                                      |
Tenant administrator                v
    +--------------------------> Mall platform (one deployment per tenant)
                                      |
Customer H5 / WeChat Mini Program    v
    +--------------------------> Storefront API /app/v1
                                      |
                                      v
                              tenant business schema
```

## Repository topology

```text
mss-shop/
  apps/
    tenant-platform/       # MSS Thin Host: control-plane admin backend + web
    mall-platform/         # MSS Thin Host: mall admin backend + web
  services/
    reconciler/            # desired/observed-state convergence and DDL owner
    legacy-importer/       # one-time bounded import into isolated development
    storefront-api/        # public/member API under /app/v1
    worker/                # asynchronous commerce jobs
  contracts/
    app-v1/                # authoritative OpenAPI and JSON schemas
  deploy/                  # environment-neutral deployment definitions
  docs/                    # architecture, ADRs, runbooks and current status

mss-shop-mobile/           # separate repository
  src/                     # uni-app Vue3/Vite application
  contracts/               # locked snapshots consumed from mss-shop
```

The repository root currently contains a generated MSS Thin Host used to prove
the Distribution and local login. It is a phase-zero artifact, not the target
monorepo layout. Generate the two final hosts directly in their destination
directories; do not move or copy managed files because the MSS Blueprint
manifest and generated paths must stay coherent.

## Component boundaries

### Tenant platform

- Owns tenant records, subscriptions, domain/AppID bindings, locale/currency/
  timezone defaults and desired lifecycle state.
- Lets platform operators request provision, suspend, resume and upgrade.
- Does not run DDL, create credentials, or mutate Kubernetes resources from an
  HTTP request.
- Uses its own control-plane storage. If it consumes MSS, keep MSS core tables
  isolated from control business tables so an MSS migration cannot rewrite
  application-owned columns.
- Owns the transitional `payments` compatibility catalog; it does not own
  product or logistics records stored in tenant mall schemas.

### Reconciler

- Is the sole writer for tenant PostgreSQL roles, schemas, snapshots,
  compatibility views and grants.
- Converts desired state into observed state through idempotent steps, leases,
  retries and recorded checkpoints.
- Produces auditable status without returning secret values to the control
  plane UI.
- Applies MSS core migrations only to a core schema and R1Shop migrations only
  to the matching business schema.
- The current fixed `mss-shop-dev` Job has no ServiceAccount token or
  Kubernetes API permission and changes only the receipt-bound isolated
  database reconciliation boundary in one locked transaction. It verifies the
  import marker and empty order tables before reconciling roles, schemas,
  compatibility owners, snapshots, views and grants. It is not a generalized
  lifecycle or production driver.

### Trusted stage operator

- Runs only from a clean checkout for the reviewed full Git SHA with an
  explicit local operator kubeconfig; it is not an HTTP or in-cluster control
  plane.
- Owns three separated stages in the isolated namespace: the exact 24-object
  create-only infrastructure boundary, six immutable foundation Secrets, and,
  only after verified import, the two application Secrets plus transient
  reconciler bootstrap Secret. Each operator runs from a clean full-SHA
  checkout with an explicit kubeconfig and never prints Secret values.
- Every Secret/workload stage defaults to API-server `DryRunAll` for the exact
  Create or Update and persists nothing. Persistence requires an explicit
  `--create` or `--apply`; the same process repeats the complete preflight
  immediately before writing only its fixed `mss-shop-dev` objects.
- The foundation credential stage may GET only the exact old database
  credential and GHCR pull Secret. It creates independent PostgreSQL/Redis
  credentials and TLS identities in `mss-shop-dev`; it does not read or reuse
  the old Redis.
- Runs an independent trigger-disabled, receipt-bound database catalog
  preflight before reconciliation. The in-cluster Job repeats the fixed target
  and catalog boundary.
- Applies only the two ConfigMaps, Deployments, Services and Ingresses recorded
  by DEC-0010 in `mss-shop-dev`, after exact object-binding and cluster-wide
  host collision checks. It does not force field conflicts or adopt any
  original-development resource.

### Mall platform

- Is one build/image deployed once per tenant.
- Starts with an immutable tenant identifier and fixed database connections.
- Uses MSS for administrator identity, roles, menus, Casbin policies, sessions
  and Admin UI composition.
- Uses an explicitly injected business database handle for commerce modules.
- Keeps the MSS connection's current schema limited to the fixed core schema;
  every legacy commerce query uses a startup-bound, validated, fully qualified
  business object name.
- Owns that tenant's product masters, categories, brands, logistics providers
  and packing rules in the fixed business schema. The tenant platform does not
  provide a permanent cross-tenant product/logistics writer.
- Never chooses a tenant or schema from a request header, route, JWT claim sent
  by an untrusted client, or UI selector.

### Admin UI design and code quality

- Preserve legacy business operations, data semantics, permissions and audit
  behavior; do not reproduce the old Vue pages, DOM structure or visual style.
- Use the mss-boot-admin Admin Web layout, design tokens, tables, forms,
  feedback, loading/empty/error states and permission interaction as the visual
  and behavioral baseline. Do not introduce a parallel Admin design system.
- Keep domain workflows explicit and typed in business-owned components,
  hooks and services. Reuse small stable primitives, but avoid a single
  configuration-driven mega page, duplicated route/API adapters, raw field
  dictionaries, magic status values and compatibility branches spread across
  presentation code.
- The generic read-only compatibility viewer is a migration aid. A qualified
  write workflow should receive a focused MSS-native page and domain model
  when its validation, state transitions or cross-table effects cannot be
  expressed clearly by the viewer.
- Code review favors clear ownership, deletable compatibility layers and tests
  around domain behavior over clever abstraction or line-for-line similarity
  with the old frontend.

### Storefront API

- Owns `/app/v1` for anonymous browsing, customer identity, cart, checkout,
  orders and payment callbacks.
- Resolves the tenant server-side from an allow-listed Host mapping (H5) or a
  trusted mini-program identity/bootstrap flow.
- Returns stable error codes and parameters; clients localize the presentation.
- Does not expose or proxy the MSS Admin API.

### Worker

- Runs asynchronous, retryable commerce work such as inventory release,
  notification dispatch and order state reconciliation.
- Carries a server-issued tenant identity and uses the same fixed tenant
  connection registry as the storefront service.

## Data isolation

Each tenant owns an immutable identifier such as a UUID. Human-readable names
and domains may change and therefore never form schema names directly. The
reconciler derives a short collision-checked key and creates:

```text
r1_m_<tenant-key>_core   MSS users, roles, menus, Casbin, sessions, MSS ledger
r1_m_<tenant-key>_biz    product masters, logistics rules, inventory, carts,
                         orders and translations
```

This pair is one tenant isolation unit. Separate database roles receive only
the privileges required by the mall runtime and migration job. A runtime may
have two pools, but both are fixed at startup and validated against the same
tenant record. Cross-tenant reporting belongs in an explicit platform service,
not in schema switching inside the mall request path.

The split is required because the MSS Admin migration history includes a legacy
migration that scans
tables in `CURRENT_SCHEMA` and relaxes `tenant_id NOT NULL`. Keeping commerce
tables outside the MSS core schema prevents that framework migration from
changing R1Shop-owned table semantics while leaving MSS itself untouched.

### Legacy compatibility window

The old database contains 54 business tables in shared storage and filters
most tenant data by `tenant_id`. It is not safe to make that schema the MSS
current schema because legacy `users`, `roles` and `tenants` collide with Admin
names and the framework migration above inspects `CURRENT_SCHEMA`.

The isolated-development migration uses one bounded importer before the
reconciler. It reads exactly 51 reviewed business tables from the fixed legacy
source into the fresh isolated target, while carrying `orders` and
`order_goods` as empty structures only; identity tables are not copied. Its
deterministic receipt binds the compiled schema fingerprint and per-table
source/target counts and hashes to the target database marker. The plaintext
source connection is a fixed exception because the immutable source has SSL
disabled; the target always uses verified TLS.

After receipt verification, the reconciler projects imported rows into the
fixed tenant business schema through security-barrier compatibility views.
Tenant-owned views have an immutable tenant predicate. The seven source-global
product and logistics tables are instead seeded as an ID-preserving,
reconciled copy in every tenant business schema; the platform does not retain
their shared writer. The Admin allocation is 50 mall resources plus one tenant
payment resource, and all 51 generic compatibility surfaces are read-only.
Writes require separately qualified domain workflows; the first four-field
Mall Settings workflow is source-implemented but still unverified and
undeployed. A future writable view requires per-resource workflow qualification
and `WITH CHECK OPTION` where PostgreSQL supports it; table shape alone is
never sufficient. The mall role has no
permission to select an arbitrary legacy schema or base table. Permanent dual
write is forbidden. See DEC-0007, DEC-0009 and
`docs/migration/legacy-tables.yaml`.

## Authentication and sessions

- Tenant-platform administrators and mall administrators have separate realms,
  keys, cookies and Redis namespaces.
- A platform operator may manage the lifecycle of a mall runtime but does not
  inherit an active mall session. Cross-platform support access must be a
  separately designed, audited capability.
- H5 and mini-program customer sessions belong to storefront identity, not MSS
  Admin identity.
- Cookies are host-scoped and keys are never shared merely to simulate SSO.

## Deployment shape

The first tenant needs one tenant-platform deployment, one reconciler,
one mall-platform deployment, one storefront API and optional workers. More
tenants add mall runtime/schema pairs without changing the image. This trades
some resource overhead for clear blast-radius, migration and rollback
boundaries.

No GitHub Actions workflow auto-deploys Kubernetes resources. Pull requests
run unit and contract validation and prove four delivery Dockerfiles without
pushing. Pushes to `codex/**` and `main` publish the tenant-platform,
mall-platform, fixed isolated database reconciler and one-time legacy importer
`linux/amd64` images to GHCR. Every image is tagged by the same complete Git
SHA and receives a digest-bound CI receipt. The root proof, storefront API and
worker are not delivery images. Development rollout remains a deliberate
manual action, and every production write needs explicit approval. The exact
package, permission and rollback policy is in
[`ci-images.md`](../runbooks/ci-images.md).

Development work may use the versioned checkout on `167.17.68.242` because the
workstation is resource-constrained. The only write target is the isolated
`mss-shop-dev` namespace with its own PostgreSQL 17.6, Redis 8.6.3, PVCs, TLS,
credentials and default-deny networking. A create-only operator creates an
exact 24-object boundary: every NetworkPolicy precedes two inert scheduling-only
storage binders, and both PVCs must then pass a cluster-wide node/local-path
exclusivity gate before either StatefulSet can be created. A second operator
creates six immutable foundation Secrets only in that namespace.

Every resource in the original `r1shop-dev` environment is immutable. The only
old data path is a constrained, read-only import from
`timescaledb-r1shop-dev.database.svc:5432/r1shop_dev`, plus GET of the exact
database credential and GHCR pull Secret. Old Redis is not shared. The
one-time importer persists a receipt-bound marker; only after a disposable Pod
independently proves that marker and zero target rows in `orders` and
`order_goods` may application/bootstrap Secrets and the reconciler be staged.
A same-revision projection verifier must then prove the fixed four-row Member
Levels slice, default integrity, zero order rows and absence of runtime source
privileges before the eight Admin runtime objects. Production is outside this
workflow. See
[`remote-development-and-dev-acceptance.md`](../runbooks/remote-development-and-dev-acceptance.md).

This remains the target delivery topology rather than completed runtime
acceptance. Its exact 24-object infrastructure boundary, six immutable
foundation Secrets and dedicated PostgreSQL/Redis datastores now exist only in
`mss-shop-dev`, and three revision-bound readiness Jobs passed. Two importer
attempts failed before opening the target transaction; the third completed one
bounded snapshot import and persisted its canonical receipt. A subsequent
revision-B disposable verifier independently proved the 51-table receipt and
zero target rows in `orders` and `order_goods`. Reconciliation Secrets, the
reconciler, Member Levels projection verification, Admin runtime deployment,
complete Kubernetes system acceptance and isolated browser acceptance remain
pending. The original `r1shop-dev` safe
metadata fingerprint is unchanged, and the business matrix is still 0/31.

## Mobile boundary

`mss-shop-mobile` uses classic uni-app Vue3/Vite/TypeScript/Pinia. Phase one
ships H5 and WeChat Mini Program from the same feature code, with platform-only
behavior behind adapters. App is not in the initial manifest, build matrix or
release procedure. If an App later becomes necessary, first evaluate whether
uni-app's hybrid target meets the product requirements; a native React Native
or other client may instead share OpenAPI, domain rules and design tokens.
