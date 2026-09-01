# Isolated Admin browser acceptance — 2026-09-02

## Status

**Pending confirmed login.** The two isolated Admin runtimes are deployed and
their login pages are open in the in-app browser. No generated administrator
password has been transmitted into either form without the project owner's
action-time confirmation, so no authenticated UI assertion is recorded yet.

This report is intentionally separate from the passing disposable Kubernetes
system checks. HTTP 200, database isolation and Redis/TLS success do not prove
an authenticated business page or close a scenario in the 31-row business
acceptance matrix.

## Fixed review target

- Source revision:
  `3e64a57dae8bb3dd4d337a423015baae6c352b32`
- Namespace: `mss-shop-dev`
- Tenant platform:
  `http://tenant-admin.167.17.68.242.nip.io`
- Mall platform:
  `http://mall-admin.167.17.68.242.nip.io`
- Deployment posture: one Ready/Available replica per platform, zero running
  Pod restarts, digest-bound images, read-only business compatibility surface.
- Data posture: fixed-tenant Member Levels projection has four matching rows;
  `orders` and `order_goods` remain structure-only and contain zero rows.

## Review checklist

After confirmed login, record the visible result and browser warning/error
state for:

1. Tenant platform: tenant list and shared payment catalog.
2. Mall platform: migration checkpoints, Mall Settings, Member Levels and a
   representative catalog/order/settings/storefront compatibility sample.
3. Mutation boundary: no generic create, update or delete control is exposed;
   Mall Settings and Member Levels remain visibly read-only in this release.
4. Localization: switch between `zh-CN` and `en-US` without changing business
   values or exposing raw message keys.
5. Empty-state behavior: the zero-row order views render a stable empty state,
   not an exception or loading loop.
6. Browser diagnostics: no new warning/error console entries or unexpected
   failed requests after the reviewed routes settle.

## Acceptance boundary

Until the authenticated checks above are completed, browser acceptance is not
executed. Even after this smoke review passes, accepted business scenarios
remain **0/31**: this release performs no representative legacy write lifecycle,
checkout/payment execution, order transition, inventory mutation or rollback
test. The original `r1shop-dev` environment and production are outside the
browser target and remain unchanged.
