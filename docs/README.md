# R1Shop engineering memory

This directory is the versioned source of truth for the R1Shop re-platforming
work. It records decisions and verified procedures, not chat transcripts or
local runtime state.

## Read in this order

1. [Current status](project/status.md)
2. [Overall solution](architecture/overall-solution.md)
3. [Architecture invariants](architecture/invariants.yaml)
4. [Internationalization](architecture/internationalization.md)
5. [Migration roadmap](migration/roadmap.md)
6. [Decision records](decisions/README.md)
7. [Memory governance](governance/memory.md)
8. [MCP integration](tooling/mcp.md)

The current root application is an MSS 1.3.7 Thin Host proof of concept.
It remains runnable while the two production Thin Hosts are generated into the
target layout described by the overall solution. Do not move generated files
by hand; regenerate and migrate only business-owned files.
