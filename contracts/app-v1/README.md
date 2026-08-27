# Storefront API v1

This directory is the authoritative contract for customer-facing H5 and WeChat
Mini Program clients. It is independent from the MSS Admin API.

The first vertical slice provides `GET /app/v1/bootstrap`. H5 requests resolve
the tenant from the exact HTTP `Host`. Mini Program requests send the public
AppID in `X-R1-Client-App-Id`; the server matches it against an explicit
binding. The AppID selector exposes public configuration only and is not an
authentication credential.

`openapi.yaml`, the JSON schemas and both locale examples change together.
Downstream clients commit byte-for-byte snapshots and SHA-256 checksums; they
do not import this repository at build time.

The response intentionally excludes internal tenant IDs, schema names,
database metadata, DSNs, secrets and arbitrary configuration JSON.
