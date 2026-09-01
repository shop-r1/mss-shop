# Remote development and isolated mss-shop-dev acceptance

This runbook stages only the isolated `mss-shop-dev` environment. The original
`r1shop-dev` application, database, Redis, Secrets, storage and networking are
immutable read-only sources/reference state. Nothing in this runbook
authorizes a write to them. It never authorizes a write to `r1shop-prod`, the
production database or production Redis.

The end-to-end run is currently **blocked at explicit implementation gates**
identified below. Do not skip a gate or replace it with an ad-hoc command. At
the time of this update, no isolated deployment, data import, Kubernetes system
acceptance or isolated browser acceptance has been executed; business
acceptance remains 0/31.

## Fixed boundaries

| Item | Fixed value |
| --- | --- |
| Remote checkout | `/root/workspace/mss-shop` on `167.17.68.242` |
| Branch | `codex/r1shop-platform` |
| Write namespace | `mss-shop-dev` only |
| Target PostgreSQL | PostgreSQL 17.6, `mss-shop-postgres.mss-shop-dev.svc:5432/mss_shop_dev` |
| Target Redis | Redis 8.6.3, `mss-shop-redis.mss-shop-dev.svc:6379` |
| Legacy read-only source | `timescaledb-r1shop-dev.database.svc:5432/r1shop_dev` only |
| Permitted old Secret reads | exact database credential Secret and `r1shop-dev/ghcr-r1shop-token`, GET only |
| Provisional tenant Admin | `http://tenant-admin.167.17.68.242.nip.io` |
| Provisional mall Admin | `http://mall-admin.167.17.68.242.nip.io` |

The legacy source has SSL disabled. Only the compiled legacy importer may use
the reviewed `sslmode=disable` exception, without fallback endpoints, from a
Pod selected by the exact source-egress NetworkPolicy. Its startup packet
disables event triggers and all source work is repeatable-read and read-only.
The isolated PostgreSQL and Redis require their generated TLS identities. The
old Redis is not read or shared.

Use Go 1.26.6, Node 24.19.0, pnpm 10.34.5 and the official MSS 1.3.7 tools
recorded by the repository. Keep resource-heavy builds in GitHub Actions; the
remote host may run bounded source tests with `GOMAXPROCS=2` and `-p=2` while
node health remains stable.

## Mandatory write declaration

Before **every** Kubernetes or database write, write this information into the
active task/evidence record:

1. namespace (`mss-shop-dev` only);
2. every resource kind/name that may be created or changed;
3. image full SHA and digest, when a workload is involved;
4. expected effect and an explicit statement that the original development
   environment and production are unchanged.

Use an explicit absolute kubeconfig path on every cluster command, for example
`/absolute/path/to/devops.kubeconfig`. Never rely on the ambient context.

## Ordered procedure

### 1. Clean SHA and four CI receipts

In `/root/workspace/mss-shop`, inspect the branch, `HEAD` and working tree.
Continue only from a clean checkout whose complete lowercase, nonzero 40-byte
Git SHA is the revision under review. Do not discard remote work to make it
clean.

Confirm the workflow for that exact SHA completed unit/contract validation and
published four immutable image receipts:

- `mss-shop-tenant-platform`;
- `mss-shop-mall-platform`;
- `mss-shop-reconciler`;
- `mss-shop-legacy-importer`.

Each receipt must bind repository, full revision, `linux/amd64` and a nonzero
`sha256:` digest. A historical run that contains only the two Admin images is
not sufficient. CI publication is not deployment approval.

### 2. Read-only fingerprint of the original environment

From the exact clean checkout, run the fixed metadata-only helper. Redirect its
single JSON document to a temporary file outside the Git checkout; shell
redirection inside the repository would create an untracked file before the
helper can prove the checkout is clean.

```shell
fingerprint_output=/tmp/mss-shop-original-dev-before.json
go run ./services/reconciler/cmd/capture-original-dev-fingerprint \
  --environment r1shop-dev-read-only \
  --kubeconfig /absolute/path/to/devops.kubeconfig \
  --revision 0123456789abcdef0123456789abcdef01234567 \
  > "$fingerprint_output"
```

Replace the example revision with the reviewed operator HEAD. Only after the
command exits successfully, copy the complete file to a new, clearly named
path under `docs/evidence/original-dev/`; do not overwrite or reinterpret the
existing `2026-09-01-before.json` baseline. The new Kubernetes-only format may
coexist with it.

