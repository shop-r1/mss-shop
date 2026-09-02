# CI and delivery image runbook

The authoritative CI workflow is `.github/workflows/ci.yml`. It keeps the
merge signal deliberately small: backend unit tests, Admin Web unit tests,
executable project/memory contracts, platform-boundary validation and
buildability of the four delivery images. CI performs no direct Kubernetes
command. For the one qualifying development pull-request shape, it may call
the repository-local reusable `.github/workflows/dev-cd.yml` after successful
four-image publication; DEC-0012 fixes that workflow to two Admin image
updates in `mss-shop-dev`.

## Triggers and gates

Pull requests targeting `main`, workflow dispatches and pushes to `main` or
`codex/**` run the same validation jobs:

1. `go test ./...` independently in the root module, tenant-platform and
   mall-platform;
2. locked Admin Web unit tests independently in the root, tenant-platform and
   mall-platform projects;
3. strict MSS 1.3.7 diagnosis, `tools/check-project-memory.sh` and the platform
   boundary check.

After those jobs pass, workflow dispatches and non-qualifying pull requests
build all four images for `linux/amd64` without pushing. Pushes to `main` or
`codex/**` publish all four images. A pull request also publishes all four
images only when its head is in this repository, its head branch matches
`codex/**`, its base is `main`, and it is not a Dependabot change. That
publication is bound to the exact PR head SHA and must finish before the local
reusable dev CD is called. Race checks, vet, lint, complete `mss verify --all`,
cluster tests and browser acceptance remain milestone evidence rather than
mandatory image-publish or CD gates.

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
`linux/amd64`, workflow and run identity. The narrow development CD waits for
all four receipt-producing jobs, then updates the two Admin Deployments with
the immutable full-SHA tag; it does not download or parse artifacts.

All four receipts must belong to the same successful run and revision before
isolated staging. A missing importer or reconciler receipt blocks deployment.

## Qualifying pull-request development CD

The `Dev CD` reusable workflow is a deliberately narrow continuation of CI,
not a general deployment tool. It accepts the qualifying PR's complete head
SHA as its only image tag. The caller must already have passed the three CI
job families above and published all four same-SHA images and receipts.

The job references the GitHub Environment `mss-shop-dev` and materializes its
Kubernetes client configuration only from the Environment secret
`MSS_SHOP_DEV_KUBECONFIG` on the ephemeral runner. The Environment and named
secret are configured outside the repository; the value is never stored in
Git, uploaded as an artifact or printed. Configuration alone cannot be
reported as a successful deployment. The local reusable-workflow caller uses
`secrets: inherit`, which is required for GitHub to propagate the secret
context into the called job; the repository and organization currently expose
no other Actions secrets, and the called workflow references only the named
Environment secret.

The kubeconfig represents the namespace-local
`ServiceAccount/mss-shop-dev-image-updater`. Its Role and RoleBinding have the
same name and grant only `get`/`patch` on the two exact Admin Deployments. The
same-named service-account token Secret completes the four-object access
bootstrap. Authorization checks have denied other Deployments, Secrets and
Pods. These objects are present but are accounted separately from the exact 24
DEC-0010 infrastructure objects and six foundation Secrets; `Dev CD` never
creates or mutates them.

The workflow runs exactly two `kubectl set image` commands in namespace
`mss-shop-dev`:

- `Deployment/mss-shop-tenant-admin`: set both `migrate` and `admin` to
  `ghcr.io/shop-r1/mss-shop-tenant-platform:<full-pr-head-sha>`;
- `Deployment/mss-shop-mall-admin-aussibuy`: set both `migrate` and `admin` to
  `ghcr.io/shop-r1/mss-shop-mall-platform:<full-pr-head-sha>`.

Changing the Pod template causes Kubernetes to replace Pods and run each
matching `migrate` init container. The CD workflow itself issues no database
or migration command. It does not update an annotation, ConfigMap, Service,
Ingress, Certificate, Secret or RBAC object; deploy the reconciler or importer;
wait for rollout; run a test; or produce acceptance evidence. It never targets
`r1shop-dev`, the shared `database` namespace or production. Concurrent calls
are serialized under one development-image concurrency group rather than
cancelled midway.

This tag-only refresh is a development convenience. Digest-bound manual
staging remains authoritative for bootstrap, data reconciliation, TLS/host
changes, evidence-bearing releases and any future production promotion.

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
four-image deployment gate.

