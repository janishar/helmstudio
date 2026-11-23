# helmstudio-next plan — Provider keys from the UI: paste the key, the server writes the file

[← Plan index](../README.md)

Status: **decided by the owner (2026-11-23); board approved by the owner (2026-11-24, "Approve"); queued to build.** API version: **1.52.0** (main is at 1.51.0, plan
25; if another plan takes 1.52.0 first, this takes the next free one at build time).

The owner's ask, 2026-11-23, on Admin › Model providers: "add support to provide api key in the ui itself. ask any
doubt". The four doubts were asked one by one; the owner took every recommended option (below).

Board: [Providers](../mock/Providers.dc.html) (the form's key area in Found with Replace key and Remove key; other
states: Missing with the field, Checking, Refused, Could not check with Save anyway, Replace open, Remove confirm).
It stays 1440 × 960; canvas.json is unchanged.

Facts come from a read on 2026-11-24 of plan 25 (as built, 29.13), plans 13 and 18, `contracts/openapi.yaml`
(`/v1/studio/admin/provider-keys/{key_secret}`, `ProviderKey`), `contracts/messages/defs.schema.json`
(`providerSnapshot`, the `key_secret` pattern), `contracts/README.md` (Model sources: Keys, Key file status),
`pipelines_service/providers.py` (`key_path`, `key_found`, `api_key`, `_classify`, `test`, `answer_key`, `serve`) and
`config.py` (`PROVIDER_KEYS_DIRS`), `pipelines_service/Dockerfile`, `studio_service/api/studio/providers.go`
(`ProviderKey`, `FetchPresetModels`, `askTest`), `deploy/compose/pipelines.yml`, `fake.yml`, `dev-remote.yml`,
`deploy/nats/nats.conf`, `.tools/nats-setup`, `.tools/make-secrets`, `kong/kong.yml`, `web_service/lib/proxy.ts`,
`web_service/components/admin/providers.tsx` and the Providers board; and a write test on the owner's Mac (31.5).

## 31. Provider keys from the UI

### Decided by the owner, 2026-11-23

1. **Storage: the server writes the key file.** The UI sends the key once; the server writes
   `deploy/secrets/providers/<key name>` (owner-only permissions). Secrets stay files; Found / Missing and Recheck work
   as built in plan 25; no database copy of the key.
2. **After saving: "Found" with the path only.** The server never reads the key back, not even part of it.
3. **Replace and Remove.** "Replace key" opens an empty field to type a new one; "Remove key" deletes the key file
   after a confirm (the provider then shows Missing).
4. **Check before saving.** The server tries the key on the provider's `/models` (only the preset's or the saved
   provider's URL, the same host rules as Fetch). A refused key (401 / 403) is not saved and the form says so; a
   network failure or timeout lets the admin save anyway, with a clear note.

### 31.1 What exists today

