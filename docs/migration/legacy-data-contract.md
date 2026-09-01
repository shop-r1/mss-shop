# Legacy R1Shop data and backend contract

## 1. Purpose and evidence boundary

This document is the migration baseline for rebuilding the legacy R1Shop
backend on the MSS 1.3.7 architecture without changing the meaning of existing
commerce data. It inventories the legacy administration surface, business
services, data tables, asynchronous work and integrations that the new tenant
platform, mall platform, storefront API and worker must preserve or explicitly
replace.

The source-code baseline is **shop-go commit 82b970f**. Relevant sources are:

- 'shop-go/app/shop/router.go' and 'shop-go/app/erp/router.go' for routes;
- 'shop-go/app/shop/controllers/' and 'shop-go/app/erp/controllers/' for HTTP
  orchestration;
- 'shop-go/app/shop/service/' for legacy domain services;
- 'shop-go/app/shop/models/', 'shop-go/app/erp/models/' and
  'shop-go/common/models/' for persisted contracts and model hooks;
- 'shop-go/cmd/consumer/' for the active Redis-stream consumer;
- 'shop-go/cmd/api/server.go' for startup, timers and integrations;
- 'shop-go/cmd/migrate/server.go:schemaOnlyModels()' for the formal table
  inventory.

On 2026-09-01 a read-only inspection of the development TimescaleDB confirmed
that its 'public' schema contains the 54 formal legacy business tables listed
below. It also confirmed that 'public.shipping_warehouses' has 16 columns.
Temporary schemas named 'compression_test_*' also exist. No credentials or row
values were read into this document.

The same fixed source currently has exactly `plpgsql 1.0` in `pg_catalog` and
`timescaledb 2.20.2` in `public`. Its `public` schema has 91 routines, all
object-level members of that exact TimescaleDB extension, and zero standalone
types after table-row arrays are excluded. The importer binds the complete,
OID-ordered `pg_proc` rows to SHA-256
`32c0b88f3178e4a15647eef85da4a718b4e490070bd7fa2c77876101f386d81e`
inside the same read-only repeatable-read snapshot used for the table import.
Any extra or missing extension, routine, standalone type or changed routine
catalog row fails closed. This fingerprint deliberately binds the current
source database instance; a dump/restore, extension reinstall or catalog OID
change requires a new read-only review instead of silently reusing it.

This is a contract and assessment, not authorization to write production or
development data. Production remains read-only. Current workspace policy also
forbids copying 'orders' rows into development; development rehearsals may
create its structure only unless that rule is explicitly changed.

## 2. Binding and ownership decisions

The compatibility layer must use fully qualified, server-owned schema
bindings. For the inspected legacy database, its source allow-list is exactly
'public.<table>' for the 54 tables below. It must never enumerate all schemas,
inherit an uncontrolled 'search_path', bind to 'compression_test_*', or accept
a schema name from a request, JWT, header or user input.

Target ownership is:

- **Control plane**: the tenant platform's MSS core schema plus its separate
  control-business schema. 'tenants' becomes a custom control-plane aggregate.
  Platform identities use MSS, not the legacy identity tables.
- **Tenant payment catalog**: `payments` remains source-global control-business
  data. It is the only one of the 51 non-identity compatibility resources owned
  by tenant-platform; tenant payment installations continue to reference its
  stable IDs.
- **Tenant-owned product and logistics data**: `brands`, `categories`,
  `classes`, `goods_infos`, `couriers`, `courier_pack_rules` and
  `courier_links` belong in every tenant's fixed mall business schema. Their
  legacy source rows have no tenant key, so conversion seeds a complete,
  ID-preserving snapshot into each tenant schema before that tenant maintains
  its own data. The platform does not provide a permanent shared writer or
  cross-schema catalog projection.
- **Mall MSS core**: each tenant's legacy 'roles' and 'users' are transformed
  into that mall's MSS identities, permissions and policies. They are not
  copied as commerce tables merely to keep old authentication alive.
- **Mall business schema**: all tenant-owned commerce tables retain their
  legacy table and column names during the compatibility period. Keep
  'tenant_id' even though one schema belongs to one tenant; validate it on
  import and use it as defence in depth.
- **Inherited tenant ownership**: tables without 'tenant_id' but whose parent
  is tenant-owned ('goods_assembles', 'goods_shipping_warehouses',
  'order_goods', 'coupon_links') inherit the parent's tenant. Import must prove
  that both sides resolve to the same tenant.

For migration ownership and the 54-table completeness gate, the target count
is **tenant-platform 4 / mall-platform 50**. Tenant-platform owns the three
control/identity migration inputs plus `payments`; mall-platform owns the other
50 tables. Transforming legacy `roles/users` into the appropriate MSS identity
realm does not make those source rows mall business data.

DEC-0009 replaces the old shared product/logistics projection choice. The
seven source-global product and logistics tables are imported into each fixed
tenant business schema with preserved IDs and relationship checks. Direct
cross-schema writes from a mall runtime remain forbidden.

### 2.1 Current Admin compatibility safety boundary

The compatibility allocation is 50 mall resources plus the one tenant payment
resource. All 51 **generic compatibility resources** remain read-only: they
advertise no create, update or delete capability, and their generic mutation
endpoints fail closed. A separate four-field Mall Settings workflow is now
implemented in source for the active `system_configs.appConfig` row; its GET
response also projects closed `operations.update` state and it does not change
the generic resource capability. PostgreSQL reads through the dedicated fixed-
tenant/name/active `r1_mall_settings_system_configs` security-barrier view with
runtime SELECT only. Cluster writes default closed and return a stable 503;
the generic nested-secret redaction and metadata query prohibition are
unchanged. A demo-SQLite development preview
starts, but the workflow remains without formal tests, production build,
isolated PostgreSQL/Kubernetes migration, deployment or acceptance. Source code
contains forward-only route and permission migrations for this allocation, but
they have not been applied or deployed to any environment. The prior published
43 mall/eight tenant projection remains historical evidence, not an alternate
ownership contract. The earlier four-plus-four writable classification is
withdrawn.

Before one resource and operation can become writable, its dedicated
Feature/workflow must restore and prove the old validation, relationships and
tenant constraints, model-hook side effects, authorization, conflict and
idempotency behavior, and deletion semantics. Passing table-shape checks or a
local SQLite editor smoke test is insufficient.

`system_configs.metadata` contains nested AppSecrets. It is recursively
redacted on output and cannot be used in free-text search,
exact/contains/icontains filters or sorting. Otherwise result counts could
confirm whether a guessed secret occurs inside the JSON document.

## 3. Migration blocker: omitted shipping warehouses

'shop-go/cmd/migrate/server.go:schemaOnlyModels()' contains 48 shop/control
models and six ERP models, for 54 formal tables. Three migration scripts list
only 53 tables and omit 'shipping_warehouses':

- 'shop-go/scripts/plan-mysql-to-timescaledb-migration.sh';
- 'shop-go/scripts/verify-migration-row-counts.sh';
- 'shop-go/scripts/verify-migration-sample-hashes.sh'.

This is a hard blocker, not a documentation discrepancy. The table connects
tenant goods pricing, checkout, orders, couriers, sales and ERP real
warehouses. A migration that omits it can preserve row counts elsewhere while
producing an unusable mall.

Before any new data-copy rehearsal:

1. add 'shipping_warehouses' to the copy, count and hash manifests;
2. assert the exact 16-column source contract listed in table row 17 below;
3. verify every 'goods_shipping_warehouses.warehouse_id',
   'orders.warehouse_id', 'sells.warehouse_id' and
   'inventory_tracks.shipping_warehouse_id' reference;
