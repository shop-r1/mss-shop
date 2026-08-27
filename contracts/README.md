# Cross-repository contracts

This directory is authoritative for data exchanged with storefront clients.
It currently contains the locale registry. The `/app/v1` OpenAPI document and
tenant bootstrap schema will be added before storefront implementation, not
guessed from legacy handlers.

Downstream repositories commit a versioned snapshot and checksum; they do not
import this repository's source tree at build time.
