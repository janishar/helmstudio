# helmstudio-next plan — Model provider presets: pick DeepSeek, do not type it

[← Plan index](../README.md)

Status: **decided by the owner (2026-11-23); built (2026-11-23)**, as-built notes in 29.13. API version: **1.51.0**
(main was at 1.50.0).

The owner's ask, 2026-11-23, after a Docker wipe made them type DeepSeek again in Admin › Model providers: "we should
also support predefined providers config like deepseek so i dont have to fill this manually for them".

Boards: [Providers](../mock/Providers.dc.html) (the Preset picker, a filled DeepSeek form, the key file found, Fetch
with its tick list; other states: key missing, fetch failed, the picker with remote off, a changed base URL) and
[AdminAgents](../mock/AdminAgents.dc.html) (one line: what Edit does, 29.9). Both stay 1440 × 960; canvas.json is
unchanged.

Facts come from a read on 2026-11-23 of `web_service/components/admin/providers.tsx` and `agents.tsx`,
`web_service/lib/providers.ts` (imported: `blankForm`, `formOf`, `putOf`), `pipelines_service/providers.py` and
`config.py`, `contracts/openapi.yaml` (the providers admin routes, `Provider`, `ProviderPut`, `ProviderTest`),
`contracts/messages/req.pipelines.providers.test.schema.json`, `contracts/README.md` (Model sources),
`deploy/compose/pipelines.yml`, `fake.yml`, `dev-remote.yml`, plans 18 and 21, and each provider's own docs (29.3).

## 29. Model provider presets

### Decided by the owner, 2026-11-23

1. **Presets: the common set.** Remote: DeepSeek, OpenAI, Anthropic, Google Gemini, OpenRouter, Groq, Mistral. Local:
   Ollama, LM Studio, llama-server.
2. **They live in a data file**, `contracts/providers/presets.json`, with a schema checked by `make contracts-check`.
   Adding a provider is a data change, no code.
3. **Models**: a preset fills a short default list; **Fetch** asks the provider's `/models` (server side, with the key
   file) and the admin ticks more.
4. **Key file status**: the form shows the key file's path (`deploy/secrets/providers/<name>`) and whether it is
   **found** (exists, not empty) or **missing**, with **Recheck**. The key is never read back, never sent to the
   browser, never logged.

### 29.1 What exists today

- **The form** (`providers.tsx` `ProviderPanel`): Kind (Local server · Remote), Name, Title, Base URL, Models (a
  textarea, the first is the default), then GPU host · Memory · Timeout for a server, Key file · In flight · Timeout
  for a remote; Enabled; Save provider. Everything is typed by hand. The Remote button is greyed while the
  deployment's switch is off.
- **The routes** (API 1.15, admin only): `GET /v1/studio/admin/providers` (`ProviderList`, with `remote_enabled`),
  `PUT …/{name}` (`ProviderPut`), `DELETE …/{name}`, `POST …/{name}/test[?model=]` (`ProviderTest`). **Test already
  lists models**: studio sends `pipelines.providers.test` with the saved provider's snapshot, and pipelines answers
  `GET <base_url>/models`, cut to the first 64 (`providers.test`, and `maxItems: 64` in the message schema). The web
  shows only "answered · 0.4 s".
- **Keys**: `key_secret` names a file in `PIPELINES_PROVIDER_KEYS_DIR`. Compose mounts `deploy/secrets/providers`
  read-only at `/run/secrets/providers` in **pipelines only** (`pipelines.yml`); studio never sees the folder.
  `providers.api_key` reads the file at call time; a missing file is the error "the key file <name> is missing",
  seen only when a call or Test runs. The owner's DeepSeek key is at `deploy/secrets/providers/deepseek` today (the
  Docker wipe kept it: it is a file, not a volume).
- **The dev overlay copies the keys at start** (`dev-remote.yml`: `cp /fake-keys/* /run/secrets/providers/*
  /tmp/provider-keys/` and `PIPELINES_PROVIDER_KEYS_DIR=/tmp/provider-keys`). On the dev stack a key file added
  after pipelines started is not seen until pipelines restarts. The real stack reads the mount live.