4. verify embedded courier JSON, payment CSV values and real-warehouse links;
5. make a 54-table allow-list assertion fail closed if any table is absent or
   an unexpected schema is selected.

Existing first/last-'id' sample hashing is also insufficient for composite-key
tables and tables without an 'id'. The replacement verifier must hash every
table by its real primary or stable business key.

## 4. Backend capability inventory

The CRUD and action names in this section inventory the frozen legacy source
requirements. They are not claims that the current MSS compatibility routes
are writable. The current 51-resource compatibility surface remains read-only
under section 2.1 until each operation is separately qualified.

### 4.1 Route totals

Static registration in the baseline contains:

| Surface | Registered routes | Authentication boundary | Target owner |
| --- | ---: | --- | --- |
| '/auth' | 2 | tenant resolution; legacy admin token flow | MSS admin authentication |
| '/tenant/v1' | 58 fixed routes plus one runtime-selected upload route | 'Tenant', 'AuthorizationTenant' | historical source: tenant lifecycle/identity/payment plus product/logistics routes that move to mall-platform |
| '/app-admin/v1' | 129 | 'Tenant', 'AuthorizationTenant' | mall platform business modules |
| '/erp/v1' | 18 (15 ERP router plus three sales routes) | 'Tenant', 'AuthorizationTenant', ERP flag | mall platform ERP modules |
| '/app/v1' | 69 (64 protected-group registrations plus five public registrations) | optional/required member identity depending on operation | storefront API |
| '/wechat/*' | 3 | mixed public/tenant bootstrap | storefront identity adapter |
| payment callback | 1 'Any' route | path token and tenant resolution | storefront payment callback |
| '/operator/goods-info' | 1 | none in the legacy router | remove or protect; never reproduce unauthenticated export |

Commented-out routes are not counted. The dynamic tenant upload route is local
storage or object storage depending on configuration and must be registered
exactly once.

### 4.2 Historical tenant routes and target ownership split

Source: 'shop-go/app/shop/router.go', 'shop-go/app/shop/controllers/' and
'shop-go/app/shop/service/tenant/'.

- Authentication: 'POST /auth/token', 'POST /auth/revoke'.
- Current administrator: 'GET /tenant/v1/user/info'.
- Tenant lifecycle: CRUD '/tenant/v1/tenants[/:id]'.
- Legacy roles and users: CRUD '/tenant/v1/roles[/:id]' and
  '/tenant/v1/users[/:id]'.
- Product and logistics routes that move to mall-platform: CRUD
  '/tenant/v1/couriers[/:id]' and '/tenant/v1/courier-pack-rules[/:id]'; CRUD
  '/tenant/v1/brands[/:id]', brand import, CRUD
  '/tenant/v1/categories[/:id]', CRUD '/tenant/v1/goods-infos[/:id]', goods
  master import, and CRUD '/tenant/v1/classes[/:id]'.
- Tenant payment definitions: CRUD '/tenant/v1/payments[/:id]'.
- Tenant UI entries: CRUD '/tenant/v1/function-circles[/:id]'; the same
  business entity is also exposed to mall administration.
- Files: 'POST /tenant/v1/files/upload' backed by either local storage or the
  configured S3-compatible store.

Tenant CRUD, lifecycle, domain bindings, ERP entitlement and payment catalog
remain custom tenant-platform capabilities. Product and logistics operations
become mall-platform capabilities under DEC-0009; the old tenant routes are
migration inventory, not authorization to keep platform-owned writers. MSS can
replace admin identity and system administration, but it is not a
tenant-provisioning, commerce-catalog or payment product.

### 4.3 Mall administration surface

Source: 'shop-go/app/shop/router.go', 'shop-go/app/shop/controllers/' and the
domain services under 'shop-go/app/shop/service/'.

- Display categories: CRUD and import for
  '/app-admin/v1/show-categories'.
- Product masters and logistics rules: preserve the old brand/category/class/
  goods-master and courier/packing-rule operations behind mall-owned routes;
  `courier_links` remains the hidden relationship side effect.
- Goods: CRUD, batch adoption, enable/disable, shelf/unshelf, topping, compact
  item lookup and price import/export under '/app-admin/v1/goods*'.
- Shipping warehouses: CRUD '/app-admin/v1/shipping-warehouses[/:id]'.
- Member levels: CRUD plus 'PATCH'; members: CRUD, import and finance
  statistics.
- Tenant UI: CRUD function circles.
- Courier and payment installations: install, uninstall, update, read and list.
- Sender/consignee administration: CRUD/list/read plus duplicate-existence
  checks.
- Orders: list/read/update/group, package edit, RC import, state advance, batch
  ship, batch print, financial audit, courier push, package-weight update,
  courier-number import and payment-method update.
- Finance: gold recharge, balance recharge, ledger list and ledger statistics.
- Marketing activities: CRUD/list/read and eligible-link lookup.
- Courier price templates: CRUD/list/read.
- Tenant configuration: create/update/delete/read/list by configuration name.
- Withdrawals: list/read, approval and paid confirmation.
- Sales: sales list/create/settle and sales-goods list.
- Coupons: parent-coupon CRUD, send and rollback; issued-coupon list/delete.
- Message automation: message-event CRUD and message-template CRUD.
- Dashboard: statistics and to-do counts.

These are commerce capabilities and must be implemented as business-owned MSS
extensions. The current adapter exposes only their read-side compatibility.
MSS-generated or handwritten CRUD may be enabled later only where the complete
legacy validation, relation, tenant, hook, authorization and deletion contract
has been restored; order state machines, imports, exports, financial actions,
integrations and data scopes require dedicated handwritten features.

### 4.4 ERP surface

Source: 'shop-go/app/erp/router.go', 'shop-go/app/erp/controllers/' and
'shop-go/app/erp/models/'.

- Real warehouses: create, delete, update, search and read.
- Inventory: search, single item read and initial-inventory export.
- Inventory checks: create a check and asynchronously fan it out.
- Inventory tracks: search.
- Purchase receipts: search, read, create and mark payment.
- Receipt goods: search.
- Sales borrowed from the shop router: sales list/create/settle.

ERP entitlement is historically 'tenants.erp'; the new design needs an
explicit tenant capability plus MSS permissions and warehouse row scopes.

### 4.5 Storefront rules that share the same business data

The administration rebuild cannot be accepted only against admin pages. The
same tables and state transitions are consumed by '/app/v1':

- public/member authentication, registration, password reset and profile;
- public configuration, file upload/presign, WeChat login, mini-program auth
  and JSSDK;
- function circles, display categories, goods, activity pricing, inventory
  checks and share posters;
- collections and carts;
- sender and consignee address books;
- order preview from goods/cart, order list/read/cancel/status, package trace,
  eligible payments, voucher upload, balance/gold and payment-order creation;
- member finance ledger, activities and coupons;
- reseller goods/pricing, reseller consumers, consumer level changes and
  reseller order advancement;
- withdrawal application.

The new storefront contract under 'contracts/app-v1' is authoritative; legacy
paths are an input to compatibility tests, not permission to expose MSS Admin
APIs to storefront clients.

## 5. MSS replacement and custom migration split

