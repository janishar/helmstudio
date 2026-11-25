# helmstudio plan — The Assistant as a first-class feature

[← Plan index](../README.md)

Status: **draft for the owner (2026-11-25); boards to draw, then approval.** For 0.1.0-alpha.4
([release list](../release/0.1.0-alpha.4.md)). Not in a milestone, like plans 25–31.

The owner's asks, 2026-11-25: "next alpha.4 plan better ux for assitant - move from the model provider and ai agents
into assitant setting page - first launch will show the assitant dialog with setup message and button", then
"assitant will be first class feature".

## 36. The Assistant's own page and first launch

### 36.1 Today (what a person must touch)

To get the Assistant working an admin goes to three places, and nothing points the way:

1. **Admin › Model providers**: add a provider from a preset, paste its key, Fetch models, tick them, pick thinking
   per model, Enable. (Or Plugins › Your Claude subscription › Add as provider, which lands here too.)
2. **Admin › AI agents › Assistant › Edit**: turn it on, pick the provider and model, window, web search, caps.
3. **Plugins**: web search and skills are turned on here; AI agents only lists them.

Until step 2 is saved, the Assistant is **hidden for everyone**: no dock item, ⌘J does nothing, and a fresh install
(the Mac app included) never says it exists. A non-admin is never told an admin must set it up.

### 36.2 The Assistant page

A new top-level page, **Assistant** (`/assistant`), in the main navigation beside Create, Pipelines, Gallery,
Timeline and Models. It holds everything about the Assistant, in this order:

1. **Model**: who answers. The provider and model, with the thinking level shown from Model providers. Setting it up
   is done here in one flow (36.3); an existing provider is a pick.
2. **Tools**: web search (on/off, which search) and the other assistant-tools plugins, with on/off switches here
   (the same switch as Plugins, not a copy).
3. **Skills**: the skill plugins, on/off, same switch as Plugins.
4. **Auto mode**: the caps (takes per hour, GPU minutes per hour, the other limits).
5. **Context**: chat window tokens, and the cache numbers (today's Context and cache panel).
6. **Memory**: your chats, likes, projects and worked lists (the drawer's Memory, also reachable here).
7. **Off switch** at the top: on/off for the whole deployment.

Who sees what:

- **An admin** edits all of it.
- **Anyone else** sees Model, Tools and Skills read-only ("Set by your admin"), and their own Memory. An admin's
  Only-you provider (e.g. Your Claude subscription) still shows only to its owner, as today.

The drawer gets a small **settings** button in its header that opens this page.

### 36.3 Setup in one flow

On the Assistant page, **Set up** (or **Change**) opens one short form instead of three screens:

1. **Pick who answers**: cards from the presets that suit chat (Your Claude subscription when the plugin is there,
   DeepSeek, Anthropic, OpenAI, a local server such as Ollama or LM Studio). Remote ones are greyed while remote
   models are off, with the same one-line reason as Model providers.
2. **Key** (remote ones): paste it; it is checked on `/models` before saving, as today. Your Claude subscription
   needs no key: it shows the plugin's state ("Install Claude Code and sign in, then Try again") and its disclaimer,
   and turns the plugin on.
3. **Model**: the list fetches by itself; the preset's default is picked.
4. **Save**: creates (or reuses) the provider, enables it, and saves the Assistant on with it. Done; the drawer opens.

Behind the form are the same routes as today (`POST /admin/providers`, `PUT /admin/provider-keys/{key}`,
`PUT /admin/assistant`, the plugin switch and Add as provider); no new way to store a key.

### 36.4 What stays in Admin

- **Model providers** stays the full list for every job (the Assistant, Session names, pipeline steps' Runs on). A
  provider the Assistant uses says so in its row ("used by the Assistant"), with a link to the page.
- **AI agents** loses the Assistant row and panel; it keeps Session names, with one line at the top: "The Assistant
  has its own page" and a link.
- **Plugins** keeps every plugin; assistant-tools and skill plugins link to the Assistant page.

### 36.5 The dock and first launch

- **The dock always shows the Assistant.** While it is not set up (no provider) or turned off, opening it shows a
  short setup message in the drawer instead of a chat:
  - an admin: "Set up the Assistant · pick the model it talks to. It helps you plan shots, pick workflows and run
    them." with a **Set up** button (opens 36.3 on the Assistant page);
  - anyone else: "The Assistant isn't set up yet. Ask your admin to set it up."
  - turned off by the admin: "The Assistant is off." (with **Turn on** for an admin).
- **First launch**: on an admin's first sign-in to a deployment whose Assistant was never set up, the drawer opens
  by itself once with that message. Dismissed (closed) it stays closed; the dock item stays.
- ⌘J opens the drawer in every state.

### 36.6 Contract

- `AssistantStatus` (`GET /v1/studio/assistant/threads` → `status`, every user) gains `set_up` (a provider is saved)
  and `can_set_up` (the caller is an admin), so the drawer can choose its message without an admin call. Additive,
  minor version.
- First launch needs "never set up": `AssistantSettings.updated_at` is null until the first save (as today); the
  "opened once" mark is per user (a user setting, not browser storage, so the Mac app and a browser agree).
- No other new routes; the page reuses the admin routes above, and each still checks the admin role.

### 36.7 Rules kept

- Keys are never read back or logged (plan 27); the key field stays out of React state.
- Remote providers and web search need the deployment's remote switch (409 otherwise); the page says why when off.
- Only-you providers are seen and used only by their owner.
- What leaves the machine is shown where the provider is picked (now the Assistant page), not as a disclaimer in the
  drawer (owner, 2026-11-22).
- Thinking stays set per model in Model providers; the page shows it.

### 36.8 Boards

To draw before building ([Assistant](../mock/Assistant.dc.html), new frames):

- 19 · The Assistant page (admin), set up.
- 20 · The Assistant page, setup form (pick who answers, key, model), including Your Claude subscription.
- 21 · The drawer's setup states: admin (Set up), non-admin, turned off; first launch.
- 22 · The Assistant page for a non-admin (read-only).
- [AdminAgents](../mock/AdminAgents.dc.html): the table without the Assistant, with the link line.

### 36.9 Open questions (owner)

1. The page's place: top-level **Assistant** in the main navigation (recommended), or a section of Settings.
2. Memory: on the page and in the drawer (recommended), or only on the page.
3. Should a non-admin see the Assistant page at all, or only the drawer messages?
