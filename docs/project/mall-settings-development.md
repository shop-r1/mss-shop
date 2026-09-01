# Mall Settings development checkpoint

Status date: 2026-09-02.

## Current status

The first dedicated Mall business workflow is implemented and wired into the
Mall Thin Host. The current release candidate passed the complete remote Go
suite, vet, frontend contract tests, TypeScript/lint, production build, strict
MSS doctor checks and the MSS v1.3.7 ten-check `verify --all` gate. The
published migration `66966149766804` remains byte-for-byte unchanged.

Immutable-image CI, isolated PostgreSQL reconciliation, Kubernetes rollout and
disposable cluster verification now pass at source revision
`3e64a57dae8bb3dd4d337a423015baae6c352b32`. The deployed workflow remains
read-only, and confirmed-login in-app-browser review is pending. This
checkpoint therefore closes none of the 31 business acceptance scenarios and
does not close `CONFIG-001`.

## Typed contract

The dedicated API is `GET|PUT /admin/api/mall-settings/general`. The Admin Web
uses the relative path `/mall-settings/general`. The PUT body remains closed
over exactly four strings. The response contains those four strings plus the
closed server-projected object `operations: { update: boolean }`:

| DTO field | Legacy row | Legacy metadata key | Current meaning |
| --- | --- | --- | --- |
| `mallName` | `name = 'appConfig'` | `mall_name` | Persisted customer-facing mall name. An empty value remains empty; the API does not disguise a runtime fallback as saved data. |
| `orderPrefix` | `name = 'appConfig'` | `ewePrefix` | Legacy reference value for later order-workflow restoration. The old backend has no active consumer, so this slice does not claim that changing it affects order, tracking or label numbers. |
| `defaultSenderName` | `name = 'appConfig'` | `default_sender_name` | Reserved fallback for the future fulfillment workflow. Saving does not call a courier or mutate historical orders. |
| `defaultSenderPhone` | `name = 'appConfig'` | `default_sender_phone` | Phone paired with the future fulfillment fallback sender. |

The frontend and backend limits are respectively 256, 64, 256 and 64 UTF-8
bytes. PUT requires a complete representation and rejects missing, unknown,
non-string, invalid UTF-8 and oversized values. Empty strings explicitly clear
the stored value without invoking any old side effect when a reviewed writer is
available. `operations.update` is not accepted in the PUT body. The Admin Web
combines it with MSS permission state, so an admin/root user still sees a clear
read-only state and no edit/configure button when the server reports `false`.

## Data and security boundary

The row selector is entirely server-issued:

```text
PostgreSQL: <fixed BusinessSchema>.r1_mall_settings_system_configs
SQLite compatibility: <fixed BusinessSchema>.system_configs
tenant_id = binding.LegacyTenantID
name = 'appConfig'
deleted_at IS NULL
```

- Schema, legacy tenant, row name and ID are not HTTP input.
- The reconciler creates the PostgreSQL private relation as a
  `security_barrier=true, security_invoker=false` view over `public.system_configs`
  with the fixed tenant, `appConfig` name and active-row predicates embedded.
  Runtime receives `SELECT` only; INSERT/UPDATE/DELETE and other table
  privileges remain absent.
- A single active row is updated in place with its ID preserved only by the
  explicitly enabled SQLite compatibility writer in this revision.
- No active row creates a fresh 18-digit decimal ID. Soft-deleted rows are
  historical evidence and are never revived.
- More than one active row fails with `409`; no arbitrary first row is chosen.
- The retained PostgreSQL merge path still contains a transaction-scoped
  advisory lock, but the repository rejects PostgreSQL writes before executing
  SQL because its current projection is SELECT-only. SQLite upgrades an
  explicitly enabled transaction to a writer before the absence check.
- Only the four allow-listed JSON keys are replaced. Every other top-level
  value, including unclassified or secret-bearing data, is retained and never
  returned or logged. Malformed, non-object or duplicate-root-key JSON fails
  closed instead of being replaced with defaults.
- The generic `system_configs` compatibility resource remains read-only, and
  its `metadata` remains excluded from search, filtering and sorting.

The MSS permissions are `mall-settings:read` and `mall-settings:update`. Each
non-root request requires the exact Component and API Casbin policies. Forward
migration `66966149766804` projects only MSS menu/policy state; it does not
create or alter any legacy business table and was not rewritten. PostgreSQL
readiness checks the migration, menu, policy, fixed binding, both private and
generic view fingerprints, security options, runtime SELECT and absence of
runtime DML privileges. SQLite readiness continues checking the historical
`system_configs` table.

`R1SHOP_MALL_SETTINGS_WRITE_ENABLED` defaults to false for every absent, empty
or unknown value and is explicitly `"false"` in the isolated development
runtime manifest. Exact `"true"` enables only the SQLite compatibility writer;
PostgreSQL continues returning stable HTTP 503 / `MALL_SETTINGS_WRITE_DISABLED`
with bilingual key `mallSettings.errors.writeDisabled` until a separately
reviewed writable projection and grant are implemented.

## Deliberately excluded settings

The old generic editor also mixed in WeChat identity and AppSecret, payment
accounts, supplier strings, native-App versions, storefront content switches,
order-state switches, logistics remarks, package-weight pricing, gift freight
and referral side effects. None is part of this workflow:

- secrets and integration identity require dedicated binding/rotation DTOs;
- payment fields wait for the accepted payment design;
- product/logistics fields wait for their reviewed domain workflows;
- storefront content waits for a consuming storefront implementation;
- native App settings remain deferred;
- `saveReferrer` is not copied because the old update could clear member
  relationships outside the configuration transaction;
- `getSelfSkipVerify` is not copied because the old code consumes it with
  conflicting polarity.

## Source locations and validation boundary

- Feature contract:
  `apps/mall-platform/.mss/features/mall-settings.yaml`
- Backend:
  `apps/mall-platform/internal/modules/mallsettings/`
- Frontend:
  `apps/mall-platform/web/src/business/mall-settings/`
- Host registration:
  `apps/mall-platform/internal/modules/custom/modules.go`
- Frontend routing and localization:
  `apps/mall-platform/web/src/business/routes.config.ts`,
  `route-registrations.ts`, `locales/zh-CN.ts`, and `locales/en-US.ts`

The remote source gate covers strict response/capability parsing, the
fail-closed mutation gate, qualified relation choice, SELECT-only readiness
invariants, stable 503 mapping, positive and negative authorization, duplicate
active rows, unknown/nested-secret preservation, malformed legacy JSON,
soft-delete behavior, bilingual frontend states and reconciler SQL. The Mall
bundle uses a project-level 930 KiB total compressed-JavaScript budget because
the two typed business workflows add about 12.6 KiB of route chunks; the
validated result is 913.92 KiB. The default 32 KiB entry and 240 KiB largest
asynchronous-chunk limits remain unchanged.

The earlier bounded local-process preview is historical exploration only. It
used the repository-owned demo SQLite database and did not access Kubernetes,
`r1shop-dev`, the isolated PostgreSQL/Redis or production. The current isolated
deployment reads the reconciled fixed relation through a least-privilege role;
cluster HTTP, database-isolation and TLS checks pass. PostgreSQL mutation
semantics and confirmed-login UI acceptance remain open, and the deployed
manifest keeps this workflow read-only.
