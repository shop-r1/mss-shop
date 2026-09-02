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
3. Keep the tenant platform as desired-state control plane. The in-cluster
   reconciler is the sole writer of tenant database roles, schemas, snapshots,
   views and grants; it has no Kubernetes API identity. Fixed trusted operator
   commands own the independent trigger-disabled database preflight, stage
   Secrets and workload objects after fail-closed service/object/host collision
   checks. The fixed development bootstrap may replay only before MSS migrates
   either exact-empty core schema; it fails closed afterwards. Bind every mall
   runtime to one immutable tenant and fixed
   core/business schema pair at startup; never accept a schema selector from a
   client.
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
   reconciler has a real, fixed `mss-shop-dev` PostgreSQL driver. The original
   `r1shop-dev` environment is immutable except for the importer's exact
   read-only source connection, and production reconciliation remains
   deliberately unavailable; the storefront worker is still a local
   simulation.
9. For create-only stage evidence, distinguish a new `created` resource from a
   read-only `exactRetry` and from a failed-closed preflight. Only `created`
   changes the resource count; neither exact retry nor rejection proves a new
   workload execution. Keep the receipt ConfigMap immutable and bound to the
   revision that created it. A later verifier revision must fail closed instead
   of deleting, relabelling or replacing that evidence object.
10. Trusted Kubernetes stage commands default to a non-persistent API-server
    dry-run of the exact objects. A render or local preflight is not a dry-run.
    Require an explicit `--create` or `--apply` for persistence and repeat the
    complete collision, evidence and object preflight immediately before the
    allowed write. This never authorizes a write outside `mss-shop-dev`.
11. Read the current sequence in `docs/project/status.md`, DEC-0013 and
    `docs/runbooks/remote-development-and-dev-acceptance.md` before starting
    validation or rollout. Develop locally, then use a same-repository
    `codex/**` pull request as the only CI, image-publication and development-
    deployment loop. Bind verification to the latest deployed PR head; a later
    push invalidates prior cluster and browser acceptance. Bring the branch up
    to date with `main` before the final cycle; that new head must pass again.
    Squash-merge only the latest accepted head and record its mapping to the
    resulting main SHA, matching source tree and image digests. Main does not
    rebuild. Production promotion is not executable until exact `mss-shop`
    production targets and minimum access are reviewed; stop at the required
    human GitHub Environment review and never approve or bypass it. Do not reuse
    prior receipts, previews or browser evidence as proof for a different head.
