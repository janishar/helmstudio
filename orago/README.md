# orago

The Go framework behind `auth_service`, `catalog_service` and `files_service`: the goserve architecture on Gin, owned
by this repo. HTTP and NATS plumbing, no domain code. The guide (packages, service layout, handlers, DTOs, errors,
NATS and JetStream helpers, testing, adding a feature): [docs/ORAGO.md](../docs/ORAGO.md).

Packages: `config`, `network` (the Gin edge), `middleware`, `micro` (NATS client and router, request/reply and
JetStream helpers), `postgres`, `storage`, `utility`.

## Use
Services import it as `github.com/helmstudio/orago/...` through `replace github.com/helmstudio/orago => ../orago` in their
`go.mod`; image builds get this folder as the `orago` named build context.

## Develop
- `make test`: tests. Without `TEST_DATABASE_URL` the Postgres tests skip; for them, run `.tools/test-db`, then
  `cp .test.env.example .test.env` and set the password from `deploy/secrets/postgres_password`. The JetStream tests
  run an in-process NATS server (`nats-server/v2`, a test-only dependency), so they need no Docker.
- `make check`: tests, lint, the three rules (no domain words, Gin only at the HTTP edge, `jetstream` only inside
  `orago/micro`) and a build of the three Go services. Run it before every change here lands.
