# DEC-0007: Access legacy business data through a qualified schema contract

Status: Accepted
Date: 2026-09-01

## Context

R1Shop must preserve the existing commerce table names, identifiers, status
values, money precision, soft-delete behavior and historical JSON/CSV fields
while moving administration to MSS 1.3.7. MSS exposes one request-scoped
database lease to a business module and runs its own migrations against the
connection's current schema. The legacy installation, however, stores MSS-like
identity names and commerce data together in `public` and separates tenants by
`tenant_id`.

Pointing MSS at that schema would let framework migrations inspect or change
business tables. Accepting a schema in an HTTP request would break the fixed
tenant boundary. Copying every row into a second model immediately would create
a long-lived dual-write problem and make acceptance harder to prove.

The authoritative legacy commerce inventory contains 54 tables: 48 shop/common
models and six ERP models. Existing migration verification scripts list only
53 and omit `shipping_warehouses`; `area` and `OrderOperateLog` are not part of
this inventory.

## Decision

Keep the MSS core schema as the connection's current schema. Every custom
commerce repository uses a schema-qualified table name assembled only from a
startup configuration that has passed a strict identifier validator. The
immutable tenant identity and core, business and shared-catalog schema names
are fixed before the first route is mounted. No header, query, route, token
claim or request body can select any of them.

The business module uses the MSS-provided request database lease, but it never
uses an unqualified legacy table name. Its own MSS menu, permission and Casbin
migration stays in the core schema. Business DDL and the 54-table schema
fingerprint belong to the reconciler's business migration phase.

During the compatibility window, the reconciler may expose old shared-storage
rows through tenant-owned, security-barrier compatibility views in the fixed
business schema. Tenant tables are filtered by the immutable tenant ID and use
`WITH CHECK OPTION` only if a future, separately accepted workflow qualifies a
writable view. The current Admin compatibility layer and its database grants
are read-only for all 43 mall resources and all eight shared-catalog resources.
The mall runtime role receives privileges on the qualified business objects,
not on arbitrary legacy schemas or base tables. An isolated tenant may later
replace a view with a physical table behind the same repository contract after
row-count, hash, relation and aggregate checks pass. There is no permanent dual
write.

The checked-in legacy manifest must enumerate all 54 tables and their owner,
key, tenant scope, soft-delete, sensitive-field and compatibility behavior.
Readiness compares the configured business objects with that manifest and
fails closed in deployed environments. Local demo fixtures may use SQLite's
`main` schema explicitly, but they do not qualify as migration evidence.

Generic mutation is not enabled from table shape alone. Before a resource and
operation become writable, a dedicated Feature/workflow must restore and prove
the legacy validation, relationship and tenant constraints, model-hook side
effects, authorization, conflict/idempotency behavior and deletion semantics.
Until then create, update and delete fail closed. A JSON column with declared
nested secrets is also forbidden as a search, filter or sort key; redaction
alone does not prevent a result-count oracle.

## Consequences

MSS remains unmodified and its migrations cannot discover the commerce tables
through `CURRENT_SCHEMA`. All business SQL must go through the qualified
repository boundary; direct `db.Table("goods")` calls are forbidden. Database
roles and compatibility views become part of reconciler acceptance, including
negative cross-tenant tests.

The first implementation can read existing rows without a destructive copy.
Its generic record viewer is useful for inventory and diagnostics, but it is
not a writer and cannot replace order, inventory, payment, financial or
promotion workflows. Future writable views and model hooks require
domain-specific qualification. Secrets in configuration, payment and courier
tables are omitted or recursively redacted from every response and log; a
nested-secret JSON document is also excluded from query operations.

Production remains read-only until a separately approved runbook supplies a
restore-tested backup, the MSS 1.3.7 permission-collision attestation, schema
fingerprints, row counts, sample hashes, aggregate reconciliation and rollback
checkpoints.