- **Key files** live in `deploy/secrets/providers/` (made by `.tools/make-secrets`, `mkdir -p providers`; the
  owner's `deepseek` file is there, mode 0644). Compose mounts it **read-only** in **pipelines only**:
  `pipelines.yml` `volumes: [./secrets/providers:/run/secrets/providers:ro]`, `PIPELINES_PROVIDER_KEYS_DIR:
  /run/secrets/providers`. Studio never sees the folder.
- **Several folders** (API 1.51): `config.PROVIDER_KEYS_DIRS` splits `PIPELINES_PROVIDER_KEYS_DIR` on `:`;
  `providers.key_path` takes the first folder that has a regular file of that name. The dev stack
  (`dev-remote.yml`) reads `/run/secrets/providers:/fake-keys` (the mount live, then the fake's placeholder); the test
  stack (`fake.yml`) reads only `/fake-keys` (a compose `config`, read-only).
- **Readers**: `api_key` reads the file at call time and strips it; `key_found` only stats it (the
  `pipelines.providers.key` responder). The key is never logged; errors name the file.
- **The routes**: `GET /v1/studio/admin/provider-keys/{key_secret}` → `ProviderKey {key_secret, path, found}` (5 s,
  else 503; 404 for a malformed name). Fetch: `POST …/provider-presets/{id}/models` (studio builds the snapshot from
  the embedded preset, never a URL from the browser) and a saved provider's `POST …/providers/{name}/test`; both ask
  `pipelines.providers.test`, which lists `GET <base_url>/models` under the host rule (https and public for a remote,
  needs `REMOTE_MODELS`; loopback, private or what `host.docker.internal` resolves to for a server; no redirects, no
  proxy, pinned connections).
- **Failure texts are ours**: `_classify` makes `"<name>: the key was refused"` for 401, `"<name>: HTTP <code>"` for
  other 4xx, `"network error: <type or HTTP code>"`; it never copies the provider's body (where some providers echo a
  masked key).
- **Key name pattern** (`defs` `key_secret`): `^[a-z0-9][a-z0-9_.-]{0,63}$`: no `/`, no leading `.`, at most 64.
- **What sees a request body**: the web BFF (`lib/proxy.ts` streams it to Kong; it logs only `proxy <method>
  <path>` on a failed fetch); Kong (plugins: apikey-auth, correlation-id, request-size-limiting, rate-limiting; no
  body logging); studio (gin). **NATS**: service request/reply is core NATS; the only streams are `CMD` (`cmd.>`),
  `EVT` (`evt.>`), `DLQ` (`dlq.>`) and KV buckets, so nothing under `pipelines.` is stored or dead-lettered;
  `nats.conf` sets no `trace` or `debug`; each service user publishes and subscribes only its listed subjects, and a
  remote GPU host's account reaches only what HELM exports (no `pipelines.` subject).
- **No admin audit exists**: no audit table or audit log in studio; admin writes leave no record today.
- **The UI says keys are never typed here**: the form's Info text ("Keys are never entered here: put the key in the
  key file the server reads"), the Missing note ("Put the key in this file, then Recheck. The key is never shown
  here."), plan 25 29.2 ("Not in: typing a key in the browser (never)"), `contracts/README.md` Keys ("No API takes or
  returns a key"), `defs.schema.json` `providerSnapshot` ("the key itself never travels").
- **The assistant** (plan 18) reads only the tab on Admin and never does provider writes ("Yours only").

### 31.2 What comes in, what does not

In: one save route and one remove route; one pipelines request (write or remove, with the check); `ProviderKey`
gains `writable`; the key area in the form (field, Save key, the check's answers, Replace key, Remove key with a
confirm); the providers mount becomes writable for pipelines; a log line per save and remove; the wording changes
(31.9).

Not in: a database copy of the key; reading a key back (whole or part); a key for any URL the browser sends; an audit
table (31.7); keys for anything but model providers (Brave search keeps its file the same way; it is a provider, so it
gets the field too, at no extra cost); rotating or expiring keys.

### 31.3 Design: one narrow path

```
browser ──PUT key──▶ web BFF ─▶ Kong ─▶ studio ──NATS request pipelines.providers.key.write──▶ pipelines
                                         │  (admin only; builds the check            │ check /models with the key in memory
                                         │   snapshot from the preset or the         │ then write <first key folder>/<name>
                                         │   saved provider, never the browser)      │ (temp + rename, 0600)
                                         ◀──────────── {found, saved, check} ────────┘
```

- **Pipelines writes, studio asks.** Pipelines already owns the key folder (the only service that mounts it, reads
  keys and stats them); studio is the admin API and checks the caller. So the key reaches the disk through exactly one
  code path in one service; studio still never sees the folder. Studio writing the file instead would mount the
  secrets into a second service; pipelines exposing HTTP would add a port it does not have.
- **Why NATS is fine for the key.** The request is core NATS request/reply on the compose network, like every
  studio → pipelines request today: no stream matches `pipelines.>`, so the key is never stored in JetStream, never in
  the DLQ, never replayed; nats-server does not log payloads (no `trace`); only the `studio` user may publish the
  subject and only `pipelines` may subscribe (`nats.conf`), and remote GPU hosts cannot reach it. When the stack runs
  with `STACK_TLS=on`, the stack's own services still talk plain inside the compose network (as all other messages);
  the key is no more exposed than the DeepSeek key file already is to anyone who can read the Docker network.
- **The one folder written**: the **first** folder of `PIPELINES_PROVIDER_KEYS_DIR`, and nowhere else. First, because
  `key_path` takes the first folder that has the file, so a saved key always wins over a same-name file further down.
  No new setting: whether saving is possible is simply whether that folder is a writable directory
  (`os.access(W_OK)`), reported as `writable`. Pipelines makes the first folder at start if it is missing
  (`mkdir(mode=0o700, exist_ok=True)`, ignored on a read-only file system).
- **The write** (`providers.write_key(name, key)`): name must match the `key_secret` pattern (so no `/`, no `..`, no
  leading `.`); `tempfile.mkstemp(dir=<first>, prefix=".tmp-")` (mode 0600 from mkstemp; the leading `.` can never
  be a key name), write the key bytes, `fsync`, `os.replace(tmp, <first>/<name>)`, `fsync` the folder; the temp file
  is removed on any failure. `os.replace` replaces a symlink of that name itself, never its target. A folder of that
  name is an error (`not a file`), nothing written.
- **The remove** (`providers.remove_key(name)`): `os.unlink(<first>/<name>)` when it is a regular file or a symlink;
  missing is fine (nothing to do); never another folder. The answer reports `found` after it (a same-name file in a
  later folder, such as the fake's placeholder on the dev stack, still counts and the line shows Found).
- **The key value**: trimmed of surrounding whitespace; 1 to 1024 characters; printable ASCII without spaces (0x21 to
  0x7E), so no newline or control character reaches the file or a header. Checked in studio (422) and again in
  pipelines (by the message schema).

### 31.4 The check before saving

- **Which URL.** The save body names one source, as Fetch does: `preset` (an id: studio takes its `kind`, `base_url`,
  `compat` from the embedded file) or `provider` (a saved name: studio takes the stored row). Never both, never a URL
  (`additionalProperties: false`: a `base_url` is 422). The form sends `preset` while Base URL equals the preset's,
  `provider` while it equals the saved provider's; otherwise Save key is greyed with Fetch's note "Fetch asks only the
  preset's or the saved URL. Save first." (the same rule, the same words).
- **The call.** Pipelines runs the existing `test` path (`check_host`, the pinned client, no redirects, no proxy) with
  the key **in memory** instead of from a file (`test(req, key=…)`; `api_key` is used only when no key is given),
  listing `/models` only (no completion), timeout 15 s. The key goes only to that `base_url`, after the host rule.
- **The verdict**, from the HTTP status itself (not the error text):
  - `/models` answers → `check: ok`, then write.
  - **401 or 403** → `check: refused`, **nothing written**: "the provider refused this key; not saved".
  - Anything else (network, timeout, 429, 5xx, another 4xx such as a provider without `/models`, the host rule
    refusing, remote models off) → `check: unreachable`, nothing written, with our own short reason ("network error:
    timeout", "HTTP 404", "remote models are off on this deployment"). The form offers **Save anyway**.
- **Save anyway** resends with `force: true`: pipelines writes **without** a second call (no second 15 s wait);
  `check: skipped`. The note says the key was not checked and is tried on first use.

### 31.5 Compose and the folder

- **Production** (base `pipelines.yml`): the mount loses `:ro` — `./secrets/providers:/run/secrets/providers`. Only
  that folder is mounted, only into pipelines; `deploy/secrets/` itself (database URLs, NATS passwords, TLS keys)
  stays as compose secrets, read-only. Acceptable because pipelines already reads every key in it: code running in
  pipelines could already take them, so write access adds only replacing or removing a provider key, which the one
  admin route also does, and nothing else in pipelines writes there.
- **Dev stack** (base + `fake.yml` + `dev-remote.yml`): writes go to the same bind mount (`/run/secrets/providers` is
  first in `dev-remote.yml`'s list). `fake.yml` re-declares the mount `:ro` (compose merges volumes by target), and
  `dev-remote.yml` re-declares it writable, so only the dev stack and production can write the real folder.
- **Test stack** (base + `fake.yml`): `PIPELINES_PROVIDER_KEYS_DIR: /tmp/provider-keys:/fake-keys`. Saved keys go to
  the container's own `/tmp/provider-keys` (made at start, gone when the container is recreated); the real folder is
  mounted read-only there and is not in the list, so a test can never write or remove a real key.
- **Ownership and permissions.**
  - **The owner's Mac (OrbStack)**, tested 2026-11-24: a container running as uid 10001 (pipelines' user) wrote a
    temp file into a bind-mounted folder owned by the Mac user (mode 0755) and renamed it; inside it is uid 10001
    mode 0600, on the Mac it shows **owned by the Mac user, mode 0600**. So no chown is needed, the owner can still
    remove or replace it by hand, and other Mac users cannot read it. (Docker Desktop behaves the same way for
    ownership.)
  - **Linux** (the GCP VM, a production host): the bind mount keeps real uids, so pipelines (uid 10001) can write
    only if the folder is theirs: `sudo chown 10001:10001 deploy/secrets/providers && chmod 700` once.
    `.tools/make-secrets` does it when run as root and otherwise prints that line. Saved files are 10001-owned 0600:
    the host user needs sudo to read them, which suits "never read back". A key file placed by hand must stay
    readable by 10001 (as today).
- **Read-only on purpose**: a deployment that keeps `:ro` (or whose first folder is not writable) answers
  `writable: false`; the form then shows plan 25's Missing note with no field, and the routes answer 409.

### 31.6 Contract changes (1.52.0)

- `openapi.yaml` (admin only, 403 otherwise; `info.version` and the studio tag line get "from 1.52 saving a provider
  key"):
  - `PUT /v1/studio/admin/provider-keys/{key_secret}` body `ProviderKeySave {key, preset? | provider?, force?}`
    (`key` string 1-1024, `writeOnly: true`, no `examples`; exactly one of `preset`, `provider`; `force` boolean,
    default false; `additionalProperties: false`) → 200 `ProviderKeySaved {key_secret, path, found, saved, check,
    detail?}`; `check` is `ok | refused | unreachable | skipped`; `saved` is true for `ok` and `skipped`. 404 malformed
    name, unknown preset or provider; 422 a bad key (the detail names the rule, never the value), both or neither
    source, a `base_url`; 409 `keys are set in files on this deployment` (not writable); 503 when pipelines does not
    answer in 30 s.
  - `DELETE /v1/studio/admin/provider-keys/{key_secret}` → 200 `ProviderKey` (the status after removal). 404
    malformed name; 409 not writable; 503 after 5 s.
  - `ProviderKey` gains `writable: boolean` (the deployment lets the admin save keys here); the GET route fills it.
- `messages/req.pipelines.providers.key.write.schema.json` (new, request/reply, not enveloped, subject
  `pipelines.providers.key.write`): request `{op: "save", key_secret, key, provider (a providerSnapshot without
  key_secret), force?}` or `{op: "remove", key_secret}`; reply `{found, writable, saved?, check?, detail?}` or
  `{error, code}` with `code` one of `readonly`, `not_file`, `internal`. Its description says the subject is core
  request/reply only and must never be added to a stream.
- `messages/req.pipelines.providers.key.schema.json`: the reply gains `writable`.
- `messages/defs.schema.json` `providerSnapshot`: the description's "the key itself never travels" becomes "the
  snapshot never carries a key; only `pipelines.providers.key.write` does".
- `deploy/nats/nats.conf`: studio may publish and pipelines subscribe `pipelines.providers.key.write`.
- `contracts/README.md` Model sources: the Keys bullet and the Key file status bullet (31.9).

### 31.7 Security

- **One way in, one place written.** Admin-only route → studio → one NATS subject only studio may publish → one
  function in pipelines → the first key folder, by a name that cannot leave it. Nothing else writes the folder.
- **The key goes only to the provider being checked**: the preset's or saved provider's base URL, after the host rule
  and the remote switch, pinned, no redirects, no proxy. Never a URL from the browser.
- **Never read back**: no route or message returns it or any part of it; `found` is a stat; replies carry our own
  fixed texts, never the provider's error body (some echo a masked key).
- **Never logged or echoed**: studio's body type has no `String`/`%v` use and its 422 texts name the rule only;
  pipelines logs the class name of an exception in the write path, never `str(e)` of an SDK error and never the
  request; the BFF logs only the path on a failed fetch; Kong has no body logging. The contract suite proves it by
  grepping every service's logs for the test key (31.10).
- **The browser**: a password field (`type="password"`, `autoComplete="off"`, `spellCheck={false}`), its value in its
  own state, not in the provider form object, cleared after every answer except Could not check (kept for Save
  anyway, cleared on Cancel), never in a URL or browser storage. The assistant never reads it (it reads only the tab
  on Admin; plan 18).
- **Audit**: no audit store exists, so a log line in studio, without the key: `provider key <key_secret> saved
  (check ok|skipped) by <admin user id>`, `… refused …`, `… removed by …`; and pipelines `key file <key_secret>
  written|removed`. Enough to say who and when; an audit table waits for a second need.
- **Size**: Kong already limits bodies; the key is at most 1024 characters, the message under 4 KB.

### 31.8 The web form

As the Providers board, in the key area under Key file · In flight · Timeout (and under a server's optional Key
file when it is filled):

- **Missing** (writable): the plan-25 line (dot, Missing, path, Recheck), then an **API key** password field
  ("Paste the key") with **Save key** beside it, and one grey line "Checked with the provider, then saved to this
  file. Never shown again." Save key is greyed while the field is empty or the Base URL is neither the preset's nor
  the saved one (with Fetch's note).
- **Checking**: the field and the button greyed, the button reads "Checking…".
- **Refused**: a red note "the provider refused this key; not saved"; the field empties.
- **Could not check**: an amber note "could not check: <reason>" with **Save anyway**; the field keeps the key;
  saved this way, the line under Found reads "Saved without a check; tried on first use."
- **Found**: dot, Found, path, Recheck, then **Replace key** and **Remove key**. No field.
- **Replace open**: an empty **New API key** field, **Save key** and **Cancel**; "The old key stays until the new one
  is saved." The same check and answers.
- **Remove confirm**: "Remove deploy/secrets/providers/<name>? Calls to <provider> fail until a key is
  saved." with **Remove** and **Cancel**. After it: the Missing state (or Found, if a later folder still has a file of
  that name).
- **Not writable**: plan 25's Missing note ("Put the key in this file, then Recheck…"), no field, no Replace or
  Remove.
- The Info text becomes: "… A remote provider is https and needs the deployment's remote switch. Paste its key under
  Key file: it is checked with the provider, saved to that file on the server and never shown again."

### 31.9 Wording to change

- `web_service/components/admin/providers.tsx`: the Info text (31.8) and the Missing note (shown only when not
  writable).
- `contracts/README.md` Model sources, **Keys**: "No API takes or returns a key" becomes "One admin route takes a key
  (`PUT …/provider-keys/{key_secret}`, checked, then written by pipelines into the first key folder); no API returns
  one"; **Key file status** gains `writable` and the remove route.
- `contracts/messages/defs.schema.json` `providerSnapshot` description (31.6).
- `deploy/compose/pipelines.yml` comment ("the admin puts them in deploy/secrets/providers/" → "the admin pastes them
  in Admin › Model providers, or puts them there"), `pipelines_service/config.py` and `providers.py` docstrings,
  `.tools/make-secrets` comment.
- Plan 25 29.2 "Not in: typing a key in the browser (never)": a one-line pointer "superseded by plan 27" when this is
  built (plan 25 is being edited by another build today).
- `CLAUDE.md`: no change needed ("Secrets are files in `deploy/secrets/`" stays true). If wanted, add "(provider keys
  may also be written there by pipelines from Admin › Model providers)".

### 31.10 Tests

- `make contracts-check`: fixtures for `ProviderKeySave` (valid; both sources, neither, a `base_url`, a 1025-char key,
  a key with a space: invalid), `ProviderKeySaved`, `ProviderKey` with `writable`, the write message (save, remove,
  a name with `/` or a leading `.`: invalid).
- **pipelines** (pytest, the fake OpenAI server, `tmp_path` folders): a save writes only the first folder, mode 0600,
  the key exactly (trimmed), by temp and rename (no temp file left, also after a failed write); a bad name is refused;
  a symlink of that name is replaced, its target untouched; a folder of that name is `not_file`; a read-only first
  folder is `readonly` and `writable: false`; 401 and 403 write nothing (`refused`); a timeout and a 404 write nothing
  (`unreachable`); `force` writes without a call; the call goes only to the snapshot's URL; remove deletes only the
  first folder's file (a same-name file in the second stays: `found: true`); the key never appears in `caplog` at any
  level nor in any reply.
- **studio** (Go): admin only; the snapshot comes from the preset file or the stored provider (a stub pipelines
  records it); a body `base_url` is 422; 422 texts never contain the key; 409 when the reply says not writable; 503 on
  silence; the log line holds the name and the admin id, never the key.
- **web** (vitest): each state of 31.8; Save key greyed with a changed URL; the field empties after Refused and after
  a save; Save anyway sends `force`; Remove asks first.
- **Contract suite** (`helmstudio-test`): a remote provider at the fake (`https://fake-openai:8443/v1`), save a key
  under a new name: ok, Found; the fake scripted to answer 401: refused, Missing; scripted to hang: unreachable, then
  Save anyway: Found; remove: Missing; 403 for a user, 404, 409 never (the test folder is writable). Then
  `docker compose logs` of studio, pipelines, kong, web and nats must not contain the test key.
- **Dev-stack check** (browser): never the owner's `deepseek` file. A DeepSeek preset with Key file `deepseek-check`,
  a made-up key: Refused, nothing written. Then a local preset on a port nothing listens on, Key file
  `local-check`: Could not check, Save anyway, Found; Replace key; Remove key, Missing. At the end
  `deploy/secrets/providers/` holds only what it held before.

### 31.11 Phases

| Phase | What | Hours |
|---|---|---|
| 1 | Contracts: openapi 1.52 (two routes, `ProviderKeySave`, `ProviderKeySaved`, `writable`), the write message, the key reply's `writable`, defs wording, README, fixtures, nats.conf | 1.5 |
| 2 | pipelines: `write_key`, `remove_key`, `test` with a key in memory, the verdict, the responder, first-folder mkdir; tests | 2 |
| 3 | studio: the two handlers, the snapshot from preset or provider, the log lines, `writable`; tests | 1.5 |
| 4 | compose (`pipelines.yml`, `fake.yml`, `dev-remote.yml`), make-secrets chown, the wording (31.9) | 0.5 |
| 5 | web: the key area's states, the field's handling, the Info text; tests | 2 |
| 6 | Gates (`make test lint` per service, `make contracts-check`, `make test`) and the dev-stack check | 1.5 |
| | Total | 9 |

### 31.12 Open points

None for the owner: the four doubts are decided above. Small calls made in this design (say if any should change):
Save key is its own button (the key is not sent with Save provider, so a key can be saved for a preset before the
provider exists, and a failed check never blocks saving the provider); Save anyway writes without a second check;
anything other than 401 / 403 (including remote models off) counts as "could not check".
