# helmstudio-next plan — The in-app assistant

[← Plan index](../README.md)

Status: a plan, not in a milestone yet: it becomes one in [08-milestones.md](08-milestones.md) when the owner says to
build it (owner, 2026-11-21: "plan with me how we can power the whole web features with ai agents in the browser
ui"). Decided with the owner the same day: it **acts, and confirms what costs or lasts**; phase 1 covers
**navigation, Create, Graph and Pipelines, the Timeline and the Gallery**; the **admin picks the model** (DeepSeek
today); it **can look at takes from the start**, through the local ask stage when Qwen Image is installed. Facts come
from a read of the code (web screens, the director agent, the ask stage) and an adversarial review, 2026-11-21.

## 22. An assistant that works every screen

### 22.1 What the user sees

- An **Assistant** drawer over the right edge of every screen, opened from a dock item or ⌘J (at lg and up it
  overlays, so Create's Elements panel and the editors' side panels stay; on a phone it is a bottom sheet that shrinks
  to one line while actions run, so the screen stays visible). One conversation per user, kept; "New chat" starts
  another.
- It knows **where you are and what is selected**: the screen, the open session, workflow, pipeline, batch, timeline
  or take, the form's values and the problems shown.
- You say what you want; it **does it on screen**, visibly: goes to the screen, fills the form, picks the model, look
  and `@elements`, adds and wires nodes, places clips, filters and sorts the Gallery. Each step is a line in the chat
  ("Opened Timeline · Seq 2", "Set Lighting: Blue hour"), with **Undo** where the screen can undo.
- Anything that **costs GPU time, deletes, or sends data off the machine** stops at a **confirm card** showing what
  will happen (a run: model, size, the memory and time estimate from `/runs/plan`, the prompt; a batch: rows and the
  egress summary; a delete: what goes). The Yes is your click on the screen's own button logic.
- It can **look at takes**: "which of these four is sharpest?", "describe this frame", "does the video match the
  prompt?" (up to 8 takes a time).

### 22.2 Examples per screen

| Screen | It can |
|---|---|
| Anywhere | Go to any screen with the right thing open; explain what is on screen; answer "how do I…" |
| Create | Write and refine prompts (with `@elements`, looks, voices); switch Image / Video / Audio and the model; set size, steps, seeds, LoRAs; Generate (confirm); explain a failed run |
| Graph | Build a workflow from a description, wire it, fix validation problems (autosaved like your edits; one "Undo assistant changes") |
| Pipelines | Turn a brief into a pipeline (steps, judge, write, gates); start a batch (confirm, with egress); review gates with you; explain a row's failure |
| Timeline | Script → shot list → New shot per shot (confirm); place, trim, split and order clips (saved like your edits, with Undo); write dialogue and voice it (confirm) |
| Gallery | Find takes by text, then look at up to 8; filter, sort, star, collect (with Undo), compare |
| Admin (admins only) | Explain hosts and models; suggest downloads (confirm) |

### 22.3 How it works

**The turn.** You send a message (with the screen summary). The model answers with text and tool calls. Read tools
run on the server inside the turn. If it asks for **screen actions**, the turn ends there: the panel runs them in
order and sends the results back as the next message, which starts the next turn. With no tab open nothing is sent
and the thread waits; your next message says which actions did not run. No timers, nothing parked.

**Two kinds of tools**

1. **Read tools, on the server**: search takes, get a take, list workflows and node schemas, the library (elements,
   looks, voices, LoRAs), pipelines and batches, a timeline, model sources, host state; `look_at`. They run in studio
   as the thread's owner (row-level security; another user's id is 404). Studio builds the tool list each turn from
   the owner's current roles: admin tools never reach a non-admin, and each admin call checks the role again.
2. **Screen actions, in the browser**, through each screen's existing entry points, registered once per screen with
   `useScreenTools` (a context in the app shell next to `UserContext`) when the screen has loaded:
   - **navigate**: `router.push` to the screen's URL (`?s=`, `?t=`, `?view=`, batch and row pages); it resolves when
     the new screen has registered its actions (or after about 10 s, "screen did not load"); actions after a navigate
     in the same batch are dropped and the new screen's summary is returned instead;
   - **Create**: `change(key, value)`, `switchTo(workflow)`; looks and elements are set as their outcome (the picked
     look, an `@handle` in the prompt), never by driving a picker;
   - **Graph and pipeline editor**: the pure edit helpers (`lib/graph-edit.ts`, `lib/pipeline.ts`) and their
     validators; edits autosave as yours do; the undo point before the turn gives "Undo assistant changes";
   - **Timeline**: the typed `Op` union through the editor's `save` (its 409 reload-and-retry included; a 409 notice
     is the action's result); Undo on the line;
   - **Gallery**: filter, search, sort, select, star, collect (Undo on the line).

