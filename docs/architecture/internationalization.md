# Internationalization architecture

## Scope and baseline

Internationalization is an architecture constraint for tenant-platform,
mall-platform, storefront API, H5 and WeChat Mini Program. Initial complete
locales are canonical BCP 47 tags `zh-CN` and `en-US`; adding a third locale is
an additive product change, not a redesign.

The uni-app runtime uses platform locale names such as `zh-Hans` and `en`.
Adapters map those values to the canonical API/domain tags. Persistence,
contracts and analytics use only canonical tags.

## Three different kinds of localized data

1. UI messages live in versioned locale catalogs and use stable semantic keys.
2. API errors use a stable code plus structured arguments. A server message is
   a diagnostic fallback, not the client's localization contract.
3. Merchant-authored content uses explicit translation records keyed by entity,
   field and locale. Do not add columns such as `name_en` or `name_fr`.

Orders, invoices and notifications that must remain historically accurate save
the localized display snapshot used at the transaction time. A later product
translation must not rewrite an old order line.

## Locale negotiation

For a signed-in user:

```text
explicit request preference
  -> user profile locale
  -> tenant default locale
  -> schema fallback en-US
```

For anonymous storefront requests, replace the user profile step with a valid
`Accept-Language` preference. The server must parse weights, normalize aliases,
match only tenant-enabled locales and return the chosen locale in the bootstrap
response. Host or AppID selects a tenant candidate; it never selects a schema
directly.

Content fallback is separate:

```text
negotiated locale -> tenant content source locale -> tenant default -> en-US
```

Missing content is observable. It must not silently mutate the user's saved UI
locale.

## Formatting rules

- Locale, currency and timezone are independent values.
- Store monetary values as minor units or exact decimals with an ISO 4217 code;
  format at the presentation boundary.
- Store instants in UTC and apply the tenant/user timezone only for display.
- Use ICU plural/select messages. Do not construct sentences by concatenating
  translated fragments.
- Layout and design tokens must allow longer text and future RTL mirroring even
  though phase one locales are left-to-right.

## MSS Admin limitation and upgrade gate

MSS 1.3.7 composes complete `zh-CN` and `en-US` catalogs for both Thin
Hosts, so those two locales are the supported admin baseline. A third Admin
locale is not implemented by patching one host. It requires a coordinated MSS
Distribution upgrade that provides:

- standards-compliant `Accept-Language` negotiation including weights;
- distinct Simplified/Traditional Chinese handling;
- dynamic Umi/react-intl, Ant Design and dayjs locale registration; and
- identical behavior in tenant-platform and mall-platform.

Until that foundation capability is released, storefront/mobile may add a
locale independently, but Admin surfaces remain explicitly limited to the two
complete locales. This limitation must be visible in the capability matrix and
release notes.

For authorized business menus on 1.3.7, each Host enables dynamic-menu locale
through its tested runtime facade. Backend root tokens stay stable and
dot-free; direct MenuSearch keys and hierarchical ProLayout keys are complete
in both catalogs. See
[`mss-1.3.7-generation-notes.md`](../tooling/mss-1.3.7-generation-notes.md)
for the upgrade constraint.

## Definition of done

- No new user-facing literal is introduced outside a locale catalog, except
  immutable brand names or user-entered content.
- `zh-CN` and `en-US` keys remain structurally equal.
- Authorized root, directory and leaf menus switch language without missing
  message warnings or changing paths and permissions.
- API error codes and arguments are covered in the client catalog.
- Date, time, number and money output uses explicit locale/timezone/currency.
- H5 and `mp-weixin` exercise language switching without a restart where the
  platform supports it.
- Tenant bootstrap never exposes schema names, DSNs or other server-only data.