| Legacy capability | Disposition | Required migration work |
| --- | --- | --- |
| Admin login, sessions, current user, users, roles, menus, API permissions, dictionaries and audit logs | Use MSS unchanged | Transform legacy users/roles; create explicit business permissions and policies; do not keep the legacy JWT as the primary admin realm |
| 'roles.privilege', 'roles.privilege_erp' | Replace UI-oriented JSON with MSS permission metadata | Preserve raw source for audit, map every enabled operation, add positive and negative backend tests |
| 'roles.warehouse_ids', 'roles.real_warehouse_ids' | Custom data scope | Normalize/validate CSV IDs and enforce them in backend queries; UI filtering is not authorization |
| Tenant record, domains, expiry, ERP entitlement and desired/observed state | Custom tenant-platform module | Transform 'tenants'; the reconciler alone creates schemas, roles, credentials and runtimes |
| Brands, categories, classes, goods masters and courier rules | Custom mall catalog/logistics modules | Seed each tenant schema from the qualified legacy snapshot; preserve IDs and relationships, then let that tenant maintain its own data |
| Payment definitions | Custom tenant-platform payment catalog | Preserve stable IDs and migrate only the separately approved payment methods and adapter contract |
| All other tenant commerce and ERP domains | Custom mall/storefront/worker modules | Preserve the remaining tenant table contracts until a separately versioned migration changes them |
| 'messages', 'message_users' | Dormant in active admin routing; candidate for archive or MSS notification mapping | Preserve data until retention and user mapping are approved; do not silently drop |
| 'message_events', 'message_templates' | Business event configuration | Migrate as custom notification rules/templates; keep tenant ownership and status |
| 'system_configs' | Business configuration, not an MSS dictionary | Keep generic Admin compatibility read-only; use dedicated closed DTOs for reviewed keys; preserve raw JSON only in the private business schema; recursively redact nested secrets and exclude `metadata` from search, filtering and sorting. The four-field general-settings source slice starts in a demo-SQLite preview but is not formal validation or deployment evidence. |

Legacy user and member passwords use scrypt with parameters 'N=16384', 'r=8',
'p=1', a 32-byte derived key and a separately stored salt
('shop-go/pkg/security.go'). Admin migration needs either a one-login
compatibility verifier followed by MSS rehash, or a controlled password reset.
Never reinterpret the hashes as an MSS-native format.

## 6. Authorization and security behavior to replace

The old route middleware authenticates an administrator and resolves a tenant,
but it does not implement route-level role authorization. Most service
'checkAuth' methods only test that a tenant is enabled, that a user/member
exists, or that the current tenant is the system tenant. 'Privilege' and
'PrivilegeErp' are largely returned to the UI. Warehouse ID lists are not a
uniform server-side scope.

The new implementation therefore must:

1. define a distinct backend permission for every handwritten operation;
2. create permission metadata and default policies through forward migrations;
3. require the injected MSS principal in each handler;
4. enforce tenant and warehouse/real-warehouse scope in the business query;
5. include negative tests for a valid user without the permission, a user with
   the wrong warehouse scope and a principal from another tenant;
6. keep a mall runtime fixed to one core/business schema pair at startup;
7. keep all 51 generic compatibility resources read-only; every write must use
   a dedicated workflow that separately passes its legacy-semantics
   qualification;
8. exclude any JSON column with declared nested secrets from free-text search,
   exact/contains/icontains filtering and sorting.

Unsafe legacy behavior is evidence for regression tests, not behavior to port:

- admin and member revoke functions are effectively no-ops;
- JWT signing combines a tenant secret with source-code constants and has no
  server-side revocation;
- tenant selection uses a Redis host cache and, in one release-mode localhost
  branch, may replace Host with 'Accept';
- copied-mall behavior can derive context from path, Referer and 'member_id';
- many errors are returned with HTTP 200;
- 'GET /operator/goods-info' is unauthenticated;
- the payment callback embeds its verification token in the path;
- several outbound HTTP calls use default clients or plain HTTP without an
  explicit timeout.

### 6.1 Admin compatibility error contract

Handwritten tenant-platform and mall-platform compatibility APIs use HTTP
status codes and a flat JSON error envelope:

```json
{
  "errorCode": "VALIDATION_FAILED",
  "errorMessage": "Legacy input validation failed",
  "messageKey": "legacy.errors.validationFailed",
  "params": { "field": "name", "rule": "required" }
}
```

`errorCode` is stable for automation, `messageKey` is stable for the complete
`zh-CN` and `en-US` catalogs, and `errorMessage` is a safe non-sensitive
fallback. `params` may contain only scalar formatting values. The frontend may
temporarily read the earlier nested `error` object while old processes drain,
but it must reduce unknown or malformed responses to a string before passing
them to React. Business failures must never return an arbitrary database error,
credential, schema name or response object to the UI.

## 7. Persisted table contract and acceptance matrix

### 7.1 Common conventions

'gdb.GModel' contributes string 'id', 'created_at', 'updated_at' and nullable
'deleted_at'. Rows with 'deleted_at IS NOT NULL' are soft-deleted history and
must be counted and hashed separately from active rows. 'gdb.ModelCreate'
contributes 'id' and 'created_at'; 'gdb.ModelUpdate' additionally contributes
'updated_at' but neither supports soft delete.

Acceptance codes used below are:

- **N**: total, active and soft-deleted counts as applicable, plus a canonical
  full-row hash ordered by the real key;
- **K**: primary/unique key and duplicate-profile check against actual DDL;
- **P**: tenant ownership check, including inherited ownership;
- **R**: relationship/orphan check;
- **J**: JSON validity and exact CSV/text round-trip check;
- **E**: distinct enum/status values profiled and preserved;
- **M**: exact decimal and signed aggregate reconciliation;
- **T**: timestamp/null/time-zone/ordering reconciliation;
- **S**: secret/PII classification, encrypted target handling and redacted
  evidence;
- **B**: domain invariant or state-machine check named in the row.

Actual PostgreSQL DDL from 'pg_catalog' is authoritative. GORM v1 tags document
intent but are not proof of the deployed primary key, nullability, type,
default, index or constraint.

### 7.2 Control and identity (3 tables)

| # | Table and source | Target ownership | Key fields and relations | Lifecycle and historical semantics | Required checks |
| ---: | --- | --- | --- | --- | --- |
| 1 | 'tenants' — 'common/models/tenant.go' | control business schema; transform | 'id'; 'name', 'system', 'expired', 'status', 'domain/domain1/domain2', 'erp', 'tag', secret; parent of all 'tenant_id' values | soft delete; status normally 1 enabled/2 disabled, while ERP history may contain 0/1/2; expiry is access control; domains and tag are mutable business identifiers, never schema names | N,K,R,E,T,S,B: every tenant-scoped row resolves to one tenant; active domain bindings are unique after normalization |
| 2 | 'roles' — 'common/models/role.go' | transform into each MSS core realm | 'id', 'tenant_id'; 'privilege' and 'privilege_erp' JSON; warehouse ID CSV fields; referenced by 'users.role_id' | soft delete; status history may use 0 despite current 1/2 enum; permissions were UI-oriented and cannot be copied as authoritative policies | N,K,P,R,J,E,B: raw-to-MSS permission mapping reviewed; all warehouse IDs resolve within tenant |
| 3 | 'users' — 'common/models/user.go' | transform into control or tenant MSS core realm | 'id', 'tenant_id', 'role_id'; 'global_username', 'username'; password hash/salt/reset hash; 'status', 'open_id' | soft delete; legacy scrypt authentication; global username is source-unique and may encode tenant tag | N,K,P,R,E,S,B: duplicate/case profile; role mapping; compatibility-login or reset path proves every enabled account outcome |

### 7.3 Source-global product, logistics and payment data (8 tables)