Run
[`33487529898`](https://github.com/shop-r1/mss-shop/actions/runs/33487529898)
later verified the complete four-image receipt contract for revision
`12c6a682e38bfef165e09d108e0bd77c53ee73ca`. Its immutable digests were:

- tenant-platform:
  `sha256:8f6348da987fe8fcd30553583c19319feac862d69d33f5ed43651a70eeb02d35`;
- mall-platform:
  `sha256:5880261198942ad53507e3aa087bbb949e96a42f0472d0d110ea13e1e8ebdd15`;
- reconciler:
  `sha256:f53404fee6fed5b77c758358c14a28d7b4197a8172393f8003857e7fac56ac71`;
- legacy-importer:
  `sha256:9eb9efcb01ff5ac115b6df772f68139cba9ce53f691a310756f81cceb161fb05`.

That revision passed readiness but its importer stopped before the target
transaction on an over-broad source-routine policy. It is historical
publication/readiness evidence only; a source change requires a new four-image
run and its receipts can never be reused for another revision.

Run
[`33494258866`](https://github.com/shop-r1/mss-shop/actions/runs/33494258866)
verified the complete four-image receipt contract for successful import
revision A `6fed45f354e93efe104045c6dde86ac33c368d6d`. Its immutable digests were:

- tenant-platform:
  `sha256:fe8db92bce70c12be7d0f6ad8de60e05d65322c2b3aecf384d212deb35abe3fd`;
- mall-platform:
  `sha256:4db62ab4c264714fd0c23a1033935d569edfd9f54c1c37d4f58f6299df50d925`;
- reconciler:
  `sha256:8cb1e54946b442efd5f33fc433b06bd9e663ec85c81091b8518861855b92e781`;
- legacy-importer:
  `sha256:881f105ea00dfac3bf4381e0177ad1349998d51059beeb155e1a96c64bbe3ba3`.

The exact importer digest passed the revision-bound readiness Job and produced
the committed canonical import receipt. It is revision-A provenance only; the
independent verifier must use a new clean revision-B importer digest.

Run
[`33497583981`](https://github.com/shop-r1/mss-shop/actions/runs/33497583981)
verified the four-image contract for revision B
`3eb4c72b485066e7b189446fab5b66a1047e66a2`. Its immutable digests were:

- tenant-platform:
  `sha256:69e790e145e81c8d1586d8c829ba45152cdfd14d508beb2c3b758bcfb4b57e43`;
- mall-platform:
  `sha256:e5415ff0dec41d08d315d74426b788e4c7cb189a39ee5a587d04364e37289f40`;
- reconciler:
  `sha256:abb9703daeaf8bab16bd6c020390b1da1de1bce31e664589a1c67749c46ec810`;
- legacy-importer/verifier:
  `sha256:a3e1609e75164187557c9207f3565efe7bf8fb413b0adc7f6cceb71c1d531799`.

The last digest is the exact image used by the one successful disposable
verifier Job. It is workload provenance for B, independent of later stage-tool
fixes.

Run
[`33500133380`](https://github.com/shop-r1/mss-shop/actions/runs/33500133380)
verified and published all four receipts for annotation-compatibility fix
revision `ebefd1c20bf51f3c43e4a2bb90085fb60ea21442`:

- tenant-platform:
  `sha256:9fe3463cbf88e4312f5b6735e25470d62fe5c9eb685f8cd8d83a3e7b57a34467`;
- mall-platform:
  `sha256:636159961ab6c0bc9303ed9e9ef759dea30510ca9a84c3e33d829e636d678a2c`;
- reconciler:
  `sha256:3afa8204a0f14dfbbe1fd9510d1c0a0e0cd0153f13dc4e3421bde56810705baf`;
- legacy-importer:
  `sha256:80a382e42aa43d49c2e4431798d72699d12077106c9fa7755466df28e822adc3`.

No image from this run was deployed. The fix was used only to validate the
existing B Job as a read-only exact retry. A new B2 verifier preflight failed
closed at the immutable B-bound receipt ConfigMap and created no Job or Pod.

Run
[`33503127917`](https://github.com/shop-r1/mss-shop/actions/runs/33503127917)
verified and published all four receipts for evidence revision C
`fc6d1bf357ca7291a0fc2fe4391ca15628f8e9b9`:

- tenant-platform:
  `sha256:87a2ba402b9dc5f82769b4fbf4c1b1220368483dde6a4c6fc580507328f05750`;
- mall-platform:
  `sha256:22a02242cc815ec7e2bf29fc5f9ec86789a245074754f49b59bc2b7def66c92e`;
- reconciler:
  `sha256:0beece2f39be8892649981db69bb20ebef6c6b27a6ab8741d4cf129b5d6a3af5`;
- legacy-importer:
  `sha256:385e5161b5a2133a482cb34330092d4e94e9e3c48d7b1a7ca01dc5f4b7e3bb38`.

The corresponding tenant, mall, reconciler and legacy-importer receipt-file
SHA-256 values are respectively
`3e377cb929b36ae4a5a972465ae8f0c6a040c94f2885238e5ba5c45546723f39`,
`cc381da20c2ac33e0046317cfaa3fd2c71bf37db8e8d96256ed7f4fe7659fa09`,
`fa2cebf94e67dfa6e291087657b4c36847879d18c2f81850299e3a298aeb3913`
and `15905b3e866410736ea407faece74a8bf57f589ef0443cc24c5e61bee44dc223`.
This run binds the committed verifier evidence and synchronized project
memory. No image from the run was deployed. A later source change to the
reconciliation-secret safety path requires a new successful four-image run;
revision-C receipts cannot authorize that changed operator or reconciler.

Run
[`33532383550`](https://github.com/shop-r1/mss-shop/actions/runs/33532383550)
successfully validated and published all four images for the isolated Admin
release revision
`3e64a57dae8bb3dd4d337a423015baae6c352b32`. Its immutable manifest digests
and receipt-file SHA-256 values were:

| Image | Manifest digest | Receipt-file SHA-256 |
| --- | --- | --- |
| tenant-platform | `sha256:c65f5e8b19033afcdae25e0ec046efc958190a0abf38ab1d2bf379d0475b742d` | `6b397aa60fe05c15a7fb5be18236c284ca99abdc728072a29197aac4207f2e18` |
| mall-platform | `sha256:a58868c78bc3e62f40b6988ec43eb4923f00d15ecc8540eb06b6b863016e1c1a` | `5e79dd78c44c63d9f693b02d00b0f54c871e3385bfb75588bfa93ea35fd17f41` |
| reconciler | `sha256:fba8a63938eef780e8eeb68e2c391bd91ad01c4214dcfa6a7089cf75cc1ab4fd` | `ea341c408c045cf547722aff34b3e03e86398d0592162403f982be5dad0a1a6c` |
| legacy-importer | `sha256:0d2d6077798328227e2b19a14d8075e25de0cdccdee5100a118ec3a888fa0bb0` | `e865042489f1a858581e0aff1afce884e3a74ac27c66850d4dbad0d9f056e82f` |

This CI run did not deploy anything. Manual staging used the exact reconciler,
tenant and mall digests above. Two earlier same-day reconciler publications
were exercised first: run
[`33529160661`](https://github.com/shop-r1/mss-shop/actions/runs/33529160661)
for `43e0fd5f18af903f076ec166efff68365dcb3a55` and run
[`33530586653`](https://github.com/shop-r1/mss-shop/actions/runs/33530586653)
for `ddb67bef4bf0b4eeae7408eb5706ad63e687dce6`. Their reconciler Jobs failed
closed, their database transactions did not commit, and post-attempt checks
found zero managed schemas, roles and relations. Those images are failed-run
provenance only and must not be reused as release images.

The final `3e64a57...` reconciler Job completed with the receipt-bound database
plan, its same-SHA projection verifier emitted the sole successful JSON, and
the tenant and mall digests above now back the isolated Admin Deployments.
Detailed immutable workload evidence is in
`docs/evidence/mss-shop-dev/2026-09-02-reconciliation.yaml` and
`docs/evidence/mss-shop-dev/2026-09-02-runtime-system-acceptance.yaml`.
This technical release and its read-only checks close none of the 31 business
acceptance scenarios.

Run
[`33565434916`](https://github.com/shop-r1/mss-shop/actions/runs/33565434916)
successfully validated and published all four images for the DNS-only HTTPS
Admin cutover revision
`f202b094fd5b2839a9020ff38db833fec40be704`:

| Image | Manifest digest | Receipt-file SHA-256 |
| --- | --- | --- |
| tenant-platform | `sha256:75ed6e8e2b42aad4a88e618f6cd9b2d0197ad12f15392c47ea458b2f3433f39d` | `15aa8a0aff1522283c1a21afd1764eb3b29b2c48ee637be5b0803ca0f7bf308b` |
| mall-platform | `sha256:32e9497279393e7cd5bc0896e594f52697cb6092a939f61e1939cb5c86208b50` | `1e97303375591f33e15b34248a1bde21574ff8c3c13692b89a23c68180a6df26` |
| reconciler | `sha256:9687e5706481c6d0f8372b3d884c699c2f03e6d78f18200815d239dbd7a1cbb3` | `a7342e9de6a3ccf425d48b3274a56ebf1f254b9df3bed45d5be6091ce03f44c1` |
| legacy-importer | `sha256:ea6caef74249251e349a736ceb56d15ee832cc11207963c6ba05ede54043e0ae` | `61b050286b0bc0cbe0def4ecfb8af6571ab2da27189c44039858b7035f4c6205` |

Manual staging consumed the exact tenant and mall digests above. The TLS stage
and eight-object runtime stage changed only `mss-shop-dev`; both trusted HTTPS
hosts then passed confirmed-login workspace smoke review. CI itself still
performed no deployment.

## Permissions and package source

Validation and build-only jobs have read-only repository permission. Only the
publication job receives `packages: write`, using the workflow-scoped
`GITHUB_TOKEN`; on a pull request that permission is exercised only for the
same-repository `codex/**`-to-`main` shape. A fork never receives package or
Environment credentials.

Only the called `Dev CD` job references the `mss-shop-dev` Environment and its
Kubernetes secret. The job does not check out the application tree or consume
a pull-request artifact, but the local reusable workflow is resolved from the
same commit as the caller. Accordingly, same-repository `codex/**` writers are
trusted development collaborators and the kubeconfig must remain restricted
to the exact two-Deployment Role; it must never contain cluster-admin, SSH or
production access. No cluster credential is stored in the repository.

The locked MSS Admin Web 1.3.7 packages come from their GitHub Release
artifacts; remaining public frontend packages come from npm. Lockfiles pin the
resolved sources and integrity metadata. No long-lived registry credential is
committed.

## Deployment boundary

Publishing an image is not general deployment approval. DEC-0012 pre-approves
only the exact qualifying PR call to the image-only reusable workflow described
above. Production and the original `r1shop-dev` environment are not CI or CD
targets, and every non-image `mss-shop-dev` change remains outside this path.

Manual isolated rollout follows DEC-0010, the DEC-0011 DNS-only Admin TLS
extension and the remote acceptance runbook:

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
8. create the same-SHA, same-reconciler-digest Member Levels projection
   verifier Job from
   `deploy/mss-shop-dev/member-levels-projection-verifier-job.yaml`, and require
   its complete success JSON before any runtime stage;
9. dry-run and explicitly create the four DEC-0011 Admin TLS bootstrap objects
   with `stage-admin-tls`, then wait for the exact Issuer and two Certificates
   to report Ready without reading generated Secret contents;
10. stage only the eight Admin runtime objects from
   `deploy/mss-shop-dev/admin-runtime.yaml` using the tenant and mall image
   digests after read-only TLS prerequisite verification;
11. run disposable-Pod system verification and, only after trusted HTTPS
   post-cutover checks, in-app-browser acceptance.

That ordered procedure remains the bootstrap and evidence-bearing release
path. A routine DEC-0012 PR-head image refresh does not repeat or satisfy any
of its infrastructure, import, reconciliation, TLS, system or browser gates.

The importer Job renderer/create-only path, disposable verification runner and
post-receipt application/bootstrap Secret operator are implemented and tested.
For revision `3e64a57dae8bb3dd4d337a423015baae6c352b32`, the receipt-bound
reconciler, Member Levels projection verifier, eight-object Admin runtime and
authoritative v3 disposable system checks have completed. The v1 and v2
disposable-verifier attempts were test-harness failures, not application or
database failures; they are retained only as non-authoritative diagnostics.
Confirmed-login in-app-browser workspace smoke passed for the later
`f202b094...` HTTPS cutover. Detailed route/locale review and explicit business
workflow acceptance remain separate gates. Do not replace any repeat or later
evidence-bearing release with the tag-only dev refresh, ad-hoc template
substitution or `kubectl apply`.

The reconciler and importer images are fixed isolated-development tools, not
production-capable general operators. Storefront API and worker images remain
outside the current delivery matrix until those components own complete
delivery entrypoints and Dockerfiles.
