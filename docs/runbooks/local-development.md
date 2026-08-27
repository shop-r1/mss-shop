# Local development runbook

Risk: local checkout only. This procedure does not authorize Kubernetes,
database or deployment writes.

## Prerequisites

- Go and Node versions declared by `.mss/project.yaml`.
- Official `mss` and `mss-mcp` release binaries at `v1.3.6`.
- pnpm version locked by `web/package.json`.

## Start the phase-zero Thin Host

```shell
mss --version
mss-mcp -version
mss doctor --strict
mss setup
mss dev --detach
```

On the first migration, supply the initial admin password through the hidden
interactive prompt. In automation, inject `MSS_ADMIN_INITIAL_PASSWORD` only for
the migration process. Never place it in a command argument, shell history,
report or repository file.

Expected endpoints:

- Backend health: `http://127.0.0.1:8080/healthz`
- Admin Web: `http://127.0.0.1:8001/`

Inspect with `mss dev status` and `mss dev logs <service>`. Stop with
`mss dev stop`. SQLite files, logs, run locks, dependencies and builds are local
artifacts and remain ignored.

## Verify

```shell
mss verify --all
```

Successful verification is evidence for the proof only. It does not prove the
future tenant isolation or legacy data migration.
