# helmstudio plan — The Assistant is the one place for AI agents

[← Plan index](../README.md)

Status: **built (37.10); owner said build it (2026-11-25, "make the web not design"), on the boards as drawn (Assistant frames 19–28 at 0de85ff1) with 37.7; the Create backdrops behind the drawer on that board are an old copy: Create is unchanged.** For 0.1.0-alpha.4
([release list](../release/0.1.0-alpha.4.md), item 2), after [plan 32](32-assistant-first-class.md).

The owner's asks, 2026-11-25, trying plan 32 as a fresh user: "why model provider and ai agents are in admin?", then
"make assistant as the only place agent related things are there. assitent is also silent assitant in pipeline lets
say".

## 37. One place for everything AI

### 37.1 The idea

The Assistant is one helper with two faces: the **chat** you talk to, and the **silent assistant** that does AI work
without a chat: the write, decide, agent and judge steps of a pipeline, and naming sessions. Everything that sets up
or steers either face lives on the Assistant page. Nothing about AI agents stays in Admin or has its own rail item.

### 37.2 The Assistant page, as tabs

`/assistant` keeps one rail item (Assistant) and gets tabs, like Admin's:

1. **Agent** (was "Chat"; owner, 2026-11-25: "rename chat to Agent and appropriate icon"): today's page (plan 32 frame 19): Model, Tools, Skills, Auto mode, Context, Memory, the on/off switch.
2. **In pipelines** (the silent assistant):
   - **Default model** for pipeline write, decide, agent and judge steps: what a step's "Runs on · Auto" means.
     Today Auto is the local LLM pack (and a family's own vision model for a judge); the admin may pick a provider
     model instead, or "Same as Agent". A step that names a model keeps it.
   - **Session names** (moved from Admin › AI agents): Off · Local LLM · a provider model, as today.
   - What leaves the machine, per choice, as today's AI agents table says it.
3. **Providers**: Admin › Model providers moved as it is (list, add from presets, keys, models, thinking per model,
   enable, test). Every AI job picks from here.
4. **Plugins**: the Plugins page moved here (model-provider, assistant-tools and skill plugins are all AI). The
   Plugins rail item goes. (A later plugin kind that is not AI would get its own home then.)

Who sees what: an admin edits every tab; anyone else sees Agent read-only plus their own Memory (plan 32 frame 22),
and not the other tabs.

### 37.3 What leaves Admin and the rail

- Admin keeps **Users, Hosts, API keys**. Model providers and AI agents tabs go; old links
  (`/admin?tab=providers`, `?tab=agents`, `/plugins`) open the matching Assistant tab.
- The rail loses Plugins: Create, Pipelines, Gallery, Timeline, Models, Assistant, Admin, then Agent (the drawer, ⌘J; was Chat, with an agent icon instead of the speech bubble) and Settings.
- Links that pointed there now point to the Assistant tabs: a pipeline step's Runs on ("Set the default in Assistant
  › In pipelines"), plan 32's "used by the Assistant" line, Add as provider's "Open in Providers".

### 37.4 Contract

- Routes stay where they are (`/v1/studio/admin/providers`, `/plugins`, `/session-names`, …): this is a move of
  screens, not of the API.
- New: the pipelines default model (an admin setting, read by pipelines when a step's model is Auto), the smallest
  additive change, with the same remote-switch (409) and only-you rules as the Assistant's own provider.

### 37.5 Rules kept

Plan 32 36.7 in full: keys never read back or logged; the remote switch; only-you providers; admin checks server-side;
what leaves the machine shown where the provider is picked.

### 37.6 Boards

To draw before building ([Assistant](../mock/Assistant.dc.html), new frames 23–26):

- 23 · The Assistant page's tabs, Agent selected (frame 19 under tabs), the rail without Plugins.
- 24 · In pipelines: Default model and Session names.
- 25 · Providers (today's Model providers board under the Assistant tabs).
- 26 · Plugins (today's Plugins list under the Assistant tabs).
- Admin with three tabs; a pipeline step's Runs on with the new line.
- Frame 21 redesigned (owner, 2026-11-25: "this ui looks like its very basic improve it", then "good"): the drawer's
  setup states get a large Assistant mark, a line of what it does, three rows of what it helps with, the provider
  chips (admin), a full-width Set up / Turn on button; built with this plan. Also built already: the setup message
  centred, the first-launch line "Close it now and set up the Assistant later from Agent or ⌘J.", and the header's
  Assistant-page button with the Assistant icon.

### 37.7 Names and who sees it (owner, 2026-11-25)

The owner: "group assitant, admin - rename admin to system and Assitant to AI, it will only be accesible to admin user.
Agent get available to user when admin set the AI on". So:

- **Rail:** the pages Create, Pipelines, Gallery, Timeline, Models; then, for admins only, a group of two: **AI** (was
  Assistant, the sparkle icon) and **System** (was Admin, the shield icon); then **Agent** (the drawer, ⌘J) and
  Settings.
- **AI** (`/ai`; `/assistant` redirects) holds the tabs Agent · In pipelines · Providers · Plugins. **System**
  (`/system`; `/admin` redirects) holds Users · Hosts · API keys. Both are admin only: a non-admin gets no rail item
  and a 404 page, as `/admin` today. Frame 22 (the read-only page) goes.
- **Agent for users:** every user has the Agent rail item and ⌘J. While it is not set up or is off, a non-admin's
  drawer shows a message with no button: "The Agent isn't set up yet · Ask your admin to set it up." (with the rows of
  what it helps with) or "The Agent is off · Your admin turned it off." (owner, 2026-11-25: "if not admin say ask admin
  to setup no button but message"). An admin gets the setup messages with their buttons (frame 21 a, c, first
  launch), titled "Set up the Agent" / "The Agent is off", the button opening AI.
- **Switches have one home:** tools and skills are switched on and off only on AI › Agent; AI › Plugins adds, removes
  and rescans, and each row says where it is used.

### 37.8 Hub (owner, 2026-11-25)

"since plugin is here and its better to call it Hub": the admin page called AI in 37.7 is **Hub** (`/hub`; the sparkle
icon and green tile), with the tabs Agent · In pipelines · Providers · Plugins. Texts that named AI name Hub ("Opens
Hub.", "Auto · set in Hub › In pipelines", "Turned on in Hub › Agent"). The rail: pages, then Hub and System (admins),
then Agent and Settings.
Then "then agent will become assistant": with the page called Hub, the drawer and helper are named **Assistant** again
(the bot icon stays): the rail button, the drawer header, Hub's first tab (Assistant · In pipelines · Providers ·
Plugins), "Same as Assistant", and the setup texts ("Set up the Assistant", "The Assistant is off", "Turned on in Hub ›
Assistant").

Then "Hub icon change": Hub's icon is lucide `Blocks` (pieces that plug together) on the green tile, in the rail, its title and the
Assistant drawer's Hub button; the Assistant keeps the bot icon, System the shield.

### 37.9 Final (owner, 2026-11-25: "lets finalize"); this list wins over every earlier line

- **Rail:** Create · Pipelines · Gallery · Timeline · Models; then, admins only, **Hub** (lucide Blocks, green tile,
  `/hub`) and **System** (shield, `/system`); then **Assistant** (bot icon, the drawer, ⌘J, every user) and Settings.
  No Plugins, Admin or AI item.
- **Hub** (admin only; a non-admin gets not-found): header Hub, On tag, the Assistant on/off switch; tabs
  **Assistant** (model, tools and skills with their only on/off switches, Auto caps, context, memory) · **In
  pipelines** (default model for pipeline AI steps on Auto: Local LLM · a provider model · Same as Assistant; Session
  names) · **Providers** (as Model providers was) · **Plugins** (add, remove, rescan; no on/off; "Turned on in Hub ›
  Assistant").
- **System** (admin only): Users · Hosts · API keys.
- **Old links:** /admin → System; /admin?tab=providers, ?tab=agents, /plugins, /assistant → the matching Hub tab.
- **Assistant drawer:** header Assistant (bot icon) and, for admins, a Hub button (Blocks icon). Not set up: an admin
  sees the redesigned welcome with "Set up the Assistant", "Runs on" chips and a full-width Set up (opens Hub); anyone
  else "The Assistant isn't set up yet · Ask your admin to set it up." with no button. Off: an admin "The Assistant is
  off" with Turn on; anyone else "Your admin turned it off." First launch (admin, never set up): opens once, "Close it
  now and set up the Assistant later from Assistant or ⌘J."
- **Your Claude subscription:** its key is picked automatically (no Key field); not offered as the pipelines default.

### 37.10 As built (2026-11-25, branch alpha.4, API 1.74), and what differs from the boards

- **Contract.** `GET/PUT /v1/studio/admin/pipeline-model` (`PipelineDefaultModel`, mode `local` · `remote` ·
  `assistant`; admin only; the Assistant's 409 and only-you rules) and `ModelSourceList.auto` (what Auto resolves to
  now for a write, decide or agent step; null: the local LLM). Studio resolves Auto at every check and Submit; pipelines
  needed no change (it runs the snapshot). contracts/README.md, Model sources: the pipelines default model.
- **A judge on Auto always keeps its family's vision model** (judges send thumbnails; a server or remote model gets
  text only). Board 27 c draws a Judge on Auto · DeepSeek; the app shows the default and the "Auto · set in Hub › In
  pipelines" line only on write, decide and agent steps.
- **A remote default needs no pipeline remote switch:** it is the admin's choice for every pipeline, so Submit turns the
  snapshot's remote on for that provider (the pipeline's own caps, else 200 calls and 400 000 tokens) and lists it in
  what leaves the machine. A default that is not usable now (disabled, deleted, a model it no longer lists, remote while
  REMOTE_MODELS is off) falls back to the local LLM.
- **In pipelines:** the Local LLM rows say "Qwen3.8 27B" without the host (no API names it). "Your Claude subscription
  is not offered here" shows only while an Only-you provider exists. A non-admin's step line says "Hub › In pipelines"
  as plain text (Hub is admin only).
- **Drawer:** the 340 px chat drawer's header stays the bot mark only (plan 32 36.10: every button fits); the setup
  states write "Assistant". "The Assistant isn't set up yet" wraps to two lines (the board's "Agent" was shorter).
- **Providers:** the form beside the table is always there; Add provider empties it.
- **Plugins:** a model-provider plugin's aside keeps its switch (board 26 draws one); tools and skills have none and
  say "Turned on in Hub › Assistant", except a skill that cannot be used.
- **The Assistant's navigate tool** keeps its `/admin` target (the tools catalog): it opens System, and its providers
  tab opens Hub › Providers. assistant_knowledge.md describes Hub and System.
- **Old links:** /admin → /system (tabs hosts, keys kept), /admin?tab=providers → /hub?tab=providers, ?tab=agents →
  /hub?tab=pipelines, /plugins → /hub?tab=plugins, /assistant → /hub (?setup=1 kept).
