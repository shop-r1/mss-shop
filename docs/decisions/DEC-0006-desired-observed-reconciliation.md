# DEC-0006: Reconcile tenant desired and observed state

Status: accepted
Date: 2026-08-28

## Context

Creating schemas, roles, credentials and runtime resources inside a tenant
platform HTTP request couples user latency to privileged, partially retryable
operations. It also makes interruption and audit recovery unsafe.

## Decision

- The tenant platform records desired state. Phase one supports only `ACTIVE`
  and `SUSPENDED`; destruction is deliberately absent.
- A separate reconciler owns all future tenant-resource mutations and converges
  one immutable tenant identity at a time under a lease.
- Every step has Ensure semantics, a versioned checkpoint and an observed
  result. Replaying a successful plan creates no duplicate resource.
- Status updates use generation plus compare-and-swap resource versions. A
  changed desired generation stops the old plan from publishing Ready.
- The first implementation is an in-memory simulation with fault injection.
  It refuses any mode other than `simulation` and does not import SQL,
  PostgreSQL or Kubernetes drivers.
- The worker uses a tenant-scoped inbox key `(tenant_id, message_id)` so
  at-least-once delivery does not duplicate successful work. Tenant identity is
  carried outside the untrusted payload.

## Consequences

The lifecycle and retry semantics can be tested locally without granting
infrastructure privileges. This implementation is not a production controller
or queue: persistent repositories, leases, transactional inboxes, database
roles and Kubernetes RBAC remain explicit later milestones.
