# contracts

The single source for everything that crosses a service boundary ([docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md)). This repo ships only
Catalog Agent. Services never import each other; each generates its own types from here into its `gen/`
(`make gen`) and commits them.

| File | What |
|---|---|
| `openapi.yaml` | OpenAPI 3.1: the public HTTP API behind Kong — `/v1/catalog/*` (catalog_service), `/files/*` (files_service), `/auth/*` (auth_service, tag `auth`, `x-status: built`; `/auth/password/{forgot,reset}` are `x-status: deferred`). |
| `messages/envelope.schema.json` | NATS envelope `{type, version, id, job_id, owner_id, occurred_at, trace, data}`; `job_id` = the command's job (a catalog process or run id) |
| `messages/cmd.catalog.process.schema.json` | catalog → agents: ingest new raw sources (files by key, CSV rows, Drive/website links), sort into SKUs, describe, group, read guidelines + details, run the checks. `ingest: []` = re-read documents and re-run the checks only (sent when guidelines, output, poses, models, backgrounds or details change during setup). Optional `families` limits sorting to those families' products |
| `messages/cmd.catalog.run.schema.json` | catalog → agents: render SKUs × shots with resolved briefs (model/background `key` null = built-in; `model` null = a product without a model; `model` or `background` null with
`model_description` / `background_description` = described only, no reference image), review, regenerate up to `MAX_REGENERATIONS`, save every try; scope `regenerate` carries `feedback` and each shot's `regenerate_of`; a shot's optional `ratio` (else `data.ratio`) and `notes` (the brand's pose instruction); per SKU `passed` (live images outside the run that passed review or were approved: the anchor reference when the run has none) and optional `not_passed` (the poses of the other live ones: a gated product's other shots are not rendered while its anchor pose is there) |
| `messages/evt.catalog.{status,log,step,question,skus,image,run,usage}.schema.json` | agents → catalog on `evt.catalog.<session_id>.<type>`: job lifecycle, chat status lines (same `key` replaces), pipeline steps, check questions (two options, each with the SKU changes to apply), the SKU list + rules, one image's state (with `attempts_detail`: every try's key, review and `best`), per-SKU run progress, one model call's usage (LLM tokens and USD cost, or image credits; the dev Jobs view sums them) |
| `messages/req.catalog.{interpret,poses.suggest}.schema.json` | NATS request/reply (core NATS, subjects `catalog.interpret`, `catalog.poses.suggest`, queue group `agents`, not enveloped): `$defs/request`, `$defs/reply`; poses.suggest answers custom shots (`defs customPose`) and `refs` (reference key → pose id), and takes `exact` (the brand's shot list) |
| `messages/req.auth.validate.{token,role}.schema.json` | NATS request/reply with auth_service (not enveloped) |
| `messages/evt.auth.revoked.schema.json` | auth_service → catalog: drop cached token validations |
| `agents.cancel` (no schema) | catalog → every agents replica, core NATS broadcast, not enveloped: the body is a run id as plain text; the replica running that job stops it. Sent by `POST …/runs/{run_id}/stop`, and again when a stopped run reports `running` |
| `agents.cancel.image` (no schema) | catalog → every agents replica, core NATS broadcast, not enveloped: the body is `<run_id> <image_id>`; the replica running that job skips the shot. Sent by `POST …/images/stop` (which also drops those shots from a command still in catalog's outbox), and again (for every id in the run's `stopped_image_ids`) when the run reports `running` |
| `messages/defs.schema.json` | shared definitions (uuid, object keys, seq, SKU refs, attributes, rules, …) |
| `registry.schema.json` | KV `registry` values agents_service publishes: `$defs/catalog_library` (families, categories = products with `family`, `uses_model`, `pose_noun` {singular, plural} and optional per-product `max_attempts` / `default_resolution`, poses, built-in models (optional `full_body_url`, `wearer`) and backgrounds, a category's optional `default_men_model`, resolutions, ratios, defaults), `$defs/catalog_prices` (credits per image by resolution, `max_attempts` renders per image at most), `$defs/catalog_checks` (the processing-check catalogue) |
| `fixtures/{messages,registry}/` | `<name>.valid[.N].json` must pass, `<name>.invalid[.N].json` must fail (request/reply: `<type>.{request,reply}.…`) |
| `check.py` | validates every fixture against its schema and lints `openapi.yaml` |

```sh
make -C contract_tests venv              # once: contract_tests' own .venv has the checker's dependencies
make -C contract_tests check-contracts   # exit 1 on any failure
```

## How the pieces meet

- **Ids.** catalog_service owns every id: session, SKU (`sku_id`; agents name SKUs by `code`, catalog upserts on
  `(session_id, code)`), question (upsert on `(session_id, key)`; an answered key is never re-opened), run, image
  (catalog creates each image row `generating` before sending `cmd.catalog.run`; agents report by `image_id`).
- **Questions.** `evt.catalog.question` options carry `apply` (brief, attributes, confirmed or `merge_into` per SKU code);
  catalog applies the chosen option on `POST /questions/{id}/answer`. HTTP shows only the labels.
- **Events to the browser.** The projector writes each `evt.catalog.*` into the tables and a per-session event log
  (`seq`); `GET /sessions/{id}/events` serves it as JSON (`?since=`) or SSE, with object keys turned into signed
  URLs (see `SessionEvent` in openapi.yaml).
- **Library.** Pictures in `catalog_library` are static web paths served by web_service (`/catalog/...`); agents keep their
  own reference assets per id. User uploads (model, background, pose reference) are session uploads whose UUID is
  used where a library id goes (`choiceId`).
- **Products.** A `category` is a product (Ring, Sofa, Tops …) inside a `family` (jewellery, furniture,
  garments); agents_service defines them (`agents_service/catalog/products/`) and publishes them in `catalog_library`
  in run order (family order, then product order). `uses_model: false` (furniture, jewellery coins) means no model:
  `default_model` and the SKU brief's `model` are null. SKU `attributes` always carry `description` and `source`; the
  other fields are the product family's (jewellery `counts, motifs, finish, size_mm, material, kind`; garments
  `pieces, fabric, colour, print, work, details, kind`; furniture `material, colour, finish, dimensions_cm, style`); garments and jewellery add `sides` (raw
  key → view: garments front, back, side; jewellery hero, front, side, angle, flat, close-up, pair, hanging, model,
  mood; null = unknown), which HTTP also shows as `Sku.raw_views` in `raw_urls` order. A background's optional
  `families` lists the families it suits (absent = all).
- **Folder uploads.** The raw multipart's optional `paths` (one per file, inside the picked folder) reach agents as
  the files' `names`; an upload spanning several sub-folders sorts one product per sub-folder.
- **Tries and feedback.** `Image.attempts[]` signs every try of a revision (`best` = the one in `url`). A SKU × shot's
  feedback thread is its revisions in order (each `Image.feedback` + its `attempts`); no separate thread resource.
- **Ratios and pose notes.** A SKU `Brief` has a default `ratio` plus `pose_ratios` and `pose_notes` (pose id → ratio /
  free text, `{}` = none), set by `PATCH /skus/{id}`; catalog resolves each shot's `ratio` and `notes` into
  `cmd.catalog.run` from the shot's own SKU brief. `RegenerateRequest.ratio` overrides the ratio of the new revisions.
  Every ratio must be a library `ratios[].id` (422).
- **Credits** are quotes from `catalog_prices` (`credits_quoted`); nothing is charged.

## Rules

- **Claim check:** messages carry object keys under `u/<owner_id>/…`, never bytes or signed URLs. Producers only
  write keys under the envelope's `owner_id` prefix; session files live under `u/<owner>/catalog/<session>/`
  (`raw/`, `guidelines/`, `uploads/`, `images/<image_id>.<ext>` = the best try, `images/<image_id>/attempt_<n>.<ext>` = every try, kept as long as the image). The only URLs in messages are links the user
  typed (Drive, website, CSV `image_url`, document links), which agents fetch (never in fake mode).
- **Envelope** is closed (`additionalProperties: false`); `data` is open: consumers ignore fields they don't know.
- **Versioning:** a message's `version` is the major version of its `data` schema (`const` in the schema).
  Additive, optional fields keep the version. Removing or renaming a field, changing a type or meaning, or making a
  field required is breaking: add `<type>` version N+1 as a new schema, publish both until every consumer reads
  N+1, then drop N. Envelope changes are breaking for every type. Registry values carry `schema_version` the same
  way. (The Catalog Agent rewrite replaced every `cmd.job.*` / `evt.job.*` type at once; nothing reads them any more.)
- **HTTP:** `info.version` follows semver; additive response fields and new optional request fields are minor;
  anything that breaks a generated client is a new major. Errors are always `{"detail": string}`. Long work is 202;
  a state conflict (blocking question open, processing or run in progress, not started, nothing to do) is 409.
- **Idempotency:** commands use `Nats-Msg-Id = <job_id>:<attempt>`; every `evt.catalog.*` carries a per-job `seq` and
  the projector dedupes on `(job_id, seq)`.
- **Ownership:** every message has `owner_id`; cross-user reads answer 404 (not 403).
- **Change process:** edit the schema, add or adjust a valid **and** an invalid fixture, run `check.py`, regenerate
  each service's `gen/`.

## auth_service internals (not in openapi.yaml)

**`GET /verify/apikey`** — internal, called only by Kong's `apikey-auth-plugin` (`http://auth:8080/verify/apikey`);
Kong never routes it. Request header `x-api-key: <key>`. Answers:

- `200 {"client_id": "<uuid>", "client": "<name>", "scopes": ["..."]}` — active key (touches `last_used_at`, at most
  once a minute).
- `401 {"detail": "invalid api key"}` — missing, longer than 200 characters, unknown or revoked.
- `500 {"detail": "internal error"}`.

**NATS request/reply** (core NATS, queue group `auth`, not JetStream, bare JSON — no envelope):

| Subject | Request | Reply |
|---|---|---|
| `auth.validate.token` | `{"token"}` | `{user_id, email, roles, jti, family_id, expires_at}` or `{"error"}` |
| `auth.validate.role` | `{"token", "role"}` | the same + `allowed` (bool), or `{"error"}` |

`roles` is omitted when empty. Errors: `invalid or expired token`, `bad request: …`, `internal error` — treat any
`error` as unauthenticated. Cache a success until `expires_at` and drop it on `evt.auth.revoked` (`scope: family` →
matching `family_id`; `scope: user` → every entry of `user_id`).
