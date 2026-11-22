# helmstudio logo plan

Status: two final candidates (Playhead h, A as drawn and B softer) for the owner to choose · 2026-11-22 · board: [docs/mock/Logo.dc.html](../mock/Logo.dc.html) (canvas page "Brand")

## Brief

- **Product.** A local-first AI media studio: image, video, audio and voices. Create, Pipelines (with judge and
  director agents), Gallery, Timeline. It runs on your own Mac and NVIDIA machines.
- **Idea.** The user steers the models like a helmsman or a film director. The name says both halves:
  **helm** (steering) and **studio** (making pictures and sound).
- **Theme.** Dark, warm and organic: forest ink `--g #0e1512` with surfaces `--s1..--s5`, text `--t1 #f0f2e8`,
  accents lime `--a1 #b9e37a` and orange `--a2 #ff8552`. Other themes (ocean, aubergine, espresso, oxblood, indigo,
  parchment) swap the accents, so the mark is drawn in `--a1` / `--a2` / `--on1` and follows the theme.
- **Type.** Wordmark "helmstudio", lowercase, Bricolage Grotesque 700. UI text is Inter.
- **Shapes already in the app.** Dock icons are 155° gradient tiles with an inset light; cards and the Sign in logo
  tile use the organic radius (three round corners, one tight bottom-left). Landing and Sign in already show a
  helm-wheel glyph in a lime-to-orange tile.
- **Must.** Read at 16 px; work in one colour; work on dark and light; 3:1 contrast or better for the mark
  (lime and orange on `--g` are both above 7:1; on parchment the mark uses ink `#1f1a14` and `#c8553d`).

## Final candidates (current)

Decisions, 2026-11-22:

- Owner picked round 1's **Playhead h**: "option 2 keep for comparison", then chose it over the round-2 concepts.
- Asked "softer touch or keep as drawn", the owner answered **"both option"**: two finals side by side, owner to choose.

Both are on the top of the board (frames A and B, then a side-by-side strip at 64, 32 and 16 px). Files:
`docs/brand/logo/a-as-drawn/` and `docs/brand/logo/b-softer/`, each with `mark.svg`, `mark-mono.svg`,
`lockup-horizontal.svg`, `lockup-stacked.svg`, `app-icon.svg` and `favicon.svg`. The wordmark is outlined to paths
from Bricolage Grotesque (no font needed to show the files); the generator is kept out of the repo.

**A · Playhead h, as drawn.** Round 1's geometry kept and polished: one 3.2 stroke (24 grid) for stem and arch with
round ends; the playhead flag centred on the stem and lifted clear of the shoulder; the mark balanced in its square;
arch in lime, stem and playhead in orange (round 1 had a cream arch). Wordmark: Bricolage Grotesque 700, tracking
−0.02em, optically kerned on top of the font's kerning (lm opened, st and tu closed, di opened).

**B · Playhead h, softer.** Same idea and silhouette in round 2's organic line: filled shapes, no even strokes. The
stem bows and tapers (heavy foot, lighter top), the flag has bowed sides and soft corners, the arch is thin where it
leaves the stem and full on the leg, ending in a soft foot. Wordmark: round 2's soft treatment, Bricolage Grotesque
500 with a 0.045em same-colour round-join stroke, tracking −0.01em, kerned like A.

**Checks.** Rendered at 64, 32 and 16 px on forest ink and on a light tab. Both read as an h with a playhead at
16 px through their own 16 px cut (whole-pixel stems in A, the same taper simplified in B). Neither resembles a known
brand: no wheel, play triangle, sound bars or AI spark; the closest shapes (a pin, a bookmark, a torch) need the arch
missing to be seen. B's flag is kept close to A's pentagon so it never reads as a goblet or a flame.

### Usage rules (both candidates)

