# CI and delivery image runbook

The authoritative workflow is `.github/workflows/ci.yml`. It keeps the merge
signal deliberately small: backend unit tests, Admin Web unit tests, executable
project/memory contracts, platform-boundary validation and buildability of the
four delivery images. It never deploys an image or mutates Kubernetes,
PostgreSQL, Redis, Secrets, Cloudflare or a production service.

## Triggers and gates

Pull requests, workflow dispatches and pushes to `main` or `codex/**` run the
same validation jobs:

1. `go test ./...` independently in the root module, tenant-platform and
   mall-platform;
2. locked Admin Web unit tests independently in the root, tenant-platform and
   mall-platform projects;
3. strict MSS 1.3.7 diagnosis, `tools/check-project-memory.sh` and the platform
   boundary check.

After those jobs pass, pull requests and workflow dispatches build all four
images for `linux/amd64` without authenticating to GHCR and without pushing.
A qualifying branch push publishes all four images. Race checks, vet, lint,
complete `mss verify --all`, cluster tests and browser acceptance remain
milestone evidence rather than mandatory image-publish gates.

## Four published images

| Runtime | Build context / Dockerfile | GHCR package |
| --- | --- | --- |
| Tenant control plane | `apps/tenant-platform/Dockerfile` | `ghcr.io/shop-r1/mss-shop-tenant-platform` |
| Mall management platform | `apps/mall-platform/Dockerfile` | `ghcr.io/shop-r1/mss-shop-mall-platform` |
| Isolated database reconciler | repository root / `services/reconciler/Dockerfile` | `ghcr.io/shop-r1/mss-shop-reconciler` |
| One-time legacy importer | repository root / `services/legacy-importer/Dockerfile` | `ghcr.io/shop-r1/mss-shop-legacy-importer` |

Every published image receives only the complete Git SHA tag. There is no
mutable `latest`, branch or environment tag. OCI labels bind source and
revision; published builds request maximum provenance and an SBOM.

For each image, the workflow writes and uploads a 30-day immutable JSON
receipt named `image-receipt-<package>-<full-sha>`. The receipt records the
repository, revision, nonzero manifest digest, tag-plus-digest reference,
`linux/amd64`, workflow and run identity. Deployment consumes the digest from
the receipt and still retains the full-SHA tag for human traceability.

All four receipts must belong to the same successful run and revision before
isolated staging. A missing importer or reconciler receipt blocks deployment.

### Historical evidence

GitHub Actions run
[`33451906040`](https://github.com/shop-r1/mss-shop/actions/runs/33451906040)
successfully published the two Admin images for revision
`ac74347cbbd6cd24f731dadd239c1044ff132e38` before the reconciler/importer
matrix and receipt format existed. Its tenant digest was
`sha256:c4d0e651553263f8cf8127351ee3d14d13076c0b23052f64ecb017d8cd2dbef0`;
its mall digest was
`sha256:2fa16ec9cf3854662726f64de978940653b3133a63b2e1951dd189656f34bc1e`.
This remains historical build evidence only and cannot satisfy the current
four-image deployment gate. No complete four-image publication is recorded as
verified yet.

## Permissions and package source

Validation and build-only jobs have read-only repository permission. Only the
push publication job receives `packages: write`, using the workflow-scoped
`GITHUB_TOKEN`. Pull requests never receive package write access.

The locked MSS Admin Web 1.3.7 packages come from their GitHub Release
artifacts; remaining public frontend packages come from npm. Lockfiles pin the
resolved sources and integrity metadata. No long-lived registry credential is
committed.

## Deployment boundary

Publishing an image is not deployment approval. The workflow contains no
Kubernetes client invocation, environment credential and rollout step.
Production and the original `r1shop-dev` environment are not CI targets.

Manual isolated rollout follows DEC-0010 and the remote acceptance runbook:

1. verify a clean full SHA and all four CI receipts;
2. fingerprint the old environment through the bounded read-only path;
3. create the exact 24 `mss-shop-dev` infrastructure objects with
   `stage-infrastructure`; the first create-only pass may stop after the two
   inert storage binders, and a clean retry admits StatefulSets only after the
   stable cluster-wide node/local-path exclusivity check;
4. create six immutable foundation Secrets with `stage-secrets` and prove
   isolated datastore readiness;
5. create the one-time importer Job from
   `deploy/mss-shop-dev/legacy-import-job.yaml`, then persist and verify its
   full logs, receipt and database marker;
6. prove the receipt binding and zero rows in both `orders` and `order_goods`
   from a disposable in-cluster Pod;
7. after the separate application/bootstrap Secret operator gate passes,
   create the receipt-bound reconciler Job from
   `deploy/mss-shop-dev/reconciler-job.yaml`;
8. stage only the eight Admin runtime objects from
   `deploy/mss-shop-dev/admin-runtime.yaml` using the tenant and mall image
   digests;
9. run disposable-Pod system verification and in-app-browser acceptance.

The importer Job renderer/create-only path, disposable verification runner and
post-receipt application/bootstrap Secret operator are implementation gates
until their repository commands and tests are finalized. Do not replace them
with ad-hoc template substitution or `kubectl apply`.

The reconciler and importer images are fixed isolated-development tools, not
production-capable general operators. Storefront API and worker images remain
outside the current delivery matrix until those components own complete
delivery entrypoints and Dockerfiles.