The helper performs only fixed Kubernetes GET/LIST operations. It never reads
a Secret, opens a database connection, execs into a Pod, or writes a resource.
It captures the exact old `shop` Deployment, Service, Ingress and unique ready
Pod, plus the old TimescaleDB and Redis StatefulSet, Service, unique ready Pod,
PVC and bound PV metadata. Its strict, non-secret output includes UID,
resourceVersion, generation, readiness, restart count, image/imageID, Ingress
host, PVC/PV claim binding and a canonical SHA-256 over the selected safe
fields.

The source database identity, TLS-disabled boundary, exact 54-table inventory
and catalog fingerprint remain the compiled importer's independent read-only
preflight responsibility. This host helper must not recreate that check with
Pod exec or a host database connection.

**Gate:** the command must succeed from the exact clean revision and its JSON
must be durably captured before staging. Any missing, ambiguous, multiple-Pod,
non-ready or non-bound resource stops the run; do not improvise a broader
read-capable or write-capable probe.

### 3. Create the isolated infrastructure boundary

Declare a write to the exact 24 resources compiled into
`deploy/mss-shop-dev/infrastructure.yaml`: Namespace, ResourceQuota,
LimitRange, two ConfigMaps, two PVCs, two Services, nine NetworkPolicies, two non-mounting scheduling-only binder Pods, two StatefulSets and two
PodDisruptionBudgets. Expected impact: new isolated storage/network/datastore
objects only; old development and production remain unchanged.

Then run the create-only operator from the clean checkout:

```shell
go run ./services/reconciler/cmd/stage-infrastructure \
  --environment mss-shop-dev \
  --kubeconfig /absolute/path/to/devops.kubeconfig \
  --revision 0123456789abcdef0123456789abcdef01234567
```

Replace the example revision with the reviewed full SHA. The operator performs
full collision and server-side dry-run checks before creating anything. It
must create every NetworkPolicy before either binder Pod or StatefulSet and
must never apply, patch, adopt, delete or roll back an object. Storage staging
is deliberately two-phase: the first phase creates and verifies the two inert,
non-mounting binder Pods and stops while a PVC is Pending; a later clean retry
admits either StatefulSet only after both PVCs and two stable cluster-wide PV
snapshots prove the reviewed node/path ownership. Every object observed on a
retry must match the complete create-only contract.

### 4. Create six foundation Secrets

At this point datastore Pods may be pending because their Secrets do not yet
exist. Declare creation of exactly these six immutable Secrets in
`mss-shop-dev`: `mss-shop-postgres-auth`, `mss-shop-postgres-tls`,
`mss-shop-redis-auth`, `mss-shop-redis-tls`,
`mss-shop-legacy-source-auth` and `mss-shop-ghcr-pull`. Expected impact: new
namespace-local credentials/TLS only.

```shell
go run ./services/reconciler/cmd/stage-secrets \
  --environment mss-shop-dev \
  --kubeconfig /absolute/path/to/devops.kubeconfig \
  --revision 0123456789abcdef0123456789abcdef01234567
```

The operator may GET only the exact old database credential and GHCR pull
Secret, preflights all six target names before its first create, preserves an
exact immutable retry and prints metadata rather than values. Any collision or
partial mismatch stops the run.

### 5. Prove datastore readiness

Wait only on the two isolated StatefulSets:

```shell
kubectl --kubeconfig /absolute/path/to/devops.kubeconfig \
  --namespace mss-shop-dev rollout status statefulset/mss-shop-postgres
kubectl --kubeconfig /absolute/path/to/devops.kubeconfig \
  --namespace mss-shop-dev rollout status statefulset/mss-shop-redis
```

Declare creation of only `Job/mss-shop-readiness-<full-sha>` in
`mss-shop-dev`, bound to the selected full SHA and matching digest of the
fourth delivery image, `mss-shop-legacy-importer`. Expected impact: one
disposable read-only readiness transaction against only the new PostgreSQL and
Redis; the legacy source, old development environment and production are
unchanged.

From the exact clean checkout, first render and preflight the fixed readiness
Job without persistence:

```shell
go run ./services/reconciler/cmd/stage-jobs \
  --mode readiness \
  --environment mss-shop-dev \
  --kubeconfig /absolute/path/to/devops.kubeconfig \
  --revision 0123456789abcdef0123456789abcdef01234567 \
  --image-digest sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
```

Replace both example image bindings with the exact values from one CI receipt.
After recording the dry-run and write declaration, repeat the identical command
with `--create`. The fixed `isolated-readiness` role can reach only the new
PostgreSQL and Redis. The Job receives only their authentication Secrets and
CA certificates; it receives no legacy-source Secret, endpoint or Kubernetes
API token.

