# RangeBar

Board only, not built (CreateMusicSound frames 1 and 3a): the song's section markers over the stage waveform, and the Repaint range picker on it.

- Markers `cues` (28px) hold one `cue` per section: 3px top rule, 4px corners, 85% `s2`, `mono` 11px `t1`. Rules: verse `a1`, chorus `c-chorus` `a2`, intro and outro `c-edge` `kind-sound`, bridge `c-bridge` `kind-music`. A click seeks there; in Repaint it takes that section's range. Only ACE-Step takes with tagged lyrics have markers.
- Waveform `wb`: bars in `a2` on `s1`, 12px corners.
- Range `rng`: a 2px `a2` frame on a 16% `a2` tint, 8px corners, with two 8 × 28 `a2` handles to drag.
- Under it a bar (`hs-abar`): the range in `mono` `t1` ("0:48 – 1:05 · 17 s"), the lyrics for that part (`hs-inl`), Cancel (`chip`) and a small key (`key video hs-key sm`) "Repaint" with its estimate ("≈ 10 s").

The consumer provides the sections (from the take's lyric timestamps), the range and the handlers. Only the range is made again; the new take goes beside the old one in the strip.
