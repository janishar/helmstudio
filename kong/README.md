# kong

The gateway: Kong 3.9 DB-less ([kong.yml](kong.yml), baked into the image) plus the Go plugin
[apikey_auth_plugin/](apikey_auth_plugin/) (go-pdk, run as a plugin server). The only service with a published port:
`127.0.0.1:${KONG_HTTP_PORT}` → container 8000 (`deploy/docker-compose.yml`); no admin API.

| Route | Upstream | Notes |
|---|---|---|
| `/auth/signin`, `/auth/refresh` | auth:8080 | `rate-limiting` per real client IP (trusted proxies only may set `X-Forwarded-For`) |
| `/auth` | auth:8080 | |
| `/v1/studio` | studio:8080 | SSE: 1 h read timeout |
| `/files/` | files:8080 | no API key: the HMAC-signed, expiring link is the auth |
| `/` | web:3000 | no API key: the BFF adds the web app's key server-side; 1 h read timeout (SSE) |

Global plugins: `apikey-auth-plugin` (key → `GET http://auth:8080/verify/apikey`, 2 s timeout, fail closed with 503,
SHA-256-keyed cache 60 s / 10 s, raw key stripped, `X-Client-Id` / `X-Key-Scopes` set), `correlation-id`
(`X-Correlation-ID`), `request-size-limiting` (256 MB).

```sh
make -C apikey_auth_plugin test        # plugin unit tests
docker compose -f ../deploy/docker-compose.yml up -d --build kong
```

`.env` is read by compose but holds nothing today (routes are static in `kong.yml`).
