# DEC-0012: Refresh isolated Admin images from qualifying pull requests

Status: accepted
Date: 2026-09-02
Extends: DEC-0010 development delivery and Admin runtime ownership

## Context

DEC-0010 deliberately separated image publication from the reviewed bootstrap,
data import, reconciliation and first Admin rollout. That separation was needed
while the isolated namespace, datastores, compatibility projections and ingress
contract were still being created and verified.

The isolated `mss-shop-dev` Admin runtimes now exist and their DNS-only HTTPS
entrypoints are stable. Repeating the complete bootstrap and acceptance sequence
for every development pull request would add cost without changing those
resources. The project owner instead wants a small development CD path that
refreshes only the two already-owned Admin workload images after the existing CI
has succeeded.

This repository is public, but it is not an invitation to deploy code from an
arbitrary fork. A privileged deployment workflow must therefore distinguish a
same-repository development branch from untrusted pull-request input, and its
cluster credential must never be committed to source.

The deliberately simple local-reusable design treats people who can push a
`codex/**` branch in this repository as trusted development collaborators. The
called workflow is resolved from the same commit as its caller, so that trust
includes changes to the workflow definition itself. This is acceptable only
because the Environment kubeconfig is bound to the resource-named Role below;
it must never be replaced with a cluster-admin, SSH or production credential.

## Decision

- A qualifying change is a pull request whose head repository is this
  repository, whose head branch matches `codex/**`, and whose base branch is
  `main`.
- The existing CI remains the validation and image-publication owner. The
  development CD call is admitted only after that pull request's CI succeeds
  and the same complete head SHA has published all four delivery images and
  their receipts. A failed, cancelled, forked, non-`codex/**` or non-`main`
  pull request does not receive the deployment credential and does not call
  CD.
- CI invokes the repository-local reusable workflow
  `.github/workflows/dev-cd.yml`. The reusable workflow is not a general
  Kubernetes deployment interface: its revision comes from the qualifying PR
  head and its environment, namespace, workloads, containers and image
  repositories are fixed in source.
- CD authenticates as the namespace-local
  `ServiceAccount/mss-shop-dev-image-updater`. Its fixed access bootstrap is
  exactly one ServiceAccount, one Role, one RoleBinding and one service-account
  token Secret, all named `mss-shop-dev-image-updater` in `mss-shop-dev`. The
  Role grants only `get` and `patch` on
  `Deployment/mss-shop-tenant-admin` and
  `Deployment/mss-shop-mall-admin-aussibuy`; other Deployments, Secrets and
  Pods are denied. The reusable workflow neither creates nor changes these
  access objects.
- Those four access-bootstrap objects are owned by DEC-0012 and are accounted
  separately. They do not change DEC-0010's exact 24 application-infrastructure
  objects or six foundation Secrets, and the token Secret is not a foundation
  application credential.
- The reusable workflow may run only against the GitHub Environment
  `mss-shop-dev`. Its Kubernetes client configuration is supplied at run time
  through the Environment secret `MSS_SHOP_DEV_KUBECONFIG`. The kubeconfig,
  client keys, tokens, certificates and Secret value are never committed,
  uploaded as workflow artifacts or printed.
- The only Kubernetes mutations are the image changes made by `kubectl set
  image` to these existing objects in namespace `mss-shop-dev`:

  - `Deployment/mss-shop-tenant-admin`: init container `migrate` and container
    `admin` both receive
    `ghcr.io/shop-r1/mss-shop-tenant-platform:<full-pr-head-sha>`;
  - `Deployment/mss-shop-mall-admin-aussibuy`: init container `migrate` and
    container `admin` both receive
    `ghcr.io/shop-r1/mss-shop-mall-platform:<full-pr-head-sha>`.

- The two image changes naturally cause Kubernetes to replace the affected
  Pods, and each new Pod runs its matching `migrate` init container. CD does
  not update annotations, ConfigMaps, Services, Ingresses, Certificates,
  Secrets, RBAC, databases or business data. It does not create or delete an
  object, wait for rollout, run system or browser verification, or record an
  acceptance result.
- The reconciler and legacy-importer images are still built and published for
  delivery consistency, but this CD path never runs either image and never
  creates a Job.
- The original `r1shop-dev` environment, the shared `database` namespace,
  `r1shop-prod` and every production resource remain outside this workflow.
  This decision grants no production deployment permission and no mutation of
  the legacy source.
- The workflow source being present is only configuration evidence. Automatic
  deployment becomes operational only after the `mss-shop-dev` Environment
  and its secret are configured outside Git. No document may claim a
  successful CD execution until a qualifying run has actually completed.

## Consequences

The development environment can follow a reviewed PR head without repeating
the one-time infrastructure, import, reconciliation, TLS or host-cutover
stages. The change is intentionally a tag-only development convenience: it is
not a replacement for digest-bound release staging, cluster verification,
browser acceptance or production promotion.

This choice favors the requested small internal-project pipeline over a
separate default-branch `workflow_run` trust boundary. If repository branch
write access becomes untrusted, the CD trigger must move to a workflow fixed on
the protected default branch before any broader credential is introduced.

Historical evidence remains bound to the revisions and digests it already
names. A later automatic image refresh does not rewrite that evidence or close
any business-acceptance scenario. The GitHub Environment credential is now
configured outside Git; until a qualifying run succeeds, the repository still
records this CD path as configured but not executed.

The four access-bootstrap objects have been created in `mss-shop-dev` and an
authorization check has proved the exact two-Deployment `get`/`patch` grant and
denial of other Deployments, Secrets and Pods. The Environment and named secret
have also been configured without committing their value; the first qualifying
CD success remains pending.