Capture the complete, untruncated stdout JSON, Pod UID and image ID before the
Job TTL expires. A successful document has version
`mss-shop-disposable-readiness/v1`, `ready=true`, exact PostgreSQL `17.6` and
Redis `8.6.3` identities, `namespace=mss-shop-dev`, and the Pod name/UID, full
image revision and digest. It also proves the `mss_shop_dev` isolated-empty
marker and strict hostname/CA verification. A readiness failure JSON is not
acceptance evidence.

**Gate:** only that complete success document permits import. If it is missing,
truncated, reports failure or cannot be bound to the declared Pod and digest,
stop before import.

### 6. Create the one-time importer Job and persist evidence

The source template is `deploy/mss-shop-dev/legacy-import-job.yaml`. It must be
rendered with the reviewed full SHA and matching importer digest from the CI
receipt. The resulting image reference must include both tag and digest. The
Job is create-only, has no ServiceAccount token, and uses the
`legacy-import` network role.

Declare creation of only
`Job/mss-shop-legacy-import-<full-sha>` in `mss-shop-dev`. Expected impact: one
read-only snapshot transaction against the fixed legacy source and one
all-or-nothing import transaction into the isolated target. `orders` and
`order_goods` must remain target-empty.

From the exact clean checkout, first run the fixed renderer and complete
preflight without persistence:

```shell
go run ./services/reconciler/cmd/stage-jobs \
  --mode importer \
  --environment mss-shop-dev \
  --kubeconfig /absolute/path/to/devops.kubeconfig \
  --revision 0123456789abcdef0123456789abcdef01234567 \
  --image-digest sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
```

Replace both example bindings with the exact importer values from the same CI
receipt. After recording the successful dry-run and the write declaration,
repeat the identical command with `--create`. `stage-jobs` reads only the fixed
repository template, rejects arbitrary manifests and performs no persistent
operation other than `Create Job`. An exact already-existing server-defaulted
Job is a read-only retry; any other collision stops the stage.

Call the importer image revision **revision A**. When its Job succeeds,
immediately capture the complete, untruncated Pod stdout, Pod UID, image ID and
Job condition before the Job TTL expires. Validate the sole JSON receipt,
derive its canonical lowercase receipt SHA-256, and persist its exact bytes at
this content-addressed path:

```text
docs/evidence/legacy-import/<receipt-sha256>/receipt.json
```

The directory name is the receipt SHA-256, never revision A. Evidence must not
contain DSNs, credentials, certificate material or row values.

Verify the receipt's compiled 51-table schema fingerprint and each table's
source/target count and streaming hash. For `orders` and `order_goods`, retain
the source evidence while requiring target count zero. Independently verify
that the database comment is exactly
`mss-shop-isolated-dev:legacy-import:v1:<receipt-sha256>`. The importer marker
transaction commits before the importer writes its stdout receipt. Therefore a
successful database import followed by missing or truncated log capture leaves
a committed marker but no admissible receipt; a retry is correctly blocked by
that marker. No exact repository-owned recovery path for this boundary is
implemented yet. Complete stdout capture before TTL is consequently a hard
deployment gate: on loss, stop, preserve the Job and database state, and seek a
separately reviewed recovery. Do not rerun the importer.

Commit the validated receipt at its SHA-addressed path to create clean
**revision B**. The receipt must be tracked in revision B before the verifier
image is built or staged.

### 7. Verify receipt and empty order tables in-cluster

Build the normal four-image CI delivery for revision B and select the
revision-B digest of `ghcr.io/shop-r1/mss-shop-legacy-importer`; the verifier is
another binary in that existing fourth image, not a fifth image. From the exact
clean revision-B checkout, declare creation of immutable
`ConfigMap/mss-shop-legacy-import-receipt` and only
`Job/mss-shop-legacy-verify-<revision-B-full-sha>` in `mss-shop-dev`. Expected
impact: delivery of non-secret, byte-exact committed receipt evidence and one
read-only verification snapshot against only the new PostgreSQL. The legacy
source, Redis, old development environment and production are unchanged.

First run the fixed renderer and complete every Namespace, global collision,
tracked-evidence, StatefulSet/PVC, Secret and server-side dry-run check without
persistence:

```shell
go run ./services/reconciler/cmd/stage-jobs \
  --mode verifier \
  --environment mss-shop-dev \
  --kubeconfig /absolute/path/to/devops.kubeconfig \
  --revision 89abcdef0123456789abcdef0123456789abcdef \
  --image-digest sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb \
  --import-receipt-sha256 cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc \
  --receipt-file /root/workspace/mss-shop/docs/evidence/legacy-import/cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc/receipt.json
```

