# Service implementation rules

- Services use the root Go module and may share repository-private domain code
  through `internal/platform`. They must not import either MSS Admin host.
- Storefront clients select public configuration only through exact server-owned
  Host or AppID bindings. Never accept a tenant ID, schema or connection name
  as a routing input.
- Only reconciler code may eventually receive database/Kubernetes mutation
  capabilities. The phase-one memory driver must refuse production mode.
- Worker envelopes carry a server-issued tenant ID outside their payload. A
  payload field cannot override tenant scope.
- Keep configuration strict and secret-free. Unknown fields fail startup.
