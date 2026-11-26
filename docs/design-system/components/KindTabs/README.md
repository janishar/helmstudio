# KindTabs

Create's output kind tabs (create.tsx `MediaSwitch`): a column of tabs on the left of the composer, each with its kind orb above a one-word label (boards: TextToVideo, CreateAudio, CreateMusicSound and the other Create screens; the orbs are the ones in Magic's Add dialog).

- Track: a `panel` (`s1`, `radius-panel` with a `radius-panel-tail`, `shadow-panel`), 6px padding, tabs 6px apart.
- Tab (`ktab bevel`): 64px wide and **63px tall**, as the app's MediaSwitch (`h-[63px] w-16`), radius 12, `shadow-bevel`, 12px/600 `t2` label 4px under the orb; selected `s5` with `t1`.
- Orb (`ki`): 28px circle, a radial gradient of the kind colour `--k` (lighter top left, darker edge) with a 3px 16% ring and a 14px 40% glow; a 14px lucide glyph in stroke `#14201a`.
- The five kinds, in this order: Image `#b8abff` image, Video `#ff9a6e` clapperboard, Voice `kind-voice` mic, Music a small black record (no glyph) with a `kind-music` glow, Sound `kind-sound` wave bars. Magic also has Storyboard `#e8d88a` pencil and Dialogue `kind-voice` two bubbles.

Built today: Image, Video and Audio. Board only, not built (plan 38): Audio becomes Voice, and Music and Sound join after it. The consumer provides the kinds and which is selected.