**Confirm cards, and why nothing costly can slip through.** The model has **no write tools on the server** at all.
Costly actions (Generate, start a batch, New shot, voicing, deletes, downloads) are screen actions that only **prepare
the screen** and show the card; Yes is a click that calls the screen's own function (`send()`, New batch's start, the
Timeline's shot run) in the tab that owns it. A card is cancelled when its screen unmounts or you navigate.

**Several tabs.** Each panel has a client id; a turn's actions run only in the tab that sent the message; other tabs
show the thread read-only with "Continue here".

**What runs where**

- **studio_service** keeps the threads: messages, tool calls, the turn loop, read tools, the screen-summary limits,
  caps, and SSE to the panel (the existing `serveEvents` helper). Row-level security on every table.
- **pipelines_service** gets one stateless worker: `cmd.assistant.turn` (provider, model, messages, tools,
  max_tokens) → `providers.chat_retried` (the host rule, keys, failure classes, the DeepSeek JSON fallback) →
  `evt.assistant.answer`. Same outbox → CMD → worker → EVT → projector pattern; no LangGraph threads, no change to
  `remote_calls`.
- **web_service**: the drawer (built like the director's `ChatPanel`), the screen-action registry, action lines,
  confirm cards.
- **Contract first** (a new API version): `assistant/threads` (list, create, get, delete), `…/messages` (202; a user
  message, or the browser's action results), `…/events` (SSE); `cmd.assistant.turn` and `evt.assistant.answer`;
  admin settings; a Gallery `order` parameter (newest, oldest, starred first, name).

**Looking at takes.** `look_at(takes ≤ 8, question)` runs the existing local ask stage (`qwen-image-ask`, Qwen3-VL;
or `llm-ask` on mlx) as an interactive ask run, like the judge test: thumbnails at 1024 px, video as 3 keyframes,
text answer. It is offered only when an ask workflow can run now (the judge test's `runnable` check: the model
downloaded, a host up). It uses the takes' own session, else the newest (as the judge test does), waits in the
shared GPU queue, and shows its place in line. Remote vision (DeepSeek cannot see) is out of this plan.

**Admin settings** (Admin › Model providers): the assistant on or off (its own egress switch, under the deployment's
`REMOTE_MODELS`), which provider and model, and three caps: model calls and tokens per user per day (counted at the
bound, as `remote.py` does), `look_at` per hour, and messages per minute.

### 22.4 Safety

- **User messages are the user's**: they go in the user role, unfenced. **Everything else is data** inside the
  `<<< >>>` fence, as tool-role content: the screen summary (built by the screen's code from typed values, never by
  scraping page text), take titles and prompts, tool results and action results. Every answer is checked against its
  schema before anything runs; model text is shown as text, never HTML.
- **Mentions**: the model may use handles you wrote, handles already in the form, and handles it got back from a
  library read; a filled prompt only runs after your click on its card.
- **Nothing costly without a click**: the loop has no server write tools; costly actions only prepare the screen.
- **What leaves the machine is shown**: with a remote provider the drawer says so (your messages, the screen summary,
  tool results; never images).
- **Owner scoping** as everywhere (studio takes the owner from its own thread row); idempotent message and call ids;
  long work 202.

### 22.5 Phases

1. **Design**: a board (`docs/mock/Assistant.dc.html`): the drawer on Create with Elements open, on the Timeline,
   and at phone width; action lines; the confirm card. Approved by the owner before anything is built.
2. **Contract and server**: threads, the turn loop, the pipelines worker, read tools, `look_at`, admin settings,
   caps, the Gallery sort.
3. **Drawer, navigation, Create and Gallery.**
4. **Graph and Pipelines builders** (edits, validation, batch start).
5. **Timeline director** (script to shots, ops, dialogue and voices).

Each phase ends with its tests: isolation (another user's thread or take is 404), a costly action never runs without
the click, injected text in a take title or tool result cannot trigger an action, admin tools never offered to a
user, caps, the fake provider for tool-call turns, a closed tab mid-turn, two tabs, and web tests per screen action.

### 22.6 Open points

- Whether "always allow" is offered for the Undo-able writes in a chat (never for runs, deletes or egress).
- Voice input and spoken replies with the voices packs: later.
