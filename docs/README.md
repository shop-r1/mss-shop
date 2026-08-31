# R1Shop engineering memory

This directory is the versioned source of truth for the R1Shop re-platforming
work. It records decisions and verified procedures, not chat transcripts or
local runtime state.

## Read in this order

1. [Current status](project/status.md) and
   [machine-readable rebuild status](project/legacy-rebuild-status.yaml)
2. [Overall solution](architecture/overall-solution.md)
3. [Architecture invariants](architecture/invariants.yaml)
4. [Internationalization](architecture/internationalization.md)
5. [Migration roadmap](migration/roadmap.md)
6. [Legacy data contract](migration/legacy-data-contract.md)
7. [Legacy table manifest](migration/legacy-tables.yaml)
8. [Legacy Admin acceptance matrix](migration/legacy-admin-acceptance-matrix.md)
9. [Mall local browser compatibility acceptance](acceptance/mall-local-browser-acceptance.md)
10. [Decision records](decisions/README.md)
11. [Memory governance](governance/memory.md)
12. [MCP integration](tooling/mcp.md)
13. [MSS 1.3.7 generation qualification notes](tooling/mss-1.3.7-generation-notes.md)

After changing an invariant, migration contract, acceptance claim, Skill or
tool connection, run `tools/check-project-memory.sh` before committing.

The root application remains an MSS 1.3.7 Thin Host proof of concept. The two
delivery Hosts live under `apps/tenant-platform` and `apps/mall-platform`, with
backend and Admin Web together in each Host directory. Do not move generated
files by hand; regenerate managed files and change only business-owned
extension paths.