| # | Table and source | Target ownership | Key fields and relations | Lifecycle and historical semantics | Required checks |
| ---: | --- | --- | --- | --- | --- |
| 4 | 'brands' — 'app/shop/models/brand.go' | tenant business schema; seed per tenant | 'id', Chinese/English names, media URLs, sort, status; referenced by goods masters and tenant goods | soft delete; bilingual source fields are business content, not application locale keys | N,K,R,E,B: both names and media preserved; every brand reference resolves in the same tenant schema |
| 5 | 'categories' — 'app/shop/models/category.go' | tenant business schema; seed per tenant | 'id', 'parent_id', name/alias, image/tag/sort, 'pack_rule' JSON; parent of classes and goods masters | soft delete; hierarchy uses logical IDs; pack rules are also expanded into 'courier_links' | N,K,R,J,B: no unexpected cycles; JSON and link-table representations reconcile per tenant |
| 6 | 'classes' — 'app/shop/models/class.go' | tenant business schema; seed per tenant | 'id', 'category_id', name, 'attributes' JSONB, status | soft delete; attributes contain radio/multiple groups | N,K,R,J,E,B: every class category exists in the same tenant schema; attribute structure round-trips |
| 7 | 'goods_infos' — 'app/shop/models/goods_info.go' | tenant business schema; seed per tenant | 'id', category/parent/brand IDs, name/barcode, album CSV, image/video/content, weight/unit/type, pack-rule JSON; referenced by 'goods' and 'goods_assembles' | soft delete; no separate status column; type 0 normal/1 assembled; CSV album order is presentation-significant | N,K,R,J,E,B: all master references resolve in the same tenant schema; album and packing rules preserve order/content |
| 8 | 'couriers' — 'app/shop/models/courier.go' | tenant business schema; seed per tenant | 'id', name, region, method, status; parent of pack rules and tenant installs | soft delete; 'method' selects application-owned adapter behavior while the tenant owns configuration | N,K,R,E,B: distinct methods are supported or explicitly retired; every rule/install resolves in the same tenant schema |
| 9 | 'courier_pack_rules' — 'app/shop/models/courier.go' | tenant business schema; seed per tenant | 'id', 'courier_id', simple/mixed/mixed_sum, 'price_unit', 'price_total' | soft delete; money is 'DECIMAL(10,2)'; quantities drive package composition | N,K,R,M,B: rule arithmetic fixtures match legacy packing results per tenant |
| 10 | 'courier_links' — 'app/shop/models/courier_link.go' | tenant business schema; seed per tenant | deployed key around 'id', 'link_id', 'left_rule_id'; 'object_ids_data' CSV; links category/goods-master objects to rules | no soft delete; created-at only; GORM tags mark multiple primary fields, so deployed DDL must decide the real key | N,K,R,J,B: unique triples; left rule and all linked/mixable object IDs resolve in the same tenant schema |
| 11 | 'payments' — 'app/shop/models/payment.go' | tenant-platform payment catalog | 'id', name, 'method', status, type, 'terminals' CSV; parent of tenant installs | soft delete; type balance/online/voucher; method dispatches a payment adapter | N,K,R,J,E,B: every historical method has an explicit migration disposition and supported terminal set |

### 7.4 Tenant product, display and warehouse data (7 tables)

| # | Table and source | Target ownership | Key fields and relations | Lifecycle and historical semantics | Required checks |
| ---: | --- | --- | --- | --- | --- |
| 12 | 'show_categories' — 'app/shop/models/show_category.go' | tenant business schema | 'id', 'tenant_id', 'parent_id', name/image/status/sort; referenced by goods parent/display IDs | soft delete; tenant hierarchy; status is display enable/disable | N,K,P,R,E,B: no cross-tenant parent or goods link; no unexpected cycles |
| 13 | 'goods' — 'app/shop/models/goods.go' | tenant business schema | 'id', 'tenant_id'; tenant product-master IDs; show-category IDs; alias/barcode/media; 'commission_rmb'; inventory flags/counts; stage/specification/metadata JSON; album/payment CSV; show/status/type; child specifications, warehouses and assemblies | soft delete; GORM marks both inherited ID and tenant as primary; 'show' and 'status' use 1/2; money is decimal; 'BeforeUpdate' physically deletes and recreates warehouse/spec rows | N,K,P,R,J,E,M,T,B: complete child graph, inventory/sales counters, display state and price fixtures match |
| 14 | 'goods_assembles' — 'app/shop/models/goods.go' | tenant business, inherited from parent goods | 'id', 'link_id' parent goods, component 'goods_id', 'goods_info_id', quantity, denormalized name/image | no soft delete; created-at only; replacement update semantics | N,K,P,R,B: parent/component belong to same tenant; positive quantities; cycle profile recorded |
| 15 | 'goods_shipping_warehouses' — 'app/shop/models/goods.go' | tenant business, inherited from goods and warehouse | 'id', 'goods_id', 'warehouse_id', price, default flag, member-level-price JSON | no soft delete or timestamps in the model; 'price' is decimal | N,K,P,R,J,M,B: goods and warehouse tenants agree; member-level IDs resolve; default-selection profile preserved |
| 16 | 'goods_specifications' — 'app/shop/models/goods.go' | tenant business schema | 'id', 'tenant_id', 'goods_id', name/barcode, specification CSV, ratio, album, inventory, default flag | soft delete; ratio is decimal and specification item order is significant; goods updates physically purge/recreate rows | N,K,P,R,J,M,B: one-to-many graph and default profile; exact CSV and inventory totals |
| 17 | 'shipping_warehouses' — 'common/models/shipping_warehouse.go' | tenant business schema | exactly 16 confirmed columns: 'id', 'created_at', 'updated_at', 'deleted_at', 'tenant_id', 'name', 'currency', 'region', 'address', 'status', 'get_self', 'need_id_card', 'couriers_data', 'custom_pay', 'payment_ids', 'real_warehouse_id' | soft delete; currency AUD/CNY; 'couriers_data' JSON and payment IDs CSV; deletion is rejected when orders or goods links exist | N,K,P,R,J,E,T,B: 16-column assertion; all courier/payment/real-warehouse links; every goods/order/sale reference; delete-guard fixtures |
| 18 | 'function_circles' — 'app/shop/models/function_circle.go' | tenant business schema | 'id', 'tenant_id', title/type/status, background/media/video, 'link_type/link_id', content/url/sort | soft delete; link target is polymorphic and may be external URL/content | N,K,P,R,E,B: internal polymorphic targets resolve; public ordering and media behavior match |

### 7.5 Members, reseller storefronts and addresses (9 tables)

