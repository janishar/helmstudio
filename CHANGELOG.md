# Changelog

## 0.1.0-alpha.4 (2026-11-25)

The Assistant gets its own place, and helmstudio installs on Windows and Linux with Docker.

### What's new

- **Hub and System.** Admins get two rail items. **Hub** holds everything AI: Assistant, In pipelines, Providers and
  Plugins. **System** holds Users, Hosts and API keys. The old Admin and Plugins items are gone (`/admin` opens
  System).
- **The Assistant for everyone.** It opens from the Assistant drawer. The first launch opens it with a setup message
  and a button; setup is one flow: pick who answers, the key, the model.
- **The pipelines default model.** Hub › In pipelines sets the model a pipeline's AI step uses on Auto (Local LLM, a
  provider model, or the same as the Assistant), next to Session names.
- **Your Claude subscription, no key to paste.** Add as provider takes the bridge's key from the Mac by itself.
- **Windows and Linux with Docker.** Each release has one `compose.yml`: `docker compose up -d --wait` with
  `HELMSTUDIO_ADMIN_EMAIL` and `HELMSTUDIO_ADMIN_PASSWORD` on the first start, then http://localhost:8300. The first
  start makes the secrets and the admin; no install script. `--profile nvidia` adds the GPU runner. The images are
  built on GitHub for Intel/AMD and Apple Silicon (linux/amd64 and linux/arm64).
- **The Mac app as before.** The `.dmg` is now built on GitHub too; from 0.1.0-alpha.2 it updates itself.

### Known limits

- Windows is untested.
- GPU generation on Windows and Linux needs `--profile nvidia`, an Intel/AMD machine with an NVIDIA card and, on
  Linux, NVIDIA's Container Toolkit. ZONOS3 and MiniMax H3 on NVIDIA are not in the compose yet.
- The Mac app needs Apple Silicon and is ad-hoc signed (no Developer ID): open it the first time with right-click ›
  Open. From 0.1.0-alpha.1 it is one manual `.dmg` replace.
- No uninstall tool for the app; delete its folders by hand (docs/guides/mac-app.md).
- A separate NVIDIA machine joining a Mac's stack is set up by hand (deploy/gpu-host/README.md).
- Your Claude subscription runs on the Mac only, and needs Claude Code installed and signed in by you.
- Web search uses unofficial search pages: it can be rate-limited or break.
- The Assistant sees a video as its poster frame only (use the frames tool to look closer).
- An Assistant steer is kept only in memory: if pipelines restarts mid-answer, it becomes the next message.
- Models are downloaded separately (on the Models screen) and keep their own licences; no weights ship with
  helmstudio.

## 0.1.0-alpha.3 (2026-11-25)

A small alpha: an optional way to use your own Claude plan, and fixes to the AI agents and Plugins pages.

### What's new

- **Your Claude subscription.** An optional personal plugin in Admin › Plugins, off by default. Turned on, it runs
  your own Claude Code on the Mac (you install it and sign in yourself) and adds a model provider only you can use.
  helmstudio never handles your Claude login. Its disclaimer: "Personal use only. Runs your own Claude login and
  plan, for you only. Never shared with other accounts." If Claude Code is missing or signed out it says Couldn't
  start · Install Claude Code and sign in, with Try again.
- **AI agents loads at once.** The table no longer waits on the plugins list. A job whose saved provider is off says
  "<Provider> is off · Edit to pick another".
- **Plugins that need you.** A plugin waiting on you (for example, to sign in) no longer says "Trying again in
  1 min."; only a plugin the runner retries by itself does.

### Known limits

- The Mac app needs Apple Silicon and is ad-hoc signed (no Developer ID): open it the first time with right-click ›
  Open. From 0.1.0-alpha.2 it updates itself; from 0.1.0-alpha.1 it is one manual `.dmg` replace.
