# Legacy catalog and logistics redesign review

Status: awaiting project-owner review
Date: 2026-09-01
Accepted boundary: DEC-0009 already assigns product and logistics data to each
tenant's mall business schema.

## Purpose

The first compatible version must preserve reasonable legacy behavior and old
IDs, but it must not reproduce defects that corrupt references, mix tenants or
make inventory and fulfillment impossible to reason about. This review lists
the remaining product decisions. None of the recommendations below authorizes
a writable business workflow until the project owner approves it and the
corresponding Feature, migration and acceptance cases exist.

## Evidence from the old system

- Creating a central product generated a `goods` row for every tenant, and
  later edits propagated across tenants. See
  `shop-go/app/shop/service/goods/goods.go:550`.
- Updating a product physically deleted and recreated SKU/warehouse relations;
  old carts, order snapshots and inventory references can therefore point at
  unstable identities.
- `goods.inventory`, `goods_specifications.inventory` and `inventories` all act
  as inventory values. One old check omits tenant, SKU and warehouse scope; see
  `shop-go/app/shop/service/goods/goods.go:532`.
- Creating a central courier generated install rows for every tenant; see
  `shop-go/app/shop/service/logistics/courier_install.go:134`.
- `courier_links.link_id` does not state whether it refers to a category,
  product master or product. Its authorization was disabled and deletion used
  the ambiguous ID; see
  `shop-go/app/shop/service/logistics/courier_link.go:48`.
- Courier install reads/updates are not consistently tenant-qualified and
  credentials are stored as ordinary plaintext model fields; see
  `shop-go/app/shop/service/logistics/courier_install.go:77`.
- Packing rules exist in both JSON and `courier_links`, include weakly
  constrained `simple/mixed/mixed_sum` arithmetic, and are evaluated through
  global process cache and hidden model hooks.
- Inventory side effects use an asynchronous Redis path without a durable
  outbox/effect idempotency key, so a repeated message can repeat a stock
  change.

These findings require safe conversion and explicit decisions; they are not a
reason to discard the legacy data contract.

## r1shop-dev read-only graph profile

The current development source was profiled read-only without recording any
business identifier or credential. `courier_links` contains 325 rows. Five
rows have a `link_id` that resolves to neither a category nor a product master;
no row resolves to both. `object_ids_data` represents mixable category IDs:
there are 22 distinct values, with one orphan occurring once. Every observed
`categories.pack_rule` and `goods_infos.pack_rule` value is a JSON array.

The first reconciler preserves those rows verbatim in the tenant snapshot and
locks this aggregate anomaly profile as a drift check. It does not guess a
subject type, delete an orphan, rewrite JSON or claim complete relationship
acceptance. The five ambiguous subjects and the one mixable-category orphan
remain isolated migration debt. Catalog/logistics writes stay closed until
CL-08 and CL-09 are explicitly approved and a dedicated conversion workflow
proves typed subjects, JSON/link reconciliation and rollback.

## Decisions requested

| ID | Decision | Recommendation | Safe behavior before approval |
| --- | --- | --- | --- |
| CL-01 | Merge `goods_infos` and `goods`, or keep two concepts? | Keep two explicit aggregates for the compatible release: tenant-owned product master and channel/sellable product. Remove cross-tenant “adoption”; later consolidation needs measured migration evidence. | Preserve both old IDs and relationships read-only. |
| CL-02 | May an SKU identity change when specifications are edited? | No. Give every SKU a stable ID; normalize option values and apply a diff so unchanged SKUs retain identity. Orders continue to store immutable snapshots. | Do not expose generic specification mutation. |
| CL-03 | What is the inventory source of truth? | `SKU × physical warehouse` balance plus an immutable movement ledger and reservations. Product totals are derived, never independently edited. | Show old values separately and report disagreement; do not silently reconcile. |
| CL-04 | Allow negative inventory or overselling? | Default to neither. Reservation must atomically reject insufficient stock. Any preorder/oversell policy must be a named per-product capability with limits and audit. | Block a write that would make available stock negative. |
| CL-05 | How do assembled products consume stock? | Reserve and release component SKUs from one versioned bill of materials; never maintain an unrelated assembled-product stock counter unless the product is explicitly preassembled. | Preserve old assembly rows and calculate only in comparison tests. |
| CL-06 | Keep both product category and storefront display category? | Yes. Product category classifies the catalog; display category owns navigation. Use a relation table so one product can appear in multiple display categories. | Preserve current single category fields and do not infer extra links. |
| CL-07 | Who defines courier integration methods? | Code releases a reviewed adapter registry; a tenant owns carrier records, accounts, enablement, templates and rules. Manual carriers may be tenant-defined, online carriers must reference a registered adapter. | Unknown old method codes remain disabled and visible in migration reports. |
| CL-08 | How should packing rules be represented? | Preserve a versioned legacy evaluator for migration/golden comparisons, but create new typed condition/action rules with validation. Do not keep JSON and link rows as competing writable truths. | Import both representations, report conflicts and keep the source read-only. |
| CL-09 | How should courier rule scope be modeled? | Replace ambiguous `link_id` with `subject_type + subject_id + rule_id`, qualified inside the tenant schema. | Resolve and quarantine ambiguous/orphan legacy rows; never guess their type. |
| CL-10 | How are courier credentials stored and rotated? | Use a typed write-only DTO and SecretRef/encrypted value, return only mask/fingerprint/version, and bind every operation to the fixed tenant schema. | Redact current fields and keep install mutation disabled. |
| CL-11 | How are cache and cross-module side effects coordinated? | Use explicit domain services, database transaction plus outbox, rule revision and observable cache invalidation. Remove model-hook writes into other modules. | Read through the qualified database contract; no new process-global cache. |
| CL-12 | How is stock-event replay handled? | Every business effect has a tenant-scoped unique key; movement and outbox are committed together; consumers record success before acknowledging. | Do not connect the old non-idempotent queue to new writes. |

The recommended answers form one coherent design. A different answer is valid
when it includes its impact on migration, storefront behavior, permissions,
concurrency and rollback.

## Compatible delivery sequence after review

1. Profile the seven old global product/logistics tables and all tenant
   references in a disposable in-cluster Pod; record row counts, stable-key
   hashes, soft deletes, orphans and JSON/CSV conflicts without secrets.
2. Seed an immutable, complete per-tenant snapshot preserving old IDs. Verify
   idempotent reruns and relationship hashes before switching the qualified
   read repository.
3. Implement focused MSS-style product master, category, brand and attribute
   pages with explicit backend permissions.
4. Implement stable SKU, sellable product, price, display category and assembly
   workflows.
5. Introduce inventory balance, movement, reservation/release and reconciliation
   before checkout can write stock.
6. Implement the courier adapter registry, tenant carrier accounts, typed
   packing/freight rules, templates and warehouse relations.
7. Close catalog and fulfillment acceptance scenarios in the development
   cluster, then connect order and storefront workflows.

The generic compatibility viewer remains a migration aid only. It must not
become the editor for products, SKU, prices, inventory, courier credentials or
packing rules.
