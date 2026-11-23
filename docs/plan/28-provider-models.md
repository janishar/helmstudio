# helmstudio-next plan — Provider models: a list from the API, a default, custom names, thinking per model

[← Plan index](../README.md)

Status: **decided by the owner (2026-11-24): layout A · Rows ("A - rows"), after seeing both drawn; built 2026-11-24 (32.12).** API version:
**1.53.0** (main is at 1.52.0, plan 27; if another plan takes 1.53.0 first, this takes the next free one at build
time). Not in a milestone, like plans 25 and 27.

The owner's ask, 2026-11-24, on Admin › Model providers, pointing at the Models text box: "better ui for this, list
from the api, select default, add custom name text". The doubts were asked one by one (below).

Board: [ProvidersModels](../mock/ProvidersModels.dc.html) (new, page models-admin, 1440 × 1080): both layouts side by
side, each with DeepSeek's fetched list, the default, a custom name, thinking set on one model and not offered on
another, Loading and Fetch failed. The Providers board is unchanged (it stays the approved form around this box).

Facts come from a read on 2026-11-24 of plans 25 and 27 (as built), `contracts/openapi.yaml` (`Provider`,
`ProviderPut`, `ProviderTest`, `ProviderPreset`, `ModelSourceList`), `contracts/providers/presets.json`,
`contracts/messages/defs.schema.json` (`providerSnapshot`, `modelSource`, `providerModel`),
`pipelines_service/providers.py` (`_client`, `chat`), `server.py`, `names.py`, `assistant.py`, `agents.py`,
`studio_service/api/studio/providers.go` (`providerRow`, `snapshot`), `cli/director.go`,
`web_service/components/admin/providers.tsx`, the Providers and AdminAgents boards, and each provider's docs (32.3).

## 32. Provider models

### Decided by the owner, 2026-11-24

1. **Layout: both options drawn** for the owner to pick (32.7): **A · Rows** (one row per model: a tick to use it,
   the id, a Default radio, × for a custom one; a filter when the list is long; "+ Add model" for a custom name) and
   **B · Chips** (chosen models as chips, a star on the default; a searchable dropdown of fetched models to add; a
   custom name typed into the same dropdown). Both have thinking mode.
2. **Thinking: per model.** Each model gets **Thinking: Off / Low / Medium / High**, shown only for models that
   support it (only the levels that model has; a model with only on and off shows **On / Off**). The Assistant,
   Session names and pipeline nodes use that model's setting.
3. **Fetch: automatic** when the key shows Found, and on opening a saved provider. A **Refresh** button loads again.
   Without a key the preset's list shows.

### 32.1 What exists today

- **The box** (`providers.tsx`): a `TextArea` `#p-models`, one id per line, "Models (the first is the default)";
  a **Fetch** chip asks the preset's route (`POST …/provider-presets/{id}/models`) or the saved provider's Test;
  the answer shows under it as a tick list (filter past 12, "N found · tick to add"; ticks add or remove lines, stop
  at 16). Fetch is greyed when the URL is not the preset's or the saved one. Errors show with Retry and "The preset's
  models stay in the list".
