# MCP integration

## Registered server

The repository registers the official `mss-mcp` stdio server in
`.codex/config.toml`. It exposes MSS project operations against the current
checkout and is not a database, Kubernetes or browser connection.

| Property | Value |
| --- | --- |
| Source | `mss-boot-admin` official release |
| Required version | `v1.3.7` |
| Transport | local stdio |
| Root | repository root |
| Credentials | none |
| External data destination | none; the child process is local |

`tools/run-mss-mcp.sh` resolves the repository root and rejects a binary whose
reported version is not 1.3.7. Install the official release bundle as described
in the generated README before starting Codex from this trusted repository.

The startup contract was verified on 2026-09-01 with the official v1.3.7
release: `mss-mcp -root <project>` starts stdio transport; `-version` identifies
the exact build. Do not add unverified flags or silently compile a private MCP
binary as the adopter path.

## Access policy

- MCP is a tool connection, not the storage location for architectural memory.
  Durable knowledge remains in docs, ADRs and Skills.
- Do not configure production DSNs, Kubernetes credentials, auth headers,
  browser sessions or tokens in committed MCP files.
- Any future server addition must document its fixed source/version, transport,
  read/write surface, data destination, environment-variable names, health
  check and removal procedure.
- A future MCP capable of production writes still requires the same explicit
  per-action approval as direct commands.
