helmstudio is a local-first AI media studio: you steer image, video, music, sound and voice models from Create, chain them in Pipelines, keep what they make in the Gallery and cut it on the Timeline. The look is dark, warm and organic: tonal surfaces that float on a deep ground, one tight corner on every round shape, two accents that mean image work and video or audio work. Build every screen to match the approved boards exactly; the web app's code is the reference for values.

## Content fundamentals

The app speaks in short, plain words. It is a tool, not a host: no greetings, no exclamation marks, no emoji, no superlatives.

- **Shortest label that says it.** Buttons and chips use the fewest words: "Generate", "Retry", "Undo", "Done", "Rename", "All settings". A chip shows the value alone ("16:9", "5 s", "Seed 42", "24 kHz"), never "Aspect: 16:9".
- **No repeated qualifiers.** Say a thing once where it is decided; don't repeat "(only you)", "(local)" or the section's name on every row.
- **Sentence case** everywhere: "Add to timeline", "Save as look", "From Pipelines". Product and model names keep their own case ("ACE-Step 1.5", "Qwen3-TTS").
- **A disabled action says why**, in its own label or its hover reason: "Write a prompt", "Pick a voice", "Not downloaded", "2 problems", "Fix line 2 first", "Reading what it fits".
- **Work in progress uses the ellipsis character**: "Sending…", "Saving…", "Loading…", "Optimising 62%". A menu row that needs more input ends with "…": "Insert @…", "First frame in…".
- **The middle dot joins parts** of a short line: "seed 42 · 0:06", "Saved as a new take · Fox at dusk", "12.4 → 1.9 MB · WebP", "Batch · <title>".
- **Empty and filtered lists** get one plain sentence: "Nothing here yet. What you make in Create lands here; you can also upload images or drop them here." / "No takes match."
- **Errors**: every API error is `{"detail": string}`. Show the `detail` as is in an ErrorNote (`bad` on `s2`), with Retry when it can be retried. Write `detail` as a plain sentence: what went wrong, then what to do if there is something to do. Another user's thing is "not found", never "forbidden".
- **Keys** are written as the platform shows them: "⌘C" on a Mac, "Ctrl+C" elsewhere; "⌘ or Ctrl + Enter" where both are meant.
- Address the person as "you" only when needed ("1 card waits for you"); the app never says "I" or "we".

## Visual foundations

**Themes.** Seven themes share one set of names; `forest` (Forest ink) is the default and the first theme here. Everything here (the cover, every preview, chips, cards) shows on `forest`, the app's greens, as the app and the boards do: the ground `g`, the surfaces `s1` to `s5`, the text `t1` / `t2` and the green accent `a1` with `on1`. The other themes are token values only. Six are dark (`forest`, `ocean`, `aubergine`, `espresso`, `oxblood`, `indigo`); `parchment` is the one light theme. A theme swaps the ground, the five surfaces, the text and the two accents; layouts never change with the theme. Name colours only by token so every theme works.

**Ground and surfaces.** Paint the page `g`. Stack things on surfaces in order: `s1` (panels, thumbnails), `s2` (popovers, menus, the composer), `s3` (chips, actions, toast), `s4` (fields, tags, the focused menu row), `s5` (pressed and expanded states, menu borders). `p0` is the side panel, `in` the inset stage behind media. Surfaces are told apart by tone and shadow, not by borders; the only borders are a menu's 1px `s5`, a choice's 1px `ln`, a take tile's 1px `s5` and the dashed `dash` of an empty card.

**Text.** Primary text `t1`, secondary `t2`, both on `g` and `s1` to `s4` (`t2` on `s4` is 4.31:1 in parchment, kept as the source has it). Keep small `t2` text off `s5`: it is under 4.5:1 there in five themes. Errors and danger are `bad`.

**Accents.** `a1` means image work and the primary action: the Generate key, the focus ring, a pressed choice, the selected take, a star, the Toggle's on track. `a2` means video and audio work: a video or audio workflow's key, the selected sound tile, running status, upload bars. Text on them is `on1` / `on2`, never literal white. Use one accent per control; never place the two keys side by side.

**Focus.** Every focusable thing shows a solid 2px `a1` ring 2px outside it (`:focus-visible`). On parchment the ring is 3.74:1 on `g`; in the dark themes 8:1 and more.

**Type.** Three families, all from Google Fonts: Inter for the UI (`sans`), Bricolage Grotesque 600 and 700 for display (`display`: page and dialog titles, the Generate key, the wordmark), JetBrains Mono for seeds, lengths, sizes, shortcuts and ids (`mono`, tabular figures). Body text is `body` 14/20 on a 16px root, so rem units land on the boards' pixels. The prompt is `prompt` 15/22. Labels and headings are 12px `t2` (`label`, `heading` at 600 and 0.04em). Chips are `chip` 13/500. Badges and status pills are 11px. The smallest text is 10px mono (`mono-sub`, `dock-label`).

