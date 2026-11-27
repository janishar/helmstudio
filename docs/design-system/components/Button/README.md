# Button

Every press in helmstudio is one of six kinds: the key, a chip, an action, a ghost, an icon button or a text link.

- **Key** (`key`): the one primary action on a screen, Generate. Fill `a1`, label `on1` in `key-label` (Bricolage Grotesque 700, 0.04em), gloss on top, `shadow-key`, corners `radius-key` with a `radius-key-tail` bottom-left, `size-key` tall with 24px side padding. A video or audio workflow's key is the second accent: fill `a2`, label `on2`. Disabled at 0.55 opacity, and its label says why in two or three words ("Write a prompt", "Pick a voice", "Not downloaded", "2 problems"); a second mono line can say what to fix ("Fix line 2 first"). Busy: "Sending…". With runs going it reads "Add to queue".
- **Chip** (`chip bevel`): a secondary action ("Retry", "Undo"), 13px/500, `s3`, pill. See Chip.
- **Action** (`act`): a take action in a side panel, 40px, `s3`, `radius-field`, `shadow-bevel`, left aligned.
- **Ghost**: a dialog footer's quiet press, 36px, transparent, 13px/500 `t2`, `radius-field`: Cancel beside the key, and a footer's way out at its left (Magic's Add step 1: From Gallery). Greyed at 0.5 with why as its hover reason ("The Gallery is in studio").
- **Icon button**: the composer's square chips (`chip sq icon`: 36px, `s4`, `radius-field`), the modal's close (`hs-x`, 28px, transparent, `s3` on hover) and back (`hs-back`, 32px, `s3`, `t2` glyph). Always give an `aria-label`; the glyph is a 14 or 16px lucide icon.
- **Text link** (`hs-link`): "All settings" under a chip's popup, 12px `t2`, underlined 3px below.

The consumer provides the label (shortest words that say it, no repeated context), the disabled reason, and for icon buttons the accessible name. One key per screen; never two accents side by side.
