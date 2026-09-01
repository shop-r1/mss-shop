# Architecture decision records

Accepted decisions describe why the current architecture exists. Do not rewrite
an accepted record to change history. Add a new record, link `supersedes`, and
update `registry.yaml`, the relevant architecture document and project status
in the same change.

- [DEC-0001](DEC-0001-control-plane-and-tenant-runtimes.md): control plane and
  per-tenant mall runtimes
- [DEC-0002](DEC-0002-fixed-schema-pair-per-tenant.md): fixed core/business
  schema pair per tenant
- [DEC-0003](DEC-0003-dedicated-storefront-api.md): dedicated `/app/v1`
- [DEC-0004](DEC-0004-internationalization-baseline.md): i18n from day one
- [DEC-0005](DEC-0005-exact-public-tenant-bindings.md): exact public Host and
  AppID bindings
- [DEC-0006](DEC-0006-desired-observed-reconciliation.md): desired/observed
  tenant reconciliation
- [DEC-0007](DEC-0007-qualified-legacy-business-contract.md): qualified
  legacy business schemas and compatibility views
- [DEC-0009](DEC-0009-tenant-owned-catalog-and-logistics.md): per-tenant
  product catalogs and logistics rules
- [DEC-0010](DEC-0010-remote-development-and-dev-reconciliation.md): remote
  development and isolated `mss-shop-dev` stage resources
- [DEC-0011](DEC-0011-dns-only-admin-tls-and-host-cutover.md): DNS-only
  ingress TLS bootstrap and isolated Admin host cutover
- [DEC-0012](DEC-0012-pr-head-admin-image-refresh.md): qualifying pull-request
  Admin image refresh in isolated development