| # | Table and source | Target ownership | Key fields and relations | Lifecycle and historical semantics | Required checks |
| ---: | --- | --- | --- | --- | --- |
| 19 | 'member_levels' — 'common/models/member_level.go' | tenant business schema | 'id', 'tenant_id', name, market/courier flags, payment ID CSV, ratio, default flag, status | soft delete; ratio decimal; one or more historical defaults may exist | N,K,P,R,J,E,M,B: payment IDs resolve; default-level and market entitlement profile preserved |
| 20 | 'members' — 'common/models/member.go' | tenant business identity | 'id', 'tenant_id', level/referrer/parent-referrer IDs; username/open/union IDs; password fields; status; metadata JSON; shop name, exchange rate, price percentages, QR assets; one finance row | soft delete; member is a customer/reseller identity, not MSS admin; '0'/empty sentinels occur; decimal exchange/percent fields affect copied-shop prices | N,K,P,R,J,E,M,S,B: referrer graph, level, finance, login outcome and copied-shop price fixtures |
| 21 | 'consumers' — 'app/shop/models/consumer.go' | tenant business schema | 'id', 'tenant_id', 'member_id', open/union IDs, profile/location, level, status | soft delete; a consumer belongs to a reseller member; level 0 retail/1 wholesale | N,K,P,R,E,S,B: member ownership; duplicate open/union IDs profiled; copied-shop identity fixtures |
| 22 | 'member_goods' — 'app/shop/models/goods.go' | tenant business schema | composite 'tenant_id/member_id/goods_id'; physical column 'show', 'use', details JSON and timestamps | no soft delete; details hold per-warehouse retail/wholesale price uplifts; 'show' is a reserved identifier in SQL contexts | N,K,P,R,J,M,B: composite uniqueness, member/goods/warehouse links and copied-price results |
| 23 | 'finances' — 'common/models/finance.go' | tenant business schema | primary 'member_id', 'tenant_id'; CNY/AUD balances, gold and three frozen balances; timestamps/deleted_at | soft delete; all values are 'DECIMAL(10,2)' despite Go 'float64'; this is the balance snapshot paired with immutable ledger rows | N,K,P,R,M,T,B: balance/freeze reconciliation against finance logs and approved withdrawals |
| 24 | 'senders' — 'app/shop/models/sender.go' | tenant business schema | 'id', 'tenant_id', 'member_id', 'consumer_id', address/contact fields, default flag | soft delete; consumer '0' means the main member storefront | N,K,P,R,S,B: owner consistency, default-address profile and order snapshot coverage |
| 25 | 'consignees' — 'app/shop/models/consignee.go' | tenant business schema | sender-like ownership plus country/province/city, ID card number/front/back and default flag | soft delete; consumer '0' sentinel; contains highly sensitive identity data | N,K,P,R,S,B: owner consistency; asset references; encryption/access/redaction controls; order snapshots remain readable |
| 26 | 'shopping_carts' — 'app/shop/models/shopping_cart.go' | tenant business schema | 'id', 'tenant_id/member_id/consumer_id', goods/spec IDs, physical 'warehouse_id', pack unit/quantity/selected; created/updated times | no soft delete; historical physical 'warehouse_id' stores 'goods_id + warehouse_id'; reads strip the first 18 characters | N,K,P,R,T,B: raw composite encoding preserved or explicitly decoded once; all goods/spec/warehouse owners agree |
| 27 | 'collections' — 'app/shop/models/collection.go' | tenant business schema | composite 'tenant_id/member_id/consumer_id/goods_id', created-at | no soft delete; consumer may be '0' | N,K,P,R,T,B: composite uniqueness and same-tenant ownership |

### 7.6 Orders, payments, logistics and sales (9 tables)

| # | Table and source | Target ownership | Key fields and relations | Lifecycle and historical semantics | Required checks |
| ---: | --- | --- | --- | --- | --- |
| 28 | 'orders' — 'app/shop/models/order.go' | tenant business schema | 'id', tenant/member/consumer; courier/install; payment IDs; status/copy status; sender/consignee IDs and JSON snapshots; warehouse; goods/packs; payment; activity JSON; referral/audit fields; all price, fee, balance, gold, commission and exchange-rate fields | soft delete; state strings include 'created', 'need-verify', 'need-verify-copy', 'pending', 'already', 'shipping', 'refund', 'completed', 'canceled', 'abolished'; legacy 'OrderCompleted' collides with 'shipping'; channel 0/1/2; money decimal; snapshots and rates are historical facts | N,K,P,R,J,E,M,T,B: full companion graph, legal transition fixtures, per-status/currency/channel totals and snapshot decoding |
| 29 | 'order_goods' — 'app/shop/models/order_goods.go' | tenant business, inherited from order | 'id', 'order_id', goods/spec IDs, quantity, price, goods/spec JSON snapshots, pack specification | soft delete; price decimal; snapshots must survive later goods deletion/change | N,K,P,R,J,M,B: every row has an order; quantity/value aggregates; snapshot JSON decodes independently of live catalog |
| 30 | 'order_unit_packs' — 'app/shop/models/courier_link.go' | tenant business schema | 'id', tenant/member/order, pack JSON, weights, goods/courier prices and copy prices, currency, courier/install/no/method, send status, print flags | soft delete; send status 'will/already/synchronize'; money decimal; pack JSON is the source for sales generation and inventory effects | N,K,P,R,J,E,M,B: pack totals reconcile to order goods/prices; courier links and status profile |
| 31 | 'payment_orders' — 'app/shop/models/payment_order.go' | tenant business schema | 'id', tenant/member/order/install, method, currency, balances/gold/order/real fee/rate, URLs, callback token, external ID, status, voucher/remark/callback flag | soft delete; payment status 'pending/success/already/failed'; decimal money; callback token is secret-like and path-exposed in legacy | N,K,P,R,E,M,S,T,B: order/payment amount reconciliation; external ID duplicate profile; callback idempotency fixtures |
| 32 | 'courier_installs' — 'app/shop/models/courier.go' | tenant business schema | 'id', tenant/courier, used, app key/secret and params, prefix/region, max amount/weight, custom prices, custom-fee JSON text, counter | soft delete; decimal fees; credentials select external logistics account | N,K,P,R,J,E,M,S,B: adapter/method compatibility; encrypted credentials; custom fee calculation and counter semantics |
| 33 | 'courier_templates' — 'app/shop/models/courier.go' | tenant business schema | 'id', tenant/install, name, first weight/price, continued price, region-code CSV | soft delete; decimal prices; '中国' acts as an all-region sentinel | N,K,P,R,J,M,B: install ownership, code normalization and price fixtures |
| 34 | 'payment_installs' — 'app/shop/models/payment.go' | tenant business schema | 'id', tenant/payment, used, app key/secret, image, sort, description | soft delete; credentials and voucher image are sensitive configuration | N,K,P,R,S,B: one tenant cannot read another install; payment method compatibility and secret encryption |
| 35 | 'sells' — 'app/shop/models/sell.go' | tenant business schema | primary string 'id', tenant, shipping/real warehouse, seller/member, currency and price fields, bill/account/date/payment, order time, channel and financial audit; timestamps/deleted_at | soft delete; online sale may reuse order ID; offline ID prefix; return sales and amounts can be negative; audit 0/1/2; bill date nullable | N,K,P,R,E,M,T,B: signed totals by currency/channel/bill/audit; order-to-sale and return linkage; child total reconciliation |
| 36 | 'sell_goods' — 'app/shop/models/sell.go' | tenant business schema | composite 'tenant_id/sell_id/goods_id'; signed quantity, unit/total price, currency, warehouses, order time and remark | no soft delete/timestamps beyond order time; negative quantity represents a return and drives reverse inventory | N,K,P,R,E,M,T,B: composite uniqueness; signed goods totals equal sale totals; inventory-effect replay is idempotent |

### 7.7 Finance and marketing (7 tables)