Replace the revision, digest and receipt SHA with the exact revision-B image
receipt and tracked import receipt. After recording the successful dry-run and
write declaration, repeat the identical command with `--create`. The operator
may persist only the byte-exact immutable
`ConfigMap/mss-shop-legacy-import-receipt` and verifier Job. An exact
ConfigMap/Job retry is read-only, while a missing ConfigMap behind an existing
Job or any mismatch fails closed. Immediately before Job creation it rechecks
the exact Namespace and ConfigMap bytes.

The fixed `legacy-verifier` role can reach only the new PostgreSQL. It receives
the new PostgreSQL authentication Secret and CA plus the complete receipt from
the immutable `ConfigMap/mss-shop-legacy-import-receipt`. It receives no
legacy-source Secret, Redis Secret, source endpoint or Kubernetes API token. In
one repeatable-read, read-only target snapshot it must independently assert:

- stored marker suffix equals the verified receipt SHA-256;
- the target inventory and schema fingerprint match the receipt;
- `SELECT count(*) FROM public.orders` returns zero;
- `SELECT count(*) FROM public.order_goods` returns zero.

Capture the complete, untruncated stdout JSON, Pod UID, image ID and Job
condition before the Job TTL expires. Do not remove the Job or Pod until the
evidence has been captured and validated.

The verifier success document has version
`mss-shop-disposable-verification/v1` and exactly these fields:
`version`, `targetDatabase`, `databaseMarker`, `receiptSHA256`,
`manifestSHA256`, `schemaSHA256`, `tableCount`, `ordersRows`,
`orderGoodsRows`, `namespace`, `podName`, `podUID`, `revision`,
`imageRepository`, `imageDigest` and `imageReference`. It binds
`mss_shop_dev`, the exact marker/receipt SHA, all 51 tables and reviewed target
schema, zero rows in both order tables, `mss-shop-dev`, the Pod identity and
`ghcr.io/shop-r1/mss-shop-legacy-importer:<revision-B>@sha256:<digest>`.
A different failure version and field set are emitted.
The resulting failure JSON is not acceptance evidence.

Persist the complete success document beside the already committed receipt:

```text
docs/evidence/legacy-import/<receipt-sha256>/receipt.json
docs/evidence/legacy-import/<receipt-sha256>/verification.json
```

Commit `verification.json` beside the receipt to create clean **revision C**.
This completes the deliberate A-to-B-to-C chain: revision A imports and emits
the receipt; revision B tracks that receipt and supplies the digest-bound
verifier image; revision C tracks both evidence documents and is the clean
checkout used for the later reconciliation-secret/reconciler stages. The
receipt digest is the directory key because it exists before either evidence
commit. A Git revision must never be used as its own evidence directory key:
adding the file changes the revision and creates an impossible self-reference.
Keep B reachable as an ancestor of C: the operator proves that B contains the
unchanged receipt blob and does not yet contain `verification.json`, so a
depth-one or rewritten checkout is insufficient evidence.

**Gate:** the exact disposable verifier must be repository-owned and tested.
If it or either committed evidence file is absent, stop. A manual host-side
query is not equivalent evidence, and neither evidence file may contain a DSN,
credential, certificate, token or row value.

### 8. Stage receipt-bound application/bootstrap Secrets

After the preceding evidence passes, run the separately reviewed
`stage-reconciliation-secrets` operator from the clean evidence-bearing
checkout. Use absolute paths to the two exact tracked files:

```shell
go run ./services/reconciler/cmd/stage-reconciliation-secrets \
  --environment mss-shop-dev \
  --kubeconfig /absolute/path/to/devops.kubeconfig \
  --revision 89abcdef0123456789abcdef0123456789abcdef \
  --import-receipt-sha256 cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc \
  --receipt-evidence /root/workspace/mss-shop/docs/evidence/legacy-import/cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc/receipt.json \
  --verification-evidence /root/workspace/mss-shop/docs/evidence/legacy-import/cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc/verification.json
```

Replace the operator revision and receipt SHA with the exact reviewed values;
the verification document independently binds its actual verifier revision and
image digest. Before initializing a Kubernetes client, the command requires
a clean operator checkout, exact fixed paths, regular non-symlink files, Git
tracking in `HEAD`, byte-for-byte agreement with the committed blobs, strict
single-document JSON, the canonical importer receipt digest and the complete
receipt/verifier bindings above. It then reads only the isolated foundation
credentials, validates every Secret collision before writing, and creates only
the two Admin application Secrets plus transient
`mss-shop-reconciler-bootstrap` in `mss-shop-dev`.

