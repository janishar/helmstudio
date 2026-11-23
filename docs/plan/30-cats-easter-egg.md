# helmstudio-next plan — The cats: double-click the logo

[← Plan index](../README.md)

Status: **decided by the owner (2026-11-24); board drawn, cats redrawn organic (2026-11-24), board approved by the owner (2026-11-24, "Approve"); built (2026-11-24, as-built notes in 34.6).** Web only: no contract, API or
service change. Not in a milestone, like plans 25, 27, 28 and 29.

The owner's ask, 2026-11-24: "on clicking the helm can you think of a cool white female and black male cat coming on
the screen do some things and leave". The doubts were asked one by one (below).

Board: [CatsEasterEgg](../mock/CatsEasterEgg.dc.html) on the canvas page Brand ("Brand · the cats: double-click the
logo", x 1520, y 0, 1440 × 3206). It has a live preview (double-click the logo, or Scene 1–4, and a Reduced motion
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
4. **Organic, not boxy** (owner on the first board, 2026-11-24: "Change it", "it should look organic its boxy right
   now"). The cats were redrawn (34.1).

### 34.1 The cats

Original characters, drawn for this (no existing character copied). Flat and illustrative but soft and alive, in the
logo's organic round (logo-plan round 2: brush-like weight, heavier where a stroke presses, lighter where it lifts),
kept subtle. Every shape is a smooth bezier path, no rects:

- **Body**: a bean, a curved back line, a soft belly, a rounder chest and haunch. Sitting is an upright teardrop with
  the folded haunch and a hind paw; napping is a low loaf.
- **Legs**: tapered (heavier at the top), slightly bent, soft oval paws; the hind leg shows the haunch curve. Each leg
  has two parts, thigh or upper arm and shin or forearm.
- **Tail**: three tapering parts that make an S-curve.
- **Head**: round with cheek fluff, ears with curved sides and inner pink, a slight muzzle, fine curved whiskers,
  eyes that blink.
- **Shading**: no lines inside the figure; a few soft brush crescents in a fur tone (`--crease`) mark the haunch,
  shoulder and brows.

- **She**: white fur (`#faf7f1`, far legs `#e2dbce`, shading `#e8e0d2`), blue eyes with a round pupil and a small
  lash, pink inner ears, nose and cheeks; a collar in `--a2` with a heart tag.
- **He**: near-black fur (`#1d1c22`, far legs `#101014`, shading `#2e2c35`), amber eyes with slit pupils, a little
  larger (×1.07), rounder ear tips; a collar strap in dark `--a1` and a bell in `--a1`.
- **Every theme**: fur is fixed, the edge is a token. A 1 px outline drawn as four `drop-shadow`s on the figure:
  on dark themes he gets a soft rim (`--t2` at 50%) and she needs none; on parchment she gets `--dash` and he needs
  none. A soft ground shadow in `--shd`. Accents come from the theme, so the collar and bell change with it.
- **One rig per cat**: a side view 120 × 96 (facing right; facing left is `scaleX(-1)`): the tail (three parts), four
  legs (two parts each), the body (three shapes: standing, sitting, napping), the wrapped tail, the head (eyes open
  and shut). A pose is a class: stand, walk, run, sit, stretch (a play bow), curl (a loaf with the tail round the
  front, breathing), boop (head forward, eyes shut), bat (a paw swat), crouch (tail tip flicks), paw (the
  reduced-motion still). A front view (sitting) is on the sheet only.
- **How it moves: layered parts that turn at joints, not path morphs.** Each part turns at its own joint (hip, knee,
  shoulder, elbow, neck, tail base and two tail joints) with CSS transforms, so it stays transform-only and small.
  Picked over path morphs because morphs need matching point counts across every pose, cost more bytes and do not
  run on the compositor. Where the silhouette itself must change (stand, sit, nap) the body shapes crossfade: the new
  shape fades in at once (0.16 s) and the old one fades out after it (0.18 s later) while both turn toward the new
  pose over 0.35 s, so it reads as one shape settling, with no see-through moment.
- **Walk**: a 0.84 s cycle in diagonal pairs (near hind with far fore, then the other pair); each leg swings back on
  the ground and lifts forward with the knee or wrist bent; the body rises and dips twice a cycle, the head bobs a
  beat later, the tail sways in an S (each tail part a beat behind the one before). Run is the same at 0.5 s with
  the tail back. Eyes blink every 3.8 s (his out of step with hers). The sheet's four walk poses and the
  storyboards are the live walk held at a phase.

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
- **No network, no files**: inline SVG, CSS and script only. Budget **24 KB raw, 8 KB gzip** (was 16 KB and 6 KB), in
  its own chunk loaded with `import()` on the first double-click, so the shell's bundle does not grow. Raised for the
  organic redraw: bezier bodies, two-part legs, the three-part tail and the walk gait measure about 20.5 KB raw and
  6.2 KB gzip on the board (one rig with his extra bits, the cats' CSS and the scene player, not minified), against
  about 14 KB and 4.3 KB for the first, boxy draw.
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
| 1 | The two cat rigs and poses as one component (SVG + CSS), from the board | 2 |
| 2 | The scene player, four scenes, random without repeat, one at a time and the 10 s rule | 1 |
| 3 | The trigger on the rail logo, the overlay layer, reduced motion, clean-up, lazy chunk | 0.5 |
| 4 | Tests, gates and a look in every theme on the dev stack | 1 |
| | Total | 4.5 |

### 34.6 As built (2026-11-24)

- **Files**: `web_service/components/cats.ts` (the chunk: the board's rig, poses, walk, four scenes, crossfades, blink
  and still pair, CSS scoped under `.cats` with `c-` keyframes, injected once as a `<style>`; the player);
  `web_service/components/shell.tsx` (the logo's `onClick` is a no-op on Create, `onDoubleClick` loads the chunk with
  `import()` and plays; leaving Create stops it); `web_service/tests/components/cats.test.tsx`.
- **Size**: the built chunk is 18.7 KB raw, 5.9 KB gzip (budget 24 KB and 8 KB). Measured on a side build
  (`NEXT_DIST_DIR`); no size check was added to the build (a one-off measure instead).
- **Layer**: a `popover="manual"` element shown with `showPopover()`, so it sits in the top layer above modal dialogs
  and popovers (z-index alone cannot); `left: 90px` (the rail), 130 px tall, `pointer-events: none`,
  `aria-hidden`, removed at the end. Phone needs nothing: the logo is hidden there.
- **One at a time** is the 10 s rule itself (every scene is shorter), counted with `Date.now()` in module memory.
- **Reduced motion**: the still pair fades with a CSS keyframe (opacity only), so no `animate` call.
- **Tests** (Vitest, 6): double-click plays a scene, layer aria-hidden, pointer-events none, gone at the end; a single
  click on Create is prevented (and not on another page); ignored while playing, the 10 s rule, no repeat; reduced
  motion; leaving Create; `visibilitychange`. Not done: the Playwright run on `helmstudio-test` (34.4); the scenes
  were checked headless in forest and parchment from a file:// harness instead.
