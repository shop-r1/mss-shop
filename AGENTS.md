# Mss Shop Agent Contract

## R1Shop architecture memory

- Treat `docs/architecture/overall-solution.md` as the current system design,
  `docs/decisions/` as immutable decision history, and
  `docs/migration/roadmap.md` as the staged delivery plan. Update the relevant
  document in the same change whenever an architectural invariant changes.
- The tenant platform is the control plane. A mall platform instance is a data
  plane bound to exactly one tenant and one fixed core/business schema pair;
  do not add request-time schema switching or allow a client to select a
  schema.
- The in-cluster reconciler is the only component allowed to create or mutate
  tenant database roles, schemas, snapshots, compatibility views and grants.
  It has no Kubernetes API identity. Fixed trusted operator commands own stage
  application Secrets and workload objects after fail-closed collision checks;
  HTTP handlers only record desired state.
- Storefront clients use the dedicated `/app/v1` contract. They do not call the
  MSS Admin API and must never receive database or schema identifiers.
- User-visible behavior is internationalized from the first change. Keep
  `zh-CN` and `en-US` complete today, use stable message/error keys, and follow
  `docs/architecture/internationalization.md` before adding a locale.
- For legacy Admin work, treat `docs/migration/legacy-tables.yaml` as the
  authoritative 54-table inventory and
  `docs/migration/legacy-admin-acceptance-matrix.md` as the business coverage
  baseline. Follow `.agents/skills/r1shop-legacy-module/SKILL.md`; never use a
  generic editor as a substitute for order, inventory, payment, wallet,
  promotion or import workflows.
- Legacy compatibility ownership is 50 mall resources plus the one
  tenant-platform `payments` resource. All 51 generic compatibility resources
  are read-only. The checked-in
  source includes forward-only ownership migrations, but they are not evidence
  that any environment has applied or deployed them. Do not enable generic
  mutation until one resource and operation has restored and proved its legacy
  semantics. Never search, filter or sort a JSON column with declared nested
  secrets, including `system_configs.metadata`.
- `r1shop-prod` and its TimescaleDB/Redis are live. This repository currently
  has no `mss-shop` production namespace, Deployment, resource-named RBAC or
  GitHub Environment credential. Do not infer those targets, create an
  executable production CD path, or point an MSS image at the existing
  `r1shop-prod` workloads. A future exact image-only promotion requires the
  production boundary to be reviewed first and must pause for a GitHub
  Environment approval performed by a human. An AI or agent must never approve,
  bypass, simulate or use a user's session to satisfy that review. Production
  topology review must include every container and init container: changing an
  image recreates Pods and can indirectly run a `migrate` init container, so
  forward compatibility and rollback must be approved even for an image-only
  change. Production writes still require explicit approval for the exact
  action. System verification runs in disposable Kubernetes Pods; never migrate
  production data while developing this repository.
- The original `r1shop-dev` environment is immutable from this repository's
  point of view. Never create, update or delete its application resources,
  PostgreSQL objects or ACLs, Redis data/configuration, Secrets, Services,
  Ingresses, roles or storage. The MSS stage uses the separate
  `mss-shop-dev` namespace with its own PostgreSQL instance/PVC, Redis
  instance/PVC, credentials and workloads as recorded by DEC-0010, with Admin
  TLS and host cutover recorded separately by DEC-0011.
  Legacy development data may enter that environment only through a bounded
  read-only snapshot operation that makes no source-side change and copies no
  `orders` or `order_goods` rows. The importer is the only one-time exception
  to the reconciler's target-database mutation ownership: it may initialize
  only an empty, exact-marker `mss_shop_dev` target, emits a receipt, and has
  no Kubernetes API identity. Its source snapshot must match the exact
  `plpgsql 1.0`/`pg_catalog` and `timescaledb 2.20.2`/`public` inventory,
  91 object-level TimescaleDB `public` routine members, zero other routines,
  zero standalone types and the reviewed instance-bound complete `pg_proc`
  SHA-256 recorded in the migration contract. A restore, extension reinstall
  or OID drift requires a new read-only review; never relax this at run time.
  A deployment command must fail closed if it targets
  `r1shop-dev`, `database/timescaledb-r1shop-dev`, or
  `database/redis-r1shop-dev` for mutation.
- The only automatic development deployment is the DEC-0012 image-only path.
  After a same-repository `codex/**` pull request to `main` passes CI and
  publishes all four images for its exact head SHA, the local reusable CD may
  use the `mss-shop-dev` GitHub Environment credential to run `kubectl set
  image` for only `mss-shop-tenant-admin` and
  `mss-shop-mall-admin-aussibuy`, updating both `migrate` and `admin`. It must
  not mutate configuration, databases, Secrets, networking, the original
  development environment or production, and it is not acceptance evidence.
  Its namespace identity is the fixed `mss-shop-dev-image-updater`
  ServiceAccount/Role/RoleBinding/token Secret set; the Role permits only
  `get`/`patch` on those two Deployments. Account these four DEC-0012 access
  objects separately from DEC-0010's 24 infrastructure objects and six
  foundation Secrets. The first verified execution is GitHub Actions run
  `33574863356` for PR-head revision
  `bf07098cb8a7c5f2c52993e28c69afc7712c4d98`; both Deployments reached
  1/1 updated, ready and available with zero container restarts. This is CD
  operation evidence only, not system or browser acceptance.