**Shape: the organic radius.** Large round shapes have three round corners and one tight bottom-left: panels `radius-panel` / `radius-panel-tail` (28 / 12), the side panel 34 / 14, popovers and dialogs 18 / 8, the key 20 / 8, the right-click menu 16 / 6, the logo tile 14 / 6. Small controls round all four corners: fields, square chips and thumbnails `radius-field` (10), choices and tiles `radius-small` (8), menu rows 9, and pills `radius-pill` for chips, tags, statuses and the Toggle.

**Depth.** Things float. Panels carry `shadow-panel` and a faint top sheen; popovers `shadow-pop`; menus `shadow-menu`. Raised controls (chips, cards, tiles, actions) carry `shadow-bevel`: a 1px light top edge, a 2px dark bottom edge, a small drop. The Generate key is the most raised: `shadow-key` and a gloss. Dock tiles are 3D gradient tiles with `shadow-tile`. Every shadow is built from `shd` (black in the dark themes, warm tan in parchment).

**Backdrop.** Behind the page sits a fixed layer: a faint warm light from the top left (1100 by 620px, rgba(255,196,120,0.11)), a 7% `a1` glow at the bottom right, and a fine 118deg line texture at 1.4% white. Keep it; it is what makes the ground feel lit rather than flat.

**Layout.** A desktop window has the rail on the left (`size-rail`, 14px from the edge, items 56px tall and 10px apart), then the page, then an optional side panel. From 960px wide (the Mac app's narrowest window) the side panel sits beside the page and each scrolls on its own; the window itself never scrolls. Narrower, the side panel wraps under the page. On a phone the rail becomes a sideways bar with no logo, and every tap target in a sheet is 44px. Keep gaps on the 14px rhythm (`space-rail`) between panels, cards and dock items.

**Media.** Pictures are cover-fit on `s1` in 16:10 cards and 112-wide tiles; video shows its poster with a length badge and plays in native controls; audio shows the `wave` mark (one speech-like shape for every sound, not the real audio) and plays in native controls. Music takes show as album covers (a mood gradient with a vinyl disc), sound and voice takes as one glowing orb (sky `kind-sound`, amber `kind-voice`).

**Motion.** Little and quick: popovers and menus appear without animation; the "named by …" hint beside Create's title, a sequence's name and (from alpha.6) a Magic board's name fades after a few seconds; spinners spin. Honour reduced motion: every animation and transition drops to 0.01ms.

**Scrollbars.** Every scrolling area uses one bar, shown only while that area scrolls (then gone 0.8 s after, as the Mac's overlay bars): 0.25rem wide, a square `a1` (accent) thumb on no track (`::-webkit-scrollbar` rules, set once for all elements; Firefox gets `scrollbar-width: thin` in the accent), never the browser's grey bar.

**Selection.** The app is an app, not a document: only fields and code select text. Menus are our own right-click menu, never the browser's (Shift + right-click still gives the browser's).

**Right-click menus.** Every new thing you act on (a card, a row, a block, a clip, a node) ships with its right-click menu in the shared ContextMenu, and its board shows that menu.

## Iconography

- Icons are **lucide** (`lucide-react` 1.73.0), outline, 24 grid, stroke 2 with round caps and joins, drawn in `currentColor` so they take the text colour. Sizes: 14px in chips and small buttons, 16px in menus, dialogs and the settings chip (stroke 1.75 there), 18px on the rail's 32px tiles (stroke 1.75, white on the gradient). The Icons assets hold every lucide icon the code imports (125 files; aliases such as `Loader2`, `MoreHorizontal`, `Music`, `History` are listed in the Icons README).
- A few glyphs are text, not icons: the info button's "i", a menu's "›" and "✓", and the ⋯ of Take actions (three filled 1.8 dots).
- No emoji anywhere. No icon without a label or an `aria-label`.
- The brand mark is not an icon: use the Logo component or the Logos assets, never a lucide glyph in its place.

## Assets

- **Logos**: Logo A "Playhead h" as drawn, six SVGs: `mark.svg` (two-colour, for dark grounds), `mark-mono.svg` (one colour), `lockup-horizontal.svg` and `lockup-stacked.svg` (mark with the outlined Bricolage wordmark, for dark grounds), `app-icon.svg` (the gradient tile) and `favicon.svg` (the 16px cut). In the app draw the mark from tokens (Logo component) so it follows the theme.
- **Icons**: the lucide icons above, as SVG files.
- Fonts are hosted Google faces (Inter, Bricolage Grotesque 600/700, JetBrains Mono); there are no font files to copy.

## Components

The components here are static renditions of the app's own classes in `components/bundle.css`; there is no JavaScript bundle. Load `tokens.css`, then `bundle.css`, and use the class names each component's guideline gives.

- **Magic** (built, alpha.6, plan 39): FixList (the board's bottom-left warning icon with a count, amber or coral, and its upward list of failed, out-of-date and overrunning blocks with their fixes and Run all; no warning bars), MagicBlockDialog (the block dialog's kind badge, Model select and hint, Loop, Run on, Tied · Unlink, Dialogue lines, Compare takes, Morph prompt, Make views toggles, Add a view and its settings chip, Variant, Stop, Level in dB, frame style Film, the take video's own full screen, section markers), the header's Export key with Send to Timeline, Undo and Redo as icon buttons (Button), Run board's estimate suffix (Chip), TransitionMarker and RangeBar's markers, the Add dialog's From Gallery kind tile (its own orb, `#f59a8c`, with the images glyph, like the kind orbs) a board's "named by" hint, and the New board dialog (size and fps, locked after).
- **Navigation**: Rail (the logo, then one panel: Create, Magic, Gallery, Timeline, Pipelines, Models, the Assistant, and Setup at its foot, folding to a wrench tile whose menu opens beside the rail), PhoneDock, KindTabs and ContextMenu (with its `beside` option).
- **Timeline**: TimelineLanes (ruler, lane heads, playhead `tl-ph`), TimelineClip (`tl-clip` and its looks: `a1` image, `a2` video, sky audio, dashed empty or offline), TimelineTools (`tl-bar`, the five tools, Snapping, Zoom, the shortcuts sheet) and TimelineOverlays (`tl-tag`, snap, blade, trim and slip). Overlays never take the pointer.
- **Assistant**: AssistantDrawer (`ast drw`; `ast sheet` on a phone), AssistantCard (`cf`, Pick one `opts`), AssistantChats, AssistantMinBar (`ast-min`) and MentionsPicker (the @ lists). Anything that uses the GPU waits for a card.
- **Pipelines**: Chain (`chain`, `pl`), GraphNode (`gn`, ports and wire colours) and RunStrip (the Graph's buttons, legend and live preview).
- **Settings pages**: SettingsSection (`apage`, `sec`), PeopleRow (`who`, `trow`, the Users table), ProviderPick (`pk`) and SetupSteps (`stp2`, `stp`, `sw`).
- **Media and screens**: MediaViewer, DropZone, SignIn and PhoneDock.
- **Error states** (built, alpha.6; docs/mock/ErrorStates.dc.html): StatusBanner (`ban`: "Studio isn’t answering. Retrying…" with Retry now, then "Back online"; a page's ErrorNote for the same cause is held back while it shows; when the app itself is restarting, Kong's own "helmstudio is starting" page, ErrorStates a4), StatusTile (now the rail logo's `logo-dot` and its `hpop` popover, opened from the logo: Runner down, No runner, Studio down; nothing when all is well; the separate status tile is gone, owner 2026-11-28), RunFailedCard (`fpanel`: "This run stopped" in plain words, Show details with the raw error and Copy, Retry; the failed tile's Retry, Show details, Copy error rows) and RunWaiting (`step wait`, `rtile wait`, the `live` chip "Live updates paused · retrying", and the `why` line under a greyed Generate: "No runner for mlx is on…", "On T9 · not connected · Models"). A greyed key says why in a visible line, never on hover only.
- **Search** (built, alpha.6; docs/mock/Search.dc.html): SearchField (`sf`: the Field look with the `search` glyph, the count `sfh`, a spinner, the × `sfx` and the `/` hint `k`; marked words `mark`, the ringed row `sf-on`, "No sessions match." `sf-none`, the server's "From older sessions" `sf-older`) on every list past 8 rows: Create's sessions, Magic's boards (now a popover), Sequences, Assistant's chats and memory, Pipelines, All batches (with state FilterChips), a batch's rows, the Elements, Looks, Voices and LoRA pickers, the Gallery and its picker, Users, API keys (Show revoked), Models, snapshots, sends, workflows and Provider models.
- **Music and Sound** (built, alpha.6; CreateMusicSound, plan 38): KindTabs (Image, Video, Voice, Music, Sound; tabs 63px at the least; the picked one ringed in `a1`), ModelPopup (MiniMax Music 3 first, the owner's default; greyed "Not on <model>" chips; the Auto chip; the time estimate on Generate), TempoKey (the BPM · Key · Time group chip and its Auto switch), LyricsBox (section tag chips, Instrumental), RangeBar (section markers and the Repaint range), Variations (Sound's grid), the album cover and the sound and voice orbs (TakeCard, TakeTile), the stage tabs Sound / Video with sound (Segmented), the paired strip tile (TakeTile) and the new menu rows (ContextMenu).
- The code writes a few colours as literals, not tokens: amber `#f2c14e` (waiting, warnings), coral `#ff8a80` (Assistant errors and danger), sky `#86cdea` (audio clips, number wires), the Assistant's green mark `#d8f58c` → `#3c9a2e`. Use them only where these components use them. They are fixed for every theme: on `parchment` amber, coral and sky text on `s1` and `s2` are 1.6 to 2.3:1, under the 4.5:1 floor; kept as the code has them, to be moved into tokens with a light-theme value.
