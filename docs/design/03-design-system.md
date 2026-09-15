# Design System

> Frozen design, 15 Sep 2026. This file is the contract.
> Do not edit to make an implementation pass — raise a finding instead.

helmstudio is the room you launch *from*. It has to read as the same family as ltx, h3, iris and AuK — near-black chrome, hairline borders, monospace for anything machine-shaped — while never being mistaken for any one of them. Every screen below is live HTML in this page: switch the theme in the header and the mockups switch with it.

## 1 · Principles

**Show the engineering, don't hide it.** Every install step, exit code, byte count and port number is visible on the surface that caused it. No spinner stands in for a log we already have.**

**The machine's real constraint is the interface's main job.** Unified memory means one heavy studio at a time. That trade is stated before it is enforced, never discovered after a crash.**

**Hairlines separate; shadows don't.** Structure comes from 1px borders and ground steps. A shadow appears in two places only — dialogs and the app window.**

**One accent, used once per screen.** Exactly one primary action is accent-filled in any view. Status colour is a separate vocabulary and never borrows the accent's hue.**

**Monospace means machine.** Paths, commands, seeds, sizes, durations, ports and logs are mono with tabular figures.**

**Every state has words.** A coloured dot is always paired with a label. Colour confirms; it never carries the message alone.**

## 2 · Colour

The accent is **signal yellow**. It is the highest-energy hue available and it earns its place here because this is a tool where one action per screen matters — Install, Launch, Render, Export — and everything else is information. Yellow also has a property no other accent has: **a yellow fill needs dark text in every theme**, so the primary button is the same colour in light and dark, which keeps the brand steady where a blue or teal would have to shift.

Two collisions came with it and both were resolved rather than tolerated. Amber was the semantic warning colour, so **warning moved to orange** — far enough round the wheel to read as a different vocabulary. And h3 studio's identity hue is amber, adjacent to the accent, so the rule is enforced by role: **identity hues appear only as a 3px stripe or a dot, never as a fill**, and the accent appears only as the single primary fill. A brighter lemon against h3's softer amber keeps them apart at a glance.

#### Dark theme — the primary design

ground-page

\#0d0c0a

ground-panel

\#161512

ground-raised

\#1f1d18

ground-inset

\#090804

border-hairline

\#292620

border-strong

\#3b372e

text-primary

\#f1ede4

text-secondary

\#a8a294

text-muted

\#716b5e

accent-base

\#ffc700

accent-hover

\#ffd633

accent-subtle

\#2b2206

#### Light theme — designed, not inverted

ground-page

\#faf8f3

ground-panel

\#ffffff

ground-raised

\#fbf8f1

ground-inset

\#1c1a15

border-hairline

\#e4dfd2

border-strong

\#c9c2b0

text-primary

\#1a1813

text-secondary

\#534f45

text-muted

\#837d70

accent-base

\#f5b800

accent-text

\#7a5600

accent-subtle

\#fff3d1

The yellow rule

Yellow is a **fill** colour, never a text colour on a light ground — `#f5b800` as type on white is about 1.9:1 and unreadable. So the token set splits them: `accent-base` is the fill in both themes and always carries `#1a1400` text, while `accent-text` is a deep amber `#7a5600` in light and a soft gold `#ffd24a` in dark. Every link, active label and accent icon uses `accent-text`; every primary button uses `accent-base`. Mixing them is the one mistake this palette makes easy.

The terminal stays dark in both themes. Log output is a machine surface, and a light terminal in a light window reads as a document rather than a stream.

#### Semantic status — a separate axis

| Status  | Dark      | Light     | Used for                                           | Always with words                                  |
|---------|-----------|-----------|----------------------------------------------------|----------------------------------------------------|
| idle    | `#716b5e` | `#837d70` | Not installed, stopped                             | Not installed              |
| running | `#4caf7d` | `#1f7a4d` | Healthy, step done                                 | Running · 12:04         |
| info    | `#4a9fe0` | `#1c6fb8` | Downloading, building                              | Downloading 43%       |
| warning | `#f0883e` | `#a85410` | Update available, unverified, marginal requirement | Update available      |
| error   | `#e05c52` | `#c0342a` | Failed, requirement not met                        | Install failed · build |

#### Studio identity hues

Stripe, dot and timeline clip only — never a button fill in launcher chrome. A studio whose manifest declares no `hue` is assigned one from a generated ramp, so a fifth studio is never colourless.

