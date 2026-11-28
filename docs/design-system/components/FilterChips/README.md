# FilterChips

The Gallery's filters: one chip per kind and per source, pressed is the light chip.

- `chip bevel` with `aria-pressed`. Pressed: `t1` fill, `p0` text, weight 600. Not pressed: `s3`, `t1` text.
- Kinds: Images, Video, Audio. Sources: From Create, From Pipelines, From Timeline, Uploads. A batch filter shows as a clearable "Batch · <title>" chip.
- Voice, Music and Sound replace Audio (audio takes carry a sound class).
- All batches' state chips (Pipelines, Search.dc.html frame 5) are FilterChips beside the SearchField, one pressed at a time: All, Running, Waiting for you, Paused, Done, Failed (Failed keeps the stopped ones too), 30px tall.

The consumer provides the label and the pressed state; a filter that cannot apply now is disabled with a `title` saying why.
