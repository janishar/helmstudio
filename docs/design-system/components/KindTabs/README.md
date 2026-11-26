# KindTabs

Create's output kind tabs: a column of tabs on the left of the composer, each with its kind orb above a one-word label (boards: TextToVideo, CreateAudio, CreateMusicSound and the other Create screens; the orbs are the ones in Magic's Add dialog).

- Track: `s1`, radius 16, 6px padding, tabs 6px apart. Tab: 64px wide, radius 12, 12px/600 `t2` label 4px under the orb; selected `s5` with `t1`.
- Orb (`ki`): 28px circle, a radial gradient of the kind colour `--k` (lighter top left, darker edge) with a 3px 16% ring and a 14px 40% glow; a 14px lucide glyph in stroke `#14201a`.
- Kinds: Image `#b8abff` image, Video `#ff9a6e` clapperboard, Voice (today's Audio tab) `kind-voice` mic, Sound `kind-sound` wave bars, Music a small black record (no glyph) with a `kind-music` glow. Magic also has Storyboard `#e8d88a` pencil and Dialogue `kind-voice` two bubbles.

The consumer provides the kinds (today Image, Video, Audio; Voice, Music and Sound with plan 38) and which is selected.
