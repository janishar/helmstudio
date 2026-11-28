# Changelog

## 0.1.0-alpha.7 (2026-11-29)

A short fix release.

### Fixes

- **The Mac app now includes the MiniMax H3 and ZONOS3 programs.** In alpha.6's .dmg those two packs failed with
  "the h3 binary is not built".
- **The Assistant no longer fails to design a voice or run blocks while other runs finish** ("board changed since
  you opened it").

### Known limits

Same as 0.1.0-alpha.6 (the Windows, NVIDIA and Turbo LoRA items moved to 0.1.0-alpha.8).

## 0.1.0-alpha.6 (2026-11-28)

Magic, a board for making a short film; music and sound in Create; audio fades; and an Assistant that watches and
listens.

### What's new

- **Magic, the film-making board.** Its own page in the rail, after Create: lanes in time for the assets
  (characters, props and places with their views), the storyboard, the shots, and voice, music and sound tied to the
  shots. Every block shows a preview and can be run, rerun, stopped, compared, looped or pinned. New board asks the
  size and frame rate (presets or Custom); Add takes a length (5 s, 10 s, Custom) or a take From Gallery. Play
  plays the board from the playhead as a rough cut (storyboard frames where no shot is yet), with full screen.
  **Export** makes the MP4 in one step; Send to Timeline makes a new sequence to cut further. Voice, music and sound
  blocks get a loudness level (Louder, Quieter, a dB slider). Make views uses a real image model (Qwen Image Turbo)
  and says what it makes. Runs say the variant they ran on; Stop runs stops what is going. Magic asks before runs
  only above 10 GPU minutes by default. Failed and out-of-date blocks sit behind one warning icon.
- **Magic's Assistant, with the Magic director skill.** On a board the Assistant plans the film (style, characters
  with views first, props, settings, beats with lengths) and waits for your OK before any GPU time, then makes the
  frames, shots from the frames you like, and the sound. It gives each speaking character a designed voice
  (design_voice, save_voice). The skill is built in and always on. It shows each step live as it works, then its
  answer, then the choices.
- **Music and sound in Create.** Two new kinds: Music (ACE-Step 1.5 and 1.5 XL, MiniMax Music 3 by default) with
  lyrics and sections, and Sound effects (Stable Audio 3 Small-SFX, MMAudio from text or for a video). The kind
  tabs show their orbs; every text box keeps its height, scrolls inside and has Expand.
- **Audio fades, crossfades and ducking.** Fade handles on the waveform of Timeline audio clips and Magic voice,
  music and sound blocks; two touching clips crossfade (a true blend, or a dip with a warning); Duck music dips the
  music under voice (on in Magic). The fades carry to the player, Send and Export; the Assistant and helm mcp set
  them too.
- **The Assistant judges sound and video.** It sees a video as one picture of 12 frames with their times, and reads
  exact facts from ffmpeg (real length, loudness, true peak, silence at each end, an abrupt end, scene cuts),
  measured the first time it asks and then kept. A new **listen** tool asks Qwen3-Omni 30B (on llama.cpp) a
  question about a video or audio take, with the speech written down by Qwen3-ASR; when Qwen3-Omni cannot run (its
  drive unplugged, not downloaded, no room) Qwen2.5-Omni-3B answers and the reply says so. Listen says plainly why
  when it cannot run.
- **Drag the playhead by its line** in Magic and on the Timeline.
- **One-panel rail.** The six tools and the Assistant at the top, Hub, System and Settings at the foot (one Setup
  gear on a short window). The status tile is gone: the logo gets a red dot while a runner or the studio is down,
  and a click opens each part's state. The Assistant's item shows a dot when it has a card or an unread answer.
- **Error states.** A studio-down banner, no runner for a model, a runner lost mid-run with Retry, an unplugged
  drive; the Mac app shows a stopped stack, a port in use, a missing models drive, low disk. While the app
  restarts, the browser shows a "helmstudio is starting" page that retries.
- **Search in long lists.** One search field (/ to search) on sessions, boards, sequences, chats, pipelines, the
  pickers, users and keys; the long lists search on the server.
- **Model folders, several per host.** A Mac folder and an external drive, in priority order; the Models page shows
  each model's disk, each disk's free space, which copy is used, and "not connected" for an unplugged drive.
- **Turbo by default** for image and video (Qwen Image Turbo, MiniMax H3 on the Turbo LoRA at 8 steps).
- **One slider design** across all 19 sliders, and **overlay scrollbars** that show only while an area scrolls.
- **helm mcp additions:** the 12 Magic board tools, design_voice and save_voice, listen, and take facts and frame
  grids in get_take.
- **The code-review refactor:** one message envelope per service, shared helpers, long functions split; about
  2,700 lines fewer.
