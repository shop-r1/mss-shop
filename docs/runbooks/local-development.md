# Local development runbook

Risk: local checkout only. This procedure does not authorize Kubernetes,
database or deployment writes.

## Prerequisites

- Go and Node versions declared by `.mss/project.yaml`.
- Official `mss` and `mss-mcp` release binaries at `v1.3.7`.
- pnpm version locked by `web/package.json`.

## Development handoff

Use a `codex/**` branch. Local work owns source edits and focused checks; it
does not deploy Kubernetes resources and it is not shared browser or business
acceptance. Push a completed development slice to its branch, then open a
same-repository pull request to `main` when the accumulated work is ready for
the shared environment. The pull request is the only automatic CI, four-image
publication and `mss-shop-dev` image-refresh path. A branch push without an open
pull request and a push to `main` do not run that pipeline.

After the latest PR head is deployed, perform system verification in disposable
Pods and UI verification in the in-app browser. If a defect requires another
push, the new head invalidates prior acceptance and the shared verification
cycle repeats. Only the latest deployed and accepted head may be squash-merged;
before the final cycle bring the branch up to date with `main`, then record the
accepted head's mapping to the resulting main SHA, matching source tree and
image digests.

Main performs no validation, build or publication. Production promotion is
only a future, human-gated image update: no reviewed `mss-shop` production
namespace, Deployment, RBAC or GitHub Environment exists today. Do not create
or guess one, do not touch the live `r1shop-prod` system, and never approve or
bypass a production Environment review on behalf of a human. A future image
change will recreate Pods and may execute a `migrate` init container; production
container inventory, migration compatibility and rollback must be reviewed
before any executable promotion exists.

## Start the phase-zero Thin Host

```shell
mss --version
mss-mcp -version
mss doctor --strict
mss setup
mss dev --detach
```

On the first migration, supply the initial admin password through the hidden
interactive prompt. In automation, inject `MSS_ADMIN_INITIAL_PASSWORD` only for
the migration process. Never place it in a command argument, shell history,
report or repository file.

Expected endpoints:

- Backend health: `http://127.0.0.1:8080/healthz`
- Admin Web: `http://127.0.0.1:8001/`

Inspect with `mss dev status` and `mss dev logs <service>`. Stop with
`mss dev stop`. SQLite files, logs, run locks, dependencies and builds are local
artifacts and remain ignored.

## Verify the final layout

```shell
mss doctor --strict
GOTOOLCHAIN=go1.26.6 mss --root apps/tenant-platform verify --all
GOTOOLCHAIN=go1.26.6 mss --root apps/mall-platform verify --all
```

MSS 1.3.7 still discovers generated modules below nested Thin Hosts as root
modules, so a root `mss verify --all` reports those files against the wrong
specification root. The root proof uses strict doctor plus its explicit
backend/frontend CI jobs; each final Host owns its complete verification.
Success in one does not substitute for either of the others. These checks still
do not prove future tenant isolation or legacy data migration.

The 1.3.7 source upgrade does not authorize a database migration. Before any
existing 1.3.3-1.3.6 database is migrated, stop writers, take a restorable
backup, and complete the documented permission-collision review required by
forward guard `20260830193000`. Never bypass or pre-mark that guard.

## Final Admin hosts

The final hosts are independent MSS projects. Run only one natively at a time
because both intentionally keep the generated backend/Admin Web ports
`8080/8001`:

```shell
GOTOOLCHAIN=go1.26.6 mss --root apps/tenant-platform doctor --strict
GOTOOLCHAIN=go1.26.6 mss --root apps/tenant-platform setup
GOTOOLCHAIN=go1.26.6 mss --root apps/tenant-platform dev

GOTOOLCHAIN=go1.26.6 mss --root apps/mall-platform doctor --strict
GOTOOLCHAIN=go1.26.6 mss --root apps/mall-platform setup
GOTOOLCHAIN=go1.26.6 mss --root apps/mall-platform dev
```

Each first setup prompts for its own local administrator password and creates
an ignored SQLite file in that app. Do not invent or commit a default password.
Container deployments may use the same internal ports because they have
separate network namespaces.

## Mall Admin local legacy UI fixture

This fixture exists only to make the 50 reviewed mall Admin resource pages
visible during local browser acceptance. It is not a legacy data migration,
does not copy production data, does not prove tenant/schema isolation and does
not close a system or business acceptance scenario. It never contacts
Kubernetes or PostgreSQL and does not use GORM `AutoMigrate`.

Run `mss setup` for `apps/mall-platform` first and choose the local
administrator password through its hidden prompt. The fixture creates no user
and has no default password. Stop the local backend before preparing the file,
then run the following from the `apps/mall-platform` directory:

```shell
GOWORK=off GOTOOLCHAIN=go1.26.6 go run ./cmd/local-legacy-fixture \
  --db "$PWD/mss-boot-admin-local.db" \
  --legacy-tenant-id local-demo \
  --confirm-local-ui-fixture
```

The command accepts only the existing `mss-boot-admin-local.db` regular file
created by setup in the current mall-platform module root; it never creates the
database file itself. The database path, demo tenant value and confirmation
flag are all mandatory. Database URLs, DSNs, directories, symbolic links,
files outside this module and non-SQLite files are rejected before database
work. Existing reviewed tables must already contain
the compiled columns; the command never alters them. Missing tables use
forward-only `CREATE TABLE IF NOT EXISTS`, while demo rows use fixed primary
keys and `ON CONFLICT DO NOTHING`, so rerunning the command neither replaces
nor deletes existing rows.

The fixture inserts non-sensitive examples for `function_circles`,
`message_events`, `message_templates` and `show_categories`, plus examples
covering goods, members, warehouses, orders and inventory. These seeded rows
exist only to exercise lists, details and empty states: all 50 compatibility
resources are read-only in the current Host. The other reviewed tables remain
empty but structurally available for empty-state UI checks.

Use the same fixed row scope when starting the local Host:

```shell
export R1SHOP_TENANT_ID=local-ui-control
export R1SHOP_ADMIN_TENANT_ID=default
export R1SHOP_LEGACY_TENANT_ID=local-demo
export R1SHOP_BIZ_SCHEMA=main
GOWORK=off GOTOOLCHAIN=go1.26.6 mss dev
```

Under DEC-0009 the seven product/logistics resources use the tenant business
schema, so the mall runtime no longer accepts or depends on a shared product/
logistics schema. Local SQLite readiness validates `main`; it does not prove
the forward authorization migrations or tenant data conversion in PostgreSQL.
Use disposable in-cluster tests against development PostgreSQL for system
verification; never point this local command at a migration source or any
production database.

## Storefront bootstrap and mobile H5

Terminal one, from `mss-shop`:

```shell
GOWORK=off go run ./services/storefront-api/cmd/storefront-api
```

Terminal two, from sibling `mss-shop-mobile`:

```shell
corepack pnpm@10.30.1 dev:h5
```

Vite proxies `/app` to `127.0.0.1:8090`. The API resolves the tenant from the
actual Host and ignores client tenant/schema inputs. For `mp-weixin`, configure
an approved HTTPS API base URL and a private development AppID outside Git.

## Platform verification

```shell
GOWORK=off GOTOOLCHAIN=go1.26.6 go test ./...
GOWORK=off GOTOOLCHAIN=go1.26.6 go test -race ./internal/platform/... ./services/...
GOWORK=off GOTOOLCHAIN=go1.26.6 go vet ./...
GOTOOLCHAIN=go1.26.6 sh scripts/check-platform-boundaries.sh
```

These commands use local simulations only. They do not create PostgreSQL
schemas, Kubernetes resources or deployments.
