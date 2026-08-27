# Storefront API

The phase-one service implements the authoritative `/app/v1/bootstrap`
contract. Run it from the repository root:

```shell
GOWORK=off go run ./services/storefront-api/cmd/storefront-api
```

The checked-in example listens on `127.0.0.1:8090` and recognizes `localhost`
and `127.0.0.1`. It contains public demo values only. Production must provide a
strict external configuration file through `R1SHOP_STOREFRONT_CONFIG`; unknown
fields, duplicate Host/AppID bindings and invalid locale/currency/timezone data
fail startup.

Example:

```shell
curl --header 'Host: localhost' \
  --header 'Accept-Language: en-US' \
  http://127.0.0.1:8090/app/v1/bootstrap
```

The WeChat AppID header is a public selector for anonymous bootstrap data. It
is not a login credential; future customer login must verify the one-time
WeChat code with a server-side secret before issuing a tenant-bound session.
