# Legacy module delivery checklist

## Contract

- Map every changed page, button and old API to a new operation or an approved
  retirement in `docs/migration/legacy-admin-acceptance-matrix.md`.
- Identify manifest tables, primary/composite keys, tenant scope, soft delete,
  sensitive fields, states, money columns and model-hook side effects.
- For unsupported MSS generator behavior, add or update a Feature contract
  before handwritten implementation.

## Backend

- Resolve only an immutable startup binding; add negative tests for forged
  tenant/schema headers, queries and bodies.
- Fully qualify every legacy table. Scope list, detail, mutation, export and
  related-table checks, including resources whose tenant scope is inherited.
- Use decimal-safe money operations, transaction boundaries, conditional state
  updates and idempotency keys where the domain requires them.
- Seed menu, component and API policies in an idempotent forward core migration;
  test root, allowed role, denied role and missing identity.
- For MSS 1.3.7, use a unique dot-free root menu token and relative child
  tokens. Apply corrections only in a new forward migration, advance the
  authorization revision, and make readiness assert the exact active root.
- Keep menu path, Admin Web route and route permission identical. Component
  grants use `<route>/permissions/<operation>`; API paths remain separate and
  must not be reused as UI permissions.
- Readiness verifies the authorization migration and exact required business
  schema fingerprint without mutating legacy tables.
- Keep all 43 mall and eight tenant shared-catalog compatibility resources
  read-only by default. Re-enable a write only for one reviewed resource and
  operation after its Feature restores legacy validation, relationship/tenant
  constraints, hooks, authorization, conflict/idempotency and deletion
  semantics. A successful local generic editor operation is not qualification.
- Redact sensitive fields from success, error, audit and log output.
- Exclude any JSON column with `NestedSecrets`, including
  `system_configs.metadata`, from free-text search, exact/contains/icontains
  filters and sorting; test that guessed secret values cannot influence result
  counts.
- Return flat `errorCode`/`errorMessage`/`messageKey`/`params` envelopes. Test
  status, stable key, safe fallback text and the absence of nested renderable
  objects for authorization, validation, conflict, not-found and readiness
  failures.
- If a dedicated workflow later qualifies a create, preserve the legacy
  18-digit decimal ID format and test length, character set and concurrent
  uniqueness.

## Frontend

- Treat mss-boot-admin Admin Web as the UI baseline. Reuse its layout, tokens,
  table/form conventions, feedback and state patterns; do not reproduce the
  old Vue page structure or introduce a second Admin design system.
- Keep pages, hooks, services and typed domain models focused. Prefer small
  stable reuse over a configuration-driven mega page, duplicated route/API
  adapters, raw field maps, magic status strings or scattered compatibility
  branches. A legacy-specific adapter must have one owner and a clear removal
  condition.
- Keep the generic compatibility viewer read-only. When a qualified operation
  has workflow-specific validation, state or cross-table effects, implement a
  focused MSS-native business surface rather than extending the viewer into a
  universal editor.
- Register handwritten routes and server-path projections in business-owned
  files only. Do not infer backend authorization from a visible menu.
- Keep direct MenuSearch keys and hierarchical ProLayout `menu.*` keys complete
  in both locales. Preserve the tested `withAuthorizedMenuLocale` Host facade
  connection; review the Blueprint-managed `src/app.tsx` customization during
  every MSS upgrade.
- Use explicit list/detail/create/update/delete capabilities from the backend;
  do not infer detail support from the presence of an `id` field on a
  composite-key resource. In the current catalogs every mutation capability is
  false and the UI must not render create, edit or delete actions.
- Cover loading, empty, error, forbidden, conflict and unavailable-schema
  states. Add destructive confirmation and duplicate-submit protection only
  when a dedicated workflow is later qualified to mutate data.
- Localize known backend `messageKey` values, safely reduce malformed or old
  nested envelopes to strings, and never pass an arbitrary response object to
  React or Ant Design message APIs.
- Keep Simplified Chinese and English message keys complete and keep business
  values independent from labels.
- Review the resulting page for clear ownership, removable compatibility code,
  duplicate transport normalization and avoidable abstractions; old visual
  parity is not an acceptance criterion.

## Verification

- Run focused unit, repository, state-machine, permission and cross-tenant
  negative tests while iterating.
- Run the Host's strict doctor, Skill validation and full verification with the
  official MSS 1.3.7 tools.
- Execute database/system scenarios in a disposable Kubernetes Pod using a
  development database copy. Record 54-table fingerprints, row counts, sample
  hashes, relations and relevant order/payment/wallet/inventory aggregates.
- Use the in-app browser for UI acceptance. Verify login, menus, list/filter,
  detail, the absence of generic mutation controls, language switching and
  console/network health, then leave the reviewed app running for manual
  follow-up. When a dedicated mutation workflow is later enabled, add its
  allowed and denied browser cases at that time.
- Record bounded local browser evidence under `docs/acceptance/`, including the
  fixture/environment, exact operations, untested items and scenario count.
  A local compatibility smoke review never closes a workflow row implicitly.
- Record only executed evidence in project status; mark remaining acceptance
  rows honestly rather than treating a build as business completion.
- Run `go test ./contracts` after updating the table manifest, coverage counts,
  implementation state or any source-of-truth path; the repository memory is
  part of the deliverable rather than a follow-up task.
