# Change checklist

| Change | Read/update | Required evidence |
| --- | --- | --- |
| Service or deployment boundary | overall solution, ADR registry, invariants, status | component tests and deployment impact |
| Tenant resolution or connection | DEC-0002, security invariants | negative cross-tenant test |
| Tenant lifecycle/DDL | DEC-0001, migration roadmap | idempotency, retry and least-privilege test |
| `/app/v1` request/response/error | DEC-0003, DEC-0005 and authoritative contract | public-field negative test, mobile snapshot and checksums |
| Locale/message/content format | i18n architecture, DEC-0004 | catalog parity and both-client-target tests |
| MSS module | `.mss` spec and `mss-thin-host` Skill | `mss verify --module` and `--all` as scoped |
| Reconciler or stage operator | DEC-0006, DEC-0010 and lifecycle invariants | transaction replay/fault test, no-overwrite pre/postflight, least privilege and production-mode rejection |
| Data migration | migration roadmap and runbook | dev-only rehearsal, row counts and rollback |

For production-impacting work, stop before mutation until the user approves the
namespace/resource/action and expected impact in the current conversation.
