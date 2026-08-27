# Add a locale

Risk: cross-platform compatibility change. No production write is authorized.

## Preconditions

1. Add the canonical BCP 47 tag and explicit fallback to
   `contracts/locale-registry.yaml`; fallback chains must terminate without a
   cycle.
2. Confirm which surfaces can ship the locale. A third Admin locale requires a
   coordinated MSS foundation release as described in the i18n architecture.
3. Define tenant enablement, content source/fallback, Intl/CLDR support and any
   mini-program package-size impact.

## Implementation

- Add complete locale catalogs and framework adapters for every enabled
  surface. Do not use a generic `zh` alias or conflate Hans and Hant.
- Add message parameter/plural validation and naked-string checks.
- Update storefront negotiation, `Content-Language`, `Vary` and cache keys.
- Update `mss-shop-mobile`'s registry snapshot and its source checksum.
- Add translation-table rows or authoring support for merchant content; do not
  add language-suffixed entity columns.

## Success criteria

- Initial first-class locale keys stay structurally equal.
- Both enabled mobile targets build and pass switch/fallback/offline checks.
- Date/time/number/money and RTL behavior are verified as applicable.
- Unsupported or malformed preferences fall back without changing tenant,
  currency, timezone or authorization context.
- Release notes state surfaces that remain unsupported.