- **Colour.** On dark: arch `--a1` (lime `#b9e37a`), stem and playhead `--a2` (orange `#ff8552`), word `--t1`. On
  light: arch and word ink `#1f1a14`, stem and playhead `#c8553d`. In the dock tile and favicon: the mark in `--on1`
  on the 155° `--a1 → --a2` tile with the organic radius. One colour: all white or all black (`mark-mono.svg` uses
  `currentColor`). Inside the app use the tokens so the mark follows the theme; the fixed hex set is for anything
  outside the app (README, social, installer). The colour files are drawn for dark grounds.
- **Lockups.** Horizontal: mark box 1.18× the font size, gap 0.4× the font size, baseline on the foot of the stem.
  Stacked: mark centred above the word, gap 0.3× the font size. Always lowercase, one word.
- **Clear space.** One flag height (a quarter of the mark) on every side; never on a busy image without the tile.
- **Minimum size.** Full mark from 24 px; the 16 px cut (`favicon.svg`, drawn on a 16 grid) at 16–23 px; never
  below 16 px. Horizontal lockup from 96 px wide.
- **Motion.** Loading: the playhead slides down the stem and back (1.6 s, ease-in-out), drawn over the arch; the
  arch stays still. For known progress the playhead stops at the share done. Reduced motion: the still mark.
- **Don't.** Stretch, rotate, outline, add shadows to the flat mark, recolour outside the theme tokens, or mix A's
  mark with B's wordmark (or the reverse).

**Open for the owner:** A or B. After the choice: export the favicon .ico/.png set and the app icon sizes from the
chosen folder, delete the other folder, and swap the helm-wheel glyph on Landing, Sign in and the browser tab.

## Round 2 · organic (for reference)

Owner's feedback on round 1, 2026-11-22: **"more organic logo"**.

Round 1 (below, kept for reference and as a small strip at the bottom of the board) was geometric: rings, straight
spokes, a playhead, a spark, a lens. Round 2 keeps the brief but draws from nature: soft, uneven, living curves;
filled shapes with brush-like weight (heavier where a stroke presses, lighter where it lifts); clean vector with
smooth bezier paths on the 24 grid, no stock clip-art. Each frame shows the mark on dark and light, the lockup, the
dock icon at 64 and 32 px in the app's tile, the 16 px favicon on a dark and a light tab, and white and black
one-colour versions. Marks still use `--a1` / `--a2` / `--on1` so they follow the theme.

**Soft wordmark.** "helmstudio", lowercase, Bricolage Grotesque 500 with a same-colour stroke of 0.045em and round
joins (reads like 600 with rounded corners and terminals), tracking −0.01em. The final artwork outlines it to paths;
an optional touch for later is a small leaf as the dot of the i.

1. **Leaf play.** An asymmetric leaf whose heart is cut out as a soft play shape (bowed sides), on a tapered stem that curls like a tiller.
   - Why: what you make grows from a seed of an idea, and you hold the stem; play says media.
   - Risks: leaf marks are common in eco and wellness brands; the diagonal leaf fills less of the square, so it is the
     smallest at 16 px (the small cut widens the play cut); play says video more than voice.
2. **Sprout h.** A brush-stroke h, heavier at the foot and lighter at the top like a stem, that sprouts a leaf flag.
   - Why: the h owns the name (helm), the leaf is both growth and the vane you steer by; bold at 16 px; in one colour
     the leaf joins the stem and reads as a sprout.
   - Risks: a letter mark needs the wordmark nearby at first; the leaf must stay a clear flag, not a tick or accent.
3. **Bud pebble.** A river-pebble tile (soft, uneven, one tighter corner like the dock tiles) with two uneven
   petals cupping a seed of light, like a pod about to open.
   - Why: the moment a take is born; the strongest at 16 px; sits in the dock as one of the family.
   - Risks: the pod can read as an eye or a map pin; the petal gaps close at 16 px (it needs its own small cut);
     the pebble is part of the mark; says little about steering.
4. **Current eye.** A wave, thin at the tail and full at the crest, that curls into a helm turn around an eye.
   - Why: you ride and steer the current of the models; the eye is the lens and the director's look; the curl is a
     natural spinner.
   - Risks: spirals and wave curls are common; it must never read as a 6, a 9 or an @.

