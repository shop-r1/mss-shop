# Local development runbook

Risk: local checkout only. This procedure does not authorize Kubernetes,
database or deployment writes.

## Prerequisites

- Go and Node versions declared by `.mss/project.yaml`.
- Official `mss` and `mss-mcp` release binaries at `v1.3.7`.
- pnpm version locked by `web/package.json`.

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