- Fixes: take cards' text no longer runs into the card edge; the Gallery picker keeps one height while you search;
  the playhead no longer draws over the lane and track names when scrolled sideways; Export with ducking under many
  voice clips; listen hears only the part asked for (up to 120 s).

### Known limits

- Windows is untested (moved to 0.1.0-alpha.7).
- NVIDIA: the runners run from a source checkout on the machine; MiniMax H3 video needs SGLang you install by hand;
  ZONOS3 on NVIDIA is not ready yet; the Turbo LoRA does not run on MiniMax H3 Base fp8 (all moved to
  0.1.0-alpha.7).
- The listen tool runs on the Mac only, and needs llama.cpp (`brew install llama.cpp`) and its models downloaded on
  the Models screen (Qwen3-Omni or Qwen2.5-Omni-3B, and Qwen3-ASR).
- The 3B listen fallback's answers are right but short.
- A take's facts are measured the first time they are asked for: the first ask can wait up to 20 s, or answer
  "being measured; ask again".
- Create's "Use in ›" menu on a take has no "Last / First frame in…" yet.
- The Mac app needs Apple Silicon and is ad-hoc signed (no Developer ID): open it the first time with right-click ›
  Open. Its new error states are not yet seen in a real app run.
- No uninstall tool for the app; delete its folders by hand (docs/guides/mac-app.md).
- A separate NVIDIA machine joining a Mac's stack is set up by hand (deploy/gpu-host/README.md).
- Your Claude subscription runs on the Mac only, and needs Claude Code installed and signed in by you.
- Web search uses unofficial search pages: it can be rate-limited or break.
- An Assistant steer is kept only in memory: if pipelines restarts mid-answer, it becomes the next message.
- Models are downloaded separately (on the Models screen) and keep their own licences; no weights ship with
  helmstudio.

## 0.1.0-alpha.5 (2026-11-26)

A move-anywhere Assistant, Create settings in popups, Timeline clones and our own right-click menus.

### What's new

- **Move the Assistant.** Drag its pill or header to float the drawer anywhere; Dock (or a double-click) puts it
  back at the right edge. Remembered per user in this browser. Close stays on screen while a chip shows, and a
  page's side form minimizes the docked drawer.
- **Create settings in popups.** Each chip under the prompt (model · precision, Run on, size, length · fps, audio)
  opens a small popup for just that setting; the sliders button opens all the settings on the right, open by
  default. Create offers only the variants a live host has.
- **Timeline.** A **Clone** button in the Sequences dialog copies a sequence with all its clips, markers, cast and
  look, using the same takes. An untitled sequence is named from its shots' prompts, like Create's sessions; a name
  you typed is kept. The sliders button in the tracks tool bar opens and closes the side panel. New shot has a ×,
  and **+ New sequence** in the Sequences dialog works again.
- **Our own right-click menus, app-wide.** Timeline, Gallery, Create, the Graph view and Pipelines, Assistant chat,
  Models, the LoRA, voice and element libraries, Hub and System lists, with shortcuts shown. Text fields get Cut,
  Copy, Paste, Select all and Insert @… (spelling in the Mac app). Shift + right-click keeps the browser's menu.
- **Host memory, Automatic or Custom.** System › Hosts picks the memory reserve: Automatic uses the Mac's GPU limit
  (`iogpu.wired_limit_mb`) when it is set, Custom keeps your own number.
- **The Mac app window never scrolls;** only the panels inside it do.

### Known limits

- Windows is untested.
- NVIDIA: ZONOS3 on NVIDIA is not ready yet, and MiniMax H3 video needs SGLang you install by hand; both move to
  0.1.0-alpha.6. The Turbo LoRA does not run on MiniMax H3 Base fp8.
- Create's "Use in ›" menu on a take has no "Last / First frame in…" yet.
- Shift+F10 in a text field opens the menu at the field's corner, not at the cursor.
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
  start makes the secrets and the admin; no install script. With an NVIDIA card the runners run on the machine itself on your own
  driver (no CUDA image; `make runners-nvidia`, docs/guides/nvidia-linux.md). The images are
  built on GitHub for Intel/AMD and Apple Silicon (linux/amd64 and linux/arm64).
- **The Mac app as before.** The `.dmg` is now built on GitHub too; from 0.1.0-alpha.2 it updates itself.

### Known limits

- Windows is untested.
- GPU generation on Windows and Linux needs an NVIDIA card with its driver, uv and ffmpeg on the machine (the runners
  run there, from the source checkout); MiniMax H3 video needs SGLang installed by you; ZONOS3 on NVIDIA is not ready
  yet. The Turbo LoRA does not run on MiniMax H3 Base fp8.
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
