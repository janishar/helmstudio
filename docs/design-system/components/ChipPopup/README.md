# ChipPopup

A composer chip that opens its one setting in a popup under it (Create, alpha.5 item 10): the chip shows the value, the popup changes it.

- The chip is `chip sq bevel` with `aria-haspopup="dialog"`; `s5` while open.
- The popup is a `pop` (`s2`, `radius-pop` with a `radius-pop-tail` bottom-left, 14px padding, `shadow-pop`), anchored `bottom span-right` 6px under the chip; on a narrow window it flips or centres and scrolls. Rows inside sit `space-popup-gap` apart.
- Its last row can be the "All settings" text link (`hs-link`), which closes the popup and opens the settings drawer.
- Escape or a press outside closes it; Escape puts focus back on the chip.

The consumer provides the chip's value text, the popup's `aria-label` (the setting's name) and its controls (choices, a slider, a field).
