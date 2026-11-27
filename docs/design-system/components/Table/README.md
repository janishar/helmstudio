# Table

The batch tables (Pipelines, `grid-table`): full width, 10px cells, 13px text, 1px `s3` row rules, no rule under the last row.

- Heads: `table-head` (11px/600, 0.04em) in `t2`.
- Mono cells (`td.m`): 12px mono `t2` (seeds, ids, sizes).
- Wide tables scroll sideways inside their panel (the app sets a 760px minimum).
- A table that can grow (Users, API keys, a batch's rows, All batches) gets a SearchField above it at the right once it has more than 8 rows; the foot says what shows ("2 of 64 users", "3 active · 21 revoked hidden"). API keys hide revoked keys behind a Show revoked Toggle (off; shown once a key is revoked).

The consumer provides columns and rows; the rows shown are example data.