- Work on `codex/...` branches and never push directly to `main`. Local work is
  code development and focused checks only. Push a completed development slice
  to its branch, and open a same-repository pull request to `main` when the
  accumulated work is ready for the shared development cycle. The pull request
  is the only automatic CI, four-image publication and `mss-shop-dev` deployment
  path; a branch push without an open pull request and a push to `main` must not
  run that pipeline.
- Bind cluster and in-app-browser verification to the latest complete pull-
  request head SHA actually deployed by DEC-0012. Any subsequent push changes
  the head, invalidates the prior deployment/acceptance claim, and requires CI,
  development image refresh and the applicable disposable-Pod and browser checks
  again. Keep iterating on the same pull request until its latest head passes and
  leave the two development URLs running for owner review.
- Only the latest accepted pull-request head may be squash-merged to `main`,
  producing one new main commit. Before the final CI/acceptance cycle, bring the
  branch up to date with current `main`; that synchronization creates a new head
  and invalidates older evidence. Record the pull request, accepted head SHA,
  resulting squash-main SHA, matching source tree and image digests because the
  two commit SHAs differ. Main performs no tests, builds or image publication. A
  future production promotion must reuse the already accepted pull-request-head
  images, change only exact approved image fields, account for Pod-restart and
  init-container migration effects, and remain blocked on human GitHub
  Environment review; the agent must stop at that gate.
- Do not commit credentials, DSNs, tokens, private keys, database files, logs,
  build caches, or machine-specific absolute paths.

## Mission

This repository is a thin business host for the `mss-boot-admin`
Admin Distribution. The complete Admin backend and frontend are consumed as
versioned dependencies; this repository owns only business modules, project
configuration, deployment, tests, and generated composition glue.

## Source ownership

- Edit business specifications under `.mss/modules/` and `.mss/features/`.
- Put custom backend behavior in `internal/modules/<name>/` files without a
  generated header.
- Put custom frontend behavior under `web/src/business/`.
- Register handwritten backend modules in `internal/modules/custom/modules.go`.
- Add handwritten Umi routes in `web/src/business/routes.config.ts` and their
  frontend-only server-path projections in
  `web/src/business/route-registrations.ts`. That file does not create Admin
  Menu or Casbin rows and does not authorize a request.
- Add handwritten Simplified Chinese and English messages together in
  `web/src/business/locales/zh-CN.ts` and
  `web/src/business/locales/en-US.ts`. Managed locale facades compose Admin
  core, generated module, and handwritten messages in that order.
- Do not edit files carrying `Code generated by mss`; change the source spec
  and regenerate them.
- Do not copy Admin core sources, the complete Admin Web source tree, or
  `mss-boot` into this repository.

## Security and validation

- Backend authorization remains authoritative; UI visibility is advisory.
- The protected business route group authenticates the session but does not
  infer a handwritten module's business permissions. Every handler must use the
  injected principal and request database to enforce its explicit backend
  permission, with positive and negative tests.
- Every handwritten permission needs a forward migration that creates or
  validates its Admin permission metadata and default role policies, plus a
  readiness check that proves the migration and policy are present before the
  module becomes ready.
- Never commit credentials, production DSNs, tokens, private keys, logs, or
  local absolute paths.
- Keep business routes behind the complete Admin middleware and readiness
  boundary.
- Use the scoped `.agents/skills/mss-thin-host/SKILL.md` workflow for business
  changes.
- Use `.agents/skills/r1shop-legacy-module/SKILL.md` in addition for a module
  that reads or writes a legacy business table.
- Run `mss doctor --strict` after setup and `mss verify --all` before opening a
  pull request.
- Run `tools/check-project-memory.sh` whenever implementation status,
  architecture, migration contracts, acceptance evidence, Skills or MCP
  configuration change; documentation drift is a failing deliverable.

## Admin Web package

- `web/.npmrc` uses the public npm registry and exact dependency pins. Installing
  `@mss-boot-io/admin-web` must not require a long-lived registry token.
- GitHub Packages remains a release mirror for compatibility and evidence. A
  host that explicitly selects that mirror must inject `NODE_AUTH_TOKEN` only
  for the command process and must never commit the expanded token.

## Admin Distribution upgrades

- Install the matching released `mss` tool and plan the complete backend,
  frontend, and host update with `mss upgrade admin <vX.Y.Z>`.
- Review managed changes, regenerated modules, conflicts, preserved business
  files, and validation commands before applying.
- The five default handwritten extension files deliberately have no generated
  header. Keep their backend registry, frontend route metadata, and bilingual
  locale edits explicit; upgrades preserve them when the Blueprint defaults
  have not changed.
- Apply only a conflict-free reviewed plan with `--apply --yes`; never upgrade
  the Go Admin Module and Admin Web package independently.

Generated by `mss` v1.3.7 from the management-system Thin
Host Blueprint.
