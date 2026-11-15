# files_service

Serves the HMAC-signed, expiring `/files/{key}?exp=&sig=` links that [studio_service](../studio_service/README.md)
puts in its answers (`GET` and `HEAD`, [contracts/openapi.yaml](../contracts/openapi.yaml) tag `files`), straight from
the object store. No database, no NATS, no Bearer token: the signature is the auth.
Gin through `helmgo/network`; built on [helmgo](../helmgo/README.md) through the `replace` in `go.mod`; image builds need
`make image` or compose, which pass `../helmgo` as a named build context.

```sh
cp .env.example .env    # FILE_URL_SECRET: the same value as studio_service's (on the stack: the file_url_secret secret)
make test               # unit tests, no services needed
make dev                # go run ./cmd: .env names the stack host seaweedfs; on the host point S3_ENDPOINT at an S3 you published by hand
docker compose -f ../deploy/docker-compose.yml up -d --build files   # on the helmstudio stack
```

Env: `.env.example`. `FILE_URL_SECRET` and the S3 keys are read from `<NAME>_FILE` when set (compose `secrets:`), else
from `<NAME>`. A bad or expired signature, a key outside `u/` (GET also serves the starter look thumbnails `packs/<family>/looks/<id>-<16 hex>.<png|jpg|webp>`, nothing else under `packs/`), a `..` or a missing object is 403; objects go out with
`Content-Security-Policy: sandbox` and `nosniff`.

## Layout

| Path | What |
|---|---|
| `cmd/main.go` | `files` (server), `files health` (container check) |
| `startup/` | config → store → module (`network.NewRouter`: `/health`, `/files`) → serve |
| `config/` | env (see `.env.example`) |
| `api/file/` | `Signer.Verify` (the format studio_service's `common.Signer` makes; both tests hold the same vector), `GET` and `HEAD /files/*path`, `PUT /files/*path` (signed uploads: images at most 50 MB; a `u/<owner>/loras/<id>.safetensors` key `application/octet-stream` at most 2 GiB) |
