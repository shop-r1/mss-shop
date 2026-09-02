# Service implementation rules

- Services use the root Go module and may share repository-private domain code
  through `internal/platform`. They must not import either MSS Admin host.
- Storefront clients select public configuration only through exact server-owned
  Host or AppID bindings. Never accept a tenant ID, schema or connection name
  as a routing input.
- Database mutation is limited to two reviewed paths in the isolated
  `mss_shop_dev` database: the single-use `legacy-importer` may create the
  sanitized 51-table snapshot only while the database carries the exact empty
  bootstrap marker, and the reconciler may then create the fixed MSS roles,
  schemas, compatibility objects and grants only when bound to that import
  receipt. Neither component has Kubernetes API credentials. No service may
  mutate the immutable source database, `r1shop-dev`, or production.
- Worker envelopes carry a server-issued tenant ID outside their payload. A
  payload field cannot override tenant scope.
- Keep configuration strict and secret-free. Unknown fields fail startup.
