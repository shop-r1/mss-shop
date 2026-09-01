# Engineering memory governance

The repository is the durable memory. Store only material that another person
or agent can verify and maintain.

## Where information belongs

- `AGENTS.md`: short, automatically applied safety and ownership constraints.
- `docs/project/status.md`: mutable verified state and next milestone.
- `docs/architecture/`: current design and machine-readable invariants.
- `docs/decisions/`: immutable decisions and their consequences.
- `docs/migration/` and `docs/runbooks/`: staged or verified operations.
- `docs/reviews/`: bounded proposals awaiting an explicit owner decision; a
  recommendation is not an accepted ADR or implementation authorization.
- `docs/migration/legacy-tables.yaml`: machine-readable source of truth for
  the 54 legacy tables, ownership, keys, tenant scope and sensitive fields.
- `docs/migration/legacy-admin-acceptance-matrix.md`: page, operation and
  business-acceptance coverage; an unchecked row is not an implemented claim.
- `docs/acceptance/`: executed, bounded acceptance evidence. Every report must
  state its environment, untested scope and whether it closes matrix rows.
- `docs/evidence/legacy-import/<receipt-sha256>/`: the byte-exact deterministic
  `receipt.json` and later `verification.json` for one executed isolated
  import. The content-addressed directory avoids self-referential Git evidence;
  both artifacts must contain no DSN, credential, certificate material or row
  value.
- `docs/evidence/mss-shop-dev/`: safe, immutable-stage and runtime evidence for
  the isolated namespace. Reconciliation and system-acceptance records bind the
  full source revision, CI image digests, object/Pod identities, restart and
  completion state, exact assertions and SHA-256 of ephemeral Job stdout;
  Secret values, DSNs, certificates and business row values remain forbidden.
- `docs/evidence/original-dev/`: metadata-only fingerprints before and after an
  isolated milestone. Each capture records whether Secret access, database
  connections or writes occurred and must preserve the reviewed safe-field
  digest when the original environment was untouched.
- `.mss/features/`: executable contracts for handwritten cross-table or
  workflow behavior that the AdminModule generator does not support.
- `.agents/skills/`: non-obvious repeatable workflows triggered by a task.
- `.codex/config.toml`: portable, reviewed tool connections only.

Do not commit chat transcripts, hidden reasoning, temporary plans, unrelated
local logs, database snapshots, runtime locks, generated caches, credentials
or machine paths as memory. The reviewed importer receipt/log evidence above
is the narrow exception: it is durable provenance, must be complete and
redacted by construction, and is reviewed before commit.

## Change protocol

- A service-boundary or invariant change adds/supersedes an ADR and updates the
  registry, architecture, invariants and status together.
- A development-topology change adds or supersedes an immutable decision,
  updates the remote runbook, architecture invariants and both status
  documents together, and never rewrites an accepted decision to describe the
  new state. The original `r1shop-dev` environment remains immutable; only
  `mss-shop-dev` may be a development write target.
- An API change starts in the authoritative contract, then updates downstream
  snapshots and generated clients with compatibility evidence.
- A new locale updates catalogs, negotiation, formatting tests and capability
  status in the same change.
- A repeated workflow that contains non-obvious judgment should become or
  update a Skill; simple commands stay in a runbook.
- A legacy module change updates its Feature contract, manifest or data
  contract when applicable, acceptance rows, bilingual catalogs, verification
  evidence and project status in the same commit. Code completion alone does
  not close a business-acceptance row.
- When the project owner defers validation until a later development
  checkpoint, record every new slice as `source-implemented-unverified`, name
  the last actually verified revision, and keep tests, migrations, deployment
  and acceptance independently false. A running local or remote-process
  preview is not durable verification evidence.
- A compatibility mutation is recorded as unavailable until one resource and
  operation has dedicated evidence for legacy validation, relationships/tenant
  scope, hooks, authorization and deletion semantics. Historical exploratory
  writes remain labelled as superseded evidence rather than current capability.
