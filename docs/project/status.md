# Project status

Last verified: 2026-09-01

## Confirmed decisions

- Admin foundation: `mss-boot-admin` Distribution `v1.3.7`, consumed as an
  exact backend/frontend dependency without forking MSS core.
- Release qualification: the complete 1.3.7 GitHub release assets are public
  and coordinated at commit `77b53d41092741eac62fa6418c0bdbf87413c7cd`;
  upstream's tagged policy still has not declared stable promotion complete.
- Admin topology: one tenant control plane and one mall management runtime per
  tenant. The current first milestone has one tenant, so it needs one of each.
- Tenant isolation: every mall runtime is configured with one immutable tenant
  identity and one core/business schema pair. There is no request-time schema
  switch.
- Storefront: a separate `/app/v1` API serves H5 and WeChat Mini Program.
- Mobile: classic uni-app with Vue 3, Vite, TypeScript and Pinia. H5 and
  `mp-weixin` are phase-one targets; App is deferred.
- Languages: all new surfaces are internationalization-ready. The initial
  complete locale set is Simplified Chinese and English.
- Engineering memory: constraints, architecture, ADRs, runbooks, Skills and
  verified MCP configuration live in the repositories and are reviewed with
  code.

## Present in this repository

- A runnable MSS 1.3.7 Thin Host proof of concept at the repository root.
- Final MSS 1.3.7 Thin Hosts under `apps/tenant-platform` and
  `apps/mall-platform`, each with its backend and Admin Web in one directory.
- A generated tenant desired-state Admin module with bilingual UI. Its delete
  action is a soft archive and never destroys tenant resources.
- A handwritten forward constraint migration that permits repeated empty
  optional WeChat AppIDs but rejects duplicate configured AppIDs before the
  tenant migration is marked ready.
- The authoritative `/app/v1` OpenAPI, JSON schemas and bilingual bootstrap
  examples.
- A runnable storefront API vertical slice with strict static configuration,
  exact Host/AppID bindings, weighted locale negotiation, stable errors and
  public-only response DTOs.
- In-memory lifecycle/reconciler and worker inbox implementations covering
  idempotent convergence, retry, suspend/resume, CAS and tenant-scoped message
  deduplication. They are simulation/test code, not production drivers.
- MSS-generated project contracts and seven MSS workflow Skills.
- Versioned target architecture, migration plan, i18n policy and project Skill.
- A project-scoped `mss-mcp` connection whose wrapper rejects any version other
  than 1.3.7.

## Not implemented yet

- A persistent control-plane repository, leases or observed-status integration
  between the tenant Admin module and reconciler.
- The authoritative normalized Host/AppID binding repository between raw Admin
  desired state and storefront serving. The current static serving directory
  already normalizes exact bindings and fails closed on duplicates.
- Real PostgreSQL role/schema drivers, secret storage, Kubernetes resources or
  a persistent worker queue/inbox.
- Mall commerce modules, customer authentication, catalog, cart, checkout,
  payments or legacy business data migration.
- Any development-cluster rollout, production migration or cutover.

## Next milestone and acceptance criteria

Rehearse one tenant in a dedicated development database: persist desired and
observed state, implement the reconciler's PostgreSQL schema/role steps, and
bind one mall runtime to its fixed core/business schema pair. Acceptance needs
idempotent retry and isolation tests in disposable Kubernetes Pods. No
production write is part of that milestone.

## Verification evidence

Verified locally on 2026-09-01:

- `GOTOOLCHAIN=go1.26.6 mss doctor --strict` reported ready with exact MSS,
  Go, Node and pnpm versions.
- The root proof and both final Thin Hosts completed independent strict doctor
  and Skill validation with the official 1.3.7 tools. Both final Hosts passed
  full verification. Root full verification remains intentionally split into
  explicit backend/frontend jobs because 1.3.7 still discovers nested Host
  modules against the root specification path.
- Root platform tests, race checks, vet, storefront build and architecture
  boundary checks passed.
- The tenant module passed deterministic 1.3.7 generation check, including its
  generated presentation registry.
- The mobile repository's locked contract check, TypeScript check and both H5
  and `mp-weixin` builds passed against this implementation.
- The repository-scoped MCP wrapper completed a stdio EOF smoke test against
  the official `mss-mcp v1.3.7` binary.

No database migration, Kubernetes operation, deployment or remote push was
performed as part of the 1.3.7 source upgrade verification. Existing databases
remain blocked on the documented `20260830193000` permission-collision review.