| # | Table and source | Target ownership | Key fields and relations | Lifecycle and historical semantics | Required checks |
| ---: | --- | --- | --- | --- | --- |
| 37 | 'finance_logs' — 'app/shop/models/finance_log.go' | tenant business ledger | 'id', tenant/member/link, identity snapshot, finance type, source type, old/change/freeze and AUD equivalents, remark, created-at | no soft delete or update; finance type 1 balance/2 gold; source types 1 recharge, 2 consume, 3 withdraw, 4 reward, 5 refund, 6 freeze, 7 unfreeze, 8 cancel recharge; decimals may be signed | N,K,P,R,E,M,T,B: immutable ordering; running balances/freeze reconcile to 'finances' and linked orders/withdrawals |
| 38 | 'gold_withdraws' — 'app/shop/models/finance_log.go' | tenant business schema | 'id', tenant/member, amount, bank/account/name/location, check status, paid, voucher; related finance logs | soft delete; check status 0 pending/1 approve/2 reject; amount decimal; bank data and voucher are sensitive | N,K,P,R,E,M,S,T,B: withdrawal-to-freeze/unfreeze/payment ledger reconciliation and legal workflow fixtures |
| 39 | 'activities' — 'app/shop/models/activity.go' | tenant business schema | 'id', tenant, name/media, status/show, start/end/expiration, type, metadata/extension JSON, member/level/warehouse CSV, sort | soft delete; types FullGift/FullReduction/PanicBuying/FreeShipping; extension may be single or multi-tier; updates replace links | N,K,P,R,J,E,M,T,B: active-window and eligibility fixtures; every CSV/link target resolves; price/quantity/weight rules match |
| 40 | 'activity_links' — 'app/shop/models/activity.go' | tenant business schema | 'id', tenant/activity, link/activity types, link ID, denormalized name/image, created-at | no soft delete; polymorphic product/category/display links; replaced as a set on activity save | N,K,P,R,E,T,B: no duplicate effective links; all internal targets and activity owners resolve |
| 41 | 'coupon_parents' — 'app/shop/models/coupon.go' | tenant business schema | 'id', tenant, name/window, send/enough types, threshold/reduction, warehouse CSV, link JSON text, status, expiration days, sent, member/level CSV | soft delete; send type orientation/new-member; historical status default may be 0; 'sent' is separate from status; decimals affect order reduction | N,K,P,R,J,E,M,T,B: target audience and warehouse resolution; issuance count and rollback fixtures |
| 42 | 'coupons' — 'app/shop/models/coupon.go' | tenant business schema | 'id', tenant, parent/member/order, inherited window/amount/warehouse fields, status, used; child links | soft delete; read hook mutates expired, unused coupons to disabled; used and status are independent historical facts | N,K,P,R,J,E,M,T,B: parent issuance counts; used coupon/order uniqueness; expiration evaluated without read-side writes in new code |
| 43 | 'coupon_links' — 'app/shop/models/coupon.go' | tenant business, inherited from coupon | composite 'coupon_id/link_type/link_id', denormalized name/image | no soft delete or timestamps; polymorphic coupon scope | N,K,P,R,E,B: every coupon and internal target resolves within tenant; unique effective scope |

### 7.8 Configuration and messages (5 tables)

| # | Table and source | Target ownership | Key fields and relations | Lifecycle and historical semantics | Required checks |
| ---: | --- | --- | --- | --- | --- |
| 44 | 'system_configs' — 'app/shop/models/config.go' | tenant business schema | 'id', 'tenant_id', indexed 'name', metadata JSON | soft delete; arbitrary JSON historically mixes public settings and secret-bearing integration settings; generic Admin compatibility is read-only and `metadata` is not searchable, filterable or sortable; a dedicated four-key `appConfig` source workflow exists but is not yet verified or deployed | N,K,P,J,S,B: duplicate-name profile; typed public/private classification; no secret can appear in storefront DTO or evidence; guessed nested-secret queries are rejected; dedicated writes preserve every unapproved metadata value and never revive tombstones |
| 45 | 'messages' — 'app/shop/models/message.go' | tenant business archive pending decision | 'id', tenant, title/content, hits/top, created-at | no soft delete/update; no active admin route found | N,K,P,T,B: retention/mapping decision; content and ordering preserved until approved |
| 46 | 'message_users' — 'app/shop/models/message.go' | tenant business archive pending decision | composite 'message_id/user_id', tenant, read flag/time | no soft delete; user refers to legacy admin identity | N,K,P,R,T,B: map user IDs to MSS identities or archive atomically with messages |
| 47 | 'message_events' — 'app/shop/models/message.go' | tenant business schema | 'id', tenant, name, app/object/event, status | soft delete; event identity is a tuple in practice | N,K,P,E,B: tuple duplicate profile and supported event inventory |
| 48 | 'message_templates' — 'app/shop/models/message.go' | tenant business schema | 'id', tenant, event ID, name/title/content/status | soft delete; template content is user-visible and requires later locale strategy | N,K,P,R,E,B: every event resolves; enabled-template uniqueness/selection fixtures |

### 7.9 ERP (6 tables)

| # | Table and source | Target ownership | Key fields and relations | Lifecycle and historical semantics | Required checks |
| ---: | --- | --- | --- | --- | --- |
| 49 | 'real_warehouses' — 'common/models/real_warehouse.go' | tenant business schema | 'id', tenant, name, region/address, status; referenced by shipping warehouses, inventory, receipts and sales | soft delete; status 1/2 | N,K,P,R,E,B: no cross-tenant logistics/inventory links; deletion policy explicit |
| 50 | 'inventories' — 'app/erp/models/inventory.go' | tenant business schema | composite 'tenant_id/goods_id/real_warehouse_id', alias/barcode, quantity, created/updated times | no soft delete; quantity is mutable snapshot and may be negative unless business policy says otherwise | N,K,P,R,T,B: composite uniqueness; quantity reconciles to ordered inventory tracks and approved baseline |
| 51 | 'inventory_tracks' — 'app/erp/models/inventory_track.go' | tenant business ledger | 'id', tenant/goods/real/shipping warehouse, link type/ID, quantity change/result, created-at | no soft delete/update; link types include receipt, outbound, online/offline sale/rollback, check and init | N,K,P,R,E,T,B: ordered replay reaches inventory; each business link exists or is explicitly historical |
| 52 | 'inventory_checks' — 'app/erp/models/inventory_check.go' | tenant business schema | 'id', tenant, content text containing inventory JSON, created-by identity/name, timestamps/deleted_at | soft delete; content is a point-in-time check request; worker fans it into single checks | N,K,P,J,T,B: JSON decodes; creator mapping; fan-out/replay produces exactly one intended effect per item |
| 53 | 'receipts' — 'app/erp/models/receipt.go' | tenant business schema | 'id', tenant/real warehouse, supplier, currency, decimal price, amount, payment state/account/time, goods, creator, bill-create time | soft delete; payment uses legacy status enum as a settlement flag; receipt creation enqueues inventory change | N,K,P,R,E,M,T,B: goods amount/value totals; payment-time semantics; exactly-once inventory effect |
| 54 | 'receipt_goods' — 'app/erp/models/receipt.go' | tenant business schema | composite 'tenant_id/receipt_id/goods_id', barcode/alias/image, unit/total price, quantity, remark | no soft delete/timestamps; decimal prices | N,K,P,R,M,B: composite uniqueness; line totals reconcile to receipt; goods and warehouse tenant ownership |

'common/models/area.go' defines an auxiliary 'area' model, but it is not in the
formal 54-table migration list. 'OrderOperateLog' is defined in
'app/shop/models/order.go' but is not registered for migration or active
routing. Neither may be silently added to the compatibility allow-list without
an explicit data-retention decision and deployed-DDL evidence.

## 8. Cross-table historical semantics

### 8.1 Status and sentinel values

- The current general enum is 1 enabled and 2 disabled, but model comments and
  defaults show historical 0/1/2 use. Profile distinct values table by table;
  never run a global '0 -> 2' conversion.
- Order completion has a source collision: one constant maps completed to
  'shipping', while the newer value is 'completed'. Preserve stored strings and
  translate through an explicit compatibility state machine.
- Package send states are 'will', 'already' and 'synchronize'; payment states
  are 'pending', 'success', 'already' and 'failed'.
- Empty string and string '0' represent no consumer, no parent, system seller
  or similar optional relations in several domains. Convert only with a
  field-specific rule.
