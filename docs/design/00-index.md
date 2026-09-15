# Frozen design — index

These eight documents are the contract for helmstudio. They were frozen on 15 September 2026 after a design sprint, and converted here from the published design artifacts so that they live beside the code they describe and change by pull request like anything else.

| File | Settles |
|---|---|
| `01-prd.md` | Requirements, the manifest contract in prose, milestones |
| `02-data-model.md` | 17 tables with full DDL, four state machines, the media layout |
| `03-design-system.md` | Yellow token set for both themes, screens, components, copy |
| `04-packages.md` | `helm-css` / `helm-runtime-sdk` / `helm-ui-sdk` and the dependency rule |
| `05-sdk-and-custom-studios.md` | Isolated development and test, the manifest library, the 15 criteria |
| `06-storage.md` | SQLite schema, the records API, the Go recipe, the directories helper |
| `07-platform-services.md` | State, assets, gallery, palette, the capability model |
| `08-h3-dry-run.md` | h3 studio's real code audited against the design |

## How to use these

**They are the contract, not documentation.** An implementation that disagrees with them is wrong by definition until the document is changed — and changing one is a deliberate act with a decision-log entry, not a side effect of making a build pass.

**If a document is inconsistent, ambiguous, or wrong: stop and raise it.** Do not resolve it in code. Several parts of the system are built against the same sections, and a contradiction resolved silently in one place becomes divergence in four.

**`docs/decisions.md` is the running record.** It carries what was decided, when, and why — including the things these documents deliberately left open. Read it before starting and append to it when you finish.

## Known gaps in the conversion

Inline diagrams did not survive the conversion to markdown and are marked in place. Where a diagram mattered, its content is also stated in prose; where only the picture existed, the published artifact is the reference.
