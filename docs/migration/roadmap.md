# Migration roadmap

This is a staged rebuild, not a big-bang rewrite. Production remains read-only
through design, local development and development-environment rehearsal.

## Phase 0 — foundation proof (complete)

- Keep the generated MSS 1.3.7 Thin Host at the repository root runnable.
- Verify login, roles, permissions, bilingual catalogs and the exact Go/npm
  dependency chain.
- Commit no SQLite database, local password, log, build output or package cache.

Exit: the root proof passes `mss doctor --strict` and `mss verify --all` with an
official 1.3.7 tool installation.

## Phase 1 — clean platform shells and contracts (complete)

- Generate `apps/tenant-platform` and `apps/mall-platform` independently with
  MSS 1.3.7. Each directory contains its backend and `web/` frontend.
- Establish the authoritative `/app/v1` OpenAPI contract and tenant bootstrap
  JSON schema under `contracts/app-v1`.
- Complete the code-level boundary audit of `shop-go`, `shop-admin-ui` and
  `shop-m-cli`. The 14 `copymall` administration pages move to mall-platform,
  not mobile.
- Produce the table-level inventory and classify every legacy table by owner,
  retention, translation needs and migration risk before any data migration.

Exit: both hosts validate independently; contract compatibility tests exist;
no copied MSS source is present; the table-level migration inventory is
reviewed and committed. The 54-table manifest, 11/43 owner split, backend route
inventory and Admin acceptance matrix now satisfy this exit. Indexes,
constraints and row-count evidence intentionally remain a Phase 5 development
migration rehearsal gate rather than a Phase 1 source-contract blocker.

## Phase 2 — control plane and reconciler (in progress)

- Implement tenant desired/observed state, immutable tenant keys, domain/AppID
  bindings and lifecycle transitions.
- Implement idempotent reconciler steps for database role, schema pair,
  migrations, runtime configuration and readiness.
- Constrain permissions so the web service cannot execute reconciler DDL and a
  tenant runtime cannot access another tenant's schemas.

Exit: create, retry, suspend and resume work in the development environment;
failure injection converges without duplicate resources or leaked credentials.

The in-memory domain/controller, fault injection and tenant-scoped worker inbox
are implemented. Persistent state, real drivers, least-privilege database roles
and development-cluster rehearsal remain before the exit criteria are met.

## Phase 3 — first mall runtime

- Migrate admin-facing commerce modules to the mall Thin Host.
- Keep MSS core and R1Shop business migrations on their respective schemas.
- Add positive and negative authorization tests for every handwritten handler.
- Prove that changing an incoming tenant header or route cannot change the
  connected schema.

Exit: one development tenant operates end-to-end with fixed connections and
passes isolation tests in disposable Kubernetes Pods.

The fixed-binding 43-resource mall compatibility backend and Admin UI are
implemented and pass local backend tests/vet/race plus frontend tests/lint/
production build. The eight-resource tenant shared catalog has the same local
verification level. All 51 compatibility resources are read-only. Generic
create, update and delete fail closed until each resource independently restores
and proves its legacy validation, relationship/tenant constraints, model hooks,
authorization and deletion semantics. Dedicated business workflows,
development database evidence and disposable-Pod system acceptance remain
open, so Phase 3 is not complete.

## Phase 4 — storefront and mobile (bootstrap slice complete)

- Implement `/app/v1` vertical slices in business priority order.
- Generate or lock the TypeScript client in `mss-shop-mobile` from the contract.
- Port consumer pages only; deliver H5 and `mp-weixin` build/test targets.
- Complete `zh-CN` and `en-US`, including errors and merchant content fallback.

Exit: browse, customer login, cart and an agreed non-production checkout path
work in both targets with contract and locale tests.

The contract-first bootstrap slice is implemented and consumed by both mobile
build targets. Catalog browsing, customer login, cart and checkout remain.

## Phase 5 — rehearsal and cutover

- Run schema/data migration only against dedicated development PostgreSQL/
  TimescaleDB first.
- Copy legacy `orders` structure only into development; do not copy order rows
  unless the user explicitly changes this rule.
- Verify row counts per table, foreign-key or equivalent integrity, money
  totals, translation coverage and rollback checkpoints.
- Design a production dry run, freeze window, compatibility period and rollback
  before requesting approval for any production write.

Exit: the user approves the exact production runbook. No production action is
implied by completing earlier phases.

## Non-negotiable migration checks

- Use immutable tenant identity; mutable names never become schema selectors.
- Every migration is forward, repeatable or checkpointed, and version recorded.
- Backups are restore-tested before destructive or irreversible operations.
- Row counts are recorded after every data-copy phase.
- Secrets are referenced by name only in evidence; values are never committed.
