---
name: r1shop-legacy-module
description: Implement or review R1Shop Admin business capabilities that must preserve legacy tables, tenant scope, workflows, permissions, or migration evidence. Use for tenant-platform or mall-platform legacy modules; do not use for MSS core changes or storefront-only work.
---

# R1Shop legacy business module

1. Read the scoped Host `AGENTS.md`, its `mss-thin-host` Skill, DEC-0007,
   `docs/migration/legacy-tables.yaml`, and the affected rows in
   `docs/migration/legacy-admin-acceptance-matrix.md`. Treat the 54-table
   manifest as the source of truth; `shipping_warehouses` is required and
   `area` is not a business table.
2. Confirm the domain owner before coding. Tenant lifecycle, legacy identity
   conversion and the eight shared catalog/logistics tables belong to
   tenant-platform. The 43 tenant business tables belong to mall-platform.
   `courier_links` is a global category/packing-rule association, not a mall
   table. MSS owns runtime login, users, roles, menus, policies and logs;
   legacy identity tables are conversion inputs only.
3. Bind the immutable control-plane tenant identity, MSS Admin tenant scope,
   legacy tenant ID, core schema, business schema and shared-catalog schema at
   startup. Validate identifiers and freeze the binding. MSS 1.3.7 currently
   reports the Admin tenant scope as `default`; do not confuse that scope with
   the control-plane tenant identity. A request must never provide or override
   any identity, schema or tenant scope.
4. Use the MSS request database lease only within its callback/request. Keep
   the core schema as the connection's current schema and fully qualify every
   legacy table through the approved repository boundary. Inject the fixed
   legacy tenant predicate for direct and inherited tenant data. Do not call
   `AutoMigrate` on legacy tables or use an unqualified `db.Table` name.
5. Put simple generated resources through the AdminModule workflow only when
   they fit its supported profile. Relations, composite keys, imports,
   workflows, money, row scope and legacy tables require a Feature contract and
   handwritten business code under the Host's owned extension paths. Never
   edit generated files.
6. Add a forward core migration for every handwritten menu, permission and API
   policy. Readiness proves that migration plus the required business schema
   fingerprint before routes mount in deployed mode. Every handler enforces its
   exact backend permission; frontend visibility is advisory. Keep the server
   menu path identical to the Admin Web route, derive component paths as
   `<route>/permissions/<operation>`, and keep the relative HTTP endpoint a
   separate contract. For MSS 1.3.7 dynamic menus, persist one unique dot-free
   root token, register relative directory/leaf names, keep direct and
   hierarchical `menu.*` keys in both locales, and enable `menu.locale` through
   the tested Host facade wrapper. Never rewrite an applied authorization
   migration; correct its menu token in a forward migration and verify it in
   readiness. `web/src/app.tsx` is a Blueprint baseline customization in this
   project and must be reviewed explicitly in every Admin Distribution upgrade.
7. Preserve legacy primary keys, soft deletes, decimal precision, historical
   status values, JSON/CSV bytes and documented model-hook side effects.
   New compatible IDs use the legacy 18-digit decimal format, not UUID or hex.
   The current 43-resource mall catalog and eight-resource tenant shared catalog
   are entirely read-only. Do not enable generic create, update or delete from
   table shape, a local editor smoke test or another resource's qualification.
   Restore old validation, relationships/tenant constraints, hooks,
   authorization and deletion semantics in a dedicated Feature/workflow before
   enabling each operation. Use transactions, state preconditions and
   idempotency where that workflow requires them.
8. Treat passwords, salts, tokens, identity documents, bank accounts,
   payment/courier credentials and nested AppSecrets as sensitive. Accept only
   when a qualified workflow requires them, never return plaintext, and never
   log or commit them. A JSON column with `NestedSecrets`, including
   `system_configs.metadata`, must not participate in free-text search,
   exact/contains/icontains filtering or sorting because result counts can leak
   the presence of a guessed secret.
9. Return business failures with a flat, React-safe envelope containing
   `errorCode`, a non-sensitive fallback `errorMessage`, stable `messageKey`
   and optional scalar `params`. Keep the key in both locale catalogs. During
   compatibility rollout the frontend may accept an older nested envelope,
   but it must reduce every transport value to a string before rendering.
10. Use [references/delivery-checklist.md](references/delivery-checklist.md) for
   implementation and evidence. Keep `zh-CN` and `en-US` complete. Update the
   manifest, acceptance matrix, architecture/status and runbooks in the same
   change when verified behavior or a boundary changes.
11. Production remains read-only without explicit approval for the exact
    action. Database system acceptance runs in disposable Kubernetes Pods
    against an isolated development copy; production order rows are not copied
    into development.
