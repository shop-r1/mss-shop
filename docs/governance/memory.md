# Engineering memory governance

The repository is the durable memory. Store only material that another person
or agent can verify and maintain.

## Where information belongs

- `AGENTS.md`: short, automatically applied safety and ownership constraints.
- `docs/project/status.md`: mutable verified state and next milestone.
- `docs/architecture/`: current design and machine-readable invariants.
- `docs/decisions/`: immutable decisions and their consequences.
- `docs/migration/` and `docs/runbooks/`: staged or verified operations.
- `docs/migration/legacy-tables.yaml`: machine-readable source of truth for
  the 54 legacy tables, ownership, keys, tenant scope and sensitive fields.
- `docs/migration/legacy-admin-acceptance-matrix.md`: page, operation and
  business-acceptance coverage; an unchecked row is not an implemented claim.
- `docs/acceptance/`: executed, bounded acceptance evidence. Every report must
  state its environment, untested scope and whether it closes matrix rows.
- `.mss/features/`: executable contracts for handwritten cross-table or
  workflow behavior that the AdminModule generator does not support.
- `.agents/skills/`: non-obvious repeatable workflows triggered by a task.
- `.codex/config.toml`: portable, reviewed tool connections only.

Do not commit chat transcripts, hidden reasoning, temporary plans, local logs,
database snapshots, runtime locks, generated caches, credentials or machine
paths as memory.

## Change protocol

- A service-boundary or invariant change adds/supersedes an ADR and updates the
  registry, architecture, invariants and status together.
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
- A compatibility mutation is recorded as unavailable until one resource and
  operation has dedicated evidence for legacy validation, relationships/tenant
  scope, hooks, authorization and deletion semantics. Historical exploratory
  writes remain labelled as superseded evidence rather than current capability.
- Documentation/code disagreement is a defect. Resolve it in the same change;
  do not silently choose whichever version is convenient.
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
5. `tools/check-project-memory.sh` passes. It checks required project memory,
   runs the executable contracts, validates all repository Skills with the
   exact MSS tool and rejects whitespace errors. The contracts prove that the
   documented source paths, 54-table ownership split, acceptance counts,
   safety flags and implemented frontend projections have not drifted.

If implementation and memory cannot be synchronized in the same commit, the
implementation remains incomplete and its acceptance status stays open.
