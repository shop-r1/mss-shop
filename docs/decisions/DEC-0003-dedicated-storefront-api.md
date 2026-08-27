# DEC-0003: Use a dedicated versioned storefront API

Status: Accepted
Date: 2026-08-27

## Context

Anonymous customers and members have different security, latency and release
needs from administrators. Exposing MSS Admin endpoints would couple the mobile
application to internal roles, routes and sessions.

## Decision

H5 and WeChat Mini Program consume an authoritative `/app/v1` contract owned by
`storefront-api`. Tenant resolution, customer authentication, stable errors and
compatibility policy live at that boundary. MSS Admin endpoints remain private
to the two administration platforms.

## Consequences

The mobile client has a stable contract and can be generated/tested from a
snapshot. Some models and validation are deliberately adapted between Admin
and storefront instead of shared as database structures.
