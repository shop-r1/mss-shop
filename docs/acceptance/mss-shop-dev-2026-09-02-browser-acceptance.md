# Isolated Admin browser acceptance — 2026-09-02

## Status

**User authorized password entry; deferred because HTTPS is not ready.** The
project owner has supplied action-time authorization to use both generated
administrator passwords. Neither password has been transmitted or typed:
both planned records have been changed to DNS Only and now resolve directly to
`167.17.68.242`, but the four-object TLS bootstrap, Certificates and new-host
runtime cutover have not yet been recorded as deployed. The planned hostnames
therefore do not yet have accepted trusted HTTPS; login and all authenticated
assertions remain deferred.

This report is intentionally separate from the passing disposable Kubernetes
system checks. HTTP 200, database isolation and Redis/TLS success do not prove
an authenticated business page or close a scenario in the 31-row business
acceptance matrix.

## Fixed review target

- Decision binding: DEC-0011 extends the DEC-0010 isolated stage with the
  DNS-only TLS bootstrap and Admin host cutover.
- Namespace: `mss-shop-dev`
- Planned tenant platform:
  `https://tenant-admin.mss.r1shop.net`
- Planned mall platform:
  `https://mall-admin.mss.r1shop.net`
- New-host posture: host cutover and trusted HTTPS are not yet recorded as
  deployed or accepted. DNS Only is confirmed, but no successful
  `stage-admin-tls --apply`, Certificate Ready result or new-host
  `stage-runtime --apply` is claimed; no authenticated browser session exists.
- Historical runtime evidence: revision
  `3e64a57dae8bb3dd4d337a423015baae6c352b32` proved one Ready/Available replica
  per platform, zero running Pod restarts and digest-bound images on
  `http://tenant-admin.167.17.68.242.nip.io` and
  `http://mall-admin.167.17.68.242.nip.io`. That evidence remains bound to the
  old hosts and must not be presented as evidence for the planned targets.
- Data posture: fixed-tenant Member Levels projection has four matching rows;
  `orders` and `order_goods` remain structure-only and contain zero rows.

## Review checklist

Before browser login, preserve the two-phase deployment evidence:

1. Run `stage-admin-tls` in its default dry-run mode for the exact full Git
   SHA, then explicitly use `--apply` to create only the four `mss-shop-dev`
   bootstrap objects:
   `NetworkPolicy/mss-shop-allow-ingress-nginx-to-acme-http01`,
   `Issuer/mss-shop-dev-letsencrypt-production`,
   `Certificate/mss-shop-tenant-admin-tls` and
   `Certificate/mss-shop-mall-admin-aussibuy-tls`. The NetworkPolicy permits
   only ingress-nginx controller traffic to solver Pods on TCP 8089. Wait until
   all four specs are exact and the Issuer plus both Certificates are Ready.
2. Treat the Issuer's generated ACME account-key Secret, the Certificates' two
   generated TLS Secrets and temporary cert-manager HTTP-01 solver Pods,
   Services and Ingresses as expected controller side effects. Never read,
   decode, log or copy generated Secret contents, and make no write outside
   `mss-shop-dev`.
3. Run `stage-runtime` in default dry-run mode. It must read-only verify all
   four TLS prerequisites before changing the core eight Admin objects. Its
   apply preconditions are DNS Only/direct resolution, reachable port 80, exact
   prerequisite specs and Ready Issuer/Certificates; the future main Ingress
   HTTPS handshake is intentionally not required before apply.
4. After `stage-runtime --apply` adds the new Ingress hosts and `spec.tls`,
   HTTPS origins, secure cookies and domain arguments, verify trusted HTTPS,
   exact-host certificate SANs, intended-Admin routing and HTTP-to-HTTPS
   redirect for both hosts.

Only after every post-apply TLS/routing check passes may the two administrator
passwords be retrieved and entered. Then record the visible result and browser
warning/error state for:

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

The password-entry authorization and DNS Only change do not close the HTTPS
gate. Until the create-only TLS stage, runtime cutover, trusted-host checks and
authenticated checks above are completed, browser acceptance is not executed.
Even after this smoke review passes, accepted business
scenarios remain **0/31**: this release performs no representative legacy
write lifecycle, checkout/payment execution, order transition, inventory
mutation or rollback test. The original `r1shop-dev` environment and
production are outside the browser target and remain unchanged.
