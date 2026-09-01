# Legacy snapshot importer

This command is a single-use, fail-closed bridge from the original R1Shop
development TimescaleDB to the isolated `mss_shop_dev` PostgreSQL database.
It never mutates the source. It embeds the reviewed 51-table/731-column catalog
allow-list, copies with PostgreSQL binary `COPY`, and creates `orders` and
`order_goods` as empty structures only. Legacy `roles`, `tenants`, and `users` are expected in
the source inventory but are not read or copied.

The source endpoint is fixed to
`timescaledb-r1shop-dev.database.svc:5432/r1shop_dev`; the target endpoint is fixed to
`mss-shop-postgres.mss-shop-dev.svc:5432/mss_shop_dev`. The immutable old
TimescaleDB has TLS disabled, so the source has one explicit legacy exception:
its DSN must say `sslmode=disable`, it has no fallback endpoint, and the import
Pod must be constrained by the exact reviewed NetworkPolicy. Startup disables
event triggers and defaults every source transaction to read-only; the import
also uses a repeatable-read `READ ONLY` transaction, access-share locks, and
disables index/bitmap/index-only and parallel scans. Any other source host,
database, or SSL mode is rejected. The target always uses CA and hostname
verification with no plaintext fallback. DSNs, passwords, certificates, and
row values are never emitted in the receipt.

Required environment variables:

- `MSS_LEGACY_IMPORT_CONFIRM=import-read-only-snapshot-without-order-data`
- `MSS_LEGACY_SOURCE_DSN`
- `MSS_LEGACY_TARGET_DSN`
- `MSS_LEGACY_TARGET_TLS_CA_FILE`
- `MSS_LEGACY_TARGET_TLS_SERVER_NAME`

The optional target mutual-TLS pair is
`MSS_LEGACY_TARGET_TLS_CERT_FILE`/`MSS_LEGACY_TARGET_TLS_KEY_FILE`. Source TLS
variables are rejected because silently attempting TLS would obscure this
bounded legacy exception.

The target must be a fresh PostgreSQL 17.6 database owned by the importing
role, contain only `plpgsql`, expose no user objects or public privileges, and
carry the exact database comment
`r1shop.io/operator-binding=mss-shop-dev:PostgreSQL:mss_shop_dev;state=isolated-empty`.
A successful
transaction replaces that comment with a receipt-bound marker and prints a
JSON receipt containing the compiled 51-table schema fingerprint plus each
table's source/target row counts and streaming SHA-256 values. The two
structure-only tables report their source evidence while proving target count
zero. No row value is emitted.

Receipt durability is part of the one-time operator contract. Standard output
must be attached to a durable, access-controlled sink before this command is
started, and reconciliation must not begin until the complete JSON receipt has
been persisted. The committed database marker is the authoritative success
signal and its SHA-256 suffix is the recovery key for the stored receipt. If
receipt output fails after the marker is committed, do not rerun the importer;
recover the receipt from the durable sink and verify that its `sha256` exactly
matches the marker before continuing.
