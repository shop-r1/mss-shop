# DEC-0004: Make internationalization a baseline

Status: Accepted
Date: 2026-08-27

## Context

Retrofitting locale keys, content translations and error contracts after pages
and schemas exist is expensive. Every platform is expected to add languages in
the future, although phase one needs only Simplified Chinese and English.

## Decision

Use canonical BCP 47 locale tags, stable message/error keys and separate
merchant-content translations from the first implementation. Locale, currency
and timezone remain independent. `zh-CN` and `en-US` must be complete in every
released surface. A third MSS Admin locale waits for a coordinated foundation
capability rather than per-host patches.

## Consequences

Every feature carries a small catalog and test cost now. Future locale work is
additive, historical order text remains stable, and API clients do not parse
human-readable messages as program state.