- **The contract**: `Provider.models` and `ProviderPut.models` are lists of strings (1-16, unique, pattern
  `^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`); `default_model` is one of them (PUT: absent = the first).
  `ProviderTest.models` is up to 1000 strings. `ModelSourceList` (the editor's "Runs on") gives `models` and
  `default_model` per provider. `ProviderPreset.models` is strings.
- **Readers of the string list**: `cli/director.go` decodes `models []string` and `default_model`; studio's
  `providerRow.Models []string` (column `models`), `ruleEnv`, `names.go`, `assistant.go`, `pipeline_rules.go`. So
  the list must stay strings on the wire (32.4).
- **Calls**: every chat call goes through `providers.chat(name, p, req)` with the snapshot `p` and `req.model`
  (assistant, summaries, names, server-ask runs, agent nodes, judge tests). `_client` sends `max_tokens` (remote) or
  `extra_body.max_tokens` (server), `temperature` 0 unless `compat.temperature` is false. Nothing about thinking is
  sent today, so **each provider's default applies**: DeepSeek thinks (high) by default, Claude 5 models think by
  default, Gemini 3 and 4 always think, OpenAI GPT-6 models reason at medium.
- **Small output allowances**: names `MAX_TOKENS = 24`, agents 800, assistant 1200, summary 300, server-ask 800.
  Reasoning tokens count inside the output allowance on every provider that reports them, so a thinking model can
  spend the whole allowance thinking and answer nothing. A likely cause of empty or failed titles on DeepSeek today
  (not checked on the dev stack; the build's first step checks it).
- **The Agents tab** (AdminAgents board, `agents.tsx`): Provider and Model selects for the Assistant and Session
  names; the model option reads `deepseek-chat (default)`.

### 32.2 What comes in, what does not

In: the new Models area (one of A or B), auto Fetch and Refresh, Default, custom names, a thinking level per model,
the thinking rules in `presets.json`, the wire mapping in pipelines with a safe fallback, the extra output allowance,
the level shown where a model is picked. Out: a thinking level per job (owner: per model), showing the model's
reasoning text, reading capability from Ollama's `/api/show` (32.3, local servers), the 16-model cap (unchanged).

### 32.3 Thinking per provider (research, 2026-11-24)

How each preset's OpenAI-compatible layer turns thinking on and sets its level. "Shown" is what the Models area offers.

| Preset | Models (preset list) | Wire | Shown | Default when nothing is sent | Source |
|---|---|---|---|---|---|
| DeepSeek | `deepseek-flash`, `deepseek-v4-pro` (both: thinking and non-thinking) | `thinking: {type: enabled \| disabled}` in the body; `reasoning_effort` `low` \| `high` \| `max` (medium and xhigh map to high) | Off / Low / High | thinking on, effort high | [Thinking mode](https://api-docs.deepseek.com/guides/thinking_mode), [pricing](https://api-docs.deepseek.com/quick_start/pricing) |
| OpenAI | `gpt-6.2-sol`, `gpt-6-luna`, `gpt-6.2-astra` (all reasoning models) | `reasoning_effort`; values per model; `none` is refused (400) on GPT-6 Astra, 6.2 Astra, 6.2 Sol | Low / Medium / High | medium | [Reasoning models](https://developers.openai.com/api/docs/guides/reasoning?api-mode=chat) |
| Anthropic | `claude-sonnet-5-5`, `claude-haiku-5-5`, `claude-opus-5-5` | `reasoning_effort` is ignored; `thinking: {type: enabled, budget_tokens: N}` (N ≥ 1024 and below `max_tokens`); on Claude 5 thinking is on by default, models that reject `enabled` get adaptive thinking and the budget has no effect; `adaptive` is a 400; no documented off in this layer | "Always thinks" (no control) | on | [OpenAI SDK compatibility](https://platform.claude.com/docs/en/api/openai-sdk) (Thinking support) |
| Google Gemini | `gemini-3.8-flash`, `gemini-3.5-flash-lite`, `gemini-4-neon` | `reasoning_effort` `minimal` \| `low` \| `medium` \| `high` (`xhigh` only gemini-4-neon); maps to `thinking_level` on Gemini 3 and 4; **cannot be turned off** on Gemini 2.5 Pro, 3 and 4 (`none` only on other 2.5 models) | Low / Medium / High | the model's own level | [OpenAI compatibility](https://aistudio.google.com/docs/openai) |
| OpenRouter | about 480 models | `reasoning: {effort: none \| minimal \| low \| medium \| high \| xhigh \| max}` (or `max_tokens`); its `/models` gives each model a `reasoning` object: `supported_efforts`, `default_effort`, `mandatory` (true: cannot be off) | from `/models`: the efforts it lists among low / medium / high, plus Off unless mandatory | per model `default_effort` | [Reasoning tokens](https://openrouter.ai/docs/guides/best-practices/reasoning-tokens) |
| Groq | `llama-3.3-70b-versatile` (no reasoning), `openai/gpt-oss-120b`, `openai/gpt-oss-20b` | `reasoning_effort` `low` \| `medium` \| `high` on GPT-OSS (cannot be off); Qwen 3.8 27B also `none`, and needs `reasoning_format: parsed \| hidden` with JSON mode or tools (`raw`, the default, is a 400) | GPT-OSS: Low / Medium / High; Qwen: Off / Low / Medium / High; Llama: none | GPT-OSS medium | [Reasoning](https://console.groq.com/docs/reasoning) |
| Mistral | `mistral-large-latest`, `mistral-small-latest` | `reasoning_effort`: Large 4 (`mistral-large-4-0`) `low` \| `medium` \| `high`, no `none`; Small 4 (`mistral-small-2603`) `none` \| `high` | Large: Low / Medium / High; Small: On / Off | Large 4: high | [Adjustable reasoning](https://docs.mistral.ai/capabilities/reasoning/adjustable) |
| Ollama | fetched | `reasoning_effort` on `/v1/chat/completions`; per model; on/off-only models map `none` to off and any effort to on; GPT-OSS cannot be off; capability is in `/api/show`, not `/v1/models` | On / Off for known thinking families by id (`qwen3`, `deepseek-r1`, `magistral`); GPT-OSS Low / Medium / High; others none | the model's | [OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility) |
| LM Studio | fetched | not in its chat completions parameter list | none | the model's | [Chat completions](https://lmstudio.ai/docs/developer/openai-compat/chat-completions) |
| llama-server | fetched | server flags only in the README: `--reasoning on\|off\|auto`, `--reasoning-effort`, `--reasoning-budget`, `--chat-template-kwargs` | none (set at start) | `auto` | [server README](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md) |

Notes:

- **Mistral's `-latest` aliases**: its page does not say which models they point to. The rules match the dated ids
  and, as an assumption, the aliases; the fallback (32.5) covers a wrong guess, and the build's dev-stack check runs
  Test with a level set.
- **DeepSeek with tools**: once tools are used in thinking mode, later requests must send back `reasoning_content`
  or get a 400. Our calls are one system and one user message each (`chat`), never a replayed history, so this does
  not bite; the plan keeps it that way.
- **What our levels mean** where a provider has more: Low = its lowest real effort above off, High = its `high`
  (never `max` or `xhigh`: cost). DeepSeek has no medium, so it shows Off / Low / High.

### 32.4 The data

**In `presets.json`** (checked by `presets.schema.json` and `check.py`): each preset may have `thinking`, a list of
rules; the first rule whose `match` (a regular expression on the model id) fits gives that model's levels. Each level
is the body fragment pipelines adds. A rule with `always: true` and no levels means "always thinks". Top-level
`thinking_tokens` is the extra output allowance per level (32.5).

```json
{
  "thinking_tokens": {"on": 4096, "low": 1024, "medium": 4096, "high": 8192},
  "presets": [
    {
      "id": "deepseek",
      "thinking": [
        {"match": "^deepseek-(flash|v4-pro|v4-flash)$", "default": "high",
         "levels": {
           "off":  {"thinking": {"type": "disabled"}},
           "low":  {"thinking": {"type": "enabled"}, "reasoning_effort": "low"},
           "high": {"thinking": {"type": "enabled"}, "reasoning_effort": "high"}}}
      ]
    },
    {"id": "anthropic", "thinking": [{"match": "^claude-[a-z]+-5", "always": true}]},
    {"id": "openrouter", "thinking": "models"}
  ]
}
```

- Level names: `off`, `on`, `low`, `medium`, `high`; a rule has `off` + `on`, or some of `off`, `low`, `medium`,
  `high`. Fragment keys are limited by the schema to `reasoning_effort`, `thinking`, `reasoning`,
  `reasoning_format` (no other body field can be set from the file).
- `"thinking": "models"` (OpenRouter): levels come from its `/models` `reasoning` object, read by Fetch (below),
  mapped to `reasoning: {effort: …}` and, unless `mandatory`, off = `reasoning: {effort: "none"}`.
- A preset without `thinking` (LM Studio, llama-server) and a provider with no preset offer no thinking control.
  `Provider.preset` stops being display only: it names the rules (studio reads the embedded file).

**In the API (1.53.0), back-compatible**: `models` stays a list of strings and `default_model` stays, because the CLI
and studio read them as strings (32.1). New fields:

- `Provider.thinking` and `ProviderPut.thinking`: an object `{<model id>: level}` (each key one of `models`; each
  level one the model's rule allows, else 422 naming the model; absent or `{}` = nothing sent, the provider's
  default). A level for a model with no rule is a 422.
- `Provider.thinking_levels` (read only): `{<model id>: {levels: [...], always?: true, default?: level}}` for each
  saved model that has a rule, so the form and the Agents tab need no copy of the rules.
- `ProviderTest.thinking` (optional): the same shape for the fetched models, filled from the rules or, for
  `"models"`, from the provider's `/models` answer (only `supported_efforts` and `mandatory` are read; anything else
  ignored). Capped like `models`.
- `ModelSourceList` providers gain `thinking` (`{<model>: level}`), so "Runs on" and the Agents tab can show it.
- `ProviderPreset` gains `thinking` (the rules, as in the file).
- A saved provider from before 1.53 reads as today: strings, no `thinking`, nothing sent.

The UI works on one list of `{id, default, thinking, custom}` per model; it saves `models` (order kept, default
first is not needed: `default_model` names it), `default_model` and `thinking`.

**Storage** (studio): a new `thinking jsonb` column on `providers` (default `{}`), no change to `models` or
`default_model`; no data migration.

**The snapshot** (`defs providerSnapshot`) gains `thinking`: `{<model id>: {level, body, tokens}}`, resolved by
studio at Submit from the saved level and the embedded rules (`body` the fragment, `tokens` from
`thinking_tokens`; an always-thinks model gets `body {}` and the `on` tokens). Pipelines never reads `presets.json`.

### 32.5 Calls (pipelines)

- `chat`: if `p.thinking[req.model]` exists, its `body` is merged into `extra_body` and its `tokens` are added to
  `max_tokens` (so a 24-token title still has room after thinking). Agent loops keep their caps: reasoning tokens are
  in `tokens_out`, so per-user token caps count them.
- **Anthropic budget**: an always-thinks model sends nothing extra (thinking is on by default); only `max_tokens`
  grows. If a later rule sends `budget_tokens`, it must stay below `max_tokens` (check.py checks each rule's budget
  against `thinking_tokens`).
- **Safe fallback**: a 400 on a call that carried a fragment is asked once more without it; the pair (provider,
  model) is remembered in the process (like `_NO_JSON_SCHEMA`) and a log line says "thinking setting refused, sent
  without it". Any other failure is classed as today.
- **Groq Qwen**: its fragments carry `reasoning_format: hidden`, so JSON mode and tools work.
- Local servers get the same merge (server-ask runs call `providers.chat` too).

### 32.6 Fetch, Default, custom names (both layouts)

- **Auto Fetch** when the key line turns Found (after Save key or Recheck), when a preset is picked and its key is
  already Found, and when a saved provider opens. A local preset (no key) fetches on pick. While loading, the
  preset's or saved list shows with "Loading models…"; **Refresh** reloads. No key: the preset's list with "Add the
  key to load the full list". Fetch failed: the error line with Retry; the list keeps what it had.
- **Fetched models are not all added**: the preset's models (or the saved ones) start chosen; others are offered.
  The 16 cap stays: a 17th says "16 at most".
- **Default**: exactly one; the first chosen at start; removing the default moves it to the next chosen.
- **Custom name**: the same pattern as today (422 text if not); shown with a "custom" tag and ×; it gets the rule
  of its id if one matches (a custom `deepseek-v4-flash` gets DeepSeek's levels), else no thinking.
- **Thinking**: shown only for chosen models with levels; the control shows that model's levels only; it starts at
  the rule's `default` (marked as the provider's default) or Off where the rule has no default; always-thinks shows
  "Always thinks".
- **Where a model is picked** (Agents tab Model select, the editor's Runs on): the option reads
  `deepseek-flash · Thinking High` (nothing when not set); no control there (per model, owner).

### 32.7 The two layouts (the open point)

**A · Rows.** Under "Models" and Refresh, one row per model: tick, id (mono), "Default" radio on the right; under a
chosen thinking model a small segmented Off / Low / High. Custom rows carry × . A filter shows past 12. "+ Add model"
opens a field and Add.

- Pros: everything visible at once, one click per change, the same tick list as today (small change to code and
  tests), works the same for 3 or 480 models with the filter.
- Cons: tall with many fetched models (scrolls inside 260 px); chosen and not chosen mix (chosen sort first).

**B · Chips.** Chosen models as chips (`★ deepseek-flash · High`); a chip opens a small panel: Make default,
Thinking, Remove. Below, one search box: typing filters the fetched list in a dropdown; Enter on a name not in the
list adds it as custom ("Add "…" as custom").

- Pros: short (the chosen few are all that shows), clean for OpenRouter's 480, one box for fetched and custom.
- Cons: thinking and default sit one click deeper (in the chip's panel); a combobox and popover need more keyboard
  and screen reader work; a bigger change to code and tests.

**Recommendation: A · Rows.** The form already uses a tick list the owner approved, every setting shows without
opening anything, and it is the smaller build. B is better only if most providers have long lists.

### 32.8 Cost and latency

Thinking spends output tokens and time: High can add thousands of tokens and many seconds per call; Off is cheapest.
The extra allowance (`thinking_tokens`) is a ceiling, not a charge. Session names make one tiny call per session:
pick a model with Off for them (the Agents tab hint says the level). Today DeepSeek already thinks at high on every
call because nothing is sent (32.1); setting Off on `deepseek-flash` is the cheapest change this plan gives.

### 32.9 Tests

- `make contracts-check`: presets with `thinking` (valid, a bad key in a fragment, a bad level, a regex that does not
  compile, a budget above `thinking_tokens`); fixtures for `Provider` and `ProviderPut` with and without `thinking`
  (a level for a model not in `models` is invalid), `ProviderTest.thinking`, a snapshot with `thinking`.
- **pipelines**: the fragment merged and `max_tokens` grown (fake OpenAI server records the body); a 400 with a
  fragment is asked again without it, once, and remembered; no `thinking` sends what is sent today; Test reads an
  OpenRouter-shaped `reasoning` object.
- **studio**: PUT with `thinking` (ok, a level the rule refuses, a model without a rule: 422), strings-only PUT as
  today, GET of a pre-1.53 row, `thinking_levels`, the snapshot resolved from the rule, ModelSourceList `thinking`.
- **web** (vitest): auto Fetch on Found and on open, Refresh, loading and failed states, Default moves on remove,
  custom add and ×, thinking shown only where levels exist, Save sends `models`, `default_model`, `thinking`.
- **Contract suite** (`helmstudio-test`): the shapes and 422s; never a real provider.
- **Dev-stack check**: DeepSeek, Fetch, set `deepseek-flash` Off, Test answers; Session names makes a title.

### 32.10 Phases

| Phase | What | Hours |
|---|---|---|
| 0 | Check on the dev stack whether names fail today with DeepSeek thinking (32.1) | 0.25 |
| 1 | Contracts: presets `thinking` and `thinking_tokens`, schema and check.py, openapi 1.53 (`thinking`, `thinking_levels`, `ProviderTest.thinking`, ModelSourceList), snapshot `thinking`, README, fixtures | 2 |
| 2 | pipelines: the merge, the extra tokens, the fallback, Test's `reasoning` read; tests | 1.5 |
| 3 | studio: column, PUT checks, GET fields, snapshot, ModelSourceList; tests | 2 |
| 4 | web: the Models area (the picked layout), auto Fetch and Refresh, the Agents tab hint; tests (B: +2) | 3 |
| 5 | Gates and the dev-stack check | 1.5 |
| | Total (A) | 10.25 |

### 32.11 Open points

**Decided by the owner, 2026-11-24:** layout **A · Rows**. B · Chips stays on the board for the record only; it is not built.

1. **Layout: A · Rows or B · Chips** (32.7; recommended A). The owner's pick.

Small calls made in this design (say if any should change): the API keeps `models` as strings and adds a
`thinking` map (the CLI reads strings); our High is the provider's `high`, never `max`; Claude 5 shows "Always
thinks" (the layer documents no off); LM Studio and llama-server get no control; the Mistral aliases are assumed to
be the 4 models until a Test says otherwise.

### 32.12 As built (layout A, all phases; 2026-11-24)

- **Phase 0 (finding).** The dev stack has no provider and no session-names setting saved, so nothing could be seen
  there; from the code and DeepSeek's docs: nothing about thinking was sent, so DeepSeek thought at high on every
  call, and its reasoning counts inside `max_tokens`. Session names ask with 24 tokens (and Test with 8): the
  thinking spends them, the answer comes back empty or cut, `names.clean` refuses it and the session stays untitled
  (log `the answer is not a 2-5 word title`). Fixed two ways: a level set per model is sent (Off for names is the
  cheapest), and every call to a model a rule covers gets that level's `thinking_tokens` added to `max_tokens`. A
  model with **nothing saved** whose rule has a `default` (DeepSeek high, OpenAI medium, Mistral Large high) or that
  always thinks (Claude 5) gets `body {}` and that level's tokens: nothing new is sent, but the call has room. So a
  DeepSeek provider saved before 1.53 names sessions again without being opened.
- **Contracts** (API 1.53.0): `presets.json` `thinking_tokens` and per-preset `thinking` (rules or `"models"`); the
  schema limits a fragment to `reasoning_effort`, `thinking`, `reasoning`, `reasoning_format`; check.py checks the
  rules (compiles, no lookaround or backreference, default among levels, on only with off, a `budget_tokens` below its
  level's tokens) and runs eight bad rules through the same check to prove each is refused. openapi: `Provider.thinking`
  and `thinking_levels`, `ProviderPut.thinking`, `ProviderTest.thinking`, `ProviderPreset.thinking`, a model source's
  `thinking`; messages: `defs providerThinking` in the snapshot, `reasoning` in the test reply. README: Model sources,
  Thinking.
- **Rules shipped**: DeepSeek flash / v4-pro / v4-flash Off, Low, High (default High); OpenAI `gpt-6*` Low, Medium,
  High (default Medium); Anthropic Claude 5 always thinks; Gemini 3 and 4 Low, Medium, High (no default); OpenRouter
  `"models"`; Groq GPT-OSS Low, Medium, High (default Medium), Qwen 3 Off to High with `reasoning_format: hidden`;
  Mistral Large (`-latest` or 4) Low, Medium, High (default High), Small (`-latest` or 2603) Off / On; Ollama `qwen3`,
  `deepseek-r1`, `magistral` Off / On (on = effort medium), `gpt-oss` Low, Medium, High. LM Studio and llama-server none.
- **Studio**: migration 00034 `providers.thinking jsonb NOT NULL DEFAULT '{}'`; PUT 422 texts `thinking: <m> is not
  one of models`, `has no thinking setting`, `takes <levels>`; the snapshot (`providerRow.snapshot`, so Submit, Run
  again, assistant, names and Test alike) carries `thinking`; Test and a preset's Fetch answer `thinking`
  (OpenRouter's from the reply's `reasoning`: its efforts among low, medium, high, plus off unless mandatory; fewer
  than two levels: none).
- **Pipelines**: `chat` merges the fragment into the body (`extra_body`, for remote and server) and adds the tokens; a
  400 tries the json_object fallback first (DeepSeek refuses json_schema, so the fragment is not blamed for it), then
  once without the fragment, remembered per (provider, model) in `_NO_THINKING`, log `thinking setting refused, sent
  without it`. Test reads a `/models` entry's `reasoning` (only `supported_efforts` and `mandatory`).
- **Web** (layout A): rows chosen first (tick, mono id, custom tag and ×, Default radio), under a chosen model the
  Thinking segment of its own levels with "provider default", "Always thinks" or "No thinking setting"; a filter past
  12 rows with "N found · M match · chosen first"; Model id + Add (Enter adds, never saves); Refresh; auto Fetch once
  per provider and key when it can be asked (key Found, a local preset on pick, a saved one on Edit); Loading models…
  with Refresh greyed and two skeleton bars; Fetch failed with Retry and "The list keeps what it had."; No key: "Add the
  key to load the full list." with the preset's models ticked. Save sends `models`, `default_model` and the level
  each chosen model shows (picked, else the rule's default, else Off). Enable/Disable keeps the saved thinking as is.
  The AI agents Model selects read `deepseek-flash · Thinking Off (default)` with "Set per model in Model providers.";
  the editor's Runs on options carry the level too.
- **Deviations**: (1) the default's room above (the plan said absent = nothing sent; still true for the body, the
  tokens are new). (2) OpenRouter's levels are not stored, so a saved OpenRouter provider's `thinking_levels` offer
  all four until Fetch answers, and PUT accepts any of off, low, medium, high for it. (3) A row is "custom" when it is
  not in the fetched list (or, before a fetch, the preset's list): a saved model the provider no longer lists shows as
  custom. (4) Gemini (no default, no Off) starts with nothing picked and sends nothing.