| Concept | Legible at 16 px | Organic | Says helm and studio | Unique | Motion and loading | Score |
|---|---|---|---|---|---|---|
| 2 Sprout h | 3 | 3 | 2 | 3 | 3: leaf unfurls as progress | 14 |
| Round 1 · 2 Playhead h | 2 | 1 | 2 | 3 | 3: playhead slides as a progress bar | 11 |
| 4 Current eye | 3 | 3 | 3 | 2 | 3: the curl turns as a spinner | 14 |
| 3 Bud pebble | 2 | 3 | 2 | 2 | 3: bud opens as a take lands | 12 |
| 1 Leaf play | 2 | 3 | 2 | 2 | 2: leaf sways, play pulses | 11 |

**Kept for comparison (owner, 2026-11-22: "option 2 keep for comparison").** Round 1's concept 2, **Playhead h**
(an h whose stem is the Timeline playhead, flag on top), is shown as a full frame right after the four round-2
concepts, exactly as drawn in commit 7dcac56, labelled "Round 1 · 2 Playhead h (kept for comparison)", and scored
above on the round-2 criteria next to Sprout h: it is a real monogram with a natural progress motion, but it is the
least organic (even strokes, round caps), its flag gets small at 16 px, and it speaks for video more than the rest.

**Recommendation: concept 2, Sprout h**, with the soft wordmark. It is the most organic and still the clearest: a
living h for helm with a leaf flag for growth and steering; it holds at 16 px, works in one colour, and the swaying
leaf gives a gentle loading state (still under reduced motion). Runner-up: **4 Current eye**, which now ties on score; the h wins the tie because it owns the name. Pick 4 if the
owner prefers a symbol to a letter. Nothing in the four borrows a well-known shape (no robot heads, sound-wave bars, shopping bags).

### Design review (2026-11-22)

Rendered at full size and at 64, 32 and 16 px on forest ink. Asked of each mark: does it truly feel organic, or is it
geometric with rounded corners; are curves and weights even; does it hold small; does it clash or look like a known
brand. All four stay distinct (leaf, letter, pod in a pebble, curl).

- **1 Leaf play.** The play cut was a straight-sided triangle with rounded corners and the stem a monoline stroke:
  the two most geometric parts of the set. Fixed: the cut now has bowed sides, and the stem is one tapered filled
  shape joined to the leaf, heavier at the leaf and fine at the tip. Still the weakest at 16 px.
- **2 Sprout h.** The leaf floated above the stem with a gap, so it read as an accent (an h with an acute, like
  ĥ or ń) rather than a sprout. Fixed: the leaf now grows out of the stem tip, in colour and in one colour, and the
  loading sway pivots on that joint.
- **3 Bud pebble.** The flame in a cup read as a candle, a lotus or a spa. A first redraw (two open petals with a
  round seed above) read as a person with raised arms, a common community and wellness mark, so it was dropped too.
  Fixed: two uneven petals now curve in around an egg-shaped seed, a pod about to open. Left: the pod can read as an
  eye or a map pin, and the gaps close at 16 px, so the final art needs a small cut (wider gaps, smaller seed).
- **4 Current eye.** The curl was traced as about 60 short segments, with small bumps and a hairline tail that
  vanished at 16 px. Fixed: redrawn as one clean curl in nine smooth segments, heaviest at the crest, tail ending in a
  soft round end that holds at 16 px; the eye moved to the optical centre of the curl.
- **Colour.** Lime and orange stay apart in every mark except the h, where the orange leaf meets the lime stem at
  one small joint; the one-colour versions carry all four. No clashes found on dark, light or the gradient tile.

