# Storefront contract rules

- `contracts/app-v1/openapi.yaml` is the authoritative client contract.
- Keep public DTOs explicit. Never add internal tenant IDs, provisioning keys,
  schema names, connection names, DSNs, credentials, secret references or
  untyped configuration maps.
- A Host or WeChat AppID is only matched against a server-owned binding. A
  public AppID may select anonymous public configuration, but it is never proof
  of identity; authenticated WeChat flows must verify a one-time code with the
  corresponding server-side secret.
- Use real HTTP status codes and stable error codes. Clients localize errors.
- Update JSON schemas and examples with OpenAPI changes, then refresh the
  checksum-locked snapshot in `mss-shop-mobile` before changing client code.
