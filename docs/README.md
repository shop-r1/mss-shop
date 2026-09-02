# R1Shop engineering memory

This directory is the versioned source of truth for the R1Shop re-platforming
work. It records decisions and verified procedures, not chat transcripts or
local runtime state.

## Read in this order

1. [Current status](project/status.md) and
   [machine-readable rebuild status](project/legacy-rebuild-status.yaml)
2. [Remaining work for complete legacy restoration](project/legacy-restoration-gap.md)
3. [Overall solution](architecture/overall-solution.md)
4. [Architecture invariants](architecture/invariants.yaml)
5. [Internationalization](architecture/internationalization.md)
6. [Migration roadmap](migration/roadmap.md)
7. [Legacy data contract](migration/legacy-data-contract.md)
8. [Legacy table manifest](migration/legacy-tables.yaml)
9. [Legacy Admin acceptance matrix](migration/legacy-admin-acceptance-matrix.md)
10. [Mall local browser compatibility acceptance](acceptance/mall-local-browser-acceptance.md)
11. [CI and delivery image runbook](runbooks/ci-images.md)
12. [Remote development and isolated mss-shop-dev acceptance](runbooks/remote-development-and-dev-acceptance.md)
13. [Catalog and logistics redesign review](reviews/legacy-catalog-logistics-redesign.md)
14. [Decision records](decisions/README.md)
15. [Memory governance](governance/memory.md)
16. [MCP integration](tooling/mcp.md)
17. [MSS 1.3.7 generation qualification notes](tooling/mss-1.3.7-generation-notes.md)

After changing an invariant, migration contract, acceptance claim, Skill or
tool connection, run `tools/check-project-memory.sh` before committing.

The original `r1shop-dev` environment is immutable. Remote development writes
only to the isolated `mss-shop-dev` namespace with dedicated datastores,
storage, TLS and credentials; see DEC-0010 before any cluster operation and
DEC-0011 for the DNS-only Admin TLS/host-cutover gate. DEC-0012 defines the
separate image-only development CD boundary for qualifying pull requests.
DEC-0013 makes the latest deployed PR head the acceptance candidate, requires
acceptance to repeat after every push, records its mapping to the squash-main
commit and keeps production promotion planned behind a human GitHub Environment
review. No executable `mss-shop` production target exists yet.

The root application remains an MSS 1.3.7 Thin Host proof of concept. The two
delivery Hosts live under `apps/tenant-platform` and `apps/mall-platform`, with
backend and Admin Web together in each Host directory. Do not move generated
files by hand; regenerate managed files and change only business-owned
extension paths.
