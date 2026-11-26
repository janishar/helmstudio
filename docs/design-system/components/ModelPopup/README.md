# ModelPopup

Board only, not built (CreateMusicSound 2a, 2b, 4a; owner 2026-11-27: MiniMax Music 3 is Music's default): the model chip's popup, what a pick greys, the Auto chip and the time estimate on Generate.

- The model chip is a composer chip (`chip sq bevel`) showing the model's name. Its popup is a `pop`, 320px, headed "Model" (`heading`); each model is a `mi` `menuitemradio` with its name and one `small` line on what it is for, its speed and memory; the picked one is `s4` and 600. Music lists **MiniMax Music 3** first ("best, the default · 30 s in ≈ 2:30 · 26.6 GB"), then ACE-Step 1.5 · 2B and XL for fast drafts and edits. Sound lists Stable Audio 3 · SFX, then MMAudio · large. The last row is "All settings".
- A pick changes only what that model can do: the chips and tiles it cannot use stay in place, greyed (`aria-disabled`, 0.45), with "Not on <model>" in their `title` ("Not on MiniMax Music 3"). The strip's Repaint, Cover and Extend grey the same way.
- Auto (`chip sq bevel` with a 14px cpu glyph, stroke 1.75): where it runs; "Run on: Auto picks a free machine" on hover.
- Generate (`key video hs-key`: audio work is `a2`) shows the time estimate under the word in `mono` before you press it: "≈ 2:30 per 30 s · 26.6 GB".

The consumer provides the models of the kind (first is the default), the picked one, what each cannot do and the estimate.
