# DEC-0013: Use the pull request as the development acceptance loop and gate production promotion on a human

Status: accepted
Date: 2026-09-02
Extends: DEC-0012 pull-request-head development image refresh
Supersedes: the push-triggered delivery portion of DEC-0010

## Context

DEC-0012 established a narrow path that publishes one immutable four-image set
for a qualifying pull-request head and updates only the two existing Admin
Deployments in `mss-shop-dev`. The project now uses that pull request as the
shared development environment: local machines are for authoring and focused
checks, while the deployed PR head is the subject of cluster and browser
verification.

A squash merge creates a new commit whose SHA is different from the accepted
pull-request head. Rebuilding from the squash commit would repeat work and could
produce artifacts that were never deployed to development. Any future
production path must therefore promote the already accepted PR-head images and
retain an explicit mapping from the pull request to both revisions and the
image digests.

Production is live and currently runs the existing R1Shop system. This
repository has no reviewed `mss-shop` production namespace, Deployments,
resource-named RBAC or GitHub Environment credential. The desired production
promotion is therefore a policy and future boundary, not authorization to
create an executable workflow or mutate the current production environment.

## Decision

- Development happens on a `codex/**` branch. A branch may be pushed when a
  development slice is complete. Pull requests to `main` run unprivileged
  validation, but only a qualifying same-repository `codex/**` pull request
  publishes all four full-head-SHA images and calls the DEC-0012 development
  image refresh. Direct pushes to `main` and branch pushes outside an open pull
  request do not run that pipeline.
- CI and development deployment are one repeatable pull-request loop. After the
  exact head SHA is deployed, system verification uses disposable Pods in
  `mss-shop-dev` and UI acceptance uses the in-app browser against the two
  trusted HTTPS Admin hosts. A deployment success alone is not acceptance.
- Every new commit pushed to the pull request creates a new head SHA. All prior
  deployment, cluster and browser acceptance applies only to the old SHA and is
  invalid for merge. CI, image publication, development refresh and the
  applicable verification must run again for the new head.
- Before the final CI and acceptance cycle, the PR branch must be synchronized
  with current `main`. A merge/rebase or conflict resolution that performs that
  synchronization creates a new head and therefore requires the complete cycle
  again. This prevents development acceptance of a tree different from the one
  later represented by the squash commit.
- The pull request may be squash-merged only when its latest head is the exact
  deployed and accepted revision. The durable record links the pull-request
  number, accepted head SHA, resulting squash-main SHA, matching source-tree
  identity and the four immutable image digests. Squash merge contributes one
  commit to `main`.
- `main` does not rerun unit tests, contracts, image builds or image publication.
  In particular, the squash-main SHA is not an image tag and must never replace
  the accepted PR-head SHA in a promotion.
- A future production action is an image-only promotion of the already accepted
  PR-head images. It may change only explicitly reviewed container image fields
  on explicitly reviewed `mss-shop` production Deployments. It may not apply
  manifests, invoke migrations directly, change configuration, Secrets, RBAC,
  networking or business data, or target an inferred resource. Image-only
  describes the Kubernetes API mutation, not its complete runtime effect:
  changing the Pod template recreates Pods, and any `migrate` init container in
  the reviewed Deployment may then execute a database migration.
- Every production promotion must use a dedicated GitHub Environment whose
  protection requires a human reviewer and disallows administrator bypass. The
  reviewer account, token and logged-in session are not exposed to Actions, an
  AI or an agent. An AI or agent may prepare the change, observe the waiting
  deployment and report its state, but must never approve, bypass, simulate or
  use a user's browser/session to complete the review.
- Before an executable production CD workflow can be added, a separate reviewed
  change must identify the exact production namespace, Deployments, container
  and init-container names, migration behavior and ordering, forward-
  compatibility and rollback strategy, least-privilege resource-named RBAC,
  Environment and secret boundary. None exists for `mss-shop` today. The live
  `r1shop-prod` workloads, database and Redis remain untouched and must not be
  treated as placeholders.

## Consequences

The latest PR head is the single development acceptance candidate. Iteration is
cheap and explicit: a defect produces another branch commit and another complete
PR-head cycle, while stale acceptance can never authorize a merge.

Main history remains compact, but provenance spans two revisions. A production
receipt must preserve the accepted artifact identity rather than pretending the
squash commit built it.

The production design cannot become operational merely because this decision
exists. Until the exact production topology, minimum credential and human
Environment protection are reviewed and configured outside Git, production
promotion remains planned and fail-closed. This decision grants no production
write permission. Repository settings must also be reviewed to require pull
requests to be current with `main` and to allow only squash merge; documentation
does not claim those external controls are configured today.
