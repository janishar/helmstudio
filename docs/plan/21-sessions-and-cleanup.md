# helmstudio-next plan — Sessions and clean-up: rename, delete, auto-name; delete takes and failed runs

[← Plan index](../README.md)

Status: **design, for the owner to approve**. A plan, not in a milestone yet: it becomes one in
[08-milestones.md](08-milestones.md) when the owner schedules it. Nothing here is built. API target **1.43.0**
(main is at 1.41.0; branch `assistant` reserves 1.42.0).

The owner's asks, 2026-11-22:

- (a) "see if I can delete any creation even if failed";
- (b) "how rename the session?";
- (c) "if a llm model is available auto rename after first run if untitled", and "which model writes it: set this in
  ai agent setting screen".

Boards (drawn before any code, owner rule): [Main](../mock/Main.dc.html) (Create: the session menu, the auto-named
title, the take tile menu and its card, failed run tiles and Clear, in its "Other states") and the new
[AdminAgents](../mock/AdminAgents.dc.html) (Admin › AI agents). Short button text throughout.

Facts come from a read of the code on 2026-11-22: `web_service/components/create/create.tsx` (the session menu
`id="sessions"`, `TakeTile`, `RunTile`, `submit`), `web_service/lib/takes.ts` (`stripItems`),
`web_service/components/gallery/gallery.tsx` (the delete card), `web_service/app/(app)/admin/page.tsx`,
`studio_service` (`migrations/00002_studio.sql`, `api/studio/runs.go`, `service.go`, `judge_tests.go`),
`contracts/openapi.yaml` (sessions, runs, takes), `contracts/messages/` (`req.studio.runs.create`,
`evt.studio.run_finished`), `pipelines_service/providers.py`, `remote.py`, `runner_service/packs/llm` (`model.yaml`,
`workflows/text-llm-ask.json`), and plan 18 with its branch `assistant` (`/v1/studio/admin/assistant`).

## 25. Sessions and clean-up

### 25.1 What the code does today

- **Takes** can be deleted only in the Gallery: `DELETE /v1/studio/takes/{id}` (soft; the take leaves the gallery
  and Create at once, a sweep removes the object later). The card there reads "Delete this take?" / "It leaves the
  gallery and Create for good." Create's takes strip (`TakeTile`) has no menu and no delete.
- **Runs** cannot be deleted at all: there is no `DELETE` on `/v1/studio/runs/{id}`. The strip (`stripItems`) shows
  every run that did not succeed, so a failed or stopped run stays in its session for good. The only way out is to
  delete the whole session, which the web cannot do either.
- **Sessions**: `PATCH /v1/studio/sessions/{id}` (rename) and `DELETE` (409 while a run is queued or running or a
  batch is queued, running or paused; its takes stay with `session_id` null) both exist, but the web's session menu
  only switches sessions and has New session.
- **Titles**: New session makes "Untitled" (`SessionCreate` without a title). Generate with no session open makes
  one titled with the prompt's first 60 characters (the web sends it). `sessions` has no column saying who wrote the
  title, so a user's title and these two cannot be told apart.
- **Local LLM**: the `llm` pack (Qwen3.8 27B, MLX 4-bit, about 16 GB, Mac only) publishes the text workflow `llm-ask`;
  an ask run stores its `answer` on the run, never a take. Asks today are batch-lane runs made through
  `studio.runs.create` (judge, write, decide, agent turns) or studio's own interactive judge-test run.
- **Remote**: only pipelines_service calls a provider (`providers.chat_retried`: host rule, key files, failure
  classes), and only while the deployment's `REMOTE_MODELS` is on. Providers are set in Admin › Model providers.
- **Admin tabs** today: Users, Hosts, API keys, Model providers. Plan 18 puts the assistant's settings under Model
  providers (`GET/PUT /v1/studio/admin/assistant`, on branch `assistant`).

### 25.2 What comes in, what does not