**Open for the owner (round 2):** pick 1–4 (recommended 2) or keep round 1's Playhead h; soft or plain wordmark; leaf in orange or both parts in
lime. The usage rules below carry over to whichever mark is chosen (for an organic mark the small cut is the 16 px
version on the board, not the wheel's simplified hub, and loading uses that mark's motion, not the wheel's).

## Round 1 · geometric (for reference)

### The four concepts

Every mark is drawn on a 24 grid as inline SVG. The board shows each one large on dark and light, the lockup with
the wordmark, the dock icon at 64 and 32 px, a 16 px favicon on a dark and a light tab, and white and black
one-colour versions.

1. **Aperture helm.** A ship's wheel (ring and six handles) whose hub is a six-blade camera iris.
   - Why: says both halves of the name, steering and camera; grows out of the wheel the app already uses.
   - Risks: wheels and apertures are common; handles crowd at 16 px, so the small version drops the blade lines and
     opens the hub; drawn too thin it reads as "settings".
2. **Playhead h.** A monogram h whose stem is the Timeline playhead, with the flag on top.
   - Why: a true letter mark and the most "studio" of the four.
   - Risks: speaks for video and Timeline more than image, voices or agents; the flag alone looks like a pin.
3. **Spark tile.** The Create spark (plus a small twinkle) inside the organic dock tile.
   - Why: sits in the dock as one of the family; the boldest at small sizes.
   - Risks: four-point sparks are the generic "AI" sign, so it is the least ours; the tile is part of the mark.
4. **Lens o.** Wordmark first: the o of "studio" becomes a lens ring with a record dot; the lone o is the icon.
   - Why: the name itself is the logo; very clear at 16 px.
   - Risks: a ring and dot is a record button in many apps; weak without the wordmark; the o needs hand tuning to
     sit on the baseline.

### Comparison

| Concept | Legible at 16 px | Fits the theme | Unique | Motion and loading | Score |
|---|---|---|---|---|---|
| 1 Aperture helm | 3 | 3 | 2 | 3: iris opens, wheel turns a sixth per step | 11 |
| 2 Playhead h | 2 | 2 | 3 | 3: playhead slides as a progress bar | 10 |
| 3 Spark tile | 3 | 3 | 1 | 2: pulse and twinkle | 9 |
| 4 Lens o | 3 | 2 | 1 | 2: breathing dot, ring meter | 8 |

### Recommendation (round 1)

**Concept 1, Aperture helm**, with the plain wordmark. It is the only one that carries both "helm" and "studio",
keeps continuity with the wheel on Landing and Sign in, holds at 16 px with the simplified hub, and has a natural
loading state (the wheel turns in sixths; the iris opens as a take finishes). Keep the lens o (concept 4) as a
type-only fallback.

## Usage rules · round 1 (superseded by the final candidates' rules above)

- **Colour.** On dark: ring and handles `--a1`, hub `--a2`. On light: ink `#1f1a14` and `#c8553d`. In the dock
  tile and favicon: the mark in `--on1` on the `--a1 → --a2` gradient. One colour: all white or all black, never
  a tint.
- **Theme.** Use the tokens, not hex, inside the app so the mark follows the theme; the fixed lime and orange set
  is the brand default for anything outside the app (README, social, the installer).
- **Wordmark.** "helmstudio", always lowercase and one word, Bricolage Grotesque 700, tracking −0.02em (the app
  currently uses +0.01em; tighten it when the logo lands). Gap between mark and word is 0.4× the font size.
- **Sizes.** Full mark from 24 px up; the small cut (no blade lines, wider hub) at 16–23 px; never below 16 px.
- **Clear space.** At least half the mark's width on every side; never on a busy image without the tile.
- **Don't.** Stretch, rotate (except the loading animation), outline, add shadows to the flat mark, or recolour
  with non-theme colours.
- **Motion.** Loading turns the wheel 60° per step; respect reduced motion (show it still).

## Open choice for the owner

Replaced by the final candidates above: the owner picks A or B.

After the choice: final SVGs (mark, small cut, lockup, dock icon, favicon .ico/.png set) and the swap on Landing,
Sign in and the browser tab. No code changes until then.
