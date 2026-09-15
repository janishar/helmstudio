# Reading copies

The design sprint's eleven documents, kept in the form they were produced.
Open `index.html` in a browser; every page is self-contained and works offline.

**These are not the contract.** The contract is the markdown in
`docs/design/`, which changes by pull request and against which an
implementation is judged. `docs/plan/` is the ordering. These pages are a
snapshot of the same material at the moment it was frozen, kept because the
tables, screens and diagrams read better here than they survive in markdown —
the design system's screens and the data model's state machines in particular.

Where a reading copy and the markdown differ, the markdown is right and this is
stale. Do not implement from here.

| File | Markdown source |
|---|---|
| `01-prd.html` | `docs/design/01-prd.md` |
| `02-data-model.html` | `docs/design/02-data-model.md` |
| `03-design-system.html` | `docs/design/03-design-system.md` |
| `04-packages.html` | `docs/design/04-packages.md` |
| `05-sdk-and-custom-studios.html` | `docs/design/05-sdk-and-custom-studios.md` |
| `06-storage.html` | `docs/design/06-storage.md` |
| `07-platform-services.html` | `docs/design/07-platform-services.md` |
| `08-h3-dry-run.html` | `docs/design/08-h3-dry-run.md` |
| `09-build-plan.html` | `docs/plan/01-build-plan.md` |
| `10-milestones.html` | `docs/plan/02-milestones.md` |
| `11-delegation.html` | `docs/plan/03-delegation.md` |

Each page loads IBM Plex from Google Fonts and falls back to the system stack
offline. Nothing else is fetched.
