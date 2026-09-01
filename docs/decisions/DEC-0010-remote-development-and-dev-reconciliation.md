# DEC-0010: Develop remotely and deploy into isolated mss-shop-dev

Status: accepted
Date: 2026-09-01

## Context

The local workstation does not have enough disk or compute headroom for the
complete backend, Admin Web, data-import and container workflow. Development
may therefore use the versioned checkout on `167.17.68.242` and the Kubernetes
cluster available there.

That cluster already contains the original R1Shop development environment. It
is a working reference environment, not capacity reserved for the rebuild.
Sharing its namespace, TimescaleDB, Redis, storage, credentials or network
policy would make a failed migration or framework migration capable of
changing the old application. A new deployment must have an independently
owned failure and data boundary.

The legacy source database has TLS disabled. Treating that limitation as a
general permission to use plaintext or alternate endpoints would make the
import boundary unverifiable. The one source connection therefore needs a
compiled exception, exact network path and read-only transaction contract.

## Decision

### Immutable original environment

- Every resource belonging to the original `r1shop-dev` environment is
  immutable for this project. No application, database, Redis, Secret, PVC,
  Service, Ingress, NetworkPolicy or workload there may be created, updated,
  restarted, scaled or deleted.
- The only legacy database source is
  `timescaledb-r1shop-dev.database.svc:5432/r1shop_dev`. The importer may read
  it through the exact source credential, but may not write DDL, DML, ACLs,
  settings or migration metadata. It has no fallback endpoint.
- Because that fixed source has PostgreSQL SSL disabled, only the legacy
  importer may use `sslmode=disable`. Its Pod is restricted by the exact
  source-egress NetworkPolicy; its startup packet disables event triggers and
  every source transaction is repeatable-read and read-only. All new database
  connections use verified TLS.
- The source catalog boundary is instance-bound. In the authoritative source
  snapshot, the extension inventory is exactly `plpgsql 1.0` in `pg_catalog`
  and `timescaledb 2.20.2` in `public`; all 91 `public` routines must be exact
  object-level TimescaleDB members whose complete ordered `pg_proc` rows match
  the reviewed SHA-256, and standalone `public` types must remain zero. This
  check runs in the same repeatable-read transaction as the table inventory
  and COPY stream. Restore or extension reinstall drift fails closed pending a
  new review.
- The original development Redis is neither a source nor a dependency and is
  not read or shared.
- The trusted foundation-credential operator may perform Kubernetes GETs for
  only the exact legacy database credential Secret and
  `r1shop-dev/ghcr-r1shop-token`. It copies the required bytes into new,
  immutable Secrets without logging them. No other old-environment Secret is
  read.

### Isolated target

- The only development write target is the new `mss-shop-dev` namespace.
  Production and `r1shop-prod` are outside this decision and remain forbidden
  without explicit approval for an exact production action.
- The target owns a vanilla PostgreSQL 17.6 instance and database
  `mss_shop_dev`, Redis 8.6.3, separate PVCs, generated TLS CAs/server
  certificates and independent credentials. It does not reuse either old
  datastore.
- The infrastructure operator runs from a clean checkout for one reviewed
  full Git SHA and an explicit absolute kubeconfig path. It owns exactly 24
  create-only objects: the Namespace, ResourceQuota, LimitRange, two
  ConfigMaps, two PVCs, two Services, nine NetworkPolicies, two inert
  scheduling-only storage-binder Pods, two StatefulSets and two
  PodDisruptionBudgets. It never applies, patches, adopts, deletes or rolls
  back an object. All NetworkPolicies precede the binders; the binders never
  mount either PVC into a process. Both bound PVs must pass two stable global
  inventory snapshots, exact claim/node/provisioner checks and node-local path
  equality-or-nesting exclusion against every other PV before either
  StatefulSet is created. An exact-object retry is allowed; any collision
  fails closed.
- The foundation-credential operator runs only after the 24-object boundary
  exists. It creates exactly six immutable Secrets in `mss-shop-dev`:
  `mss-shop-postgres-auth`, `mss-shop-postgres-tls`,
  `mss-shop-redis-auth`, `mss-shop-redis-tls`,
  `mss-shop-legacy-source-auth` and `mss-shop-ghcr-pull`. It cannot update or
  delete a Secret and never prints a value.
