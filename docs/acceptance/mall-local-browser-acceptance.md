# Mall Admin local browser compatibility acceptance

Last verified: 2026-09-01

## Scope and result

The mall-platform Admin compatibility surface passed a bounded local browser
smoke review against the guarded SQLite fixture for navigation and read paths.
The review used the in-app browser, MSS 1.3.7, the local backend on port 8080
and the local Admin Web on port 8001. It did not use a development or
production database, Kubernetes, or customer data.

This evidence proves that the reviewed compatibility UI can run and perform a
small set of representative reads. All 43 mall compatibility resources and all
eight tenant shared-catalog resources are now read-only. It does **not** close
any row in the 31-scenario legacy business acceptance matrix. The
closed-scenario count remains zero until the dedicated workflows and
disposable-Pod system evidence exist.

The `show_categories` create/update below was executed against an earlier,
superseded implementation before the generic-write risk was fully qualified.
It revealed that table-level shape checks were insufficient: the old
validation, relationship and tenant constraints, model-hook effects and delete
semantics had not been restored per resource. The current implementation no
longer advertises or accepts that generic write path. This historical step is
retained as problem-discovery evidence, not as a passed write capability.

## Executed checks

| Check | Executed evidence | Result |
| --- | --- | --- |
| Login | Signed in with the local administrator fixture and reached protected business routes. No credential is stored in this repository. | Passed |
| Authorized navigation | Verified the `Business` / `业务管理` root, all 11 domain labels, and the six catalog resource labels in English and Simplified Chinese. | Passed |
| Product list and detail | Loaded the local `goods` sample list and opened its detail view. | Passed |
| Empty search | Submitted a non-matching resource search and observed the localized empty state. | Passed |
| Superseded create/update exploration | Against the earlier implementation, created local `show_categories` record `178820180637328500`, then updated its name to `浏览器验收分类（已更新）`. This exposed an under-qualified generic-write boundary and is not a current acceptance pass. | Superseded evidence |
| Post-correction mutation controls | After applying the forward authorization lockdown and restarting the backend, reloaded `show_categories`, `function_circles`, `message_events` and `message_templates`. All four rendered as read-only pages with no create, edit or delete control; available rows retained view-only detail. | Passed |
| Composite-key safety | Loaded `inventories`, verified the composite-key row and confirmed that no generic mutation action is offered. | Passed |
| Field localization | Verified readable English inventory headers and their Simplified Chinese counterparts; raw backend field message keys were not displayed. | Passed |
| Review-time console health | Opened a fresh tab with the bundle under review, rendered the two-row display-category list, and observed no browser warning or error entries. | Passed |
| Read-side server round trips | List, detail and search returned renderable application states. | Passed for reads |

The historical record intentionally remains in the ignored local fixture
database so the user can inspect the exploration artifact manually. Its
presence does not imply that the current source exposes create or update. No
delete operation was executed.

## Not covered

- a current write workflow for any of the 43 resources; generic create, update
  and delete are disabled rather than accepted;
- an exhaustive browser recheck across every resource; the four resources
  formerly classified as generic-writable were rechecked after correction,
  while the complete 51-resource read-only boundary is enforced by manifests,
  authorization-lockdown migrations and automated negative tests;
- delete confirmation or deletion;
- a browser-level denied-role/HTTP 403 flow;
- tenant-platform browser acceptance;
- exhaustive navigation or operation coverage across all 43 mall resources;
- historical order, payment, inventory, wallet, promotion, import/export, or
  other cross-table side-effect workflows;
- a legacy PostgreSQL/TimescaleDB data copy, row-count reconciliation, or
  disposable Kubernetes Pod system test;
- any production migration, deployment, or write.

`system_configs.metadata` contains recursively redacted nested secrets. That
JSON column is excluded from free-text search, exact/contains/icontains filters
and sorting so result counts cannot become a secret-presence oracle. This
repository-level safety rule was not inferred from the historical browser
create/update step.

The authoritative open/closed business status remains
[`legacy-admin-acceptance-matrix.md`](../migration/legacy-admin-acceptance-matrix.md).
