# KindTabs

Create's output kind tabs (create.tsx `MediaSwitch`): a column of tabs on the left of the composer, each with its kind orb above a one-word label (boards: TextToVideo, CreateAudio, CreateMusicSound and the other Create screens; the orbs are the ones in Magic's Add dialog).

- Track: a `panel` (`s1`, `radius-panel` with a `radius-panel-tail`, `shadow-panel`), 6px padding, tabs 6px apart.
- Track: stretches to the composer's height; a short composer is at least the tabs' height (5 × 63px), so switching kinds never resizes it.
- Tab (`ktab bevel`): 64px wide and **63px tall at the least**, as the app's MediaSwitch (`min-h-[63px] w-16 flex-1`): a taller composer (a dialogue script, many tiles) stretches the tabs evenly, so no blank is left under them. Radius 12, `shadow-bevel`, 12px/600 `t2` label 4px under the orb.
- Selected (owner, 2026-11-27: "make selected one more differentiated from on active ones"): `s5`, a thin 1px `a1` ring inside (`inset 0 0 0 1px var(--a1)`, as Magic's picked chips), its orb and label at full strength (`t1`, 700).
- The other tabs: their orbs dimmed (opacity 0.4, saturate 0.45), back to full on hover or keyboard focus, the label to `t1`; the label stays `t2` otherwise (any dimmer fails AA on Parchment). The focus ring stays the 2px `a1` outline 2px outside, so focus and selection never look alike.
- Orb (`ki`): 28px circle, a radial gradient of the kind colour `--k` (lighter top left, darker edge) with a 3px 16% ring and a 14px 40% glow; a 14px lucide glyph in stroke `#14201a`.
- The five kinds, in this order: Image `#b8abff` image, Video `#ff9a6e` clapperboard, Voice `kind-voice` mic, Music a small black record (no glyph) with a `kind-music` glow, Sound `kind-sound` wave bars. Magic also has Storyboard `#e8d88a` pencil and Dialogue `kind-voice` two bubbles.

Built: Image, Video, Voice, Music and Sound (Voice was Audio; Music and Sound joined in alpha.6). The consumer provides the kinds and which is selected.
