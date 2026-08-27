# Project status

Last verified: 2026-08-28

## Confirmed decisions

- Admin foundation: `mss-boot-admin` Distribution `v1.3.6`, consumed as an
  exact backend/frontend dependency without forking MSS core.
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

- A runnable MSS 1.3.6 Thin Host proof of concept at the repository root.
- MSS-generated project contracts and seven MSS workflow Skills.
- Versioned target architecture, migration plan, i18n policy and project Skill.
- A project-scoped `mss-mcp` connection whose wrapper rejects any version other
  than 1.3.6.

## Not implemented yet

- The final `apps/tenant-platform` and `apps/mall-platform` Thin Hosts.
- Tenant lifecycle control tables and the reconciler.
- Per-tenant PostgreSQL schema roles and credentials.
- The `/app/v1` OpenAPI contract and storefront service.
- Legacy business data migration or any production cutover.

## Next milestone and acceptance criteria

Create clean Thin Hosts in `apps/tenant-platform` and `apps/mall-platform` with
the MSS generator, keeping frontend and backend together in each app directory.
Acceptance requires both hosts to pass `mss doctor --strict` and
`mss verify --all`, use exact Distribution 1.3.6 dependencies, and contain no
copied MSS core source. The proof-of-concept root can be retired only after the
new hosts reproduce its login and authorization checks.

## Verification evidence

Verified locally on 2026-08-28:

- `GOTOOLCHAIN=go1.26.6 mss doctor --strict` reported ready with exact MSS,
  Go, Node and pnpm versions.
- `GOTOOLCHAIN=go1.26.6 mss verify --all` completed successfully, including
  Thin Host boundaries, backend build/tests, frontend lint/tests/build and Git
  text checks.
- The repository-scoped MCP wrapper completed a stdio EOF smoke test against
  the official `mss-mcp v1.3.6` binary.

No database migration, Kubernetes operation, deployment or remote push was
performed as part of this verification.
