# helmstudio logo plan

Status: concepts for the owner to choose · 2026-11-22 · board: [docs/mock/Logo.dc.html](../mock/Logo.dc.html) (canvas page "Brand")

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

## The four concepts

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

## Comparison

| Concept | Legible at 16 px | Fits the theme | Unique | Motion and loading | Score |
|---|---|---|---|---|---|
| 1 Aperture helm | 3 | 3 | 2 | 3: iris opens, wheel turns a sixth per step | 11 |
| 2 Playhead h | 2 | 2 | 3 | 3: playhead slides as a progress bar | 10 |
| 3 Spark tile | 3 | 3 | 1 | 2: pulse and twinkle | 9 |
| 4 Lens o | 3 | 2 | 1 | 2: breathing dot, ring meter | 8 |

## Recommendation

**Concept 1, Aperture helm**, with the plain wordmark. It is the only one that carries both "helm" and "studio",
keeps continuity with the wheel on Landing and Sign in, holds at 16 px with the simplified hub, and has a natural
loading state (the wheel turns in sixths; the iris opens as a take finishes). Keep the lens o (concept 4) as a
type-only fallback.

## Usage rules (for whichever concept is chosen)

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

1. Which concept: 1, 2, 3 or 4 (recommended: 1).
2. Wordmark: plain, or with the lens o from concept 4.
3. Accent: the lime and orange split, or the mark in one accent only.

After the choice: final SVGs (mark, small cut, lockup, dock icon, favicon .ico/.png set) and the swap on Landing,
Sign in and the browser tab. No code changes until then.
