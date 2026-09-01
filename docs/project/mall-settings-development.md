# Mall Settings development checkpoint

Status date: 2026-09-01.

## Current status

The first dedicated Mall business workflow is implemented in source and wired
into the Mall Thin Host. A remote local-process development preview compiled
the backend, started the Admin Web and applied the authorization migration only
to the repository-owned demo SQLite database. It has **not** completed unit or
integration tests, a production build, immutable-image CI, an isolated
PostgreSQL/Kubernetes migration, deployment, or acceptance. This checkpoint
therefore closes none of the 31 business acceptance scenarios and does not
close `CONFIG-001`.

The project owner requested a development-first sequence: continue restoring
the business modules, then perform unit/integration validation, immutable-image
CI, isolated Kubernetes rollout and in-app-browser acceptance after the planned
development work is complete. Earlier Revision C evidence remains valid only
for Revision C; it is not evidence for the current working tree.

## Typed contract

The dedicated API is `GET|PUT /admin/api/mall-settings/general`. The Admin Web
uses the relative path `/mall-settings/general`. Both response and PUT body are
closed over exactly four strings:

| DTO field | Legacy row | Legacy metadata key | Current meaning |
| --- | --- | --- | --- |
| `mallName` | `name = 'appConfig'` | `mall_name` | Persisted customer-facing mall name. An empty value remains empty; the API does not disguise a runtime fallback as saved data. |
| `orderPrefix` | `name = 'appConfig'` | `ewePrefix` | Legacy reference value for later order-workflow restoration. The old backend has no active consumer, so this slice does not claim that changing it affects order, tracking or label numbers. |
| `defaultSenderName` | `name = 'appConfig'` | `default_sender_name` | Reserved fallback for the future fulfillment workflow. Saving does not call a courier or mutate historical orders. |
| `defaultSenderPhone` | `name = 'appConfig'` | `default_sender_phone` | Phone paired with the future fulfillment fallback sender. |

The frontend and backend limits are respectively 256, 64, 256 and 64 UTF-8
bytes. PUT requires a complete representation and rejects missing, unknown,
non-string, invalid UTF-8 and oversized values. Empty strings explicitly clear
the stored value without invoking any old side effect.

## Data and security boundary

The row selector is entirely server-issued:

```text
<fixed BusinessSchema>.system_configs
tenant_id = binding.LegacyTenantID
name = 'appConfig'
deleted_at IS NULL
```

- Schema, legacy tenant, row name and ID are not HTTP input.
- A single active row is updated in place with its ID preserved.
- No active row creates a fresh 18-digit decimal ID. Soft-deleted rows are
  historical evidence and are never revived.
- More than one active row fails with `409`; no arbitrary first row is chosen.
- PostgreSQL uses a transaction-scoped advisory lock for the fixed
  tenant/configuration identity before the absence check. SQLite upgrades the
  transaction to a writer before the same check. This avoids concurrent first
  creates without adding an unreviewed constraint to the legacy table.
- Only the four allow-listed JSON keys are replaced. Every other top-level
  value, including unclassified or secret-bearing data, is retained and never
  returned or logged. Malformed, non-object or duplicate-root-key JSON fails
  closed instead of being replaced with defaults.
- The generic `system_configs` compatibility resource remains read-only, and
  its `metadata` remains excluded from search, filtering and sorting.

The MSS permissions are `mall-settings:read` and `mall-settings:update`. Each
non-root request requires the exact Component and API Casbin policies. Forward
migration `66966149766804` projects only MSS menu/policy state; it does not
create or alter any legacy business table. Readiness checks the migration,
menu, policy, fixed binding and required `system_configs` columns before the
module is ready.

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

## Source locations and deferred evidence

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

The bounded preview ran from the fixed development checkout on the remote host,
used only `apps/mall-platform/mss-boot-admin-local.db`, and exposed loopback
ports through an SSH tunnel. It did not access Kubernetes, `r1shop-dev`, the
isolated PostgreSQL/Redis, or production. Its login page and post-login redirect
to `/business/settings/mall-settings` are available for manual exploration,
but the preview is intentionally not durable acceptance evidence.

Deferred validation must cover strict DTO parsing, positive and negative
authorization, duplicate active rows, first-create concurrency, preservation of
unknown/nested-secret JSON, malformed legacy JSON, soft-delete behavior,
SQLite/PostgreSQL behavior, bilingual frontend states, compilation and the
isolated cluster/UI acceptance sequence. Until that evidence exists, the
machine-readable status remains `source-implemented-unverified`.
