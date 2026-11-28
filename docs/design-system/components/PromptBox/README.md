# PromptBox

Create's composer (create.tsx, the form "Composer"): the prompt, its references, the quick chips and the Generate key.

- A `panel` on `s2` with `shadow-composer`, `space-composer-y` / `space-composer-x` padding, rows `space-composer-gap` apart, basis `size-composer`.
- Free height (owner, 2026-11-27: "text only in two line it waster bottom empty area"): the composer is at least the KindTabs' height, and the kind's main text box takes what its rows leave (`grow`, its own height the least, never its text's): the prompt (Image, Video, Sound, a voice-over's words), Voice's dialogue script, Music's lyrics (twice the style's share; the style keeps its two rows). The chip row stays at the bottom. A tiles row with nothing in it (Music before an `@` mention) takes no place and no gap (`empty:hidden`).
- Prompt: a borderless textarea, two rows at the least, `prompt` (15/22) in `t1`, placeholder `t2` ("Describe what you want to see"; for speech "Type the words to speak"). ⌘ or Ctrl + Enter generates. It never grows with its text: it scrolls; Expand opens it big. The textarea is `display: block`, so the `@` colour layer under it is exactly as tall and scrolls as far (an inline textarea left a 5px gap and the colours drifted at the bottom).
- Dialogue script (Voice, a dialogue model): 8 lines (176px) at the least in both views, then the composer's free height, the numbered lines with their speaker chips and the editor; more lines, and each line's hint row ("Line 3: write @name: text or @name (style): text"), scroll inside it. Expand opens the script as text with `@` mentions, from either view (owner, 2026-11-27: "same is the case with voice").
- Mentions: `@handle` names an element; known ones are highlighted on `s5` (`mention-chip`), unknown ones get a 22% `bad` tint and a wavy `bad` underline (`mention-unknown`).
- Chip row, `space-chip-row` apart: + (add image, video or audio), @ (insert element), the quick chips (each a ChipPopup), the seeds stepper (n / 4), All settings, then the key at the end, bottom right even when the row wraps.

The consumer provides the workflow's fields; the shown prompt, model and handles are example content. The two prompt lines show the field and how mentions are drawn behind it.