- **The host rule** (`providers.check_host`, `resolve`, `_Pinned`, at every call and every connection): a server
  must resolve only to loopback or private addresses (or, since the OrbStack fix, to what `host.docker.internal`
  resolves to in the container), never link-local; a remote must be https, needs
  `PIPELINES_REMOTE_MODELS` and must resolve only to public addresses; no userinfo, query or fragment; no redirects;
  no proxy from the environment. Studio checks literals at PUT.
- **Local servers from Docker**: pipelines runs in a container, so `127.0.0.1` there is the container itself. The
  form's placeholder and the `Provider` example already use `http://host.docker.internal:11434/v1`; on Docker
  Desktop (the owner's Mac) that name resolves to a private address and passes the server rule. No compose file sets
  `extra_hosts: host.docker.internal:host-gateway`, so on a Linux host that name does not resolve today (open point 3).
- **Calls as made today** (`providers._client`): `temperature=0` always; a schema goes as `response_format
  json_schema` with `strict: false`, and on a 400 once more as `json_object` (DeepSeek needs this).

### 29.2 What comes in, what does not

In: the presets file and its schema; three studio routes (list presets, fetch models from a preset, key file
status); one pipelines request (key file status); the Preset picker, the key status line and Fetch with a tick list
in the form; a per-provider `compat` for providers whose OpenAI layer refuses what we send (Anthropic; 29.4); the
Assistant row's Edit fix (29.9).

Not in: typing a key in the browser (never; superseded by [plan 27](27-provider-keys.md), API 1.52); presets for search providers (Brave stays typed; one provider); a
preset that updates saved providers (a preset only fills the form; a saved provider never changes on its own);
fetching from a URL the browser sends; Linux `host-gateway` (open point 3); model prices or context sizes.

### 29.3 The presets

Base URLs and model ids are from each provider's own docs, read 2026-11-23. Remote key file = the preset id. Local
presets need no key and ship with no models (Fetch fills them: what is pulled differs per machine).

| id | Kind | Title | Base URL | Default models (first = default) | Source |
|---|---|---|---|---|---|
| `deepseek` | remote | DeepSeek | `https://api.deepseek.com` | `deepseek-flash`, `deepseek-v4-pro` | [api-docs.deepseek.com](https://api-docs.deepseek.com/), [pricing](https://api-docs.deepseek.com/quick_start/pricing) |
| `openai` | remote | OpenAI | `https://api.openai.com/v1` | `gpt-6.2-sol`, `gpt-6-luna`, `gpt-6.2-astra` | [models](https://developers.openai.com/api/docs/models), [API overview](https://developers.openai.com/api/reference/overview) |
| `anthropic` | remote | Anthropic | `https://api.anthropic.com/v1/` | `claude-sonnet-5-5`, `claude-haiku-5-5`, `claude-opus-5-5` | [OpenAI SDK compatibility](https://platform.claude.com/docs/en/api/openai-sdk) |
| `gemini` | remote | Google Gemini | `https://generativelanguage.googleapis.com/v1beta/openai/` | `gemini-3.8-flash`, `gemini-3.5-flash-lite`, `gemini-4-neon` | [OpenAI compatibility](https://ai.google.dev/gemini-api/docs/openai), [models](https://ai.google.dev/gemini-api/docs/models) |
| `openrouter` | remote | OpenRouter | `https://openrouter.ai/api/v1` | `openai/gpt-6.2-sol`, `anthropic/claude-sonnet-5.5`, `google/gemini-4-neon`, `deepseek/deepseek-v4.1-flash` | [quickstart](https://openrouter.ai/docs/quickstart); ids from its public `GET /api/v1/models` |
| `groq` | remote | Groq | `https://api.groq.com/openai/v1` | `llama-3.3-70b-versatile`, `openai/gpt-oss-120b`, `openai/gpt-oss-20b` | [OpenAI compatibility](https://console.groq.com/docs/openai), [models](https://console.groq.com/docs/models) |
| `mistral` | remote | Mistral | `https://api.mistral.ai/v1` | `mistral-large-latest`, `mistral-small-latest` | [API reference](https://docs.mistral.ai/api) |
| `ollama` | server | Ollama | `http://host.docker.internal:11434/v1` | none (Fetch) | [OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility) |
| `lmstudio` | server | LM Studio | `http://host.docker.internal:1234/v1` | none (Fetch) | [OpenAI compatibility](https://lmstudio.ai/docs/developer/openai-compat) |
| `llama-server` | server | llama-server | `http://host.docker.internal:9931/v1` | none (Fetch) | [llama.cpp server README](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md) |

Notes per preset (each goes in the preset's `note`, shown under the picker):

- **DeepSeek**: the docs give the base URL without `/v1`. `deepseek-chat` (the board's old example, and maybe the
  owner's saved provider) is no longer listed; the legacy `deepseek-v4-flash` is still accepted but served by the
  Flash model. DeepSeek refuses `json_schema`; our `json_object` retry already handles it.
- **OpenAI**: the reference shows `https://api.openai.com/v1` in its examples.
- **Anthropic: partial.** The docs call the layer a way "to test and compare", not for production. Two of our habits
  are refused with a 400: `temperature` below 1 on Claude 4.7 and later (we always send 0), and `response_format`
  other than `json_schema` with `strict: true` (we send `strict: false`, then `json_object`). `tool_choice` forced is
  refused on the 5.5 models (we send `auto`: fine). So the preset carries `compat` (29.4); without it every call
  fails.
- **Google Gemini: partial (beta).** The docs say the layer is "still in beta". Thinking cannot be turned off on
  Gemini 3 and 4. `gemini-3.8-flash` and `gemini-3.5-flash-lite` are marked stable; `gemini-4-neon` has no label.
- **OpenRouter**: about 480 models in `/models`, so the tick list needs its filter. Ids carry the maker's prefix.
- **Groq**: `temperature` 0 is sent as 1e-8 (fine); `logprobs`, `logit_bias` and `n` > 1 are refused (we send none).
- **Mistral**: the reference shows the same chat shape at `/v1/chat/completions` and a models list; it does not use
  the words "OpenAI compatible". Ids are the `-latest` aliases its examples use.
- **Ollama**: listens on `localhost:11434`; ignores the key. **LM Studio**: examples use port 1234. **llama-server**:
  the default port is now **9931** (`--port`, "default: 9931"); many guides still start it with `--port 8080`, so
  the note says "change the port if you started it with --port". The key is optional (`--api-key`); a server
  provider never sends one today (open point 4).
- **Local from Docker**: the docs give `localhost`; the presets use `host.docker.internal`, as the form's
  placeholder does, because pipelines runs in a container (29.1). The server rule still applies at every call.

### 29.4 The data file

`contracts/providers/presets.json` (new folder `contracts/providers/`), checked by `presets.schema.json` beside it:

```json
{
  "presets": [
    {
      "id": "deepseek",
      "title": "DeepSeek",
      "kind": "remote",
      "base_url": "https://api.deepseek.com",
      "models": ["deepseek-flash", "deepseek-v4-pro"],
      "key_secret": "deepseek",
      "docs": "https://api-docs.deepseek.com/",
      "note": "Refuses json_schema; answers in JSON mode."
    },
    {
      "id": "anthropic",
      "title": "Anthropic",
      "kind": "remote",
      "base_url": "https://api.anthropic.com/v1/",
      "models": ["claude-sonnet-5-5", "claude-haiku-5-5", "claude-opus-5-5"],
      "key_secret": "anthropic",
      "compat": {"temperature": false, "strict_schema": true},
      "docs": "https://platform.claude.com/docs/en/api/openai-sdk",
      "note": "Partial: Claude's OpenAI layer is for testing."
    },
    {
      "id": "ollama",
      "title": "Ollama",
      "kind": "server",
      "base_url": "http://host.docker.internal:11434/v1",
      "models": [],
      "docs": "https://docs.ollama.com/api/openai-compatibility",
      "note": "Fetch lists the models you pulled."
    }
  ]
}
```

- **Schema** (`presets.schema.json`): `id` = the provider name pattern `^[a-z][a-z0-9-]{0,31}$`, unique; `title` ≤ 60;
  `kind` server | remote; `base_url` = `ProviderPut.base_url`'s pattern; `models` 0-16 of `ProviderPut`'s model
  pattern; `key_secret` = `ProviderPut`'s pattern, **required for remote, absent for server**; `compat` optional
  `{temperature: bool, strict_schema: bool}`; `docs` an https URL; `note` ≤ 120. `additionalProperties: false`
  everywhere. Order in the file = order in the picker.
- **check.py** also checks: each remote base_url is https and not a literal private or loopback address; each server
  base_url host is `host.docker.internal`, a loopback or a private literal; each preset as a body (`kind`,
  `base_url`, `models` or a placeholder model, `key_secret`) passes `ProviderPut`.
- **Who reads it**: studio only. `make gen` copies it into `studio_service/gen/providers/presets.json`, embedded in the
  binary (like `gen/tools/`). The web gets presets from the API, never from the file. Pipelines never needs it:
  studio sends it a provider snapshot as today.
- **`compat`** (the one code-shaped field; it exists because Anthropic's layer refuses what we send): `temperature:
  false` makes pipelines leave `temperature` out; `strict_schema: true` sends `json_schema` with `strict: true` and no
  `json_object` retry. It is copied into the saved provider (`Provider.compat`, `ProviderPut.compat`, the
  `providerSnapshot`), shown in the form as one line under Models ("Sends no temperature; strict JSON schema"), and
  editable only by choosing the preset again (no switches in the form). Absent = today's behaviour.

### 29.5 Contract changes (1.51.0)

- `openapi.yaml` (admin only, 403 otherwise; the `info.version` and the studio tag line get "from 1.51 provider
  presets…"):
  - `GET /v1/studio/admin/provider-presets` → `ProviderPresetList {remote_enabled, presets: [ProviderPreset]}`
    (`ProviderPreset` = the file's item).
  - `POST /v1/studio/admin/provider-presets/{id}/models` → `ProviderTest`. Body `ProviderFetch {key_secret?}`
    (`additionalProperties: false`, so a `base_url` in it is a 422). Studio builds the snapshot from **the preset's
    base_url and kind** (never the browser's), `key_secret` from the body or the preset, `timeout_s` 15, `compat` from
    the preset, and asks `pipelines.providers.test` without `model` (30 s, else 503). 404 unknown preset. A remote
    preset while the switch is off: 200, `ok: false`, `failure: off` (as Test).
  - A **saved** provider fetches through the existing `POST /v1/studio/admin/providers/{name}/test` without `?model`:
    no new route.
  - `GET /v1/studio/admin/provider-keys/{key_secret}` → `ProviderKey {key_secret, path, found}`; `path` is
    `deploy/secrets/providers/<key_secret>` (text for the admin; studio composes it, it never reads the folder).
    404 for a malformed name; 503 when pipelines does not answer in 5 s.
  - `Provider`, `ProviderPut` gain optional `compat` and optional `preset` (the id it was filled from; display only).
  - `ProviderTest.models` gains `maxItems: 1000` (it had none).
- `messages/req.pipelines.providers.key.schema.json` (new, request/reply, not enveloped): request `{key_secret}`,
  reply `{found: bool}` or `{error, code}`. Pipelines answers `found = file is a regular file and its size > 0`, by
  `stat` only: it never opens the file.
- `messages/req.pipelines.providers.test.schema.json`: reply `models` `maxItems` 64 → 1000.
- `messages/defs.schema.json` `providerSnapshot`: optional `compat`.
- `contracts/README.md`: the new rows in the file table, and Model sources gains a **Presets** bullet and the key
  status rule. `contracts/providers/` joins the file table.

### 29.6 The web form

As the Providers board:

- **Preset** first: a select with None, then **Local** (Ollama, LM Studio, llama-server) and **Remote** (the seven),
  in the file's order. Remote presets are greyed while `remote_enabled` is false (the board's "remote models off"
  state). Choosing one **fills** Kind, Name (= id), Title, Base URL, Models, Key file, and keeps `compat`; Host,
  Memory, In flight, Timeout and Enabled keep their values (a server's GPU host and memory are the admin's). Every
  field stays editable. The preset's note shows under the select as one grey line, with a "Docs" link. Choosing
  None clears nothing. Editing a saved provider shows its `preset` (if any) and does not refill unless picked again.
- **Name in use**: if a provider with that name exists, the form says "deepseek exists: Save replaces it" (Save is
  already a replace for the same name).
- **Fetch** beside the Models label. It sends the preset route while the Base URL still equals the preset's, the Test
  route (no model) while it equals the saved provider's, and otherwise is greyed with "Fetch asks only the preset's
  or the saved URL. Save first." While waiting: "Fetching…". Answer: a tick list under the textarea, "N found · tick
  to add", ticked = already in Models; ticking adds a line at the end, unticking removes it; at most 16 (the 17th tick
  is refused with "16 models at most"). Over 12 found, a filter box tops the list. Fetch on a remote preset sends the
  Key file field as `key_secret`.
- **Fetch failed**: the `ProviderTest.error` in a red note with **Retry**; the preset's models stay. `failure: off`
  reads "remote models are off on this deployment".
- **Key status line** (remote only), under Key file: a dot, **Found** or **Missing**, the path in mono, **Recheck**.
  It asks the key route when the Key file field settles (500 ms after typing stops), on Recheck, and after Save.
  Missing adds "Put the key in this file, then Recheck. The key is never shown here." A malformed name shows nothing.
  On the dev stack Missing also says "restart pipelines after adding a key" while open point 2 is open.
- Labels: Preset, Fetch, Recheck, Retry, Save provider (short, as the memory rule says).

### 29.7 Security

- **No URL from the browser is ever fetched.** The preset route takes only `key_secret`; studio takes the URL from
  the embedded file. The saved route uses the stored provider. So Fetch cannot be pointed at an arbitrary address,
  inside or outside.
- **The host rule and the switch still apply** in pipelines on every fetch: a remote preset needs
  `PIPELINES_REMOTE_MODELS`, https and public addresses; a local one loopback or private (or what `host.docker.internal`
  resolves to there), never link-local (so no cloud metadata). No redirects, no environment proxy, pinned connections, as Test.
- **Timeouts**: 15 s for the `/models` call (`timeout_s` of the snapshot), studio waits 30 s, the key check 5 s.
- **The key**: read by pipelines only, only to call the provider (`api_key`), never returned (`ProviderTest` has no
  field for it), never logged (errors name the file, never its content). The key check stats the file; it never
  opens it. The path shown is the deploy-side text, never a container path. `key_secret` keeps its pattern (no `/`,
  so no path outside the folder).
- **Who**: every new route is admin only (403), like the rest of Model providers.
- **Size**: models cut at 1000 ids of ≤ 128 chars.

### 29.8 Pipelines and studio changes

- **pipelines**: `providers.key_found(name)` (stat), the `pipelines.providers.key` responder beside the test one;
  `test` cuts models at 1000; `_client` and `chat` honour `compat` (no `temperature`; `strict: true` and no
  `json_object` retry).
- **studio**: embed `gen/providers/presets.json`; the three handlers; `compat` and `preset` stored on the provider
  (one migration: two nullable columns) and copied into the snapshot; PUT checks `compat` only by schema.
- **web**: `lib/providers.ts` `presetForm(preset, form)`, `tickModel(form, id)`; `providers.tsx` the picker, Fetch,
  the tick list, the key line; `gen/api.ts` regenerated.

### 29.9 The AI agents tab: Edit

The owner: the Assistant row's **Edit** does nothing. In the built app it is `<a href="#assistant-settings">`: it only
jumps to the Assistant box, which is always open right under the table, so nothing visible happens. Session names'
Edit is `<a href="#session-names">` to the panel beside, which is also always open; it seems to work only because the
panel is the one editor beside the table.

The approved board did not say: it drew the Assistant box open (with Cancel and Save, the shape of an editor that
opens) and the Session names row selected with its panel beside, but no closed state. So the board gets the smallest
change (done in this plan): the Assistant row is drawn selected too, and one line under the box says **"Until Edit
the Assistant box is closed. Edit opens it and marks the row; Cancel or Save closes it. Session names' Edit marks
its row and moves to the panel beside."**

Fix to build with this plan (web only, `agents.tsx`): both Edits become buttons. Assistant: the box is closed until
Edit; Edit opens it, marks the row (`aria-selected`, the board's row colour), scrolls to it and focuses its first
field; Cancel and Save close it and clear the mark. Session names: Edit marks its row and moves focus to the panel's
checked option. A test for each.

### 29.10 Tests

- `make contracts-check`: the presets file against its schema and check.py's rules (29.4); fixtures for
  `ProviderPresetList`, `ProviderFetch` (a body with `base_url` is invalid), `ProviderKey`, the key message (valid
  and invalid), a snapshot with `compat`.
- **pipelines** (pytest, the fake OpenAI server): key found, missing, empty, a folder of that name (missing), and
  `open` is never called; `test` returns more than 64 models; `compat` sends no `temperature` and `strict: true`, and
  a 400 is not retried as `json_object`; the host rule refuses a preset snapshot as today.
- **studio** (Go): presets list equals the embedded file, admin only; the preset route builds the snapshot from the
  file (a stub pipelines records it) and refuses a body `base_url` (422), unknown id 404; the key route 404 on a bad
  name, 503 on silence; `compat` and `preset` round-trip through PUT and GET.
- **web** (vitest): picking DeepSeek fills the form and keeps host and memory; Fetch greyed when the URL changed;
  ticks add and remove lines and stop at 16; the key line states; the fetch error and Retry; the Agents Edit fixes.
- **Contract suite** (`helmstudio-test`): shapes, 403 for a user, 404 and 422 cases, the key route against the fake
  key file (`fake-openai`: found; `nope`: missing). It never fetches a real preset: the test stack must never reach a
  real provider; the real fetch is the dev-stack check.
- **Dev-stack check** (browser): Admin › Model providers, preset DeepSeek, key found, Fetch, Save, Test answers;
  then the AI agents Edits.

### 29.11 Phases

| Phase | What | Hours |
|---|---|---|
| 1 | Contracts: presets file and schema, check.py rules, openapi 1.51, the key message, the test cap, `compat`, README, fixtures | 2.5 |
| 2 | pipelines: key responder, cap 1000, `compat`; tests | 1.5 |
| 3 | studio: embed, three routes, migration, snapshot; tests | 2.5 |
| 4 | web: picker, fill, Fetch and tick list, key line, errors; the Agents Edit fix; tests | 3 |
| 5 | Gates (`make test lint` per service, `make contracts-check`, `make test`) and the dev-stack check | 1.5 |
| | Total | 11 |

### 29.12 Open points

**Decided by the owner, 2026-11-23 (board approved; open points one by one):** 1. Build Anthropic's `compat`.
2. Dev key files are read live (two folders in `PIPELINES_PROVIDER_KEYS_DIR`), so Recheck works without a restart.
3. Add `extra_hosts: ["host.docker.internal:host-gateway"]` to pipelines now (not the recommended "later"), so a
local server on a Linux host is reachable. 4. A local-server provider may name a key file too (not the recommended
"leave it"), sent as a remote provider's is; still optional, never required.

1. **Anthropic's `compat`**: build it (recommended; without it the Anthropic preset cannot make a call) · ship the
   Anthropic preset with only its "partial" note and fix later.
2. **Dev key files**: let `PIPELINES_PROVIDER_KEYS_DIR` take two folders (`/run/secrets/providers:/fake-keys`) so the
   dev stack reads the mount live and Recheck works without a restart (recommended; a few lines in config.py and
   dev-remote.yml) · keep the copy and the "restart pipelines" hint.
3. **Local servers on a Linux host** (the GCP VM): add `extra_hosts: ["host.docker.internal:host-gateway"]` to
   pipelines in compose (resolves to the Docker bridge, private, passes the rule; the local server must listen on that
   bridge, not only loopback) · leave it until a local server runs on Linux (recommended: nothing runs there today).
4. **llama-server `--api-key`**: a server provider never sends a key today. Leave it (recommended) · let a server
   name a key file too.

### 29.13 As built (all phases; 2026-11-23)

- **Contracts (1.51.0).** `contracts/providers/presets.json` (10 presets, Local first, then Remote, as 29.3) and
  `presets.schema.json` (it `$ref`s `defs providerName`, `providerModel`, `providerCompat`); check.py `check_presets`:
  the schema, unique ids, the remote and server host rules, each preset as a `ProviderPut` body and as openapi
  `ProviderPreset`. openapi: the three routes, `ProviderPreset`, `ProviderPresetList`, `ProviderFetch`, `ProviderKey`,
  `ProviderCompat`; `Provider`/`ProviderPut` `compat` and `preset`; `ProviderTest.models` `maxItems: 1000`. Messages:
  `req.pipelines.providers.key` (new), the test reply cap 1000, `defs providerCompat` in the snapshot. Fixtures for all
  of these. `make gen` copies in studio (`gen/providers/`), pipelines, runner and web.
- **pipelines.** `PIPELINES_PROVIDER_KEYS_DIR` takes folders joined by `:` (`config.PROVIDER_KEYS_DIRS`, first that has
  the file); `key_found` stats only; the `pipelines.providers.key` responder; `compat` (no temperature; strict schema,
  no `json_object` retry); a server sends a key when it names one; `/models` kept to 1000. Tests: `tests/test_presets.py`.
- **studio.** Embedded presets; `ProviderPresets`, `FetchPresetModels` (snapshot from the file, `timeout_s` 15; remote
  with the switch off answers `off` without asking pipelines), `ProviderKey` (5 s); migration 00033 (`compat`,
  `preset`); a server may name `key_secret`. Tests: `TestProviderPresets`, `TestProviderPutShape`.
- **compose.** `extra_hosts: host.docker.internal:host-gateway` on pipelines; dev-remote.yml reads
  `/run/secrets/providers:/fake-keys` live (the copy at start is gone, so no "restart pipelines" hint); nats.conf grants
  `pipelines.providers.key`.
- **web.** As the board, with: the preset's note and a Docs link under the picker; "<name> exists: Save replaces it"
  when a preset names a saved provider; a server's optional "Key file (optional)" field under host, memory and timeout
  (open point 4; the board has none), with the key line when it is filled. AI agents: the Edits are buttons; the
  Assistant box mounts on Edit (so Cancel simply closes it), focus goes to its on switch.
- **Contract suite.** `contract_tests/tests/test_provider_presets.py`. The "remote off greys remote presets" case is a
  web test only (the test stack has remote on).
- **Found on the owner's Mac (OrbStack):** `host.docker.internal` resolves to `0.250.250.254` inside containers, with
  or without `host-gateway`; that is not a loopback or private address, so the host rule refused the local presets'
  base URL (422 at Save, `refused` at Fetch). **Fixed (owner's OK):** both host rules (studio `hostRule`, pipelines
  `resolve`) also allow, for a server, any address `host.docker.internal` resolves to in that container at check time
  (looked up only when an address fails the ranges), whatever name or literal reached it; never link-local. So
  OrbStack, Docker Desktop and Linux `host-gateway` all work, no range is widened, and a literal `0.250.250.254` is
  allowed only where that name resolves to it. studio.yml now maps `host.docker.internal:host-gateway` too, so studio
  resolves the name at Save on Linux. check.py's preset check is static (no DNS): a preset names
  `host.docker.internal`, which it already allows. Remotes are unchanged. Tests: studio `TestHostRuleDockerHost`,
  pipelines `test_a_server_may_live_at_what_host_docker_internal_resolves_to`.

