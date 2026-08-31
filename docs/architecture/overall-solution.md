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

### Reconciler

- Is the sole writer for tenant schemas, database roles, generated credentials
  and mall runtime resources.
- Converts desired state into observed state through idempotent steps, leases,
  retries and recorded checkpoints.
- Produces auditable status without returning secret values to the control
  plane UI.
- Applies MSS core migrations only to a core schema and R1Shop migrations only
  to the matching business schema.

### Mall platform

- Is one build/image deployed once per tenant.
- Starts with an immutable tenant identifier and fixed database connections.
- Uses MSS for administrator identity, roles, menus, Casbin policies, sessions
  and Admin UI composition.
- Uses an explicitly injected business database handle for commerce modules.
- Keeps the MSS connection's current schema limited to the fixed core schema;
  every legacy commerce query uses a startup-bound, validated, fully qualified
  business or shared-catalog object name.
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
r1_m_<tenant-key>_biz    products, inventory, carts, orders and translations
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

During migration, the reconciler may project old rows into the fixed tenant
business schema through security-barrier compatibility views. Tenant-owned
views have an immutable tenant predicate. In the current phase all 43 mall and
eight shared-catalog Admin compatibility resources are read-only. A future
writable view requires per-resource workflow qualification and `WITH CHECK
OPTION` where PostgreSQL supports it; table shape alone is never sufficient.
The mall role has no permission to select an arbitrary legacy schema or base
table. A verified isolated copy can replace a view behind the same qualified
repository contract; permanent dual write is forbidden. See DEC-0007 and
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
run unit and contract validation and prove both delivery Dockerfiles without
pushing. Pushes to `codex/**` and `main` publish only the tenant-platform and
mall-platform `linux/amd64` images to GHCR, tagged by the complete immutable Git
SHA. The root proof, storefront API, reconciler and worker are not delivery
images. Development rollout remains a deliberate manual action, and every
production write needs explicit approval. The exact package, permission and
rollback policy is in [`ci-images.md`](../runbooks/ci-images.md).

## Mobile boundary

`mss-shop-mobile` uses classic uni-app Vue3/Vite/TypeScript/Pinia. Phase one
ships H5 and WeChat Mini Program from the same feature code, with platform-only
behavior behind adapters. App is not in the initial manifest, build matrix or
release procedure. If an App later becomes necessary, first evaluate whether
uni-app's hybrid target meets the product requirements; a native React Native
or other client may instead share OpenAPI, domain rules and design tokens.
