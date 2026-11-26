# CLAUDE.md — working rules for this repo

- **Read first:** [docs/README.md](docs/README.md) and the plan section for your area; the service architecture is
  [docs/plan/13-services.md](docs/plan/13-services.md), versions are owned by
  [docs/plan/14-dependencies.md](docs/plan/14-dependencies.md). Build only the current milestone
  ([docs/plan/08-milestones.md](docs/plan/08-milestones.md)): no scaffolding for later ones.
- **Contracts first.** [contracts/](contracts/) is the single source for everything that crosses a service boundary
  (`openapi.yaml`, `messages/`, `registry.schema.json`). Change the contract first, then the services, then run
  `make contracts-check`. Services regenerate or copy what they need with `make gen` and commit it.
- **Services are isolated.** Each `*_service/`, `kong/`, `cli/` and `contract_tests/` has its own dependencies,
  lockfile, `.env` / `.env.example`, Dockerfile and Makefile. Services never import each other's code or read each
  other's `.env`; `deploy/.env` holds only compose-level values. No root virtualenv, `node_modules` or Go workspace.
- **Gates** before calling work done, tiered (owner, 2026-11-24; the full suite takes ~16 min):
  - **Every change:** the touched service's `make test lint`, `make contracts-check`, and only the contract tests
    the change touches: `make test T="tests/test_x.py tests/test_y.py"` (find them by grepping `contract_tests/tests`
    for the routes, messages and texts changed). Go DB tests need `.tools/test-db`.
  - **A feature or plan built:** the full `make test` once, at the end of the build (not after every fix).
  - **Before pushing a day's run of small changes** that never had a full run: the full `make test` once.
- **Safety.** Tests never run against the dev stack (`helmstudio`, Kong 8300, Postgres 5463) or its volumes; they use
  `helmstudio-test` (8390 / 5464), scratch databases and fake mode. Never `down -v` a stack you did not create.
- **Never commit secrets.** Secrets are files in `deploy/secrets/` (`.tools/make-secrets`), read through `X_FILE`;
  `.env` files are git-ignored and `.env.example` files hold placeholders only. Never print keys from them.
- **Patterns kept from the reference:** errors are `{"detail": string}`; another user's id is 404; long work is 202;
  owner scoping by row-level security (`WithUser`); outbox → `CMD` → worker → `EVT` → projector → SSE; object keys
  under `u/<owner_id>/`, never bytes or URLs in messages.
- **Right-click menus.** Every new feature that adds something you act on (a card, a row, a block, a clip, a node) ships
  with its right-click menu in the shared menu, and its board shows that menu (owner, 2026-11-26: "new features will come
  with menu as well in future"; docs/mock/ContextMenus.dc.html).
- **Docs live under docs/**, organised by topic; never a loose plan or spec file at the root.
