# Isolated Admin browser acceptance — 2026-09-02

## Status

**DNS-only HTTPS cutover and confirmed-login smoke review passed.** The project
owner supplied action-time authorization to use both generated administrator
passwords. The passwords were read directly from their namespace-local runtime
Secrets and entered into the intended HTTPS login forms without being printed,
copied into evidence or committed. Both browser sessions reached the visible
`/workplace` page as `admin` and remain open for owner review.

This report is intentionally separate from the passing disposable Kubernetes
system checks. HTTP 200, database isolation and Redis/TLS success do not prove
an authenticated business page or close a scenario in the 31-row business
acceptance matrix.

## Fixed review target

- Decision binding: DEC-0011 extends the DEC-0010 isolated stage with the
  DNS-only TLS bootstrap and Admin host cutover.
- Namespace: `mss-shop-dev`
- Tenant platform:
  `https://tenant-admin.mss.r1shop.net`
- Mall platform:
  `https://mall-admin.mss.r1shop.net`
- Deployed source revision:
  `f202b094fd5b2839a9020ff38db833fec40be704`; successful GitHub Actions run
  `33565434916`.
- New-host posture: DNS Only resolves both names directly to `167.17.68.242`.
  The create-only TLS stage and eight-object runtime cutover completed only in
  `mss-shop-dev`; both Ingresses require HTTPS and use exact-host certificates.
- Historical runtime evidence: revision
  `3e64a57dae8bb3dd4d337a423015baae6c352b32` proved one Ready/Available replica
  per platform, zero running Pod restarts and digest-bound images on
  `http://tenant-admin.167.17.68.242.nip.io` and
  `http://mall-admin.167.17.68.242.nip.io`. That evidence remains bound to the
  old hosts and must not be presented as evidence for the planned targets.
- Data posture: fixed-tenant Member Levels projection has four matching rows;
  `orders` and `order_goods` remain structure-only and contain zero rows.

## Executed cutover and smoke evidence

- CI published and the runtime consumed exact tenant and mall image digests
  `sha256:75ed6e8e2b42aad4a88e618f6cd9b2d0197ad12f15392c47ea458b2f3433f39d`
  and
  `sha256:32e9497279393e7cd5bc0896e594f52697cb6092a939f61e1939cb5c86208b50`.
- `stage-admin-tls` passed server dry-run, then created only the reviewed
  NetworkPolicy, Issuer and two Certificates. The Issuer and both Certificates
  reached current-generation `Ready=True`; both certificates expire on
  2026-11-30 and contain only their exact Admin hostname SAN.
- `stage-runtime` passed dry-run, applied the exact eight Admin objects and
  passed an exact post-apply dry-run. Both Deployments rolled out 1/1 Ready.
  Existing Service cluster IPs remained `10.233.31.9` and `10.233.36.137`.
- Public checks followed redirects to an HTTPS login response of 200 for both
  hosts. Plain HTTP returned 308 to the corresponding HTTPS URL; both public
  certificates were issued by Let's Encrypt and validated for the requested
  hostname.
- In the in-app browser, both administrator logins reported success and reached
  `/workplace`. The tenant session visibly exposed its tenant/payment menus;
  the mall session visibly exposed its business and migration-checkpoint menus.
  Both headers identified the authenticated user as `admin`.
- Credential values are intentionally absent. The source records remain
  `Secret/mss-shop-tenant-admin-runtime` and
  `Secret/mss-shop-mall-admin-aussibuy-runtime`, key
  `initial-admin-password`, in `mss-shop-dev`.

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

The HTTPS and confirmed-login smoke gate is closed for these two fixed Admin
hosts. The owner still needs to perform the requested manual visual review, and
the broader route, locale, empty-state and browser-diagnostic checklist above
was not executed in this fast development cutover. Accepted business scenarios
therefore remain **0/31**: this release performs no representative legacy
write lifecycle, checkout/payment execution, order transition, inventory
mutation or rollback test. The original `r1shop-dev` environment and
production are outside the browser target and remain unchanged.
