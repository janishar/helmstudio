# FixList

What to fix on a board (Magic, MagicBoard frames 11 and 5b): one warning icon at the board's bottom left, in a fixed slot under the lane heads (left of "+ Lane"), so its showing or going moves nothing. It replaces the full-width warning bars, which pushed the board down.

- The icon (`fixb`): 34×30, `radius-field`, `s4` (`s5` while open), a 16px lucide `triangle-alert` and a 1px inset ring. Amber `#e8c468` while all is out of date or doesn't fit; coral `#ff8a80` (`fixb bad`) once any block failed. Hidden when there is nothing to fix.
- Its count (`fixc`): a 16px pill at the top right, the ring colour with `#111` text, the number of rows.
- It is a button with `aria-haspopup="dialog"`, `aria-expanded`, an `aria-label` "3 blocks to fix: 1 failed · 2 out of date" and that reason as its tooltip.
- The list is a `pop` opening upward (`top span-right`, 6px), 420px wide at most, labelled "Blocks to fix" with the reason beside it; rows scroll past about eight (60vh, 400px at most). Escape or a press outside closes it.
- A row (`fixr`, focusable, `s3` on hover or focus): the block's thumbnail (`fixt`, 40×26, the take's art, else its kind badge), its name over why ("failed: out of memory" in coral, "out of date" or "doesn't fit: runs 3 s past Shot 1" in amber), then its keys as small chips (28px): Retry and Open (failed); Run and Pin newest / Unpin / Pin take (out of date); Speed up, Trim, Unlink, Keep (doesn't fit). The name selects the block and scrolls to it; a right-click gives the block's own menu (ContextMenu, `blockMenu`).
- The foot: "Run all (n)" (`chip on`) runs every failed or out-of-date block.
- The block itself keeps its status badge ("Failed", "Out of date", "Doesn't fit").
