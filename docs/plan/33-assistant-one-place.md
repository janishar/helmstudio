# helmstudio plan — The Assistant is the one place for AI agents

[← Plan index](../README.md)

Status: **owner said build it (2026-11-25, "make the web not design"), on the boards as drawn (Assistant frames 19–28 at 0de85ff1) with 37.7; the Create backdrops behind the drawer on that board are an old copy: Create is unchanged.** For 0.1.0-alpha.4
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
