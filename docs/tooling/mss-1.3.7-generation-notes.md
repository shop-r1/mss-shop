# MSS 1.3.7 generation qualification notes

Last verified: 2026-09-01

This repository consumes the released MSS 1.3.7 Distribution without copying
or patching MSS core. The notes below record generator behavior reproduced
while creating the `legacycheckpoint` AdminModule. They are host-side
qualification constraints, not reasons to edit generated files by hand.

## Qualified specification shape

- Use an identifier that is also a valid Go package identifier for
  `metadata.name`. The module is named `legacycheckpoint`; a hyphenated name
  produced invalid generated Go package references in this qualification.
- Keep only the workflow state as an enum. Generating two enum fields in the
  same entity produced a duplicate local `definition` declaration in the
  generated migration, so `owner` is currently a validated informational
  string. It is not an authorization or readiness input.
- Give required string fields an executable validation rule. The `resource`
  field uses `^[A-Za-z0-9._-]+$`; without a string validation case the generated
  service test retained an unused `invalid` fixture.
- Declare the created and updated events used by the generated service tests.
  Omitting all events left an unused typed event fixture in this module shape.

These constraints apply only to the informational migration-checkpoint module.
The 54-table legacy manifest, fixed schema binding, backend authorization and
runtime readiness remain handwritten business contracts.

## Authorized business-menu localization

MSS 1.3.7 normalizes a dot-delimited backend menu name to its final segment
before the Admin Web receives it. Business menu roots must therefore use one
unique, dot-free token (`legacyBusiness` for mall-platform and `sharedCatalog`
for tenant-platform). Handwritten route registrations use the relative leaf
resource token; ProLayout then composes the hierarchical locale ID from the
root, directory and leaf. The locale catalogs also carry direct directory and
leaf keys because MenuSearch formats backend nodes outside that hierarchy.

The 1.3.7 runtime layout supplies a dynamic `menu.request` but does not set
`menu.locale`. Each Host therefore keeps a minimal `src/app.tsx` facade that
wraps the unchanged MSS layout with the business-owned
`withAuthorizedMenuLocale` helper. MSS package source is not copied or
modified. Unit tests cover the wrapper behavior and source-contract tests
prove that both facades remain connected to it.

`web/src/app.tsx` is part of the Thin Host Blueprint managed baseline even
though it has no generated header. Every `mss upgrade admin` plan must review
this facade as an intentional Host customization and either preserve it or
remove it when a later MSS version enables authorized-menu locale itself.

The original authorization projections (`66966149766800` for mall and
`20260901120000` for tenant) were already published and remain reproducible.
Mall migration `66966149766801` and tenant migration `20260901120001` apply
the corrected root token forward-only. Mall migration `66966149766802` and
tenant migration `20260901120002` then disable the historical generic-write
components/API entries and revoke their policies. Each correction advances
the authorization revision, leaves legacy business tables untouched, and is
required by readiness.

## Legacy compatibility mutation qualification

The generated AdminModule CRUD profile is not evidence that a legacy table is
safe to mutate. The current compatibility catalogs expose 43 mall resources
and eight tenant shared-catalog resources as read-only. All generic create,
update and delete requests fail closed. This includes the eight resources that
were initially treated as simple configuration/catalog records.

The earlier four-plus-four writable classification was withdrawn after review
showed that table shape, fixed-schema binding and soft-delete support do not by
themselves recover the old validation, relationship/tenant constraints,
model-hook effects or deletion semantics. A resource may regain a write
operation only through a separately reviewed Feature/workflow with positive
and negative authorization, tenant/relationship, hook, conflict, idempotency
and deletion tests. Qualification is per resource and per operation; it is not
inherited from another table or from a successful local editor smoke test.

`system_configs.metadata` is a JSON document with declared nested secrets.
Response redaction is not sufficient because search/filter result counts could
act as a secret-presence oracle. Any JSON column with `NestedSecrets`, including
this field, is excluded from free-text search, exact/contains/icontains filters
and sorting.

## Generated warning

Two generated Ant Design pages currently import React hooks that their selected
field shapes do not use. Biome reports one `noUnusedImports` warning in each
Host: `LegacyMigrationCheckpointPage.tsx` in mall-platform and `TenantPage.tsx`
in tenant-platform. Both MSS Admin Web lint commands still exit successfully
and both production builds succeed. Do not remove the imports by hand because
regeneration would restore them.

## Verification

After changing the AdminModule specification:

1. Plan the owning Feature and review every output path.
2. Regenerate with the exact MSS 1.3.7 tool.
3. Run deterministic generation check.
4. Run the generated backend module tests and Admin Web tests, lint and build.
5. If a workaround is no longer needed in a later MSS release, remove it from
   the specification and this note in the same upgrade change.

Current successful frontend evidence is:

- mall-platform: 9 test files / 33 tests, lint with the generated warning above,
  and a production build containing all 43 mall legacy resource routes;
- tenant-platform: 7 test files / 26 tests, lint with the generated warning
  above, and a production build containing all eight shared-catalog routes.