This host-side operator does **not** connect to PostgreSQL and its committed
evidence checks are not a replacement for a live database boundary. The later
in-cluster reconciler independently queries the exact database marker and
requires `public.orders` and `public.order_goods` to remain empty before any
DDL.

**Gate:** real importer and disposable-verifier evidence has not yet been
produced and committed. Until both exact files exist and pass this command,
reconciler and Admin runtime deployment remain forbidden. Do not fabricate
placeholder evidence.

### 9. Create the receipt-bound reconciler Job

Render `deploy/mss-shop-dev/reconciler-job.yaml` with the same full SHA,
reconciler digest and verified receipt SHA. Declare and create only
`Job/mss-shop-reconciler-<full-sha>` in `mss-shop-dev`. Expected impact:
receipt-bound reconciliation of the fixed isolated roles, compatibility
owners, schemas, snapshots, views and grants; no Kubernetes API access and no
legacy-source write.

From the same exact clean checkout, first run the fixed renderer and complete
preflight without persistence:

```shell
go run ./services/reconciler/cmd/stage-jobs \
  --mode reconciler \
  --environment mss-shop-dev \
  --kubeconfig /absolute/path/to/devops.kubeconfig \
  --revision 0123456789abcdef0123456789abcdef01234567 \
  --image-digest sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb \
  --import-receipt-sha256 cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
```

Replace all three example bindings with the reconciler CI receipt and the
independently verified import receipt. The command requires the immutable
bootstrap Secret to contain that exact receipt SHA. After recording the
successful dry-run and the write declaration, repeat the identical command
with `--create`. It reads only the fixed repository template, creates only the
receipt-bound Job, and treats only a fully equivalent server-defaulted Job as
an exact retry. After success, persist complete safe logs, ownership/ACL
inventory, counts and hashes. Delete the transient bootstrap Secret only
through its separately reviewed lifecycle operation; never delete either
application Secret ad hoc.

### 10. Stage the Admin runtime in mss-shop-dev only

Once every previous gate passes, run the trusted operator without `--apply`
for collision and server-side dry-run checks. Supply the two Admin digests from
the same four-image CI run:

```shell
go run ./services/reconciler/cmd/stage-runtime \
  --environment mss-shop-dev \
  --kubeconfig /absolute/path/to/devops.kubeconfig \
  --revision 0123456789abcdef0123456789abcdef01234567 \
  --tenant-image-digest sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  --mall-image-digest sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
```

After recording the dry-run result, declare the exact eight runtime objects
and expected one-replica impact, then repeat the same command with `--apply`.
Replace the revision and example digests with the verified receipt values.
The operator is compiled for `mss-shop-dev`, rejects tag-only images, checks
Ingress collisions cluster-wide and never forces a field conflict.

### 11. Cluster system verification

Run every system-verification case in disposable, one-time Pods in
`mss-shop-dev`. At minimum record:

- desired/available replicas, zero new restarts and digest-matching image IDs;
- PostgreSQL/Redis TLS and least-privilege negative cases;
- `/healthz` and `/readyz` for both Admin hosts;
- fixed tenant/schema binding and cross-schema/cross-tenant denials;
- receipt/marker and both zero-order-table assertions;
- menu, permission, locale, empty/error and read-only compatibility contracts.

Capture Pod logs before removing only the named test Pods. A health check or a
generic compatibility list does not close any of the 31 business scenarios.

### 12. In-app-browser acceptance and owner handoff

Use the in-app browser against the two isolated provisional URLs. Verify login,
authorized navigation, `zh-CN` and `en-US`, list/detail, empty/error states,
read-only mutation absence and the MSS Admin visual language. Save screenshots
and console/network findings under `docs/acceptance/`, state the exact image
digests and untested scope, and leave both URLs running for manual owner review.

Finally repeat the original-environment read-only fingerprint from step 2. Its
UIDs, generations, images, readiness, restarts and named object fingerprints
must be unchanged. Any difference blocks acceptance and is investigated
without changing the old environment.

## Failure and rollback

- Stop at the first failed gate and preserve safe evidence. Do not continue
  because the old application is still available.
- Never repair a failed isolated stage by changing the old development or
  production environment.
- Infrastructure and foundation credentials are create-only. Their deletion,
  replacement or rotation requires a separate reviewed lifecycle decision;
  this runbook grants none.
- Runtime rollback may select a previously verified digest through the same
  `stage-runtime` preflight/apply path and changes only the two new Admin
  Deployments. Never roll back database/import migrations destructively.
- The marker transaction commits before the importer writes its stdout receipt.
  There is currently no exact repository-owned recovery if that complete output
  is lost. Preserve the Job/database state, stop for separate review, and do not
  rerun the importer.
