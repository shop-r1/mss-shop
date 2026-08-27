# Engineering memory governance

The repository is the durable memory. Store only material that another person
or agent can verify and maintain.

## Where information belongs

- `AGENTS.md`: short, automatically applied safety and ownership constraints.
- `docs/project/status.md`: mutable verified state and next milestone.
- `docs/architecture/`: current design and machine-readable invariants.
- `docs/decisions/`: immutable decisions and their consequences.
- `docs/migration/` and `docs/runbooks/`: staged or verified operations.
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
- Documentation/code disagreement is a defect. Resolve it in the same change;
  do not silently choose whichever version is convenient.
- Accepted ADRs and prior migration evidence are superseded, not deleted.

## Review cadence

Review status dates and dependency/version facts at every milestone. Review
architecture invariants and external tool access before a release. A status
claim without a command, test, artifact or code reference is an assumption and
must be labelled as such.