| Studio                                  | Dark      | Light     | Kinds         |
|-----------------------------------------|-----------|-----------|---------------|
| ltx studio  | `#5b8def` | `#2f5fc4` | video + audio |
| h3 studio   | `#e0a33c` | `#a06a10` | video + audio |
| iris studio | `#d8558f` | `#b12f68` | image         |
| AuK studio  | `#8b7cf0` | `#5c4bc4` | audio         |

## 3 · Type

**IBM Plex Sans and IBM Plex Mono.** Not Inter, the default of every AI product since 2022; not Space Grotesk, whose wide caps fight a dense rail. The decisive argument is the *matched* mono: a metadata line and the label above it share a skeleton, so the switch to mono reads as "this is machine output", not "this is a different app".**

page title 20/600<span style="font:600 20px/28px var(--sans);letter-spacing:-.01em">Studios

section 11/600<span style="font:600 11px/16px var(--sans);letter-spacing:.09em;text-transform:uppercase;color:var(--tx3)">Requirements

card title 14/600<span style="font:600 14px/20px var(--sans)">h3 studio

body 13/400<span style="font:400 13px/20px var(--sans);color:var(--tx2)">MiniMax-H3 video and audio through a native Metal engine.

mono 12/400<span style="font:400 12px/18px var(--mono);font-variant-numeric:tabular-nums;color:var(--tx3)">800×448 · 124f · 12 steps · seed 42 · 6m 11s

log 12/400<span style="font:400 12px/17px var(--mono);color:var(--logtx)">h3 profile: video VAE decoder wall= 85.487s peak= 9.365GiB

micro 11/500<span style="font:500 11px/15px var(--sans);color:var(--tx3)">4 studios · 2 installed · 38.4 GB on disk

Tabular numerals everywhere numbers stack or tick — a timer that jitters as it counts is a bug. Uppercase labels take 0.09em tracking, never exceed 11px or three words, and sit in muted text. Sentence case everywhere else, buttons included.

## 4 · Space and form

Base unit 4px; scale 4, 8, 12, 16, 20, 24, 32, 40, 56. Rails take 16px of internal padding, cards 14px, dense control rails 12px. Radii are 4px for buttons, chips, inputs and bars; 6px for cards, panels, terminals and dialogs; 0 for table rows, log lines and the identity stripe. Borders are always exactly 1px — the only 2px line is the focus ring, an outline at 2px offset.

The catalogue grid is `repeat(auto-fill, minmax(320px, 1fr))` with a 16px gap. The studio detail page is `280px | 1fr | 320px` above 1180px, dropping the right column below the centre at 1180px and stacking entirely below 900px — requirements first, terminal last but never under 240px tall. Minimum supported width is 380px.

## 5 · helm-css, and how studios wear it

The system ships as a package so studios can look like family without importing a component library. Five files: `helm-tokens.css`, `helm-base.css`, `helm-layout.css`, `helm-components.css`, and `helm.css` concatenating them. Every class is prefixed `helm-`, nothing is `!important`, and specificity stays at a single class so a studio overrides by writing one rule rather than fighting.

| Concern     | Rule                                                                                                                                                                                                                                                |
|-------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Identity    | `--helm-studio-accent` is set from the manifest's `hue`, so h3's Render button stays amber and AuK's stays violet while every ground, border, radius and typeface matches. Consistent, not uniform.                                                 |
| Theme       | The daemon passes the theme at launch and pushes a change over SSE; the studio sets `data-theme` on its root. Toggling the launcher re-themes every running studio. Standalone falls back to `prefers-color-scheme`.                                |
| Components  | `helm-ui-sdk` renders in Shadow DOM, so class rules do not reach inside — but custom properties inherit straight through. Tokens theme the components; classes style the studio's own markup; neither breaks the other. `::part()` covers the rest. |
| Enforcement | `helm validate` fails on raw hex outside the vendored token file, on non-Plex families, and on any token pair below 4.5:1 in either theme. Advisory locally, blocking in registry CI.                                                               |
| Stability   | Token names are API within a major. A renamed token silently restyles four studios — the worst kind of break, because nothing errors.                                                                                                               |

## 6 · Catalogue

helmstudio 127.0.0.1:8700  ltx studio · 12:04 <span style="color:var(--tx3);font-family:var(--mono)">:8720

Stop

1 running

<a href="#catalogue" class="on">Studios</a>[Gallery](#gallery)[Timeline](#timeline)[Models & disk](#disk)[Doctor](#app)

**Studios**

4 studios · 2 installed · 38.4 GB on disk<span class="sp" style="flex:1">

Add studio

###### ltx studio video+audio

LTX-2.5 video with synchronised audio, MLX on Apple Silicon.

MLX · Metal · 21.4 GB · peak ~20 GB

Running · 12:04

Stop

Open
