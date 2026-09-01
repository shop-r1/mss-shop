# Member Levels development checkpoint

Status date: 2026-09-02.

## Scope and current release posture

The dedicated Member Levels workflow restores a typed Mall Admin surface over
the legacy `member_levels` table. The legacy row remains authoritative; this
workflow does not introduce a replacement table or a permanent dual write.

The current release candidate is deliberately read-only in the isolated
cluster. `R1SHOP_MEMBER_LEVELS_MUTATION_MODE` is unset by default, so the API
and Admin Web expose list/detail behavior while create, update, set-default and
delete remain unavailable. The first cluster migration test projected the
already imported fixed-tenant slice into the final business schema and proved
that the projection is complete and isolated. It did not mutate a member level
and does not close `MEMBER-001`.

## Typed contract

The dedicated API is rooted at `/admin/api/member-levels`. It supports list,
detail, create, update, set-default and soft-delete routes, but each mutation
is protected by both the exact MSS permission and the server-side cutover
gate. The public DTO contains only:

- opaque legacy ID;
- name;
- decimal discount percentage;
- normalized enabled, disabled or unknown status;
- default flag;
- timestamps;
- an opaque optimistic-concurrency revision.

`tenant_id`, `payment_ids`, `has_market`, `change_courier` and `deleted_at`
never enter the response or frontend state. The generic legacy
`member_levels` compatibility resource remains read-only.

## Legacy-data safety

- Tenant and schema selection come only from the immutable startup binding.
- Names are unique among active rows for that tenant and are validated as
  non-empty UTF-8 values of at most 100 bytes.
- Discount percentages are canonical decimal strings between zero and 100
  with at most two fractional digits.
- Creation copies the raw payment policy only from an active same-tenant
  source, or from the unique active enabled default. A missing, ambiguous,
  null or empty policy fails closed.
- Update changes only the owned columns and checks the opaque revision.
- Set-default serializes the tenant aggregate and repairs zero or multiple
  flags in one transaction around an enabled target.
- Delete is a soft delete and rejects the default level or active references
  from members, activities, coupon templates and active goods member-level
  prices.

The old service does not participate in the new aggregate lock. Consequently,
ordinary runtime operation must stay read-only. `isolated-cutover` may enable
create, update and set-default only after the old member-level writer is
stopped. Delete requires the stronger
`isolated-cutover-all-reference-writers-stopped` assertion until all reference
writers share a database locking protocol.

## Isolated migration-test contract

The first fixed tenant is legacy tenant `518729051064631297`. The checked-in
operator renders a revision-bound, disposable Kubernetes Job that reads the
isolated PostgreSQL database only. It must prove all of the following before
the release is considered deployed:

- the imported `public.member_levels` fixed-tenant slice has four rows;
- the fixed Mall business relation has the same four rows;
- a reviewed twelve-column comparison has zero differences in both
  directions;
- the business relation exposes no other tenant;
- exactly one active enabled default exists;
- imported and projected `orders` and `order_goods` remain empty;
- the runtime role cannot read the imported `public` source tables directly.

The verifier received no Kubernetes API credential, ran a read-only database
transaction and emitted no row values or connection material. Its successful
2026-09-02 result proved all assertions above for source revision
`3e64a57dae8bb3dd4d337a423015baae6c352b32` and reconciler image digest
`sha256:fba8a63938eef780e8eeb68e2c391bd91ad01c4214dcfa6a7089cf75cc1ab4fd`.
This is data-projection evidence only, not mutation acceptance.

## Acceptance boundary

The feature contract is
`apps/mall-platform/.mss/features/member-levels.yaml`. Formal source validation
has passed the module unit/integration suite, frontend contract tests,
authorization matrix, exact-schema readiness, production build and the MSS
v1.3.7 ten-check `verify --all` gate on the remote development host.
Immutable-image CI and the cluster evidence now bind the reconciler, runtime
and disposable verifier to the same full Git revision and exact image digests.
The runtime remains read-only, and confirmed-login browser review is pending.

`MEMBER-001` remains open until an isolated environment executes the permitted
write lifecycle and then creates and maintains customers, referral links,
senders and consignees with preserved legacy IDs and relationships.
