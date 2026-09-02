# DEC-0011: Use DNS-only ingress TLS for isolated Admin host cutover

Status: accepted
Date: 2026-09-02
Extends: DEC-0010 Admin ingress and browser-acceptance delivery

## Context

DEC-0010 created an isolated `mss-shop-dev` deployment boundary while the two
Admin runtimes used provisional HTTP `nip.io` hosts. Those hosts were suitable
for bounded historical system checks, but administrator passwords must not be
sent through plain HTTP and the permanent review endpoints need stable names.

The selected names are `tenant-admin.mss.r1shop.net` and
`mall-admin.mss.r1shop.net`. They are multi-level subdomains. Their initial
Cloudflare-proxied path did not have suitable edge-certificate coverage, while
the origin had neither explicit certificates for the names nor matching main
Ingress rules. The project owner changed both records to DNS Only so they
resolve directly to `167.17.68.242` and public TLS can terminate at the
development ingress.

Certificate creation and main Ingress host cutover cannot safely be treated as
one precondition. cert-manager must first solve ACME over port 80, but the two
main Admin Ingresses continue to own their historical hosts until the runtime
cutover. Requiring those future Ingresses to complete HTTPS before they own the
new hosts would create a circular gate.

The namespace also has default-deny ingress. A generic ingress exception for
ACME solvers would be too broad, and manually reading or copying generated
private-key material would violate the repository's Secret boundary.

## Decision

- The fixed Admin review endpoints are
  `https://tenant-admin.mss.r1shop.net` and
  `https://mall-admin.mss.r1shop.net`. Their DNS records remain DNS Only and
  resolve directly to the development ingress address. Cloudflare proxy TLS is
  not part of this isolated development route, and plain HTTP is never an
  accepted administrator login transport.
- TLS bootstrap and runtime cutover are separate, full-Git-SHA-bound operator
  stages. Both default to a non-persistent dry-run and require explicit
  `--apply` for their permitted writes. Every write is limited to
  `mss-shop-dev`; the original `r1shop-dev`, production and shared `database`
  namespace remain unchanged.
- `stage-admin-tls` is create-only and owns exactly four bootstrap objects:
  `NetworkPolicy/mss-shop-allow-ingress-nginx-to-acme-http01`,
  `Issuer/mss-shop-dev-letsencrypt-production`,
  `Certificate/mss-shop-tenant-admin-tls` and
  `Certificate/mss-shop-mall-admin-aussibuy-tls`. It may create an absent exact
  object but may not update, adopt, replace or delete a collision.
- The NetworkPolicy selects only cert-manager HTTP-01 solver Pods and permits
  only ingress-nginx controller Pods to reach TCP 8089. It does not add a
  general ingress exception to the default-deny namespace.
- cert-manager owns the Issuer's generated ACME account-key Secret, each
  Certificate's generated TLS Secret, and temporary HTTP-01 solver Pods,
  Services and Ingresses. These are expected controller side effects of the
  four declared bootstrap objects, not extra operator-owned inventory. No
  stage, verification or acceptance workflow reads, decodes, logs or copies
  generated Secret contents.
- After TLS bootstrap apply, all four object specs must match the compiled
  contract and the Issuer plus both Certificates must report Ready.
  `stage-runtime` performs this prerequisite verification read-only before its
  existing resourceVersion-bound update of the two ConfigMaps, two
  Deployments, two Services and two Ingresses.
- Runtime apply switches the two Ingress hosts and `spec.tls` Secret
  references, both HTTPS application/CORS origins, secure browser-session
  cookies and matching migration-domain arguments. The Service data-plane
  contract remains unchanged.
- The runtime pre-apply network gate requires both names to resolve DNS Only
  directly to the ingress address, port 80 to be reachable, all four bootstrap
  specs to remain exact, and the Issuer plus both Certificates to remain
  Ready. It intentionally does not require either future main Admin Ingress to
  complete HTTPS before apply.
- Immediately after runtime apply, each exact hostname must present publicly
  trusted HTTPS, appear in the certificate SANs, route to the intended
  isolated Admin and redirect plain HTTP to HTTPS. Only after all checks pass
  may either generated administrator password be retrieved and entered in the
  in-app browser.
- Revision `3e64a57dae8bb3dd4d337a423015baae6c352b32` and its `nip.io` HTTP
  results remain historical evidence bound only to those former hosts. They do
  not prove the new DNS, certificate, Ingress or authenticated-browser
  contract.

## Consequences

The stable Admin names receive certificates without depending on Cloudflare
edge-certificate coverage. The split stage lets cert-manager complete HTTP-01
issuance before the main host switch while preserving a fail-closed,
create-only TLS bootstrap and a resourceVersion-bound runtime transition.

Certificate renewal and transient solver reconciliation remain cert-manager
responsibilities. Replacing or deleting the solver NetworkPolicy, Issuer or
either Certificate requires a separate reviewed lifecycle decision; this
decision grants no destructive TLS action.

DNS Only and Certificate Ready are necessary but do not themselves constitute
browser acceptance. Authenticated acceptance remains pending until the
post-cutover trusted-HTTPS, SAN, routing and redirect checks pass and the two
authorized login flows are completed. At the time of this decision, no new
Certificate, generated Secret or new-host Admin Ingress deployment is claimed.