- The released images are linux/arm64 only. On an x86 Linux host, build them from source (`make prod`).
- No uninstall tool for the app; delete its folders by hand (docs/guides/mac-app.md).
- NVIDIA hosts are set up by hand (deploy/gpu-host/README.md).
- Your Claude subscription runs on the Mac only, and needs Claude Code installed and signed in by you.
- Web search uses unofficial search pages: it can be rate-limited or break.
- The Assistant sees a video as its poster frame only (use the frames tool to look closer).
- An Assistant steer is kept only in memory: if pipelines restarts mid-answer, it becomes the next message.
- Models are downloaded separately (on the Models screen) and keep their own licences; no weights ship with
  helmstudio.

## 0.1.0-alpha.2 (2026-11-25)

The second alpha: a much more capable Assistant, plugins, media tools, and a Mac app that updates itself. Expect
rough edges and breaking changes between releases.

### What you can do now

- **Assistant, bigger and smaller.** Expand it to full screen, or minimize it to a small bar while you work.
- **Auto mode.** A per-chat switch: the Assistant runs safe local generations and safe saves (voice, element, look,
  preset, star, rename, add to timeline) without asking, within caps per hour an admin sets in Admin › AI agents.
  It still asks before deletes, overwrites, anything sent off your machines and very large runs. Every auto step
  shows in the chat with Stop and Undo.
- **It sees images.** Attach or paste an image, or type `@` to pick a Gallery take, an upload, an element or a skill.
  A model with vision looks at it directly (a text-only model falls back to Look at), and it checks the takes it
  makes against your ask. Video goes to it as its poster frame.
- **Media in answers.** Takes, uploads and files show inline as images, playable video and audio, with Open and
  Use in…; ids show as chips with a thumbnail, never raw.
- **Pick-one cards.** When it offers choices, they are buttons that send your pick; when the next shot is unclear it
  asks this way instead of guessing.
- **Shot lists that stick.** When you plan a film it saves the shot list in Memory › Projects and reads it from any
  screen and any chat; it can mirror it on the Timeline.
- **Better answers.** Lists, bold, code blocks with Copy; Copy and Retry on answers; clickable Timeline timecodes;
  long answers clamped with More; plans as a checklist; earlier turns fold into one summary line, and each reply
  folds with one chevron (the latest always in full); a counting
  "Working… N s"; Stop works at once, even while it is thinking. Errors are in plain words, with Details.
- **Memory in plain words.** No JSON or ids in what it remembers; the Memory lists use the full width.
- **Longer chats.** The chat window fits the model's real limit and folds the oldest part into a summary instead of
  dropping it; prompt caching for DeepSeek, OpenAI and Anthropic, with the hit rate in Admin › AI agents.
- **Model guides.** Each model pack carries a guide for the Assistant (what the model is good at, the settings that
  matter, times, pitfalls) and named recipes (fast draft, final, …). The Assistant reads the guide when it works
  with that model.
- **Voice workflows.** Switching to a voice workflow keeps your last script, never the picture prompt; the
  Assistant writes the spoken line or a dialogue script itself.
- **Plugins.** Admin › Plugins lists plugins, shows whether they run, their permissions and disclaimer. Two kinds in
  this first version: model providers and Assistant tools. Drop a folder or `.zip` into `~/.helm/plugins/` to add
  one without an app update; each is off until you turn it on. A plugin with its own server (web search) starts
  itself on the Mac when you turn it on, and again whenever the runners start; the page shows Starting…, Running or
  Couldn't start with the reason.
- **Skills.** Text plugins that teach the Assistant a craft, read in when your ask fits or on `@<skill>`. Built in:
  Animator and Cinematic filmmaker (an end-to-end film pipeline).
- **Media tools.** Images: crop, resize, rotate, flip, convert, extend the canvas. Video: trim, a frame as an image,
  speed, mute or replace the audio, and frames pulled at set times or scene changes for the Assistant to look at.
  Audio: trim, volume, join. Each makes a new take (the original stays) with Undo, from the Assistant or the
  Gallery's Edit. They run in the stack's own `media-tools` container: nothing to install.
