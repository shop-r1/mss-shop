# DEC-0009: Let each tenant own its product catalog and logistics rules

Status: Accepted
Date: 2026-09-01
Supersedes: DEC-0007 shared ownership for `brands`, `categories`, `classes`,
`goods_infos`, `couriers`, `courier_pack_rules`, and `courier_links`

## Context

DEC-0007 qualified the legacy database boundary and recorded the old
deployment's eight source-global commerce tables as tenant-platform shared
data. That classification was safe for an initial read-only compatibility
surface, but it preserves an old operating model in which the platform owns
product masters and logistics rules while a mall merely installs or adopts
them.

The target product model is different: every tenant operates its own mall and
must be able to maintain its own categories, brands, attributes, product
masters, couriers and packing rules. Keeping those records in a platform
schema would couple otherwise isolated tenant runtimes, require cross-schema
projections for ordinary mall work, and make platform access part of every
catalog or logistics change.

The seven affected source tables do not contain `tenant_id`, so the legacy
schema alone cannot express the target ownership. Their existing identifiers
and relationships are nevertheless referenced throughout the tenant commerce
graph and cannot be renumbered or replaced during compatibility migration.

## Decision

The target owner of `brands`, `categories`, `classes`, `goods_infos`,
`couriers`, `courier_pack_rules`, and `courier_links` is **mall-platform**.
Each tenant's records live in that tenant's fixed business schema. The schema
binding supplies the tenant boundary even where the preserved legacy row shape
has no `tenant_id`; clients still cannot select a schema at request time.

Among the 51 non-identity legacy compatibility resources, mall-platform owns
50 and tenant-platform owns one: `payments`. The legacy `tenants`, `roles` and
`users` rows remain tenant-platform migration inputs, so the complete 54-table
owner inventory is tenant-platform 4 and mall-platform 50.

Legacy conversion takes one immutable, qualified snapshot of the seven
source-global tables and seeds a complete referentially consistent copy into
each existing tenant business schema. It preserves primary keys, soft-deleted
rows, status values, decimal values, JSON/CSV bytes and the category/product/
packing-rule graph. Each tenant copy is checked by table count, stable-key hash
and relationship validation before it can replace the read-only source view.
The import is repeatable and checkpointed; it does not establish permanent
dual write. A newly provisioned tenant receives either an explicitly versioned
bootstrap seed or an empty catalog, then owns all subsequent changes.

The tenant platform may request and observe migration/provisioning through the
reconciler, but it does not become a cross-tenant product or logistics editor.
Product and logistics management APIs, permissions and Admin pages belong to
the mall platform. Application code may own a supported courier-adapter
registry, while each tenant owns its courier records, credentials, selection
and packing rules.

The previously published 43-resource mall and eight-resource tenant catalogs
are historical read-only evidence, not the target allocation. The source
implementation moves the seven resources only through forward migrations and
reviewed mall modules; no applied migration may be rewritten, and checked-in
code is not evidence that an environment executed or deployed it. All 51
resources remain read-only until each affected workflow is independently
qualified under DEC-0007.

## Consequences

The mall runtime no longer needs a permanent platform shared-catalog schema in
the target architecture. Catalog, sellable-product, inventory and logistics
relationships can be validated inside one tenant business schema, and one
tenant's changes cannot alter another tenant's assortment or fulfillment
rules.

The old `/tenant/v1` product and logistics endpoints and their permissions are
migration inputs only. Source code replaces them with mall-owned operations and
forward tenant revocations; environment application still requires explicit
migration evidence. They cannot remain writable platform APIs. The historical
`goods_infos` to `goods` adoption flow becomes an intra-tenant
product-master-to-sellable-product workflow rather than a cross-platform copy.

Several legacy shapes remain deliberately unsimplified for the first
compatible release. `goods_infos` and `goods` overlap, category packing data is
duplicated between JSON and `courier_links`, and courier `method` values select
hard-coded adapters. Migration must first preserve and reconcile those
semantics. Collapsing tables, choosing one packing-rule representation, or
redesigning adapter configuration requires a separately reviewed forward
decision and acceptance evidence.

DEC-0007 continues to govern schema qualification, read-only defaults,
sensitive-field handling, migration evidence and the ban on permanent dual
write. This record changes only target ownership and placement for the seven
named tables.
