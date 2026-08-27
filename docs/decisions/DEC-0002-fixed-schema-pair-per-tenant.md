# DEC-0002: Bind each mall runtime to a fixed schema pair

Status: Accepted
Date: 2026-08-27

## Context

The tenant requires strong data isolation without modifying MSS. MSS owns admin
identity and authorization tables, while R1Shop owns commerce tables. Framework
migrations must not accidentally change business-table constraints.

## Decision

Each tenant has one isolation unit containing an MSS core schema and an R1Shop
business schema. The reconciler derives both names from an immutable tenant key
and provisions least-privilege roles. A mall runtime validates and fixes both
connections at startup. Requests cannot provide or change schema identity.

## Consequences

MSS and commerce migrations can evolve independently. Tenant backup, restore,
upgrade and deletion have a clear boundary. Cross-tenant analytics needs an
explicit aggregate path rather than request-time schema switching.
