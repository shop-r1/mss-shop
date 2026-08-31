---
name: r1shop-platform
description: Apply R1Shop's control-plane, per-tenant runtime, schema-isolation, storefront-contract, migration, and cross-platform i18n decisions when changing mss-shop architecture or business code.
---

# R1shop Platform

1. Read `AGENTS.md`, `docs/project/status.md`,
   `docs/architecture/overall-solution.md`, and
   `docs/architecture/invariants.yaml` before changing a service boundary,
   tenant lifecycle, database access, storefront contract, or locale behavior.
2. Identify the affected invariant and accepted decision. If the requested
   design contradicts one, add a superseding ADR and update the registry,
   architecture, invariants, migration plan and status in the same change.
3. Keep the tenant platform as desired-state control plane and the reconciler
   as the sole tenant-resource writer. Bind every mall runtime to one immutable
   tenant and fixed core/business schema pair at startup; never accept a schema
   selector from a client.
4. Keep MSS core unchanged. For a Thin Host business change also follow the
   narrower `mss-thin-host` Skill, edit specifications/business-owned files,
   and run the MSS validations it requires. If the change reads, writes,
   exposes or migrates one of the 54 legacy tables, also follow the narrower
   `r1shop-legacy-module` Skill and its delivery checklist.
5. Treat `/app/v1` as the storefront boundary. Update the authoritative
   `contracts/app-v1` contract before implementation. Refresh every changed
   file as a byte-for-byte snapshot in `mss-shop-mobile`, record the committed
   backend source revision and SHA-256 values, then update the typed client;
   Admin endpoints are never a mobile fallback.
6. Follow `docs/architecture/internationalization.md` for every user-visible
   feature. Keep `zh-CN` and `en-US` complete, keep locale/currency/timezone/
   tenant independent, and use stable message and error keys.
7. Use `references/change-checklist.md` to determine the minimum evidence and
   documentation set. Never run a production write without explicit approval
   for the exact action.
8. For repository-wide platform work, keep root services in the root Go module
   and the generated Admin hosts in their nested modules. Validate services
   with `GOWORK=off`, validate each host from its own root with the official
   MSS 1.3.7 binary, and run `scripts/check-platform-boundaries.sh`. The
   phase-one reconciler/worker are local simulations; do not present them as
   production drivers.
