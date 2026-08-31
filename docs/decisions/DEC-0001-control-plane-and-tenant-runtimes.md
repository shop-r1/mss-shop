# DEC-0001: Separate control plane from per-tenant mall runtimes

Status: Accepted
Date: 2026-08-27

## Context

The platform needs a tenant-facing lifecycle console and a mall management
console. MSS should remain unchanged, and a tenant failure or upgrade should
not change another tenant's database context.

## Decision

Run one tenant platform as the control plane and one mall-platform deployment
per tenant. Both are independent MSS Thin Hosts built from the same exact 1.3.6
Distribution. The control plane records desired state; a separate reconciler is
the sole writer for tenant infrastructure and schemas.

Implementation note (2026-09-01): the coordinated Distribution advanced from
1.3.6 to 1.3.7 without changing this topology decision.

## Consequences

The first tenant needs two admin deployments. Resource use is higher than a
single runtime with dynamic tenant switching, but authentication realms,
configuration, upgrades, rollback and blast radius are explicit. A single mall
image can still serve all tenants through separate deployments.
