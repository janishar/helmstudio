# helmstudio-next plan — The in-app assistant

[← Plan index](../README.md)

Status: board v72 approved by the owner on 2026-11-22 ("Approve"); building on branch `assistant`; `look_at` gets a
card (decision 2). Before that: a plan, not in a milestone yet: it becomes one in [08-milestones.md](08-milestones.md) when the owner says to
build it (owner, 2026-11-21: "plan with me how we can power the whole web features with ai agents in the browser
ui"). Decided with the owner the same day: it **acts, and confirms what costs or lasts**; phase 1 covers
**navigation, Create, Graph and Pipelines, the Timeline and the Gallery**; the **admin picks the model** (DeepSeek
today); it **can look at takes from the start**, through the local ask stage when Qwen Image is installed. Facts come
from a read of the code (web screens, the director agent, the ask stage) and an adversarial review, 2026-11-21, and a
coverage audit of every built feature, 2026-11-22.

**Decided by the owner, 2026-11-22** (the owner, the same day: "also use langraph; give tools and memory to this ai
assistant"):

1. **Chaining**: a screen action `await` (run, job, download, export, clone) waits on the tab's own live state and
   its result resumes the turn, so "make an image then animate it" is one ask. Each costly step still needs its click.
2. **Cards for every costly, deleting or data-leaving action**, one layout, with "uses the GPU", "sends to
   DeepSeek" (or the search provider) or "can't be undone". Yes calls the screen's own button.
3. **Downloads**: an admin gets the Models row and a card (size, machine, licence); the licence tick and Download
   stay the admin's clicks; then `await`, then back to Create (which reads `/models` again when it mounts). A non-admin is told to ask an admin.
4. **Run on**: it may pick Auto, Mac or NVIDIA among the rows the form allows, with a reason. Every user gets the
   hosts read (up, busy, memory, queue; no pins or settings). The Generate card names the machine and place in line.
5. **Library** (looks, elements, voices, clone and design): it opens the screen's own dialog prefilled; the user's
   Save (and the voice rights tick) is the click; the action resolves on close with the new id and `@handle`, or
   "cancelled".
6. **Always the user's**: files to or from disk, sign in and out, password, theme, admin writes (users, API keys,
   providers, host pins and settings) and messages to a pipeline's director chat. It explains and navigates only.
7. **LangGraph**: the assistant is a LangGraph agent in pipelines_service, on the existing Postgres checkpointer, one
   thread per chat; screen actions and `await` are `interrupt()`s the browser's results resume. Studio keeps the
   thread rows, row-level security, SSE and roles.
8. **Memory across chats**, per user, in the existing LangGraph Postgres store: preferences, project facts, what
   worked, past-chat summaries. It saves on its own with a "Remembered: …" line and Undo; a Memory list shows, edits
   and deletes everything.
9. **Tools**: one tool catalog shared with `helm mcp` ([16-mcp.md](16-mcp.md)); `look_at` kept; web search
   (admin-picked provider, off by default, its own egress switch); memory tools `remember`, `recall`, `forget`.

## 22. An assistant that works every screen

### 22.1 What the user sees

- An **Assistant** drawer over the right edge of every screen in the app shell, opened from a dock item or ⌘J. At lg
  and up it overlays, so Create's Elements panel stays; on **Graph** and the **pipeline editor and Studio view** the
  side panel moves left to sit beside the drawer and the canvas narrows (board frame 5), so nothing is covered. On a
  phone it is a bottom sheet that shrinks to one line while actions run. One conversation per chat, kept; "New chat"
  starts another. It is not on `/signin` or `/welcome` (outside the shell).
- **Sees** chips under the title say what it reads now: the screen, session, workflow and version, Run on, and
  `@elements`.
- You say what you want; it **does it on screen**, visibly. Each step is a line ("Opened Timeline · Seq 2", "Set
  Lighting: Blue hour"), with **Undo** where the screen can undo.
- Anything that **costs GPU time, deletes, or sends data off the machine** stops at a **card** (22.6). Yes is your
  click and calls the screen's own button.
- It **chains**: "make an image then animate it" prepares Generate, waits on your click, waits for the run ("Waiting
  for run · 2nd in line · 0:40"), then prepares the video with the new take.
- It **remembers** across chats (22.8) and can **search the web** when the admin turns it on (22.7).
- It can **look at takes**: "which of these four is sharpest?", "does the video match the prompt?" (up to 8).

### 22.2 Examples per screen

| Screen | It can |
|---|---|
| Anywhere | Go to any screen with the right thing open (22.4); explain what is on screen; answer "how do I…" (also GPU host setup over TLS, which is ops, not in the app) |
| Create | Prompts with `@elements`, looks, voices; Image / Video / Audio and the model and version; Run on; presets; size, steps, seeds, LoRAs; Generate (card); stop a run; explain a failed run; chain into Video or voicing |
| Graph | Build a workflow from a description, wire it, fix validation problems; Generate (card) |
| Pipelines | Turn a brief into a pipeline; test the judge (card); start, resume, retry, run again, stop (cards), pause; pick at a gate (card); explain a row |
| Timeline | Script → shots (card); place, trim, split, order clips (Undo); dialogue and voicing (card); re-render, export (cards) |
| Gallery | Find takes, look at up to 8; filter, sort, star, rename, collect (Undo); delete (card) |
| Models | Explain variants, add-ons, licences and machines; for an admin, prepare a download (card); for others, "ask an admin" |
| Admin | Explain and navigate only (decision 6) |

### 22.3 How it works: a LangGraph agent

Replaces the 2026-11-21 design of one stateless `cmd.assistant.turn` worker.

**Who does what**

- **studio_service** keeps the **thread rows** (chats, messages, action lines, cards), row-level security,
  roles, caps and **SSE** to the drawer (`serveEvents`). It builds each turn's tool list from the owner's current
  roles and the catalog (22.7), and runs the read tools for the agent through `req.studio.tools` as the thread's
  owner (taken from its own row, never from the request).
- **pipelines_service** runs the **agent**: a LangGraph graph per chat on the existing `AsyncPostgresSaver`,
  thread id `assistant:<thread_id>`, its state carrying `owner_id` (every command is checked against it). Model
  calls go through `providers.chat_retried` (host rule, keys, failure classes, the DeepSeek JSON fallback); caps
  are counted at the bound, as `remote.py` does. Memory is in its existing `AsyncPostgresStore`.
- **web_service**: the drawer (built like the director's `ChatPanel`), the screen-action registry
  (`useScreenTools`, a context in the app shell next to `UserContext`), action lines, cards, the Memory list.

**The graph** (plain nodes, like `agent_loop.py`, no prebuilt agent):

```
start ─▶ model ─▶ tools ─▶ model … ─▶ reply (end of turn)
                    │  ▲
                    │  └─ wait: interrupt({pending}) ─▶ resumed by the browser's results, a card answer, a run
                    ├─ server tools: reads (req.studio.tools), memory (store), web_search, look_at
                    └─ screen actions, await, cards ─▶ pending ─▶ wait
```

- `model`: one call with the fenced context (22.9), the tool list and the transcript (cut oldest first, as
  `agents._fit_oldest`). An answer that fails its schema is asked once more, then the turn ends with an error line.
- `tools`: runs the calls in order, each result kept in state under its call id. Server reads and memory return
  at once. A screen action, `await`, a card, a `look_at` run or a `web_search` card stops the node with that work
  as `pending`, and the next calls wait for its result.
- `wait` only calls `interrupt(pending)`, as `agent_loop.py`'s wait does (a resumed node runs again from its start,
  so nothing with an effect sits before the interrupt). The step is published, the drawer runs the actions in the
  sending tab and posts the results, studio sends them on, and the graph resumes (`Command(resume=results)`) back
  into `tools`. Server calls are idempotent on their call id (`req.studio.tools` already is; a memory write is
  keyed by it), so a replay after a crash does nothing twice.
- **await** is a screen action like any other: the browser resolves it from the tab's existing state (the session
  SSE in `lib/runs.ts`, the batch events, the Models row, the export and media jobs, the dialog's close) with a cap
  of 10 min, then "still running"; a hidden tab waits until shown and its line says "Waiting for the tab". Nothing
  waits in pipelines but a parked checkpoint.
- **Caps** per message: 12 model calls, 30 tool calls, 4 memory writes (as `MEMORY_WRITES`), 8 takes per `look_at`;
  per user per day and per minute as the admin sets (22.10).
- **A closed tab**: nothing is resumed, the thread stays parked; your next message resumes it with "did not run:
  the tab was closed" for each pending action. **Several tabs**: each panel has a client id; a turn's actions run
  only in the tab that sent the message; other tabs show it read-only with "Continue here", which moves it.
- **Deleting a chat** deletes its rows in studio and its checkpoints (`adelete_thread`) and its summary (22.8).

**Messages** (same outbox → `CMD` → worker → `EVT` → projector → SSE pattern):

| Subject | Carries |
|---|---|
| `cmd.assistant.turn` | thread, owner, roles, tool names, and one input: a user message (with the screen summary), the browser's action results, or a card answer |
| `cmd.assistant.delete` | thread, owner: drop the checkpoints and the summary |
| `evt.assistant.step` | text, lines, the actions to run (with ids), cards, `done`, caps used |
| `req.studio.tools` | extended with the catalog's read tools when the caller is the assistant, with `thread_id` in place of batch, row and node |
| `req.pipelines.assistant.memory` | list, edit, delete, clear: the Memory list's calls, only in the request owner's namespace |

### 22.4 What it sees and where it can go

**The screen summary** is built by each screen's code from values it already works out (no new derivation, no
scraped text), sent with each message, fenced as data. Every summary has the route, the open ids, the roles and an
**"as of"** time; a summary from a hidden tab says so.

| Screen | Summary fields |
|---|---|
| Create | session; workflow (family, version); the fields shown and their values (prompt, size, seeds, LoRAs, looks, `@elements`, references with binding Auto / Replace / Guide, source range, keyframes, dialogue); each variant and its download state; presets with their greyed reason; **Run on** with each host's enabled and reason; each host's up, busy, memory and queue; add-ons per variant (downloaded, not downloaded, Mac only) and the controls they grey; Enhance / No enhance / Think; uploads in flight or failed per field; the plan estimate and run count; the Studio or Graph view; selected takes; runs in progress |
| Graph | workflow and version, node count, selected node and its params, validation problems, unsaved |
| Pipeline editor | pipeline, nodes, selected node, problems, Editor or Studio view, Studio step states and picks |
| Pipelines list | pipelines, recent batches, the open New batch draft (rows, seeds, looks) |
| Batch, review, row | status, row counts by state, parked rows and candidates, the open row's steps, attempt and decisions |
| Timeline | sequence, length, playhead and range, selected clips, tracks, shots running, exports |
| Gallery | search, filters, sort, selection, count shown |
| Models | tab, the rows shown, selected host, each variant's state, jobs running (admin) |
| Admin | the tab only |

A Create action refuses any control the form shows greyed or hidden, with the form's own reason, using the same
gating (`addonBlocks`, the Run on rules, `when` / `requires`).

**navigate** targets (from `web_service/app/(app)`); it resolves when the new screen has registered its actions
(about 10 s, else "screen did not load"); actions after it in the same batch are dropped and the new summary comes
back instead. Every batch's results carry the screen as its actions left it (a switchTo's new form), so the model
never works from the summary sent with the message; an action that repeats this message's last one of its name with
the same arguments, done and with Undo, answers at once "Already done" and never reaches the tab (the switch loop).

| Route | Params |
|---|---|
| `/` Create | `?s=<session>`, `?reuse=<take>`, `?use=<take>&w=<workflow>`; the Studio / Graph view is the Create action `view` |
| `/gallery` | `?batch=<id>` |
| `/timeline` | `?t=<sequence>` |
| `/pipelines` | none (list, recent batches, New batch) |
| `/pipelines/[id]` | `?view=studio` |
| `/pipelines/batches/[id]` | none |
| `/pipelines/batches/[id]/review` | none |
| `/pipelines/batches/[id]/rows/[row]` | `?attempt=` |
| `/models` | `?tab=loras` |
| `/admin` (admins only) | `?tab=hosts`, `keys`, `providers` (Users by default) |

Not targets: `/signin`, `/welcome`, the empty `[...section]` catch-all.

### 22.5 Screen actions

Named actions call the screen's existing entry points, registered when the screen has loaded. **Line** = an action
line; **Undo** = a line with Undo; **card** = a card first (22.6); **dialog** = opens the screen's own dialog
prefilled and resolves on close with the new id and `@handle`, or "cancelled".

| Screen | Actions |
|---|---|
| Shell | `navigate` (line), `await(run \| job \| download \| export \| clone)` (line) |
| Create | `change(key, value)` and `switchTo(workflow)` (Undo; variant, Enhance, Think, references, bindings, source, LoRAs, looks, dialogue are all form keys); `preset(name)` (Undo, refused when greyed); `runOn(auto \| host, reason)` (Undo); `seeds(n)` (Undo, at most 4); `view(studio \| graph)` (line); `newSession` (line); `renameWorkflow`, `duplicateWorkflow` (line); `deleteWorkflow` (card); `frameIn(take, first \| last)` (line); `select(take)`, `star(take)` (Undo); `stop(run)` (line); `generate` (card); `saveAsLook`, `saveAsElement`, `editItem`, `cloneVoice`, `designVoice` (dialog; Hear tries is a card); `deleteItem` (card); LoRA `rename` (line), `delete` (card) |
| Graph | the edit helpers in `lib/graph-edit.ts` and their validators (one "Undo assistant changes"); `generate` (card) |
| Pipeline editor | `lib/pipeline.ts` edits (one "Undo assistant changes"); `new`, `rename` (line); `delete` (card); `judgeTest` (card); memory `add` (dialog), `delete` (card); Studio `runStep` (card), `pickTake` (Undo) |
| Batches | `newBatch` fill (Undo), `start`, `resume`, `retry(row)`, `again`, `stop` (card); `pause` (line); `pick(row, take)` (card); review note (dialog) |
| Timeline | the typed `Op` union through `save` (409 reload-and-retry; Undo); `newSequence`, `rename` (line); `delete` (card); `setPlayhead`, `setRange` (line); `newShot` and every `actions.tsx` shot kind (extend, retake, insert, replace, redo) (card); `newLine` voicing (card); `saveAsVoice` (dialog); `rerender`, `export` (card); cancel an export (line) |
| Gallery | `search`, `filter`, `sort`, `select` (line); `star`, `rename`, `collect` (Undo); `saveAsPipeline` (line); `saveAsElement`, `saveAsLook` (dialog); `delete` (card); Reuse and Use in are `navigate` |
| Models (admin) | `openRow(model, variant, host)` (line), `download` (card, 22.6); cancel a job (line); `delete` (card) |

**Yours only** (decision 6): uploads and Export file, sign in and out, password, theme, users, API keys, providers
(and their test), host pins, unload and settings, imports, playback, and messages to a director chat (it may read the
chat). Asked to do one, it says it is yours and points at the button (board frame 6).

### 22.6 Cards

One layout (the board's `.cf`): title, what will happen, a tag line, Yes and Not now. Yes calls the screen's own
function in the tab that owns it; a card is cancelled when its screen unmounts or you navigate.

| Kind | Used by | Shows | Tag |
|---|---|---|---|
| Generate | Create, Graph, Studio step, Timeline shots and lines | model and version, machine and place in line, size, run count, total time and memory from `/runs/plan`, the prompt | uses the GPU |
| Batch | start, resume, retry, again, re-render | rows, caps, the egress summary | uses the GPU (+ sends to DeepSeek) |
| Stop | batch stop | rows that stop | can't be undone |
| Pick | a gate | the row, the take, what runs next | uses the GPU |
| Judge test | pipeline editor | takes, criteria, provider | uses the GPU, sends to DeepSeek |
| Export | Timeline | preset, size, fps, time from `export/plan` | uses the machine |
| Hear | clone and design dialogs | tries, voice model | uses the GPU |
| Download | Models (admin) | size, machine, licence state ("tick I accept on the row"); Yes is the row's own Download, enabled only once ticked | uses disk and network |
| Web search | any | the query, the provider | sends to <provider> |
| Delete | workflows, LoRAs, library items, pipelines, pipeline memory, assistant memory (`forget`), sequences, takes | what goes, what stays | can't be undone |

The licence "I accept" tick is never set or sent by the assistant. Starter looks and voices are refused.

### 22.7 Tools

**One catalog**, `contracts/tools/catalog.json`: each tool's name, description, input and output schema, the API
route it maps to, `read_only`, the roles it needs, and its surfaces (`assistant`, `mcp`). Studio copies it with
`make gen` (Go embed) to build the assistant's tool list and run the read tools; `cli/` copies it for `helm mcp`,
which registers the same tools over Kong. Write tools in it (run, batch, timeline edits) are `mcp` only; for the
assistant writes are screen actions and cards (`wait_run` is `mcp` only; the assistant uses `await`). A tool on
both surfaces has plan 16's name. The screen actions and card kinds are in
`contracts/tools/actions.json` (assistant only).

| Tool | Reads | Who |
|---|---|---|
| `sessions` | `/sessions`, a session's runs and takes | every user |
| `list_workflows`, `get_workflow` | `/workflows`, `/workflows/{id}`, `/nodes` | every user |
| `get_run`, `plan_run` | `/runs/{id}`: status, stage, error, enhanced prompt; `/runs/plan` | every user |
| `models` | `/models`: variants, add-ons, licences, per-host presence | every user |
| `hosts` | `/hosts` (redacted: up, busy, memory) and `/queue` (place in line) | every user |
| `search_takes`, `get_take` | `/takes` (the director's `gallery.search`), a take with uses and decisions | every user |
| `list_library` | `/library`, `/loras`, pipeline `/memory` (the director's `library.read`) | every user |
| `pipelines`, `batch_status` | pipelines; batches with rows, execution, step state, decisions, review, the director chat (read) | every user |
| `list_timelines`, `show_timeline` | sequences; one with its exports and media jobs | every user |
| `custom_nodes`, `model_sources` | `/custom-nodes`, `/model-sources` | every user |
| `model_jobs` | `/admin/model-jobs` | admin |
| `look_at` | the local ask stage (below) | every user |
| `web_search` | the admin's search provider (below) | every user, when on |
| `remember`, `recall`, `forget` | the assistant's memory (22.8) | every user |

**look_at**(takes ≤ 8, question) runs the local ask stage (`qwen-image-ask`, Qwen3-VL; or `llm-ask` on mlx) as an
interactive ask run, like the judge test: thumbnails at 1024 px, video as 3 keyframes, text answer. Offered only when
an ask workflow can run now (the judge test's `runnable` check); the takes' own session, else the newest; it waits in
the shared queue and shows its place in line. Remote vision is out of this plan.

**web_search**(query): off by default. The admin picks a provider (Admin › Model providers, kind `search`, its key
stored like the model providers' keys) and turns on its own egress switch, under the deployment's `REMOTE_MODELS`.
Each search is a card showing the query; the call goes through the providers' host rule; at most 5 results of 500
characters come back fenced as data, and the line lists the sources.

`rows.add` and `rows.edit` stay director-only.

### 22.8 Memory

- **Where**: the existing LangGraph store in `pipelines`, namespace `(<owner_id>, "assistant", <kind>)`, kinds
  `preference`, `project`, `worked` (the settings and prompt behind a starred or picked take), `summary` (one per
  finished chat: written by one model call on New chat or 24 h after the last message, at most 600 characters).
- **Saving**: `remember(kind, text)` saves on its own and shows "Remembered: …" with Undo (Undo deletes it). At most
  4 writes a message, 200 items a user, 500 characters an item; when full the oldest `summary` goes first.
- **Reading**: preferences are put in every turn (≤ 2000 characters); `recall(query, kind?)` is a plain text match
  over the owner's items (no embeddings), at most 8.
- **forget**(id) is a delete card. The **Memory** list in the drawer (board frame 6) shows the four kinds as tabs (Likes, Projects, Worked, Chats),
  each item with its source chat, Edit and Delete, and Clear all (a card).
- **Rules**: memory text is data, fenced, never instructions; only the owner's namespace is ever read (another
  user's item id is 404); **deleting a chat deletes its summary**, the other items it saved stay.

### 22.9 Safety

- **Your messages are yours**: the user role, unfenced. **Everything else is data** in the `<<< >>>` fence: the
  screen summary, take titles and prompts, tool results, action results, **memory items and web results**. Every
  answer is checked against its schema before anything runs; model text is shown as text, never HTML.
- **No server write tools except memory**; costly actions only prepare the screen and wait for a card. Injected text
  in a take, a tool result, memory or a web page can never trigger an action: at most it asks for one, which is a
  card you can refuse.
- **Mentions**: handles you wrote, handles already in the form, and handles from a library read or a dialog result.
- **What leaves the machine is shown**: a remote model provider (your messages, the summary, tool results, memory
  put in the turn; never images) in Admin › AI agents; web search per card. Owner, 2026-11-22: no remote disclaimer
  in the drawer.
- **Owner scoping**: studio takes the owner from its thread row; pipelines checks it against the graph's state;
  idempotent message and call ids; long work 202; admin tools never reach a non-admin and each admin read checks the
  role again.

### 22.10 Contract changes (the next free API version after main, 1.41 today)

- `openapi.yaml`: `assistant/threads` (list, create, get, delete), `…/messages` (202: a message, action results, or
  a card answer), `…/events` (SSE); `assistant/memory` (list by kind, clear all), `assistant/memory/{id}` (edit,
  delete); admin settings under Admin › AI agents since the merge (plan 21 §25.11; assistant on or off, provider and model, search provider, caps:
  model calls and tokens per user per day, `look_at` per hour, messages per minute); a Gallery `order` parameter
  (newest, oldest, starred first, name).
- `messages/`: `cmd.assistant.turn`, `cmd.assistant.delete`, `evt.assistant.step`, `req.pipelines.assistant.memory`;
  `req.studio.tools` gains the catalog's read tools.
- `contracts/tools/catalog.json` and `actions.json` (22.7), checked by `make contracts-check`.

### 22.11 Phases

1. **Design**: the board (`docs/mock/Assistant.dc.html`), approved by the owner before anything is built.
2. **Contract and catalog** (6 h).
3. **pipelines agent**: the graph, checkpoints, interrupts and resume, caps, memory, web search, `look_at` (16 h).
4. **studio**: threads, SSE, roles, read tools through `req.studio.tools`, admin settings, the Gallery order (14 h).
5. **Drawer, navigate, `await`, Create and Gallery**, cards (18 h).
6. **Graph, Pipelines and batches** (12 h).
7. **Timeline, library dialogs, Models downloads**, the Memory list (14 h).

About 80 h. Each phase ends with its tests: another user's thread, take or memory item is 404; a costly action never
runs without the click; injected text in a take, tool result, memory item or web result cannot trigger an action;
admin tools never offered to a user; caps; the fake provider for tool-call turns; a closed and a hidden tab mid-turn;
two tabs; `await` past its cap; a resumed checkpoint after a pipelines restart; web tests per screen action.

### 22.12 Open points

- Whether "always allow" is offered for the Undo-able writes or web searches in a chat (never for runs or deletes).
- Whether `look_at` (a short local GPU ask, capped per hour) should get a card too; today it runs with a line.
- Voice input and spoken replies with the voices packs: later.