- **Web search.** A plugin that gives the Assistant a `web_search` tool with no key (DuckDuckGo, then Bing, Google
  or Yahoo when one refuses). It asks first, since it sends your words off the machine.
- **Render time.** A small clock with the time each take took, in Create, the Gallery and the Timeline (with the
  sequence total).
- **Gallery.** Double-click opens a take's full preview; thousands of takes stay fast (only the rows near the screen
  are drawn), in the Create picker too.
- **Create.** Fields keep an even grid when the row wraps.
- **Sound tiles.** One speech-like waveform for every sound.

### The Mac app

- **Updates itself.** It checks GitHub's releases at start, every 24 h and on **Check for updates…**, shows **Update
  to <version>**, downloads only when you press **Install**, checks the SHA256, then swaps itself and opens again.
- The first open of a new version pulls its images with real progress (MB or GB of the total), refreshes the runners
  and removes the old version's images once it runs.
- The first-open images step says which size is which: "0.6 GB download · 2.5 GB on disk".

### Known limits

- The Mac app needs Apple Silicon and is ad-hoc signed (no Developer ID): open it the first time with right-click ›
  Open. Updating from 0.1.0-alpha.1 is one manual `.dmg` replace; from this version on, the app updates itself.
- The released images are linux/arm64 only. On an x86 Linux host, build them from source (`make prod`).
- No uninstall tool for the app; delete its folders by hand (docs/guides/mac-app.md).
- NVIDIA hosts are set up by hand (deploy/gpu-host/README.md).
- Web search uses unofficial search pages: it can be rate-limited or break.
- The Assistant sees a video as its poster frame only (use the frames tool to look closer).
- An Assistant steer is kept only in memory: if pipelines restarts mid-answer, it becomes the next message.
- Models are downloaded separately (on the Models screen) and keep their own licences; no weights ship with
  helmstudio.

## 0.1.0-alpha.1 (2026-11-24)

The first public alpha. Expect rough edges and breaking changes between releases.

### What you can do

- Create images, video and voice with open-weight models on your own Mac (MLX, PyTorch MPS) and NVIDIA machines.
  Call characters and places by name with `@`, and set a look (film, camera, colour, lighting) once.
- Work with the Assistant: it fills the form, wires the graph, places clips, asks before it uses the GPU, and each
  step has its own Undo.
- See every workflow two ways: a Studio form or a Graph of typed steps on a node canvas; editing one edits the other.
- Run pipelines over a list while you are away; agent nodes judge each take against a checklist, retry, and learn
  from the takes you keep.
- Keep every take in the Gallery with the exact settings and inputs that made it.
- Cut on the Timeline (trim, blade, ripple, direct new shots in place) and export an MP4.
- Manage models per machine: download weights, pick variants, add add-ons, upload LoRAs to a shared library.
  Remote models (OpenAI, Anthropic, DeepSeek and others) with your own keys.
- Use it from Claude: `helm mcp` gives MCP clients the Assistant's tools.
- Host it for a team: self-hosted, multi-user, each person sees only their own work.

### The Mac app (prototype)

- `helmstudio.dmg` on the GitHub release: the whole studio on one Mac, the services in a small VM of the app's own,
  the runners on the Mac's GPU, a menu-bar item. No Docker or OrbStack needed: the images are downloaded from
  GitHub's registry on first open.

### Known limits

- The Mac app needs Apple Silicon.
- The app is ad-hoc signed (no Developer ID): open it the first time with right-click › Open.
- No uninstall tool and no auto-update for the app; delete its folders by hand (docs/guides/mac-app.md).
- NVIDIA hosts are set up by hand (deploy/gpu-host/README.md).
- An Assistant steer is kept only in memory: if pipelines restarts mid-answer, it becomes the next message.
- Models are downloaded separately (on the Models screen) and keep their own licences; no weights ship with
  helmstudio.
