# DEC-0005: Resolve public storefronts from exact server-owned bindings

Status: accepted
Date: 2026-08-28

## Context

The legacy storefront middleware could replace Host with values from unrelated
headers, remove business prefixes and infer a copied mall from member input.
Its anonymous configuration endpoint could also return an untyped object that
mixed public AppID data with AppSecret. Reusing that middleware would make
tenant selection ambiguous and could expose secrets.

## Decision

- H5 bootstrap uses only the normalized HTTP request Host and an exact
  server-owned allow-list binding. Host normalization lowercases, removes a
  valid port and final dot, and converts IDNs to ASCII; it never removes a
  business prefix.
- WeChat Mini Program bootstrap may submit its public AppID as a selector for
  anonymous public configuration. AppID is not proof of identity. Customer
  login must later verify a one-time WeChat code using the matching server-side
  secret before issuing a tenant-bound session.
- `Accept`, Referer, Origin, `member_id`, tenant IDs, schema names and database
  selectors never choose a tenant.
- Bootstrap is assembled from an explicit public DTO. Unknown configuration
  fields fail startup, and arbitrary JSON or secret-bearing fields cannot pass
  through.

## Consequences

Unknown and duplicate bindings fail closed. A caller may request public data
for a known public AppID, but cannot use that selector for authenticated access
or connection selection. Trusted proxy Host handling, customer login and
persistent bindings require separate implementations and tests before use.
