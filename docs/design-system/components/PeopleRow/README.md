# PeopleRow

People and provider rows on the settings pages: a person `who`, a provider or tool `trow` (`apage`; assistant/page.tsx), and a row of System's Users table (admin/users.tsx).

- Row `who` / `trow`: `s2`, 12px corners, 10/12px padding, 12px gap; `who` centres, `trow` aligns to the top. The name 13px 600; under it `who` has the email in `mono` 11px `t2`, `trow` a 12/16 `t2` line.
- Mark `lg`: 34px, 10 10 10 4 corners, the initial in display 15px 700 white with a soft text shadow, on the consumer's gradient.
- Roles: `tag` pills; admin is `tag-accent`.
- Users table: a `panel` with 6/8px padding holding a `grid-table` (10px cells, 13px, 1px `s3` rules, heads 11px 600 0.04em `t2`, ids and emails `mono` 12px `t2`). Its actions are 30px chips (Roles, Password, Enable or Disable); the root admin's row has none and says "Locked · set in the server's .env". A disabled account's status is `t2`.
- System's tabs (Users, Hosts, API keys) are chips; the current one is `t1` with `p0` text and 600.

Every row has its right-click menu (ContextMenu) with the same actions. Names and addresses here are examples.