- 'financial_audit', withdrawal check status and receipt payment use 0/1/2 in
  different workflows. They are not interchangeable enums.

### 8.2 Money and exchange rates

Source money columns are generally 'DECIMAL(10,2)' even when the Go model uses
'float64'. Target code must use an exact decimal representation for storage,
calculation and canonical hashing. Do not round through binary floating point.
Reconcile independently by tenant, currency, state and channel:

- order money, copy money, goods, courier, balance, gold, reductions,
  commission and stored exchange rates;
- payment order fee, real fee, balance/gold and exchange rate;
- member finance balances and freezes plus ledger changes;
- sales/returns and sales goods, preserving negative values;
- courier, goods-warehouse and member-level pricing;
- receipts and receipt goods.

AUD and CNY are stored currencies. Historical order/payment exchange rates are
snapshots and must not be recalculated with today's rate.

### 8.3 JSON, CSV and snapshots

JSON-like fields are stored variously as 'json', 'jsonb', byte arrays and text.
CSV fields include albums, payment IDs, terminal IDs, warehouse IDs, member IDs,
member-level IDs, region codes, specifications and courier object IDs. First
migration must preserve both semantic decoded values and exact source text;
normalization happens only in a later versioned migration.

Order goods, addresses, package content, activity application and prices are
historical snapshots. They must remain readable even if live goods, users,
addresses or integrations have been deleted.

### 8.4 Model-hook behavior that must become explicit services

- 'Order.AfterCreate' writes 'order_goods' and 'order_unit_packs'.
- goods updates physically purge and recreate warehouse/specification rows;
  assembled-goods saves replace component rows.
- category and goods-master saves synchronize courier packing links.
- activity saves replace all activity links.
- coupon saves create/replace scope links; coupon reads can mutate expiry
  status in the legacy code.
- sales creation writes sales goods, changes goods inventory/sales counters and
  may enqueue ERP inventory changes.
- receipt creation enqueues ERP inventory changes.
- shared courier/payment/catalog creation can provision tenant install/goods
  rows.

These side effects are not safe to inherit from ORM callbacks. Rebuild them as
transactional application services with an outbox, idempotency key, explicit
authorization and tests.

## 9. Asynchronous and scheduled contracts

The active consumer is 'shop-go/cmd/consumer/server.go'. A duplicate exported
implementation exists under 'shop-go/cmd/api/consumer/', but registration in
the API server is commented out. Do not run both implementations for the same
stream during compatibility operation.

| Stream | Producer | Legacy consumer effect | Migration requirement |
| --- | --- | --- | --- |
| 'stream.send_coupon' | coupon-parent send action | resolve audience from member IDs/levels and fan out create messages | idempotent campaign-send key and recorded audience snapshot |
| 'stream.send_coupon.create' | send-coupon consumer | create one member coupon and its links | unique issuance key; duplicate delivery must not duplicate coupons |
| 'stream.send_coupon.registry' | new member/WeChat registration | create every enabled new-member coupon with calculated validity | unique parent/member issuance and deterministic time input |
| 'stream.use_coupon' | order creation | set coupon used and bind order | atomic order/coupon invariant; duplicate and conflicting order rejected |
| 'stream.send_coupon.rollback' | parent rollback action | delete unused enabled children, mark parent unsent/disabled | idempotent rollback; used coupons retained |
| 'sync.courier' | order state service | load tenant/member and request courier numbers | provider idempotency key, bounded timeout/retry and manual reconciliation |
| 'sync.order.sell.generate' | paid/order transition | create sale from order/package snapshot if absent | unique sale/order key; transaction/outbox |
| 'sync.order.sell.rollback' | cancellation/rollback transition | create negative return sale | unique return key; fix legacy existence-check ambiguity |
| 'sync.inventory.change' | sales and receipts | increment inventory and append inventory track | highest-risk duplicate: event ID/outbox required so quantity changes once |
| 'sync.inventory.check' | ERP inventory-check endpoint | fan out check snapshot rows | persistent check progress and unique item keys |
| 'sync.inventory.chen_single' | inventory-check consumer | set/init one inventory result and append track | preserve historical misspelling during transition or dual-consume with one idempotency ledger |

Redis streams use visibility, reclaim and concurrent consumers, so delivery is
at least once. Before cutover, freeze/drain the old producers, record pending
and claimed entries, deploy idempotent consumers, replay under a tenant-bound
connection and prove no duplicate financial, coupon, courier, sales or
inventory effect.

Two in-process timers start in non-development API instances
('shop-go/cmd/api/server.go'):

- exchange-rate refresh runs immediately and then at the next local midnight;
- unpaid-order cancellation runs immediately and then hourly, using the
  configured timeout even though the legacy description says 48 hours.

Every API replica runs both timers and there is no leader election. Move them
to a distributed scheduler/worker with a lease, idempotency and recorded run
history.

## 10. External dependency inventory

| Dependency | Source paths | Business use | Migration requirement |
| --- | --- | --- | --- |
| PostgreSQL/TimescaleDB and historical MySQL dialect | 'gdb/', migration scripts | all persistence | fixed schema-qualified pools; PostgreSQL-native SQL; production read-only until approved |
| Redis | 'pkg/cache.go', 'pkg/cache/redis.go', 'cmd/consumer/' | tenant/domain cache, OAuth/JSSDK cache, locks and 11 streams | realm/tenant namespaces, TLS/auth from Secret references, outbox/idempotency and observable lag |
| Local and S3-compatible object storage | 'store/', 'controllers/file.go', API startup | public/private uploads, vouchers, ID cards, posters and dictionary/assets | separate public/private policies, signed access, tenant prefixes and malware/content controls |
| WeChat | 'controllers/wechat.go', 'tools/uni_user.go', 'tools/multiple_ticket.go', 'pkg/wxsign/' | OAuth, official account, mini-program login, JSSDK and official payment | server-owned AppID binding; secrets never in public config; sandbox tests and code-exchange verification |
| Payment providers | 'tools/method.go', 'tools/pay/', payment services/controllers | RoyalPay, SandPay variants, SuperPay, Omipay, Paylinx, WeChat official, voucher/offline and balance | per-install encrypted credentials, signed/idempotent callbacks, amount/currency verification, bounded clients and sandbox fixtures |
| Logistics providers | 'controllers/payment_order.go', 'controllers/order_app.go', logistics DTOs/services | EWE, RLG/RLG-TK, AU, XD, AUS, POL and AJ order creation, ID-card upload and trace | adapter boundary, encrypted credentials, HTTPS/timeouts, provider idempotency, PII minimization and recorded reconciliation |
| Exchange-rate provider | 'tools/exchange_rate.go', API timer | AUD to CNY tenant configuration | scheduled singleton, timeout/cache/fallback and immutable order-rate snapshots |
| QR/poster/font and segmentation assets | 'pkg/poster/', 'controllers/base.go', 'controllers/goods.go' | share QR/posters and goods search tokenization | versioned assets, deterministic rendering and no secret/private-object leakage |
| TLS/static merged hosting | 'cmd/api/server.go' | autocert and legacy admin/mobile static files | terminate TLS in platform infrastructure; keep app routes separate from current frontends |

No active email or SMS provider integration was found in the baseline. Adding
one is new scope, not a compatibility assumption.

## 11. PostgreSQL and TimescaleDB compatibility risks

1. **Schema selection**: qualify all compatibility queries. Permit the exact
   legacy 'public' schema only for the source adapter and the reconciler-fixed
   tenant business schema only for a target runtime. Exclude
   'compression_test_*' and every unknown schema.