- The datastore Pods may remain pending between infrastructure creation and
  foundation-Secret creation. This ordering is intentional: deny policies
  exist before a Pod can start. PostgreSQL and Redis readiness with strict TLS
  is a mandatory gate before import.

### One-time import and reconciliation

- One create-only, no-ServiceAccount importer Job copies the compiled 51-table
  legacy business inventory into the fresh isolated database. `orders` and
  `order_goods` are created from the reviewed structure but receive zero target
  rows. Legacy `roles`, `tenants` and `users` are not part of the copied
  inventory.
- The importer emits a deterministic, complete receipt containing the
  compiled schema fingerprint and per-table source/target row counts and
  streaming hashes. Row values, DSNs and credentials are absent. Successful
  commit changes the database marker from the exact isolated-empty binding to
  `mss-shop-isolated-dev:legacy-import:v1:<receipt-sha256>`.
- Complete untruncated Job logs, the extracted receipt and its verification
  record must be persisted before the Job TTL expires. The marker suffix must
  equal the verified receipt SHA-256. Receipt loss is a recovery incident, not
  permission to rerun the one-time importer.
- A disposable in-cluster verification Pod independently proves the receipt
  binding and that both `public.orders` and `public.order_goods` contain zero
  rows. Reconciliation cannot start before that evidence passes.
- Only after receipt verification may a separate trusted operator create the
  two Admin application Secrets and the transient reconciler bootstrap Secret
  in `mss-shop-dev`. The command and its tests exist, but successful receipt
  verification remains their deployment gate; no reconciler or Admin runtime
  may be staged before it passes.
- The receipt-bound reconciler Job has no ServiceAccount token or Kubernetes
  API permission. It may reconcile only the fixed isolated roles, core/shared/
  business schemas, compatibility owners, snapshots, views and grants in one
  locked transaction. It must independently require the exact receipt marker
  and zero order-table counts.
- The Admin runtime operator is restricted to the eight named ConfigMap,
  Deployment, Service and Ingress objects in `mss-shop-dev`. It uses immutable
  image digest references, performs collision and dry-run checks, never forces
  field ownership and never adopts an old-environment object.

### Delivery and acceptance

- GitHub Actions is the only delivery-image builder. Pull requests validate
  four `linux/amd64` images without pushing; pushes to `main` or `codex/**`
  publish tenant-platform, mall-platform, reconciler and legacy-importer
  images tagged by the full Git SHA and produce one digest-bound receipt per
  image. CI has no Kubernetes deployment step.
- Every stage write is preceded by a statement naming the namespace, exact
  resources and expected impact. System verification runs in disposable
  one-time Pods in `mss-shop-dev`; UI verification runs in the in-app browser
  and leaves the isolated URLs available for owner review.
- Health, login and generic compatibility reads do not close a legacy business
  scenario. The 31-scenario acceptance matrix remains the authority.

## Consequences

The rebuild can use remote compute without making the original development
environment a staging area. New state has independent storage, credentials,
TLS and network policy, while the one necessary plaintext source connection is
small, explicit and auditable.

Create-only infrastructure and immutable foundation Secrets deliberately make
replacement and rotation separate reviewed lifecycle operations. They prevent
a retry from silently taking ownership of a colliding resource. Receipt-bound
import and reconciliation make data provenance part of the database state
rather than an operator assumption.

As of this decision update, the exact 24-object isolated boundary and six
immutable foundation Secrets exist only in `mss-shop-dev`; PostgreSQL 17.6 and
Redis 8.6.3 are ready, and a revision-bound disposable readiness Job passed.
Two create-only importer Jobs are preserved as failed pre-target diagnostics:
the first exposed a PostgreSQL catalog-alias defect and the second exposed the
over-broad rejection of reviewed TimescaleDB extension routines. Neither
reached the target import transaction, no receipt or imported marker exists,
and a fresh readiness gate is required before a new revision may try once.
No successful data import, reconciler/Admin runtime rollout, Kubernetes system
acceptance or isolated in-app-browser acceptance has completed. Accepted
business scenarios remain 0/31, and the original `r1shop-dev` environment and
production remain unchanged.
