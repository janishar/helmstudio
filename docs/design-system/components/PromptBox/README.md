# PromptBox

Create's composer (create.tsx, the form "Composer"): the prompt, its references, the quick chips and the Generate key.

- A `panel` on `s2` with `shadow-composer`, `space-composer-y` / `space-composer-x` padding, rows `space-composer-gap` apart, basis `size-composer`.
- Prompt: a borderless textarea, two rows, `prompt` (15/22) in `t1`, placeholder `t2` ("Describe what you want to see"; for speech "Type the words to speak"). ⌘ or Ctrl + Enter generates.
- Mentions: `@handle` names an element; known ones are highlighted on `s5` (`mention-chip`), unknown ones get a 22% `bad` tint and a wavy `bad` underline (`mention-unknown`).
- Chip row, `space-chip-row` apart: + (add image, video or audio), @ (insert element), the quick chips (each a ChipPopup), the seeds stepper (n / 4), All settings, then the key at the end, bottom right even when the row wraps.

The consumer provides the workflow's fields; the shown prompt, model and handles are example content. The two prompt lines show the field and how mentions are drawn behind it.