2. **Reserved identifiers**: columns such as 'show' and 'change' require safe
   identifier quoting. Never interpolate identifiers from user input.
3. **Case-insensitive search**: MySQL collation-backed 'LIKE' behavior generally
   needs PostgreSQL 'ILIKE'; negative cases need 'NOT ILIKE'. Lock expected
   Unicode/case behavior in tests.
4. **JSON versus JSONB**: legacy tags mix 'json', 'jsonb', byte arrays and text.
   Profile invalid JSON and JSON 'null' separately from SQL 'NULL'; do not cast
   invalid source values during the first copy.
5. **Composite keys**: GORM v1 'primary_key' tags, embedded IDs and deployed DDL
   may disagree. This affects goods, activities, coupons, withdrawals and all
   link/composite tables. Read the actual source and target constraints before
   generating 'ON CONFLICT' clauses.
6. **Soft delete**: preserve nullable 'deleted_at' and count active/deleted rows
   separately. Do not turn soft-deleted history into hard deletes or make null
   timestamps zero values.
7. **Decimals**: keep 'NUMERIC/DECIMAL(10,2)' and canonical decimal strings.
   Avoid Go/JavaScript floating-point round trips in migration and validation.
8. **Booleans and statuses**: MySQL tinyint-like values and PostgreSQL booleans
   are not interchangeable with integer status enums. Profile actual types and
   values before casting.
9. **Timestamps**: profile column type, time zone, zero/invalid dates, nulls and
   application locale. Canonical hashes must normalize valid instants without
   rewriting historical wall times accidentally.
10. **Uniqueness and collation**: MySQL and PostgreSQL differ for case,
    trailing spaces and multiple nulls. Profile duplicates for domains,
    usernames, open IDs, external order IDs and business composite keys before
    adding stricter constraints.
11. **CSV fields**: keep source text and decoded arrays. PostgreSQL arrays or
    normalized link tables are later migrations, not an implicit cast.
12. **Grouping and ORM SQL**: PostgreSQL strict 'GROUP BY', identifier quoting
    and null ordering can change dashboards/search/export results. Capture
    golden query fixtures per domain.
13. **Missing foreign keys**: most source relations are logical only. Import in
    dependency order, report orphans and add constraints only after approved
    remediation.
14. **String IDs and sequences**: most IDs are 18–20 character generated
    strings. Do not replace them with sequences or numeric casts during
    compatibility migration.
15. **Timescale tooling**: the formal business tables are ordinary compatibility
    data. Temporary compression-test schemas and Timescale internal schemas are
    never migration inputs.

## 12. Data acceptance prerequisites and gates

No data-copy task may begin until the following inputs exist:

1. an immutable, restore-tested source backup and exact DDL for all 54
   'public' tables, including columns, types, defaults, nullability, keys,
   indexes and sequences;
2. a signed 54-table source/target manifest with ownership, copy order and
   stable key for each table; 'shipping_warehouses' must be present;
3. per-table and per-tenant total/active/deleted counts and canonical hashes;
4. a distinct-value profile for every status, order/package/payment state,
   currency, channel, payment method, courier method and polymorphic link type;
5. JSON validity reports plus exact CSV/raw-text snapshots without including
   secret values or row-level PII in committed evidence;
6. orphan and cross-tenant reports for tenant, identity, catalog, goods,
   warehouse, member, order, payment, sales, coupon and ERP graphs;
7. monetary baselines by tenant/currency/state/channel plus finance/freeze,
   withdrawal, sales/return, receipt and inventory baselines;
8. a reviewed legacy-user/role-to-MSS identity, permission, default-policy and
   warehouse-scope mapping;
9. an allow-listed integration test configuration using sandbox/fake
   credentials only;
10. a stream freeze, drain and idempotent replay plan with old/new consumer
    ownership and rollback checkpoints.

### 12.1 Import acceptance

- Run at least two clean, deterministic imports from the same immutable input.
  Their table counts, canonical hashes and generated reports must match.
- Execute system-verification cases inside the Kubernetes cluster using
  disposable one-time Pods. Do not run migration system tests directly from
  the local host.
- For each tenant, seed the seven product/logistics tables from the same
  immutable qualified snapshot before importing tenant references. Import
  parent records before inherited-tenant children and defer new foreign keys
  until orphan reports are accepted.
- Verify all 54 tables even if a table has zero rows. Zero is evidence, not a
  reason to omit a contract.
- For development, create 'orders' structure only and do not copy order rows.
  Order-row hashes and monetary baselines may be computed read-only at source;
  full row-copy rehearsal needs a separately approved environment/rule change.

### 12.2 Domain acceptance

- **Identity**: every enabled legacy admin has a documented MSS login/reset
  outcome; every role operation has a backend permission; negative permission
  and warehouse-scope tests pass.
- **Catalog/goods**: shared-to-tenant projection, goods/spec/warehouse graphs,
  display ordering, imported prices, assembled goods and copied-shop prices
  match golden fixtures.
- **Orders/payments**: order companions are complete; legal transitions,
  cancellation, financial audit, packages, vouchers and callbacks are
  idempotent; monetary totals match exact decimals.
- **Finance**: balances and freezes reconcile to immutable logs and approved
  withdrawals; duplicate requests cannot change money twice.
- **Marketing**: activity eligibility/pricing and coupon issue/use/rollback
  match fixtures without read-side database writes.
- **ERP**: inventory track replay reaches each inventory snapshot; receipts,
  sales, returns and checks each apply exactly once.
- **Storefront**: public bootstrap contains no secret/schema identifier and
  exact Host/AppID bindings fail closed for unknown or duplicate mappings.

### 12.3 Cutover gate

Cutover remains blocked until:

- every 54-table count/hash and aggregate check passes;
- all unexplained orphans, duplicates, invalid JSON and status values are zero
  or covered by an approved, auditable remediation;
- streams are drained or safely dual-run with one idempotency ledger;
- backup restore and rollback have been rehearsed;
- both mall admin and storefront regression suites pass;
- the user explicitly approves the exact production write plan.

## 13. Secret rotation and redaction requirements

The legacy repository contains historical plaintext environment/integration
configuration. Values must never be copied into this repository, migration
documents, skills, MCP configuration, fixtures, command output or reports.
Before any new environment is trusted, rotate every affected database, Redis,
object-storage, exchange-rate, WeChat, payment and logistics credential.

Secret-bearing persisted fields include at least:

- 'tenants.secret';
- admin/member password hashes, reset hashes and salts;
- 'payment_installs.app_key/app_secret';
- 'courier_installs.app_key/app_secret/param0/param1' where provider-specific;
- 'payment_orders.token' and provider identifiers/URLs where sensitive;
- bank/identity-card data and private voucher/object references;
- secret-like entries inside 'system_configs.metadata'.

Requirements:

1. keep secrets in the platform Secret store and persist only an opaque
   reference where possible;
2. where compatibility requires a database value, encrypt it with managed key
   rotation and restrict decrypt permission to the owning adapter;
3. return dedicated redacted DTOs; never serialize model structs containing an
   app secret, tenant secret, password material or private object key;
4. scrub request paths, callback tokens, authorization headers, provider
   payloads and PII from logs/traces/errors;
5. compare verification data using counts, keyed digests or redacted metadata,
   never committed row dumps;
6. exclude JSON documents with declared nested secrets from search, filtering
   and sorting so result counts cannot reveal guessed secret values;
7. record rotation completion by secret reference/version and timestamp, not by
   value.

This contract must be updated when a table, legacy semantic or migration gate
is deliberately retired. Silence, an empty table or MSS availability is not a
retirement decision.