- Documentation/code disagreement is a defect. Resolve it in the same change;
  do not silently choose whichever version is convenient.
- A planned operator or verification command remains labelled as a gate until
  its implementation and tests exist. A manifest or local build is not
  evidence that infrastructure, import, reconciliation or UI acceptance ran.
- Record create-only stage outcomes without collapsing their meaning:
  `created` proves one new object, `exactRetry` proves only a read-only identity
  match, and a failed-closed preflight proves no execution. Exact retry and
  rejected preflight never increase resource counts or close an acceptance
  row. An immutable receipt ConfigMap remains bound to the verifier revision
  that created it; do not delete or rewrite it to force a later verifier run.
- Preserve failed-closed reconciliation and disposable verifier attempts when
  they explain a repaired release gate. Distinguish a rolled-back database
  transaction from Kubernetes Job deletion, and distinguish a test-harness
  defect from an application failure. Only the final authoritative Job may be
  cited as passing evidence.
- Browser acceptance is a separate human-visible layer. Do not infer it from
  HTTP 200 checks, screenshots of an unauthenticated page or a successful API
  Job; record the exact environment, confirmed login, routes/locales reviewed,
  browser errors and the scope left for owner inspection.
- Every trusted stage command that can persist a Kubernetes object defaults to
  a non-persistent API-server dry-run. "Dry-run" means the exact Create or
  Update is submitted with `DryRunAll`; a local render or collision preflight
  alone is insufficient. Persistence requires the command's explicit
  `--create`/`--apply` flag and a fresh complete preflight in that same process.
- Accepted ADRs and prior migration evidence are superseded, not deleted.

## Review cadence

Review status dates, dependency/version facts and legacy coverage counts at
every milestone. Review architecture invariants and external tool access before
a release. A status claim without a command, test, artifact or code reference
is an assumption and must be labelled as such. Local builds are implementation
evidence, not Kubernetes system acceptance or production migration evidence.

## Synchronization gate

Before a milestone is committed, review these artifacts as one change set:

1. `docs/project/status.md` describes the human-readable outcome, open work and
   exact verification level without claiming system acceptance from a build.
2. `docs/project/legacy-rebuild-status.yaml` carries the same inventory,
   implementation state, evidence counts, safety boundaries and source paths
   in machine-readable form.
   Local browser evidence is registered separately from Kubernetes system or
   production evidence and cannot close a workflow scenario by implication.
3. A changed boundary is reflected in the applicable ADR, architecture
   invariant, data manifest, acceptance row, Feature contract and Skill. An
   unchanged boundary should not produce documentation churn.
4. Executed checks are recorded with stable commands and results; local
   absolute paths, credentials, transient logs and copied terminal output are
   not evidence artifacts.
   An isolated import additionally persists its safe full output, deterministic
   receipt, receipt-marker match and independent zero counts for `orders` and
   `order_goods` before reconciliation evidence can be recorded. Runtime
   acceptance persists exact object/workload identities and hashes of
   short-lived verifier logs before their TTL can remove the Pods.
5. The topology record proves that the 24 infrastructure objects are
   create-only, with NetworkPolicies before two inert storage binders and a
   stable, cluster-wide node/local-path exclusivity gate before StatefulSets;
   the six foundation Secrets are immutable and namespace-local; old Redis is
   not shared; and CI produces four images without deploying them.
6. `tools/check-project-memory.sh` passes. It checks required project memory,
   runs the executable contracts, validates all repository Skills with the
   exact MSS tool and rejects whitespace errors. The contracts prove that the
   documented source paths, 54-table four/50 ownership split, 51-resource
   one/50 compatibility allocation, acceptance counts, safety flags and
   implemented frontend projections have not drifted.

If implementation and memory cannot be synchronized in the same commit, the
implementation remains incomplete and its acceptance status stays open.