In: delete a take from Create; delete a finished run (one, or every failed and stopped run of a session with Clear);
rename and delete a session from the session menu; auto-name an untitled session after its first run; the Admin
tab **AI agents** with the session-name setting (and, once `assistant` merges, the assistant's).

Not in: undo for deletes (the cards say "can't be undone"); renaming from the title chip (the menu is enough);
bulk select in the strip; naming from images or references (the prompt only); naming older sessions in a sweep;
other AI jobs in the AI agents tab (it is shaped to hold them, none is added).

### 25.3 (a) Delete takes and failed runs on Create

**Take tiles.** Each take tile in the strip gets a small ⋯ button (on hover and on the current tile; also the
context menu). Its menu holds **Delete**. Delete opens the card **"Delete this take?"** · "It leaves the gallery
and Create. This can't be undone." · Cancel / Delete. Delete calls the existing `DELETE /v1/studio/takes/{id}`; the
tile goes, the next tile is picked. Nothing new in the API.

**Run tiles.** A failed or stopped run tile gets a small ✕ in its corner (no card: a failed run holds nothing to
lose; its error is in the tile's title). When the session has any, a chip after the tiles reads **"2 failed ·
Clear"** (the count is failed plus stopped runs; its title says "1 failed, 1 stopped"); Clear removes them all
(no card, same reason). A running or queued tile has no ✕; its title says "Stop it first". Clear never touches a
running run.

**The rule (API).** `DELETE /v1/studio/runs/{run_id}` removes a **finished** run from its session:

- allowed when the run is `failed`, `cancelled` or `succeeded`; **409** `stop the run first` when `queued` or
  `running`; **409** `a batch's run goes with its batch` for a batch-lane run;
- soft: `runs.deleted_at` is set; the run leaves `GET …/sessions/{id}/runs`, `GET /runs/{id}` is 404, its events are
  left out of the session's replay; deleting again is 404;
- **its takes stay** (they are the Gallery's; `made_with` is a snapshot). So deleting a succeeded run changes
  nothing a user sees in the strip (the takes show, not the run) and only drops a run with text outputs or with
  every take deleted;
- the rows stay until their session is deleted (they are small; no sweep).

Clear is the web calling this for each run (at most 100 per session, as the gallery's bulk delete does). No bulk
endpoint.

### 25.4 (b) Rename and delete a session

The session menu (the title chip's ⌄) keeps New session and the list. Each row shows, on hover, focus and on the
current row, two small text buttons, **Rename** and **Delete** (as the workflow picker's Mine rows have).

- **Rename** turns the row into a text box with the title selected; Enter or Save saves (`PATCH`, trimmed, 1–200
  characters, blank keeps the old one), Esc cancels. A rename marks the title as the user's (25.5).
- **Delete** opens the card **"Delete this session?"** · "Its runs go. Its takes stay in the gallery. This can't
  be undone." · Cancel / Delete (existing `DELETE`). While a run of the session is queued or running, or a batch in
  it is active, the row shows "running" and its Delete is greyed with "Stop its runs first" (the 409 detail is
  shown if it races). The menu knows which sessions are busy from `GET /v1/studio/queue` (the caller's queued and
  running runs, every session; a naming run does not count); an active batch is caught by the 409. After a delete
  Create opens the newest other session, else the empty state.

### 25.5 (c) Auto-name an untitled session

**When.** A session whose title nobody typed (`title_source` `default` "Untitled", or `prompt`, the 60-character
slice; open point 1) gets a 2–5 word title once its **first run succeeds** (open point 2), if the admin turned
naming on. Once per session: studio marks it asked and never asks again, even if the answer fails. **A user's
title is never overwritten**: a rename sets `title_source` `user`, and the write that applies a name only matches
`default` or `prompt`, so a rename made while a name is on its way wins.

**What is sent.** The run's prompt only (the workflow's first text param, the one the web titles from today), cut
to 2000 characters, with `<imageN>` tags removed. Never images, references, other runs or the user's name. A run
with no prompt (an upload, a clone with no text) does not trigger naming; the next run with one does.

**Who does what** (contracts first; studio owns the API, RLS and settings; Python owns the models; outbox → CMD →
worker → EVT):

```
studio projector: run succeeded, session unnamed, naming on
   └─ outbox: cmd.session.name {session_id, owner_id, run_id, prompt, mode, provider snapshot | local}
pipelines_service names.py (durable consumer on CMD)
   ├─ remote: providers.chat_retried(provider, model) · system "a 2-5 word title" · schema {title} · max 24 tokens
   └─ local:  studio.runs.create {purpose: name} → a hidden llm-ask run (batch lane) → evt.studio.run_finished.answer
   └─ cleans the answer (2-5 words, ≤ 60 chars, one line, no quotes or markup) → outbox: evt.session.named
studio consumer: UPDATE sessions SET title, title_source 'model', named_by
                 WHERE id AND title_source IN ('default','prompt') → session event `session` on the session's SSE
```

- **studio_service**: `sessions` gains `title_source` (`default` | `prompt` | `user` | `model`; backfill: "Untitled"
  → `default`, others → `user`), `named_by` (the model's short name, e.g. `Qwen3.8`, `DeepSeek`) and `name_asked`.
  `SessionCreate` gains `from_prompt` (the web's Generate-with-no-session sets it, so the slice is stored as
  `prompt`). The projector writes `cmd.session.name` in the same transaction that makes the session's first
  succeeded run terminal. It keeps the setting (25.6) and sends the provider snapshot, as batches do.
- **The hidden naming run** (local only): `studio.runs.create` accepts `purpose: "name"` with no batch (owner and
  session from the session row, workflow `llm-ask`, idempotency key `name:<session_id>`). It runs in the batch lane
  (behind everyone's interactive runs, not counted in the 100-open-run cap), is left out of the session's runs, the
  strip, the Gallery and the session events, shows in the queue as "Session name", and does not block deleting its
  session (the delete cancels it). `evt.studio.run_finished` is sent for it with `batch_id` null.
- **pipelines_service** `names.py`: the prompt template (`name.v1`), the call, the answer checks. Remote goes
  through `providers.chat_retried` (host rule, key files, failure classes; network retries only). Local skips
  quietly when no live host can run `llm-ask` (not downloaded, host down). Any failure: the title stays, a log line,
  nothing shown to the user.
- **web_service**: on the `session` event it replaces the session's title. The first time a user sees a
  model-written title, a small hint under the title chip reads **"named by Qwen3.8"** (or "named by DeepSeek")
  and fades after a few seconds (remembered per browser, so it shows once). With Remote picked, the session menu
  ends with one line: "New sessions are named by DeepSeek from their first prompt" (open point 4).

### 25.6 Admin › AI agents

A new Admin tab **AI agents** (`/admin?tab=agents`, admins only; 403 otherwise) lists each AI job and its model,
one row each, in the Model providers style (a table, an editor beside it):

| Row | Model | Sends | Status |
|---|---|---|---|
| Assistant (plan 18) | on/off, provider and model, web search and its provider, caps (model calls and tokens per user per day, `look_at` per hour, messages per minute) | "sends your messages to DeepSeek" | from `/v1/studio/admin/assistant` |
| Session names | **Off** · **Local LLM** · **Remote** | "sends the prompt to DeepSeek" for Remote; nothing for Local | new |

- **Session names** options: **Off** (default, until the owner says otherwise). **Local LLM**: the runner's `llm`
  pack, Qwen3.8 27B on the local GPU queue; greyed with "Qwen3.8 27B not downloaded" when no host has it (a link to
  Models). **Remote**: a provider and model from Admin › Model providers ("DeepSeek · deepseek-chat"), with "sends
  the prompt to DeepSeek"; shown only while the deployment's remote switch is on (greyed "remote models are off on
  this deployment" otherwise). Save applies to sessions named from then on.
- **Assistant**: when branch `assistant` merges, its settings move here from Model providers (same API; only the web
  moves). Until then the tab shows the Session names row alone.
- The table shape (Job, Model, Sends, Status, Edit) is the place for later AI jobs; none is added now.

### 25.7 Contract changes (1.43.0)

- `openapi.yaml`:
  - `DELETE /v1/studio/runs/{run_id}` (204; 404 unknown, another user's or already deleted; 409 queued or running,
    or a batch run);
  - `StudioSession` gains `title_source` and `named_by` (null unless `model`); `SessionCreate` gains `from_prompt`;
    `PATCH` sets `title_source` `user`;
  - `SessionEvent` gains `session` (`{title, title_source, named_by}`), sent when a name is applied;
  - `GET/PUT /v1/studio/admin/session-names` (`SessionNames {mode: off|local|remote, provider, model,
    local_available, remote_enabled, updated_at}`; PUT 422 for a provider that is not an enabled remote or a model
    not in its models, 409 `remote models are off on this deployment`, 422 `Qwen3.8 27B is not downloaded` for
    local with no host);
  - `Run` gains `purpose` (null, or `name` for the hidden run; only `GET /v1/studio/queue` ever lists one).
- `messages/`: `cmd.session.name`, `evt.session.named` (new); `req.studio.runs.create` gains `purpose: name` without
  a batch; `evt.studio.run_finished` allows `batch_id` null for it.
- `contracts/README.md`: "Sessions: titles and naming", "Deleting runs".
- Services copy what they need with `make gen`; `make contracts-check`.

### 25.8 Safety

- **Owner scoping**: every new query runs under `WithUser` (row-level security); another user's run, take or
  session is **404**, never 403. The naming worker never takes the owner from a message alone: studio takes it from
  the session row when it creates the hidden run and when it applies the name.
- **Egress shown**: Remote is named in the admin row ("sends the prompt to DeepSeek"), in the session menu line and
  in the "named by DeepSeek" hint; Local sends nothing off the machine. Remote needs the deployment's switch, as
  every remote call does.
- **Model text is data**: the title is checked (2–5 words, ≤ 60 characters, one line, no markup) and shown as text;
  a prompt that tries to steer the namer can at most produce an odd title the user can rename.
- **Deletes** are soft, need a click, and a card where something is lost (a take, a session); a running run cannot
  be deleted.

### 25.9 Tests

- studio (Go, `.tools/test-db`): run delete per status (409 queued/running/batch, 204 finished, 404 twice and for
  another user's run), the run gone from lists, GET and replay, its takes kept; rename sets `user`; the name write
  skips a `user` title (rename raced with a name); once-per-session; the hidden run left out of runs, strip and
  Gallery, counted out of the open-run cap, and session delete with it queued; settings 403/409/422.
- pipelines (pytest, fake provider): the template, the answer cleaning (quotes, newlines, too long, empty), remote
  with remote off (`off`, never a connection), local via the fake `studio.runs.create`, a failed answer leaves the
  title.
- contract suite on `helmstudio-test`: the new paths and messages; another user's ids are 404.
- web (Vitest + the browser pass): take menu Delete and its card; ✕ and Clear (none on a running tile); session
  Rename (Enter, Esc, blank) and Delete (greyed while running); the `session` event swaps the title and the hint
  shows once; the AI agents tab, greyed Local and Remote states.

### 25.10 Phases

1. **Design**: this plan and the boards, approved by the owner (done when approved).
2. **Contract** 1.43.0 and the messages (2 h).
3. **studio**: run delete, `title_source`, settings, the naming CMD, the hidden run, the EVT apply, session events
   (8 h).
4. **pipelines**: `names.py`, remote and local (4 h).
5. **web**: take menu and card, run ✕ and Clear, session Rename and Delete, the title event and hint, Admin › AI
   agents (8 h).
6. **Gates**: each service's `make test lint`, `make contracts-check`, `make test` on `helmstudio-test`, the browser
   pass (2 h).

About 24 h. (a) and (b) can ship before (c): phases 2–3 and 5 without the naming parts.

### 25.11 Open points for the owner

Each with the recommended option first.

1. **Which titles the namer may replace**: "Untitled" and the 60-character prompt slice Generate writes (neither was
   typed) · only "Untitled".
2. **Which run triggers naming**: the session's first run that succeeds · the first run whatever its outcome.
3. **Local servers in the picker** (Ollama and the like from Model providers, no egress): list them under Remote's
   picker, marked "on this network" · only remote providers, as asked.
4. **Telling users before the prompt leaves** (Remote only): one line at the foot of the session menu · only the
   "named by DeepSeek" hint after the fact.
5. **Which runs can be deleted**: every finished run, its takes kept · only failed and stopped runs.
6. **What "N failed" counts**: failed and stopped runs together · failed only (stopped keep their ✕ alone).
7. **A card for run delete and Clear**: none (nothing is lost) · a card for Clear only.
8. **Turning naming on**: keep Off until you say · Local LLM where Qwen3.8 is downloaded.
9. **When `assistant` merges**: move its settings into AI agents in the same PR · leave them in Model providers
   until this plan is built.
