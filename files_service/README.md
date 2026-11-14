# files_service

Serves the HMAC-signed, expiring `/files/{key}?exp=&sig=` links that [catalog_service](../catalog_service/README.md)
puts in its answers (`GET` and `HEAD`, [contracts/openapi.yaml](../contracts/openapi.yaml) tag `files`), straight from
the object store, and runs the storage janitor. No database, no NATS, no Bearer token: the signature is the auth.
Gin through `orago/network`; built on [orago](../orago/README.md) through the `replace` in `go.mod`; image builds need
`make image` or compose, which pass `../orago` as a named build context.

```sh
cp .env.example .env    # FILE_URL_SECRET: the same value as catalog_service's (on the stack: the file_url_secret secret)
make test               # unit tests, no services needed
make dev                # go run ./cmd: .env names the stack host seaweedfs; on the host point S3_ENDPOINT at an S3 you published by hand
docker compose -f ../deploy/docker-compose.yml up -d --build files   # on the ora-catalog stack (deploy/compose/files.yml)
```

Env: `.env.example`. `FILE_URL_SECRET` and the S3 keys are read from `<NAME>_FILE` when set (compose `secrets:`), else
from `<NAME>`. A bad or expired signature, a key outside `u/`, a `..` or a missing object is 403; objects go out with
`Content-Security-Policy: sandbox` and `nosniff` (uploaded documents may be HTML or SVG).

**Janitor**, at start and hourly: deletes `u/*/work/` and `u/*/catalog/*/work/` objects older than `RETENTION_WORK_DAYS`
(default 30) and `u/*/archives/` (download zips) older than 1 day, using only List + Delete. Every replica sweeps
(deletes are idempotent); the stack runs one.

## Layout

| Path | What |
|---|---|
| `cmd/main.go` | `files` (server), `files health` (container check) |
| `startup/` | config → store → module (`network.NewRouter`: `/health`, `/files`) → janitor → serve; `Sweep`, `Janitor` |
| `config/` | env (see `.env.example`) |
| `api/file/` | `Signer.Verify` (the format catalog_service's `common.Signer` makes; both tests hold the same vector), `GET` and `HEAD /files/*path` |
