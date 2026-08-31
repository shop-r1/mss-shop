# CI and delivery image runbook

This repository uses one GitHub Actions workflow at
`.github/workflows/ci.yml`. It deliberately keeps the merge signal small:
unit tests, the executable project contracts, and container buildability.
It never deploys an image or mutates a database, Kubernetes resource, Secret,
Cloudflare project, or production service.

## Triggers and gates

Pull requests and pushes to `main` or `codex/**` run the same three gates:

1. `go test ./...` independently in the root module, tenant-platform and
   mall-platform;
2. the locked Admin Web unit tests independently in the same three projects;
3. strict MSS 1.3.7 environment diagnosis, project-memory contracts and the
   platform boundary check.

After those gates pass, Buildx builds both delivery images for `linux/amd64`.
A pull request proves that each Dockerfile builds but does not authenticate to
GHCR and does not push an image. A branch push publishes the images.

Race checks, vet, lint, complete `mss verify --all`, browser acceptance and
cluster system tests remain valuable milestone evidence, but they are not
duplicated as mandatory image-publish gates. Run the risk-appropriate checks
locally before a pull request, including the full MSS verification required by
each Thin Host's `AGENTS.md`.

## Published images

| Runtime | Build context | GHCR package |
| --- | --- | --- |
| Tenant control plane | `apps/tenant-platform` | `ghcr.io/shop-r1/mss-shop-tenant-platform` |
| Mall management platform | `apps/mall-platform` | `ghcr.io/shop-r1/mss-shop-mall-platform` |

Every published image has one immutable tag: the complete Git commit SHA.
There is no mutable `latest`, branch or environment tag. The OCI labels record
the repository source and exact revision.

The root Dockerfile remains a phase-zero MSS proof and is not a delivery
runtime. Storefront API, reconciler and worker images are not published until
those components own production entrypoints and Dockerfiles; an empty or
misleading placeholder image is not a release artifact.

## Permissions and package source

All validation jobs have read-only repository permission. Only the image job
receives `packages: write`, and it uses the workflow-scoped `GITHUB_TOKEN` for
push events. Pull requests never receive package write access.

Admin Web dependencies install from the public npm registry with exact lock
files. No npm, GitHub Packages or other long-lived registry token is committed
or required by this workflow.

## Deployment boundary

Publishing an image is not a deployment approval. Development rollout remains
a separate, deliberate action. Any production rollout requires explicit
approval for the exact namespace, resources and image SHA, followed by the
production health checks in the workspace safety policy.

Rollback means selecting a previously verified full-SHA image in a separately
approved deployment operation. The CI workflow itself has no rollout or
rollback step.
