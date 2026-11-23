# helmstudio-next plan — The cats: double-click the logo

[← Plan index](../README.md)

Status: **decided by the owner (2026-11-24); board drawn, for the owner to approve.** Web only: no contract, API or
service change. Not in a milestone, like plans 25, 27, 28 and 29.

The owner's ask, 2026-11-24: "on clicking the helm can you think of a cool white female and black male cat coming on
the screen do some things and leave". The doubts were asked one by one (below).

Board: [CatsEasterEgg](../mock/CatsEasterEgg.dc.html) on the canvas page Brand ("Brand · the cats: double-click the
logo", x 1520, y 0, 1440 × 3166). It has a live preview (double-click the logo, or Scene 1–4, and a Reduced motion
toggle), a storyboard per scene, and the two cats' design sheet in forest and parchment.

Facts come from a read on 2026-11-24 of [docs/brand/logo-plan.md](../brand/logo-plan.md) (the rail logo item) and the
Main board's `.rail-logo` (the logo panel above the dock, `web_service/components/shell.tsx`, `AppShell`).

## 34. The cats

### Decided by the owner, 2026-11-24

1. **Trigger: double-click the rail logo.** A single click still just goes home to Create.
2. **A different scene each time**, picked at random from a few short scenes (about 6–8 s): walk in and nose-boop
   with a small heart; chase a ball of yarn across the bottom; sit side by side and stretch; one naps and the other
   curls up next to it. Then both stroll off the other side.
3. **Style: soft flat, app style.** Simple rounded shapes like the logo, smooth SVG animation, an accent colour on
   small details (her collar, his bell), works in every theme.

### 34.1 The cats

Original characters, drawn for this (no existing character copied). Rounded rects and ellipses, the logo's shape
language, no outlines inside the figure.

- **She**: white fur (`#faf7f1`, far legs `#e2dbce`), blue eyes with a small lash, pink inner ears, nose and cheeks; a
  collar in `--a2` with a heart tag.
- **He**: near-black fur (`#1d1c22`, far legs `#101014`), amber eyes with slit pupils, a little larger (×1.07), rounder
  ear tips; a collar strap in dark `--a1` and a bell in `--a1`.
- **Every theme**: fur is fixed, the edge is a token. A 1 px outline drawn as four `drop-shadow`s on the figure:
  on dark themes he gets a soft rim (`--t2` at 50%) and she needs none; on parchment she gets `--dash` and he needs
  none. A soft ground shadow in `--shd`. Accents come from the theme, so the collar and bell change with it.
- **One rig per cat**: a side view 120 × 96 (facing right; facing left is `scaleX(-1)`), parts tail, four legs, body,
  haunch, curled tail, head (eyes open and shut). A pose is a class that moves parts with CSS transforms and a 0.35 s
  transition, so poses blend: walk (a 0.42 s leg swing, body bob, tail sway; four poses on the sheet), run, stand,
  sit, stretch (a play bow), curl (a loaf with the tail round the front, breathing), boop (head forward, eyes shut),
  bat (a paw swat), crouch (tail flick), wave (the reduced-motion still). A front view (sitting) is on the sheet only.

### 34.2 The scenes

x is the cat's place in the overlay strip; W is its width.

| # | Scene | Length | How it goes |
|---|---|---|---|
| 1 | Nose boop | 7.0 s | She walks in from the left, he from the right; they stop face to face at 2.3 s; boop with eyes shut, a small `--a2` heart pops up and floats away (2.6–4.3 s); he turns and both stroll off to the right. |
| 2 | Yarn chase | 7.0 s | A yarn ball (`--a2`) rolls in from the left with him running after it; he catches it at 1.6 s and bats it (it hops on); she trots in, both crouch with tails flicking (3.3–4.8 s); they pounce and chase it off to the right. |
| 3 | Side by side stretch | 7.5 s | Both walk in from the left, she leads; they sit side by side at 2.4 s; a long stretch with eyes shut (3.5–4.9 s); sit, then stroll off to the right. |
| 4 | Nap together | 8.0 s | He walks in from the right and curls up at 2.2 s, a small "z z z" in `--t2` floats up; she walks in from the left and curls up next to him (4.1 s), head to head; both wake at 6 s, stretch, and stroll off to the right. |

### 34.3 Design

- **Trigger**: `dblclick` on the rail logo link only (`.rail-logo a`), nowhere else, no keyboard shortcut. The
  link keeps its `href` and single-click behaviour. A double-click sends two `click`s first: on Create the link's
  click is a no-op (same route, `preventDefault`), so nothing navigates twice; from another page the first click goes
  home and, since the rail stays mounted across routes, the `dblclick` still fires and the scene plays on Create. The
  logo panel gets `user-select: none` so a double-click does not select text.
- **Random scene, no repeat**: pick from the four, never the one that played last (kept in memory, not stored).
- **One at a time**: a double-click while a scene plays is ignored; then at most one scene per 10 s (counted from the
  start of the last one), so double-click spam does nothing.
- **Layer**: a fixed strip 130 px tall at the bottom of the content area (right of the rail), `position: fixed`,
  above everything (above drawers, toasts and dialogs), `pointer-events: none`, `aria-hidden="true"`. It never
  takes focus or input: typing, clicking and scrolling go through it. It is removed from the DOM when the scene ends.
- **Animation**: positions with the Web Animations API (`element.animate`, `transform` and `opacity` only, so it stays
  on the compositor); poses by class at set times. One timeline per scene, data only (steps of time, x, pose,
  facing). Stops and cleans up if the page hides (`visibilitychange`) or the route changes away from Create.
- **Reduced motion** (`prefers-reduced-motion: reduce`): nothing moves. A still pair (he waves a paw, she sits) fades
  in at the bottom right for 2 s and fades out (opacity only). Picked over "nothing" so the double-click still says
  something. The board's Reduced motion toggle shows it.
- **No network, no files**: inline SVG, CSS and script only. Budget 16 KB raw, 6 KB gzip, in its own chunk loaded
  with `import()` on the first double-click, so the shell's bundle does not grow.
- **Themes**: every theme, through tokens (34.1). Checked on the board in forest and parchment.
- **Phone**: not shown (the phone dock has no logo).
- **No settings**, no telemetry, no sound.

### 34.4 Tests

- **web** (vitest): a double-click on the logo mounts the overlay with a scene; a single click on Create does not
  navigate; two double-clicks in a row give two different scenes (random stubbed) and the second is ignored while
  the first plays; the 10 s rule; with reduced motion the still pair shows and no `animate` is called; the overlay
  has `pointer-events: none` and `aria-hidden`; it is gone after the scene's length (fake timers); clean-up on route
  change.
- **Playwright** (`helmstudio-test`): double-click the logo, the overlay shows, a click on the prompt box through the
  overlay still focuses it, the overlay goes; a screenshot of one mid-scene frame in forest and parchment.
- Size: the chunk is under the budget (a check in the web build's size report).

### 34.5 Phases

| Phase | What | Hours |
|---|---|---|
| 1 | The two cat rigs and poses as one component (SVG + CSS), from the board | 1.5 |
| 2 | The scene player, four scenes, random without repeat, one at a time and the 10 s rule | 1 |
| 3 | The trigger on the rail logo, the overlay layer, reduced motion, clean-up, lazy chunk | 0.5 |
| 4 | Tests, gates and a look in every theme on the dev stack | 1 |
| | Total | 4 |
