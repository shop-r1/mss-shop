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
